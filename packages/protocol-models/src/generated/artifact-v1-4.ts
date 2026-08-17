export interface ArtifactMetadataV14 {
    artifactId: string;
    createdAt:  Date;
    kind:       ArtifactKindV14;
    mimeType:   ArtifactMIMETypeV14;
    sha256:     string;
    sizeBytes:  number;
    taskId:     string;
    uri:        string;
}

export enum ArtifactKindV14 {
    BuildSummary = "build-summary",
    CoverageHTML = "coverage-html",
    CoverageJSON = "coverage-json",
    Diagnostics = "diagnostics",
    ExecutionPlan = "execution-plan",
    JunitXML = "junit-xml",
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

export enum ArtifactMIMETypeV14 {
    ApplicationJSON = "application/json",
    ApplicationOctetStream = "application/octet-stream",
    ApplicationXML = "application/xml",
    ApplicationXNdjson = "application/x-ndjson",
    TextHTML = "text/html",
}
