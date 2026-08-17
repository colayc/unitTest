export interface ArtifactMetadataV13 {
    artifactId: string;
    createdAt:  Date;
    kind:       ArtifactKindV13;
    mimeType:   ArtifactMIMETypeV13;
    sha256:     string;
    sizeBytes:  number;
    taskId:     string;
    uri:        string;
}

export enum ArtifactKindV13 {
    BuildSummary = "build-summary",
    Diagnostics = "diagnostics",
    ExecutionPlan = "execution-plan",
    Stderr = "stderr",
    Stdout = "stdout",
    TaskSummary = "task-summary",
    TestCatalog = "test-catalog",
    TestDiagnostics = "test-diagnostics",
    TestOutput = "test-output",
    TestResults = "test-results",
    TestRunSummary = "test-run-summary",
    TestSelection = "test-selection",
}

export enum ArtifactMIMETypeV13 {
    ApplicationJSON = "application/json",
    ApplicationOctetStream = "application/octet-stream",
    ApplicationXNdjson = "application/x-ndjson",
}
