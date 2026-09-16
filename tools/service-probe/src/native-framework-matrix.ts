import { createHash, randomBytes } from "node:crypto";
import { lstat, mkdir, open, rename, rm } from "node:fs/promises";
import { isAbsolute, join, normalize, resolve, sep } from "node:path";
import type {
  BuildProfileElement,
  ToolchainElement,
  WorkspaceSnapshot,
} from "@unit-test-ide/protocol-models";
import type {
  ProtocolClient,
  ProtocolTaskSnapshot,
  ProtocolTestCatalog,
  ProtocolTestRun,
} from "@unit-test-ide/test-client";
import {
  buildFrameworkPlatformReport,
  FRAMEWORK_SCENARIO_IDS,
  type CMockProvenance,
  type FrameworkBenchmarkEvidence,
  type FrameworkEvidence,
  type FrameworkId,
  type FrameworkPlatform,
  type FrameworkPlatformReport,
  type FrameworkScenarioEvidence,
  type FrameworkScenarioId,
  type FrameworkToolchainEvidence,
  type FrameworkToolchainFamily,
} from "./native-framework-report.js";
import { withNamedTimeout, type TaskServiceFixture } from "./probe.js";

const DEFAULT_TIMEOUT_MS = 30_000;
const MAX_TIMEOUT_MS = 120_000;
const DIGEST = /^[0-9a-f]{64}$/u;
const COMMIT = /^[0-9a-f]{40}$/u;

const SCENARIO_RESULTS = {
  all: ["failed", "aggregate"],
  "assertion-failure": ["failed", "assertion"],
  cancel: ["cancelled", "cancelled"],
  crash: ["errored", "crash"],
  discovery: ["passed", "discovery"],
  "failed-rerun": ["failed", "assertion"],
  filter: ["passed", "selection"],
  "malformed-output": ["errored", "malformed-output"],
  "mock-failure": ["failed", "mock-expectation"],
  "opaque-fallback": ["passed", "opaque-fallback"],
  "reconnect-replay": ["passed", "replay"],
  repeat: ["passed", "repeat"],
  "service-restart": ["interrupted", "service-restarted"],
  single: ["passed", "test"],
  skip: ["skipped", "ignored"],
  "stale-catalog": ["rejected", "stale-catalog"],
  timeout: ["timed-out", "timeout"],
} as const;

const PLATFORM_FAMILIES: Readonly<Record<FrameworkPlatform, readonly FrameworkToolchainFamily[]>> = {
  linux: ["clang", "gcc"],
  win32: ["clang-cl", "msvc"],
};

const MATRIX_KEYS = [
  "candidateCommit", "evidence", "fixture", "frameworkId", "now", "platform",
  "timeoutMs", "toolchainFamily",
] as const;
const EVIDENCE_KEYS = [
  "executableArtifactSha256", "sourceArtifactSha256", "sourceLocationDigest",
] as const;

type FrameworkFixture = Pick<TaskServiceFixture, "client" | "kill" | "restart">;

export interface FrameworkMatrixEvidence {
  readonly sourceArtifactSha256: string;
  readonly sourceLocationDigest: string;
  readonly executableArtifactSha256: string;
}

export interface FrameworkMatrixOptions {
  readonly candidateCommit: string;
  readonly evidence: FrameworkMatrixEvidence;
  readonly fixture: FrameworkFixture;
  readonly frameworkId: FrameworkId;
  readonly platform: FrameworkPlatform;
  readonly toolchainFamily: FrameworkToolchainFamily;
  readonly timeoutMs?: number;
  readonly now?: () => Date;
}

export interface FrameworkMatrixResult {
  readonly frameworkId: FrameworkId;
  readonly catalogRevision: string;
  readonly scenarios: readonly FrameworkScenarioEvidence[];
}

