export interface CoverageContractV16 {
    detailFilesRequest:     CoverageDetailFilesRequestV16;
    detailFunctionsRequest: CoverageDetailFunctionsRequestV16;
    detailLinesRequest:     CoverageDetailLinesRequestV16;
    detailProject:          CoverageProjectV16;
    detailProjectRequest:   CoverageDetailProjectRequestV16;
    filePage:               CoverageFilePageV16;
    functionPage:           CoverageFunctionPageV16;
    linePage:               CoverageLinePageV16;
    report:                 CoverageReportV16;
    run:                    CoverageRunV16;
    runPage:                CoverageRunPageV16;
    runStartRequest:        CoverageRunStartRequestV16;
}

export interface CoverageDetailFilesRequestV16 {
    coverageReportId:    string;
    cursor?:             string;
    limit?:              number;
    projectId:           string;
    workspaceGeneration: string;
}

export interface CoverageDetailFunctionsRequestV16 {
    coverageReportId:    string;
    cursor?:             string;
    fileId:              string;
    limit?:              number;
    workspaceGeneration: string;
}

export interface CoverageDetailLinesRequestV16 {
    coverageReportId:    string;
    cursor?:             string;
    fileId?:             string;
    functionId?:         string;
    limit?:              number;
    workspaceGeneration: string;
}

export interface CoverageProjectV16 {
    coverageReportId:    string;
    fileCount:           number;
    projectId:           string;
    reasons:             CoverageDetailReasonV16[];
    status:              CoverageDetailStatusV16;
    summary:             CoverageSummaryV16;
    workspaceGeneration: string;
}

export enum CoverageDetailReasonV16 {
    AttributionAmbiguous = "attribution_ambiguous",
    BaselineUnavailable = "baseline_unavailable",
    ReportPartial = "report_partial",
    SourceChanged = "source_changed",
    SourceMissing = "source_missing",
    ToolIdentityChanged = "tool_identity_changed",
}

export enum CoverageDetailStatusV16 {
    Current = "current",
    Incomplete = "incomplete",
    Stale = "stale",
}

export interface CoverageSummaryV16 {
    branches:  CoverageMetricV16;
    functions: CoverageMetricV16;
    lines:     CoverageMetricV16;
}

export interface CoverageMetricV16 {
    covered:      number;
    coveredDelta: number;
    total:        number;
}

export interface CoverageDetailProjectRequestV16 {
    coverageReportId:    string;
    projectId:           string;
    workspaceGeneration: string;
}

export interface CoverageFilePageV16 {
    coverageReportId:    string;
    items:               CoverageFileV16[];
    nextCursor?:         string;
    workspaceGeneration: string;
}

export interface CoverageFileV16 {
    fileId:        string;
    functionCount: number;
    reasons:       CoverageDetailReasonV16[];
    relativePath:  string;
    sourceSha256:  string;
    status:        CoverageDetailStatusV16;
    summary:       CoverageSummaryV16;
}

export interface CoverageFunctionPageV16 {
    coverageReportId:    string;
    items:               CoverageFunctionV16[];
    nextCursor?:         string;
    workspaceGeneration: string;
}

export interface CoverageFunctionV16 {
    coverageGapId?: string;
    endLine:        number;
    fileId:         string;
    functionId:     string;
    qualifiedName:  string;
    reasons:        CoverageDetailReasonV16[];
    startLine:      number;
    status:         CoverageDetailStatusV16;
    summary:        CoverageSummaryV16;
}

export interface CoverageLinePageV16 {
    coverageReportId:    string;
    items:               CoverageLineDetailV16[];
    nextCursor?:         string;
    workspaceGeneration: string;
}

export interface CoverageLineDetailV16 {
    baselineBranchesCovered: number;
    baselineBranchesTotal:   number;
    baselineCount:           number;
    branchesCovered:         number;
    branchesTotal:           number;
    count:                   number;
    coverageGapId?:          string;
    line:                    number;
}

export interface CoverageReportV16 {
    artifactId:     string;
    completeness:   CoverageCompletenessV16;
    coverageRunId:  string;
    createdAt:      Date;
    reportId:       string;
    schemaVersion:  SchemaVersion;
    sources?:       CoverageSourceSnapshotV16[];
    summary:        CoverageReportSummaryV16;
    testRunId:      string;
    toolProvenance: CoverageToolProvenanceV16;
}

