export interface CapabilitiesV11 {
    artifactRead:       boolean;
    coverageTools:      string[];
    eventReplay:        boolean;
    frameworks:         string[];
    platform:           Platform;
    processTreeControl: ProcessTreeControl;
    sqliteHistory:      boolean;
    taskExecution:      boolean;
    toolchains:         string[];
    transports:         Transport[];
}

export enum Platform {
    Linux = "linux",
    Windows = "windows",
}

export enum ProcessTreeControl {
    JobObject = "job-object",
    ProcessGroup = "process-group",
}

export enum Transport {
    NamedPipe = "named-pipe",
    UnixSocket = "unix-socket",
}