export interface FrameworkPlatformFrameworkOptions extends FrameworkMatrixOptions {
  readonly catalogArtifactSha256: string;
  readonly stableIdDigest: string;
  readonly dependencyVersion: string;
  readonly dependencySha256: string;
  readonly dependencyTreeSha256: string;
  readonly cMockProvenance?: CMockProvenance;
}

export interface FrameworkPlatformToolchainOptions {
  readonly family: FrameworkToolchainFamily;
  readonly compilerVersion: string;
  readonly compilerSha256: string;
  readonly frameworks: readonly FrameworkPlatformFrameworkOptions[];
}

export interface FrameworkPlatformOptions {
  readonly artifactDirectory: string;
  readonly benchmark: Omit<FrameworkBenchmarkEvidence, "startedAt" | "finishedAt">;
  readonly candidateCommit: string;
  readonly now?: () => Date;
  readonly platform: FrameworkPlatform;
  readonly toolchains: readonly FrameworkPlatformToolchainOptions[];
}

interface SelectedWorkspace {
  readonly projectId: string;
  readonly profile: BuildProfileElement;
  readonly toolchain: ToolchainElement;
}

interface CatalogSelection {
  readonly all: { readonly mode: "all" };
  readonly assertion: { readonly mode: "items"; readonly itemIds: string[] };
  readonly crash: { readonly mode: "items"; readonly itemIds: string[] };
  readonly malformed: { readonly mode: "items"; readonly itemIds: string[] };
  readonly mock: { readonly mode: "items"; readonly itemIds: string[] };
  readonly opaque: { readonly mode: "containers"; readonly containerIds: string[] };
  readonly pass: { readonly mode: "items"; readonly itemIds: string[] };
  readonly filter: {
    readonly mode: "filter";
    readonly filter: { readonly includeItemIds: string[] };
  };
  readonly skip: { readonly mode: "items"; readonly itemIds: string[] };
  readonly timeout: { readonly mode: "items"; readonly itemIds: string[] };
}

export async function runFrameworkMatrix(options: FrameworkMatrixOptions): Promise<FrameworkMatrixResult> {
  validateMatrixOptions(options);
  const timeoutMs = options.timeoutMs ?? DEFAULT_TIMEOUT_MS;
  const now = options.now ?? (() => new Date());
  const client = options.fixture.client;
  const workspace = await bounded(
    `${options.frameworkId} workspace inspection`,
    client.inspectWorkspace(),
    timeoutMs,
  );
  const selected = selectWorkspace(workspace, options.toolchainFamily);
  const discoveryStarted = now();
  const discovery = await bounded(
    `${options.frameworkId} discovery start`,
    client.discoverTests({
      idempotencyKey: idempotencyKey(),
      projectId: selected.projectId,
      profileId: selected.profile.buildProfileId,
    }),
    timeoutMs,
  );
  const discoveryTask = await waitForTerminalTask(
    () => options.fixture.client,
    discovery.taskId,
    `${options.frameworkId} discovery`,
    timeoutMs,
  );
  if (discoveryTask.outcome !== "succeeded") {
    throw new Error(`${options.frameworkId} discovery finished with ${String(discoveryTask.outcome)}`);
  }
  const catalog = await bounded(
    `${options.frameworkId} catalog read`,
    options.fixture.client.getTestCatalog({
      projectId: selected.projectId,
      profileId: selected.profile.buildProfileId,
      limit: 1000,
    }),
    timeoutMs,
  );
  validateCatalog(catalog, selected, options.frameworkId);
  const selection = catalogSelection(catalog, options.frameworkId);
  const discoveryRecord = scenarioRecord(
    options,
    "discovery",
    catalog.revision,
    discoveryStarted,
    now(),
    { taskId: discovery.taskId, outcome: "passed" },
  );
  const completedRunIds = new Map<FrameworkScenarioId, string>();
  const scenarios: FrameworkScenarioEvidence[] = [];

  for (const id of FRAMEWORK_SCENARIO_IDS) {
    if (id === "discovery") {
      scenarios.push(discoveryRecord);
      continue;
    }
    const startedAt = now();
    const observation = await executeScenario({
      catalog,
      client: options.fixture.client,
      completedRunIds,
      fixture: options.fixture,
      frameworkId: options.frameworkId,
      id,
      projectId: selected.projectId,
      profileId: selected.profile.buildProfileId,
      selection,
      timeoutMs,
    });
    const expected = SCENARIO_RESULTS[id][0];
    if (observation.outcome !== expected) {
      throw new Error(
        `${options.frameworkId} ${id} observed ${observation.outcome}, expected ${expected}`,
      );
    }
    if (observation.runId !== undefined) completedRunIds.set(id, observation.runId);
    scenarios.push(scenarioRecord(
      options,
      id,
      catalog.revision,
      startedAt,
      now(),
      observation,
    ));
  }
  return Object.freeze({
    frameworkId: options.frameworkId,
    catalogRevision: catalog.revision,
    scenarios: Object.freeze(scenarios),
  });
}

