export interface TargetList {
    projectId:           string;
    targets:             TargetListSchema[];
    workspaceGeneration: string;
}

export interface TargetListSchema {
    name:     string;
    targetId: string;
}
