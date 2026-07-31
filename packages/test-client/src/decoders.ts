import type {
  ArtifactMetadata,
  ArtifactMetadataV12,
  ArtifactMetadataV13,
  TargetList,
  TaskEvent,
  TaskEventV12,
  TaskEventV13,
  TaskSnapshot,
  TaskSnapshotV12,
  TaskSnapshotV13,
  TestCatalog,
  TestItemResult,
  TestRun,
  TestRunPage,
  TestRunSummaryV13,
  TestSourceLocationV13,
  WorkspaceSnapshot
} from "@unit-test-ide/protocol-models";
import type { ProtocolTaskEvent } from "./envelopes.js";

function record(value: unknown, name: string): Record<string, unknown> {
  if (!value || typeof value !== "object" || Array.isArray(value)) throw new Error(`invalid ${name} object`);
  return value as Record<string, unknown>;
}

function date(value: unknown, name: string): Date {
  if (typeof value !== "string") throw new Error(`invalid ${name} date-time`);
  const parsed = new Date(value);
  if (Number.isNaN(parsed.getTime())) throw new Error(`invalid ${name} date-time`);
  return parsed;
}

function optionalDate(value: unknown, name: string): Date | undefined {
  return value === undefined ? undefined : date(value, name);
}

function safeInteger(value: unknown, name: string): number {
  if (typeof value !== "number" || !Number.isSafeInteger(value)) throw new Error(`${name} must be a safe integer`);
  return value;
}

function optionalSafeInteger(value: unknown, name: string): number | undefined {
  return value === undefined ? undefined : safeInteger(value, name);
}

function iteration(value: unknown, name: string): number {
  const result = safeInteger(value, name);
  if (result < 1 || result > 100) throw new Error(`${name} must be between 1 and 100`);
  return result;
}

export function decodeTaskSnapshot(value: unknown): TaskSnapshot {
  const wire = record(value, "task snapshot");
  return {
    taskId: wire.taskId as string,
    kind: wire.kind as TaskSnapshot["kind"],
    scenario: wire.scenario as TaskSnapshot["scenario"],
    status: wire.status as TaskSnapshot["status"],
    outcome: wire.outcome as TaskSnapshot["outcome"],
    createdAt: date(wire.createdAt, "task createdAt"),
    startedAt: optionalDate(wire.startedAt, "task startedAt"),
    finishedAt: optionalDate(wire.finishedAt, "task finishedAt"),
    timeoutMs: optionalSafeInteger(wire.timeoutMs, "task timeoutMs"),
    lastSequence: safeInteger(wire.lastSequence, "task lastSequence"),
    errorCode: wire.errorCode as string | undefined,
    errorMessage: wire.errorMessage as string | undefined
  };
}

export function decodeTaskSnapshotV12(value: unknown): TaskSnapshotV12 {
  const wire = record(value, "protocol 1.2 task snapshot");
  const common = {
    taskId: wire.taskId as string,
    status: wire.status,
    outcome: wire.outcome,
    createdAt: date(wire.createdAt, "task createdAt"),
    startedAt: optionalDate(wire.startedAt, "task startedAt"),
    finishedAt: optionalDate(wire.finishedAt, "task finishedAt"),
    lastSequence: safeInteger(wire.lastSequence, "task lastSequence"),
    errorCode: wire.errorCode as string | undefined,
    errorMessage: wire.errorMessage as string | undefined
  };
  switch (wire.kind) {
    case "cmakeBuild":
      return {
        ...common,
        kind: "cmakeBuild",
        workspaceGeneration: wire.workspaceGeneration,
        projectId: wire.projectId,
        buildProfileId: wire.buildProfileId,
        targetIds: [...(wire.targetIds as string[])],
        jobs: safeInteger(wire.jobs, "task jobs"),
        timeoutMs: safeInteger(wire.timeoutMs, "task timeoutMs")
      } as unknown as TaskSnapshotV12;
    case "simulation":
      return {
        ...common,
        kind: "simulation",
        scenario: wire.scenario,
        timeoutMs: optionalSafeInteger(wire.timeoutMs, "task timeoutMs")
      } as unknown as TaskSnapshotV12;
    default:
      throw new Error("invalid protocol 1.2 task kind");
  }
}

