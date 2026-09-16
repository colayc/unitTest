import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { mkdir, mkdtemp, readFile, readdir, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";
import {
  FRAMEWORK_SCENARIO_IDS,
  type FrameworkBenchmarkEvidence,
  type FrameworkId,
  type FrameworkToolchainFamily,
} from "./native-framework-report.js";
import {
  runFrameworkMatrix,
  runFrameworkPlatform,
  type FrameworkMatrixOptions,
  type FrameworkPlatformOptions,
} from "./native-framework-matrix.js";
import {
  __testing as nativeBuildTesting,
  type NativeMatrixOptions,
  type PreparedCMakeBundle,
} from "./native-build.js";
import type { TaskServiceFixture } from "./probe.js";

const candidateCommit = "1".repeat(40);
const catalogRevision = digest("catalog");
const evidence = Object.freeze({
  sourceArtifactSha256: digest("source"),
  sourceLocationDigest: digest("locations"),
  executableArtifactSha256: digest("executable"),
});

const catalogItems = [
  ["pass", "Pass"],
  ["assertion", "AssertionFailure"],
  ["crash", "Crash"],
  ["malformed", "MalformedOutput"],
  ["mock", "MockMissingCall"],
  ["skip", "Skipped"],
  ["timeout", "Timeout"],
].map(([suffix, logicalName]) => ({
  id: `utid-v1-${digest(suffix!)}`,
  containerId: `utid-v1-${digest("container")}`,
  disabled: logicalName === "Skipped",
  displayName: logicalName,
  framework: "cpputest",
  kind: "case",
  labels: [],
  logicalName,
}));

const scenarioRunOrder = FRAMEWORK_SCENARIO_IDS.filter((id) => id !== "discovery");
const scenarioOutcomes = new Map([
  ["all", "failed"],
  ["assertion-failure", "failed"],
  ["cancel", "cancelled"],
  ["crash", "errored"],
  ["failed-rerun", "failed"],
  ["filter", "passed"],
  ["malformed-output", "errored"],
  ["mock-failure", "failed"],
  ["opaque-fallback", "passed"],
  ["reconnect-replay", "passed"],
  ["repeat", "passed"],
  ["service-restart", "interrupted"],
  ["single", "passed"],
  ["skip", "passed"],
  ["stale-catalog", "rejected"],
  ["timeout", "timed_out"],
] as const);

interface ProtocolCall {
  readonly method: string;
  readonly value?: unknown;
}

class FakeProtocolClient {
  readonly calls: ProtocolCall[] = [];
  readonly runRequests: Array<Record<string, unknown>> = [];
  readonly #tasks = new Map<string, Record<string, unknown>>();
  readonly #runs = new Map<string, Record<string, unknown>>();
  #runIndex = 0;
  reconnectPromise: Promise<void> | undefined;
  cancelPromise: Promise<Record<string, unknown>> | undefined;
  readonly #frameworkId: FrameworkId;
  readonly #family: FrameworkToolchainFamily;
  readonly #onInspect?: () => void;

  constructor(
    frameworkId: FrameworkId = "cpputest",
    family: FrameworkToolchainFamily = "clang",
    onInspect?: () => void,
  ) {
    this.#frameworkId = frameworkId;
    this.#family = family;
    this.#onInspect = onInspect;
  }

  async inspectWorkspace() {
    this.#onInspect?.();
    this.calls.push({ method: "inspectWorkspace" });
    return {
      capabilities: { cmakeBuild: true, targetList: true, workspaceInspect: true },
      diagnostics: [],
      projects: [{
        projectId: "root",
        sourceUri: "file:///workspace",
        buildProfiles: [{
          buildProfileId: "profile",
          generator: "Ninja",
          name: "Debug",
          origin: "generated",
          toolchainId: "toolchain",
        }],
      }],
      toolchains: [{
        capabilities: { coverageDrivers: [] },
        family: this.#family,
        generators: ["Ninja"],
        hostArchitecture: "x64",
        targetArchitecture: "x64",
        targetTriple: "x86_64-test",
        toolchainId: "toolchain",
        version: "18.1.0",
      }],
      workspaceGeneration: digest("workspace"),
      workspaceUri: "file:///workspace",
    };
  }

  async discoverTests(value: Record<string, unknown>) {
    this.calls.push({ method: "discoverTests", value });
    const task = taskSnapshot("discovery-task", "succeeded");
    this.#tasks.set("discovery-task", task);
    return task;
  }

  async getTestCatalog(value: Record<string, unknown>) {
    this.calls.push({ method: "getTestCatalog", value });
    return {
      containers: [{
        capabilities: {
          canDiscoverCases: true,
          canReportMockDetails: true,
          canReportSkipped: true,
          canReportSourceLocation: true,
          canRunCase: true,
        },
        ctestLogicalName: "framework-tests",
        disabled: false,
        displayName: "framework-tests",
        framework: this.#frameworkId,
        id: `utid-v1-${digest("container")}`,
        labels: [],
        projectId: "root",
      }, {
        capabilities: {
          canDiscoverCases: false,
          canReportMockDetails: false,
          canReportSkipped: false,
          canReportSourceLocation: false,
          canRunCase: false,
        },
        ctestLogicalName: "opaque-framework-tests",
        degradedReason: "adapter-contract-invalid",
        disabled: false,
        displayName: "opaque-framework-tests",
        framework: "opaque-ctest",
        id: `utid-v1-${digest("opaque-container")}`,
        labels: [],
        projectId: "root",
      }],
      diagnostics: [],
      generatedAt: new Date("2026-09-16T00:00:00.000Z"),
      items: catalogItems,
      partial: false,
      profileId: "profile",
      projectId: "root",
      revision: catalogRevision,
    };
  }

  async runTests(value: Record<string, unknown>) {
    const scenario = scenarioRunOrder[this.#runIndex++];
    assert.ok(scenario, "runner started more than the contracted scenario set");
    this.calls.push({ method: "runTests", value: { scenario, ...value } });
    this.runRequests.push(value);
    if (scenario === "stale-catalog") {
      const error = new Error("catalog stale");
      Object.assign(error, { code: "CATALOG_STALE" });
      throw error;
    }
    const taskId = `task-${scenario}`;
    const runId = `run-${scenario}`;
    const outcome = scenarioOutcomes.get(scenario)!;
    const task = {
      ...taskSnapshot(taskId, outcome === "interrupted" ? "interrupted" : outcome === "timed_out" ? "timed_out" : outcome === "cancelled" ? "cancelled" : "succeeded"),
      kind: "testRun",
      projectId: "root",
      profileId: "profile",
      catalogRevision,
      runId,
      repeatCount: value.repeatCount,
    };
    const skipped = scenario === "skip" ? 1 : 0;
    this.#tasks.set(taskId, task);
    this.#runs.set(runId, {
      catalogRevision,
      incomplete: false,
      outcome,
      profileId: "profile",
      projectId: "root",
      resultRevision: digest(`result:${scenario}`),
      runId,
      selectionSnapshot: value.selection,
      startedAt: new Date("2026-09-16T00:00:00.000Z"),
      finishedAt: new Date("2026-09-16T00:00:01.000Z"),
      status: "completed",
      summary: {
        cancelled: outcome === "cancelled" ? 1 : 0,
        completed: 1,
        errored: outcome === "errored" ? 1 : 0,
        failed: outcome === "failed" ? 1 : 0,
        iterations: value.repeatCount,
        notRun: 0,
        passed: outcome === "passed" && skipped === 0 ? 1 : 0,
        skipped,
        timedOut: outcome === "timed_out" ? 1 : 0,
        total: 1,
      },
      taskId,
      toolchainId: "toolchain",
    });
    return task;
  }

  async getTask(taskId: string) {
    this.calls.push({ method: "getTask", value: taskId });
    const task = this.#tasks.get(taskId);
    if (!task) throw new Error(`unknown task ${taskId}`);
    return task;
  }

  async getTestRun(runId: string) {
    this.calls.push({ method: "getTestRun", value: runId });
    const run = this.#runs.get(runId);
    if (!run) throw new Error(`unknown run ${runId}`);
    return run;
  }

  async cancelTask(taskId: string) {
    this.calls.push({ method: "cancelTask", value: taskId });
    return this.cancelPromise ?? this.getTask(taskId);
  }

  async reconnect() {
    this.calls.push({ method: "reconnect" });
    return this.reconnectPromise ?? Promise.resolve();
  }
}

