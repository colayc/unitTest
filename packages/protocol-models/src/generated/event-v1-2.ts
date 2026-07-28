export interface TaskEventV12 {
    event:           Event;
    kind:            Kind;
    messageId:       string;
    payload:         Payload;
    payloadVersion:  number;
    protocolVersion: ProtocolVersion;
    sentAt:          Date;
    sequence:        number;
    taskId:          string;
}

export enum Event {
    ArtifactCreated = "artifact.created",
    TaskCancellationRequested = "task.cancellation_requested",
    TaskCreated = "task.created",
    TaskDiagnostic = "task.diagnostic",
    TaskFinished = "task.finished",
    TaskOutput = "task.output",
    TaskStarted = "task.started",
}

export enum Kind {
    Event = "event",
}

export interface Payload {
    diagnostic?: Diagnostic;
}

export interface Diagnostic {
    code:       string;
    column?:    number;
    line?:      number;
    message:    string;
    severity:   Severity;
    sourceUri?: string;
}

export enum Severity {
    Error = "error",
    Info = "info",
    Warning = "warning",
}

export enum ProtocolVersion {
    The12 = "1.2",
}