export async function runFrameworkPlatform(options: FrameworkPlatformOptions): Promise<FrameworkPlatformReport> {
  validatePlatformOptions(options);
  const now = options.now ?? (() => new Date());
  const startedAt = now().toISOString();
  const expectedFamilies = PLATFORM_FAMILIES[options.platform];
  const toolchains: FrameworkToolchainEvidence[] = [];
  for (const family of expectedFamilies) {
    toolchains.push(await runFrameworkToolchain(options, family));
  }
  return publishFrameworkPlatformReport(options, toolchains, startedAt);
}

/** Runs one already-launched F1 toolchain pair without publishing a partial report. */
export async function runFrameworkToolchain(
  options: FrameworkPlatformOptions,
  family: FrameworkToolchainFamily,
): Promise<FrameworkToolchainEvidence> {
  validatePlatformOptions(options);
  if (!PLATFORM_FAMILIES[options.platform].includes(family)) {
    throw new Error("framework toolchain is incompatible with the platform");
  }
  const now = options.now ?? (() => new Date());
  const toolchain = options.toolchains.find((candidate) => candidate.family === family)!;
  const byFramework = new Map(toolchain.frameworks.map((framework) => [framework.frameworkId, framework]));
  const frameworks: FrameworkEvidence[] = [];
  for (const frameworkId of ["cpputest", "unity"] as const) {
    const framework = byFramework.get(frameworkId)!;
    const matrix = await runFrameworkMatrix({
      candidateCommit: options.candidateCommit,
      evidence: framework.evidence,
      fixture: framework.fixture,
      frameworkId,
      now,
      platform: options.platform,
      ...(framework.timeoutMs === undefined ? {} : { timeoutMs: framework.timeoutMs }),
      toolchainFamily: family,
    });
    frameworks.push({
      id: frameworkId,
      dependencyVersion: framework.dependencyVersion,
      dependencySha256: framework.dependencySha256,
      dependencyTreeSha256: framework.dependencyTreeSha256,
      catalogRevision: matrix.catalogRevision,
      catalogArtifactSha256: framework.catalogArtifactSha256,
      sourceArtifactSha256: framework.evidence.sourceArtifactSha256,
      sourceLocationDigest: framework.evidence.sourceLocationDigest,
      executableArtifactSha256: framework.evidence.executableArtifactSha256,
      stableIdDigest: framework.stableIdDigest,
      ...(frameworkId === "unity" ? { cMockProvenance: framework.cMockProvenance! } : {}),
      scenarios: [...matrix.scenarios],
    });
  }
  return {
    family,
    compilerVersion: toolchain.compilerVersion,
    compilerSha256: toolchain.compilerSha256,
    frameworks,
  };
}

