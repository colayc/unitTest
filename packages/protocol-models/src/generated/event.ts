export interface TaskEvent {
    event:           Event;
    kind:            EventKind;
    messageId:       string;
    payload:         { [key: string]: any };
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
    TaskFinished = "task.finished",
    TaskOutput = "task.output",
    TaskStarted = "task.started",
}

export enum EventKind {
    Event = "event",
}

export enum ProtocolVersion {
    The11 = "1.1",
}
