import type { CoverageCompletenessV14, CoverageRunOutcomeV14, CoverageRunReasonV14, CoverageSummaryV14 } from "./coverage-v1-4.js";
import type { DiagnosticV14 } from "./diagnostic-v1-4.js";
import type { TaskOutcomeV14 } from "./task-v1-4.js";
import type { TestItemResult, TestRunOutcomeV14, TestRunSummaryV14 } from "./test-v1-4.js";

export interface TaskEventBaseV14 { protocolVersion: EventProtocolVersionV14; kind: EventKindV14.Event; messageId: string; sentAt: Date; sequence: number; taskId: string; payloadVersion: 1; }
export interface TaskCreatedEventV14 extends TaskEventBaseV14 { event: TaskEventNameV14.TaskCreated; payload: TaskCreatedPayloadV14; }
export interface TaskStartedEventV14 extends TaskEventBaseV14 { event: TaskEventNameV14.TaskStarted; payload: TaskStartedPayloadV14; }
export interface TaskStepStartedEventV14 extends TaskEventBaseV14 { event: TaskEventNameV14.TaskStepStarted; payload: TaskStepStartedPayloadV14; }
export interface TaskOutputEventV14 extends TaskEventBaseV14 { event: TaskEventNameV14.TaskOutput; payload: TaskOutputPayloadV14; }
export interface TaskStepFinishedEventV14 extends TaskEventBaseV14 { event: TaskEventNameV14.TaskStepFinished; payload: TaskStepFinishedPayloadV14; }
export interface TaskCancellationRequestedEventV14 extends TaskEventBaseV14 { event: TaskEventNameV14.TaskCancellationRequested; payload: TaskCancellationRequestedPayloadV14; }
export interface ArtifactCreatedEventV14 extends TaskEventBaseV14 { event: TaskEventNameV14.ArtifactCreated; payload: ArtifactCreatedPayloadV14; }
export interface TaskFinishedEventV14 extends TaskEventBaseV14 { event: TaskEventNameV14.TaskFinished; payload: TaskFinishedPayloadV14; }
export interface TaskDiagnosticEventV14 extends TaskEventBaseV14 { event: TaskEventNameV14.TaskDiagnostic; payload: TaskDiagnosticPayloadV14; }
export interface TestDiscoveryStartedEventV14 extends TaskEventBaseV14 { event: TaskEventNameV14.TestDiscoveryStarted; payload: TestDiscoveryStartedPayloadV14; }
export interface TestContainerDiscoveredEventV14 extends TaskEventBaseV14 { event: TaskEventNameV14.TestContainerDiscovered; payload: TestContainerDiscoveredPayloadV14; }
export interface TestCatalogPublishedEventV14 extends TaskEventBaseV14 { event: TaskEventNameV14.TestCatalogPublished; payload: TestCatalogPublishedPayloadV14; }
export interface TestRunStartedEventV14 extends TaskEventBaseV14 { event: TaskEventNameV14.TestRunStarted; payload: TestRunStartedPayloadV14; }
export interface TestContainerStartedEventV14 extends TaskEventBaseV14 { event: TaskEventNameV14.TestContainerStarted; payload: TestContainerStartedPayloadV14; }
export interface TestItemStartedEventV14 extends TaskEventBaseV14 { event: TaskEventNameV14.TestItemStarted; payload: TestItemStartedPayloadV14; }
export interface TestOutputEventV14 extends TaskEventBaseV14 { event: TaskEventNameV14.TestOutput; payload: TestOutputPayloadV14; }
export interface TestItemFinishedEventV14 extends TaskEventBaseV14 { event: TaskEventNameV14.TestItemFinished; payload: TestItemFinishedPayloadV14; }
export interface TestContainerFinishedEventV14 extends TaskEventBaseV14 { event: TaskEventNameV14.TestContainerFinished; payload: TestContainerFinishedPayloadV14; }
export interface TestRunFinishedEventV14 extends TaskEventBaseV14 { event: TaskEventNameV14.TestRunFinished; payload: TestRunFinishedPayloadV14; }
export interface CoverageRunStartedEventV14 extends TaskEventBaseV14 { event: TaskEventNameV14.CoverageRunStarted; payload: CoverageRunStartedPayloadV14; }
export interface CoverageBuildFinishedEventV14 extends TaskEventBaseV14 { event: TaskEventNameV14.CoverageBuildFinished; payload: CoverageBuildFinishedPayloadV14; }
export interface CoverageCollectionStartedEventV14 extends TaskEventBaseV14 { event: TaskEventNameV14.CoverageCollectionStarted; payload: CoverageCollectionStartedPayloadV14; }
export interface CoverageReportAvailableEventV14 extends TaskEventBaseV14 { event: TaskEventNameV14.CoverageReportAvailable; payload: CoverageReportAvailablePayloadV14; }
export interface CoverageRunFinishedEventV14 extends TaskEventBaseV14 { event: TaskEventNameV14.CoverageRunFinished; payload: CoverageRunFinishedPayloadV14; }
export type CoverageEventV14 = CoverageRunStartedEventV14 | CoverageBuildFinishedEventV14 | CoverageCollectionStartedEventV14 | CoverageReportAvailableEventV14 | CoverageRunFinishedEventV14;
export type TaskEventV14 = TaskCreatedEventV14 | TaskStartedEventV14 | TaskStepStartedEventV14 | TaskOutputEventV14 | TaskStepFinishedEventV14 | TaskCancellationRequestedEventV14 | ArtifactCreatedEventV14 | TaskFinishedEventV14 | TaskDiagnosticEventV14 | TestDiscoveryStartedEventV14 | TestContainerDiscoveredEventV14 | TestCatalogPublishedEventV14 | TestRunStartedEventV14 | TestContainerStartedEventV14 | TestItemStartedEventV14 | TestOutputEventV14 | TestItemFinishedEventV14 | TestContainerFinishedEventV14 | TestRunFinishedEventV14 | CoverageEventV14;
export interface TaskCreatedPayloadV14 { status: "queued"; }
export interface TaskStartedPayloadV14 { status: "running"; }
export interface TaskStepStartedPayloadV14 { stepId: string; kind: TaskStepKindV14; status: "running"; }
export interface TaskOutputPayloadV14 { stepId: string; stream: TaskOutputStreamV14; text: string; truncated: boolean; }
export interface TaskStepFinishedPayloadV14 { stepId: string; kind: TaskStepKindV14; status: TaskStepFinishedStatusV14; exitCode?: number; errorCode?: string; }
export interface TaskCancellationRequestedPayloadV14 { status: "cancelling"; }
export interface ArtifactCreatedPayloadV14 { artifactId: string; kind: string; }
export interface TaskFinishedPayloadV14 { outcome: TaskOutcomeV14; }
export interface TaskDiagnosticPayloadV14 { diagnostic: DiagnosticV14; }
export interface TestDiscoveryStartedPayloadV14 { projectId: string; profileId: string; }
export interface TestContainerDiscoveredPayloadV14 { containerId: string; framework: TestEventFrameworkV14; displayName: string; }
export interface TestCatalogPublishedPayloadV14 { projectId: string; profileId: string; revision: string; containerCount: number; itemCount: number; }
export interface TestRunStartedPayloadV14 { runId: string; catalogRevision: string; total: number; }
export interface TestContainerStartedPayloadV14 { runId: string; containerId: string; iteration: number; }
export interface TestItemStartedPayloadV14 { runId: string; itemId: string; containerId: string; iteration: number; }
export interface TestOutputPayloadV14 { runId: string; containerId: string; itemId?: string; iteration: number; stream: TaskOutputStreamV14; text: string; truncated: boolean; }
export interface TestItemFinishedPayloadV14 { runId: string; result: TestItemResult; }
export interface TestContainerFinishedPayloadV14 { runId: string; containerId: string; iteration: number; outcome: TestContainerOutcomeV14; }
export interface TestRunFinishedPayloadV14 { runId: string; outcome: TestRunOutcomeV14; summary: TestRunSummaryV14; resultRevision: string; incomplete: boolean; }
export interface CoverageRunStartedPayloadV14 { coverageRunId: string; testRunId: string; catalogRevision: string; repeatCount: number; }
export interface CoverageBuildFinishedPayloadV14 { coverageRunId: string; }
export interface CoverageCollectionStartedPayloadV14 { coverageRunId: string; testRunId: string; }
export interface CoverageReportAvailablePayloadV14 { coverageRunId: string; reportId: string; artifactId: string; completeness: CoverageCompletenessV14; summary: CoverageSummaryV14; }
export interface CoverageRunFinishedPayloadV14 { coverageRunId: string; outcome: CoverageRunOutcomeV14; reason?: CoverageRunReasonV14; reportId?: string; }
export enum EventProtocolVersionV14 { The14 = "1.4" }
export enum EventKindV14 { Event = "event" }
export enum TaskEventNameV14 { TaskCreated = "task.created", TaskStarted = "task.started", TaskStepStarted = "task.step_started", TaskOutput = "task.output", TaskStepFinished = "task.step_finished", TaskCancellationRequested = "task.cancellation_requested", ArtifactCreated = "artifact.created", TaskFinished = "task.finished", TaskDiagnostic = "task.diagnostic", TestDiscoveryStarted = "test.discovery.started", TestContainerDiscovered = "test.container.discovered", TestCatalogPublished = "test.catalog.published", TestRunStarted = "test.run.started", TestContainerStarted = "test.container.started", TestItemStarted = "test.item.started", TestOutput = "test.output", TestItemFinished = "test.item.finished", TestContainerFinished = "test.container.finished", TestRunFinished = "test.run.finished", CoverageRunStarted = "coverage.run.started", CoverageBuildFinished = "coverage.build.finished", CoverageCollectionStarted = "coverage.collection.started", CoverageReportAvailable = "coverage.report.available", CoverageRunFinished = "coverage.run.finished" }
export enum TaskStepKindV14 { Simulation = "simulation", Configure = "configure", Build = "build", TestDiscovery = "test-discovery", TestRun = "test-run", CoverageConfigure = "coverage-configure", CoverageBuild = "coverage-build", CoverageTest = "coverage-test", CoverageMerge = "coverage-merge", CoverageNormalize = "coverage-normalize", CoverageReport = "coverage-report", CoveragePublish = "coverage-publish" }
export enum TaskStepFinishedStatusV14 { Succeeded = "succeeded", Failed = "failed", Skipped = "skipped" }
export enum TaskOutputStreamV14 { Stdout = "stdout", Stderr = "stderr", Combined = "combined" }
export enum TestEventFrameworkV14 { Cpputest = "cpputest", Unity = "unity", OpaqueCtest = "opaque-ctest" }
export enum TestContainerOutcomeV14 { Passed = "passed", Failed = "failed", Errored = "errored", Cancelled = "cancelled", TimedOut = "timed_out", NotRun = "not_run" }