/** Validates and atomically publishes only a complete platform result. */
export async function publishFrameworkPlatformReport(
  options: FrameworkPlatformOptions,
  toolchains: readonly FrameworkToolchainEvidence[],
  startedAt: string,
): Promise<FrameworkPlatformReport> {
  validatePlatformOptions(options);
  const now = options.now ?? (() => new Date());
  const byFamily = new Map(toolchains.map((toolchain) => [toolchain.family, toolchain]));
  const orderedToolchains = PLATFORM_FAMILIES[options.platform].map((family) => byFamily.get(family));
  if (orderedToolchains.some((toolchain) => toolchain === undefined) || byFamily.size !== orderedToolchains.length) {
    throw new Error("framework platform execution is incomplete");
  }
  const benchmarkStartedAt = now().toISOString();
  const benchmarkFinishedAt = now().toISOString();
  const finishedAt = now().toISOString();
  const report = buildFrameworkPlatformReport({
    schemaVersion: 1,
    candidateCommit: options.candidateCommit,
    sourceCommit: options.candidateCommit,
    platform: options.platform,
    architecture: "x64",
    executionMode: "native",
    publication: "atomic-after-cleanup",
    startedAt,
    finishedAt,
    toolchains: orderedToolchains as FrameworkToolchainEvidence[],
    benchmark: {
      ...options.benchmark,
      allocationsPerOperation: [...options.benchmark.allocationsPerOperation],
      startedAt: benchmarkStartedAt,
      finishedAt: benchmarkFinishedAt,
    },
  });
  await publishReportAtomically(options.artifactDirectory, report);
  return report;
}

interface ScenarioContext {
  readonly catalog: ProtocolTestCatalog;
  readonly client: ProtocolClient;
  readonly completedRunIds: ReadonlyMap<FrameworkScenarioId, string>;
  readonly fixture: FrameworkFixture;
  readonly frameworkId: FrameworkId;
  readonly id: Exclude<FrameworkScenarioId, "discovery">;
  readonly projectId: string;
  readonly profileId: string;
  readonly selection: CatalogSelection;
  readonly timeoutMs: number;
}

interface ScenarioObservation {
  readonly taskId?: string;
  readonly runId?: string;
  readonly outcome: FrameworkScenarioEvidence["observedOutcome"];
  readonly resultRevision?: string;
}

async function executeScenario(context: ScenarioContext): Promise<ScenarioObservation> {
  const selection = selectionForScenario(context);
  const catalogRevision = context.id === "stale-catalog" ? "0".repeat(64) : context.catalog.revision;
  const repeatCount = context.id === "repeat" ? 2 : 1;
  let task: Awaited<ReturnType<ProtocolClient["runTests"]>>;
  try {
    task = await bounded(
      `${context.frameworkId} ${context.id} start`,
      context.client.runTests({
        idempotencyKey: idempotencyKey(),
        projectId: context.projectId,
        profileId: context.profileId,
        catalogRevision,
        selection: selection as Parameters<ProtocolClient["runTests"]>[0]["selection"],
        repeatCount,
      }),
      context.timeoutMs,
    );
  } catch (error) {
    if (context.id === "stale-catalog" && staleCatalogError(error)) {
      return { outcome: "rejected", resultRevision: digestText(errorCode(error)) };
    }
    throw error;
  }
  if (context.id === "stale-catalog") {
    throw new Error("stale catalog scenario unexpectedly started a task");
  }
  if (!("runId" in task) || typeof task.runId !== "string") {
    throw new Error(`${context.frameworkId} ${context.id} task omitted runId`);
  }

  if (context.id === "cancel") {
    await bounded(
      `${context.frameworkId} cancel task`,
      context.client.cancelTask(task.taskId),
      context.timeoutMs,
    );
  } else if (context.id === "reconnect-replay") {
    await bounded(
      `${context.frameworkId} reconnect replay`,
      context.client.reconnect(),
      context.timeoutMs,
    );
  } else if (context.id === "service-restart") {
    await bounded(
      `${context.frameworkId} service kill`,
      context.fixture.kill(),
      context.timeoutMs,
    );
    await bounded(
      `${context.frameworkId} service restart`,
      context.fixture.restart(),
      context.timeoutMs,
    );
  }

  await waitForTerminalTask(
    () => context.fixture.client,
    task.taskId,
    `${context.frameworkId} ${context.id}`,
    context.timeoutMs,
  );
  const run = await bounded(
    `${context.frameworkId} ${context.id} result`,
    context.fixture.client.getTestRun(task.runId),
    context.timeoutMs,
  );
  const outcome = observedOutcome(context.id, run);
  return {
    taskId: task.taskId,
    runId: task.runId,
    outcome,
    resultRevision: run.resultRevision,
  };
}

