export interface TargetList {
    buildProfileId:      string;
    projectId:           string;
    targets:             TargetListSchema[];
    workspaceGeneration: string;
}

export interface TargetListSchema {
    name:     string;
    targetId: string;
}
