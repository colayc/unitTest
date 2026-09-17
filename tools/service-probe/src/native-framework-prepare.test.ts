import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { cp, mkdir, mkdtemp, readFile, readdir, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { basename, join, resolve } from "node:path";
import test from "node:test";
import * as prepareModule from "./native-framework-prepare.js";
import { prepareFrameworkRuntime, type FrameworkRuntimePrepareDependencies } from "./native-framework-prepare.js";
import { stableFrameworkIdDigest } from "./native-framework-matrix.js";
import { hashCompiledFrameworkExecutable } from "./native-framework-workspace.js";
import { publishFrameworkRuntime } from "./native-framework-publish.js";
import { loadRequiredFrameworkRuntime } from "./native-framework-runtime.js";
import type { FrameworkId, FrameworkToolchainFamily } from "./native-framework-report.js";
import type { TaskServiceFixture } from "./probe.js";

const root = resolve(import.meta.dirname, "../../..");
const digest = (value: string) => createHash("sha256").update(value).digest("hex");
const benchmark: FrameworkRuntimePrepareDependencies["benchmark"] = {
  id: "catalog-10000", itemCount: 10000, sampleCount: 3, allocationBudgetPerOperation: 300000,
  allocationsPerOperation: [101, 102, 103], catalogRevision: "1".repeat(64),
  catalogArtifactSha256: "2".repeat(64), stableIdDigest: "3".repeat(64), status: "passed",
};

test("prepare CLI accepts only ordered platform and full lowercase candidate", () => {
  const parse = (prepareModule as any).parseFrameworkPrepareArguments;
  assert.equal(typeof parse, "function");
  assert.deepEqual(parse(["--platform", "win32", "--candidate", "1".repeat(40)]), { platform: "win32", candidateCommit: "1".repeat(40) });
  for (const args of [[], ["--platform", "darwin", "--candidate", "1".repeat(40)], ["--candidate", "1".repeat(40), "--platform", "linux"],
    ["--platform", "linux", "--candidate", "ABC"], ["--platform", "linux", "--candidate", "1".repeat(40), "--benchmark", "private.json"]]) {
    assert.throws(() => parse(args), /arguments/u);
  }
});

async function fixture(t: test.TestContext, mutation = "", platform: "linux" | "win32" = "linux") {
  const repositoryRoot = await mkdtemp(join(tmpdir(), "framework-producer-"));
  t.after(() => rm(repositoryRoot, { recursive: true, force: true }));
  for (const path of ["testdata/frameworks", "testdata/framework-matrix", "tools/framework-bundle", "sdk/cmake"]) {
    await cp(join(root, path), join(repositoryRoot, path), { recursive: true });
  }
  const roots = { cpputest: join(repositoryRoot, "prepared", "cpputest"), unity: join(repositoryRoot, "prepared", "unity"), cmock: join(repositoryRoot, "prepared", "cmock") };
  for (const path of Object.values(roots)) await mkdir(path, { recursive: true });
  await mkdir(join(repositoryRoot, "build"));
  await writeFile(join(repositoryRoot, "build", platform === "win32" ? "unity-runner-generator.exe" : "unity-runner-generator"), "generator");
  if (mutation === "fixture-input") await writeFile(join(repositoryRoot, "testdata/frameworks/cpputest/tests/framework_tests.cpp"), "substituted executable inputs");
  if (mutation === "matrix-input") await writeFile(join(repositoryRoot, "testdata/framework-matrix/opaque.c"), "substituted matrix executable input");
  if (mutation === "alternate-contract") {
    const path = join(repositoryRoot, "testdata/framework-matrix/contract.json");
    const contract = JSON.parse(await readFile(path, "utf8"));
    for (const id of ["cpputest", "unity"]) contract.frameworks[id].primaryCTestName = `${id}.alternate`;
    await writeFile(path, JSON.stringify(contract));
  }
  let disposed = 0;
  let started = 0;
  const catalogs: any[] = [];
  const dependencies: FrameworkRuntimePrepareDependencies = {
    benchmark,
    verifyInputs: async () => roots,
    startService: async (_binary: string, directory: string) => {
      started++;
      const frameworkId = basename(resolve(directory, "..")) as FrameworkId;
      await writeFile(join(directory, "session-secret"), "credential-must-not-survive");
      const family = basename(resolve(directory, "../..")) as FrameworkToolchainFamily;
      for (const profile of mutation === "duplicate" ? ["a", "b"] : ["a"]) {
        const bin = join(directory, "data/build", profile.repeat(64), "bin");
        await mkdir(bin, { recursive: true });
        await writeFile(join(bin, `phase9_${frameworkId}${platform === "win32" ? ".exe" : ""}`), `compiled:${family}:${frameworkId}`);
      }
      const names = frameworkId === "cpputest" ? ["Pass", "AssertionFailure", "Crash", "MockMissingCall", "Skipped", "Timeout"] : ["test_pass", "test_assertion_failure", "test_crash", "test_cmock_expectation_failure", "test_skipped", "test_timeout"];
      const capabilities = { canDiscoverCases: true, canReportMockDetails: true, canReportSkipped: true, canReportSourceLocation: true, canRunCase: true };
      const catalog = {
        projectId: "root", profileId: "profile", revision: digest("catalog"), generatedAt: new Date(0), diagnostics: [], partial: mutation === "partial",
        containers: ["framework", "matrix.malformed", "matrix.opaque"].map((name, i) => ({
          capabilities, ctestLogicalName: `${frameworkId}.${name}`, disabled: false, displayName: name,
          framework: i === 2 ? "opaque-ctest" : frameworkId, id: `container-${i}`, labels: [], projectId: "root",
        })),
        items: [...names, frameworkId === "cpputest" ? "MalformedOutput" : "test_malformed_output"].map((name, i) => ({
          containerId: i === 6 ? "container-1" : "container-0", disabled: i === 4, displayName: name, framework: frameworkId,
          id: `item-${i}`, kind: "case", labels: [], logicalName: name,
        })),
      };
      if (mutation === "framework") catalog.containers[0]!.framework = "wrong";
      if (mutation === "container") catalog.containers[0]!.ctestLogicalName = "wrong";
      if (mutation === "alternate-contract") catalog.containers[0]!.ctestLogicalName = `${frameworkId}.alternate`;
      catalogs.push(catalog);
      const bytes = Buffer.from(JSON.stringify(catalog));
      const client = {
        inspectWorkspace: async () => ({ projects: [{ projectId: "root", buildProfiles: [{ buildProfileId: "profile", toolchainId: "tc" }] }], toolchains: [{ family: mutation === "family" ? "wrong" : family, toolchainId: "tc", version: "18.1.0", ...(mutation === "compiler" ? {} : { compilerSha256: digest(`compiler:${family}`) }) }] }),
        discoverTests: async () => ({ taskId: "discovery" }),
        getTask: async () => ({ status: "finished", outcome: mutation === "task" ? "failed" : "succeeded" }),
        getTestCatalog: async () => catalog,
        listArtifacts: async () => ({ items: [{ taskId: "discovery", artifactId: "artifact", kind: "test-catalog", sizeBytes: bytes.length, sha256: mutation === "artifact" ? "0".repeat(64) : digest(bytes.toString()) }] }),
        readArtifact: async () => bytes,
        runTests: async () => { throw new Error("preparation must not execute matrix scenarios"); },
      };
      return { client, dispose: async () => { disposed++; await rm(directory, { recursive: true, force: true }); if (mutation === "cleanup") throw new Error("secret raw cleanup output"); }, kill: async () => {}, restart: async () => {} } as unknown as TaskServiceFixture;
    },
  };
  return { repositoryRoot, dependencies, catalogs, counts: () => ({ disposed, started }) };
}

test("producer binds Service discovery and actual compiled bytes to closed F1 identity", async (t) => {
  const input = await fixture(t);
  const result = await prepareFrameworkRuntime({ repositoryRoot: input.repositoryRoot, platform: "linux", candidateCommit: "1".repeat(40) }, input.dependencies);
  assert.deepEqual(result.manifest.toolchains.map(({ family }: { family: string }) => family), ["clang", "gcc"]);
  assert.deepEqual(input.counts(), { started: 4, disposed: 4 });
  assert.equal(result.ownedStagingRoots.length, 4);
  assert.ok(result.ownedStagingRoots.every(path => path.includes(join(".staging", result.ownershipId))));
  await assert.rejects(readdir(join(input.repositoryRoot, ".native-e2e/framework-work/linux")), { code: "ENOENT" });
  assert.deepEqual(result.manifest.benchmark, benchmark);
  // @ts-expect-error F1 loader is covered by its Node tests.
  const { loadF1FrameworkIdentity } = await import("../../framework-bundle/consume.mjs");
  const identity = await loadF1FrameworkIdentity(input.repositoryRoot);
  let index = 0;
  for (const toolchain of result.manifest.toolchains) for (const framework of toolchain.frameworks) {
    assert.equal(framework.stableIdDigest, stableFrameworkIdDigest(framework.frameworkId, input.catalogs[index], identity));
    assert.equal(framework.catalogArtifactSha256, digest(JSON.stringify(input.catalogs[index++])));
    assert.equal(framework.evidence.executableArtifactSha256, digest(`compiled:${toolchain.family}:${framework.frameworkId}`));
    assert.equal(framework.evidence.sourceArtifactSha256, identity.fixtures[framework.frameworkId].sourceSha256);
    assert.equal(toolchain.compilerSha256, digest(`compiler:${toolchain.family}`));
    if (framework.frameworkId === "unity") assert.equal(framework.cMockProvenance!.manifestSha256, "4f0a73e5decc2402930fc4d609d1640150fe6addb30e20cc9900e1cf418520a8");
  }
});

test("Windows producer selects exactly clang-cl and msvc and hashes .exe files", async (t) => {
  const input = await fixture(t, "", "win32");
  const result = await prepareFrameworkRuntime({ repositoryRoot: input.repositoryRoot, platform: "win32", candidateCommit: "1".repeat(40) }, input.dependencies);
  assert.deepEqual(result.manifest.toolchains.map(({ family }) => family), ["clang-cl", "msvc"]);
  for (const toolchain of result.manifest.toolchains) {
    assert.deepEqual(toolchain.frameworks.map(({ frameworkId }) => frameworkId), ["cpputest", "unity"]);
    for (const framework of toolchain.frameworks) assert.equal(framework.evidence.executableArtifactSha256, digest(`compiled:${toolchain.family}:${framework.frameworkId}`));
  }
  assert.deepEqual(input.counts(), { started: 4, disposed: 4 });
});

test("producer requires audited benchmark evidence and rejects invalid evidence", async (t) => {
  const input = await fixture(t);
  const options = { repositoryRoot: input.repositoryRoot, platform: "linux", candidateCommit: "1".repeat(40) } as const;
  await assert.rejects(prepareFrameworkRuntime(options), /framework runtime preparation failed/u);
  assert.equal(input.counts().started, 0);
  await assert.rejects(prepareFrameworkRuntime(options, { ...input.dependencies, benchmark: { ...benchmark, allocationsPerOperation: [300001, 2, 3] } }), /framework runtime preparation failed/u);
  assert.equal(input.counts().disposed, input.counts().started);
});

test("prepared executable evidence survives destructive Service disposal without retaining Service secrets", async (t) => {
  const input = await fixture(t);
  const result = await prepareFrameworkRuntime({ repositoryRoot: input.repositoryRoot, platform: "linux", candidateCommit: "1".repeat(40) }, input.dependencies);
  for (const toolchain of result.manifest.toolchains) for (const framework of toolchain.frameworks) {
    const workRoot = join(input.repositoryRoot, ".native-e2e/framework-work/.staging", result.ownershipId);
    assert.equal(await hashCompiledFrameworkExecutable(workRoot, "linux", toolchain.family, framework.frameworkId), framework.evidence.executableArtifactSha256);
    const serviceRoot = join(workRoot, "linux", toolchain.family, framework.frameworkId, "service");
    assert.deepEqual(await readdir(serviceRoot), ["data"]);
    assert.equal(await readFile(join(serviceRoot, "data/build", "a".repeat(64), "bin", `phase9_${framework.frameworkId}`), "utf8"), `compiled:${toolchain.family}:${framework.frameworkId}`);
  }
});

test("prepare and publish can replace a runtime after deletion-equivalent consumer disposal", async (t) => {
  const input = await fixture(t);
  const options = { repositoryRoot: input.repositoryRoot, platform: "linux", candidateCommit: "1".repeat(40) } as const;
  await publishFrameworkRuntime(await prepareFrameworkRuntime(options, input.dependencies));
  const runtime = await loadRequiredFrameworkRuntime(input.repositoryRoot, "linux", join(input.repositoryRoot, "artifacts"), {
    loadFrameworkIdentity: async () => ({}) as any,
    startService: input.dependencies.startService!,
  });
  await runtime.dispose();
  const next = await prepareFrameworkRuntime({ ...options, candidateCommit: "2".repeat(40) }, input.dependencies);
  const published = await publishFrameworkRuntime(next);
  assert.equal(published.candidateCommit, "2".repeat(40));
  for (const toolchain of next.manifest.toolchains) for (const framework of toolchain.frameworks) {
    assert.equal(await hashCompiledFrameworkExecutable(join(input.repositoryRoot, ".native-e2e/framework-work"), "linux", toolchain.family, framework.frameworkId), framework.evidence.executableArtifactSha256);
  }
});

test("producer discovers against the selected alternate repository contract", async (t) => {
  const input = await fixture(t, "alternate-contract");
  const result = await prepareFrameworkRuntime({ repositoryRoot: input.repositoryRoot, platform: "linux", candidateCommit: "1".repeat(40) }, input.dependencies);
  assert.equal(result.manifest.contractSha256, digest(await readFile(join(input.repositoryRoot, "testdata/framework-matrix/contract.json"), "utf8")));
  assert.notEqual(result.manifest.contractSha256, digest(await readFile(join(root, "testdata/framework-matrix/contract.json"), "utf8")));
  assert.equal(result.manifest.toolchains[0]!.frameworks[0]!.catalogArtifactSha256, digest(JSON.stringify(input.catalogs[0])));
});

for (const phase of ["inputs", "startup", "early-filesystem"] as const) {
  test(`preparation sanitizes ${phase} errors without cause, output, environment, or paths`, async (t) => {
    const input = await fixture(t);
    const unsafe = new Error(`${input.repositoryRoot} TOKEN=secret stdout=private stderr=private`);
    const dependencies = { ...input.dependencies,
      ...(phase === "inputs" ? { verifyInputs: async () => { throw unsafe; } } : {}),
      ...(phase === "startup" ? { startService: async () => { throw unsafe; } } : {}),
    };
    if (phase === "early-filesystem") await rm(join(input.repositoryRoot, "tools/framework-bundle/manifest.json"));
    await assert.rejects(prepareFrameworkRuntime({ repositoryRoot: input.repositoryRoot, platform: "linux", candidateCommit: "1".repeat(40) }, dependencies), (error: Error & { code?: string }) => {
      assert.equal(error.message, "framework runtime preparation failed");
      assert.equal(error.code, "FRAMEWORK_RUNTIME_PREPARE_FAILED");
      assert.equal(error.cause, undefined);
      assert.doesNotMatch(`${error.stack} ${JSON.stringify(error)}`, /TOKEN|secret|stdout|stderr|framework-producer-|[A-Za-z]:\\/u);
      return true;
    });
  });
}

for (const mutation of ["partial", "framework", "container", "family", "artifact", "task", "compiler", "fixture-input", "matrix-input", "duplicate", "cleanup"]) {
  test(`producer rejects ${mutation} and disposes all started Services`, async (t) => {
    const input = await fixture(t, mutation);
    await assert.rejects(prepareFrameworkRuntime({ repositoryRoot: input.repositoryRoot, platform: "linux", candidateCommit: "1".repeat(40) }, input.dependencies), (error: Error) => { assert.ok(!error.message.includes("secret raw")); return true; });
    assert.equal(input.counts().disposed, input.counts().started);
    if (mutation === "fixture-input") assert.equal(input.counts().started, 0);
  });
}
