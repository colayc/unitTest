import type { CoverageCompletenessV16, CoverageRunOutcomeV16, CoverageRunReasonV16, CoverageSummaryV16 } from "./coverage-v1-6.js";
import type { DiagnosticV16 } from "./diagnostic-v1-6.js";
import type { TaskOutcomeV16 } from "./task-v1-6.js";
import type { TestGenerationStateV16 } from "./test-generation-v1-6.js";
import type { TestItemResult, TestRunOutcomeV16, TestRunSummaryV16 } from "./test-v1-6.js";

export interface TaskEventBaseV16 { protocolVersion: EventProtocolVersionV16; kind: EventKindV16.Event; messageId: string; sentAt: Date; sequence: number; taskId: string; payloadVersion: 1; }
export interface TaskCreatedEventV16 extends TaskEventBaseV16 { event: TaskEventNameV16.TaskCreated; payload: TaskCreatedPayloadV16; }
export interface TaskStartedEventV16 extends TaskEventBaseV16 { event: TaskEventNameV16.TaskStarted; payload: TaskStartedPayloadV16; }
export interface TaskStepStartedEventV16 extends TaskEventBaseV16 { event: TaskEventNameV16.TaskStepStarted; payload: TaskStepStartedPayloadV16; }
export interface TaskOutputEventV16 extends TaskEventBaseV16 { event: TaskEventNameV16.TaskOutput; payload: TaskOutputPayloadV16; }
export interface TaskStepFinishedEventV16 extends TaskEventBaseV16 { event: TaskEventNameV16.TaskStepFinished; payload: TaskStepFinishedPayloadV16; }
export interface TaskCancellationRequestedEventV16 extends TaskEventBaseV16 { event: TaskEventNameV16.TaskCancellationRequested; payload: TaskCancellationRequestedPayloadV16; }
export interface ArtifactCreatedEventV16 extends TaskEventBaseV16 { event: TaskEventNameV16.ArtifactCreated; payload: ArtifactCreatedPayloadV16; }
export interface TaskFinishedEventV16 extends TaskEventBaseV16 { event: TaskEventNameV16.TaskFinished; payload: TaskFinishedPayloadV16; }
export interface TaskDiagnosticEventV16 extends TaskEventBaseV16 { event: TaskEventNameV16.TaskDiagnostic; payload: TaskDiagnosticPayloadV16; }
export interface TestDiscoveryStartedEventV16 extends TaskEventBaseV16 { event: TaskEventNameV16.TestDiscoveryStarted; payload: TestDiscoveryStartedPayloadV16; }
export interface TestContainerDiscoveredEventV16 extends TaskEventBaseV16 { event: TaskEventNameV16.TestContainerDiscovered; payload: TestContainerDiscoveredPayloadV16; }
export interface TestCatalogPublishedEventV16 extends TaskEventBaseV16 { event: TaskEventNameV16.TestCatalogPublished; payload: TestCatalogPublishedPayloadV16; }
export interface TestRunStartedEventV16 extends TaskEventBaseV16 { event: TaskEventNameV16.TestRunStarted; payload: TestRunStartedPayloadV16; }
export interface TestContainerStartedEventV16 extends TaskEventBaseV16 { event: TaskEventNameV16.TestContainerStarted; payload: TestContainerStartedPayloadV16; }
export interface TestItemStartedEventV16 extends TaskEventBaseV16 { event: TaskEventNameV16.TestItemStarted; payload: TestItemStartedPayloadV16; }
export interface TestOutputEventV16 extends TaskEventBaseV16 { event: TaskEventNameV16.TestOutput; payload: TestOutputPayloadV16; }
export interface TestItemFinishedEventV16 extends TaskEventBaseV16 { event: TaskEventNameV16.TestItemFinished; payload: TestItemFinishedPayloadV16; }
export interface TestContainerFinishedEventV16 extends TaskEventBaseV16 { event: TaskEventNameV16.TestContainerFinished; payload: TestContainerFinishedPayloadV16; }
export interface TestRunFinishedEventV16 extends TaskEventBaseV16 { event: TaskEventNameV16.TestRunFinished; payload: TestRunFinishedPayloadV16; }
export interface CoverageRunStartedEventV16 extends TaskEventBaseV16 { event: TaskEventNameV16.CoverageRunStarted; payload: CoverageRunStartedPayloadV16; }
export interface CoverageBuildFinishedEventV16 extends TaskEventBaseV16 { event: TaskEventNameV16.CoverageBuildFinished; payload: CoverageBuildFinishedPayloadV16; }
export interface CoverageCollectionStartedEventV16 extends TaskEventBaseV16 { event: TaskEventNameV16.CoverageCollectionStarted; payload: CoverageCollectionStartedPayloadV16; }
export interface CoverageReportAvailableEventV16 extends TaskEventBaseV16 { event: TaskEventNameV16.CoverageReportAvailable; payload: CoverageReportAvailablePayloadV16; }
export interface CoverageRunFinishedEventV16 extends TaskEventBaseV16 { event: TaskEventNameV16.CoverageRunFinished; payload: CoverageRunFinishedPayloadV16; }
export interface TestGenerationStateChangedEventV16 extends TaskEventBaseV16 { event: TaskEventNameV16.TestGenerationStateChanged; payload: TestGenerationStateChangedPayloadV16; }
export type CoverageEventV16 = CoverageRunStartedEventV16 | CoverageBuildFinishedEventV16 | CoverageCollectionStartedEventV16 | CoverageReportAvailableEventV16 | CoverageRunFinishedEventV16;
export type TaskEventV16 = TaskCreatedEventV16 | TaskStartedEventV16 | TaskStepStartedEventV16 | TaskOutputEventV16 | TaskStepFinishedEventV16 | TaskCancellationRequestedEventV16 | ArtifactCreatedEventV16 | TaskFinishedEventV16 | TaskDiagnosticEventV16 | TestDiscoveryStartedEventV16 | TestContainerDiscoveredEventV16 | TestCatalogPublishedEventV16 | TestRunStartedEventV16 | TestContainerStartedEventV16 | TestItemStartedEventV16 | TestOutputEventV16 | TestItemFinishedEventV16 | TestContainerFinishedEventV16 | TestRunFinishedEventV16 | CoverageEventV16 | TestGenerationStateChangedEventV16;
export interface TaskCreatedPayloadV16 { status: "queued"; }
export interface TaskStartedPayloadV16 { status: "running"; }
export interface TaskStepStartedPayloadV16 { stepId: string; kind: TaskStepKindV16; status: "running"; }
export interface TaskOutputPayloadV16 { stepId: string; stream: TaskOutputStreamV16; text: string; truncated: boolean; }
export interface TaskStepFinishedPayloadV16 { stepId: string; kind: TaskStepKindV16; status: TaskStepFinishedStatusV16; exitCode?: number; errorCode?: string; }
export interface TaskCancellationRequestedPayloadV16 { status: "cancelling"; }
export interface ArtifactCreatedPayloadV16 { artifactId: string; kind: string; }
export interface TaskFinishedPayloadV16 { outcome: TaskOutcomeV16; }
export interface TaskDiagnosticPayloadV16 { diagnostic: DiagnosticV16; }
export interface TestDiscoveryStartedPayloadV16 { projectId: string; profileId: string; }
export interface TestContainerDiscoveredPayloadV16 { containerId: string; framework: TestEventFrameworkV16; displayName: string; }
export interface TestCatalogPublishedPayloadV16 { projectId: string; profileId: string; revision: string; containerCount: number; itemCount: number; }
export interface TestRunStartedPayloadV16 { runId: string; catalogRevision: string; total: number; }
export interface TestContainerStartedPayloadV16 { runId: string; containerId: string; iteration: number; }
export interface TestItemStartedPayloadV16 { runId: string; itemId: string; containerId: string; iteration: number; }
export interface TestOutputPayloadV16 { runId: string; containerId: string; itemId?: string; iteration: number; stream: TaskOutputStreamV16; text: string; truncated: boolean; }
export interface TestItemFinishedPayloadV16 { runId: string; result: TestItemResult; }
export interface TestContainerFinishedPayloadV16 { runId: string; containerId: string; iteration: number; outcome: TestContainerOutcomeV16; }
export interface TestRunFinishedPayloadV16 { runId: string; outcome: TestRunOutcomeV16; summary: TestRunSummaryV16; resultRevision: string; incomplete: boolean; }
export interface CoverageRunStartedPayloadV16 { coverageRunId: string; testRunId: string; catalogRevision: string; repeatCount: number; }
export interface CoverageBuildFinishedPayloadV16 { coverageRunId: string; }
export interface CoverageCollectionStartedPayloadV16 { coverageRunId: string; testRunId: string; }
export interface CoverageReportAvailablePayloadV16 { coverageRunId: string; reportId: string; artifactId: string; completeness: CoverageCompletenessV16; summary: CoverageSummaryV16; }
export interface CoverageRunFinishedPayloadV16 { coverageRunId: string; outcome: CoverageRunOutcomeV16; reason?: CoverageRunReasonV16; reportId?: string; }
export interface TestGenerationStateChangedPayloadV16 { runId: string; from: TestGenerationStateV16; to: TestGenerationStateV16; }
export enum EventProtocolVersionV16 { The16 = "1.6" }
export enum EventKindV16 { Event = "event" }
export enum TaskEventNameV16 { TaskCreated = "task.created", TaskStarted = "task.started", TaskStepStarted = "task.step_started", TaskOutput = "task.output", TaskStepFinished = "task.step_finished", TaskCancellationRequested = "task.cancellation_requested", ArtifactCreated = "artifact.created", TaskFinished = "task.finished", TaskDiagnostic = "task.diagnostic", TestDiscoveryStarted = "test.discovery.started", TestContainerDiscovered = "test.container.discovered", TestCatalogPublished = "test.catalog.published", TestRunStarted = "test.run.started", TestContainerStarted = "test.container.started", TestItemStarted = "test.item.started", TestOutput = "test.output", TestItemFinished = "test.item.finished", TestContainerFinished = "test.container.finished", TestRunFinished = "test.run.finished", CoverageRunStarted = "coverage.run.started", CoverageBuildFinished = "coverage.build.finished", CoverageCollectionStarted = "coverage.collection.started", CoverageReportAvailable = "coverage.report.available", CoverageRunFinished = "coverage.run.finished", TestGenerationStateChanged = "testGeneration.state.changed" }
export enum TaskStepKindV16 { Simulation = "simulation", Configure = "configure", Build = "build", TestDiscovery = "test-discovery", TestRun = "test-run", CoverageConfigure = "coverage-configure", CoverageBuild = "coverage-build", CoverageTest = "coverage-test", CoverageMerge = "coverage-merge", CoverageNormalize = "coverage-normalize", CoverageReport = "coverage-report", CoveragePublish = "coverage-publish" }
export enum TaskStepFinishedStatusV16 { Succeeded = "succeeded", Failed = "failed", Skipped = "skipped" }
export enum TaskOutputStreamV16 { Stdout = "stdout", Stderr = "stderr", Combined = "combined" }
export enum TestEventFrameworkV16 { Cpputest = "cpputest", Unity = "unity", OpaqueCtest = "opaque-ctest" }
export enum TestContainerOutcomeV16 { Passed = "passed", Failed = "failed", Errored = "errored", Cancelled = "cancelled", TimedOut = "timed_out", NotRun = "not_run" }