export interface CoverageCompletenessV16 {
    outcome: CompletenessOutcome;
    reasons: ReasonElement[];
}

export enum CompletenessOutcome {
    Available = "available",
    Partial = "partial",
}

export enum ReasonElement {
    ProfileMissingForFailedInvocation = "profile_missing_for_failed_invocation",
    TestCrashed = "test_crashed",
    TestTimedOut = "test_timed_out",
}

export enum SchemaVersion {
    The10 = "1.0",
}

export interface CoverageSourceSnapshotV16 {
    sha256: string;
    uri:    string;
}

export interface CoverageReportSummaryV16 {
    branches:  CoverageReportMetricV16;
    functions: CoverageReportMetricV16;
    lines:     CoverageReportMetricV16;
}

export interface CoverageReportMetricV16 {
    covered: number;
    total:   number;
}

export interface CoverageToolProvenanceV16 {
    architecture:               Architecture;
    collector:                  CoverageCollectorV16;
    compiler:                   CoverageCompilerV16;
    driver:                     CoverageDriverV16;
    instrumentationFingerprint: string;
    normalizerVersion:          string;
    platform:                   Platform;
}

export enum Architecture {
    Arm64 = "arm64",
    X64 = "x64",
    X86 = "x86",
}

export interface CoverageCollectorV16 {
    name:    CollectorName;
    version: string;
}

export enum CollectorName {
    Gcovr = "gcovr",
    LlvmCov = "llvm-cov",
}

export interface CoverageCompilerV16 {
    family:  Family;
    version: string;
}

export enum Family {
    Clang = "clang",
    ClangCl = "clang-cl",
    GCC = "gcc",
}

export interface CoverageDriverV16 {
    name:    DriverName;
    version: string;
}

export enum DriverName {
    Gcov = "gcov",
    LlvmCov = "llvm-cov",
}

export enum Platform {
    Linux = "linux",
    Windows = "windows",
}

export interface CoverageRunV16 {
    catalogRevision:     string;
    coverageProfileId:   string;
    coverageRunId:       string;
    createdAt:           Date;
    finishedAt?:         Date;
    lastSequence:        number;
    outcome?:            CoverageRunOutcomeV16;
    projectId:           string;
    reason?:             CoverageRunReasonV16;
    repeatCount:         number;
    reportId?:           string;
    selectionSnapshot:   TestSelectionSnapshotV16;
    startedAt?:          Date;
    status:              Status;
    taskId:              string;
    testRunId:           string;
    timeoutMs:           number;
    workspaceGeneration: string;
}

export enum CoverageRunOutcomeV16 {
    Available = "available",
    Cancelled = "cancelled",
    Partial = "partial",
    Unavailable = "unavailable",
}

export enum CoverageRunReasonV16 {
    BuildFailed = "build_failed",
    InstrumentationFailed = "instrumentation_failed",
    MergeFailed = "merge_failed",
    NormalizationFailed = "normalization_failed",
    PersistenceFailed = "persistence_failed",
    ProfileCollectionFailed = "profile_collection_failed",
    ReportGenerationFailed = "report_generation_failed",
    ServiceRestarted = "service_restarted",
    TaskTimedOut = "task_timed_out",
    UserCancelled = "user_cancelled",
}

export interface TestSelectionSnapshotV16 {
    containerIds: string[];
    itemIds:      string[];
    mode:         TestSelectionModeV16;
}

export enum TestSelectionModeV16 {
    All = "all",
    Containers = "containers",
    FailedFromRun = "failedFromRun",
    Filter = "filter",
    Items = "items",
}

export enum Status {
    Finished = "finished",
    Queued = "queued",
    Running = "running",
}

export interface CoverageRunPageV16 {
    items:       CoverageRunV16[];
    nextCursor?: string;
}

export interface CoverageRunStartRequestV16 {
    catalogRevision:     string;
    coverageProfileId:   string;
    idempotencyKey:      string;
    projectId:           string;
    repeatCount:         number;
    selection:           TestSelection;
    timeoutMs:           number;
    workspaceGeneration: string;
}

export interface TestSelection {
    mode:          TestSelectionModeV16;
    containerIds?: string[];
    itemIds?:      string[];
    filter?:       TestFilterV16;
    runId?:        string;
}

export interface TestFilterV16 {
    excludeItemIds?: string[];
    group?:          string;
    includeItemIds?: string[];
    label?:          string;
    nameContains?:   string;
    suite?:          string;
}
