import { createHash, randomBytes } from "node:crypto";
import { lstat, mkdir, open, readFile, rename, rm } from "node:fs/promises";
import { isAbsolute, join, normalize, resolve, sep } from "node:path";
import type {
  BuildProfileElement,
  ToolchainElement,
  WorkspaceSnapshot,
} from "@unit-test-ide/protocol-models";
import type {
  EventSubscription,
  ProtocolClient,
  ProtocolArtifactMetadata,
  ProtocolTaskSnapshot,
  ProtocolTaskEvent,
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

const repositoryRoot = resolve(import.meta.dirname, "../../..");

const PLATFORM_FAMILIES: Readonly<Record<FrameworkPlatform, readonly FrameworkToolchainFamily[]>> = {
  linux: ["clang", "gcc"],
  win32: ["clang-cl", "msvc"],
};

const MATRIX_KEYS = [
  "candidateCommit", "evidence", "fixture", "frameworkId", "now", "platform",
  "timeoutMs", "toolchainFamily", "executionEvidence",
] as const;
const EVIDENCE_KEYS = [
  "executableArtifactSha256", "sourceArtifactSha256", "sourceLocationDigest",
] as const;

type FrameworkFixture = Pick<TaskServiceFixture, "client" | "kill" | "restart">;

export interface F1FrameworkFixtureIdentity {
  readonly metadataSha256: string;
  readonly sourceSha256: string;
  readonly executableSha256: string;
}

export interface F1FrameworkIdentity {
  readonly manifestSha256: string;
  readonly frameworkTreeSha256: Readonly<Record<"cpputest" | "unity" | "cmock", string>>;
  readonly cMockProvenanceSha256: string;
  readonly fixtures: Readonly<Record<FrameworkId, F1FrameworkFixtureIdentity>>;
}

export interface FrameworkMatrixEvidence {
  readonly sourceArtifactSha256: string;
  readonly sourceLocationDigest: string;
  readonly executableArtifactSha256: string;
}

export function stableFrameworkIdDigest(
  frameworkId: FrameworkId,
  catalog: ProtocolTestCatalog,
  provenance: F1FrameworkIdentity,
): string {
  if (frameworkId !== "cpputest" && frameworkId !== "unity") throw new Error("stable framework ID is invalid");
  validateF1Identity(provenance);
  if (catalog.partial) throw new Error("stable framework identity requires a complete catalog");
  const selectedContainers = catalog.containers.filter((container) =>
    container.framework === frameworkId ||
    (container.framework === "opaque-ctest" && container.ctestLogicalName.startsWith(`${frameworkId}.`))
  );
  if (selectedContainers.length === 0) throw new Error(`${frameworkId} stable identity has no catalog container`);
  const selectedContainerIds = new Set(selectedContainers.map(({ id }) => id));
  const itemsById = new Map(catalog.items.map((item) => [item.id, item]));
  const containers = selectedContainers.map((container) => ({
    capabilities: container.capabilities,
    ctestLogicalName: container.ctestLogicalName,
    degradedReason: container.degradedReason ?? null,
    disabled: container.disabled,
    displayName: container.displayName,
    framework: container.framework,
    labels: [...container.labels].sort(codePointCompare),
    sourceLocation: canonicalSourceLocation(container.sourceLocation, frameworkId),
  })).sort(canonicalCompare);
  const items = catalog.items.filter(({ containerId }) => selectedContainerIds.has(containerId)).map((item) => {
    const container = selectedContainers.find(({ id }) => id === item.containerId);
    if (container === undefined) throw new Error("stable framework item container is missing");
    const parent = item.parentId === undefined ? undefined : itemsById.get(item.parentId);
    if (item.parentId !== undefined && (parent === undefined || !selectedContainerIds.has(parent.containerId))) {
      throw new Error("stable framework item parent is outside the selected framework");
    }
    return {
      containerCTestLogicalName: container.ctestLogicalName,
      disabled: item.disabled,
      displayName: item.displayName,
      framework: item.framework,
      kind: item.kind,
      labels: [...item.labels].sort(codePointCompare),
      logicalName: item.logicalName,
      parameters: [...(item.parameters ?? [])].map(({ name, value }) => ({ name, value })).sort(canonicalCompare),
      parent: parent === undefined ? null : { kind: parent.kind, logicalName: parent.logicalName },
      sourceLocation: canonicalSourceLocation(item.sourceLocation, frameworkId),
    };
  }).sort(canonicalCompare);
  const relevantTrees = frameworkId === "unity"
    ? { cmock: provenance.frameworkTreeSha256.cmock, unity: provenance.frameworkTreeSha256.unity }
    : { cpputest: provenance.frameworkTreeSha256.cpputest };
  return digestText(canonicalJson({
    schemaVersion: 1,
    frameworkId,
    catalog: { containers, items },
    provenance: {
      manifestSha256: provenance.manifestSha256,
      frameworkTreeSha256: relevantTrees,
      fixture: provenance.fixtures[frameworkId],
      ...(frameworkId === "unity" ? { cMockProvenanceSha256: provenance.cMockProvenanceSha256 } : {}),
    },
  }));
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
  /** In-process consumer binding; never accepted from the serialized runtime manifest. */
  readonly executionEvidence?: (discovery: DiscoveredFrameworkCatalog) => Promise<FrameworkMatrixEvidence>;
}

export interface FrameworkMatrixResult {
  readonly frameworkId: FrameworkId;
  readonly catalogRevision: string;
  readonly catalogArtifactSha256: string;
  readonly compilerVersion: string;
  readonly compilerSha256: string;
  readonly evidence: FrameworkMatrixEvidence;
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
  readonly collectBenchmark?: () => Promise<Omit<FrameworkBenchmarkEvidence, "startedAt" | "finishedAt">>;
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
  readonly all: { readonly mode: "items"; readonly itemIds: string[] };
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

interface MatrixFrameworkContract {
  readonly fixtureSha256: string;
  readonly augmentationSha256: string;
  readonly primaryCTestName: string;
  readonly malformedCTestName: string;
  readonly opaqueCTestName: string;
  readonly pass: string;
  readonly assertion: string;
  readonly crash: string;
  readonly malformed: string;
  readonly mock: string;
  readonly skip: string;
  readonly timeout: string;
}

interface ScenarioObservation {
  readonly taskId?: string;
  readonly runId?: string;
  readonly outcome: FrameworkScenarioEvidence["observedOutcome"];
  readonly classification: FrameworkScenarioEvidence["classification"];
  readonly artifactSha256: string;
  readonly artifactSizeBytes: number;
}

export interface FrameworkDiscoveryOptions {
  readonly fixture: FrameworkFixture;
  readonly repositoryRoot?: string;
  readonly frameworkId: FrameworkId;
  readonly toolchainFamily: FrameworkToolchainFamily;
  readonly timeoutMs?: number;
}

export interface DiscoveredFrameworkCatalog extends SelectedWorkspace {
  readonly catalog: ProtocolTestCatalog;
  readonly catalogArtifactSha256: string;
  readonly catalogArtifactSizeBytes: number;
  readonly taskId: string;
}

export async function discoverFrameworkCatalog(options: FrameworkDiscoveryOptions): Promise<DiscoveredFrameworkCatalog> {
  closedKeys(options, ["fixture", "repositoryRoot", "frameworkId", "toolchainFamily", "timeoutMs"], "framework discovery options");
  if (options.frameworkId !== "cpputest" && options.frameworkId !== "unity") throw new Error("framework ID is invalid");
  if (options.repositoryRoot !== undefined && (!isAbsolute(options.repositoryRoot) || options.repositoryRoot.includes("\0"))) throw new Error("framework discovery repository root is invalid");
  const contract = await loadMatrixContract(options.frameworkId, options.repositoryRoot);
  const timeoutMs = options.timeoutMs ?? DEFAULT_TIMEOUT_MS;
  if (!Number.isSafeInteger(timeoutMs) || timeoutMs < 1 || timeoutMs > MAX_TIMEOUT_MS) throw new Error("framework discovery timeout is invalid");
  const client = options.fixture.client;
  const workspace = await bounded(
    `${options.frameworkId} workspace inspection`,
    client.inspectWorkspace(),
    timeoutMs,
  );
  const selected = selectWorkspace(workspace, options.toolchainFamily);
  const eventSubscription = await subscribeDiscoveryEvents(client);
  let phase = "discovery-start";
  try {
    phase = "discovery-start";
    const discovery = await bounded(
      `${options.frameworkId} discovery start`,
      client.discoverTests({
        idempotencyKey: idempotencyKey(),
        projectId: selected.projectId,
        profileId: selected.profile.buildProfileId,
      }),
      timeoutMs,
    );
    phase = "task-wait";
    const discoveryObservation = await waitForTerminalTask(
      () => options.fixture.client,
      discovery.taskId,
      `${options.frameworkId} discovery`,
      timeoutMs,
      eventSubscription,
    );
    const discoveryTask = discoveryObservation.task;
  if (discoveryTask.outcome !== "succeeded") {
    const errorCode = typeof discoveryTask.errorCode === "string" && /^[a-z0-9_-]+$/u.test(discoveryTask.errorCode)
      ? discoveryTask.errorCode
      : "unknown";
    const errorDetail = classifyTaskErrorMessage([
      discoveryTask.errorMessage,
      ...discoveryObservation.events.flatMap((event) => eventFailureFragments(event)),
    ].filter((value): value is string => typeof value === "string").join("\n"));
      throw new Error(`${options.frameworkId} discovery finished with ${String(discoveryTask.outcome)} [code=${errorCode}; detail=${errorDetail}]`);
  }
  phase = "catalog-read";
  const catalog = await bounded(
    `${options.frameworkId} catalog read`,
    options.fixture.client.getTestCatalog({
      projectId: selected.projectId,
      profileId: selected.profile.buildProfileId,
      limit: 1000,
    }),
    timeoutMs,
  );
  phase = "catalog-validation";
  validateCatalog(catalog, selected, options.frameworkId);
  phase = "catalog-selection";
  catalogSelection(catalog, options.frameworkId, contract);
    phase = "artifact-read";
    const artifact = await readTaskArtifact(options.fixture.client, discovery.taskId, "test-catalog", timeoutMs);
    return { ...selected, catalog, taskId: discovery.taskId, catalogArtifactSha256: artifact.sha256, catalogArtifactSizeBytes: artifact.bytes.byteLength };
  } catch (error) {
    const message = error instanceof Error ? error.message : String(error);
    throw new Error(`${message} [phase=${phase}]`);
  } finally {
    eventSubscription?.close();
  }
}

async function subscribeDiscoveryEvents(client: ProtocolClient): Promise<EventSubscription | undefined> {
  try {
    return await client.subscribeEvents(0);
  } catch {
    // Test doubles and older protocol fixtures may not expose event replay.
    return undefined;
  }
}

function eventFailureFragments(event: ProtocolTaskEvent): readonly string[] {
  if (event.event === "task.output") {
    const text = (event.payload as { text?: unknown }).text;
    return typeof text === "string" ? [text] : [];
  }
  if (event.event === "task.step_finished") {
    const errorCode = (event.payload as { errorCode?: unknown }).errorCode;
    return typeof errorCode === "string" ? [errorCode] : [];
  }
  if (event.event === "task.diagnostic") {
    const diagnostic = (event.payload as { diagnostic?: { code?: unknown; message?: unknown } }).diagnostic;
    if (diagnostic === undefined) return [];
    return [
      ...(typeof diagnostic.code === "string" ? [diagnostic.code] : []),
      ...(typeof diagnostic.message === "string" ? [diagnostic.message] : []),
    ];
  }
  return [];
}

function classifyTaskErrorMessage(message: string): string {
  const value = message.toLowerCase();
  if (value.includes("program database") || value.includes("pdb")) return "pdb-path";
  if (/\berror\s+c\d{4}\b/iu.test(message) || value.includes("clang-cl")) return "compiler";
  if (value.includes("lnk") || value.includes("undefined symbol") || value.includes("unresolved external")) return "linker";
  if (value.includes("spectre")) return "spectre-library";
  if (value.includes("cmake") || value.includes("configure")) return "cmake-configure";
  if (value.includes("ninja") || value.includes("msbuild") || value.includes("link")) return "build-tool";
  if (value.includes("clang") || value.includes("cl.exe") || value.includes("compiler")) return "compiler";
  if (value.includes("generator") || value.includes("catalog")) return "generator";
  if (value.includes("not found") || value.includes("no such file") || value.includes("cannot find")) return "missing-input";
  return "unknown";
}

export async function runFrameworkMatrix(
  options: FrameworkMatrixOptions,
  requiredIdentity?: Readonly<{ provenance: F1FrameworkIdentity; stableIdDigest: string }>,
): Promise<FrameworkMatrixResult> {
  validateMatrixOptions(options);
  const contract = await loadMatrixContract(options.frameworkId);
  const timeoutMs = options.timeoutMs ?? DEFAULT_TIMEOUT_MS;
  const now = options.now ?? (() => new Date());
  const discoveryStarted = now();
  const discovery = await discoverFrameworkCatalog({ fixture: options.fixture, frameworkId: options.frameworkId, toolchainFamily: options.toolchainFamily, timeoutMs });
  const { catalog } = discovery;
  const selected = discovery;
  const compilerVersion = discovery.toolchain.version;
  const compilerSha256 = discovery.toolchain.compilerSha256;
  if (typeof compilerSha256 !== "string" || !DIGEST.test(compilerSha256) ||
      !/^[0-9]+(?:\.[0-9]+){1,3}$/u.test(compilerVersion)) {
    throw new Error("verified Service compiler identity is required");
  }
  if (
    requiredIdentity !== undefined &&
    stableFrameworkIdDigest(options.frameworkId, catalog, requiredIdentity.provenance) !== requiredIdentity.stableIdDigest
  ) {
    throw new Error(`${options.frameworkId} stable ID does not match discovered Service catalog and F1 identity`);
  }
  const readExecutionEvidence = () => options.executionEvidence === undefined ? Promise.resolve(options.evidence) :
    bounded(`${options.frameworkId} execution evidence`, options.executionEvidence(discovery), timeoutMs);
  const evidence = { ...await readExecutionEvidence() };
  const boundOptions = { ...options, evidence };
  validateMatrixOptions(boundOptions);
  const verifyExecution = async () => {
    if (options.executionEvidence === undefined) return;
    const current = await readExecutionEvidence();
    if (EVIDENCE_KEYS.some((key) => current[key] !== evidence[key])) {
      throw new Error(`${options.frameworkId} execution evidence changed during scenarios`);
    }
  };
  const selection = catalogSelection(catalog, options.frameworkId, contract);
  const discoveryRecord = scenarioRecord(
    boundOptions,
    "discovery",
    catalog.revision,
    discoveryStarted,
    now(),
    {
      taskId: discovery.taskId,
      outcome: "passed",
      classification: "discovery",
      artifactSha256: discovery.catalogArtifactSha256,
      artifactSizeBytes: discovery.catalogArtifactSizeBytes,
    },
  );
  const completedRunIds = new Map<FrameworkScenarioId, string>();
  const scenarios: FrameworkScenarioEvidence[] = [];

  for (const id of FRAMEWORK_SCENARIO_IDS) {
    if (id === "discovery") {
      scenarios.push(discoveryRecord);
      continue;
    }
    await verifyExecution();
    const startedAt = now();
    const observation = await executeScenario({
      catalog,
      completedRunIds,
      fixture: options.fixture,
      frameworkId: options.frameworkId,
      id,
      projectId: selected.projectId,
      profileId: selected.profile.buildProfileId,
      platform: options.platform,
      selection,
      contract,
      timeoutMs,
      toolchainFamily: options.toolchainFamily,
    });
    await verifyExecution();
    const expected = expectedOutcome(id);
    if (observation.outcome !== expected) {
      throw new Error(
        `${options.frameworkId} ${id} observed ${observation.outcome}, expected ${expected}`,
      );
    }
    if (observation.classification !== expectedClassification(id)) {
      throw new Error(
        `${options.frameworkId} ${id} classified ${observation.classification}, expected ${expectedClassification(id)}`,
      );
    }
    if (observation.runId !== undefined) completedRunIds.set(id, observation.runId);
    scenarios.push(scenarioRecord(
      boundOptions,
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
    catalogArtifactSha256: discovery.catalogArtifactSha256,
    compilerVersion,
    compilerSha256,
    evidence: Object.freeze(evidence),
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
  requiredIdentity?: F1FrameworkIdentity,
): Promise<FrameworkToolchainEvidence> {
  validatePlatformOptions(options);
  if (!PLATFORM_FAMILIES[options.platform].includes(family)) {
    throw new Error("framework toolchain is incompatible with the platform");
  }
  const now = options.now ?? (() => new Date());
  const toolchain = options.toolchains.find((candidate) => candidate.family === family)!;
  const byFramework = new Map(toolchain.frameworks.map((framework) => [framework.frameworkId, framework]));
  const frameworks: FrameworkEvidence[] = [];
  let compiler: { compilerVersion: string; compilerSha256: string } | undefined;
  for (const frameworkId of ["cpputest", "unity"] as const) {
    const framework = byFramework.get(frameworkId)!;
    const matrix = await runFrameworkMatrix({
      candidateCommit: options.candidateCommit,
      evidence: framework.evidence,
      ...(framework.executionEvidence === undefined ? {} : { executionEvidence: framework.executionEvidence }),
      fixture: framework.fixture,
      frameworkId,
      now,
      platform: options.platform,
      ...(framework.timeoutMs === undefined ? {} : { timeoutMs: framework.timeoutMs }),
      toolchainFamily: family,
    }, requiredIdentity === undefined ? undefined : {
      provenance: requiredIdentity,
      stableIdDigest: framework.stableIdDigest,
    });
    if (compiler !== undefined && (compiler.compilerVersion !== matrix.compilerVersion || compiler.compilerSha256 !== matrix.compilerSha256)) {
      throw new Error("Service compiler identity changed across frameworks");
    }
    compiler = { compilerVersion: matrix.compilerVersion, compilerSha256: matrix.compilerSha256 };
    frameworks.push({
      id: frameworkId,
      dependencyVersion: framework.dependencyVersion,
      dependencySha256: framework.dependencySha256,
      dependencyTreeSha256: framework.dependencyTreeSha256,
      catalogRevision: matrix.catalogRevision,
      catalogArtifactSha256: matrix.catalogArtifactSha256,
      sourceArtifactSha256: matrix.evidence.sourceArtifactSha256,
      sourceLocationDigest: matrix.evidence.sourceLocationDigest,
      executableArtifactSha256: matrix.evidence.executableArtifactSha256,
      stableIdDigest: framework.stableIdDigest,
      ...(frameworkId === "unity" ? { cMockProvenance: framework.cMockProvenance! } : {}),
      scenarios: [...matrix.scenarios],
    });
  }
  return {
    family,
    ...compiler!,
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
  const lastScenarioFinish = Math.max(Date.parse(startedAt), ...toolchains.flatMap(({ frameworks }) =>
    frameworks.flatMap(({ scenarios }) => scenarios.map(({ finishedAt }) => Date.parse(finishedAt)))));
  const benchmarkStartedAt = new Date(Math.max(now().getTime(), lastScenarioFinish)).toISOString();
  const benchmark = options.collectBenchmark === undefined ? options.benchmark : await options.collectBenchmark();
  if (benchmark === undefined || benchmark === null) throw new Error("consumer benchmark evidence is missing");
  const benchmarkFinishedAt = new Date(Math.max(now().getTime(), Date.parse(benchmarkStartedAt) + 1)).toISOString();
  const finishedAt = new Date(Math.max(now().getTime(), Date.parse(benchmarkFinishedAt))).toISOString();
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
      ...benchmark,
      allocationsPerOperation: [...benchmark.allocationsPerOperation],
      startedAt: benchmarkStartedAt,
      finishedAt: benchmarkFinishedAt,
    },
  });
  await publishReportAtomically(options.artifactDirectory, report);
  return report;
}

interface ScenarioContext {
  readonly catalog: ProtocolTestCatalog;
  readonly completedRunIds: ReadonlyMap<FrameworkScenarioId, string>;
  readonly fixture: FrameworkFixture;
  readonly frameworkId: FrameworkId;
  readonly id: Exclude<FrameworkScenarioId, "discovery">;
  readonly projectId: string;
  readonly profileId: string;
  readonly platform: FrameworkPlatform;
  readonly selection: CatalogSelection;
  readonly contract: MatrixFrameworkContract;
  readonly timeoutMs: number;
  readonly toolchainFamily: FrameworkToolchainFamily;
}

async function executeScenario(context: ScenarioContext): Promise<ScenarioObservation> {
  const selection = selectionForScenario(context);
  const catalogRevision = context.id === "stale-catalog" ? "0".repeat(64) : context.catalog.revision;
  const repeatCount = context.id === "repeat" ? 2 : 1;
  let task: Awaited<ReturnType<ProtocolClient["runTests"]>>;
  try {
    task = await bounded(
      `${context.frameworkId} ${context.id} start`,
      context.fixture.client.runTests({
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
      const bytes = Buffer.from(canonicalJson({
        code: errorCode(error),
        frameworkId: context.frameworkId,
        projectId: context.projectId,
        profileId: context.profileId,
        platform: context.platform,
        requestedCatalogRevision: catalogRevision,
        toolchainFamily: context.toolchainFamily,
      }), "utf8");
      return {
        outcome: "rejected",
        classification: "stale-catalog",
        artifactSha256: digestBytes(bytes),
        artifactSizeBytes: bytes.byteLength,
      };
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
      context.fixture.client.cancelTask(task.taskId),
      context.timeoutMs,
    );
  } else if (context.id === "reconnect-replay") {
    await bounded(
      `${context.frameworkId} reconnect replay`,
      context.fixture.client.reconnect(),
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

  const terminalTask = await waitForTerminalTask(
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
  if (context.id === "timeout" && terminalTask.task.outcome !== "timed_out") {
    throw new Error("timeout scenario did not produce a durable Service timed_out task");
  }
  const evidence = await readRunEvidence(context, task.taskId, task.runId, run);
  return {
    taskId: task.taskId,
    runId: task.runId,
    ...evidence,
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

function catalogSelection(
  catalog: ProtocolTestCatalog,
  frameworkId: FrameworkId,
  contract: MatrixFrameworkContract,
): CatalogSelection {
  const primary = catalog.containers.find((candidate) =>
    candidate.framework === frameworkId && candidate.ctestLogicalName === contract.primaryCTestName
  );
  const malformedContainer = catalog.containers.find((candidate) =>
    candidate.framework === frameworkId && candidate.ctestLogicalName === contract.malformedCTestName
  );
  const opaqueContainer = catalog.containers.find((candidate) =>
    candidate.framework === "opaque-ctest" && candidate.ctestLogicalName === contract.opaqueCTestName
  );
  const missing = [
    primary === undefined ? "primary" : undefined,
    malformedContainer === undefined ? "malformed" : undefined,
    opaqueContainer === undefined ? "opaque" : undefined,
  ].filter((value): value is string => value !== undefined);
  if (missing.length > 0) throw new Error(`${frameworkId} catalog contract containers missing [${missing.join(",")}]`);
  const item = (
    label: string,
    logicalName: string,
      containerId = primary!.id,
  ): ProtocolTestCatalog["items"][number] => {
    const matched = catalog.items.find((candidate) =>
      candidate.kind === "case" && candidate.containerId === containerId &&
      candidate.framework === frameworkId && candidate.logicalName === logicalName
    );
    if (matched === undefined) throw new Error(`catalog is missing the ${label} case`);
    return matched;
  };
  const pass = item("passing", contract.pass);
  const assertion = item("assertion failure", contract.assertion);
  const crash = item("crash", contract.crash);
  const malformed = item("malformed output", contract.malformed, malformedContainer!.id);
  const mock = item("mock failure", contract.mock);
  const skip = item("skipped", contract.skip);
  const timeout = item("timeout", contract.timeout);
  return {
    all: {
      mode: "items",
      itemIds: catalog.items.filter((candidate) =>
        candidate.kind === "case" && candidate.containerId === primary!.id &&
        candidate.id !== crash.id && candidate.id !== timeout.id
      ).map(({ id }) => id).sort(),
    },
    assertion: { mode: "items", itemIds: [assertion.id] },
    crash: { mode: "items", itemIds: [crash.id] },
    malformed: { mode: "items", itemIds: [malformed.id] },
    mock: { mode: "items", itemIds: [mock.id] },
    opaque: { mode: "containers", containerIds: [opaqueContainer!.id] },
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
  subscription?: EventSubscription,
): Promise<Readonly<{ task: ProtocolTaskSnapshot; events: readonly ProtocolTaskEvent[] }>> {
  const deadline = Date.now() + timeoutMs;
  const events: ProtocolTaskEvent[] = [];
  let lastStatus = "unknown";
  let pendingEvent = subscription?.next();
  for (;;) {
    const remaining = deadline - Date.now();
    if (remaining <= 0) throw new Error(`${label} task timed out after ${timeoutMs}ms [status=${lastStatus}]`);
    const task = await bounded(`${label} task lookup`, client().getTask(taskId), remaining);
    lastStatus = task.status;
    if (task.status === "finished") {
      if (subscription !== undefined) {
        while (subscription.lastSequence < task.lastSequence && pendingEvent !== undefined) {
          const next = await bounded(`${label} event replay`, pendingEvent, Math.max(deadline - Date.now(), 1));
          if (next.done) break;
          events.push(next.value);
          pendingEvent = subscription.next();
        }
        while (pendingEvent !== undefined) {
          const next = await Promise.race([
            pendingEvent,
            delay(Math.min(25, Math.max(deadline - Date.now(), 1))).then(() => undefined),
          ]);
          if (next === undefined || next.done) break;
          events.push(next.value);
          pendingEvent = subscription.next();
        }
      }
      return { task, events };
    }
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
  if (finishedAtValue.getTime() <= startedAtValue.getTime()) {
    finishedAtValue = new Date(startedAtValue.getTime() + 1);
  }
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
    resultArtifactSha256: observation.artifactSha256,
    resultArtifactSizeBytes: observation.artifactSizeBytes,
    startedAt: startedAtValue.toISOString(),
    finishedAt: finishedAtValue.toISOString(),
    observedOutcome: observation.outcome,
    classification: observation.classification,
  };
}

interface ArtifactEvidence {
  readonly metadata: ProtocolArtifactMetadata;
  readonly bytes: Uint8Array;
  readonly sha256: string;
}

interface ResultItemEvidence {
  readonly itemId: string;
  readonly containerId: string;
  readonly iteration: number;
  readonly outcome: string;
  readonly reason?: string;
  readonly failureDetails: readonly { readonly category: string; readonly subtype?: string }[];
}

async function readRunEvidence(
  context: ScenarioContext,
  taskId: string,
  runId: string,
  run: ProtocolTestRun,
): Promise<ScenarioObservation> {
  const [summaryArtifact, resultsArtifact] = await Promise.all([
    readTaskArtifact(context.fixture.client, taskId, "test-run-summary", context.timeoutMs),
    readTaskArtifact(context.fixture.client, taskId, "test-results", context.timeoutMs),
  ]);
  const summary = parseJsonObject(summaryArtifact.bytes, "test-run-summary");
  if (
    summary.runId !== runId || summary.taskId !== taskId || summary.status !== "completed" ||
    summary.outcome !== run.outcome || summary.resultRevision !== run.resultRevision ||
    summary.catalogRevision !== context.catalog.revision ||
    canonicalJson(summary.summary) !== canonicalJson(run.summary)
  ) throw new Error(`${context.frameworkId} ${context.id} summary artifact is not bound to its Service run`);
  const results = parseResultLines(resultsArtifact.bytes);
  const classification = classifyRunEvidence(context, run, results);
  const outcome = evidenceOutcome(context.id, run, results);
  return {
    outcome,
    classification,
    artifactSha256: summaryArtifact.sha256,
    artifactSizeBytes: summaryArtifact.bytes.byteLength,
  };
}

async function readTaskArtifact(
  client: ProtocolClient,
  taskId: string,
  kind: string,
  timeoutMs: number,
): Promise<ArtifactEvidence> {
  const page = await bounded(
    `${kind} artifact listing`,
    client.listArtifacts(taskId, { limit: 100 }),
    timeoutMs,
  );
  if (page.nextCursor !== undefined) throw new Error(`${kind} artifact listing was unexpectedly paginated`);
  const matches = page.items.filter((candidate) => candidate.kind === kind);
  if (matches.length !== 1) {
    const kinds = [...new Set(page.items.map((candidate) => candidate.kind))]
      .filter((value) => /^[a-z0-9-]+$/u.test(value))
      .sort();
    const countLabel = matches.length === 0 ? "zero" : "multiple";
    throw new Error(
      `framework discovery artifact count ${countLabel} [kinds=${kinds.length > 0 ? kinds.join(",") : "none"}]`,
    );
  }
  const metadata = matches[0]!;
  if (
    metadata.taskId !== taskId || !DIGEST.test(metadata.sha256) ||
    !Number.isSafeInteger(metadata.sizeBytes) || metadata.sizeBytes < 0
  ) throw new Error(`${kind} artifact metadata is invalid`);
  const bytes = await bounded(
    `${kind} artifact read`,
    client.readArtifact(metadata.artifactId),
    timeoutMs,
  );
  const sha256 = digestBytes(bytes);
  if (bytes.byteLength !== metadata.sizeBytes || sha256 !== metadata.sha256) {
    throw new Error(`${kind} artifact bytes do not match Service metadata`);
  }
  return { metadata, bytes, sha256 };
}

function parseResultLines(bytes: Uint8Array): ResultItemEvidence[] {
  const text = Buffer.from(bytes).toString("utf8");
  if (text.length === 0) return [];
  return text.trimEnd().split("\n").map((line) => {
    const value = parseJsonObject(Buffer.from(line, "utf8"), "test-results line");
    if (
      typeof value.itemId !== "string" || typeof value.containerId !== "string" ||
      !Number.isSafeInteger(value.iteration) || typeof value.outcome !== "string" ||
      !Array.isArray(value.failureDetails)
    ) throw new Error("test-results artifact contains an invalid result");
    const failureDetails = value.failureDetails.map((detail) => {
      if (detail === null || typeof detail !== "object" || Array.isArray(detail) ||
          typeof (detail as Record<string, unknown>).category !== "string") {
        throw new Error("test-results artifact contains invalid failure evidence");
      }
      const record = detail as Record<string, unknown>;
      return {
        category: record.category as string,
        ...(typeof record.subtype === "string" ? { subtype: record.subtype } : {}),
      };
    });
    return {
      itemId: value.itemId,
      containerId: value.containerId,
      iteration: value.iteration as number,
      outcome: value.outcome,
      ...(typeof value.reason === "string" ? { reason: value.reason } : {}),
      failureDetails,
    };
  });
}

function parseJsonObject(bytes: Uint8Array, label: string): Record<string, unknown> {
  let value: unknown;
  try {
    value = JSON.parse(Buffer.from(bytes).toString("utf8"));
  } catch {
    throw new Error(`${label} artifact is not valid JSON`);
  }
  if (value === null || typeof value !== "object" || Array.isArray(value)) {
    throw new Error(`${label} artifact must contain an object`);
  }
  return value as Record<string, unknown>;
}

function classifyRunEvidence(
  context: ScenarioContext,
  run: ProtocolTestRun,
  results: readonly ResultItemEvidence[],
): FrameworkScenarioEvidence["classification"] {
  const details = results.flatMap(({ failureDetails }) => failureDetails);
  if (context.id === "all") {
    const selected = new Set(context.selection.all.itemIds);
    const observed = new Set(results.map(({ itemId }) => itemId));
    if (
      run.outcome !== "failed" || selected.size < 2 || observed.size < 2 ||
      results.length < 2 || results.some(({ itemId }) => !selected.has(itemId)) ||
      !results.some(({ outcome }) => outcome === "failed")
    ) {
      throw new Error("all scenario did not produce bound aggregate Service result evidence");
    }
    return "aggregate";
  }
  if (details.some(({ category }) => category === "framework_output_invalid")) return "malformed-output";
  if (details.some(({ subtype }) => subtype?.startsWith("mock_") || subtype === "mock_failure")) return "mock-expectation";
  if (details.some(({ category }) => category === "test_process_crash")) return "crash";
  if (details.some(({ category }) => category === "test_timeout") && run.outcome === "timed_out") return "timeout";
  if (results.some(({ reason }) => reason === "service_restarted") && run.outcome === "interrupted") return "service-restarted";
  if (run.outcome === "cancelled") return "cancelled";
  if (results.some(({ outcome }) => outcome === "skipped")) return "ignored";
  if (details.some(({ category }) => category === "assertion_failure")) {
    if (results.length > 1) return "aggregate";
    return "assertion";
  }
  const opaqueId = context.selection.opaque.containerIds[0];
  if (results.length > 0 && results.every(({ containerId }) => containerId === opaqueId)) return "opaque-fallback";
  if (new Set(results.map(({ iteration }) => iteration)).size > 1) return "repeat";
  if (context.id === "reconnect-replay") return "replay";
  if (context.id === "filter") return "selection";
  return "test";
}

function evidenceOutcome(
  id: FrameworkScenarioId,
  run: ProtocolTestRun,
  results: readonly ResultItemEvidence[],
): FrameworkScenarioEvidence["observedOutcome"] {
  if (id === "discovery" || id === "stale-catalog") {
    throw new Error(`${id} does not produce a TestRun artifact`);
  }
  if (id === "skip") {
    if (!results.some(({ outcome }) => outcome === "skipped")) {
      throw new Error("skip scenario produced no skipped item evidence");
    }
    return "skipped";
  }
  if (run.outcome === "timed_out") return "timed-out";
  if (
    run.outcome === "passed" || run.outcome === "failed" || run.outcome === "errored" ||
    run.outcome === "cancelled" || run.outcome === "interrupted"
  ) return run.outcome;
  throw new Error(`${id} produced unsupported TestRun outcome ${String(run.outcome)}`);
}

function expectedOutcome(id: FrameworkScenarioId): FrameworkScenarioEvidence["observedOutcome"] {
  switch (id) {
    case "all":
    case "assertion-failure":
    case "failed-rerun": return "failed";
    case "cancel": return "cancelled";
    case "crash":
    case "malformed-output": return "errored";
    case "discovery":
    case "filter":
    case "opaque-fallback":
    case "reconnect-replay":
    case "repeat":
    case "single": return "passed";
    case "mock-failure": return "failed";
    case "service-restart": return "interrupted";
    case "skip": return "skipped";
    case "stale-catalog": return "rejected";
    case "timeout": return "timed-out";
  }
}

function expectedClassification(id: FrameworkScenarioId): FrameworkScenarioEvidence["classification"] {
  switch (id) {
    case "all": return "aggregate";
    case "assertion-failure":
    case "failed-rerun": return "assertion";
    case "cancel": return "cancelled";
    case "crash": return "crash";
    case "discovery": return "discovery";
    case "filter": return "selection";
    case "malformed-output": return "malformed-output";
    case "mock-failure": return "mock-expectation";
    case "opaque-fallback": return "opaque-fallback";
    case "reconnect-replay": return "replay";
    case "repeat": return "repeat";
    case "service-restart": return "service-restarted";
    case "single": return "test";
    case "skip": return "ignored";
    case "stale-catalog": return "stale-catalog";
    case "timeout": return "timeout";
  }
}

export async function loadMatrixContract(frameworkId: FrameworkId, root = repositoryRoot): Promise<MatrixFrameworkContract> {
  const contractRoot = join(root, "testdata", "framework-matrix");
  const contractPath = join(contractRoot, "contract.json");
  const contract = parseJsonObject(await readFile(contractPath), "framework matrix contract");
  closedKeys(contract, ["frameworks", "schemaVersion", "workspace"], "framework matrix contract");
  if (contract.schemaVersion !== 1) throw new Error("framework matrix contract version is invalid");
  const frameworks = contract.frameworks as Record<string, unknown> | undefined;
  const workspace = contract.workspace as Record<string, unknown> | undefined;
  if (frameworks === undefined || workspace === undefined) throw new Error("framework matrix contract is incomplete");
  closedKeys(frameworks, ["cpputest", "unity"], "framework matrix frameworks");
  closedKeys(workspace, ["cmakeSha256", "opaqueSourceSha256"], "framework matrix workspace contract");
  const selected = frameworks[frameworkId];
  if (selected === null || typeof selected !== "object" || Array.isArray(selected)) {
    throw new Error("framework matrix contract has no selected framework");
  }
  const value = selected as unknown as MatrixFrameworkContract;
  closedKeys(value, [
    "assertion", "augmentationSha256", "crash", "fixtureSha256", "malformed",
    "malformedCTestName", "mock", "opaqueCTestName", "pass", "primaryCTestName", "skip", "timeout",
  ], "framework matrix framework contract");
  const expectedDigests = [value.fixtureSha256, value.augmentationSha256, workspace.cmakeSha256, workspace.opaqueSourceSha256];
  if (!expectedDigests.every((digest) => typeof digest === "string" && DIGEST.test(digest))) {
    throw new Error("framework matrix contract digest is invalid");
  }
  for (const name of [
    value.primaryCTestName, value.malformedCTestName, value.opaqueCTestName,
    value.pass, value.assertion, value.crash, value.malformed, value.mock, value.skip, value.timeout,
  ]) {
    if (typeof name !== "string" || name.length === 0 || name.length > 128 || /[\\/\0]/u.test(name)) {
      throw new Error("framework matrix contract identity is invalid");
    }
  }
  const [fixture, augmentation, cmake, opaque] = await Promise.all([
    readFile(join(root, "testdata", "frameworks", frameworkId, "fixture.json")),
    readFile(join(contractRoot, frameworkId === "cpputest" ? "malformed_cpputest.cpp" : "malformed_unity.c")),
    readFile(join(contractRoot, "CMakeLists.txt")),
    readFile(join(contractRoot, "opaque.c")),
  ]);
  const actualDigests = [fixture, augmentation, cmake, opaque].map(digestBytes);
  if (actualDigests.some((digest, index) => digest !== expectedDigests[index])) {
    throw new Error("framework matrix workspace digest binding failed");
  }
  return Object.freeze({ ...value });
}

function validateMatrixOptions(options: FrameworkMatrixOptions): void {
  closedKeys(options, MATRIX_KEYS, "framework matrix options");
  exactKeys(options.evidence, EVIDENCE_KEYS, "framework matrix evidence");
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
  if (options.executionEvidence !== undefined && typeof options.executionEvidence !== "function") {
    throw new Error("framework execution evidence binding is invalid");
  }
}

function validatePlatformOptions(options: FrameworkPlatformOptions): void {
  closedKeys(
    options,
    ["artifactDirectory", "benchmark", "candidateCommit", "collectBenchmark", "now", "platform", "toolchains"],
    "framework platform options",
  );
  if (options.collectBenchmark !== undefined && typeof options.collectBenchmark !== "function") {
    throw new Error("framework benchmark collector is invalid");
  }
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
    ...(framework.executionEvidence === undefined ? {} : { executionEvidence: framework.executionEvidence }),
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

function validateF1Identity(identity: F1FrameworkIdentity): void {
  exactKeys(identity, ["cMockProvenanceSha256", "fixtures", "frameworkTreeSha256", "manifestSha256"], "F1 framework identity");
  exactKeys(identity.frameworkTreeSha256, ["cmock", "cpputest", "unity"], "F1 framework tree identity");
  exactKeys(identity.fixtures, ["cpputest", "unity"], "F1 fixture identities");
  const digests = [
    identity.manifestSha256,
    identity.cMockProvenanceSha256,
    ...Object.values(identity.frameworkTreeSha256),
  ];
  for (const frameworkId of ["cpputest", "unity"] as const) {
    const fixture = identity.fixtures[frameworkId];
    exactKeys(fixture, ["executableSha256", "metadataSha256", "sourceSha256"], `${frameworkId} F1 fixture identity`);
    digests.push(fixture.metadataSha256, fixture.sourceSha256, fixture.executableSha256);
  }
  if (digests.some((value) => !DIGEST.test(value))) throw new Error("F1 framework identity digest is invalid");
}

function exactKeys(value: unknown, keys: readonly string[], label: string): void {
  closedKeys(value, keys, label);
  if (keys.some((key) => !Object.hasOwn(value as object, key))) throw new Error(`${label} is missing required fields`);
}

function canonicalSourceLocation(
  location: ProtocolTestCatalog["containers"][number]["sourceLocation"],
  frameworkId: FrameworkId,
): { readonly column: number | null; readonly line: number | null; readonly navigable: boolean; readonly path: string; readonly provenance: string } | null {
  if (location === undefined) return null;
  return {
    column: location.column ?? null,
    line: location.line ?? null,
    navigable: location.navigable,
    path: portableSourcePath(location.uri, frameworkId),
    provenance: location.provenance,
  };
}

function portableSourcePath(uri: string, frameworkId: FrameworkId): string {
  if (typeof uri !== "string" || uri.length === 0 || uri.includes("\0")) throw new Error("catalog source URI is invalid");
  let pathname = uri;
  try {
    pathname = new URL(uri).pathname;
  } catch {
    // Protocol source locations may use platform paths rather than URL syntax.
  }
  try {
    pathname = decodeURIComponent(pathname);
  } catch {
    throw new Error("catalog source URI encoding is invalid");
  }
  pathname = pathname.replaceAll("\\", "/");
  const markers = [
    `/testdata/frameworks/${frameworkId}/`,
    "/testdata/framework-matrix/",
    `/source/frameworks/${frameworkId}/`,
    "/source/framework-matrix/",
  ];
  let portable;
  for (const marker of markers) {
    const index = pathname.lastIndexOf(marker);
    if (index !== -1) {
      portable = pathname.slice(index + marker.length);
      break;
    }
  }
  if (portable === undefined) {
    for (const marker of ["/tests/", "/include/", "/mocks/"]) {
      const index = pathname.lastIndexOf(marker);
      if (index !== -1) {
        portable = pathname.slice(index + 1);
        break;
      }
    }
  }
  if (portable === undefined) {
    const basename = pathname.slice(pathname.lastIndexOf("/") + 1);
    if (["CMakeLists.txt", "fixture.json"].includes(basename)) portable = basename;
  }
  if (
    portable === undefined || portable.length === 0 || portable.startsWith("/") ||
    portable.split("/").some((part) => part === "" || part === "." || part === "..") ||
    /[\0\r\n]/u.test(portable)
  ) throw new Error("catalog source URI cannot be reduced to a portable fixture path");
  return portable;
}

function codePointCompare(left: string, right: string): number {
  return left < right ? -1 : left > right ? 1 : 0;
}

function canonicalCompare(left: unknown, right: unknown): number {
  return codePointCompare(canonicalJson(left), canonicalJson(right));
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
