export interface ArtifactMetadataV12 {
    artifactId: string;
    createdAt:  Date;
    kind:       Kind;
    mimeType:   MIMEType;
    sha256:     string;
    sizeBytes:  number;
    taskId:     string;
    uri:        string;
}

export enum Kind {
    TaskSummary = "task-summary",
}

export enum MIMEType {
    ApplicationJSON = "application/json",
}
