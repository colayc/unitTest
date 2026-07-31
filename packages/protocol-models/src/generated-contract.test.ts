import test from "node:test";
import assert from "node:assert/strict";
import type { ArtifactMetadata, ArtifactMetadataV12, Capabilities, CapabilitiesV12, Diagnostic, TargetList, TaskEvent, TaskEventV12, TaskSnapshot, TaskSnapshotV12, WorkspaceSnapshot } from "./index.js";
import { ArtifactKind, MIMEType } from "./generated/artifact.js";
import { Event, EventKind, ProtocolVersion } from "./generated/event.js";
import { Outcome, Scenario, Status, TaskKind } from "./generated/task.js";
import { Severity } from "./generated/diagnostic.js";
import { CoverageDriver, Family, Generator, TArchitecture } from "./generated/workspace.js";
import { SimulationScenarioV12, TaskKindV12, TaskStatusV12 } from "./generated/task-v1-2.js";
import { EventKindV12, EventProtocolVersionV12, TaskEventDiagnosticSeverityV12, TaskEventNameV12, TaskOutputStreamV12, TaskStepKindV12, TaskStepStatusV12 } from "./generated/event-v1-2.js";
import { Kind as ArtifactKindV12, MIMEType as ArtifactMIMETypeV12 } from "./generated/artifact-v1-2.js";
import type {
  ArtifactMetadataV13,
  CapabilitiesV13,
  DiagnosticV13,
  TaskEventV13,
  TaskSnapshotV13,
  TestCatalog,
  TestItemResult,
  TestRun,
  TestSelection
} from "./index.js";
import { TaskOutcomeV13 } from "./index.js";
import {
  TestFrameworkV13,
  TestItemKindV13,
  TestItemOutcomeV13,
  TestRunOutcomeV13,
  TestRunStatusV13,
  TestSelectionModeV13
} from "./generated/test-v1-3.js";
import {
  SimulationScenarioV13,
  TaskKindV13,
  TaskStatusV13
} from "./generated/task-v1-3.js";
import {
  EventKindV13,
  EventProtocolVersionV13,
  TaskEventNameV13
} from "./generated/event-v1-3.js";
import {
  ArtifactKindV13,
  ArtifactMIMETypeV13
} from "./generated/artifact-v1-3.js";
import {
  DiagnosticCategoryV13,
  DiagnosticSeverityV13
} from "./generated/diagnostic-v1-3.js";

test("generated capabilities represent an empty Windows service", () => {
  const value: Capabilities = {
    platform: "windows",
    transports: ["named-pipe"],
    toolchains: [],
    frameworks: [],
    coverageTools: []
  };
  assert.equal(value.platform, "windows");
});

test("generated protocol 1.1 models represent finished tasks, events, and artifacts", () => {
  const task: TaskSnapshot = {
    taskId: "0123456789abcdef0123456789abcdef",
    kind: TaskKind.Simulation,
    scenario: Scenario.Success,
    status: Status.Finished,
    outcome: Outcome.Cancelled,
    createdAt: new Date("2026-07-22T00:00:00Z"),
    finishedAt: new Date("2026-07-22T00:00:01Z"),
    lastSequence: 3
  };
  const event: TaskEvent = {
    protocolVersion: ProtocolVersion.The11,
    kind: EventKind.Event,
    messageId: "fedcba9876543210fedcba9876543210",
    sentAt: new Date("2026-07-22T00:00:01Z"),
    sequence: 3,
    event: Event.TaskFinished,
    taskId: task.taskId,
    payloadVersion: 1,
    payload: {}
  };
  const artifact: ArtifactMetadata = {
    artifactId: "abcdef0123456789abcdef0123456789",
    taskId: task.taskId,
    kind: ArtifactKind.TaskSummary,
    mimeType: MIMEType.ApplicationJSON,
    sizeBytes: 128,
    sha256: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
    createdAt: new Date("2026-07-22T00:00:01Z")
  };

  assert.equal(event.event, Event.TaskFinished);
  assert.equal(artifact.kind, ArtifactKind.TaskSummary);
});

