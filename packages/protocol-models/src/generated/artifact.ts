export interface ArtifactMetadata {
    artifactId: string;
    createdAt:  Date;
    kind:       ArtifactKind;
    mimeType:   MIMEType;
    sha256:     string;
    sizeBytes:  number;
    taskId:     string;
}

export enum ArtifactKind {
    TaskSummary = "task-summary",
}

export enum MIMEType {
    ApplicationJSON = "application/json",
}