function selectionForScenario(context: ScenarioContext) {
  switch (context.id) {
    case "all": return context.selection.all;
    case "assertion-failure": return context.selection.assertion;
    case "cancel": return context.selection.timeout;
    case "crash": return context.selection.crash;
    case "failed-rerun": {
      const runId = context.completedRunIds.get("assertion-failure");
      if (runId === undefined) throw new Error("failed rerun has no prior failed run");
      return { mode: "failedFromRun" as const, runId };
    }
    case "filter": return context.selection.filter;
    case "malformed-output": return context.selection.malformed;
    case "mock-failure": return context.selection.mock;
    case "opaque-fallback": return context.selection.opaque;
    case "reconnect-replay":
    case "repeat":
    case "single":
      return context.selection.pass;
    case "service-restart": return context.selection.timeout;
    case "skip": return context.selection.skip;
    case "stale-catalog": return context.selection.pass;
    case "timeout": return context.selection.timeout;
  }
}

function observedOutcome(
  id: Exclude<FrameworkScenarioId, "discovery" | "stale-catalog">,
  run: ProtocolTestRun,
): FrameworkScenarioEvidence["observedOutcome"] {
  if (id === "skip") {
    if (run.summary.skipped < 1) throw new Error("skip scenario produced no skipped item");
    return "skipped";
  }
  if (run.outcome === "timed_out") return "timed-out";
  if (
    run.outcome === "passed" || run.outcome === "failed" || run.outcome === "errored" ||
    run.outcome === "cancelled" || run.outcome === "interrupted"
  ) return run.outcome;
  throw new Error(`${id} produced unsupported TestRun outcome ${String(run.outcome)}`);
}

function catalogSelection(catalog: ProtocolTestCatalog, frameworkId: FrameworkId): CatalogSelection {
  const item = (label: string, pattern: RegExp): ProtocolTestCatalog["items"][number] => {
    const matched = catalog.items.find((candidate) =>
      candidate.kind === "case" && pattern.test(candidate.logicalName)
    );
    if (matched === undefined) throw new Error(`catalog is missing the ${label} case`);
    return matched;
  };
  const pass = item("passing", /^(?:Pass|test_pass)$/iu);
  const assertion = item("assertion failure", /assertion[_ -]?failure/iu);
  const crash = item("crash", /crash/iu);
  const malformed = item("malformed output", /malformed[_ -]?output/iu);
  const mock = item("mock failure", /(?:c?mock).*(?:failure|missing|unexpected|mismatch)|(?:missing|unexpected|mismatch).*call/iu);
  const skip = item("skipped", /skip/iu);
  const timeout = item("timeout", /timeout/iu);
  const container = catalog.containers.find((candidate) => candidate.framework === "opaque-ctest");
  if (container === undefined) {
    throw new Error(`${frameworkId} catalog has no opaque fallback container`);
  }
  return {
    all: { mode: "all" },
    assertion: { mode: "items", itemIds: [assertion.id] },
    crash: { mode: "items", itemIds: [crash.id] },
    malformed: { mode: "items", itemIds: [malformed.id] },
    mock: { mode: "items", itemIds: [mock.id] },
    opaque: { mode: "containers", containerIds: [container.id] },
    pass: { mode: "items", itemIds: [pass.id] },
    filter: { mode: "filter", filter: { includeItemIds: [pass.id] } },
    skip: { mode: "items", itemIds: [skip.id] },
    timeout: { mode: "items", itemIds: [timeout.id] },
  };
}