test("generated protocol 1.2 models expose workspace build contracts", () => {
  const capabilities: CapabilitiesV12 = { workspaceInspect: true, targetList: true, cmakeBuild: true };
  const workspace: WorkspaceSnapshot = {
    workspaceUri: "file:///workspace",
    workspaceGeneration: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
    capabilities,
    diagnostics: [],
    toolchains: [{
      toolchainId: "gcc-test",
      family: Family.GCC,
      version: "15.1.0",
      targetTriple: "x86_64-linux-gnu",
      hostArchitecture: TArchitecture.X64,
      targetArchitecture: TArchitecture.X64,
      generators: [Generator.Ninja],
      capabilities: { coverageDrivers: [CoverageDriver.Gcov] }
    }],
    projects: []
  };
  const targets: TargetList = {
    workspaceGeneration: workspace.workspaceGeneration,
    projectId: "example-project",
    buildProfileId: "fedcba9876543210fedcba9876543210fedcba9876543210fedcba9876543210",
    targets: []
  };
  const diagnostic: Diagnostic = { severity: Severity.Error, code: "CMAKE_ERROR", message: "configuration failed" };
  const task: TaskSnapshotV12 = {
    taskId: "fedcba9876543210fedcba9876543210",
    kind: TaskKindV12.CmakeBuild,
    workspaceGeneration: workspace.workspaceGeneration,
    projectId: targets.projectId,
    buildProfileId: "fedcba9876543210fedcba9876543210fedcba9876543210fedcba9876543210",
    targetIds: [],
    jobs: 4,
    timeoutMs: 30000,
    status: TaskStatusV12.Queued,
    createdAt: new Date("2026-07-26T00:00:00Z"),
    lastSequence: 1
  };
  const event: TaskEventV12 = {
    protocolVersion: EventProtocolVersionV12.The12,
    kind: EventKindV12.Event,
    messageId: task.taskId,
    sentAt: new Date("2026-07-26T00:00:00Z"),
    sequence: 1,
    event: TaskEventNameV12.TaskDiagnostic,
    taskId: task.taskId,
    payloadVersion: 1,
    payload: { diagnostic: { severity: TaskEventDiagnosticSeverityV12.Error, code: diagnostic.code, message: diagnostic.message } }
  };
  const outputEvent: TaskEventV12 = {
    ...event,
    sequence: 2,
    event: TaskEventNameV12.TaskOutput,
    payload: {
      stepId: "configure",
      stream: TaskOutputStreamV12.Stdout,
      text: "configured",
      truncated: false
    }
  };
  const simulationStepEvent: TaskEventV12 = {
    ...event,
    sequence: 3,
    event: TaskEventNameV12.TaskStepStarted,
    payload: {
      stepId: "simulate",
      kind: TaskStepKindV12.Simulation,
      status: TaskStepStatusV12.Running
    }
  };
  const artifact: ArtifactMetadataV12 = {
    artifactId: task.taskId,
    taskId: task.taskId,
    kind: ArtifactKindV12.TaskSummary,
    mimeType: ArtifactMIMETypeV12.ApplicationJSON,
    sizeBytes: 0,
    sha256: workspace.workspaceGeneration,
    createdAt: event.sentAt,
    uri: "file:///workspace/task-summary.json"
  };
  const buildArtifactKinds: ArtifactKindV12[] = [
    ArtifactKindV12.BuildSummary,
    ArtifactKindV12.ExecutionPlan,
    ArtifactKindV12.Diagnostics,
    ArtifactKindV12.Stdout,
    ArtifactKindV12.Stderr
  ];
  const buildArtifactMIMETypes: ArtifactMIMETypeV12[] = [
    ArtifactMIMETypeV12.ApplicationJSON,
    ArtifactMIMETypeV12.ApplicationXNdjson,
    ArtifactMIMETypeV12.ApplicationOctetStream
  ];

  assert.equal(artifact.uri, "file:///workspace/task-summary.json");
  assert.equal(buildArtifactKinds.length, 5);
  assert.equal(buildArtifactMIMETypes.length, 3);
  assert.equal(outputEvent.event, TaskEventNameV12.TaskOutput);
  assert.equal(simulationStepEvent.payload.kind, TaskStepKindV12.Simulation);
});

