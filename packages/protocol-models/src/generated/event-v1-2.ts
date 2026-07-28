export interface TaskEventBaseV12 { protocolVersion: EventProtocolVersionV12; kind: EventKindV12.Event; messageId: string; sentAt: Date; sequence: number; taskId: string; payloadVersion: 1; }
export interface TaskDiagnosticEventV12 extends TaskEventBaseV12 { event: TaskEventNameV12.TaskDiagnostic; payload: TaskDiagnosticPayloadV12; }
export interface TaskStepStartedEventV12 extends TaskEventBaseV12 { event: TaskEventNameV12.TaskStepStarted; payload: TaskStepStartedPayloadV12; }
export interface TaskStepFinishedEventV12 extends TaskEventBaseV12 { event: TaskEventNameV12.TaskStepFinished; payload: TaskStepFinishedPayloadV12; }
export interface TaskEmptyEventV12 extends TaskEventBaseV12 { event: TaskEmptyEventNameV12; payload: Record<string, never>; }
export type TaskEventV12 = TaskDiagnosticEventV12 | TaskStepStartedEventV12 | TaskStepFinishedEventV12 | TaskEmptyEventV12;
export interface TaskDiagnosticPayloadV12 { diagnostic: TaskEventDiagnosticV12; }
export interface TaskStepStartedPayloadV12 { step: TaskStepNameV12; }
export interface TaskStepFinishedPayloadV12 { step: TaskStepNameV12; outcome: TaskStepOutcomeV12; }
export interface TaskEventDiagnosticV12 { severity: TaskEventDiagnosticSeverityV12; code: string; message: string; sourceUri?: string; line?: number; column?: number; }
export enum EventProtocolVersionV12 { The12 = "1.2" }
export enum EventKindV12 { Event = "event" }
export enum TaskEventNameV12 { TaskDiagnostic = "task.diagnostic", TaskStepStarted = "task.step_started", TaskStepFinished = "task.step_finished" }
export enum TaskEmptyEventNameV12 { TaskCreated = "task.created", TaskStarted = "task.started", TaskOutput = "task.output", TaskCancellationRequested = "task.cancellation_requested", TaskFinished = "task.finished", ArtifactCreated = "artifact.created" }
export enum TaskStepNameV12 { Configure = "configure", Build = "build" }
export enum TaskStepOutcomeV12 { Succeeded = "succeeded", CommandFailed = "command_failed", Cancelled = "cancelled", TimedOut = "timed_out", Interrupted = "interrupted", InfrastructureFailed = "infrastructure_failed" }
export enum TaskEventDiagnosticSeverityV12 { Error = "error", Warning = "warning", Info = "info" }
