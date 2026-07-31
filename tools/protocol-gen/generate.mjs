import { spawnSync } from "node:child_process";
import { mkdtemp, mkdir, readFile, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { dirname, join, resolve } from "node:path";

const check = process.argv.includes("--check");
const root = resolve(import.meta.dirname, "../..");
const quicktype = join(root, "node_modules", "quicktype", "dist", "index.js");
const models = [
  { directory: "v1", schema: "capabilities.schema.json", top: "Capabilities", ts: "capabilities.ts", go: "generated.go" },
  { directory: "v1.1", schema: "capabilities.schema.json", top: "CapabilitiesV11", ts: "capabilities-v1-1.ts", go: "capabilities_v11_generated.go" },
  { directory: "v1.1", schema: "task.schema.json", top: "TaskSnapshot", ts: "task.ts", go: "task_generated.go" },
  { directory: "v1.1", schema: "event.schema.json", top: "TaskEvent", ts: "event.ts", go: "event_generated.go" },
  { directory: "v1.1", schema: "artifact.schema.json", top: "ArtifactMetadata", ts: "artifact.ts", go: "artifact_generated.go" },
  { directory: "v1.2", schema: "capabilities.schema.json", top: "CapabilitiesV12", ts: "capabilities-v1-2.ts", go: "v1_2/capabilities/generated.go", goPackage: "protocolmodelv12capabilities" },
  { directory: "v1.2", schema: "diagnostic.schema.json", top: "Diagnostic", ts: "diagnostic.ts", go: "v1_2/diagnostic/generated.go", goPackage: "protocolmodelv12diagnostic" },
  { directory: "v1.2", schema: "workspace.schema.json", top: "WorkspaceSnapshot", ts: "workspace.ts", go: "v1_2/workspace/generated.go", goPackage: "protocolmodelv12workspace" },
  { directory: "v1.2", schema: "workspace.schema.json", definition: "targetList", top: "TargetList", ts: "target-list.ts", go: "v1_2/targetlist/generated.go", goPackage: "protocolmodelv12targetlist" },
  { directory: "v1.2", schema: "task.schema.json", top: "TaskSnapshotV12", template: "task", ts: "task-v1-2.ts", go: "v1_2/task/generated.go", goPackage: "protocolmodelv12task" },
  { directory: "v1.2", schema: "event.schema.json", top: "TaskEventV12", template: "event", ts: "event-v1-2.ts", go: "v1_2/event/generated.go", goPackage: "protocolmodelv12event" },
  { directory: "v1.2", schema: "artifact.schema.json", top: "ArtifactMetadataV12", ts: "artifact-v1-2.ts", go: "v1_2/artifact/generated.go", goPackage: "protocolmodelv12artifact" },
  { directory: "v1.3", schema: "capabilities.schema.json", top: "CapabilitiesV13", ts: "capabilities-v1-3.ts", go: "v1_3/capabilities/generated.go", goPackage: "protocolmodelv13capabilities" },
  { directory: "v1.3", schema: "diagnostic.schema.json", top: "DiagnosticV13", ts: "diagnostic-v1-3.ts", go: "v1_3/diagnostic/generated.go", goPackage: "protocolmodelv13diagnostic" },
  { directory: "v1.3", schema: "test.schema.json", top: "TestContractV13", bundle: ["diagnostic.schema.json"], template: "testV13", ts: "test-v1-3.ts", go: "v1_3/test/generated.go", goPackage: "protocolmodelv13test" },
  { directory: "v1.3", schema: "task.schema.json", top: "TaskSnapshotV13", template: "taskV13", ts: "task-v1-3.ts", go: "v1_3/task/generated.go", goPackage: "protocolmodelv13task" },
  { directory: "v1.3", schema: "event.schema.json", top: "TaskEventV13", bundle: ["diagnostic.schema.json", "test.schema.json"], template: "eventV13", ts: "event-v1-3.ts", go: "v1_3/event/generated.go", goPackage: "protocolmodelv13event" },
  { directory: "v1.3", schema: "artifact.schema.json", top: "ArtifactMetadataV13", ts: "artifact-v1-3.ts", go: "v1_3/artifact/generated.go", goPackage: "protocolmodelv13artifact" }
];
const typescriptTemplates = {
  task: `export interface TaskSnapshotBaseV12 { taskId: string; status: TaskStatusV12; createdAt: Date; lastSequence: number; outcome?: TaskOutcomeV12; startedAt?: Date; finishedAt?: Date; errorCode?: string; errorMessage?: string; }\nexport interface CmakeBuildTaskSnapshotV12 extends TaskSnapshotBaseV12 { kind: TaskKindV12.CmakeBuild; workspaceGeneration: string; projectId: string; buildProfileId: string; targetIds: string[]; jobs: number; timeoutMs: number; scenario?: never; }\nexport interface SimulationTaskSnapshotV12 extends TaskSnapshotBaseV12 { kind: TaskKindV12.Simulation; scenario: SimulationScenarioV12; timeoutMs?: number; workspaceGeneration?: never; projectId?: never; buildProfileId?: never; targetIds?: never; jobs?: never; }\nexport type TaskSnapshotV12 = CmakeBuildTaskSnapshotV12 | SimulationTaskSnapshotV12;\nexport enum TaskKindV12 { CmakeBuild = "cmakeBuild", Simulation = "simulation" }\nexport enum TaskStatusV12 { Queued = "queued", Running = "running", Cancelling = "cancelling", Finished = "finished" }\nexport enum TaskOutcomeV12 { Succeeded = "succeeded", CommandFailed = "command_failed", Cancelled = "cancelled", TimedOut = "timed_out", Interrupted = "interrupted", InfrastructureFailed = "infrastructure_failed" }\nexport enum SimulationScenarioV12 { Success = "success", ExitNonzero = "exit-nonzero", Hang = "hang", SpawnChild = "spawn-child", EmitOutput = "emit-output" }\n`,
  event: `export interface TaskEventBaseV12 { protocolVersion: EventProtocolVersionV12; kind: EventKindV12.Event; messageId: string; sentAt: Date; sequence: number; taskId: string; payloadVersion: 1; }
export interface TaskCreatedEventV12 extends TaskEventBaseV12 { event: TaskEventNameV12.TaskCreated; payload: { status: "queued" }; }
export interface TaskStartedEventV12 extends TaskEventBaseV12 { event: TaskEventNameV12.TaskStarted; payload: { status: "running" }; }
export interface TaskStepStartedEventV12 extends TaskEventBaseV12 { event: TaskEventNameV12.TaskStepStarted; payload: TaskStepStartedPayloadV12; }
export interface TaskOutputEventV12 extends TaskEventBaseV12 { event: TaskEventNameV12.TaskOutput; payload: TaskOutputPayloadV12; }
export interface TaskStepFinishedEventV12 extends TaskEventBaseV12 { event: TaskEventNameV12.TaskStepFinished; payload: TaskStepFinishedPayloadV12; }
export interface TaskCancellationRequestedEventV12 extends TaskEventBaseV12 { event: TaskEventNameV12.TaskCancellationRequested; payload: { status: "cancelling" }; }
export interface ArtifactCreatedEventV12 extends TaskEventBaseV12 { event: TaskEventNameV12.ArtifactCreated; payload: ArtifactCreatedPayloadV12; }
export interface TaskFinishedEventV12 extends TaskEventBaseV12 { event: TaskEventNameV12.TaskFinished; payload: { outcome: TaskOutcomeV12 }; }
export interface TaskDiagnosticEventV12 extends TaskEventBaseV12 { event: TaskEventNameV12.TaskDiagnostic; payload: TaskDiagnosticPayloadV12; }
export type TaskEventV12 = TaskCreatedEventV12 | TaskStartedEventV12 | TaskStepStartedEventV12 | TaskOutputEventV12 | TaskStepFinishedEventV12 | TaskCancellationRequestedEventV12 | ArtifactCreatedEventV12 | TaskFinishedEventV12 | TaskDiagnosticEventV12;
export interface TaskStepStartedPayloadV12 { stepId: string; kind: TaskStepKindV12; status: TaskStepStatusV12.Running; }
export interface TaskOutputPayloadV12 { stepId: string; stream: TaskOutputStreamV12; text: string; truncated: boolean; }
export interface TaskStepFinishedPayloadV12 { stepId: string; kind: TaskStepKindV12; status: TaskStepStatusV12.Succeeded | TaskStepStatusV12.Failed | TaskStepStatusV12.Skipped; exitCode?: number; errorCode?: string; }
export interface ArtifactCreatedPayloadV12 { artifactId: string; kind: string; }
export interface TaskDiagnosticPayloadV12 { diagnostic: TaskEventDiagnosticV12; }
export interface TaskEventDiagnosticV12 { severity: TaskEventDiagnosticSeverityV12; code: string; message: string; sourceUri?: string; line?: number; column?: number; }
export enum EventProtocolVersionV12 { The12 = "1.2" }
export enum EventKindV12 { Event = "event" }
export enum TaskEventNameV12 { TaskCreated = "task.created", TaskStarted = "task.started", TaskStepStarted = "task.step_started", TaskOutput = "task.output", TaskStepFinished = "task.step_finished", TaskCancellationRequested = "task.cancellation_requested", ArtifactCreated = "artifact.created", TaskFinished = "task.finished", TaskDiagnostic = "task.diagnostic" }
export enum TaskStepKindV12 { Simulation = "simulation", Configure = "configure", Build = "build" }
export enum TaskStepStatusV12 { Running = "running", Succeeded = "succeeded", Failed = "failed", Skipped = "skipped" }
export enum TaskOutputStreamV12 { Stdout = "stdout", Stderr = "stderr", Combined = "combined" }
export enum TaskOutcomeV12 { Succeeded = "succeeded", CommandFailed = "command_failed", Cancelled = "cancelled", TimedOut = "timed_out", Interrupted = "interrupted", InfrastructureFailed = "infrastructure_failed" }
export enum TaskEventDiagnosticSeverityV12 { Error = "error", Warning = "warning", Info = "info" }
`,
  taskV13: `export interface TaskSnapshotBaseV13 { taskId: string; status: TaskStatusV13; createdAt: Date; lastSequence: number; outcome?: TaskOutcomeV13; startedAt?: Date; finishedAt?: Date; errorCode?: string; errorMessage?: string; }
export interface CmakeBuildTaskSnapshotV13 extends TaskSnapshotBaseV13 { kind: TaskKindV13.CmakeBuild; workspaceGeneration: string; projectId: string; buildProfileId: string; targetIds: string[]; jobs: number; timeoutMs: number; scenario?: never; profileId?: never; catalogRevision?: never; runId?: never; repeatCount?: never; }
export interface SimulationTaskSnapshotV13 extends TaskSnapshotBaseV13 { kind: TaskKindV13.Simulation; scenario: SimulationScenarioV13; timeoutMs?: number; workspaceGeneration?: never; projectId?: never; buildProfileId?: never; targetIds?: never; jobs?: never; profileId?: never; catalogRevision?: never; runId?: never; repeatCount?: never; }
export interface TestDiscoveryTaskSnapshotV13 extends TaskSnapshotBaseV13 { kind: TaskKindV13.TestDiscovery; projectId: string; profileId: string; catalogRevision?: string; scenario?: never; workspaceGeneration?: never; buildProfileId?: never; targetIds?: never; jobs?: never; timeoutMs?: never; runId?: never; repeatCount?: never; }
export interface TestRunTaskSnapshotV13 extends TaskSnapshotBaseV13 { kind: TaskKindV13.TestRun; projectId: string; profileId: string; catalogRevision: string; runId: string; repeatCount: number; scenario?: never; workspaceGeneration?: never; buildProfileId?: never; targetIds?: never; jobs?: never; timeoutMs?: never; }
export type TaskSnapshotV13 = CmakeBuildTaskSnapshotV13 | SimulationTaskSnapshotV13 | TestDiscoveryTaskSnapshotV13 | TestRunTaskSnapshotV13;
export enum TaskKindV13 { CmakeBuild = "cmakeBuild", Simulation = "simulation", TestDiscovery = "testDiscovery", TestRun = "testRun" }
export enum TaskStatusV13 { Queued = "queued", Running = "running", Cancelling = "cancelling", Finished = "finished" }
export enum TaskOutcomeV13 { Succeeded = "succeeded", CommandFailed = "command_failed", Cancelled = "cancelled", TimedOut = "timed_out", Interrupted = "interrupted", InfrastructureFailed = "infrastructure_failed" }
export enum SimulationScenarioV13 { Success = "success", ExitNonzero = "exit-nonzero", Hang = "hang", SpawnChild = "spawn-child", EmitOutput = "emit-output" }
`,
  eventV13: `import type { DiagnosticV13 } from "./diagnostic-v1-3.js";
import type { TaskOutcomeV13 } from "./task-v1-3.js";
import type { TestItemResult, TestRunOutcomeV13, TestRunSummaryV13 } from "./test-v1-3.js";

export interface TaskEventBaseV13 { protocolVersion: EventProtocolVersionV13; kind: EventKindV13.Event; messageId: string; sentAt: Date; sequence: number; taskId: string; payloadVersion: 1; }
export interface TaskCreatedEventV13 extends TaskEventBaseV13 { event: TaskEventNameV13.TaskCreated; payload: { status: "queued" }; }
export interface TaskStartedEventV13 extends TaskEventBaseV13 { event: TaskEventNameV13.TaskStarted; payload: { status: "running" }; }
export interface TaskStepStartedEventV13 extends TaskEventBaseV13 { event: TaskEventNameV13.TaskStepStarted; payload: TaskStepStartedPayloadV13; }
export interface TaskOutputEventV13 extends TaskEventBaseV13 { event: TaskEventNameV13.TaskOutput; payload: TaskOutputPayloadV13; }
export interface TaskStepFinishedEventV13 extends TaskEventBaseV13 { event: TaskEventNameV13.TaskStepFinished; payload: TaskStepFinishedPayloadV13; }
export interface TaskCancellationRequestedEventV13 extends TaskEventBaseV13 { event: TaskEventNameV13.TaskCancellationRequested; payload: { status: "cancelling" }; }
export interface ArtifactCreatedEventV13 extends TaskEventBaseV13 { event: TaskEventNameV13.ArtifactCreated; payload: ArtifactCreatedPayloadV13; }
export interface TaskFinishedEventV13 extends TaskEventBaseV13 { event: TaskEventNameV13.TaskFinished; payload: { outcome: TaskOutcomeV13 }; }
export interface TaskDiagnosticEventV13 extends TaskEventBaseV13 { event: TaskEventNameV13.TaskDiagnostic; payload: { diagnostic: DiagnosticV13 }; }
export interface TestDiscoveryStartedEventV13 extends TaskEventBaseV13 { event: TaskEventNameV13.TestDiscoveryStarted; payload: TestDiscoveryStartedPayloadV13; }
export interface TestContainerDiscoveredEventV13 extends TaskEventBaseV13 { event: TaskEventNameV13.TestContainerDiscovered; payload: TestContainerDiscoveredPayloadV13; }
export interface TestCatalogPublishedEventV13 extends TaskEventBaseV13 { event: TaskEventNameV13.TestCatalogPublished; payload: TestCatalogPublishedPayloadV13; }
export interface TestRunStartedEventV13 extends TaskEventBaseV13 { event: TaskEventNameV13.TestRunStarted; payload: TestRunStartedPayloadV13; }
export interface TestContainerStartedEventV13 extends TaskEventBaseV13 { event: TaskEventNameV13.TestContainerStarted; payload: TestContainerStartedPayloadV13; }
export interface TestItemStartedEventV13 extends TaskEventBaseV13 { event: TaskEventNameV13.TestItemStarted; payload: TestItemStartedPayloadV13; }
export interface TestOutputEventV13 extends TaskEventBaseV13 { event: TaskEventNameV13.TestOutput; payload: TestOutputPayloadV13; }
export interface TestItemFinishedEventV13 extends TaskEventBaseV13 { event: TaskEventNameV13.TestItemFinished; payload: { runId: string; result: TestItemResult }; }
export interface TestContainerFinishedEventV13 extends TaskEventBaseV13 { event: TaskEventNameV13.TestContainerFinished; payload: TestContainerFinishedPayloadV13; }
export interface TestRunFinishedEventV13 extends TaskEventBaseV13 { event: TaskEventNameV13.TestRunFinished; payload: TestRunFinishedPayloadV13; }
export type TaskEventV13 = TaskCreatedEventV13 | TaskStartedEventV13 | TaskStepStartedEventV13 | TaskOutputEventV13 | TaskStepFinishedEventV13 | TaskCancellationRequestedEventV13 | ArtifactCreatedEventV13 | TaskFinishedEventV13 | TaskDiagnosticEventV13 | TestDiscoveryStartedEventV13 | TestContainerDiscoveredEventV13 | TestCatalogPublishedEventV13 | TestRunStartedEventV13 | TestContainerStartedEventV13 | TestItemStartedEventV13 | TestOutputEventV13 | TestItemFinishedEventV13 | TestContainerFinishedEventV13 | TestRunFinishedEventV13;
export interface TaskStepStartedPayloadV13 { stepId: string; kind: TaskStepKindV13; status: TaskStepStatusV13.Running; }
export interface TaskOutputPayloadV13 { stepId: string; stream: TaskOutputStreamV13; text: string; truncated: boolean; }
export interface TaskStepFinishedPayloadV13 { stepId: string; kind: TaskStepKindV13; status: TaskStepStatusV13.Succeeded | TaskStepStatusV13.Failed | TaskStepStatusV13.Skipped; exitCode?: number; errorCode?: string; }
export interface ArtifactCreatedPayloadV13 { artifactId: string; kind: string; }
export interface TestDiscoveryStartedPayloadV13 { projectId: string; profileId: string; }
export interface TestContainerDiscoveredPayloadV13 { containerId: string; framework: TestEventFrameworkV13; displayName: string; }
export interface TestCatalogPublishedPayloadV13 { projectId: string; profileId: string; revision: string; containerCount: number; itemCount: number; }
export interface TestRunStartedPayloadV13 { runId: string; catalogRevision: string; total: number; }
export interface TestContainerStartedPayloadV13 { runId: string; containerId: string; iteration: number; }
export interface TestItemStartedPayloadV13 { runId: string; itemId: string; containerId: string; iteration: number; }
export interface TestOutputPayloadV13 { runId: string; containerId: string; itemId?: string; iteration: number; stream: TaskOutputStreamV13; text: string; truncated: boolean; }
export interface TestContainerFinishedPayloadV13 { runId: string; containerId: string; iteration: number; outcome: TestContainerOutcomeV13; }
export interface TestRunFinishedPayloadV13 { runId: string; outcome: TestRunOutcomeV13; summary: TestRunSummaryV13; resultRevision: string; incomplete: boolean; }
export enum EventProtocolVersionV13 { The13 = "1.3" }
export enum EventKindV13 { Event = "event" }
export enum TaskEventNameV13 { TaskCreated = "task.created", TaskStarted = "task.started", TaskStepStarted = "task.step_started", TaskOutput = "task.output", TaskStepFinished = "task.step_finished", TaskCancellationRequested = "task.cancellation_requested", ArtifactCreated = "artifact.created", TaskFinished = "task.finished", TaskDiagnostic = "task.diagnostic", TestDiscoveryStarted = "test.discovery.started", TestContainerDiscovered = "test.container.discovered", TestCatalogPublished = "test.catalog.published", TestRunStarted = "test.run.started", TestContainerStarted = "test.container.started", TestItemStarted = "test.item.started", TestOutput = "test.output", TestItemFinished = "test.item.finished", TestContainerFinished = "test.container.finished", TestRunFinished = "test.run.finished" }
export enum TaskStepKindV13 { Simulation = "simulation", Configure = "configure", Build = "build", TestDiscovery = "test-discovery", TestRun = "test-run" }
export enum TaskStepStatusV13 { Running = "running", Succeeded = "succeeded", Failed = "failed", Skipped = "skipped" }
export enum TaskOutputStreamV13 { Stdout = "stdout", Stderr = "stderr", Combined = "combined" }
export enum TestEventFrameworkV13 { Cpputest = "cpputest", Unity = "unity", OpaqueCtest = "opaque-ctest" }
export enum TestContainerOutcomeV13 { Passed = "passed", Failed = "failed", Errored = "errored", Cancelled = "cancelled", TimedOut = "timed_out", NotRun = "not_run" }
`
};
const typescriptTestSelectionV13 = `export interface AllTestSelectionV13 { mode: TestSelectionModeV13.All; containerIds?: never; itemIds?: never; filter?: never; runId?: never; }
export interface ContainersTestSelectionV13 { mode: TestSelectionModeV13.Containers; containerIds: string[]; itemIds?: never; filter?: never; runId?: never; }
export interface ItemsTestSelectionV13 { mode: TestSelectionModeV13.Items; itemIds: string[]; containerIds?: never; filter?: never; runId?: never; }
export interface FilterTestSelectionV13 { mode: TestSelectionModeV13.Filter; filter: TestFilterV13; containerIds?: never; itemIds?: never; runId?: never; }
export interface FailedFromRunTestSelectionV13 { mode: TestSelectionModeV13.FailedFromRun; runId: string; containerIds?: never; itemIds?: never; filter?: never; }
export type TestSelection = AllTestSelectionV13 | ContainersTestSelectionV13 | ItemsTestSelectionV13 | FilterTestSelectionV13 | FailedFromRunTestSelectionV13;
`;
const goUnionBodies = {
  task: `type TaskSnapshotV12 interface{ isTaskSnapshotV12() }
type CmakeBuildTaskSnapshotV12 struct { TaskID string \`json:"taskId"\`; Kind TaskKindV12 \`json:"kind"\`; WorkspaceGeneration string \`json:"workspaceGeneration"\`; ProjectID string \`json:"projectId"\`; BuildProfileID string \`json:"buildProfileId"\`; TargetIDs []string \`json:"targetIds"\`; Jobs int64 \`json:"jobs"\`; TimeoutMS int64 \`json:"timeoutMs"\`; Status TaskStatusV12 \`json:"status"\`; CreatedAt time.Time \`json:"createdAt"\`; LastSequence int64 \`json:"lastSequence"\`; Outcome *TaskOutcomeV12 \`json:"outcome,omitempty"\`; StartedAt *time.Time \`json:"startedAt,omitempty"\`; FinishedAt *time.Time \`json:"finishedAt,omitempty"\`; ErrorCode *string \`json:"errorCode,omitempty"\`; ErrorMessage *string \`json:"errorMessage,omitempty"\` }
func (CmakeBuildTaskSnapshotV12) isTaskSnapshotV12() {}
type SimulationTaskSnapshotV12 struct { TaskID string \`json:"taskId"\`; Kind TaskKindV12 \`json:"kind"\`; Scenario SimulationScenarioV12 \`json:"scenario"\`; Status TaskStatusV12 \`json:"status"\`; CreatedAt time.Time \`json:"createdAt"\`; LastSequence int64 \`json:"lastSequence"\`; TimeoutMS *int64 \`json:"timeoutMs,omitempty"\`; Outcome *TaskOutcomeV12 \`json:"outcome,omitempty"\`; StartedAt *time.Time \`json:"startedAt,omitempty"\`; FinishedAt *time.Time \`json:"finishedAt,omitempty"\`; ErrorCode *string \`json:"errorCode,omitempty"\`; ErrorMessage *string \`json:"errorMessage,omitempty"\` }
func (SimulationTaskSnapshotV12) isTaskSnapshotV12() {}

`,
  event: `type TaskEventV12 interface{ isTaskEventV12() }
type TaskEventBaseV12 struct { ProtocolVersion EventProtocolVersionV12 \`json:"protocolVersion"\`; Kind EventKindV12 \`json:"kind"\`; MessageID string \`json:"messageId"\`; SentAt time.Time \`json:"sentAt"\`; Sequence int64 \`json:"sequence"\`; TaskID string \`json:"taskId"\`; PayloadVersion float64 \`json:"payloadVersion"\` }
type TaskCreatedEventV12 struct { TaskEventBaseV12; Event TaskEventNameV12 \`json:"event"\`; Payload struct { Status string \`json:"status"\` } \`json:"payload"\` }
func (TaskCreatedEventV12) isTaskEventV12() {}
type TaskStartedEventV12 struct { TaskEventBaseV12; Event TaskEventNameV12 \`json:"event"\`; Payload struct { Status string \`json:"status"\` } \`json:"payload"\` }
func (TaskStartedEventV12) isTaskEventV12() {}
type TaskStepStartedEventV12 struct { TaskEventBaseV12; Event TaskEventNameV12 \`json:"event"\`; Payload struct { StepID string \`json:"stepId"\`; Kind TaskStepKindV12 \`json:"kind"\`; Status TaskStepStatusV12 \`json:"status"\` } \`json:"payload"\` }
func (TaskStepStartedEventV12) isTaskEventV12() {}
type TaskOutputEventV12 struct { TaskEventBaseV12; Event TaskEventNameV12 \`json:"event"\`; Payload struct { StepID string \`json:"stepId"\`; Stream TaskOutputStreamV12 \`json:"stream"\`; Text string \`json:"text"\`; Truncated bool \`json:"truncated"\` } \`json:"payload"\` }
func (TaskOutputEventV12) isTaskEventV12() {}
type TaskStepFinishedEventV12 struct { TaskEventBaseV12; Event TaskEventNameV12 \`json:"event"\`; Payload struct { StepID string \`json:"stepId"\`; Kind TaskStepKindV12 \`json:"kind"\`; Status TaskStepStatusV12 \`json:"status"\`; ExitCode *int64 \`json:"exitCode,omitempty"\`; ErrorCode *string \`json:"errorCode,omitempty"\` } \`json:"payload"\` }
func (TaskStepFinishedEventV12) isTaskEventV12() {}
type TaskCancellationRequestedEventV12 struct { TaskEventBaseV12; Event TaskEventNameV12 \`json:"event"\`; Payload struct { Status string \`json:"status"\` } \`json:"payload"\` }
func (TaskCancellationRequestedEventV12) isTaskEventV12() {}
type ArtifactCreatedEventV12 struct { TaskEventBaseV12; Event TaskEventNameV12 \`json:"event"\`; Payload struct { ArtifactID string \`json:"artifactId"\`; Kind string \`json:"kind"\` } \`json:"payload"\` }
func (ArtifactCreatedEventV12) isTaskEventV12() {}
type TaskFinishedEventV12 struct { TaskEventBaseV12; Event TaskEventNameV12 \`json:"event"\`; Payload struct { Outcome TaskOutcomeV12 \`json:"outcome"\` } \`json:"payload"\` }
func (TaskFinishedEventV12) isTaskEventV12() {}
type TaskDiagnosticEventV12 struct { TaskEventBaseV12; Event TaskEventNameV12 \`json:"event"\`; Payload struct { Diagnostic TaskEventDiagnosticV12 \`json:"diagnostic"\` } \`json:"payload"\` }
func (TaskDiagnosticEventV12) isTaskEventV12() {}
type TaskStepKindV12 string
const ( StepSimulation TaskStepKindV12 = "simulation"; StepConfigure TaskStepKindV12 = "configure"; StepBuild TaskStepKindV12 = "build" )
type TaskStepStatusV12 string
const ( StepRunning TaskStepStatusV12 = "running"; StepSucceeded TaskStepStatusV12 = "succeeded"; StepFailed TaskStepStatusV12 = "failed"; StepSkipped TaskStepStatusV12 = "skipped" )
type TaskEventDiagnosticV12 struct { Code string \`json:"code"\`; Column *int64 \`json:"column,omitempty"\`; Line *int64 \`json:"line,omitempty"\`; Message string \`json:"message"\`; Severity TaskEventDiagnosticSeverityV12 \`json:"severity"\`; SourceURI *string \`json:"sourceUri,omitempty"\` }
type TaskEventNameV12 string
const ( ArtifactCreated TaskEventNameV12 = "artifact.created"; TaskCancellationRequested TaskEventNameV12 = "task.cancellation_requested"; TaskCreated TaskEventNameV12 = "task.created"; TaskDiagnostic TaskEventNameV12 = "task.diagnostic"; TaskFinished TaskEventNameV12 = "task.finished"; TaskOutput TaskEventNameV12 = "task.output"; TaskStarted TaskEventNameV12 = "task.started"; TaskStepFinished TaskEventNameV12 = "task.step_finished"; TaskStepStarted TaskEventNameV12 = "task.step_started" )
type EventKindV12 string
const Event EventKindV12 = "event"
type TaskEventDiagnosticSeverityV12 string
const ( Error TaskEventDiagnosticSeverityV12 = "error"; Info TaskEventDiagnosticSeverityV12 = "info"; Warning TaskEventDiagnosticSeverityV12 = "warning" )
type TaskOutcomeV12 string
const ( OutcomeCancelled TaskOutcomeV12 = "cancelled"; OutcomeCommandFailed TaskOutcomeV12 = "command_failed"; OutcomeInfrastructureFailed TaskOutcomeV12 = "infrastructure_failed"; OutcomeInterrupted TaskOutcomeV12 = "interrupted"; OutcomeSucceeded TaskOutcomeV12 = "succeeded"; OutcomeTimedOut TaskOutcomeV12 = "timed_out" )
type TaskOutputStreamV12 string
const ( OutputCombined TaskOutputStreamV12 = "combined"; OutputStderr TaskOutputStreamV12 = "stderr"; OutputStdout TaskOutputStreamV12 = "stdout" )
type EventProtocolVersionV12 string
const The12 EventProtocolVersionV12 = "1.2"

`,
  testV13: `type TestSelection interface{ isTestSelection() }
type AllTestSelectionV13 struct { Mode TestSelectionModeV13 \`json:"mode"\` }
func (AllTestSelectionV13) isTestSelection() {}
type ContainersTestSelectionV13 struct { Mode TestSelectionModeV13 \`json:"mode"\`; ContainerIDs []string \`json:"containerIds"\` }
func (ContainersTestSelectionV13) isTestSelection() {}
type ItemsTestSelectionV13 struct { Mode TestSelectionModeV13 \`json:"mode"\`; ItemIDs []string \`json:"itemIds"\` }
func (ItemsTestSelectionV13) isTestSelection() {}
type FilterTestSelectionV13 struct { Mode TestSelectionModeV13 \`json:"mode"\`; Filter TestFilterV13 \`json:"filter"\` }
func (FilterTestSelectionV13) isTestSelection() {}
type FailedFromRunTestSelectionV13 struct { Mode TestSelectionModeV13 \`json:"mode"\`; RunID string \`json:"runId"\` }
func (FailedFromRunTestSelectionV13) isTestSelection() {}

`,
  taskV13: `type TaskSnapshotV13 interface{ isTaskSnapshotV13() }
type CmakeBuildTaskSnapshotV13 struct { TaskID string \`json:"taskId"\`; Kind Kind \`json:"kind"\`; WorkspaceGeneration string \`json:"workspaceGeneration"\`; ProjectID string \`json:"projectId"\`; BuildProfileID string \`json:"buildProfileId"\`; TargetIDs []string \`json:"targetIds"\`; Jobs int64 \`json:"jobs"\`; TimeoutMS int64 \`json:"timeoutMs"\`; Status TaskStatusV13 \`json:"status"\`; CreatedAt time.Time \`json:"createdAt"\`; LastSequence int64 \`json:"lastSequence"\`; Outcome *TaskOutcomeV13 \`json:"outcome,omitempty"\`; StartedAt *time.Time \`json:"startedAt,omitempty"\`; FinishedAt *time.Time \`json:"finishedAt,omitempty"\`; ErrorCode *string \`json:"errorCode,omitempty"\`; ErrorMessage *string \`json:"errorMessage,omitempty"\` }
func (CmakeBuildTaskSnapshotV13) isTaskSnapshotV13() {}
type SimulationTaskSnapshotV13 struct { TaskID string \`json:"taskId"\`; Kind Kind \`json:"kind"\`; Scenario SimulationScenarioV13 \`json:"scenario"\`; TimeoutMS *int64 \`json:"timeoutMs,omitempty"\`; Status TaskStatusV13 \`json:"status"\`; CreatedAt time.Time \`json:"createdAt"\`; LastSequence int64 \`json:"lastSequence"\`; Outcome *TaskOutcomeV13 \`json:"outcome,omitempty"\`; StartedAt *time.Time \`json:"startedAt,omitempty"\`; FinishedAt *time.Time \`json:"finishedAt,omitempty"\`; ErrorCode *string \`json:"errorCode,omitempty"\`; ErrorMessage *string \`json:"errorMessage,omitempty"\` }
func (SimulationTaskSnapshotV13) isTaskSnapshotV13() {}
type TestDiscoveryTaskSnapshotV13 struct { TaskID string \`json:"taskId"\`; Kind Kind \`json:"kind"\`; ProjectID string \`json:"projectId"\`; ProfileID string \`json:"profileId"\`; CatalogRevision *string \`json:"catalogRevision,omitempty"\`; Status TaskStatusV13 \`json:"status"\`; CreatedAt time.Time \`json:"createdAt"\`; LastSequence int64 \`json:"lastSequence"\`; Outcome *TaskOutcomeV13 \`json:"outcome,omitempty"\`; StartedAt *time.Time \`json:"startedAt,omitempty"\`; FinishedAt *time.Time \`json:"finishedAt,omitempty"\`; ErrorCode *string \`json:"errorCode,omitempty"\`; ErrorMessage *string \`json:"errorMessage,omitempty"\` }
func (TestDiscoveryTaskSnapshotV13) isTaskSnapshotV13() {}
type TestRunTaskSnapshotV13 struct { TaskID string \`json:"taskId"\`; Kind Kind \`json:"kind"\`; ProjectID string \`json:"projectId"\`; ProfileID string \`json:"profileId"\`; CatalogRevision string \`json:"catalogRevision"\`; RunID string \`json:"runId"\`; RepeatCount int64 \`json:"repeatCount"\`; Status TaskStatusV13 \`json:"status"\`; CreatedAt time.Time \`json:"createdAt"\`; LastSequence int64 \`json:"lastSequence"\`; Outcome *TaskOutcomeV13 \`json:"outcome,omitempty"\`; StartedAt *time.Time \`json:"startedAt,omitempty"\`; FinishedAt *time.Time \`json:"finishedAt,omitempty"\`; ErrorCode *string \`json:"errorCode,omitempty"\`; ErrorMessage *string \`json:"errorMessage,omitempty"\` }
func (TestRunTaskSnapshotV13) isTaskSnapshotV13() {}

`,
  eventV13: `import (
	"time"

	protocolmodelv13diagnostic "unit-test-ide.local/test-service/internal/protocolmodel/v1_3/diagnostic"
	protocolmodelv13test "unit-test-ide.local/test-service/internal/protocolmodel/v1_3/test"
)

type TaskEventV13 interface{ isTaskEventV13() }
type TaskEventBaseV13 struct { ProtocolVersion EventProtocolVersionV13 \`json:"protocolVersion"\`; Kind EventKindV13 \`json:"kind"\`; MessageID string \`json:"messageId"\`; SentAt time.Time \`json:"sentAt"\`; Sequence int64 \`json:"sequence"\`; TaskID string \`json:"taskId"\`; PayloadVersion int64 \`json:"payloadVersion"\` }
type TaskCreatedEventV13 struct { TaskEventBaseV13; Event TaskEventNameV13 \`json:"event"\`; Payload struct { Status string \`json:"status"\` } \`json:"payload"\` }
func (TaskCreatedEventV13) isTaskEventV13() {}
type TaskStartedEventV13 struct { TaskEventBaseV13; Event TaskEventNameV13 \`json:"event"\`; Payload struct { Status string \`json:"status"\` } \`json:"payload"\` }
func (TaskStartedEventV13) isTaskEventV13() {}
type TaskStepStartedEventV13 struct { TaskEventBaseV13; Event TaskEventNameV13 \`json:"event"\`; Payload TaskStepStartedPayloadV13 \`json:"payload"\` }
func (TaskStepStartedEventV13) isTaskEventV13() {}
type TaskOutputEventV13 struct { TaskEventBaseV13; Event TaskEventNameV13 \`json:"event"\`; Payload TaskOutputPayloadV13 \`json:"payload"\` }
func (TaskOutputEventV13) isTaskEventV13() {}
type TaskStepFinishedEventV13 struct { TaskEventBaseV13; Event TaskEventNameV13 \`json:"event"\`; Payload TaskStepFinishedPayloadV13 \`json:"payload"\` }
func (TaskStepFinishedEventV13) isTaskEventV13() {}
type TaskCancellationRequestedEventV13 struct { TaskEventBaseV13; Event TaskEventNameV13 \`json:"event"\`; Payload struct { Status string \`json:"status"\` } \`json:"payload"\` }
func (TaskCancellationRequestedEventV13) isTaskEventV13() {}
type ArtifactCreatedEventV13 struct { TaskEventBaseV13; Event TaskEventNameV13 \`json:"event"\`; Payload ArtifactCreatedPayloadV13 \`json:"payload"\` }
func (ArtifactCreatedEventV13) isTaskEventV13() {}
type TaskFinishedEventV13 struct { TaskEventBaseV13; Event TaskEventNameV13 \`json:"event"\`; Payload struct { Outcome TaskOutcomeV13 \`json:"outcome"\` } \`json:"payload"\` }
func (TaskFinishedEventV13) isTaskEventV13() {}
type TaskDiagnosticEventV13 struct { TaskEventBaseV13; Event TaskEventNameV13 \`json:"event"\`; Payload struct { Diagnostic protocolmodelv13diagnostic.DiagnosticV13 \`json:"diagnostic"\` } \`json:"payload"\` }
func (TaskDiagnosticEventV13) isTaskEventV13() {}
type TestDiscoveryStartedEventV13 struct { TaskEventBaseV13; Event TaskEventNameV13 \`json:"event"\`; Payload TestDiscoveryStartedPayloadV13 \`json:"payload"\` }
func (TestDiscoveryStartedEventV13) isTaskEventV13() {}
type TestContainerDiscoveredEventV13 struct { TaskEventBaseV13; Event TaskEventNameV13 \`json:"event"\`; Payload TestContainerDiscoveredPayloadV13 \`json:"payload"\` }
func (TestContainerDiscoveredEventV13) isTaskEventV13() {}
type TestCatalogPublishedEventV13 struct { TaskEventBaseV13; Event TaskEventNameV13 \`json:"event"\`; Payload TestCatalogPublishedPayloadV13 \`json:"payload"\` }
func (TestCatalogPublishedEventV13) isTaskEventV13() {}
type TestRunStartedEventV13 struct { TaskEventBaseV13; Event TaskEventNameV13 \`json:"event"\`; Payload TestRunStartedPayloadV13 \`json:"payload"\` }
func (TestRunStartedEventV13) isTaskEventV13() {}
type TestContainerStartedEventV13 struct { TaskEventBaseV13; Event TaskEventNameV13 \`json:"event"\`; Payload TestContainerStartedPayloadV13 \`json:"payload"\` }
func (TestContainerStartedEventV13) isTaskEventV13() {}
type TestItemStartedEventV13 struct { TaskEventBaseV13; Event TaskEventNameV13 \`json:"event"\`; Payload TestItemStartedPayloadV13 \`json:"payload"\` }
func (TestItemStartedEventV13) isTaskEventV13() {}
type TestOutputEventV13 struct { TaskEventBaseV13; Event TaskEventNameV13 \`json:"event"\`; Payload TestOutputPayloadV13 \`json:"payload"\` }
func (TestOutputEventV13) isTaskEventV13() {}
type TestItemFinishedEventV13 struct { TaskEventBaseV13; Event TaskEventNameV13 \`json:"event"\`; Payload struct { RunID string \`json:"runId"\`; Result protocolmodelv13test.TestItemResult \`json:"result"\` } \`json:"payload"\` }
func (TestItemFinishedEventV13) isTaskEventV13() {}
type TestContainerFinishedEventV13 struct { TaskEventBaseV13; Event TaskEventNameV13 \`json:"event"\`; Payload TestContainerFinishedPayloadV13 \`json:"payload"\` }
func (TestContainerFinishedEventV13) isTaskEventV13() {}
type TestRunFinishedEventV13 struct { TaskEventBaseV13; Event TaskEventNameV13 \`json:"event"\`; Payload TestRunFinishedPayloadV13 \`json:"payload"\` }
func (TestRunFinishedEventV13) isTaskEventV13() {}
type TaskStepStartedPayloadV13 struct { StepID string \`json:"stepId"\`; Kind TaskStepKindV13 \`json:"kind"\`; Status TaskStepStatusV13 \`json:"status"\` }
type TaskOutputPayloadV13 struct { StepID string \`json:"stepId"\`; Stream TaskOutputStreamV13 \`json:"stream"\`; Text string \`json:"text"\`; Truncated bool \`json:"truncated"\` }
type TaskStepFinishedPayloadV13 struct { StepID string \`json:"stepId"\`; Kind TaskStepKindV13 \`json:"kind"\`; Status TaskStepStatusV13 \`json:"status"\`; ExitCode *int64 \`json:"exitCode,omitempty"\`; ErrorCode *string \`json:"errorCode,omitempty"\` }
type ArtifactCreatedPayloadV13 struct { ArtifactID string \`json:"artifactId"\`; Kind string \`json:"kind"\` }
type TestDiscoveryStartedPayloadV13 struct { ProjectID string \`json:"projectId"\`; ProfileID string \`json:"profileId"\` }
type TestContainerDiscoveredPayloadV13 struct { ContainerID string \`json:"containerId"\`; Framework TestEventFrameworkV13 \`json:"framework"\`; DisplayName string \`json:"displayName"\` }
type TestCatalogPublishedPayloadV13 struct { ProjectID string \`json:"projectId"\`; ProfileID string \`json:"profileId"\`; Revision string \`json:"revision"\`; ContainerCount int64 \`json:"containerCount"\`; ItemCount int64 \`json:"itemCount"\` }
type TestRunStartedPayloadV13 struct { RunID string \`json:"runId"\`; CatalogRevision string \`json:"catalogRevision"\`; Total int64 \`json:"total"\` }
type TestContainerStartedPayloadV13 struct { RunID string \`json:"runId"\`; ContainerID string \`json:"containerId"\`; Iteration int64 \`json:"iteration"\` }
type TestItemStartedPayloadV13 struct { RunID string \`json:"runId"\`; ItemID string \`json:"itemId"\`; ContainerID string \`json:"containerId"\`; Iteration int64 \`json:"iteration"\` }
type TestOutputPayloadV13 struct { RunID string \`json:"runId"\`; ContainerID string \`json:"containerId"\`; ItemID *string \`json:"itemId,omitempty"\`; Iteration int64 \`json:"iteration"\`; Stream TaskOutputStreamV13 \`json:"stream"\`; Text string \`json:"text"\`; Truncated bool \`json:"truncated"\` }
type TestContainerFinishedPayloadV13 struct { RunID string \`json:"runId"\`; ContainerID string \`json:"containerId"\`; Iteration int64 \`json:"iteration"\`; Outcome TestContainerOutcomeV13 \`json:"outcome"\` }
type TestRunFinishedPayloadV13 struct { RunID string \`json:"runId"\`; Outcome protocolmodelv13test.TestRunOutcomeV13 \`json:"outcome"\`; Summary protocolmodelv13test.TestRunSummaryV13 \`json:"summary"\`; ResultRevision string \`json:"resultRevision"\`; Incomplete bool \`json:"incomplete"\` }
type EventProtocolVersionV13 string
const The13 EventProtocolVersionV13 = "1.3"
type EventKindV13 string
const Event EventKindV13 = "event"
type TaskEventNameV13 string
const ( TaskCreated TaskEventNameV13 = "task.created"; TaskStarted TaskEventNameV13 = "task.started"; TaskStepStarted TaskEventNameV13 = "task.step_started"; TaskOutput TaskEventNameV13 = "task.output"; TaskStepFinished TaskEventNameV13 = "task.step_finished"; TaskCancellationRequested TaskEventNameV13 = "task.cancellation_requested"; ArtifactCreated TaskEventNameV13 = "artifact.created"; TaskFinished TaskEventNameV13 = "task.finished"; TaskDiagnostic TaskEventNameV13 = "task.diagnostic"; TestDiscoveryStarted TaskEventNameV13 = "test.discovery.started"; TestContainerDiscovered TaskEventNameV13 = "test.container.discovered"; TestCatalogPublished TaskEventNameV13 = "test.catalog.published"; TestRunStarted TaskEventNameV13 = "test.run.started"; TestContainerStarted TaskEventNameV13 = "test.container.started"; TestItemStarted TaskEventNameV13 = "test.item.started"; TestOutput TaskEventNameV13 = "test.output"; TestItemFinished TaskEventNameV13 = "test.item.finished"; TestContainerFinished TaskEventNameV13 = "test.container.finished"; TestRunFinished TaskEventNameV13 = "test.run.finished" )
type TaskStepKindV13 string
const ( StepSimulationV13 TaskStepKindV13 = "simulation"; StepConfigureV13 TaskStepKindV13 = "configure"; StepBuildV13 TaskStepKindV13 = "build"; StepTestDiscoveryV13 TaskStepKindV13 = "test-discovery"; StepTestRunV13 TaskStepKindV13 = "test-run" )
type TaskStepStatusV13 string
const ( StepRunningV13 TaskStepStatusV13 = "running"; StepSucceededV13 TaskStepStatusV13 = "succeeded"; StepFailedV13 TaskStepStatusV13 = "failed"; StepSkippedV13 TaskStepStatusV13 = "skipped" )
type TaskOutputStreamV13 string
const ( OutputStdoutV13 TaskOutputStreamV13 = "stdout"; OutputStderrV13 TaskOutputStreamV13 = "stderr"; OutputCombinedV13 TaskOutputStreamV13 = "combined" )
type TaskOutcomeV13 string
const ( OutcomeSucceededV13 TaskOutcomeV13 = "succeeded"; OutcomeCommandFailedV13 TaskOutcomeV13 = "command_failed"; OutcomeCancelledV13 TaskOutcomeV13 = "cancelled"; OutcomeTimedOutV13 TaskOutcomeV13 = "timed_out"; OutcomeInterruptedV13 TaskOutcomeV13 = "interrupted"; OutcomeInfrastructureFailedV13 TaskOutcomeV13 = "infrastructure_failed" )
type TestEventFrameworkV13 string
const ( FrameworkCppUTestV13 TestEventFrameworkV13 = "cpputest"; FrameworkUnityV13 TestEventFrameworkV13 = "unity"; FrameworkOpaqueCTestV13 TestEventFrameworkV13 = "opaque-ctest" )
type TestContainerOutcomeV13 string
const ( ContainerPassedV13 TestContainerOutcomeV13 = "passed"; ContainerFailedV13 TestContainerOutcomeV13 = "failed"; ContainerErroredV13 TestContainerOutcomeV13 = "errored"; ContainerCancelledV13 TestContainerOutcomeV13 = "cancelled"; ContainerTimedOutV13 TestContainerOutcomeV13 = "timed_out"; ContainerNotRunV13 TestContainerOutcomeV13 = "not_run" )

`
};
const temp = await mkdtemp(join(tmpdir(), "unit-test-ide-protocol-"));

function rewriteReferences(value, transform) {
  if (Array.isArray(value)) {
    return value.map((item) => rewriteReferences(item, transform));
  }
  if (value === null || typeof value !== "object") return value;
  const result = {};
  for (const [key, item] of Object.entries(value)) {
    result[key] = key === "$ref" && typeof item === "string"
      ? transform(item)
      : rewriteReferences(item, transform);
  }
  return result;
}

function normalizeGoImports(source) {
  const lines = source.replaceAll("\r\n", "\n").split("\n");
  const imports = [];
  const body = [];
  for (let index = 0; index < lines.length; index++) {
    if (lines[index].startsWith("import \"")) {
      imports.push(lines[index]);
      continue;
    }
    if (lines[index] === "import (") {
      const block = [lines[index]];
      while (++index < lines.length) {
        block.push(lines[index]);
        if (lines[index] === ")") break;
      }
      imports.push(block.join("\n"));
      continue;
    }
    body.push(lines[index]);
  }
  const uniqueImports = [...new Set(imports)];
  if (uniqueImports.length === 0) return body.join("\n");
  return `${uniqueImports.join("\n\n")}\n\n${body.join("\n").trimStart()}`;
}

async function bundledSchema(schema, model) {
  const document = JSON.parse(await readFile(schema, "utf8"));
  if (!model.bundle?.length) return document;

  const dependencies = [];
  for (const name of model.bundle) {
    const dependencyPath = join(dirname(schema), name);
    const dependency = JSON.parse(await readFile(dependencyPath, "utf8"));
    const key = `external${dependency.title.replaceAll(/[^A-Za-z0-9]/g, "")}`;
    dependencies.push({ dependency, key });
  }
  const references = new Map(dependencies.map(({ dependency, key }) => [dependency.$id, key]));
  const rewriteExternal = (reference) => {
    for (const [id, key] of references) {
      if (reference === id) return `#/$defs/${key}`;
      if (reference.startsWith(`${id}#`)) {
        return `#/$defs/${key}${reference.slice(id.length + 1)}`;
      }
    }
    return reference;
  };
  const result = rewriteReferences(document, rewriteExternal);
  result.$defs ??= {};
  for (const { dependency, key } of dependencies) {
    const embedded = structuredClone(dependency);
    delete embedded.$schema;
    delete embedded.$id;
    result.$defs[key] = rewriteReferences(embedded, (reference) => {
      if (reference.startsWith("#/")) {
        return `#/$defs/${key}${reference.slice(1)}`;
      }
      return rewriteExternal(reference);
    });
  }
  return result;
}

try {
  let targetIndex = 0;
  for (const model of models) {
    const schema = join(root, "packages/protocol-schema/schema", model.directory, model.schema);
    const document = await bundledSchema(schema, model);
    let source = schema;
    if (model.bundle?.length) {
      source = join(temp, `${model.top}.bundled.schema.json`);
      await writeFile(source, `${JSON.stringify(document, null, 2)}\n`);
    }
    if (model.definition) {
      const definition = document.$defs?.[model.definition];
      if (!definition) throw new Error(`Missing schema definition ${model.definition} in ${schema}`);
      const generatedSchema = { $schema: document.$schema, ...definition, title: model.top, $defs: document.$defs };
      source = join(temp, `${model.top}.schema.json`);
      await writeFile(source, `${JSON.stringify(generatedSchema, null, 2)}\n`);
    }
    const targets = [
      {
        output: join(root, "packages/protocol-models/src/generated", model.ts),
        args: ["--lang", "typescript", "--just-types", "--top-level", model.top]
      },
      {
        output: join(root, "apps/test-service/internal/protocolmodel", model.go),
        args: ["--lang", "go", "--just-types", "--package", model.goPackage ?? "protocolmodel", "--top-level", model.top],
        packageName: model.goPackage ?? "protocolmodel"
      }
    ];

    for (const target of targets) {
      const output = check ? join(temp, String(targetIndex++)) : target.output;
      await mkdir(dirname(output), { recursive: true });
      const result = spawnSync(process.execPath, [quicktype, "--quiet", "--src-lang", "schema", "--src", source, ...target.args, "--out", output], { cwd: root, stdio: "inherit" });
      if (result.status !== 0) throw new Error(`quicktype failed for ${model.top} with status ${result.status ?? 1}`);
      if (!target.packageName && model.template) {
        if (model.template === "testV13") {
          const generated = await readFile(output, "utf8");
          const replaced = generated.replace(
            /export interface TestSelection \{[\s\S]*?\n\}\n/,
            typescriptTestSelectionV13
          );
          if (replaced === generated) throw new Error("Unable to create TypeScript union for TestSelection");
          await writeFile(output, replaced);
        } else {
          await writeFile(output, typescriptTemplates[model.template]);
        }
      }
      if (target.packageName && model.template) {
        if (model.template === "event" || model.template === "eventV13") {
          const imports = model.template === "event" ? `import "time"\n\n` : "";
          await writeFile(output, `${imports}${goUnionBodies[model.template]}`);
        } else {
          const generated = await readFile(output, "utf8");
          const unionType = model.template === "testV13" ? "TestSelection" : model.top;
          const replaced = generated.replace(new RegExp(`type ${unionType} struct \\{[\\s\\S]*?\\n\\}\\n\\n`), goUnionBodies[model.template]);
          if (replaced === generated) throw new Error(`Unable to create Go union for ${unionType}`);
          await writeFile(output, replaced);
        }
      }
      if (target.packageName) {
        const sourceWithImports = normalizeGoImports(await readFile(output, "utf8"));
        await writeFile(output, `package ${target.packageName}\n\n${sourceWithImports}`);
        const formatted = spawnSync("gofmt", ["-w", output], { cwd: root, stdio: "inherit" });
        if (formatted.status !== 0) throw new Error(`gofmt failed with status ${formatted.status ?? 1}`);
      }
      if (check) {
        const normalize = (value) => value.replaceAll("\r\n", "\n");
        if (normalize(await readFile(output, "utf8")) !== normalize(await readFile(target.output, "utf8"))) {
          throw new Error(`Generated file is stale: ${target.output}`);
        }
      }
    }
  }
} finally {
  await rm(temp, { recursive: true, force: true });
}
