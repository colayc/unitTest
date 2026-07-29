export interface TaskEventBaseV12 { protocolVersion: EventProtocolVersionV12; kind: EventKindV12.Event; messageId: string; sentAt: Date; sequence: number; taskId: string; payloadVersion: 1; }
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