function validateCatalog(catalog: ProtocolTestCatalog, selected: SelectedWorkspace, frameworkId: FrameworkId): void {
  if (
    catalog.partial || catalog.projectId !== selected.projectId ||
    catalog.profileId !== selected.profile.buildProfileId || !DIGEST.test(catalog.revision)
  ) throw new Error(`${frameworkId} catalog is incomplete or unbound`);
  if (!catalog.containers.some((container) => container.framework === frameworkId)) {
    throw new Error(`${frameworkId} catalog has no matching container`);
  }
}

function selectWorkspace(snapshot: WorkspaceSnapshot, family: FrameworkToolchainFamily): SelectedWorkspace {
  const toolchain = snapshot.toolchains.find((candidate) => candidate.family === family);
  if (toolchain === undefined) throw new Error(`framework toolchain ${family} is not present`);
  for (const project of snapshot.projects) {
    const profile = project.buildProfiles.find((candidate) => candidate.toolchainId === toolchain.toolchainId);
    if (profile !== undefined) return { projectId: project.projectId, profile, toolchain };
  }
  throw new Error(`framework toolchain ${family} has no build profile`);
}

async function waitForTerminalTask(
  client: () => ProtocolClient,
  taskId: string,
  label: string,
  timeoutMs: number,
): Promise<ProtocolTaskSnapshot> {
  const deadline = Date.now() + timeoutMs;
  for (;;) {
    const remaining = deadline - Date.now();
    if (remaining <= 0) throw new Error(`${label} task timed out after ${timeoutMs}ms`);
    const task = await bounded(`${label} task lookup`, client().getTask(taskId), remaining);
    if (task.status === "finished") return task;
    await bounded(`${label} task poll`, delay(Math.min(10, remaining)), remaining);
  }
}

function scenarioRecord(
  options: FrameworkMatrixOptions,
  id: FrameworkScenarioId,
  catalogRevisionValue: string,
  startedAtValue: Date,
  finishedAtValue: Date,
  observation: ScenarioObservation,
): FrameworkScenarioEvidence {
  const [observedOutcomeValue, classification] = SCENARIO_RESULTS[id];
  if (finishedAtValue.getTime() <= startedAtValue.getTime()) {
    finishedAtValue = new Date(startedAtValue.getTime() + 1);
  }
  const resultBytes = Buffer.from(canonicalJson({
    candidateCommit: options.candidateCommit,
    catalogRevision: catalogRevisionValue,
    classification,
    frameworkId: options.frameworkId,
    id,
    observedOutcome: observation.outcome,
    platform: options.platform,
    resultRevision: observation.resultRevision ?? "none",
    runId: observation.runId ?? "none",
    taskId: observation.taskId ?? "none",
    toolchainFamily: options.toolchainFamily,
  }));
  return {
    id,
    status: "passed",
    candidateCommit: options.candidateCommit,
    platform: options.platform,
    toolchainFamily: options.toolchainFamily,
    frameworkId: options.frameworkId,
    catalogRevision: catalogRevisionValue,
    sourceArtifactSha256: options.evidence.sourceArtifactSha256,
    sourceLocationDigest: options.evidence.sourceLocationDigest,
    executableArtifactSha256: options.evidence.executableArtifactSha256,
    resultArtifactSha256: digestBytes(resultBytes),
    resultArtifactSizeBytes: resultBytes.byteLength,
    startedAt: startedAtValue.toISOString(),
    finishedAt: finishedAtValue.toISOString(),
    observedOutcome: observedOutcomeValue,
    classification,
  };
}