test("generated protocol 1.2 models preserve discriminated branches", () => {
  // @ts-expect-error cmakeBuild requires its workspace build identifiers.
  const missingCmakeFields: TaskSnapshotV12 = {
    taskId: "fedcba9876543210fedcba9876543210",
    kind: TaskKindV12.CmakeBuild,
    status: TaskStatusV12.Queued,
    createdAt: new Date("2026-07-26T00:00:00Z"),
    lastSequence: 1
  };
  // @ts-expect-error simulation must reject cmakeBuild-only fields.
  const simulationWithBuildField: TaskSnapshotV12 = {
    taskId: "fedcba9876543210fedcba9876543210",
    kind: TaskKindV12.Simulation,
    scenario: SimulationScenarioV12.Success,
    workspaceGeneration: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
    status: TaskStatusV12.Queued,
    createdAt: new Date("2026-07-26T00:00:00Z"),
    lastSequence: 1
  };
  const diagnosticWithoutPayload: TaskEventV12 = {
    protocolVersion: EventProtocolVersionV12.The12,
    kind: EventKindV12.Event,
    messageId: "fedcba9876543210fedcba9876543210",
    sentAt: new Date("2026-07-26T00:00:00Z"),
    sequence: 1,
    event: TaskEventNameV12.TaskDiagnostic,
    taskId: "fedcba9876543210fedcba9876543210",
    payloadVersion: 1,
    // @ts-expect-error task.diagnostic requires diagnostic payload.
    payload: {}
  };

  assert.equal(missingCmakeFields.kind, TaskKindV12.CmakeBuild);
  assert.equal(simulationWithBuildField.kind, TaskKindV12.Simulation);
  assert.equal(diagnosticWithoutPayload.event, TaskEventNameV12.TaskDiagnostic);
});