export function decodeTaskSnapshotV13(value: unknown): TaskSnapshotV13 {
  const wire = record(value, "protocol 1.3 task snapshot");
  const common = {
    taskId: wire.taskId,
    status: wire.status,
    outcome: wire.outcome,
    createdAt: date(wire.createdAt, "task createdAt"),
    startedAt: optionalDate(wire.startedAt, "task startedAt"),
    finishedAt: optionalDate(wire.finishedAt, "task finishedAt"),
    lastSequence: safeInteger(wire.lastSequence, "task lastSequence"),
    errorCode: wire.errorCode as string | undefined,
    errorMessage: wire.errorMessage as string | undefined
  };
  switch (wire.kind) {
    case "cmakeBuild":
      return {
        ...common,
        kind: "cmakeBuild",
        workspaceGeneration: wire.workspaceGeneration,
        projectId: wire.projectId,
        buildProfileId: wire.buildProfileId,
        targetIds: [...(wire.targetIds as string[])],
        jobs: safeInteger(wire.jobs, "task jobs"),
        timeoutMs: safeInteger(wire.timeoutMs, "task timeoutMs")
      } as unknown as TaskSnapshotV13;
    case "simulation":
      return {
        ...common,
        kind: "simulation",
        scenario: wire.scenario,
        timeoutMs: optionalSafeInteger(wire.timeoutMs, "task timeoutMs")
      } as unknown as TaskSnapshotV13;
    case "testDiscovery":
      return {
        ...common,
        kind: "testDiscovery",
        projectId: wire.projectId,
        profileId: wire.profileId,
        catalogRevision: wire.catalogRevision
      } as unknown as TaskSnapshotV13;
    case "testRun":
      return {
        ...common,
        kind: "testRun",
        projectId: wire.projectId,
        profileId: wire.profileId,
        catalogRevision: wire.catalogRevision,
        runId: wire.runId,
        repeatCount: iteration(wire.repeatCount, "task repeatCount")
      } as unknown as TaskSnapshotV13;
    default:
      throw new Error("invalid protocol 1.3 task kind");
  }
}

export function decodeTaskEvent(value: unknown): ProtocolTaskEvent {
  const wire = record(value, "task event");
  if (wire.protocolVersion === "1.3") return decodeTaskEventV13(wire);
  if (wire.protocolVersion === "1.2") return decodeTaskEventV12(wire);
  return {
    protocolVersion: wire.protocolVersion as TaskEvent["protocolVersion"],
    kind: wire.kind as TaskEvent["kind"],
    messageId: wire.messageId as string,
    sentAt: date(wire.sentAt, "event sentAt"),
    sequence: safeInteger(wire.sequence, "event sequence"),
    event: wire.event as TaskEvent["event"],
    taskId: wire.taskId as string,
    payloadVersion: safeInteger(wire.payloadVersion, "event payloadVersion"),
    payload: record(wire.payload, "event payload")
  };
}

function decodeTaskEventV12(wire: Record<string, unknown>): TaskEventV12 {
  let payload = { ...record(wire.payload, "event payload") };
  if (wire.event === "task.step_finished") {
    const exitCode = optionalSafeInteger(payload.exitCode, "event step exitCode");
    if (exitCode === undefined) delete payload.exitCode;
    else payload.exitCode = exitCode;
  }
  if (wire.event === "task.diagnostic") {
    const diagnostic = { ...record(payload.diagnostic, "event diagnostic") };
    const line = optionalSafeInteger(diagnostic.line, "event diagnostic line");
    const column = optionalSafeInteger(diagnostic.column, "event diagnostic column");
    if (line === undefined) delete diagnostic.line;
    else diagnostic.line = line;
    if (column === undefined) delete diagnostic.column;
    else diagnostic.column = column;
    payload = { diagnostic };
  }
  return {
    protocolVersion: "1.2",
    kind: "event",
    messageId: wire.messageId,
    sentAt: date(wire.sentAt, "event sentAt"),
    sequence: safeInteger(wire.sequence, "event sequence"),
    event: wire.event,
    taskId: wire.taskId,
    payloadVersion: safeInteger(wire.payloadVersion, "event payloadVersion"),
    payload
  } as unknown as TaskEventV12;
}

