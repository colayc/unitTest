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
    BuildSummary = "build-summary",
    Diagnostics = "diagnostics",
    ExecutionPlan = "execution-plan",
    Stderr = "stderr",
    Stdout = "stdout",
    TaskSummary = "task-summary",
}

export enum MIMEType {
    ApplicationJSON = "application/json",
    ApplicationOctetStream = "application/octet-stream",
    ApplicationXNdjson = "application/x-ndjson",
}
