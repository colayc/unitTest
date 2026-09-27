import { createHash } from "node:crypto";
import type {
  ArtifactMetadata,
  ArtifactMetadataV12,
  ArtifactMetadataV13,
  ArtifactMetadataV14,
  ArtifactMetadataV15,
  CapabilitiesV15,
  CoverageCompletenessV14,
  CoverageIncompleteReasonV14,
  CoverageMetricV14,
  CoverageReport,
  CoverageRun,
  CoverageRunPage,
  CoverageSummaryV14,
  CoverageToolProvenanceV14,
  TargetList,
  TaskEvent,
  TaskEventV12,
  TaskEventV13,
  TaskEventV14,
  TaskEventV15,
  TaskSnapshot,
  TaskSnapshotV12,
  TaskSnapshotV13,
  TaskSnapshotV14,
  TaskSnapshotV15,
  TestCatalog,
  TestCatalogV14,
  TestItemResult,
  TestRun,
  TestRunPage,
  TestRunPageV14,
  TestRunSummaryV13,
  TestRunV14,
  TestSourceLocationV13,
  TestGenerationTargetListV15,
  TestGenerationRunV15,
  TestGenerationCandidatePageV15,
  TestGenerationEventPageV15,
  TestGenerationCoverageV15,
  WorkspaceSnapshot
} from "@unit-test-ide/protocol-models";
import {
  FrameworkAdapterIDV15,
  ArtifactKindV15,
  ArtifactMIMETypeV15,
  EventProtocolVersionV15,
  EventKindV15,
  TaskEventNameV15,
  TaskKindV15,
  TaskStatusV15,
  TaskOutcomeV15,
  SimulationScenarioV15,
  TestGenerationCandidateKindV15,
  TestGenerationStateV15,
  TestGenerationAssertionKindV15,
  TestGenerationDiagnosticSeverityV15,
  TestGenerationDiagnosticCodeV15,
  TestGenerationDiagnosticReasonV15,
  TestGenerationEditOperationV15,
  TestGenerationTargetFrameworkV15,
  TestGenerationTargetKindV15
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

function wireString(value: unknown, name: string): string {
  if (typeof value !== "string") throw new Error(`invalid ${name}`);
  return value;
}

function wireBoolean(value: unknown, name: string): boolean {
  if (typeof value !== "boolean") throw new Error(`invalid ${name}`);
  return value;
}

function wireEnum<T extends string>(value: unknown, values: readonly T[], name: string): T {
  const found = values.find((item) => item === value);
  if (found === undefined) throw new Error(`invalid ${name}`);
  return found;
}

function wireArray(value: unknown, name: string): unknown[] {
  if (!Array.isArray(value)) throw new Error(`invalid ${name}`);
  return value;
}

export function decodeCapabilitiesV15(value: unknown): CapabilitiesV15 {
  const wire = record(value, "protocol 1.5 capabilities");
  return {
    workspaceInspect: wireBoolean(wire.workspaceInspect, "workspaceInspect"),
    targetList: wireBoolean(wire.targetList, "targetList"),
    cmakeBuild: wireBoolean(wire.cmakeBuild, "cmakeBuild"),
    testDiscovery: wireBoolean(wire.testDiscovery, "testDiscovery"),
    testRun: wireBoolean(wire.testRun, "testRun"),
    frameworkAdapters: wireArray(wire.frameworkAdapters, "frameworkAdapters").map((entry) => {
      const adapter = record(entry, "framework adapter");
      return {
        id: wireEnum(adapter.id, Object.values(FrameworkAdapterIDV15), "framework adapter id"),
        contractVersion: wireString(adapter.contractVersion, "contract version"),
        displayName: wireString(adapter.displayName, "display name"),
        canDiscoverCases: wireBoolean(adapter.canDiscoverCases, "canDiscoverCases"),
        canRunCase: wireBoolean(adapter.canRunCase, "canRunCase"),
        canReportSkipped: wireBoolean(adapter.canReportSkipped, "canReportSkipped"),
        canReportSourceLocation: wireBoolean(adapter.canReportSourceLocation, "canReportSourceLocation"),
        canReportMockDetails: wireBoolean(adapter.canReportMockDetails, "canReportMockDetails")
      };
    }),
    opaqueCTestFallback: wireBoolean(wire.opaqueCTestFallback, "opaqueCTestFallback"),
    ctestJson: wireBoolean(wire.ctestJson, "ctestJson"),
    maxRepeatCount: safeInteger(wire.maxRepeatCount, "maxRepeatCount"),
    maxSelectionSize: safeInteger(wire.maxSelectionSize, "maxSelectionSize"),
    maxCatalogPageSize: safeInteger(wire.maxCatalogPageSize, "maxCatalogPageSize"),
    unityHelperContractVersion: wireString(wire.unityHelperContractVersion, "unityHelperContractVersion"),
    unityRunnerContractVersion: wireString(wire.unityRunnerContractVersion, "unityRunnerContractVersion"),
    coverageRun: wireBoolean(wire.coverageRun, "coverageRun"),
    coverageReport: wireBoolean(wire.coverageReport, "coverageReport"),
    maxCoveragePageSize: safeInteger(wire.maxCoveragePageSize, "maxCoveragePageSize"),
    maxCoverageTimeoutMs: safeInteger(wire.maxCoverageTimeoutMs, "maxCoverageTimeoutMs"),
    testGeneration: wireBoolean(wire.testGeneration, "testGeneration"),
    maxTestGenerationCandidates: safeInteger(wire.maxTestGenerationCandidates, "maxTestGenerationCandidates")
  };
}

export function decodeArtifactMetadataV15(value: unknown): ArtifactMetadataV15 {
  const wire = record(value, "protocol 1.5 artifact metadata");
  return {
    artifactId: wireString(wire.artifactId, "artifact id"),
    taskId: wireString(wire.taskId, "artifact task id"),
    kind: wireEnum(wire.kind, Object.values(ArtifactKindV15), "artifact kind"),
    mimeType: wireEnum(wire.mimeType, Object.values(ArtifactMIMETypeV15), "artifact mime type"),
    sizeBytes: safeInteger(wire.sizeBytes, "artifact sizeBytes"),
    sha256: wireString(wire.sha256, "artifact sha256"),
    createdAt: date(wire.createdAt, "artifact createdAt"),
    uri: wireString(wire.uri, "artifact uri")
  };
}

export function decodeTaskSnapshotV15(value: unknown): TaskSnapshotV15 {
  const wire = record(value, "protocol 1.5 task snapshot");
  const common = {
    taskId: wireString(wire.taskId, "task id"),
    status: wireEnum(wire.status, Object.values(TaskStatusV15), "task status"),
    createdAt: date(wire.createdAt, "task createdAt"),
    lastSequence: safeInteger(wire.lastSequence, "task lastSequence"),
    ...(wire.outcome === undefined ? {} : { outcome: wireEnum(wire.outcome, Object.values(TaskOutcomeV15), "task outcome") }),
    ...(wire.startedAt === undefined ? {} : { startedAt: date(wire.startedAt, "task startedAt") }),
    ...(wire.finishedAt === undefined ? {} : { finishedAt: date(wire.finishedAt, "task finishedAt") }),
    ...(wire.errorCode === undefined ? {} : { errorCode: wireString(wire.errorCode, "task errorCode") }),
    ...(wire.errorMessage === undefined ? {} : { errorMessage: wireString(wire.errorMessage, "task errorMessage") })
  };
  switch (wire.kind) {
    case "testGeneration":
      return {
        ...common, kind: TaskKindV15.TestGeneration,
        runId: wireString(wire.runId, "generation run id"),
        workspaceGeneration: wireString(wire.workspaceGeneration, "workspace generation"),
        projectId: wireString(wire.projectId, "project id")
      };
    case "cmakeBuild":
      return {
        ...common, kind: TaskKindV15.CmakeBuild,
        workspaceGeneration: wireString(wire.workspaceGeneration, "workspace generation"),
        projectId: wireString(wire.projectId, "project id"),
        buildProfileId: wireString(wire.buildProfileId, "build profile id"),
        targetIds: wireArray(wire.targetIds, "target ids").map((id) => wireString(id, "target id")),
        jobs: safeInteger(wire.jobs, "jobs"),
        timeoutMs: safeInteger(wire.timeoutMs, "timeoutMs")
      };
    case "simulation":
      return {
        ...common, kind: TaskKindV15.Simulation,
        scenario: wireEnum(wire.scenario, Object.values(SimulationScenarioV15), "scenario"),
        ...(wire.timeoutMs === undefined ? {} : { timeoutMs: safeInteger(wire.timeoutMs, "timeoutMs") })
      };
    case "testDiscovery":
      return {
        ...common, kind: TaskKindV15.TestDiscovery,
        projectId: wireString(wire.projectId, "project id"),
        profileId: wireString(wire.profileId, "profile id"),
        ...(wire.catalogRevision === undefined ? {} : { catalogRevision: wireString(wire.catalogRevision, "catalog revision") })
      };
    case "testRun":
      return {
        ...common, kind: TaskKindV15.TestRun,
        projectId: wireString(wire.projectId, "project id"),
        profileId: wireString(wire.profileId, "profile id"),
        catalogRevision: wireString(wire.catalogRevision, "catalog revision"),
        runId: wireString(wire.runId, "run id"),
        repeatCount: safeInteger(wire.repeatCount, "repeat count")
      };
    case "coverageRun":
      return {
        ...common, kind: TaskKindV15.CoverageRun,
        workspaceGeneration: wireString(wire.workspaceGeneration, "workspace generation"),
        projectId: wireString(wire.projectId, "project id"),
        coverageProfileId: wireString(wire.coverageProfileId, "coverage profile id"),
        catalogRevision: wireString(wire.catalogRevision, "catalog revision"),
        coverageRunId: wireString(wire.coverageRunId, "coverage run id"),
        testRunId: wireString(wire.testRunId, "test run id"),
        repeatCount: safeInteger(wire.repeatCount, "repeat count"),
        timeoutMs: safeInteger(wire.timeoutMs, "timeoutMs")
      };
    default:
      throw new Error("invalid protocol 1.5 task kind");
  }
}

function decodeGenerationCoverage(value: unknown): TestGenerationCoverageV15 {
  const wire = record(value, "test generation coverage");
  return {
    functionPercent: wireNumber(wire.functionPercent, "function percentage"),
    linePercent: wireNumber(wire.linePercent, "line percentage"),
    branchPercent: wireNumber(wire.branchPercent, "branch percentage")
  };
}

function wireNumber(value: unknown, name: string): number {
  if (typeof value !== "number" || !Number.isFinite(value)) throw new Error(`invalid ${name}`);
  return value;
}

export function decodeTestGenerationTargetList(value: unknown): TestGenerationTargetListV15 {
  const wire = record(value, "test generation target list");
  const items = wireArray(wire.items, "test generation targets").map((entry) => {
    const target = record(entry, "test generation target");
    return {
      kind: wireEnum(target.kind, Object.values(TestGenerationTargetKindV15), "target kind"),
      targetId: wireString(target.targetId, "target id"),
      frameworks: wireArray(target.frameworks, "target frameworks").map((framework) =>
        wireEnum(framework, Object.values(TestGenerationTargetFrameworkV15), "target framework"))
    };
  });
  return {
    items,
    ...(wire.nextCursor === undefined ? {} : { nextCursor: wireString(wire.nextCursor, "target cursor") })
  };
}

export function decodeTestGenerationRun(value: unknown): TestGenerationRunV15 {
  const wire = record(value, "test generation run");
  const preview = wire.preview === undefined ? undefined : record(wire.preview, "generation preview");
  const previewDiff = preview?.diff === undefined ? undefined : wireString(preview.diff, "generation preview diff");
  if (previewDiff !== undefined && createHash("sha256").update(previewDiff, "utf8").digest("hex") !== preview?.diffDigest) {
    throw new Error("invalid generation preview diff digest");
  }
  return {
    runId: wireString(wire.runId, "generation run id"),
    taskId: wireString(wire.taskId, "generation task id"),
    workspaceGeneration: wireString(wire.workspaceGeneration, "workspace generation"),
    projectId: wireString(wire.projectId, "generation project id"),
    state: wireEnum(wire.state, Object.values(TestGenerationStateV15), "generation state"),
    createdAt: date(wire.createdAt, "generation createdAt"),
    lastSequence: safeInteger(wire.lastSequence, "generation lastSequence"),
    ...(wire.finishedAt === undefined ? {} : { finishedAt: date(wire.finishedAt, "generation finishedAt") }),
    ...(wire.candidateCount === undefined ? {} : { candidateCount: safeInteger(wire.candidateCount, "generation candidateCount") }),
    ...(preview === undefined ? {} : { preview: {
      candidateSetDigest: wireString(preview.candidateSetDigest, "candidate set digest"),
      diffDigest: wireString(preview.diffDigest, "preview diff digest"),
      confirmationDigest: wireString(preview.confirmationDigest, "preview confirmation digest"),
      ...(previewDiff === undefined ? {} : { diff: previewDiff }),
      ...(preview.characterizationDigest === undefined ? {} : { characterizationDigest: wireString(preview.characterizationDigest, "characterization digest") })
    } })
  };
}

export function decodeTestGenerationEventPage(value: unknown): TestGenerationEventPageV15 {
  const wire = record(value, "test generation event page");
  return {
    items: wireArray(wire.items, "test generation events").map((entry) => {
      const event = record(entry, "test generation progress event");
      return {
        sequence: safeInteger(event.sequence, "generation event sequence"),
        state: wireEnum(event.state, Object.values(TestGenerationStateV15), "generation event state"),
        occurredAt: date(event.occurredAt, "generation event occurredAt")
      };
    }),
    nextAfterSequence: safeInteger(wire.nextAfterSequence, "generation event nextAfterSequence")
  };
}

export function decodeTestGenerationCandidatePage(value: unknown): TestGenerationCandidatePageV15 {
  const wire = record(value, "test generation candidate page");
  const items = wireArray(wire.items, "generation candidates").map((entry) => {
    const candidate = record(entry, "generation candidate");
    const provenance = record(candidate.assertionProvenance, "assertion provenance");
    return {
      candidateId: wireString(candidate.candidateId, "candidate id"),
      kind: wireEnum(candidate.kind, Object.values(TestGenerationCandidateKindV15), "candidate kind"),
      codeDigest: wireString(candidate.codeDigest, "code digest"),
      artifactDigest: wireString(candidate.artifactDigest, "artifact digest"),
      assertionProvenance: {
        kind: wireEnum(provenance.kind, Object.values(TestGenerationAssertionKindV15), "assertion provenance kind"),
        evidenceDigest: wireString(provenance.evidenceDigest, "evidence digest")
      },
      baselineCoverage: decodeGenerationCoverage(candidate.baselineCoverage),
      deltaCoverage: decodeGenerationCoverage(candidate.deltaCoverage),
      plannedEdits: wireArray(candidate.plannedEdits, "planned edits").map((entry) => {
        const edit = record(entry, "planned edit");
        return {
          path: wireString(edit.path, "planned edit path"),
          operation: wireEnum(edit.operation, Object.values(TestGenerationEditOperationV15), "planned edit operation"),
          afterDigest: wireString(edit.afterDigest, "planned edit digest"),
          ...(edit.beforeDigest === undefined ? {} : { beforeDigest: wireString(edit.beforeDigest, "prior digest") })
        };
      }),
      diagnostics: wireArray(candidate.diagnostics, "generation diagnostics").map((entry) => {
        const diagnostic = record(entry, "generation diagnostic");
        return {
          code: wireEnum(diagnostic.code, Object.values(TestGenerationDiagnosticCodeV15), "diagnostic code"),
          severity: wireEnum(diagnostic.severity, Object.values(TestGenerationDiagnosticSeverityV15), "diagnostic severity"),
          ...(diagnostic.reason === undefined ? {} : { reason: wireEnum(diagnostic.reason, Object.values(TestGenerationDiagnosticReasonV15), "diagnostic reason") })
        };
      }),
      characterizationConfirmed: wireBoolean(candidate.characterizationConfirmed, "characterization confirmation")
    };
  });
  return {
    items,
    ...(wire.nextCursor === undefined ? {} : { nextCursor: wireString(wire.nextCursor, "candidate cursor") })
  };
}

function decodeCoverageMetric(value: unknown, name: string): CoverageMetricV14 {
  const wire = record(value, name);
  const covered = safeInteger(wire.covered, `${name} covered`);
  const total = safeInteger(wire.total, `${name} total`);
  if (covered < 0 || total < 0 || covered > total) throw new Error(`${name} covered exceeds total`);
  return { covered, total };
}

function decodeCoverageSummary(value: unknown): CoverageSummaryV14 {
  const wire = record(value, "coverage summary");
  return {
    lines: decodeCoverageMetric(wire.lines, "coverage lines"),
    branches: decodeCoverageMetric(wire.branches, "coverage branches"),
    functions: decodeCoverageMetric(wire.functions, "coverage functions")
  };
}

function decodeCoverageCompleteness(value: unknown): CoverageCompletenessV14 {
  const wire = record(value, "coverage completeness");
  if (!Array.isArray(wire.reasons)) throw new Error("coverage completeness reasons must be an array");
  const reasons = [...wire.reasons] as CoverageIncompleteReasonV14[];
  if (new Set(reasons).size !== reasons.length) throw new Error("coverage completeness reasons are not unique");
  if (wire.outcome === "available" && reasons.length !== 0) {
    throw new Error("available coverage completeness contains reasons");
  }
  if (wire.outcome === "partial" && reasons.length === 0) {
    throw new Error("partial coverage completeness is missing reasons");
  }
  return { outcome: wire.outcome as CoverageCompletenessV14["outcome"], reasons };
}

function decodeCoverageToolProvenance(value: unknown): CoverageToolProvenanceV14 {
  const wire = record(value, "coverage tool provenance");
  return {
    platform: wire.platform as CoverageToolProvenanceV14["platform"],
    architecture: wire.architecture as CoverageToolProvenanceV14["architecture"],
    compiler: { ...record(wire.compiler, "coverage compiler") } as unknown as CoverageToolProvenanceV14["compiler"],
    driver: { ...record(wire.driver, "coverage driver") } as unknown as CoverageToolProvenanceV14["driver"],
    collector: { ...record(wire.collector, "coverage collector") } as unknown as CoverageToolProvenanceV14["collector"],
    normalizerVersion: wire.normalizerVersion as string,
    instrumentationFingerprint: wire.instrumentationFingerprint as string
  };
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

export function decodeTaskSnapshotV14(value: unknown): TaskSnapshotV14 {
  const wire = record(value, "protocol 1.4 task snapshot");
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
      } as unknown as TaskSnapshotV14;
    case "simulation":
      return {
        ...common,
        kind: "simulation",
        scenario: wire.scenario,
        timeoutMs: optionalSafeInteger(wire.timeoutMs, "task timeoutMs")
      } as unknown as TaskSnapshotV14;
    case "testDiscovery":
      return {
        ...common,
        kind: "testDiscovery",
        projectId: wire.projectId,
        profileId: wire.profileId,
        catalogRevision: wire.catalogRevision
      } as unknown as TaskSnapshotV14;
    case "testRun":
      return {
        ...common,
        kind: "testRun",
        projectId: wire.projectId,
        profileId: wire.profileId,
        catalogRevision: wire.catalogRevision,
        runId: wire.runId,
        repeatCount: iteration(wire.repeatCount, "task repeatCount")
      } as unknown as TaskSnapshotV14;
    case "coverageRun":
      return {
        ...common,
        kind: "coverageRun",
        workspaceGeneration: wire.workspaceGeneration,
        projectId: wire.projectId,
        coverageProfileId: wire.coverageProfileId,
        catalogRevision: wire.catalogRevision,
        coverageRunId: wire.coverageRunId,
        testRunId: wire.testRunId,
        repeatCount: iteration(wire.repeatCount, "task repeatCount"),
        timeoutMs: safeInteger(wire.timeoutMs, "task timeoutMs")
      } as unknown as TaskSnapshotV14;
    default:
      throw new Error("invalid protocol 1.4 task kind");
  }
}