class FakeFixture {
  readonly client: FakeProtocolClient;
  readonly calls: string[] = [];
  killPromise: Promise<void> | undefined;
  restartPromise: Promise<this> | undefined;

  constructor(
    frameworkId: FrameworkId = "cpputest",
    family: FrameworkToolchainFamily = "clang",
    onInspect?: () => void,
  ) {
    this.client = new FakeProtocolClient(frameworkId, family, onInspect);
  }

  async kill(): Promise<void> {
    this.calls.push("kill");
    return this.killPromise ?? Promise.resolve();
  }

  async restart(): Promise<this> {
    this.calls.push("restart");
    return this.restartPromise ?? Promise.resolve(this);
  }
}

test("runner emits the exact 17 scenarios and derives every selection from the catalog", async () => {
  const fixture = new FakeFixture();
  const result = await runFrameworkMatrix(matrixOptions(fixture));

  assert.deepEqual(result.scenarios.map(({ id }) => id), FRAMEWORK_SCENARIO_IDS);
  assert.deepEqual(
    result.scenarios.map(({ observedOutcome, classification }) => [observedOutcome, classification]),
    [
      ["failed", "aggregate"], ["failed", "assertion"], ["cancelled", "cancelled"],
      ["errored", "crash"], ["passed", "discovery"], ["failed", "assertion"],
      ["passed", "selection"], ["errored", "malformed-output"],
      ["failed", "mock-expectation"], ["passed", "opaque-fallback"],
      ["passed", "replay"], ["passed", "repeat"], ["interrupted", "service-restarted"],
      ["passed", "test"], ["skipped", "ignored"], ["rejected", "stale-catalog"],
      ["timed-out", "timeout"],
    ],
  );
  const itemIds = new Set(catalogItems.map(({ id }) => id));
  const containerIds = new Set([
    `utid-v1-${digest("container")}`,
    `utid-v1-${digest("opaque-container")}`,
  ]);
  const priorRunIds = new Set<string>();
  for (const request of fixture.client.runRequests) {
    assert.equal(request.projectId, "root");
    assert.equal(request.profileId, "profile");
    const selection = request.selection as Record<string, unknown>;
    if (selection.mode === "items") {
      assert.ok((selection.itemIds as string[]).every((id) => itemIds.has(id)));
    } else if (selection.mode === "containers") {
      assert.ok((selection.containerIds as string[]).every((id) => containerIds.has(id)));
    } else if (selection.mode === "filter") {
      const filter = selection.filter as { includeItemIds?: string[]; excludeItemIds?: string[] };
      assert.ok([...(filter.includeItemIds ?? []), ...(filter.excludeItemIds ?? [])].every((id) => itemIds.has(id)));
    } else if (selection.mode === "failedFromRun") {
      assert.ok(priorRunIds.has(selection.runId as string), "failed rerun must reference a completed catalog-selected run");
    } else {
      assert.equal(selection.mode, "all");
    }
    const runId = fixture.client.calls.findLast((call) =>
      call.method === "runTests" && (call.value as { idempotencyKey?: unknown }).idempotencyKey === request.idempotencyKey
    )?.value;
    if (runId) {
      const scenario = (runId as { scenario: string }).scenario;
      if (scenario !== "stale-catalog") priorRunIds.add(`run-${scenario}`);
    }
  }
  assert.equal(fixture.client.calls.filter(({ method }) => method === "inspectWorkspace").length >= 1, true);
  assert.equal(fixture.client.calls.filter(({ method }) => method === "discoverTests").length, 1);
  assert.equal(fixture.client.calls.filter(({ method }) => method === "cancelTask").length, 1);
  assert.equal(fixture.client.calls.filter(({ method }) => method === "reconnect").length, 1);
  const opaque = fixture.client.calls.find((call) =>
    call.method === "runTests" && (call.value as { scenario?: unknown }).scenario === "opaque-fallback"
  )?.value as { selection?: unknown } | undefined;
  assert.deepEqual(opaque?.selection, {
    mode: "containers",
    containerIds: [`utid-v1-${digest("opaque-container")}`],
  });
  assert.deepEqual(fixture.calls, ["kill", "restart"]);
});

