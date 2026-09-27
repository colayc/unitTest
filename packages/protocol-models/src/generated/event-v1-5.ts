import type { CoverageCompletenessV15, CoverageRunOutcomeV15, CoverageRunReasonV15, CoverageSummaryV15 } from "./coverage-v1-5.js";
import type { DiagnosticV15 } from "./diagnostic-v1-5.js";
import type { TaskOutcomeV15 } from "./task-v1-5.js";
import type { TestGenerationStateV15 } from "./test-generation-v1-5.js";
import type { TestItemResult, TestRunOutcomeV15, TestRunSummaryV15 } from "./test-v1-5.js";

export interface TaskEventBaseV15 { protocolVersion: EventProtocolVersionV15; kind: EventKindV15.Event; messageId: string; sentAt: Date; sequence: number; taskId: string; payloadVersion: 1; }
export interface TaskCreatedEventV15 extends TaskEventBaseV15 { event: TaskEventNameV15.TaskCreated; payload: TaskCreatedPayloadV15; }
export interface TaskStartedEventV15 extends TaskEventBaseV15 { event: TaskEventNameV15.TaskStarted; payload: TaskStartedPayloadV15; }
export interface TaskStepStartedEventV15 extends TaskEventBaseV15 { event: TaskEventNameV15.TaskStepStarted; payload: TaskStepStartedPayloadV15; }
export interface TaskOutputEventV15 extends TaskEventBaseV15 { event: TaskEventNameV15.TaskOutput; payload: TaskOutputPayloadV15; }
export interface TaskStepFinishedEventV15 extends TaskEventBaseV15 { event: TaskEventNameV15.TaskStepFinished; payload: TaskStepFinishedPayloadV15; }
export interface TaskCancellationRequestedEventV15 extends TaskEventBaseV15 { event: TaskEventNameV15.TaskCancellationRequested; payload: TaskCancellationRequestedPayloadV15; }
export interface ArtifactCreatedEventV15 extends TaskEventBaseV15 { event: TaskEventNameV15.ArtifactCreated; payload: ArtifactCreatedPayloadV15; }
export interface TaskFinishedEventV15 extends TaskEventBaseV15 { event: TaskEventNameV15.TaskFinished; payload: TaskFinishedPayloadV15; }
export interface TaskDiagnosticEventV15 extends TaskEventBaseV15 { event: TaskEventNameV15.TaskDiagnostic; payload: TaskDiagnosticPayloadV15; }
export interface TestDiscoveryStartedEventV15 extends TaskEventBaseV15 { event: TaskEventNameV15.TestDiscoveryStarted; payload: TestDiscoveryStartedPayloadV15; }
export interface TestContainerDiscoveredEventV15 extends TaskEventBaseV15 { event: TaskEventNameV15.TestContainerDiscovered; payload: TestContainerDiscoveredPayloadV15; }
export interface TestCatalogPublishedEventV15 extends TaskEventBaseV15 { event: TaskEventNameV15.TestCatalogPublished; payload: TestCatalogPublishedPayloadV15; }
export interface TestRunStartedEventV15 extends TaskEventBaseV15 { event: TaskEventNameV15.TestRunStarted; payload: TestRunStartedPayloadV15; }
export interface TestContainerStartedEventV15 extends TaskEventBaseV15 { event: TaskEventNameV15.TestContainerStarted; payload: TestContainerStartedPayloadV15; }
export interface TestItemStartedEventV15 extends TaskEventBaseV15 { event: TaskEventNameV15.TestItemStarted; payload: TestItemStartedPayloadV15; }
export interface TestOutputEventV15 extends TaskEventBaseV15 { event: TaskEventNameV15.TestOutput; payload: TestOutputPayloadV15; }
export interface TestItemFinishedEventV15 extends TaskEventBaseV15 { event: TaskEventNameV15.TestItemFinished; payload: TestItemFinishedPayloadV15; }
export interface TestContainerFinishedEventV15 extends TaskEventBaseV15 { event: TaskEventNameV15.TestContainerFinished; payload: TestContainerFinishedPayloadV15; }
export interface TestRunFinishedEventV15 extends TaskEventBaseV15 { event: TaskEventNameV15.TestRunFinished; payload: TestRunFinishedPayloadV15; }
export interface CoverageRunStartedEventV15 extends TaskEventBaseV15 { event: TaskEventNameV15.CoverageRunStarted; payload: CoverageRunStartedPayloadV15; }
export interface CoverageBuildFinishedEventV15 extends TaskEventBaseV15 { event: TaskEventNameV15.CoverageBuildFinished; payload: CoverageBuildFinishedPayloadV15; }
export interface CoverageCollectionStartedEventV15 extends TaskEventBaseV15 { event: TaskEventNameV15.CoverageCollectionStarted; payload: CoverageCollectionStartedPayloadV15; }
export interface CoverageReportAvailableEventV15 extends TaskEventBaseV15 { event: TaskEventNameV15.CoverageReportAvailable; payload: CoverageReportAvailablePayloadV15; }
export interface CoverageRunFinishedEventV15 extends TaskEventBaseV15 { event: TaskEventNameV15.CoverageRunFinished; payload: CoverageRunFinishedPayloadV15; }
export interface TestGenerationStateChangedEventV15 extends TaskEventBaseV15 { event: TaskEventNameV15.TestGenerationStateChanged; payload: TestGenerationStateChangedPayloadV15; }
export type CoverageEventV15 = CoverageRunStartedEventV15 | CoverageBuildFinishedEventV15 | CoverageCollectionStartedEventV15 | CoverageReportAvailableEventV15 | CoverageRunFinishedEventV15;
export type TaskEventV15 = TaskCreatedEventV15 | TaskStartedEventV15 | TaskStepStartedEventV15 | TaskOutputEventV15 | TaskStepFinishedEventV15 | TaskCancellationRequestedEventV15 | ArtifactCreatedEventV15 | TaskFinishedEventV15 | TaskDiagnosticEventV15 | TestDiscoveryStartedEventV15 | TestContainerDiscoveredEventV15 | TestCatalogPublishedEventV15 | TestRunStartedEventV15 | TestContainerStartedEventV15 | TestItemStartedEventV15 | TestOutputEventV15 | TestItemFinishedEventV15 | TestContainerFinishedEventV15 | TestRunFinishedEventV15 | CoverageEventV15 | TestGenerationStateChangedEventV15;
export interface TaskCreatedPayloadV15 { status: "queued"; }
export interface TaskStartedPayloadV15 { status: "running"; }
export interface TaskStepStartedPayloadV15 { stepId: string; kind: TaskStepKindV15; status: "running"; }
export interface TaskOutputPayloadV15 { stepId: string; stream: TaskOutputStreamV15; text: string; truncated: boolean; }
export interface TaskStepFinishedPayloadV15 { stepId: string; kind: TaskStepKindV15; status: TaskStepFinishedStatusV15; exitCode?: number; errorCode?: string; }
export interface TaskCancellationRequestedPayloadV15 { status: "cancelling"; }
export interface ArtifactCreatedPayloadV15 { artifactId: string; kind: string; }
export interface TaskFinishedPayloadV15 { outcome: TaskOutcomeV15; }
export interface TaskDiagnosticPayloadV15 { diagnostic: DiagnosticV15; }
export interface TestDiscoveryStartedPayloadV15 { projectId: string; profileId: string; }
export interface TestContainerDiscoveredPayloadV15 { containerId: string; framework: TestEventFrameworkV15; displayName: string; }
export interface TestCatalogPublishedPayloadV15 { projectId: string; profileId: string; revision: string; containerCount: number; itemCount: number; }
export interface TestRunStartedPayloadV15 { runId: string; catalogRevision: string; total: number; }
export interface TestContainerStartedPayloadV15 { runId: string; containerId: string; iteration: number; }
export interface TestItemStartedPayloadV15 { runId: string; itemId: string; containerId: string; iteration: number; }
export interface TestOutputPayloadV15 { runId: string; containerId: string; itemId?: string; iteration: number; stream: TaskOutputStreamV15; text: string; truncated: boolean; }
export interface TestItemFinishedPayloadV15 { runId: string; result: TestItemResult; }
export interface TestContainerFinishedPayloadV15 { runId: string; containerId: string; iteration: number; outcome: TestContainerOutcomeV15; }
export interface TestRunFinishedPayloadV15 { runId: string; outcome: TestRunOutcomeV15; summary: TestRunSummaryV15; resultRevision: string; incomplete: boolean; }
export interface CoverageRunStartedPayloadV15 { coverageRunId: string; testRunId: string; catalogRevision: string; repeatCount: number; }
export interface CoverageBuildFinishedPayloadV15 { coverageRunId: string; }
export interface CoverageCollectionStartedPayloadV15 { coverageRunId: string; testRunId: string; }
export interface CoverageReportAvailablePayloadV15 { coverageRunId: string; reportId: string; artifactId: string; completeness: CoverageCompletenessV15; summary: CoverageSummaryV15; }
export interface CoverageRunFinishedPayloadV15 { coverageRunId: string; outcome: CoverageRunOutcomeV15; reason?: CoverageRunReasonV15; reportId?: string; }
export interface TestGenerationStateChangedPayloadV15 { runId: string; from: TestGenerationStateV15; to: TestGenerationStateV15; }
export enum EventProtocolVersionV15 { The15 = "1.5" }
export enum EventKindV15 { Event = "event" }
export enum TaskEventNameV15 { TaskCreated = "task.created", TaskStarted = "task.started", TaskStepStarted = "task.step_started", TaskOutput = "task.output", TaskStepFinished = "task.step_finished", TaskCancellationRequested = "task.cancellation_requested", ArtifactCreated = "artifact.created", TaskFinished = "task.finished", TaskDiagnostic = "task.diagnostic", TestDiscoveryStarted = "test.discovery.started", TestContainerDiscovered = "test.container.discovered", TestCatalogPublished = "test.catalog.published", TestRunStarted = "test.run.started", TestContainerStarted = "test.container.started", TestItemStarted = "test.item.started", TestOutput = "test.output", TestItemFinished = "test.item.finished", TestContainerFinished = "test.container.finished", TestRunFinished = "test.run.finished", CoverageRunStarted = "coverage.run.started", CoverageBuildFinished = "coverage.build.finished", CoverageCollectionStarted = "coverage.collection.started", CoverageReportAvailable = "coverage.report.available", CoverageRunFinished = "coverage.run.finished", TestGenerationStateChanged = "testGeneration.state.changed" }
export enum TaskStepKindV15 { Simulation = "simulation", Configure = "configure", Build = "build", TestDiscovery = "test-discovery", TestRun = "test-run", CoverageConfigure = "coverage-configure", CoverageBuild = "coverage-build", CoverageTest = "coverage-test", CoverageMerge = "coverage-merge", CoverageNormalize = "coverage-normalize", CoverageReport = "coverage-report", CoveragePublish = "coverage-publish" }
export enum TaskStepFinishedStatusV15 { Succeeded = "succeeded", Failed = "failed", Skipped = "skipped" }
export enum TaskOutputStreamV15 { Stdout = "stdout", Stderr = "stderr", Combined = "combined" }
export enum TestEventFrameworkV15 { Cpputest = "cpputest", Unity = "unity", OpaqueCtest = "opaque-ctest" }
export enum TestContainerOutcomeV15 { Passed = "passed", Failed = "failed", Errored = "errored", Cancelled = "cancelled", TimedOut = "timed_out", NotRun = "not_run" }
