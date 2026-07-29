import test from "node:test";
import assert from "node:assert/strict";
import type { ArtifactMetadata, ArtifactMetadataV12, Capabilities, CapabilitiesV12, Diagnostic, TargetList, TaskEvent, TaskEventV12, TaskSnapshot, TaskSnapshotV12, WorkspaceSnapshot } from "./index.js";
import { ArtifactKind, MIMEType } from "./generated/artifact.js";
import { Event, EventKind, ProtocolVersion } from "./generated/event.js";
import { Outcome, Scenario, Status, TaskKind } from "./generated/task.js";
import { Severity } from "./generated/diagnostic.js";
import { SimulationScenarioV12, TaskKindV12, TaskStatusV12 } from "./generated/task-v1-2.js";
import { EventKindV12, EventProtocolVersionV12, TaskEventDiagnosticSeverityV12, TaskEventNameV12, TaskOutputStreamV12, TaskStepKindV12, TaskStepStatusV12 } from "./generated/event-v1-2.js";
import { Kind as ArtifactKindV12, MIMEType as ArtifactMIMETypeV12 } from "./generated/artifact-v1-2.js";

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

  assert.equal(artifact.uri, "file:///workspace/task-summary.json");
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