function decodeTaskEventV13(wire: Record<string, unknown>): TaskEventV13 {
  let payload = { ...record(wire.payload, "event payload") };
  if (wire.event === "task.step_finished") {
    const exitCode = optionalSafeInteger(payload.exitCode, "event step exitCode");
    if (exitCode === undefined) delete payload.exitCode;
    else payload.exitCode = exitCode;
  }
  if (wire.event === "task.diagnostic") {
    const diagnostic = { ...record(payload.diagnostic, "event diagnostic") };
    const line = optionalSafeInteger(diagnostic.line, "event diagnostic line");
    const column = optionalSafeInteger(diagnostic.column, "event diagnostic column");
    if (line === undefined) delete diagnostic.line;
    else diagnostic.line = line;
    if (column === undefined) delete diagnostic.column;
    else diagnostic.column = column;
    payload = { diagnostic };
  }
  if (wire.event === "test.item.finished") {
    payload = {
      runId: payload.runId,
      result: decodeTestItemResult(payload.result)
    };
  }
  if (wire.event === "test.run.finished") {
    const summary = decodeTestRunSummary(payload.summary);
    validateTestRunSummary(summary);
    payload = { ...payload, summary };
  }
  if (wire.event === "test.container.started" ||
    wire.event === "test.item.started" ||
    wire.event === "test.output" ||
    wire.event === "test.container.finished") {
    payload.iteration = iteration(payload.iteration, "event iteration");
  }
  return {
    protocolVersion: "1.3",
    kind: "event",
    messageId: wire.messageId,
    sentAt: date(wire.sentAt, "event sentAt"),
    sequence: safeInteger(wire.sequence, "event sequence"),
    event: wire.event,
    taskId: wire.taskId,
    payloadVersion: safeInteger(wire.payloadVersion, "event payloadVersion"),
    payload
  } as unknown as TaskEventV13;
}

function decodeSourceLocation(value: unknown, name: string): TestSourceLocationV13 {
  const wire = record(value, name);
  const line = optionalSafeInteger(wire.line, `${name} line`);
  const column = optionalSafeInteger(wire.column, `${name} column`);
  return {
    uri: wire.uri,
    navigable: wire.navigable,
    provenance: wire.provenance,
    ...(line === undefined ? {} : { line }),
    ...(column === undefined ? {} : { column })
  } as unknown as TestSourceLocationV13;
}

function decodeTestItemResult(value: unknown): TestItemResult {
  const wire = record(value, "test item result");
  const resultIteration = iteration(wire.iteration, "test item result iteration");
  const partial = wire.partial as boolean;
  if (wire.outcome === "not_run" && partial !== true) {
    throw new Error("test item result with not_run outcome must be partial");
  }
  const durationMs = optionalSafeInteger(wire.durationMs, "test item result durationMs");
  return {
    itemId: wire.itemId,
    containerId: wire.containerId,
    iteration: resultIteration,
    outcome: wire.outcome,
    failureDetails: (wire.failureDetails as unknown[]).map((detailValue) => {
      const detail = record(detailValue, "test failure detail");
      return {
        ...detail,
        locations: (detail.locations as unknown[]).map((location) =>
          decodeSourceLocation(location, "test failure location")),
        evidenceRefs: [...(detail.evidenceRefs as string[])]
      };
    }),
    outputRefs: [...(wire.outputRefs as string[])],
    partial,
    ...(durationMs === undefined ? {} : { durationMs }),
    ...(wire.sourceLocation === undefined ? {} : {
      sourceLocation: decodeSourceLocation(wire.sourceLocation, "test result source location")
    }),
    ...(wire.reason === undefined ? {} : { reason: wire.reason })
  } as unknown as TestItemResult;
}

function decodeTestRunSummary(value: unknown): TestRunSummaryV13 {
  const wire = record(value, "test run summary");
  return {
    total: safeInteger(wire.total, "test run summary total"),
    completed: safeInteger(wire.completed, "test run summary completed"),
    passed: safeInteger(wire.passed, "test run summary passed"),
    failed: safeInteger(wire.failed, "test run summary failed"),
    skipped: safeInteger(wire.skipped, "test run summary skipped"),
    errored: safeInteger(wire.errored, "test run summary errored"),
    cancelled: safeInteger(wire.cancelled, "test run summary cancelled"),
    timedOut: safeInteger(wire.timedOut, "test run summary timedOut"),
    notRun: safeInteger(wire.notRun, "test run summary notRun"),
    iterations: iteration(wire.iterations, "test run summary iterations")
  };
}

function validateTestRunSummary(summary: TestRunSummaryV13, terminal = true): void {
  const completed = summary.passed + summary.failed + summary.skipped +
    summary.errored + summary.cancelled + summary.timedOut;
  if (!Number.isSafeInteger(completed) || completed !== summary.completed) {
    throw new Error("test run summary completed count is inconsistent");
  }
  const total = completed + summary.notRun;
  if (!Number.isSafeInteger(total) ||
    (terminal ? total !== summary.total : total > summary.total)) {
    throw new Error("test run summary total count is inconsistent");
  }
}