for (const [operation, prepare, pattern] of [
  ["cancellation", (fixture: FakeFixture) => { fixture.client.cancelPromise = new Promise(() => undefined); }, /cancel.*timed out/iu],
  ["reconnect", (fixture: FakeFixture) => { fixture.client.reconnectPromise = new Promise(() => undefined); }, /reconnect.*timed out/iu],
  ["service restart", (fixture: FakeFixture) => { fixture.killPromise = new Promise(() => undefined); }, /service.*(?:stop|kill|restart).*timed out/iu],
] as const) {
  test(`${operation} is bounded by the runner deadline`, async () => {
    const fixture = new FakeFixture();
    prepare(fixture);
    const started = Date.now();
    await assert.rejects(runFrameworkMatrix(matrixOptions(fixture, 5)), pattern);
    assert.ok(Date.now() - started < 500, `${operation} exceeded the bounded test allowance`);
  });
}

test("runner rejects arbitrary execution controls before contacting the Service", async () => {
  for (const key of ["command", "args", "shell", "environment", "cwd", "workingDirectory", "hook"]) {
    const fixture = new FakeFixture();
    await assert.rejects(
      runFrameworkMatrix({ ...matrixOptions(fixture), [key]: key === "args" ? ["--unsafe"] : "unsafe" } as never),
      /unexpected|unsupported|execution control/iu,
    );
    assert.deepEqual(fixture.client.calls, []);
  }
});

