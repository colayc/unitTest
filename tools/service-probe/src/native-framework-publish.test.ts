import assert from "node:assert/strict";
import { createHash, randomUUID } from "node:crypto";
import fs from "node:fs/promises";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import test from "node:test";
import type { PreparedFrameworkRuntime } from "./native-framework-prepare.js";
import { loadRequiredFrameworkRuntime } from "./native-framework-runtime.js";

const url = new URL("./native-framework-publish.js", import.meta.url).href;
const { publishFrameworkRuntime, acquireFrameworkRuntimeLock } = await import(url);
const digest = (value: string) => createHash("sha256").update(value).digest("hex");

async function fixture(t: test.TestContext, root?: string, candidate = "1".repeat(40)) {
  const repositoryRoot = root ?? await fs.mkdtemp(join(tmpdir(), "framework-publish-"));
  if (!root) t.after(() => fs.rm(repositoryRoot, { recursive: true, force: true }));
  const ownershipId = randomUUID();
  const work = join(repositoryRoot, ".native-e2e/framework-work");
  const staging = join(work, ".staging", ownershipId);
  const roots: string[] = [];
  await fs.mkdir(join(repositoryRoot, "testdata/framework-matrix"), { recursive: true });
  await fs.writeFile(join(repositoryRoot, "testdata/framework-matrix/contract.json"), "contract");
  const toolchains = [];
  for (const family of ["clang", "gcc"] as const) {
    const frameworks = [];
    for (const frameworkId of ["cpputest", "unity"] as const) {
      const stage = join(staging, "linux", family, frameworkId);
      roots.push(stage);
      await fs.mkdir(join(stage, "workspace"), { recursive: true });
      await fs.writeFile(join(stage, "owner.json"), JSON.stringify({ schemaVersion: 1, platform: "linux", candidate, invocationId: ownershipId }));
      const bin = join(stage, "service/data/build", "a".repeat(64), "bin");
      await fs.mkdir(bin, { recursive: true });
      await fs.writeFile(join(bin, `phase9_${frameworkId}`), candidate);
      frameworks.push({ frameworkId, dependencyVersion: "1.0", dependencySha256: digest("dependency"), dependencyTreeSha256: digest("tree"),
        catalogArtifactSha256: digest("catalog"), stableIdDigest: digest("ids"), timeoutMs: 120000,
        evidence: { executableArtifactSha256: digest(candidate), sourceArtifactSha256: digest("source"), sourceLocationDigest: digest("location") },
        ...(frameworkId === "unity" ? { cMockProvenance: { revision: "a".repeat(40), generatorVersion: "1.0", inputSha256: digest("input"), outputSha256: digest("output"), manifestSha256: digest("manifest"), generatedAtRuntime: false as const } } : {}),
      });
    }
    toolchains.push({ family, compilerVersion: "1.0", compilerSha256: digest("compiler"), frameworks });
  }
  const prepared: PreparedFrameworkRuntime = { ownershipId, ownedStagingRoots: roots, manifest: {
    schemaVersion: 1, candidateCommit: candidate, platform: "linux", contractSha256: digest("contract"), toolchains,
    benchmark: { id: "catalog-10000", itemCount: 10000, sampleCount: 3, allocationBudgetPerOperation: 300000,
      allocationsPerOperation: [101, 102, 103], catalogRevision: digest("revision"), catalogArtifactSha256: digest("artifact"), stableIdDigest: digest("ids"), status: "passed" },
  } };
  return { repositoryRoot, prepared, staging, work, manifest: join(repositoryRoot, ".native-e2e/framework-runtime/linux.json") };
}

async function assertComplete(input: Awaited<ReturnType<typeof fixture>>, candidate: string) {
  assert.equal(JSON.parse(await fs.readFile(input.manifest, "utf8")).candidateCommit, candidate);
  for (const family of ["clang", "gcc"]) for (const framework of ["cpputest", "unity"]) {
    assert.equal(await fs.readFile(join(input.work, "linux", family, framework, "service/data/build", "a".repeat(64), "bin", `phase9_${framework}`), "utf8"), candidate);
  }
}

