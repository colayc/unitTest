export interface ArtifactMetadataV16 {
    artifactId: string;
    createdAt:  Date;
    kind:       ArtifactKindV16;
    mimeType:   ArtifactMIMETypeV16;
    sha256:     string;
    sizeBytes:  number;
    taskId:     string;
    /**
     * Opaque service artifact URI only; filesystem and network URIs are forbidden in protocol
     * v1.6.
     */
    uri: string;
}

export enum ArtifactKindV16 {
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

export enum ArtifactMIMETypeV16 {
    ApplicationJSON = "application/json",
    ApplicationOctetStream = "application/octet-stream",
    ApplicationXML = "application/xml",
    ApplicationXNdjson = "application/x-ndjson",
    TextHTML = "text/html",
}
