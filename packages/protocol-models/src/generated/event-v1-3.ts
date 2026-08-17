import type { DiagnosticV13 } from "./diagnostic-v1-3.js";
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