test("publisher installs and replaces complete runtime, preserving compiled evidence and removing owned staging", async (t) => {
  const input = await fixture(t);
  const result = await publishFrameworkRuntime(input.prepared);
  await assertComplete(input, "1".repeat(40));
  assert.equal(result.manifestSha256, digest(await fs.readFile(input.manifest, "utf8")));
  assert.deepEqual(result.toolchainFamilies, ["clang", "gcc"]);
  assert.doesNotMatch(JSON.stringify(result), /framework-work|[A-Z]:\\/u);
  await assert.rejects(fs.lstat(input.staging), { code: "ENOENT" });
  const next = await fixture(t, input.repositoryRoot, "2".repeat(40));
  await publishFrameworkRuntime(next.prepared);
  await assertComplete(input, "2".repeat(40));
  assert.deepEqual(await fs.readdir(dirname(input.manifest)), ["linux.json"]);
});

for (const phase of ["rename", "validation"] as const) test(`publisher restores old pair after ${phase} failure and holds consumer lock`, async (t) => {
  const input = await fixture(t);
  await publishFrameworkRuntime(input.prepared);
  const next = await fixture(t, input.repositoryRoot, "2".repeat(40));
  const rename = fs.rename;
  let injected = false;
  t.mock.method(fs, "rename", async (from: string, to: string) => {
    if (from === join(next.staging, "linux")) {
      await assert.rejects(loadRequiredFrameworkRuntime(input.repositoryRoot, "linux", join(input.repositoryRoot, "artifacts")), /publication|locked/u);
      if (phase === "rename") { injected = true; throw new Error("private rename failure"); }
    }
    await rename(from, to);
    if (phase === "validation" && to === input.manifest && !injected) {
      injected = true;
      await fs.writeFile(to, "invalid");
    }
  });
  await assert.rejects(publishFrameworkRuntime(next.prepared), /publication failed/u);
  assert.ok(injected);
  await assertComplete(input, "1".repeat(40));
  await assert.rejects(fs.lstat(next.staging), { code: "ENOENT" });
});

test("publisher refuses unknown staging, backups and locks without removing them", async (t) => {
  for (const kind of ["stage", "backup", "lock"]) {
    const input = await fixture(t);
    const path = kind === "stage" ? join(input.prepared.ownedStagingRoots[0]!, "owner.json") : kind === "backup" ? join(input.work, ".backup-linux") : join(dirname(input.manifest), "linux.lock");
    await fs.mkdir(dirname(path), { recursive: true });
    await fs.writeFile(path, "unknown");
    await assert.rejects(publishFrameworkRuntime(input.prepared), /publication/u);
    assert.equal(await fs.readFile(path, "utf8"), "unknown");
  }
});

test("publication lock is exclusive, owner checked and consumer respects it", async (t) => {
  const input = await fixture(t);
  const release = await acquireFrameworkRuntimeLock(input.repositoryRoot, "linux");
  await assert.rejects(publishFrameworkRuntime(input.prepared), /publication/u);
  await assert.rejects(loadRequiredFrameworkRuntime(input.repositoryRoot, "linux", join(input.repositoryRoot, "artifacts")), /publication|locked/u);
  await release();
  await publishFrameworkRuntime(input.prepared);
  await assertComplete(input, "1".repeat(40));
});

test("consumer holds publication lock through Service disposal and releases it on startup failure", async (t) => {
  const input = await fixture(t);
  await publishFrameworkRuntime(input.prepared);
  const runtime = await loadRequiredFrameworkRuntime(input.repositoryRoot, "linux", join(input.repositoryRoot, "artifacts"), {
    loadFrameworkIdentity: async () => ({}) as any,
    startService: async () => ({ dispose: async () => {} }) as any,
  });
  await assert.rejects(acquireFrameworkRuntimeLock(input.repositoryRoot, "linux"), /publication/u);
  await runtime.dispose();
  const release = await acquireFrameworkRuntimeLock(input.repositoryRoot, "linux");
  await release();
  await assert.rejects(loadRequiredFrameworkRuntime(input.repositoryRoot, "linux", join(input.repositoryRoot, "artifacts"), {
    loadFrameworkIdentity: async () => { throw new Error("identity failure"); }, startService: async () => { throw new Error("must not run"); },
  }), /identity failure/u);
  await (await acquireFrameworkRuntimeLock(input.repositoryRoot, "linux"))();
});

test("lock release preserves unknown ownership instead of clearing another invocation", async (t) => {
  const input = await fixture(t);
  const release = await acquireFrameworkRuntimeLock(input.repositoryRoot, "linux");
  const owner = join(dirname(input.manifest), "linux.lock/owner");
  await fs.writeFile(owner, "unknown");
  await assert.rejects(release(), /publication lock release/u);
  assert.equal(await fs.readFile(owner, "utf8"), "unknown");
});
