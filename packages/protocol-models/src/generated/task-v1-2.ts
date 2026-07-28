export interface TaskSnapshotV12 {
    createdAt:            Date;
    errorCode?:           string;
    errorMessage?:        string;
    finishedAt?:          Date;
    lastSequence:         number;
    outcome?:             Outcome;
    startedAt?:           Date;
    status:               Status;
    taskId:               string;
    timeoutMs?:           number;
    buildProfileId?:      string;
    jobs?:                number;
    kind:                 Kind;
    projectId?:           string;
    targetIds?:           string[];
    workspaceGeneration?: string;
    scenario?:            Scenario;
}

export enum Kind {
    CmakeBuild = "cmakeBuild",
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
