export interface WorkspaceSnapshot {
    capabilities:        Capabilities;
    projects:            ProjectElement[];
    workspaceGeneration: string;
    workspaceUri:        string;
}

export interface Capabilities {
    cmakeBuild:       boolean;
    targetList:       boolean;
    workspaceInspect: boolean;
}

export interface ProjectElement {
    buildProfiles: BuildProfileElement[];
    projectId:     string;
    sourceUri:     string;
}

export interface BuildProfileElement {
    buildProfileId: string;
    name:           string;
}
