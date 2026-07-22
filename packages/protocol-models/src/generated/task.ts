export interface TaskSnapshot {
    createdAt:     Date;
    errorCode?:    string;
    errorMessage?: string;
    finishedAt?:   Date;
    kind:          TaskKind;
    lastSequence:  number;
    outcome?:      Outcome;
    scenario:      Scenario;
    startedAt?:    Date;
    status:        Status;
    taskId:        string;
    timeoutMs?:    number;
}

export enum TaskKind {
    Simulation = "simulation",
}

export enum Outcome {
    Cancelled = "cancelled",
    CommandFailed = "command_failed",
    InfrastructureFailed = "infrastructure_failed",
    Interrupted = "interrupted",
    Succeeded = "succeeded",
    TimedOut = "timed_out",
}

export enum Scenario {
    EmitOutput = "emit-output",
    ExitNonzero = "exit-nonzero",
    Hang = "hang",
    SpawnChild = "spawn-child",
    Success = "success",
}

export enum Status {
    Cancelling = "cancelling",
    Finished = "finished",
    Queued = "queued",
    Running = "running",
}