function validateMatrixOptions(options: FrameworkMatrixOptions): void {
  closedKeys(options, MATRIX_KEYS, "framework matrix options");
  closedKeys(options.evidence, EVIDENCE_KEYS, "framework matrix evidence");
  if (!COMMIT.test(options.candidateCommit)) throw new Error("candidate commit is invalid");
  if (options.frameworkId !== "cpputest" && options.frameworkId !== "unity") {
    throw new Error("framework ID is invalid");
  }
  if (options.platform !== "linux" && options.platform !== "win32") {
    throw new Error("framework platform is invalid");
  }
  if (!PLATFORM_FAMILIES[options.platform].includes(options.toolchainFamily)) {
    throw new Error("framework toolchain is incompatible with the platform");
  }
  for (const value of Object.values(options.evidence)) {
    if (!DIGEST.test(value)) throw new Error("framework matrix evidence digest is invalid");
  }
  if (
    options.timeoutMs !== undefined &&
    (!Number.isSafeInteger(options.timeoutMs) || options.timeoutMs < 1 || options.timeoutMs > MAX_TIMEOUT_MS)
  ) throw new Error("framework matrix timeout is invalid");
  if (
    options.fixture === null || typeof options.fixture !== "object" ||
    typeof options.fixture.kill !== "function" || typeof options.fixture.restart !== "function"
  ) throw new Error("framework Service fixture is invalid");
  if (options.now !== undefined && typeof options.now !== "function") {
    throw new Error("framework matrix clock is invalid");
  }
}

function validatePlatformOptions(options: FrameworkPlatformOptions): void {
  closedKeys(
    options,
    ["artifactDirectory", "benchmark", "candidateCommit", "now", "platform", "toolchains"],
    "framework platform options",
  );
  if (!isAbsolute(options.artifactDirectory) || options.artifactDirectory.includes("\0")) {
    throw new Error("framework artifact directory must be absolute");
  }
  if (options.platform !== "linux" && options.platform !== "win32") {
    throw new Error("framework platform is invalid");
  }
  const suffix = join(".native-e2e", "artifacts", options.platform === "linux" ? "linux" : "windows");
  const normalized = normalize(options.artifactDirectory);
  if (normalized !== suffix && !normalized.endsWith(`${sep}${suffix}`)) {
    throw new Error("framework report must be below the platform artifact directory");
  }
  if (!COMMIT.test(options.candidateCommit)) throw new Error("candidate commit is invalid");
  const expected = PLATFORM_FAMILIES[options.platform];
  if (
    options.toolchains.length !== expected.length ||
    new Set(options.toolchains.map(({ family }) => family)).size !== expected.length ||
    expected.some((family) => !options.toolchains.some((candidate) => candidate.family === family))
  ) throw new Error("framework platform toolchains are incomplete");
  for (const toolchain of options.toolchains) {
    closedKeys(toolchain, ["compilerSha256", "compilerVersion", "family", "frameworks"], "framework toolchain options");
    if (!DIGEST.test(toolchain.compilerSha256)) throw new Error("compiler digest is invalid");
    if (toolchain.frameworks.length !== 2) throw new Error("framework set is incomplete");
    const ids = new Set(toolchain.frameworks.map(({ frameworkId }) => frameworkId));
    if (!ids.has("cpputest") || !ids.has("unity") || ids.size !== 2) throw new Error("framework set is invalid");
    for (const framework of toolchain.frameworks) validatePlatformFramework(framework, options, toolchain.family);
  }
}