test("platform runner writes one validated report atomically after both frameworks", async () => {
  const root = await mkdtemp(join(tmpdir(), "framework-platform-"));
  const artifactDirectory = join(root, ".native-e2e", "artifacts", "linux");
  try {
    const options = platformOptions(artifactDirectory);
    const report = await runFrameworkPlatform(options);
    assert.deepEqual(report.toolchains.map(({ family }) => family), ["clang", "gcc"]);
    assert.ok(report.toolchains.every(({ frameworks }) =>
      frameworks.map(({ id }) => id).join(",") === "cpputest,unity" &&
      frameworks.every(({ scenarios }) => scenarios.length === 17)
    ));
    assert.deepEqual(JSON.parse(await readFile(join(artifactDirectory, "framework-report.json"), "utf8")), report);
    assert.deepEqual(await readdir(artifactDirectory), ["framework-report.json"]);
  } finally {
    await rm(root, { recursive: true, force: true });
  }
});

test("native integration runs each framework toolchain after launch and publishes after cleanup", async () => {
  const root = await mkdtemp(join(tmpdir(), "framework-native-integration-"));
  const artifactDirectory = join(root, ".native-e2e", "artifacts", "linux");
  const serviceBinary = join(root, "build", "unit-test-service");
  const events: string[] = [];
  try {
    await mkdir(join(root, "build"), { recursive: true });
    await writeFile(serviceBinary, "fixture");
    const frameworkPlatform = platformOptions(artifactDirectory, events);
    let launchIndex = 0;
    const dependencies: Parameters<typeof nativeBuildTesting.runNativeMatrixWithDependencies>[1] = {
      environment: {},
      architecture: "x64",
      repositoryRoot: root,
      verifyBundle: async () => fakePreparedBundle(join(root, ".bundled-tools", "cmake")),
      createWorkspace: async (_root, _platform, family) => {
        const workspaceRoot = join(root, "work", family, "workspace");
        const serviceDirectory = join(root, "work", family, "service");
        await mkdir(workspaceRoot, { recursive: true });
        await mkdir(serviceDirectory, { recursive: true });
        return { root: join(root, "work", family), workspaceRoot, serviceDirectory };
      },
      launchService: async () => {
        const family = (["gcc", "clang"] as const)[launchIndex++]!;
        events.push(`launch:${family}`);
        return {
          client: new FakeProtocolClient("cpputest", family),
          dispose: async () => { events.push(`dispose:${family}`); },
        } as unknown as TaskServiceFixture;
      },
      executeScenarios: async ({ family }) => {
        events.push(`core:${family}`);
        return { "default-build": "passed" };
      },
      cleanupWorkspace: async ({ root: workspaceRoot }) => {
        events.push(`cleanup:${workspaceRoot.includes("gcc") ? "gcc" : "clang"}`);
      },
      writeReport: async () => {
        await assert.rejects(readFile(join(artifactDirectory, "framework-report.json")));
        events.push("native-report");
        return join(artifactDirectory, "toolchain-report.json");
      },
    };
    const options: NativeMatrixOptions = {
      platform: "linux",
      requiredFamilies: ["gcc", "clang"],
      artifactDirectory,
      frameworkPlatform,
    };
    await nativeBuildTesting.runNativeMatrixWithDependencies(options, dependencies);
    assert.deepEqual(events, [
      "launch:gcc", "framework:gcc:cpputest", "framework:gcc:unity", "core:gcc", "dispose:gcc", "cleanup:gcc",
      "launch:clang", "framework:clang:cpputest", "framework:clang:unity", "core:clang", "dispose:clang", "cleanup:clang",
      "native-report",
    ]);
    assert.equal(JSON.parse(await readFile(join(artifactDirectory, "framework-report.json"), "utf8")).publication, "atomic-after-cleanup");
  } finally {
    await rm(root, { recursive: true, force: true });
  }
});