export function decodeTaskEvent(value: unknown): ProtocolTaskEvent {
  const wire = record(value, "task event");
  if (wire.protocolVersion === "1.5") return decodeTaskEventV15(wire);
  if (wire.protocolVersion === "1.4") return decodeTaskEventV14(wire);
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

function decodeTaskEventV15(wire: Record<string, unknown>): TaskEventV15 {
  const event = wireEnum(wire.event, Object.values(TaskEventNameV15), "protocol 1.5 event");
  let payload: Record<string, unknown>;
  if (event === TaskEventNameV15.TestGenerationStateChanged) {
    const state = record(wire.payload, "generation state transition");
    const from = wireEnum(state.from, Object.values(TestGenerationStateV15), "previous generation state");
    const to = wireEnum(state.to, Object.values(TestGenerationStateV15), "next generation state");
    const transitions: Record<string, readonly string[]> = {
      queued: ["baseline", "cancelled", "failed"],
      baseline: ["analyzing", "cancelled", "failed"],
      analyzing: ["solving", "cancelled", "failed"],
      solving: ["rendering", "cancelled", "failed"],
      rendering: ["validating", "cancelled", "failed"],
      validating: ["minimizing", "rejected", "cancelled", "failed"],
      minimizing: ["awaiting_confirmation", "rejected", "cancelled", "failed"],
      awaiting_confirmation: ["accepted", "rejected", "cancelled", "failed"],
      accepted: [], rejected: [], cancelled: [], failed: []
    };
    if (!transitions[from]?.includes(to)) throw new Error("invalid test generation state transition");
    payload = { runId: wireString(state.runId, "generation run id"), from, to };
  } else {
    const legacy = decodeTaskEventV14({ ...wire, protocolVersion: "1.4" });
    payload = record(legacy.payload, "protocol 1.5 event payload");
  }
  const decoded = {
    protocolVersion: EventProtocolVersionV15.The15,
    kind: EventKindV15.Event,
    messageId: wireString(wire.messageId, "event message id"),
    sentAt: date(wire.sentAt, "event sentAt"),
    sequence: safeInteger(wire.sequence, "event sequence"),
    event,
    taskId: wireString(wire.taskId, "event task id"),
    payloadVersion: safeInteger(wire.payloadVersion, "event payload version"),
    payload
  };
  if (!isDecodedTaskEventV15(decoded)) throw new Error("invalid protocol 1.5 event");
  return decoded;
}

function isDecodedTaskEventV15(value: unknown): value is TaskEventV15 {
  const wire = record(value, "decoded protocol 1.5 event");
  if (wire.protocolVersion !== EventProtocolVersionV15.The15 ||
      wire.kind !== EventKindV15.Event ||
      !Object.values(TaskEventNameV15).includes(wire.event as TaskEventNameV15)) return false;
  const payload = record(wire.payload, "decoded protocol 1.5 payload");
  if (wire.event === TaskEventNameV15.TestGenerationStateChanged) {
    return typeof payload.runId === "string" &&
      Object.values(TestGenerationStateV15).includes(payload.from as TestGenerationStateV15) &&
      Object.values(TestGenerationStateV15).includes(payload.to as TestGenerationStateV15);
  }
  return true;
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

function decodeTaskEventV14(wire: Record<string, unknown>): TaskEventV14 {
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
  if (wire.event === "coverage.run.started") {
    payload.repeatCount = iteration(payload.repeatCount, "event repeatCount");
  }
  if (wire.event === "coverage.report.available") {
    payload = {
      ...payload,
      completeness: decodeCoverageCompleteness(payload.completeness),
      summary: decodeCoverageSummary(payload.summary)
    };
  }
  return {
    protocolVersion: "1.4",
    kind: "event",
    messageId: wire.messageId,
    sentAt: date(wire.sentAt, "event sentAt"),
    sequence: safeInteger(wire.sequence, "event sequence"),
    event: wire.event,
    taskId: wire.taskId,
    payloadVersion: safeInteger(wire.payloadVersion, "event payloadVersion"),
    payload
  } as unknown as TaskEventV14;
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

export function decodeArtifactMetadataV14(value: unknown): ArtifactMetadataV14 {
  const wire = record(value, "protocol 1.4 artifact metadata");
  return {
    artifactId: wire.artifactId,
    taskId: wire.taskId,
    kind: wire.kind,
    mimeType: wire.mimeType,
    sizeBytes: safeInteger(wire.sizeBytes, "artifact sizeBytes"),
    sha256: wire.sha256,
    createdAt: date(wire.createdAt, "artifact createdAt"),
    uri: wire.uri
  } as unknown as ArtifactMetadataV14;
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

export function decodeTestCatalogV14(value: unknown): TestCatalogV14 {
  return decodeTestCatalog(value) as unknown as TestCatalogV14;
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

export function decodeTestRunV14(value: unknown): TestRunV14 {
  return decodeTestRun(value) as unknown as TestRunV14;
}

export function decodeTestRunPage(value: unknown): TestRunPage {
  const wire = record(value, "test run page");
  return {
    items: (wire.items as unknown[]).map((item) => decodeTestRun(item)),
    ...(wire.nextCursor === undefined ? {} : { nextCursor: wire.nextCursor })
  } as TestRunPage;
}

export function decodeTestRunPageV14(value: unknown): TestRunPageV14 {
  const wire = record(value, "test run page");
  return {
    items: (wire.items as unknown[]).map((item) => decodeTestRunV14(item)),
    ...(wire.nextCursor === undefined ? {} : { nextCursor: wire.nextCursor })
  } as TestRunPageV14;
}

export function decodeCoverageRun(value: unknown): CoverageRun {
  const wire = record(value, "coverage run");
  const selection = record(wire.selectionSnapshot, "coverage selection snapshot");
  if (wire.status !== "finished" &&
      (wire.outcome !== undefined || wire.reason !== undefined || wire.finishedAt !== undefined || wire.reportId !== undefined)) {
    throw new Error("non-terminal coverage run contains terminal metadata");
  }
  if ((wire.outcome === "available" || wire.outcome === "partial") && wire.reportId === undefined) {
    throw new Error("report-bearing coverage run is missing reportId");
  }
  if ((wire.outcome === "unavailable" || wire.outcome === "cancelled") && wire.reportId !== undefined) {
    throw new Error("report-free coverage run contains reportId");
  }
  if (wire.status === "finished" && (wire.outcome === undefined || wire.finishedAt === undefined)) {
    throw new Error("finished coverage run is missing terminal metadata");
  }
  if ((wire.outcome === "available" || wire.outcome === "partial") && wire.reason !== undefined) {
    throw new Error("report-bearing coverage run contains failure reason");
  }
  const cancelledReason = wire.reason === "user_cancelled" || wire.reason === "task_timed_out";
  if (wire.outcome === "cancelled" && !cancelledReason) {
    throw new Error("cancelled coverage run has an invalid reason");
  }
  if (wire.outcome === "unavailable" && (wire.reason === undefined || cancelledReason)) {
    throw new Error("unavailable coverage run has an invalid reason");
  }
  return {
    coverageRunId: wire.coverageRunId,
    taskId: wire.taskId,
    testRunId: wire.testRunId,
    workspaceGeneration: wire.workspaceGeneration,
    projectId: wire.projectId,
    coverageProfileId: wire.coverageProfileId,
    catalogRevision: wire.catalogRevision,
    selectionSnapshot: {
      ...selection,
      containerIds: [...(selection.containerIds as string[])],
      itemIds: [...(selection.itemIds as string[])]
    },
    repeatCount: iteration(wire.repeatCount, "coverage repeatCount"),
    timeoutMs: safeInteger(wire.timeoutMs, "coverage timeoutMs"),
    status: wire.status,
    createdAt: date(wire.createdAt, "coverage createdAt"),
    lastSequence: safeInteger(wire.lastSequence, "coverage lastSequence"),
    ...(wire.outcome === undefined ? {} : { outcome: wire.outcome }),
    ...(wire.reason === undefined ? {} : { reason: wire.reason }),
    ...(wire.startedAt === undefined ? {} : { startedAt: date(wire.startedAt, "coverage startedAt") }),
    ...(wire.finishedAt === undefined ? {} : { finishedAt: date(wire.finishedAt, "coverage finishedAt") }),
    ...(wire.reportId === undefined ? {} : { reportId: wire.reportId })
  } as CoverageRun;
}

export function decodeCoverageRunPage(value: unknown): CoverageRunPage {
  const wire = record(value, "coverage run page");
  return {
    items: (wire.items as unknown[]).map((item) => decodeCoverageRun(item)),
    ...(wire.nextCursor === undefined ? {} : { nextCursor: wire.nextCursor })
  } as CoverageRunPage;
}

export function decodeCoverageReport(value: unknown): CoverageReport {
  const wire = record(value, "coverage report");
  return {
    reportId: wire.reportId as string,
    coverageRunId: wire.coverageRunId as string,
    testRunId: wire.testRunId as string,
    schemaVersion: wire.schemaVersion as CoverageReport["schemaVersion"],
    createdAt: date(wire.createdAt, "coverage report createdAt"),
    completeness: decodeCoverageCompleteness(wire.completeness),
    summary: decodeCoverageSummary(wire.summary),
    toolProvenance: decodeCoverageToolProvenance(wire.toolProvenance),
    artifactId: wire.artifactId as string,
    ...(wire.sources === undefined
      ? {}
      : { sources: (wire.sources as Record<string, unknown>[]).map((source) => ({ uri: source.uri as string, sha256: source.sha256 as string })) })
  };
}