function validatePlatformFramework(
  framework: FrameworkPlatformFrameworkOptions,
  options: FrameworkPlatformOptions,
  family: FrameworkToolchainFamily,
): void {
  closedKeys(
    framework,
    [
      ...MATRIX_KEYS, "cMockProvenance", "catalogArtifactSha256", "dependencySha256",
      "dependencyTreeSha256", "dependencyVersion", "stableIdDigest",
    ],
    "framework platform matrix options",
  );
  validateMatrixOptions({
    candidateCommit: options.candidateCommit,
    evidence: framework.evidence,
    fixture: framework.fixture,
    frameworkId: framework.frameworkId,
    platform: options.platform,
    toolchainFamily: family,
    ...(framework.timeoutMs === undefined ? {} : { timeoutMs: framework.timeoutMs }),
    ...(framework.now === undefined ? {} : { now: framework.now }),
  });
  if (
    framework.candidateCommit !== options.candidateCommit ||
    framework.platform !== options.platform ||
    framework.toolchainFamily !== family
  ) throw new Error("framework platform matrix binding is invalid");
  for (const value of [
    framework.catalogArtifactSha256, framework.stableIdDigest,
    framework.dependencySha256, framework.dependencyTreeSha256,
  ]) if (!DIGEST.test(value)) throw new Error("framework platform digest is invalid");
  if (framework.frameworkId === "unity" && framework.cMockProvenance === undefined) {
    throw new Error("Unity CMock provenance is required");
  }
  if (framework.frameworkId === "cpputest" && framework.cMockProvenance !== undefined) {
    throw new Error("CppUTest cannot carry CMock provenance");
  }
}

async function publishReportAtomically(
  artifactDirectory: string,
  report: FrameworkPlatformReport,
): Promise<void> {
  const directory = resolve(artifactDirectory);
  await mkdir(directory, { recursive: true, mode: 0o700 });
  const info = await lstat(directory);
  if (!info.isDirectory() || info.isSymbolicLink()) {
    throw new Error("framework artifact directory is unsafe");
  }
  const destination = join(directory, "framework-report.json");
  const temporary = join(directory, `.framework-report-${randomBytes(8).toString("hex")}.tmp`);
  const bytes = Buffer.from(`${JSON.stringify(report)}\n`, "utf8");
  let handle;
  try {
    handle = await open(temporary, "wx", 0o600);
    await handle.writeFile(bytes);
    await handle.sync();
    await handle.close();
    handle = undefined;
    await rename(temporary, destination);
  } catch (error) {
    await handle?.close().catch(() => undefined);
    await rm(temporary, { force: true }).catch(() => undefined);
    throw error;
  }
}

function closedKeys(value: unknown, allowed: readonly string[], label: string): void {
  if (
    value === null || typeof value !== "object" || Array.isArray(value) ||
    Object.getPrototypeOf(value) !== Object.prototype
  ) {
    throw new Error(`${label} must be a plain object`);
  }
  const extras = Object.keys(value).filter((key) => !allowed.includes(key));
  if (extras.length > 0) {
    throw new Error(`${label} has unexpected execution control: ${extras.join(",")}`);
  }
}

function staleCatalogError(error: unknown): boolean {
  const code = errorCode(error).toUpperCase();
  return code.includes("CATALOG_STALE") || code.includes("STALE_CATALOG") ||
    code.includes("WORKSPACE_CHANGED") || code.includes("TEST_NOT_FOUND");
}

function errorCode(error: unknown): string {
  if (error !== null && typeof error === "object" && "code" in error && typeof error.code === "string") {
    return error.code;
  }
  return error instanceof Error ? error.message : String(error);
}

function idempotencyKey(): string {
  return randomBytes(16).toString("hex");
}

function bounded<T>(label: string, promise: Promise<T>, timeoutMs: number): Promise<T> {
  return withNamedTimeout(label, promise, timeoutMs);
}

function delay(milliseconds: number): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, milliseconds));
}

function digestText(value: string): string {
  return digestBytes(Buffer.from(value));
}

function digestBytes(value: Uint8Array): string {
  return createHash("sha256").update(value).digest("hex");
}

function canonicalJson(value: unknown): string {
  if (Array.isArray(value)) return `[${value.map(canonicalJson).join(",")}]`;
  if (value !== null && typeof value === "object") {
    const record = value as Record<string, unknown>;
    return `{${Object.keys(record).sort().map((key) =>
      `${JSON.stringify(key)}:${canonicalJson(record[key])}`
    ).join(",")}}`;
  }
  return JSON.stringify(value);
}
