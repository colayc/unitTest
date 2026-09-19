import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { mkdtemp, mkdir, readFile, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { dirname, join, resolve } from "node:path";
import test from "node:test";
import type { WorkspaceSnapshot } from "@unit-test-ide/protocol-models";
import { ProtocolError, type EventSubscription, type ProtocolClient } from "@unit-test-ide/test-client";
import type { TaskServiceFixture } from "./probe.js";
import { FRAMEWORK_SCENARIO_IDS } from "./native-framework-report.js";
import {
  __testing,
  parseRequiredToolchains,
  verifyPreparedCMakeBundle,
  type NativeMatrixOptions,
  type PreparedCMakeBundle,
} from "./native-build.js";
import { __testing as reportTesting } from "./native-report.js";
import type { F1FrameworkIdentity, FrameworkPlatformOptions } from "./native-framework-matrix.js";

const trackedManifestPath = resolve(import.meta.dirname, "../../../tools/cmake-bundle/manifest.json");

test("required native toolchain parsing is closed and deterministic", () => {
  assert.deepEqual([...parseRequiredToolchains(undefined)], []);
  assert.deepEqual([...parseRequiredToolchains("gcc, clang")], ["gcc", "clang"]);
  assert.throws(() => parseRequiredToolchains("gcc,gcc"), /duplicate required/);
  assert.throws(() => parseRequiredToolchains("gcc,cuda"), /invalid required/);
});

test("preset compiler validation accepts another installed version of the requested family", () => {
  const events = [{
    event: "task.output",
    payload: { text: "-- The CXX compiler identification is MSVC 19.43.34810.0\n" },
  }];

  assert.doesNotThrow(() =>
    __testing.assertPresetCompiler(
      events as never,
      "msvc",
      "MSVC",
    )
  );
});

test("prepared bundle verification fails for a missing bundle before any Service launch", async (t) => {
  const root = await mkdtemp(join(tmpdir(), "native-bundle-missing-"));
  t.after(() => rm(root, { recursive: true, force: true }));
  await assert.rejects(
    verifyPreparedCMakeBundle(join(root, "absent"), "linux", "x64"),
    /bundle root is unavailable/,
  );
});

test("prepared bundle verification binds state, version, and installed digests", async (t) => {
  const fixture = await createPreparedBundleFixture();
  t.after(() => rm(fixture.container, { recursive: true, force: true }));

  const verified = await verifyPreparedCMakeBundle(
    fixture.bundleRoot,
    "linux",
    "x64",
    fixture.operations,
  );
  assert.equal(verified.bundleRoot, fixture.bundleRoot);
  assert.equal(verified.cmakeVersion, "4.3.4");
  assert.equal(verified.executable, fixture.executable);

  const state = JSON.parse(await readFile(fixture.statePath, "utf8")) as Record<string, unknown>;
  state.archiveSha256 = "0".repeat(64);
  await writeFile(fixture.statePath, `${JSON.stringify(state, null, 2)}\n`);
  await assert.rejects(
    verifyPreparedCMakeBundle(fixture.bundleRoot, "linux", "x64", fixture.operations),
    /bundle state mismatch/,
  );
});

test("native matrix uses only the verified bundle and explicit trusted workspace options", async (t) => {
  const root = await mkdtemp(join(tmpdir(), "native-matrix-"));
  t.after(() => rm(root, { recursive: true, force: true }));
  const serviceBinary = join(root, "build", "unit-test-service");
  await mkdir(dirname(serviceBinary), { recursive: true });
  await writeFile(serviceBinary, "fixture");

  const bundle = fakePreparedBundle(join(root, ".bundled-tools", "cmake"));
  const launches: Array<{ options: Record<string, unknown>; disposed: boolean }> = [];
  let reportResults: readonly unknown[] = [];
  const dependencies: Parameters<typeof __testing.runNativeMatrixWithDependencies>[1] = {
    environment: { PATH: join(root, "poison-path") },
    architecture: "x64",
    repositoryRoot: root,
    verifyBundle: async (bundleRoot) => {
      assert.equal(bundleRoot, bundle.bundleRoot);
      return bundle;
    },
    createWorkspace: async (_root, _platform, family) => {
      const familyRoot = join(root, "work", family);
      const workspaceRoot = join(familyRoot, "workspace");
      const serviceDirectory = join(familyRoot, "service");
      await mkdir(workspaceRoot, { recursive: true });
      await mkdir(serviceDirectory, { recursive: true });
      return { root: familyRoot, workspaceRoot, serviceDirectory };
    },
    launchService: async (_binary, _directory, options) => {
      const launch = { options: options as Record<string, unknown>, disposed: false };
      launches.push(launch);
      const family = launches.length === 1 ? "gcc" : "clang";
      return {
        client: {
          inspectWorkspace: async () => family === "gcc"
            ? workspaceSnapshot("gcc")
            : workspaceSnapshot(),
        },
        dispose: async () => { launch.disposed = true; },
      } as unknown as TaskServiceFixture;
    },
    executeScenarios: async (context) => {
      assert.equal(context.family, "gcc");
      assert.equal(context.profile.toolchainId, "gcc-test");
      return { "default-build": "passed" };
    },
    cleanupWorkspace: async () => undefined,
    writeReport: async (_directory, _platform, _architecture, _bundle, results) => {
      reportResults = results;
      return join(root, "toolchain-report.json");
    },
  };
  const options: NativeMatrixOptions = {
    platform: "linux",
    requiredFamilies: ["gcc", "clang"],
    artifactDirectory: join(root, "artifacts"),
    workDirectory: join(root, "short-work"),
  };
  const results = await __testing.runNativeMatrixWithDependencies(options, dependencies);

  assert.equal(results[0]?.toolchainFamily, "gcc");
  assert.equal(results[0]?.scenarios["default-build"], "passed");
  assert.equal(results[1]?.toolchainFamily, "clang");
  assert.equal(results[1]?.scenarios.discovery, "skipped");
  assert.equal(reportResults, results);
  assert.equal(launches.length, 2);
  for (const launch of launches) {
    assert.equal(launch.options.trustedWorkspace, true);
    assert.equal(launch.options.cmakeBundleRoot, bundle.bundleRoot);
    assert.equal("devCMakeExecutable" in launch.options, false);
    assert.equal(launch.disposed, true);
  }
});

test("declared required family absence fails and bundle preflight stays before launch", async (t) => {
  const root = await mkdtemp(join(tmpdir(), "native-required-"));
  t.after(() => rm(root, { recursive: true, force: true }));
  const serviceBinary = join(root, "build", "unit-test-service");
  await mkdir(dirname(serviceBinary), { recursive: true });
  await writeFile(serviceBinary, "fixture");
  let launches = 0;
  let workspaces = 0;
  const base: Parameters<typeof __testing.runNativeMatrixWithDependencies>[1] = {
    environment: { UNIT_TEST_IDE_NATIVE_REQUIRED_TOOLCHAINS: "gcc,clang" },
    architecture: "x64",
    repositoryRoot: root,
    verifyBundle: async () => fakePreparedBundle(join(root, ".bundled-tools", "cmake")),
    createWorkspace: async (_root, _platform, family) => {
      workspaces++;
      const familyRoot = join(root, "work", family);
      await mkdir(join(familyRoot, "workspace"), { recursive: true });
      await mkdir(join(familyRoot, "service"), { recursive: true });
      return {
        root: familyRoot,
        workspaceRoot: join(familyRoot, "workspace"),
        serviceDirectory: join(familyRoot, "service"),
      };
    },
    launchService: async () => {
      launches++;
      return {
        client: { inspectWorkspace: async () => workspaceSnapshot() },
        dispose: async () => undefined,
      } as unknown as TaskServiceFixture;
    },
    executeScenarios: async () => ({ "default-build": "passed" }),
    cleanupWorkspace: async () => undefined,
    writeReport: async () => join(root, "report.json"),
  };
  const options: NativeMatrixOptions = {
    platform: "linux",
    requiredFamilies: ["gcc", "clang"],
    artifactDirectory: join(root, "artifacts"),
  };
  await assert.rejects(
    __testing.runNativeMatrixWithDependencies(options, base),
    /required native toolchain gcc was not discovered/,
  );
  assert.equal(launches, 1);

  launches = 0;
  workspaces = 0;
  await assert.rejects(
    __testing.runNativeMatrixWithDependencies(options, {
      ...base,
      verifyBundle: async () => {
        throw new Error("prepared CMake bundle state mismatch");
      },
    }),
    /bundle state mismatch/,
  );
  assert.equal(launches, 0);
  assert.equal(workspaces, 0);
});

test("required native mode rejects a missing framework platform before Service launch", async (t) => {
  const root = await mkdtemp(join(tmpdir(), "native-framework-required-"));
  t.after(() => rm(root, { recursive: true, force: true }));
  const serviceBinary = join(root, "build", "unit-test-service");
  await mkdir(dirname(serviceBinary), { recursive: true });
  await writeFile(serviceBinary, "fixture");
  let launches = 0;
  let bundleChecks = 0;
  const dependencies: Parameters<typeof __testing.runNativeMatrixWithDependencies>[1] = {
    environment: {
      UNIT_TEST_IDE_NATIVE_REQUIRED_TOOLCHAINS: "gcc,clang",
      UNIT_TEST_IDE_P4_FRAMEWORK_MATRIX_REQUIRED: "1",
    },
    architecture: "x64",
    repositoryRoot: root,
    verifyBundle: async () => {
      bundleChecks++;
      return fakePreparedBundle(join(root, ".bundled-tools", "cmake"));
    },
    createWorkspace: async () => {
      throw new Error("workspace creation must not run");
    },
    launchService: async () => {
      launches++;
      throw new Error("Service launch must not run");
    },
    executeScenarios: async () => ({ "default-build": "passed" }),
    cleanupWorkspace: async () => undefined,
    writeReport: async () => join(root, "toolchain-report.json"),
  };

  await assert.rejects(
    __testing.runNativeMatrixWithDependencies({
      platform: "linux",
      requiredFamilies: ["gcc", "clang"],
      artifactDirectory: join(root, "artifacts"),
    }, dependencies),
    /required framework platform is missing/u,
  );
  assert.equal(bundleChecks, 0);
  assert.equal(launches, 0);
});

test("Linux framework evidence hashes the unique compiled executable from the fixed Service build root", async (t) => {
  const root = await mkdtemp(join(tmpdir(), "native-framework-executable-linux-"));
  t.after(() => rm(root, { recursive: true, force: true }));
  const executable = join(
    root, ".native-e2e", "framework-work", "linux", "gcc", "unity",
    "service", "data", "build", "a".repeat(64), "bin", "phase9_unity",
  );
  await mkdir(dirname(executable), { recursive: true });
  const bytes = Buffer.from("compiled-gcc-unity", "utf8");
  await writeFile(executable, bytes);

  assert.equal(
    await __testing.frameworkExecutableDigest(root, "linux", "gcc", "unity"),
    createHash("sha256").update(bytes).digest("hex"),
  );
});

test("required Linux framework preflight rejects a missing fixture or F1 provenance before Service launch", async (t) => {
  const root = await mkdtemp(join(tmpdir(), "native-framework-preflight-linux-"));
  t.after(() => rm(root, { recursive: true, force: true }));
  const serviceBinary = join(root, "build", "unit-test-service");
  await mkdir(dirname(serviceBinary), { recursive: true });
  await writeFile(serviceBinary, "fixture");
  let launches = 0;
  const dependencies: Parameters<typeof __testing.runNativeMatrixWithDependencies>[1] = {
    environment: {
      UNIT_TEST_IDE_NATIVE_REQUIRED_TOOLCHAINS: "gcc,clang",
      UNIT_TEST_IDE_P4_FRAMEWORK_MATRIX_REQUIRED: "1",
    },
    architecture: "x64",
    repositoryRoot: root,
    verifyBundle: async () => fakePreparedBundle(join(root, ".bundled-tools", "cmake")),
    loadFrameworkIdentity: async () => frameworkIdentity,
    createWorkspace: async () => { throw new Error("workspace creation must not run"); },
    launchService: async () => {
      launches++;
      throw new Error("Service launch must not run");
    },
    executeScenarios: async () => ({ "default-build": "passed" }),
    cleanupWorkspace: async () => undefined,
    writeReport: async () => join(root, "toolchain-report.json"),
  };
  const incomplete = requiredLinuxFrameworkPlatform(join(root, "artifacts"));
  (incomplete.toolchains[0]!.frameworks as Array<unknown>).pop();
  await assert.rejects(
    __testing.runNativeMatrixWithDependencies({
      platform: "linux", requiredFamilies: ["gcc", "clang"], artifactDirectory: join(root, "artifacts"),
      frameworkPlatform: incomplete,
    }, dependencies),
    /required framework fixture set is incomplete/u,
  );

  const missingProvenance = requiredLinuxFrameworkPlatform(join(root, "artifacts"));
  delete (missingProvenance.toolchains[0]!.frameworks[1] as { cMockProvenance?: unknown }).cMockProvenance;
  await assert.rejects(
    __testing.runNativeMatrixWithDependencies({
      platform: "linux", requiredFamilies: ["gcc", "clang"], artifactDirectory: join(root, "artifacts"),
      frameworkPlatform: missingProvenance,
    }, dependencies),
    /required unity provenance does not match F1/u,
  );
  assert.equal(launches, 0);
});

test("required Linux native run carries F1 identity and verifies exact 2x2x17 report evidence", async (t) => {
  const root = await mkdtemp(join(tmpdir(), "native-framework-platform-linux-"));
  t.after(() => rm(root, { recursive: true, force: true }));
  const artifactDirectory = join(root, "artifacts");
  const serviceBinary = join(root, "build", "unit-test-service");
  await mkdir(dirname(serviceBinary), { recursive: true });
  await writeFile(serviceBinary, "fixture");
  const frameworkPlatform = requiredLinuxFrameworkPlatform(artifactDirectory);
  const events: string[] = [];
  let launchIndex = 0;
  const dependencies: Parameters<typeof __testing.runNativeMatrixWithDependencies>[1] = {
    environment: {
      UNIT_TEST_IDE_NATIVE_REQUIRED_TOOLCHAINS: "gcc,clang",
      UNIT_TEST_IDE_P4_FRAMEWORK_MATRIX_REQUIRED: "1",
    },
    architecture: "x64",
    repositoryRoot: root,
    verifyBundle: async () => fakePreparedBundle(join(root, ".bundled-tools", "cmake")),
    loadFrameworkIdentity: async () => frameworkIdentity,
    frameworkExecutableDigest: async (_root, _platform, family, frameworkId) =>
      createHash("sha256").update(`${family}:${frameworkId}`).digest("hex"),
    createWorkspace: async (_work, _platform, family) => {
      const familyRoot = join(root, "work", family);
      const workspaceRoot = join(familyRoot, "workspace");
      const serviceDirectory = join(familyRoot, "service");
      await mkdir(workspaceRoot, { recursive: true });
      await mkdir(serviceDirectory, { recursive: true });
      return { root: familyRoot, workspaceRoot, serviceDirectory };
    },
    launchService: async () => {
      const family = (["gcc", "clang"] as const)[launchIndex++]!;
      return {
        client: { inspectWorkspace: async () => workspaceSnapshot(family) },
        dispose: async () => { events.push(`dispose:${family}`); },
      } as unknown as TaskServiceFixture;
    },
    runFrameworkToolchain: async (_options, family, identity) => {
      assert.equal(identity, frameworkIdentity, "validated F1 identity must reach the Linux Service catalog runner");
      events.push(`framework:${family}`);
      return {
        family,
        compilerVersion: "15.1.0",
        compilerSha256: createHash("sha256").update(`compiler:${family}`).digest("hex"),
        frameworks: (["cpputest", "unity"] as const).map((id) => ({
          id,
          dependencyVersion: id === "cpputest" ? "4.0" : "2.6.1",
          dependencySha256: createHash("sha256").update(`dependency:${id}`).digest("hex"),
          dependencyTreeSha256: frameworkIdentity.frameworkTreeSha256[id],
          catalogRevision: createHash("sha256").update(`revision:${family}:${id}`).digest("hex"),
          catalogArtifactSha256: createHash("sha256").update(`catalog:${family}:${id}`).digest("hex"),
          sourceArtifactSha256: frameworkIdentity.fixtures[id].sourceSha256,
          sourceLocationDigest: createHash("sha256").update(`locations:${id}`).digest("hex"),
          executableArtifactSha256: createHash("sha256").update(`${family}:${id}`).digest("hex"),
          stableIdDigest: createHash("sha256").update(`stable:${family}:${id}`).digest("hex"),
          ...(id === "unity" ? { cMockProvenance: {
            revision: "6ea503340b1d3fdc0f2bcaf69273ba0160ec83af",
            generatorVersion: "2.7.0",
            inputSha256: "007f23aea2dba06d111f66be95905adde8fe32e7d2031bf8a8c70117a8209f57",
            outputSha256: "1565d1a2d39b655eb551a729663fae7e167f1c0d6cd3f9a8c2aafe2d67348128",
            manifestSha256: frameworkIdentity.cMockProvenanceSha256,
            generatedAtRuntime: false,
          } } : {}),
          scenarios: FRAMEWORK_SCENARIO_IDS.map((scenarioId) => ({ id: scenarioId })),
        })),
      } as never;
    },
    executeScenarios: async ({ family }) => {
      events.push(`core:${family}`);
      return { "default-build": "passed" };
    },
    cleanupWorkspace: async ({ root: workspaceRoot }) => {
      events.push(`cleanup:${workspaceRoot.includes("gcc") ? "gcc" : "clang"}`);
    },
    writeReport: async () => join(artifactDirectory, "toolchain-report.json"),
    publishFrameworkReport: async (_options, toolchains) => {
      const serialized = {
        platform: "linux",
        toolchains: toolchains.map(({ family, frameworks }) => ({
          family,
          frameworks: frameworks.map(({ id, scenarios, sourceArtifactSha256, stableIdDigest, cMockProvenance }) => ({
            id, scenarios, sourceArtifactSha256, stableIdDigest, cMockProvenance,
          })),
        })),
      };
      await mkdir(artifactDirectory, { recursive: true });
      await writeFile(join(artifactDirectory, "framework-report.json"), `${JSON.stringify(serialized)}\n`);
      return serialized as never;
    },
    verifyFrameworkReport: async (directory, platform) => {
      const report = JSON.parse(await readFile(join(directory, "framework-report.json"), "utf8")) as {
        platform: string;
        toolchains: Array<{ family: string; frameworks: Array<{
          id: "cpputest" | "unity";
          scenarios: Array<{ id: string }>;
          sourceArtifactSha256: string;
          stableIdDigest: string;
          cMockProvenance?: { manifestSha256: string; generatedAtRuntime: boolean };
        }> }>;
      };
      assert.equal(platform, "linux");
      assert.equal(report.platform, "linux");
      assert.deepEqual(report.toolchains.map(({ family }) => family), ["gcc", "clang"]);
      for (const { family, frameworks } of report.toolchains) {
        assert.deepEqual(frameworks.map(({ id }) => id), ["cpputest", "unity"]);
        for (const framework of frameworks) {
          assert.deepEqual(framework.scenarios.map(({ id }) => id), [...FRAMEWORK_SCENARIO_IDS]);
          assert.equal(framework.sourceArtifactSha256, frameworkIdentity.fixtures[framework.id].sourceSha256);
          assert.equal(
            framework.stableIdDigest,
            createHash("sha256").update(`stable:${family}:${framework.id}`).digest("hex"),
          );
          assert.equal(framework.cMockProvenance?.manifestSha256,
            framework.id === "unity" ? frameworkIdentity.cMockProvenanceSha256 : undefined);
          assert.equal(framework.cMockProvenance?.generatedAtRuntime,
            framework.id === "unity" ? false : undefined);
        }
      }
      return report as never;
    },
  };

  await __testing.runNativeMatrixWithDependencies({
    platform: "linux",
    requiredFamilies: ["gcc", "clang"],
    artifactDirectory,
    frameworkPlatform,
  }, dependencies);
  assert.deepEqual(events, [
    "framework:gcc", "core:gcc", "dispose:gcc", "cleanup:gcc",
    "framework:clang", "core:clang", "dispose:clang", "cleanup:clang",
  ]);
});

test("generated family build repeats its fresh checkpoint after one stale Start", async () => {
  let inspections = 0;
  const starts: Array<{
    idempotencyKey: string;
    workspaceGeneration: string;
    targetIds: string[];
  }> = [];
  const client = {
    inspectWorkspace: async () => {
      inspections++;
      const snapshot = workspaceSnapshot("gcc");
      snapshot.workspaceGeneration = (inspections === 1 ? "c" : "e").repeat(64);
      return snapshot;
    },
    startCMakeBuild: async (
      request: {
        idempotencyKey: string;
        workspaceGeneration: string;
        targetIds: string[];
      },
    ) => {
      starts.push(request);
      if (starts.length === 1) {
        throw new ProtocolError("WORKSPACE_CHANGED", "workspace generation is stale", true);
      }
      return { taskId: "task-after-family-refresh" };
    },
  } as unknown as ProtocolClient;

  const task = await __testing.startFamilyBuildAtCheckpoint(
    client,
    "gcc",
    "configure-invalidation",
    [],
    60_000,
  );

  assert.equal(task.taskId, "task-after-family-refresh");
  assert.equal(task.selected.snapshot.workspaceGeneration, "e".repeat(64));
  assert.equal(inspections, 2);
  assert.deepEqual(
    starts.map((request) => request.workspaceGeneration),
    ["c".repeat(64), "e".repeat(64)],
  );
  assert.deepEqual(starts.map((request) => request.targetIds), [[], []]);
  assert.notEqual(starts[0]?.idempotencyKey, starts[1]?.idempotencyKey);
});

test("generated family stale-checkpoint retry is bounded to one", async () => {
  let inspections = 0;
  let starts = 0;
  const client = {
    inspectWorkspace: async () => {
      inspections++;
      return workspaceSnapshot("gcc");
    },
    startCMakeBuild: async () => {
      starts++;
      throw new ProtocolError("WORKSPACE_CHANGED", "workspace generation is stale", true);
    },
  } as unknown as ProtocolClient;

  await assert.rejects(
    __testing.startFamilyBuildAtCheckpoint(
      client,
      "gcc",
      "default-build",
      [],
      60_000,
    ),
    (error) => error instanceof ProtocolError && error.code === "WORKSPACE_CHANGED",
  );
  assert.equal(inspections, 2);
  assert.equal(starts, 2);
});

test("late named-target build establishes a fresh workspace checkpoint", async () => {
  const inspections: string[] = [];
  const listings: Array<{ workspaceGeneration: string }> = [];
  const starts: Array<{
    idempotencyKey: string;
    workspaceGeneration: string;
    targetIds: string[];
  }> = [];
  const snapshot = workspaceSnapshot("gcc");
  snapshot.workspaceGeneration = "c".repeat(64);
  const targetId = "d".repeat(64);
  const client = {
    inspectWorkspace: async () => {
      inspections.push(snapshot.workspaceGeneration);
      return snapshot;
    },
    listCMakeTargets: async (request: { workspaceGeneration: string }) => {
      listings.push(request);
      return {
        workspaceGeneration: request.workspaceGeneration,
        projectId: "root",
        buildProfileId: "b".repeat(64),
        targets: [{ targetId, name: "slow_target" }],
      };
    },
    startCMakeBuild: async (
      request: {
        idempotencyKey: string;
        workspaceGeneration: string;
        targetIds: string[];
      },
    ) => {
      starts.push(request);
      return { taskId: "task-at-checkpoint" };
    },
  } as unknown as ProtocolClient;

  const task = await __testing.startNamedTargetBuildAtCheckpoint(
    client,
    "gcc",
    "timeout",
    "slow_target",
    1_000,
  );

  assert.equal(task.taskId, "task-at-checkpoint");
  assert.deepEqual(inspections, ["c".repeat(64)]);
  assert.deepEqual(listings.map((request) => request.workspaceGeneration), ["c".repeat(64)]);
  assert.equal(starts.length, 1);
  assert.equal(starts[0]?.workspaceGeneration, "c".repeat(64));
  assert.deepEqual(starts[0]?.targetIds, [targetId]);
});

test("late named-target build repeats its checkpoint when target listing is stale", async () => {
  let inspections = 0;
  const listings: string[] = [];
  const starts: Array<{ workspaceGeneration: string; targetIds: string[] }> = [];
  const client = {
    inspectWorkspace: async () => {
      inspections++;
      const snapshot = workspaceSnapshot("gcc");
      snapshot.workspaceGeneration = (inspections === 1 ? "c" : "e").repeat(64);
      return snapshot;
    },
    listCMakeTargets: async (request: { workspaceGeneration: string }) => {
      listings.push(request.workspaceGeneration);
      if (listings.length === 1) {
        throw new ProtocolError("WORKSPACE_CHANGED", "workspace generation is stale", true);
      }
      return {
        workspaceGeneration: request.workspaceGeneration,
        projectId: "root",
        buildProfileId: "b".repeat(64),
        targets: [{ targetId: "f".repeat(64), name: "slow_target" }],
      };
    },
    startCMakeBuild: async (
      request: { workspaceGeneration: string; targetIds: string[] },
    ) => {
      starts.push(request);
      return { taskId: "task-after-target-list-refresh" };
    },
  } as unknown as ProtocolClient;

  const task = await __testing.startNamedTargetBuildAtCheckpoint(
    client,
    "gcc",
    "cancellation-reconnect",
    "slow_target",
    60_000,
  );

  assert.equal(task.taskId, "task-after-target-list-refresh");
  assert.equal(inspections, 2);
  assert.deepEqual(listings, ["c".repeat(64), "e".repeat(64)]);
  assert.equal(starts.length, 1);
  assert.equal(starts[0]?.workspaceGeneration, "e".repeat(64));
  assert.deepEqual(starts[0]?.targetIds, ["f".repeat(64)]);
});

test("late named-target build relists its target after one stale checkpoint", async () => {
  let inspections = 0;
  const listings: Array<{ workspaceGeneration: string }> = [];
  const starts: Array<{
    idempotencyKey: string;
    workspaceGeneration: string;
    targetIds: string[];
  }> = [];
  const client = {
    inspectWorkspace: async () => {
      inspections++;
      const snapshot = workspaceSnapshot("gcc");
      snapshot.workspaceGeneration = (inspections === 1 ? "c" : "e").repeat(64);
      return snapshot;
    },
    listCMakeTargets: async (request: { workspaceGeneration: string }) => {
      listings.push(request);
      const targetId = request.workspaceGeneration === "c".repeat(64)
        ? "d".repeat(64)
        : "f".repeat(64);
      return {
        workspaceGeneration: request.workspaceGeneration,
        projectId: "root",
        buildProfileId: "b".repeat(64),
        targets: [{ targetId, name: "slow_target" }],
      };
    },
    startCMakeBuild: async (
      request: {
        idempotencyKey: string;
        workspaceGeneration: string;
        targetIds: string[];
      },
    ) => {
      starts.push(request);
      if (starts.length === 1) {
        throw new ProtocolError("WORKSPACE_CHANGED", "workspace generation is stale", true);
      }
      return { taskId: "task-after-checkpoint-refresh" };
    },
  } as unknown as ProtocolClient;

  const task = await __testing.startNamedTargetBuildAtCheckpoint(
    client,
    "gcc",
    "cancellation-reconnect",
    "slow_target",
    60_000,
  );

  assert.equal(task.taskId, "task-after-checkpoint-refresh");
  assert.equal(inspections, 2);
  assert.deepEqual(
    listings.map((request) => request.workspaceGeneration),
    ["c".repeat(64), "e".repeat(64)],
  );
  assert.deepEqual(starts.map((request) => request.targetIds), [
    ["d".repeat(64)],
    ["f".repeat(64)],
  ]);
  assert.notEqual(starts[0]?.idempotencyKey, starts[1]?.idempotencyKey);
});

test("late named-target stale-checkpoint retry is bounded to one", async () => {
  let inspections = 0;
  let listings = 0;
  let starts = 0;
  const client = {
    inspectWorkspace: async () => {
      inspections++;
      return workspaceSnapshot("gcc");
    },
    listCMakeTargets: async () => {
      listings++;
      return {
        workspaceGeneration: "a".repeat(64),
        projectId: "root",
        buildProfileId: "b".repeat(64),
        targets: [{ targetId: "d".repeat(64), name: "slow_target" }],
      };
    },
    startCMakeBuild: async () => {
      starts++;
      throw new ProtocolError("WORKSPACE_CHANGED", "workspace generation is stale", true);
    },
  } as unknown as ProtocolClient;

  await assert.rejects(
    __testing.startNamedTargetBuildAtCheckpoint(
      client,
      "gcc",
      "timeout",
      "slow_target",
      1_000,
    ),
    (error) => error instanceof ProtocolError && error.code === "WORKSPACE_CHANGED",
  );
  assert.equal(inspections, 2);
  assert.equal(listings, 2);
  assert.equal(starts, 2);
});

test("cancellation recovery establishes a fresh default-build checkpoint", async () => {
  let inspections = 0;
  let starts = 0;
  const client = {
    inspectWorkspace: async () => {
      inspections++;
      return workspaceSnapshot("gcc");
    },
    startCMakeBuild: async () => {
      starts++;
      return { taskId: "post-cancellation-recovery" };
    },
  } as unknown as ProtocolClient;
  const events = [{
    taskId: "post-cancellation-recovery",
    event: "task.finished",
    payload: { outcome: "succeeded" },
  }];
  const subscription = {
    lastSequence: 0,
    next: async () => ({ done: false, value: events.shift()! }),
  } as unknown as EventSubscription;

  await __testing.recoverAfterCancellation(client, "gcc", subscription);

  assert.equal(inspections, 1);
  assert.equal(starts, 1);
  assert.equal(events.length, 0);
});

test("native liveness recovery retries a transient reconnect failure", async () => {
  let reconnects = 0;
  const client = {
    reconnect: async () => {
      reconnects++;
      if (reconnects === 1) {
        throw new Error("transient reconnect failure");
      }
    },
  } as unknown as ProtocolClient;

  await __testing.recoverNativeLiveness(client, "native-task", 16);
  assert.equal(reconnects, 2);
});

test("diagnostic fixture refreshes one stale generation before Task creation", async () => {
  const requests: Array<{ idempotencyKey: string; workspaceGeneration: string }> = [];
  let inspections = 0;
  const refreshed = workspaceSnapshot("gcc");
  refreshed.workspaceGeneration = "b".repeat(64);
  const client = {
    inspectWorkspace: async () => {
      inspections++;
      return refreshed;
    },
    startCMakeBuild: async (
      request: { idempotencyKey: string; workspaceGeneration: string },
    ) => {
      requests.push(request);
      if (requests.length === 1) {
        throw new ProtocolError("WORKSPACE_CHANGED", "workspace generation is stale", true);
      }
      return { taskId: "task-after-refresh" };
    },
  } as unknown as ProtocolClient;

  const selected = __testing.selectGeneratedProfile(workspaceSnapshot("gcc"), "gcc");
  assert.ok(selected);
  const task = await __testing.startFailureBuildWithStaleRetry(
    client,
    selected,
    "gcc",
    "linker-failure",
  );

  assert.equal(task.taskId, "task-after-refresh");
  assert.equal(inspections, 1);
  assert.equal(requests.length, 2);
  assert.equal(requests[0]?.workspaceGeneration, "a".repeat(64));
  assert.equal(requests[1]?.workspaceGeneration, "b".repeat(64));
  assert.notEqual(requests[0]?.idempotencyKey, requests[1]?.idempotencyKey);
});

test("diagnostic fixture stale-generation retry is bounded to one", async () => {
  let starts = 0;
  let inspections = 0;
  const client = {
    inspectWorkspace: async () => {
      inspections++;
      return workspaceSnapshot("gcc");
    },
    startCMakeBuild: async () => {
      starts++;
      throw new ProtocolError("WORKSPACE_CHANGED", "workspace generation is stale", true);
    },
  } as unknown as ProtocolClient;
  const selected = __testing.selectGeneratedProfile(workspaceSnapshot("gcc"), "gcc");
  assert.ok(selected);

  await assert.rejects(
    __testing.startFailureBuildWithStaleRetry(
      client,
      selected,
      "gcc",
      "linker-failure",
    ),
    (error) => error instanceof ProtocolError && error.code === "WORKSPACE_CHANGED",
  );
  assert.equal(starts, 2);
  assert.equal(inspections, 1);
});

test("diagnostic fixture retries one transient stale inspection before Task creation", async () => {
  let starts = 0;
  let inspections = 0;
  const refreshed = workspaceSnapshot("gcc");
  refreshed.workspaceGeneration = "b".repeat(64);
  const client = {
    inspectWorkspace: async () => {
      inspections++;
      if (inspections === 1) {
        throw new ProtocolError("WORKSPACE_CHANGED", "workspace generation is stale", true);
      }
      return refreshed;
    },
    startCMakeBuild: async () => {
      starts++;
      if (starts === 1) {
        throw new ProtocolError("WORKSPACE_CHANGED", "workspace generation is stale", true);
      }
      return { taskId: "task-after-inspection-refresh" };
    },
  } as unknown as ProtocolClient;
  const selected = __testing.selectGeneratedProfile(workspaceSnapshot("gcc"), "gcc");
  assert.ok(selected);

  const task = await __testing.startFailureBuildWithStaleRetry(
    client,
    selected,
    "gcc",
    "linker-failure",
  );

  assert.equal(task.taskId, "task-after-inspection-refresh");
  assert.equal(starts, 2);
  assert.equal(inspections, 2);
});

test("native report contains stable summaries and rejects absolute tool paths", () => {
  const bundle = fakePreparedBundle("/bundle");
  const result = {
    platform: "linux" as const,
    toolchainFamily: "gcc" as const,
    toolchainVersion: "15.1.0",
    generator: "Ninja",
    cmakeVersion: "4.3.4",
    scenarios: { "default-build": "passed" as const },
  };
  const report = reportTesting.buildReport("linux", "x64", bundle, [result]);
  assert.deepEqual(report.cmake, {
    version: "4.3.4",
    archiveSha256: "a".repeat(64),
  });
  assert.equal(JSON.stringify(report).includes("/bundle"), false);
  assert.throws(
    () => reportTesting.buildReport("linux", "x64", bundle, [{
      ...result,
      generator: process.platform === "win32" ? "C:\\LLVM\\bin\\ninja.exe" : "/usr/bin/ninja",
    }]),
    /invalid native scenario report/,
  );
});

async function createPreparedBundleFixture() {
  const container = await mkdtemp(join(tmpdir(), "native-bundle-valid-"));
  const bundleRoot = join(container, "cmake");
  const manifestBytes = await readFile(trackedManifestPath);
  const manifest = JSON.parse(manifestBytes.toString("utf8")) as {
    cmakeVersion: string;
    archives: Record<string, {
      archiveSha256: string;
      rootDirectory: string;
      executable: string;
      installedFiles: Record<string, string>;
    }>;
  };
  const archive = manifest.archives["linux-x64"]!;
  const platformRoot = join(bundleRoot, manifest.cmakeVersion, "linux-x64");
  const installRoot = join(platformRoot, archive.rootDirectory);
  await mkdir(installRoot, { recursive: true });
  await writeFile(join(bundleRoot, "manifest.json"), manifestBytes);
  for (const relativePath of Object.keys(archive.installedFiles)) {
    const path = join(installRoot, ...relativePath.split("/"));
    await mkdir(dirname(path), { recursive: true });
    await writeFile(path, relativePath);
  }
  const statePath = join(platformRoot, "bundle-state.json");
  await writeFile(statePath, `${JSON.stringify({
    schemaVersion: 1,
    key: "linux-x64",
    cmakeVersion: manifest.cmakeVersion,
    archiveSha256: archive.archiveSha256,
    installedFiles: archive.installedFiles,
  }, null, 2)}\n`);
  const executable = join(installRoot, ...archive.executable.split("/"));
  return {
    container,
    bundleRoot,
    statePath,
    executable,
    operations: {
      manifestPath: trackedManifestPath,
      sha256File: async (path: string) => {
        const relativePath = Object.keys(archive.installedFiles).find((candidate) =>
          path.endsWith(join(...candidate.split("/")))
        );
        assert.ok(relativePath);
        return archive.installedFiles[relativePath]!;
      },
      readCapabilities: async () => ({ version: { string: manifest.cmakeVersion } }),
    },
  };
}

const frameworkIdentity: F1FrameworkIdentity = {
  manifestSha256: "2ed58a385314968813598dd0ca89cbdc491e7c1eda87d58a676efb61a5af5fa0",
  frameworkTreeSha256: {
    cpputest: "c564fb5e4e32836dc66f46efb86edb6f1f2fa6afa255a57052031aa00fc56f04",
    unity: "abfb7b2b7aec36739a7b138490d2e9dd178cc4f00e806ed372cbb8cfe98f73ae",
    cmock: "19e013d70a3f032decb2e836b875a997d085ca72b34c92c91b528e6ad2e49ac3",
  },
  cMockProvenanceSha256: "038b46e53f833097d6311564c3d647c418ba254e1b129ada161ab40f3b0d961a",
  fixtures: {
    cpputest: {
      metadataSha256: "6eef50ec7940e4a6b80891d0ff452ed503b5996de313df711ecad607185761ee",
      sourceSha256: "114b3d7c6aadcc487b2df01a917c4c0702ba1fdb381456b12c406839181ac5f2",
      executableSha256: "fed16783995c7fe860a931f8d783e50b1bac04c64bd98843fa61613b41a821b3",
    },
    unity: {
      metadataSha256: "1287993f09fb2d8079933ac89a85bd464122edac7b3c92621692a02484536333",
      sourceSha256: "cfec3aea0fec4f1c6a84a4e835edb060415266deb156893d1c10de5ddb0fae15",
      executableSha256: "c094e9ebbf45592d8b087410f6905844d82132bec626dbc549f1db75a9d5e44e",
    },
  },
};

function requiredLinuxFrameworkPlatform(artifactDirectory: string): FrameworkPlatformOptions {
  return {
    artifactDirectory,
    platform: "linux",
    toolchains: (["gcc", "clang"] as const).map((family) => ({
      family,
      compilerVersion: "15.1.0",
      compilerSha256: createHash("sha256").update(`compiler:${family}`).digest("hex"),
      frameworks: (["cpputest", "unity"] as const).map((frameworkId) => ({
        frameworkId,
        dependencyTreeSha256: frameworkIdentity.frameworkTreeSha256[frameworkId],
        stableIdDigest: createHash("sha256").update(`stable:${frameworkId}`).digest("hex"),
        evidence: {
          sourceArtifactSha256: frameworkIdentity.fixtures[frameworkId].sourceSha256,
          executableArtifactSha256: createHash("sha256").update(`${family}:${frameworkId}`).digest("hex"),
        },
        ...(frameworkId === "unity" ? { cMockProvenance: { manifestSha256: frameworkIdentity.cMockProvenanceSha256 } } : {}),
      })),
    })),
  } as unknown as FrameworkPlatformOptions;
}

function fakePreparedBundle(bundleRoot: string): PreparedCMakeBundle {
  return {
    bundleRoot,
    installRoot: join(bundleRoot, "4.3.4", "linux-x64", "cmake-4.3.4-linux-x86_64"),
    executable: join(bundleRoot, "cmake"),
    key: "linux-x64",
    cmakeVersion: "4.3.4",
    archiveSha256: "a".repeat(64),
  };
}

function workspaceSnapshot(family?: "gcc" | "clang"): WorkspaceSnapshot {
  if (family === undefined) {
    return {
      workspaceUri: "file:///workspace",
      workspaceGeneration: "a".repeat(64),
      capabilities: { workspaceInspect: true, targetList: true, cmakeBuild: true },
      diagnostics: [],
      toolchains: [],
      projects: [{
        projectId: "root",
        sourceUri: "file:///workspace",
        buildProfiles: [],
      }],
    };
  }
  const toolchainId = `${family}-test`;
  return {
    workspaceUri: "file:///workspace",
    workspaceGeneration: "a".repeat(64),
    capabilities: { workspaceInspect: true, targetList: true, cmakeBuild: true },
    diagnostics: [],
    toolchains: [{
      toolchainId,
      family,
      version: "15.1.0",
      targetTriple: "x86_64-linux-gnu",
      hostArchitecture: "x64",
      targetArchitecture: "x64",
      generators: ["Ninja"],
      capabilities: { coverageDrivers: family === "gcc" ? ["gcov"] : ["llvm-cov"] },
    }],
    projects: [{
      projectId: "root",
      sourceUri: "file:///workspace",
      buildProfiles: [{
        buildProfileId: "b".repeat(64),
        name: "Debug",
        origin: "generated",
        toolchainId,
        generator: "Ninja",
        configuration: "Debug",
      }],
    }],
  } as WorkspaceSnapshot;
}