test("generated protocol 1.3 models expose closed test contracts", () => {
  const stableContainerId = `utid-v1-${"a".repeat(64)}`;
  const stableItemId = `utid-v1-${"b".repeat(64)}`;
  const profileId = "c".repeat(64);
  const revision = "d".repeat(64);
  const selection: TestSelection = {
    mode: TestSelectionModeV13.Items,
    itemIds: [stableItemId]
  };
  const catalog: TestCatalog = {
    projectId: "core",
    profileId,
    revision,
    generatedAt: new Date("2026-07-31T00:00:00Z"),
    containers: [{
      id: stableContainerId,
      projectId: "core",
      ctestLogicalName: "core.cpputest",
      displayName: "Core CppUTest",
      framework: TestFrameworkV13.Cpputest,
      capabilities: {
        canDiscoverCases: true,
        canRunCase: true,
        canReportSkipped: true,
        canReportSourceLocation: true,
        canReportMockDetails: true
      },
      labels: [],
      disabled: false
    }],
    items: [{
      id: stableItemId,
      containerId: stableContainerId,
      kind: TestItemKindV13.Case,
      framework: TestFrameworkV13.Cpputest,
      logicalName: "adds_numbers",
      displayName: "adds_numbers",
      labels: [],
      disabled: false
    }],
    diagnostics: [],
    partial: false
  };
  const result: TestItemResult = {
    itemId: stableItemId,
    containerId: stableContainerId,
    iteration: 1,
    outcome: TestItemOutcomeV13.Passed,
    failureDetails: [],
    outputRefs: [],
    partial: false
  };
  const run: TestRun = {
    runId: "1".repeat(32),
    taskId: "2".repeat(32),
    projectId: "core",
    profileId,
    toolchainId: "linux-clang",
    catalogRevision: revision,
    selectionSnapshot: {
      mode: TestSelectionModeV13.Items,
      containerIds: [],
      itemIds: [stableItemId]
    },
    status: TestRunStatusV13.Completed,
    outcome: TestRunOutcomeV13.Passed,
    summary: {
      total: 1,
      completed: 1,
      passed: 1,
      failed: 0,
      skipped: 0,
      errored: 0,
      cancelled: 0,
      timedOut: 0,
      notRun: 0,
      iterations: 1
    },
    resultRevision: revision,
    incomplete: false
  };
  const task: TaskSnapshotV13 = {
    taskId: run.taskId,
    kind: TaskKindV13.TestRun,
    projectId: run.projectId,
    profileId,
    catalogRevision: revision,
    runId: run.runId,
    repeatCount: 1,
    status: TaskStatusV13.Finished,
    createdAt: new Date("2026-07-31T00:00:00Z"),
    lastSequence: 7
  };
  const event: TaskEventV13 = {
    protocolVersion: EventProtocolVersionV13.The13,
    kind: EventKindV13.Event,
    messageId: "3".repeat(32),
    sentAt: new Date("2026-07-31T00:00:01Z"),
    sequence: 7,
    event: TaskEventNameV13.TestItemFinished,
    taskId: task.taskId,
    payloadVersion: 1,
    payload: { runId: run.runId, result }
  };
  const finishedEvent: TaskEventV13 = {
    protocolVersion: EventProtocolVersionV13.The13,
    kind: EventKindV13.Event,
    messageId: "6".repeat(32),
    sentAt: new Date("2026-07-31T00:00:02Z"),
    sequence: 8,
    event: TaskEventNameV13.TaskFinished,
    taskId: task.taskId,
    payloadVersion: 1,
    payload: { outcome: TaskOutcomeV13.Succeeded }
  };
  const diagnostic: DiagnosticV13 = {
    severity: DiagnosticSeverityV13.Error,
    category: DiagnosticCategoryV13.AssertionFailure,
    code: "ASSERTION_FAILED",
    message: "expected 4 but got 5"
  };
  const artifact: ArtifactMetadataV13 = {
    artifactId: "4".repeat(32),
    taskId: task.taskId,
    kind: ArtifactKindV13.TestResults,
    mimeType: ArtifactMIMETypeV13.ApplicationJSON,
    sizeBytes: 2,
    sha256: "5".repeat(64),
    createdAt: event.sentAt,
    uri: "unit-test-ide://artifact/44444444444444444444444444444444"
  };
  const capabilities: CapabilitiesV13 = {
    workspaceInspect: true,
    targetList: true,
    cmakeBuild: true,
    testDiscovery: true,
    testRun: true,
    frameworkAdapters: [],
    opaqueCTestFallback: true,
    ctestJson: true,
    maxRepeatCount: 100,
    maxSelectionSize: 100000,
    maxCatalogPageSize: 1000,
    unityHelperContractVersion: "1",
    unityRunnerContractVersion: "utide.runner.v1"
  };

  assert.equal(selection.mode, TestSelectionModeV13.Items);
  assert.equal(catalog.items[0]!.kind, TestItemKindV13.Case);
  assert.equal(event.event, TaskEventNameV13.TestItemFinished);
  assert.equal(finishedEvent.payload.outcome, TaskOutcomeV13.Succeeded);
  assert.equal(diagnostic.category, DiagnosticCategoryV13.AssertionFailure);
  assert.equal(artifact.kind, ArtifactKindV13.TestResults);
  assert.equal(capabilities.maxSelectionSize, 100000);
});

test("generated protocol 1.3 selections and tasks preserve discriminated branches", () => {
  // @ts-expect-error failedFromRun accepts only runId.
  const failedRunWithItems: TestSelection = {
    mode: TestSelectionModeV13.FailedFromRun,
    runId: "1".repeat(32),
    itemIds: []
  };
  // @ts-expect-error testRun task requires run and catalog identity.
  const testRunWithoutIdentity: TaskSnapshotV13 = {
    taskId: "2".repeat(32),
    kind: TaskKindV13.TestRun,
    status: TaskStatusV13.Queued,
    createdAt: new Date("2026-07-31T00:00:00Z"),
    lastSequence: 1
  };
  const simulation: TaskSnapshotV13 = {
    taskId: "3".repeat(32),
    kind: TaskKindV13.Simulation,
    scenario: SimulationScenarioV13.Success,
    status: TaskStatusV13.Finished,
    createdAt: new Date("2026-07-31T00:00:00Z"),
    lastSequence: 1
  };

  assert.equal(failedRunWithItems.mode, TestSelectionModeV13.FailedFromRun);
  assert.equal(testRunWithoutIdentity.kind, TaskKindV13.TestRun);
  assert.equal(simulation.kind, TaskKindV13.Simulation);
});