export function decodeArtifactMetadata(value: unknown): ArtifactMetadata {
  const wire = record(value, "artifact metadata");
  return {
    artifactId: wire.artifactId as string,
    taskId: wire.taskId as string,
    kind: wire.kind as ArtifactMetadata["kind"],
    mimeType: wire.mimeType as ArtifactMetadata["mimeType"],
    sizeBytes: safeInteger(wire.sizeBytes, "artifact sizeBytes"),
    sha256: wire.sha256 as string,
    createdAt: date(wire.createdAt, "artifact createdAt")
  };
}

export function decodeArtifactMetadataV12(value: unknown): ArtifactMetadataV12 {
  const wire = record(value, "protocol 1.2 artifact metadata");
  return {
    artifactId: wire.artifactId,
    taskId: wire.taskId,
    kind: wire.kind,
    mimeType: wire.mimeType,
    sizeBytes: safeInteger(wire.sizeBytes, "artifact sizeBytes"),
    sha256: wire.sha256,
    createdAt: date(wire.createdAt, "artifact createdAt"),
    uri: wire.uri
  } as unknown as ArtifactMetadataV12;
}

export function decodeArtifactMetadataV13(value: unknown): ArtifactMetadataV13 {
  const wire = record(value, "protocol 1.3 artifact metadata");
  return {
    artifactId: wire.artifactId,
    taskId: wire.taskId,
    kind: wire.kind,
    mimeType: wire.mimeType,
    sizeBytes: safeInteger(wire.sizeBytes, "artifact sizeBytes"),
    sha256: wire.sha256,
    createdAt: date(wire.createdAt, "artifact createdAt"),
    uri: wire.uri
  } as unknown as ArtifactMetadataV13;
}

export function decodeWorkspaceSnapshot(value: unknown): WorkspaceSnapshot {
  const wire = record(value, "workspace snapshot");
  return {
    workspaceUri: wire.workspaceUri,
    workspaceGeneration: wire.workspaceGeneration,
    capabilities: { ...record(wire.capabilities, "workspace capabilities") },
    diagnostics: (wire.diagnostics as unknown[]).map((diagnostic) => ({
      ...record(diagnostic, "workspace diagnostic")
    })),
    toolchains: (wire.toolchains as unknown[]).map((toolchainValue) => {
      const toolchain = record(toolchainValue, "workspace toolchain");
      const capabilities = record(toolchain.capabilities, "workspace toolchain capabilities");
      return {
        ...toolchain,
        generators: [...(toolchain.generators as unknown[])],
        capabilities: {
          ...capabilities,
          coverageDrivers: [...(capabilities.coverageDrivers as unknown[])]
        }
      };
    }),
    projects: (wire.projects as unknown[]).map((projectValue) => {
      const project = record(projectValue, "workspace project");
      return {
        projectId: project.projectId,
        sourceUri: project.sourceUri,
        buildProfiles: (project.buildProfiles as unknown[]).map((profileValue) => ({
          ...record(profileValue, "workspace build profile")
        }))
      };
    })
  } as unknown as WorkspaceSnapshot;
}

export function decodeTargetList(value: unknown): TargetList {
  const wire = record(value, "CMake target list");
  return {
    workspaceGeneration: wire.workspaceGeneration,
    projectId: wire.projectId,
    buildProfileId: wire.buildProfileId,
    targets: (wire.targets as unknown[]).map((target) => ({ ...record(target, "CMake target") }))
  } as unknown as TargetList;
}