function matrixOptions(fixture: FakeFixture, timeoutMs = 100): FrameworkMatrixOptions {
  return {
    candidateCommit,
    evidence,
    fixture: fixture as never,
    frameworkId: "cpputest",
    platform: "linux",
    timeoutMs,
    toolchainFamily: "clang",
    now: monotonicClock(),
  };
}

function platformOptions(artifactDirectory: string, events?: string[]): FrameworkPlatformOptions {
  const makeFramework = (family: FrameworkToolchainFamily, id: FrameworkId) => {
    const fixture = new FakeFixture(id, family, () => events?.push(`framework:${family}:${id}`));
    return {
      ...matrixOptions(fixture),
      evidence: {
        sourceArtifactSha256: digest(`source:${id}`),
        sourceLocationDigest: digest(`locations:${id}`),
        executableArtifactSha256: digest(`executable:${family}:${id}`),
      },
      frameworkId: id,
      toolchainFamily: family,
      catalogArtifactSha256: digest(`catalog-artifact:${family}:${id}`),
      stableIdDigest: digest(`stable:${id}`),
      dependencyVersion: id === "cpputest" ? "4.0" : "2.6.1",
      dependencySha256: id === "cpputest"
        ? "21c692105db15299b5529af81a11a7ad80397f92c122bd7bf1e4a4b0e85654f7"
        : "b41a66d45a6b99758fb3202ace6178177014d52fc524bf1f72687d93e9867292",
      dependencyTreeSha256: id === "cpputest"
        ? "c564fb5e4e32836dc66f46efb86edb6f1f2fa6afa255a57052031aa00fc56f04"
        : "abfb7b2b7aec36739a7b138490d2e9dd178cc4f00e806ed372cbb8cfe98f73ae",
      ...(id === "unity" ? {
        cMockProvenance: {
          revision: "6ea503340b1d3fdc0f2bcaf69273ba0160ec83af",
          generatorVersion: "2.7.0",
          inputSha256: "007f23aea2dba06d111f66be95905adde8fe32e7d2031bf8a8c70117a8209f57",
          outputSha256: "1565d1a2d39b655eb551a729663fae7e167f1c0d6cd3f9a8c2aafe2d67348128",
          manifestSha256: "4f0a73e5decc2402930fc4d609d1640150fe6addb30e20cc9900e1cf418520a8",
          generatedAtRuntime: false as const,
        },
      } : {}),
    };
  };
  const benchmark: Omit<FrameworkBenchmarkEvidence, "startedAt" | "finishedAt"> = {
    id: "catalog-10000",
    itemCount: 10000,
    sampleCount: 3,
    allocationBudgetPerOperation: 300000,
    allocationsPerOperation: [100, 100, 100],
    catalogRevision: digest("benchmark-catalog"),
    catalogArtifactSha256: digest("benchmark-artifact"),
    stableIdDigest: digest("benchmark-stable"),
    status: "passed",
  };
  return {
    artifactDirectory,
    benchmark,
    candidateCommit,
    now: monotonicClock(),
    platform: "linux",
    toolchains: (["gcc", "clang"] as const).map((family) => ({
      compilerSha256: digest(`compiler:${family}`),
      compilerVersion: family === "gcc" ? "15.2.0" : "18.1.0",
      family,
      frameworks: [makeFramework(family, "cpputest"), makeFramework(family, "unity")],
    })),
  };
}

function fakePreparedBundle(bundleRoot: string): PreparedCMakeBundle {
  return {
    bundleRoot,
    installRoot: join(bundleRoot, "install"),
    executable: join(bundleRoot, "install", "bin", "cmake"),
    key: "linux-x64",
    cmakeVersion: "4.3.4",
    archiveSha256: digest("cmake-archive"),
  };
}

function taskSnapshot(taskId: string, outcome: string): Record<string, unknown> {
  return {
    taskId,
    kind: "testDiscovery",
    status: "finished",
    outcome,
    createdAt: new Date("2026-09-16T00:00:00.000Z"),
    startedAt: new Date("2026-09-16T00:00:00.000Z"),
    finishedAt: new Date("2026-09-16T00:00:01.000Z"),
    lastSequence: 1,
    projectId: "root",
    profileId: "profile",
    catalogRevision,
  };
}

function monotonicClock(): () => Date {
  let tick = 0;
  return () => new Date(Date.UTC(2026, 8, 16, 0, 0, tick++));
}

function digest(value: string): string {
  return createHash("sha256").update(value).digest("hex");
}
