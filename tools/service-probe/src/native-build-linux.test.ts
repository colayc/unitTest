import assert from "node:assert/strict";
import { mkdtemp, mkdir, readFile, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { dirname, join, resolve } from "node:path";
import test from "node:test";
import type { WorkspaceSnapshot } from "@unit-test-ide/protocol-models";
import { ProtocolError, type ProtocolClient } from "@unit-test-ide/test-client";
import type { TaskServiceFixture } from "./probe.js";
import {
  __testing,
  parseRequiredToolchains,
  verifyPreparedCMakeBundle,
  type NativeMatrixOptions,
  type PreparedCMakeBundle,
} from "./native-build.js";
import { __testing as reportTesting } from "./native-report.js";

const trackedManifestPath = resolve(import.meta.dirname, "../../../tools/cmake-bundle/manifest.json");

test("required native toolchain parsing is closed and deterministic", () => {
  assert.deepEqual([...parseRequiredToolchains(undefined)], []);
  assert.deepEqual([...parseRequiredToolchains("gcc, clang")], ["gcc", "clang"]);
  assert.throws(() => parseRequiredToolchains("gcc,gcc"), /duplicate required/);
  assert.throws(() => parseRequiredToolchains("gcc,cuda"), /invalid required/);
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