export function decodeTestCatalog(value: unknown): TestCatalog {
  const wire = record(value, "test catalog");
  const containers = (wire.containers as unknown[]).map((containerValue) => {
    const container = record(containerValue, "test container");
    return {
      ...container,
      capabilities: { ...record(container.capabilities, "test container capabilities") },
      labels: [...(container.labels as string[])],
      ...(container.sourceLocation === undefined ? {} : {
        sourceLocation: decodeSourceLocation(container.sourceLocation, "test container source location")
      })
    };
  });
  const items = (wire.items as unknown[]).map((itemValue) => {
    const item = record(itemValue, "test item");
    return {
      ...item,
      labels: [...(item.labels as string[])],
      ...(item.sourceLocation === undefined ? {} : {
        sourceLocation: decodeSourceLocation(item.sourceLocation, "test item source location")
      }),
      ...(item.parameters === undefined ? {} : {
        parameters: (item.parameters as unknown[]).map((parameter) => ({
          ...record(parameter, "test item parameter")
        }))
      })
    };
  });
  const diagnostics = (wire.diagnostics as unknown[]).map((diagnosticValue) => {
    const diagnostic = { ...record(diagnosticValue, "test catalog diagnostic") };
    const line = optionalSafeInteger(diagnostic.line, "test catalog diagnostic line");
    const column = optionalSafeInteger(diagnostic.column, "test catalog diagnostic column");
    if (line === undefined) delete diagnostic.line;
    else diagnostic.line = line;
    if (column === undefined) delete diagnostic.column;
    else diagnostic.column = column;
    return diagnostic;
  });
  validateCatalogReferences(wire.projectId, containers, items);
  return {
    projectId: wire.projectId,
    profileId: wire.profileId,
    revision: wire.revision,
    generatedAt: date(wire.generatedAt, "test catalog generatedAt"),
    containers,
    items,
    diagnostics,
    partial: wire.partial,
    ...(wire.nextCursor === undefined ? {} : { nextCursor: wire.nextCursor })
  } as unknown as TestCatalog;
}

function validateCatalogReferences(
  projectId: unknown,
  containers: Record<string, unknown>[],
  items: Record<string, unknown>[]
): void {
  const containersByID = new Map<string, Record<string, unknown>>();
  for (const container of containers) {
    const id = container.id as string;
    if (containersByID.has(id)) throw new Error("test catalog contains a duplicate container id");
    if (container.projectId !== projectId) throw new Error("test catalog container project reference is inconsistent");
    containersByID.set(id, container);
  }
  const itemsByID = new Map<string, Record<string, unknown>>();
  for (const item of items) {
    const id = item.id as string;
    if (itemsByID.has(id)) throw new Error("test catalog contains a duplicate item id");
    itemsByID.set(id, item);
  }
  for (const item of items) {
    const container = containersByID.get(item.containerId as string);
    if (!container) throw new Error("test catalog item has an unknown container reference");
    if (container.framework !== item.framework) {
      throw new Error("test catalog item framework does not match its container reference");
    }
    if (item.parentId !== undefined) {
      const parent = itemsByID.get(item.parentId as string);
      if (!parent) throw new Error("test catalog item has an unknown parent reference");
      if (parent.containerId !== item.containerId) {
        throw new Error("test catalog item parent reference crosses containers");
      }
    }
  }
  for (const item of items) {
    const visited = new Set<string>();
    let current: Record<string, unknown> | undefined = item;
    while (current?.parentId !== undefined) {
      const currentID = current.id as string;
      if (visited.has(currentID)) throw new Error("test catalog item parent reference contains a cycle");
      visited.add(currentID);
      current = itemsByID.get(current.parentId as string);
    }
  }
}

export function decodeTestRun(value: unknown): TestRun {
  const wire = record(value, "test run");
  const summary = decodeTestRunSummary(wire.summary);
  const selectionSnapshot = record(wire.selectionSnapshot, "test run selection snapshot");
  validateTestRunSummary(summary, wire.status === "completed");
  if (wire.status === "completed" && wire.incomplete === false && summary.notRun !== 0) {
    throw new Error("complete test run summary cannot contain notRun results");
  }
  return {
    runId: wire.runId,
    taskId: wire.taskId,
    projectId: wire.projectId,
    profileId: wire.profileId,
    toolchainId: wire.toolchainId,
    catalogRevision: wire.catalogRevision,
    selectionSnapshot: {
      ...selectionSnapshot,
      containerIds: [...(selectionSnapshot.containerIds as string[])],
      itemIds: [...(selectionSnapshot.itemIds as string[])]
    },
    status: wire.status,
    outcome: wire.outcome,
    startedAt: optionalDate(wire.startedAt, "test run startedAt"),
    finishedAt: optionalDate(wire.finishedAt, "test run finishedAt"),
    summary,
    resultRevision: wire.resultRevision,
    incomplete: wire.incomplete
  } as unknown as TestRun;
}

export function decodeTestRunPage(value: unknown): TestRunPage {
  const wire = record(value, "test run page");
  return {
    items: (wire.items as unknown[]).map((item) => decodeTestRun(item)),
    ...(wire.nextCursor === undefined ? {} : { nextCursor: wire.nextCursor })
  } as TestRunPage;
}
