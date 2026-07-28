import test from "node:test";
import assert from "node:assert/strict";
import type { ArtifactMetadata, ArtifactMetadataV12, Capabilities, CapabilitiesV12, Diagnostic, TargetList, TaskEvent, TaskEventV12, TaskSnapshot, TaskSnapshotV12, WorkspaceSnapshot } from "./index.js";
import { ArtifactKind, MIMEType } from "./generated/artifact.js";
import { Event, EventKind, ProtocolVersion } from "./generated/event.js";
import { Outcome, Scenario, Status, TaskKind } from "./generated/task.js";
import { Severity } from "./generated/diagnostic.js";
import { Kind as TaskKindV12, Status as TaskStatusV12 } from "./generated/task-v1-2.js";
import { Event as TaskEventNameV12, Kind as TaskEventKindV12, ProtocolVersion as TaskProtocolVersionV12 } from "./generated/event-v1-2.js";
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
    status: TaskStatusV12.Queued,
    createdAt: new Date("2026-07-26T00:00:00Z"),
    lastSequence: 1
  };
  const event: TaskEventV12 = {
    protocolVersion: TaskProtocolVersionV12.The12,
    kind: TaskEventKindV12.Event,
    messageId: task.taskId,
    sentAt: new Date("2026-07-26T00:00:00Z"),
    sequence: 1,
    event: TaskEventNameV12.TaskDiagnostic,
    taskId: task.taskId,
    payloadVersion: 1,
    payload: { diagnostic }
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
});
