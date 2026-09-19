import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { createHash } from "node:crypto";
import { cp, mkdir, mkdtemp, readFile, readdir, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import test from "node:test";
import {
  FRAMEWORK_SCENARIO_IDS,
  type FrameworkBenchmarkEvidence,
  type FrameworkId,
  type FrameworkToolchainFamily,
} from "./native-framework-report.js";
import {
  discoverFrameworkCatalog,
  loadMatrixContract,
  publishFrameworkPlatformReport,
  runFrameworkMatrix,
  runFrameworkPlatform,
  runFrameworkToolchain,
  stableFrameworkIdDigest,
  type F1FrameworkIdentity,
  type FrameworkMatrixOptions,
  type FrameworkPlatformOptions,
} from "./native-framework-matrix.js";
import {
  frameworkMatrixRequired,
  loadRequiredFrameworkRuntime,
} from "./native-framework-runtime.js";
import { main as runNativeMain } from "./native-run.js";
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

const f1Identity: F1FrameworkIdentity = Object.freeze({
  manifestSha256: "6ac0e8fd1393c0d84d882445d7ab49ca2cec6b87ee9f1e4b44cfd828125a9441",
  frameworkTreeSha256: Object.freeze({
    cpputest: "c564fb5e4e32836dc66f46efb86edb6f1f2fa6afa255a57052031aa00fc56f04",
    unity: "abfb7b2b7aec36739a7b138490d2e9dd178cc4f00e806ed372cbb8cfe98f73ae",
    cmock: "19e013d70a3f032decb2e836b875a997d085ca72b34c92c91b528e6ad2e49ac3",
  }),
  cMockProvenanceSha256: "e039f3f6ed52900ef70ddd8f3892bbd695bf97747903a860e2d919f124358bb6",
  fixtures: Object.freeze({
    cpputest: Object.freeze({ metadataSha256: "6eef50ec7940e4a6b80891d0ff452ed503b5996de313df711ecad607185761ee", sourceSha256: "114b3d7c6aadcc487b2df01a917c4c0702ba1fdb381456b12c406839181ac5f2", executableSha256: digest("cpp-executable") }),
    unity: Object.freeze({ metadataSha256: "1287993f09fb2d8079933ac89a85bd464122edac7b3c92621692a02484536333", sourceSha256: "cfec3aea0fec4f1c6a84a4e835edb060415266deb156893d1c10de5ddb0fae15", executableSha256: digest("unity-executable") }),
  }),
});

const frameworkNames = {
  cpputest: ["Pass", "AssertionFailure", "Crash", "MockMissingCall", "Skipped", "Timeout"],
  unity: ["test_pass", "test_assertion_failure", "test_crash", "test_cmock_expectation_failure", "test_skipped", "test_timeout"],
} as const;

function stableDigestCatalog() {
  const cppContainer = `utid-v1-${digest("cpp-container")}`;
  const unityContainer = `utid-v1-${digest("unity-container")}`;
  const cppSuite = `utid-v1-${digest("cpp-suite")}`;
  return {
    containers: [
      {
        capabilities: {
          canDiscoverCases: true, canReportMockDetails: true, canReportSkipped: true,
          canReportSourceLocation: true, canRunCase: true,
        },
        ctestLogicalName: "cpputest.framework",
        disabled: false,
        displayName: "CppUTest framework",
        framework: "cpputest",
        id: cppContainer,
        labels: ["smoke", "Framework"],
        projectId: "project-win32-msvc",
        sourceLocation: {
          line: 1, navigable: true, provenance: "framework-manifest",
          uri: "file:///C:/agent/build/testdata/frameworks/cpputest/CMakeLists.txt",
        },
      },
      {
        capabilities: {
          canDiscoverCases: true, canReportMockDetails: true, canReportSkipped: true,
          canReportSourceLocation: true, canRunCase: true,
        },
        ctestLogicalName: "unity.framework",
        disabled: false,
        displayName: "Unity framework",
        framework: "unity",
        id: unityContainer,
        labels: ["Framework"],
        projectId: "project-win32-msvc",
      },
    ],
    diagnostics: [],
    generatedAt: new Date("2026-09-16T01:02:03.000Z"),
    items: [
      {
        containerId: cppContainer, disabled: false, displayName: "Upper", framework: "cpputest",
        id: cppSuite, kind: "suite", labels: ["z", "A"], logicalName: "Upper",
        sourceLocation: {
          line: 10, column: 2, navigable: true, provenance: "test-declaration",
          uri: "file:///C:/agent/build/testdata/frameworks/cpputest/tests/A_test.cpp",
        },
      },
      {
        containerId: cppContainer, disabled: false, displayName: "lower", framework: "cpputest",
        id: `utid-v1-${digest("cpp-lower")}`, kind: "case", labels: ["beta", "Alpha"], logicalName: "lower",
        parameters: [{ name: "second", value: 2 }, { name: "first", value: "one" }],
        parentId: cppSuite,
        sourceLocation: {
          line: 20, navigable: true, provenance: "test-declaration",
          uri: "file:///C:/agent/build/testdata/frameworks/cpputest/tests/a_test.cpp",
        },
      },
      {
        containerId: unityContainer, disabled: false, displayName: "other", framework: "unity",
        id: `utid-v1-${digest("unity-other")}`, kind: "case", labels: [], logicalName: "other",
      },
    ],
    partial: false,
    profileId: "profile-win32-msvc",
    projectId: "project-win32-msvc",
    revision: digest("catalog-win32-msvc"),
  } as Parameters<typeof stableFrameworkIdDigest>[1];
}

test("stable framework digest ignores timestamps, array indexes, compiler, platform, and build roots", () => {
  const original = stableDigestCatalog();
  const substituted = structuredClone(original) as typeof original & { compilerVersion?: string; platform?: string };
  substituted.generatedAt = new Date("2038-01-19T03:14:07.000Z");
  substituted.projectId = "project-linux-gcc";
  substituted.profileId = "profile-linux-gcc";
  substituted.revision = digest("catalog-linux-gcc");
  substituted.compilerVersion = "gcc 99";
  substituted.platform = "linux";
  substituted.containers.reverse();
  substituted.items.reverse();
  for (const container of substituted.containers) {
    container.id = `utid-v1-${digest(`replacement:${container.ctestLogicalName}`)}`;
    container.projectId = substituted.projectId;
    container.labels.reverse();
    if (container.sourceLocation !== undefined) {
      container.sourceLocation.uri = "file:///opt/runner/out/testdata/frameworks/cpputest/CMakeLists.txt";
    }
  }
  const cppContainer = substituted.containers.find((value) => value.framework === "cpputest")!;
  const cppItems = substituted.items.filter((value) => value.framework === "cpputest");
  const suite = cppItems.find((value) => value.kind === "suite")!;
  const oldSuiteId = suite.id;
  suite.id = `utid-v1-${digest("replacement-suite")}`;
  for (const item of cppItems) {
    item.containerId = cppContainer.id;
    item.labels.reverse();
    item.parameters?.reverse();
    if (item.parentId === oldSuiteId) item.parentId = suite.id;
    if (item.sourceLocation !== undefined) {
      const basename = item.sourceLocation.uri.endsWith("A_test.cpp") ? "A_test.cpp" : "a_test.cpp";
      item.sourceLocation.uri = `/var/lib/build/testdata/frameworks/cpputest/tests/${basename}`;
    }
  }

  const expected = stableFrameworkIdDigest("cpputest", original, f1Identity);
  assert.match(expected, /^[0-9a-f]{64}$/u);
  assert.equal(stableFrameworkIdDigest("cpputest", substituted, f1Identity), expected);
});

test("stable framework digest uses code-point path ordering and detects source or provenance drift", () => {
  const original = stableDigestCatalog();
  const reordered = structuredClone(original);
  reordered.items.reverse();
  assert.equal(
    stableFrameworkIdDigest("cpputest", reordered, f1Identity),
    stableFrameworkIdDigest("cpputest", original, f1Identity),
  );

  const caseDrift = structuredClone(original);
  const upper = caseDrift.items.find((value) => value.logicalName === "Upper")!;
  upper.sourceLocation!.uri = upper.sourceLocation!.uri.replace("A_test.cpp", "a_test.cpp");
  assert.notEqual(
    stableFrameworkIdDigest("cpputest", caseDrift, f1Identity),
    stableFrameworkIdDigest("cpputest", original, f1Identity),
  );

  const provenanceDrift: F1FrameworkIdentity = {
    ...f1Identity,
    frameworkTreeSha256: { ...f1Identity.frameworkTreeSha256, cpputest: digest("drifted-tree") },
  };
  assert.notEqual(
    stableFrameworkIdDigest("cpputest", original, provenanceDrift),
    stableFrameworkIdDigest("cpputest", original, f1Identity),
  );
});

test("stable identity accepts the staged Unity adapter malformed-source location", () => {
  const original = stableDigestCatalog();
  const malformed = original.items.find(({ framework }) => framework === "unity")!;
  malformed.sourceLocation = { ...original.items[0]!.sourceLocation!, line: 7, uri: "file:///repo/testdata/framework-matrix/malformed_unity.c" };
  const staged = structuredClone(original);
  staged.items.find(({ framework }) => framework === "unity")!.sourceLocation!.uri = "file:///repo/.native-e2e/framework-work/linux/clang/unity/workspace/source/framework-matrix/malformed_unity.c";
  assert.equal(stableFrameworkIdDigest("unity", staged, f1Identity), stableFrameworkIdDigest("unity", original, f1Identity));
});

test("clean autocrlf checkout preserves raw matrix contract digests", async (t) => {
  const root = await mkdtemp(join(tmpdir(), "framework-autocrlf-"));
  t.after(() => rm(root, { recursive: true, force: true }));
  const repository = resolve(import.meta.dirname, "../../..");
  for (const path of [".gitattributes", "testdata/framework-matrix", "testdata/frameworks"]) {
    await cp(join(repository, path), join(root, path), { recursive: true });
  }
  const git = (...args: string[]) => {
    const result = spawnSync("git", args, { cwd: root, encoding: "utf8", windowsHide: true });
    assert.equal(result.status, 0, result.error?.message ?? result.stderr);
  };
  git("init", "--quiet");
  git("-c", "core.autocrlf=false", "add", ".");
  const checkout = join(root, "checkout");
  git("-c", "core.autocrlf=true", "checkout-index", "--all", `--prefix=${checkout.replaceAll("\\", "/")}/`);
  for (const frameworkId of ["cpputest", "unity"] as const) await loadMatrixContract(frameworkId, checkout);
});

function fakeCatalogItems(frameworkId: FrameworkId) {
  const primaryId = `utid-v1-${digest(`container:${frameworkId}`)}`;
  const values: Array<{
    id: string; containerId: string; disabled: boolean; displayName: string;
    framework: FrameworkId; kind: string; labels: never[]; logicalName: string;
  }> = frameworkNames[frameworkId].map((logicalName) => ({
    id: `utid-v1-${digest(`${frameworkId}:${logicalName}`)}`,
    containerId: primaryId,
    disabled: logicalName.toLowerCase().includes("skip"),
    displayName: logicalName,
    framework: frameworkId,
    kind: "case",
    labels: [],
    logicalName,
  }));
  const malformedName = frameworkId === "cpputest" ? "MalformedOutput" : "test_malformed_output";
  values.push({
    id: `utid-v1-${digest(`${frameworkId}:${malformedName}`)}`,
    containerId: `utid-v1-${digest(`malformed-container:${frameworkId}`)}`,
    disabled: false,
    displayName: malformedName,
    framework: frameworkId,
    kind: "case",
    labels: [],
    logicalName: malformedName,
  });
  return values;
}

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

interface FakeClientState {
  readonly calls: ProtocolCall[];
  readonly runRequests: Array<Record<string, unknown>>;
  readonly tasks: Map<string, Record<string, unknown>>;
  readonly runs: Map<string, Record<string, unknown>>;
  readonly artifactsByTask: Map<string, Array<Record<string, unknown>>>;
  readonly artifactBytes: Map<string, Uint8Array>;
  runIndex: number;
  compilerIdentity?: { version: string; compilerSha256: string };
}

class FakeProtocolClient {
  readonly calls: ProtocolCall[];
  readonly runRequests: Array<Record<string, unknown>>;
  readonly #state: FakeClientState;
  reconnectPromise: Promise<void> | undefined;
  cancelPromise: Promise<Record<string, unknown>> | undefined;
  readonly #frameworkId: FrameworkId;
  readonly #family: FrameworkToolchainFamily;
  readonly #onInspect?: () => void;
  #retired = false;

  constructor(
    frameworkId: FrameworkId = "cpputest",
    family: FrameworkToolchainFamily = "clang",
    onInspect?: () => void,
    state?: FakeClientState,
  ) {
    this.#frameworkId = frameworkId;
    this.#family = family;
    this.#onInspect = onInspect;
    this.#state = state ?? {
      calls: [], runRequests: [], tasks: new Map(), runs: new Map(),
      artifactsByTask: new Map(), artifactBytes: new Map(), runIndex: 0,
    };
    this.calls = this.#state.calls;
    this.runRequests = this.#state.runRequests;
  }

  replacement(): FakeProtocolClient {
    this.#retired = true;
    return new FakeProtocolClient(this.#frameworkId, this.#family, this.#onInspect, this.#state);
  }

  setCompilerIdentity(version: string, compilerSha256: string): void {
    this.#state.compilerIdentity = { version, compilerSha256 };
  }

  artifact(taskId: string, kind: string): Record<string, unknown> | undefined {
    return this.#state.artifactsByTask.get(taskId)?.find((value) => value.kind === kind);
  }

  task(taskId: string): Record<string, unknown> | undefined {
    return this.#state.tasks.get(taskId);
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
        version: this.#family === "gcc" ? "15.2.0" : "18.1.0",
        compilerSha256: digest(`compiler:${this.#family}`),
        ...this.#state.compilerIdentity,
      }],
      workspaceGeneration: digest("workspace"),
      workspaceUri: "file:///workspace",
    };
  }

  async discoverTests(value: Record<string, unknown>) {
    this.calls.push({ method: "discoverTests", value });
    const taskId = `discovery-task-${this.#frameworkId}-${this.#family}`;
    const task = taskSnapshot(taskId, "succeeded");
    this.#state.tasks.set(taskId, task);
    this.addArtifact(taskId, "test-catalog", Buffer.from(JSON.stringify({
      catalog: "validated", framework: this.#frameworkId, family: this.#family,
    }) + "\n"));
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
        ctestLogicalName: `${this.#frameworkId}.framework`,
        disabled: false,
        displayName: "framework-tests",
        framework: this.#frameworkId,
        id: `utid-v1-${digest(`container:${this.#frameworkId}`)}`,
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
        ctestLogicalName: `${this.#frameworkId}.matrix.malformed`,
        disabled: false,
        displayName: "matrix-malformed",
        framework: this.#frameworkId,
        id: `utid-v1-${digest(`malformed-container:${this.#frameworkId}`)}`,
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
        ctestLogicalName: `${this.#frameworkId}.matrix.opaque`,
        degradedReason: "adapter-contract-invalid",
        disabled: false,
        displayName: "opaque-framework-tests",
        framework: "opaque-ctest",
        id: `utid-v1-${digest(`opaque-container:${this.#frameworkId}`)}`,
        labels: [],
        projectId: "root",
      }],
      diagnostics: [],
      generatedAt: new Date("2026-09-16T00:00:00.000Z"),
      items: [
        ...fakeCatalogItems(this.#frameworkId),
        {
          id: `utid-v1-${digest("foreign-pass")}`,
          containerId: `utid-v1-${digest("foreign-container")}`,
          disabled: false,
          displayName: "Pass",
          framework: this.#frameworkId === "cpputest" ? "unity" : "cpputest",
          kind: "case",
          labels: [],
          logicalName: this.#frameworkId === "cpputest" ? "test_pass" : "Pass",
        },
      ],
      partial: false,
      profileId: "profile",
      projectId: "root",
      revision: catalogRevision,
    };
  }

  async runTests(value: Record<string, unknown>) {
    if (this.#retired) throw new Error("stale client used after Service restart");
    const scenario = scenarioRunOrder[this.#state.runIndex++];
    assert.ok(scenario, "runner started more than the contracted scenario set");
    this.calls.push({ method: "runTests", value: { scenario, ...value } });
    this.runRequests.push(value);
    if (scenario === "stale-catalog") {
      const error = new Error("catalog stale");
      Object.assign(error, { code: "CATALOG_STALE" });
      throw error;
    }
    const taskId = `task-${scenario}-${this.#frameworkId}-${this.#family}`;
    const runId = `run-${scenario}-${this.#frameworkId}-${this.#family}`;
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
    this.#state.tasks.set(taskId, task);
    const run = {
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
    };
    this.#state.runs.set(runId, run);
    const results = fakeResults(scenario, value, this.#frameworkId);
    this.addArtifact(taskId, "test-results", Buffer.from(results.map((item) => JSON.stringify(item)).join("\n") + "\n"));
    this.addArtifact(taskId, "test-run-summary", Buffer.from(JSON.stringify({
      runId, taskId, status: "completed", outcome, startedAt: run.startedAt,
      finishedAt: run.finishedAt, summary: run.summary, resultRevision: run.resultRevision,
      incomplete: false, catalogRevision,
    }) + "\n"));
    return task;
  }

  async getTask(taskId: string) {
    this.calls.push({ method: "getTask", value: taskId });
    const task = this.#state.tasks.get(taskId);
    if (!task) throw new Error(`unknown task ${taskId}`);
    return task;
  }

  async getTestRun(runId: string) {
    this.calls.push({ method: "getTestRun", value: runId });
    const run = this.#state.runs.get(runId);
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

  async listArtifacts(taskId: string) {
    this.calls.push({ method: "listArtifacts", value: taskId });
    return { items: this.#state.artifactsByTask.get(taskId) ?? [] };
  }

  async readArtifact(artifactId: string) {
    this.calls.push({ method: "readArtifact", value: artifactId });
    const bytes = this.#state.artifactBytes.get(artifactId);
    if (bytes === undefined) throw new Error(`unknown artifact ${artifactId}`);
    return bytes;
  }

  private addArtifact(taskId: string, kind: string, bytes: Uint8Array): void {
    const artifactId = `artifact-${taskId}-${kind}`;
    const metadata = {
      artifactId, createdAt: new Date("2026-09-16T00:00:01.000Z"), kind,
      mimeType: kind === "test-results" ? "application/x-ndjson" : "application/json",
      sha256: digestBytes(bytes), sizeBytes: bytes.byteLength, taskId,
      uri: `artifact://${artifactId}`,
    };
    const values = this.#state.artifactsByTask.get(taskId) ?? [];
    values.push(metadata);
    this.#state.artifactsByTask.set(taskId, values);
    this.#state.artifactBytes.set(artifactId, bytes);
  }
}

class FakeFixture {
  client: FakeProtocolClient;
  readonly calls: string[] = [];
  killPromise: Promise<void> | undefined;
  restartPromise: Promise<this> | undefined;
  clientBeforeRestart: FakeProtocolClient | undefined;

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
    if (this.restartPromise !== undefined) return this.restartPromise;
    this.clientBeforeRestart = this.client;
    this.client = this.client.replacement();
    return this;
  }
}

function fakeResults(
  scenario: typeof scenarioRunOrder[number],
  request: Record<string, unknown>,
  frameworkId: FrameworkId,
): Array<Record<string, unknown>> {
  const primaryId = `utid-v1-${digest(`container:${frameworkId}`)}`;
  const malformedId = `utid-v1-${digest(`malformed-container:${frameworkId}`)}`;
  const opaqueId = `utid-v1-${digest(`opaque-container:${frameworkId}`)}`;
  const selected = request.selection as { itemIds?: string[]; containerIds?: string[] };
  const selectedItems = selected.itemIds ?? [];
  const selectedItem = selectedItems[0] ?? `utid-v1-${digest(`${frameworkId}:Pass`)}`;
  const make = (
    outcome: string,
    failureDetails: Array<Record<string, unknown>> = [],
    extra: Record<string, unknown> = {},
  ) => ({
    itemId: selectedItem, containerId: primaryId, iteration: 1, outcome,
    failureDetails, outputRefs: [], partial: false, ...extra,
  });
  const assertion = { category: "assertion_failure", evidenceRefs: [], locations: [], message: "redacted" };
  const mock = { ...assertion, subtype: "mock_missing_call" };
  switch (scenario) {
    case "all": return [
      make("passed", [], { itemId: selectedItems[0] }),
      make("failed", [assertion], { itemId: selectedItems[1] }),
      make("failed", [mock], { itemId: selectedItems[2] }),
    ];
    case "assertion-failure":
    case "failed-rerun": return [make("failed", [assertion])];
    case "cancel": return [make("cancelled")];
    case "crash": return [make("errored", [{ category: "test_process_crash", evidenceRefs: [], locations: [], message: "redacted" }])];
    case "filter":
    case "reconnect-replay":
    case "single": return [make("passed")];
    case "malformed-output": return [make("errored", [{ category: "framework_output_invalid", evidenceRefs: [], locations: [], message: "redacted" }], { containerId: malformedId })];
    case "mock-failure": return [make("failed", [mock])];
    case "opaque-fallback": return [make("passed", [], { containerId: opaqueId, itemId: opaqueId })];
    case "repeat": return [make("passed"), make("passed", [], { iteration: 2 })];
    case "service-restart": return [make("not_run", [], { reason: "service_restarted" })];
    case "skip": return [make("skipped")];
    case "timeout": return [make("timed_out", [{ category: "test_timeout", evidenceRefs: [], locations: [], message: "redacted" }])];
    case "stale-catalog": return [];
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
  const aggregateEvidence = Buffer.from(await fixture.client.readArtifact(
    "artifact-task-all-cpputest-clang-test-results",
  )).toString("utf8");
  assert.match(aggregateEvidence, /"subtype":"mock_missing_call"/u);
  assert.equal(result.scenarios.find(({ id }) => id === "all")?.classification, "aggregate");
  const itemIds = new Set(fakeCatalogItems("cpputest").map(({ id }) => id));
  const containerIds = new Set([
    `utid-v1-${digest("container:cpputest")}`,
    `utid-v1-${digest("malformed-container:cpputest")}`,
    `utid-v1-${digest("opaque-container:cpputest")}`,
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
      if (scenario !== "stale-catalog") priorRunIds.add(`run-${scenario}-cpputest-clang`);
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
    containerIds: [`utid-v1-${digest("opaque-container:cpputest")}`],
  });
  const foreignItem = `utid-v1-${digest("foreign-pass")}`;
  assert.ok(fixture.client.runRequests.every((request) =>
    !JSON.stringify(request.selection).includes(foreignItem)
  ), "mixed-framework catalog entries must never enter selections");
  assert.deepEqual(fixture.calls, ["kill", "restart"]);
  assert.notEqual(fixture.client, fixture.clientBeforeRestart, "restart must reacquire a replacement client");
  const timeoutTaskId = "task-timeout-cpputest-clang";
  assert.equal(fixture.client.task(timeoutTaskId)?.outcome, "timed_out");
  const timeoutScenario = result.scenarios.find(({ id }) => id === "timeout")!;
  assert.equal(
    timeoutScenario.resultArtifactSha256,
    fixture.client.artifact(timeoutTaskId, "test-run-summary")?.sha256,
    "reported digest must come from the Service summary artifact",
  );
});

test("required framework mode is explicit and its missing fixed manifest fails closed", async () => {
  assert.equal(frameworkMatrixRequired({}), false);
  assert.equal(frameworkMatrixRequired({ UNIT_TEST_IDE_P4_FRAMEWORK_MATRIX_REQUIRED: "0" }), false);
  assert.equal(frameworkMatrixRequired({ UNIT_TEST_IDE_P4_FRAMEWORK_MATRIX_REQUIRED: "1" }), true);
  assert.throws(
    () => frameworkMatrixRequired({ UNIT_TEST_IDE_P4_FRAMEWORK_MATRIX_REQUIRED: "yes" }),
    /must be 0 or 1/,
  );
  const root = await mkdtemp(join(tmpdir(), "framework-runtime-missing-"));
  try {
    await assert.rejects(
      loadRequiredFrameworkRuntime(root, "linux", join(root, ".native-e2e", "artifacts", "linux")),
      /required framework runtime manifest is missing/,
    );
  } finally {
    await rm(root, { recursive: true, force: true });
  }
});

test("required runtime validates F1 identity before starting any framework Service and carries it forward", async (t) => {
  const root = await mkdtemp(join(tmpdir(), "framework-runtime-order-"));
  t.after(() => rm(root, { recursive: true, force: true }));
  const artifactDirectory = join(root, ".native-e2e", "artifacts", "linux");
  const contract = Buffer.from('{"schemaVersion":1}\n');
  await mkdir(join(root, "testdata", "framework-matrix"), { recursive: true });
  await mkdir(join(root, ".native-e2e", "framework-runtime"), { recursive: true });
  await writeFile(join(root, "testdata", "framework-matrix", "contract.json"), contract);
  const runtimeFramework = (family: "clang" | "gcc", frameworkId: "cpputest" | "unity") => ({
    frameworkId,
    catalogArtifactSha256: digest(`catalog:${family}:${frameworkId}`),
    dependencyVersion: frameworkId === "cpputest" ? "4.0" : "2.6.1",
    dependencySha256: digest(`archive:${frameworkId}`),
    dependencyTreeSha256: f1Identity.frameworkTreeSha256[frameworkId],
    stableIdDigest: digest(`stable:${family}:${frameworkId}`),
    timeoutMs: 100,
    evidence: {
      sourceArtifactSha256: f1Identity.fixtures[frameworkId].sourceSha256,
      sourceLocationDigest: digest(`locations:${family}:${frameworkId}`),
      executableArtifactSha256: digest(`binary:${family}:${frameworkId}`),
    },
    ...(frameworkId === "unity" ? { cMockProvenance: {
      revision: "6ea503340b1d3fdc0f2bcaf69273ba0160ec83af",
      generatorVersion: "2.7.0",
      inputSha256: digest("cmock input"),
      outputSha256: digest("cmock output"),
      manifestSha256: f1Identity.cMockProvenanceSha256,
      generatedAtRuntime: false,
    } } : {}),
  });
  await writeFile(join(root, ".native-e2e", "framework-runtime", "linux.json"), `${JSON.stringify({
    schemaVersion: 1,
    platform: "linux",
    candidateCommit,
    contractSha256: digestBytes(contract),
    benchmark: {
      id: "catalog-10000",
      itemCount: 10_000,
      sampleCount: 3,
      allocationBudgetPerOperation: 300_000,
      allocationsPerOperation: [100, 100, 100],
      catalogRevision: digest("benchmark catalog"),
      catalogArtifactSha256: digest("benchmark artifact"),
      stableIdDigest: digest("benchmark stable"),
      status: "passed",
    },
    toolchains: (["clang", "gcc"] as const).map((family) => ({
      family,
      compilerVersion: family === "clang" ? "18.1.0" : "15.2.0",
      compilerSha256: digest(`compiler:${family}`),
      frameworks: [runtimeFramework(family, "cpputest"), runtimeFramework(family, "unity")],
    })),
  })}\n`);
  const events: string[] = [];
  for (const family of ["clang", "gcc"]) for (const framework of ["cpputest", "unity"]) {
    const base = join(root, ".native-e2e/framework-work/linux", family, framework);
    await mkdir(join(base, "service"), { recursive: true });
    await mkdir(join(base, "workspace"), { recursive: true });
  }
  const loaded = await loadRequiredFrameworkRuntime(root, "linux", artifactDirectory, {
    loadFrameworkIdentity: async () => {
      events.push("identity");
      return f1Identity;
    },
    startService: async () => {
      events.push("service");
      return {
        client: {},
        dispose: async () => { events.push("dispose"); },
      } as unknown as TaskServiceFixture;
    },
  });
  assert.equal(loaded.identity, f1Identity);
  assert.deepEqual(events, ["identity", "service", "service", "service", "service"]);
  await loaded.dispose();
  assert.deepEqual(events, [
    "identity", "service", "service", "service", "service",
    "dispose", "dispose", "dispose", "dispose",
  ]);
});

test("required native CLI carries the pre-Service F1 identity into native-build", async () => {
  const sentinel = new Error("matrix runner reached");
  let disposed = false;
  await assert.rejects(
    runNativeMain(
      [],
      undefined,
      { UNIT_TEST_IDE_P4_FRAMEWORK_MATRIX_REQUIRED: "1" },
      async (_root, platform, artifactDirectory) => ({
        identity: f1Identity,
        options: {
          artifactDirectory,
          platform,
        } as FrameworkPlatformOptions,
        dispose: async () => { disposed = true; },
      }),
      async (options) => {
        assert.equal(options.frameworkIdentity, f1Identity);
        throw sentinel;
      },
    ),
    sentinel,
  );
  assert.equal(disposed, true);
});

test("required toolchain rejects a stable ID that omits discovered catalog or F1 fixture identity", async (t) => {
  const root = await mkdtemp(join(tmpdir(), "framework-stable-binding-"));
  t.after(() => rm(root, { recursive: true, force: true }));
  const options = platformOptions(join(root, ".native-e2e", "artifacts", "linux"));
  const clang = options.toolchains.find(({ family }) => family === "clang")!;
  for (const framework of clang.frameworks) {
    const catalog = await framework.fixture.client.getTestCatalog({
      projectId: "root", profileId: "profile", limit: 1000,
    });
    (framework as { stableIdDigest: string }).stableIdDigest = stableFrameworkIdDigest(
      framework.frameworkId,
      catalog,
      f1Identity,
    );
  }
  const substitutedIdentity: F1FrameworkIdentity = {
    ...f1Identity,
    fixtures: {
      ...f1Identity.fixtures,
      cpputest: {
        ...f1Identity.fixtures.cpputest,
        executableSha256: digest("substituted F1 executable identity"),
      },
    },
  };
  await assert.rejects(
    runFrameworkToolchain(options, "clang", substitutedIdentity),
    /cpputest stable ID does not match discovered Service catalog and F1 identity/u,
  );
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

test("consumer execution-evidence verification is bounded before any scenarios", async () => {
  const fixture = new FakeFixture();
  const guard = AbortSignal.timeout(500);
  await assert.rejects(Promise.race([
    runFrameworkMatrix({ ...matrixOptions(fixture, 10), executionEvidence: async () => new Promise(() => undefined) }),
    new Promise<never>((_resolve, reject) => guard.addEventListener("abort", () => reject(new Error("test guard expired")), { once: true })),
  ]), /execution evidence.*timed out/u);
  assert.equal(fixture.client.runRequests.length, 0);
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

test("constant clock keeps benchmark and scenario intervals positive and inside the report", async (t) => {
  const root = await mkdtemp(join(tmpdir(), "framework-constant-clock-"));
  t.after(() => rm(root, { recursive: true, force: true }));
  const options = { ...platformOptions(join(root, ".native-e2e", "artifacts", "linux")), now: () => new Date("2026-09-16T00:00:00.000Z") };
  const report = await runFrameworkPlatform(options);
  assert.ok(Date.parse(report.startedAt) < Date.parse(report.finishedAt));
  for (const interval of [report.benchmark, ...report.toolchains.flatMap(({ frameworks }) => frameworks.flatMap(({ scenarios }) => scenarios))]) {
    assert.ok(Date.parse(interval.startedAt) < Date.parse(interval.finishedAt));
    assert.ok(Date.parse(interval.startedAt) >= Date.parse(report.startedAt));
    assert.ok(Date.parse(interval.finishedAt) <= Date.parse(report.finishedAt));
  }
});

test("default clock cannot publish a zero-duration benchmark", async (t) => {
  const root = await mkdtemp(join(tmpdir(), "framework-default-clock-"));
  t.after(() => rm(root, { recursive: true, force: true }));
  const options = platformOptions(join(root, ".native-e2e", "artifacts", "linux"));
  const toolchains = [await runFrameworkToolchain(options, "clang"), await runFrameworkToolchain(options, "gcc")];
  const { now: _now, ...defaultClockOptions } = options;
  t.mock.timers.enable({ apis: ["Date"], now: new Date("2026-09-16T01:00:00.000Z") });
  const report = await publishFrameworkPlatformReport(defaultClockOptions, toolchains, "2026-09-16T00:00:00.000Z");
  assert.ok(Date.parse(report.benchmark.finishedAt) > Date.parse(report.benchmark.startedAt));
  assert.ok(Date.parse(report.finishedAt) >= Date.parse(report.benchmark.finishedAt));
});

for (const mutation of ["none", "source", "source-and-repository", "catalog-identity", "executable-during-run", "compiler-during-run", "benchmark-failure", "benchmark-missing"] as const) test(`loaded consumer binds execution and preserves published inputs (${mutation})`, async (t) => {
  const root = await mkdtemp(join(tmpdir(), "framework-consumer-runtime-"));
  t.after(() => rm(root, { recursive: true, force: true }));
  const artifactDirectory = join(root, ".native-e2e/artifacts/linux");
  const repository = resolve(import.meta.dirname, "../../..");
  await cp(join(repository, "testdata"), join(root, "testdata"), { recursive: true });
  const producer = platformOptions(artifactDirectory);
  const manifest = {
    schemaVersion: 1, candidateCommit, platform: "linux",
    contractSha256: digestBytes(await readFile(join(root, "testdata/framework-matrix/contract.json"))),
    benchmark: producer.benchmark,
    toolchains: await Promise.all([...producer.toolchains].sort((left, right) => left.family.localeCompare(right.family)).map(async (toolchain) => ({
      ...toolchain,
      frameworks: await Promise.all(toolchain.frameworks.map(async ({ fixture, now: _now, candidateCommit: _candidate, platform: _platform, toolchainFamily: _family, ...framework }) => {
        const workspace = join(root, ".native-e2e/framework-work/linux", toolchain.family, framework.frameworkId, "workspace");
        for (const name of ["framework-matrix", "frameworks"]) await cp(join(root, "testdata", name), join(workspace, "source", name), { recursive: true });
        const service = join(workspace, "../service/data/build", "b".repeat(64), "bin");
        await mkdir(service, { recursive: true });
        await writeFile(join(service, `phase9_${framework.frameworkId}`), "producer binary");
        return { ...framework, timeoutMs: 5000, stableIdDigest: stableFrameworkIdDigest(framework.frameworkId, await fixture.client.getTestCatalog({ projectId: "root", profileId: "profile", limit: 1000 }), f1Identity) };
      })),
    }))),
  };
  await mkdir(join(root, ".native-e2e/framework-runtime"), { recursive: true });
  const manifestPath = join(root, ".native-e2e/framework-runtime/linux.json");
  await writeFile(manifestPath, JSON.stringify(manifest));
  const freshBenchmark = { ...producer.benchmark, allocationsPerOperation: [201, 202, 203] as [number, number, number], catalogArtifactSha256: digest("consumer benchmark artifact") };
  const consumers: FakeFixture[] = [];
  const loaded = await loadRequiredFrameworkRuntime(root, "linux", artifactDirectory, {
    loadFrameworkIdentity: async () => f1Identity,
    collectBenchmark: async () => {
      if (mutation === "benchmark-failure") throw new Error("benchmark collection failed");
      return mutation === "benchmark-missing" ? undefined as never : freshBenchmark;
    },
    startService: async (_binary, directory) => {
      const [family, frameworkId] = directory.replaceAll("\\", "/").split("/").slice(-3, -1) as [FrameworkToolchainFamily, FrameworkId];
      const fixture = new FakeFixture(frameworkId, family);
      consumers.push(fixture);
      fixture.client.setCompilerIdentity("99.0.0", digest(`consumer:${family}`));
      const discover = fixture.client.discoverTests.bind(fixture.client);
      fixture.client.discoverTests = async (request) => {
        const bin = join(directory, "data/build", "a".repeat(64), "bin");
        await mkdir(bin, { recursive: true });
        await writeFile(join(bin, `phase9_${frameworkId}`), `consumer:${family}:${frameworkId}`);
        return discover(request);
      };
      if (family === "clang" && frameworkId === "cpputest") {
        if (mutation === "catalog-identity") {
          const catalog = fixture.client.getTestCatalog.bind(fixture.client);
          fixture.client.getTestCatalog = async (request) => {
            const result = await catalog(request);
            result.containers[0]!.displayName = "changed consumer catalog identity";
            return result;
          };
        }
        if (mutation === "source" || mutation === "source-and-repository") {
          await writeFile(join(directory, "../workspace/source/frameworks/cpputest/tests/framework_tests.cpp"), "changed consumer source");
          if (mutation === "source-and-repository") await writeFile(join(root, "testdata/frameworks/cpputest/tests/framework_tests.cpp"), "changed consumer source");
        }
        const run = fixture.client.runTests.bind(fixture.client);
        fixture.client.runTests = async (request) => {
          const result = await run(request);
          if (mutation === "compiler-during-run") fixture.client.setCompilerIdentity("99.0.0", digest("changed compiler during execution"));
          if (mutation === "executable-during-run") await writeFile(join(directory, "data/build", "a".repeat(64), "bin/phase9_cpputest"), "changed consumer binary during run");
          return result;
        };
      }
      return Object.assign(fixture, { dispose: async () => { await rm(directory, { recursive: true, force: true }); } }) as unknown as TaskServiceFixture;
    },
  });
  try {
    if (mutation !== "none") {
      await assert.rejects(runFrameworkPlatform({ ...loaded.options, now: monotonicClock() }), /source|executable|compiler|benchmark|catalog|execution evidence/u);
      if (mutation === "source" || mutation === "source-and-repository") assert.ok(consumers.every(({ client }) => client.runRequests.length === 0), "unverified sources must not execute scenarios");
      await assert.rejects(readFile(join(artifactDirectory, "framework-report.json")), { code: "ENOENT" });
      return;
    }
    const report = await runFrameworkPlatform({ ...loaded.options, now: monotonicClock() });
    assert.deepEqual(report.benchmark.allocationsPerOperation, [201, 202, 203]);
    assert.equal(report.benchmark.catalogArtifactSha256, digest("consumer benchmark artifact"));
    for (const toolchain of report.toolchains) for (const framework of toolchain.frameworks) {
      assert.equal(toolchain.compilerVersion, "99.0.0");
      assert.equal(toolchain.compilerSha256, digest(`consumer:${toolchain.family}`));
      assert.equal(framework.executableArtifactSha256, digest(`consumer:${toolchain.family}:${framework.id}`));
      assert.equal(framework.catalogArtifactSha256, framework.scenarios.find(({ id }) => id === "discovery")!.resultArtifactSha256);
      assert.ok(framework.scenarios.every(({ executableArtifactSha256 }) => executableArtifactSha256 === framework.executableArtifactSha256));
      assert.equal(await readFile(join(root, ".native-e2e/framework-work/linux", toolchain.family, framework.id, "service/data/build", "b".repeat(64), "bin", `phase9_${framework.id}`), "utf8"), "producer binary");
    }
    assert.deepEqual(JSON.parse(await readFile(manifestPath, "utf8")), manifest);
  } finally { await loaded.dispose(); }
});

test("toolchain report binds changed consumer compiler and discovery artifact instead of producer evidence", async (t) => {
  const root = await mkdtemp(join(tmpdir(), "framework-consumer-binding-"));
  t.after(() => rm(root, { recursive: true, force: true }));
  const options = platformOptions(join(root, ".native-e2e", "artifacts", "linux"));
  const clang = options.toolchains.find(({ family }) => family === "clang")!;
  for (const framework of clang.frameworks) {
    const client = framework.fixture.client as unknown as FakeProtocolClient;
    const inspect = client.inspectWorkspace.bind(client);
    client.inspectWorkspace = async () => {
      const result = await inspect();
      Object.assign(result.toolchains[0]!, { version: "99.0.0", compilerSha256: digest("consumer compiler") });
      return result;
    };
  }
  const report = await runFrameworkToolchain(options, "clang");
  assert.equal(report.compilerVersion, "99.0.0");
  assert.equal(report.compilerSha256, digest("consumer compiler"));
  for (const framework of report.frameworks) {
    const discovery = framework.scenarios.find(({ id }) => id === "discovery")!;
    assert.equal(framework.catalogArtifactSha256, discovery.resultArtifactSha256);
    assert.notEqual(framework.catalogArtifactSha256, clang.frameworks.find(({ frameworkId }) => frameworkId === framework.id)!.catalogArtifactSha256);
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

test("discovery-only path returns verified catalog evidence without executing scenarios", async () => {
  const fixture = new FakeFixture();
  const result = await discoverFrameworkCatalog({ fixture: fixture as never, frameworkId: "cpputest", toolchainFamily: "clang", timeoutMs: 100 });
  assert.equal(result.catalog.revision, catalogRevision);
  assert.equal(result.catalogArtifactSha256, digest(JSON.stringify({ catalog: "validated", framework: "cpputest", family: "clang" }) + "\n"));
  assert.ok(result.catalogArtifactSizeBytes > 0);
  assert.equal(fixture.client.runRequests.length, 0);
});

for (const mutation of ["partial", "framework", "container", "family", "artifact", "task"] as const) {
  test(`both discovery consumers reject ${mutation} substitution`, async () => {
    for (const producer of [false, true]) {
      const fixture = new FakeFixture();
      const catalog = fixture.client.getTestCatalog.bind(fixture.client);
      fixture.client.getTestCatalog = async (value) => {
        const result = await catalog(value);
        if (mutation === "partial") result.partial = true;
        if (mutation === "framework") result.containers[0]!.framework = "unity";
        if (mutation === "container") result.containers[0]!.ctestLogicalName = "substituted.framework";
        return result;
      };
      if (mutation === "family") {
        const inspect = fixture.client.inspectWorkspace.bind(fixture.client);
        fixture.client.inspectWorkspace = async () => { const value = await inspect(); value.toolchains[0]!.family = "gcc"; return value; };
      }
      if (mutation === "artifact") fixture.client.readArtifact = async () => Buffer.from("substituted");
      if (mutation === "task") {
        const discover = fixture.client.discoverTests.bind(fixture.client);
        fixture.client.discoverTests = async (value) => { const task = await discover(value); fixture.client.task(task.taskId as string)!.outcome = "failed"; return task; };
      }
      await assert.rejects(producer
        ? discoverFrameworkCatalog({ fixture: fixture as never, frameworkId: "cpputest", toolchainFamily: "clang", timeoutMs: 100 })
        : runFrameworkMatrix(matrixOptions(fixture)));
      assert.equal(fixture.client.runRequests.length, 0);
    }
  });
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
          manifestSha256: "e039f3f6ed52900ef70ddd8f3892bbd695bf97747903a860e2d919f124358bb6",
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

function digestBytes(value: Uint8Array): string {
  return createHash("sha256").update(value).digest("hex");
}
