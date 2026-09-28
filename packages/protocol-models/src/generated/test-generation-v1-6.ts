export interface TestGenerationContractV16 {
    acceptRequest:         TestGenerationAcceptRequestV16;
    candidateListRequest:  TestGenerationCandidateListRequestV16;
    candidatePage:         TestGenerationCandidatePageV16;
    eventPage:             TestGenerationEventPageV16;
    eventReplayRequest:    TestGenerationEventReplayRequestV16;
    managedRecordPage:     ManagedTestRecordPageV16;
    managedRecordsRequest: ManagedRecordsRequestV16;
    managedReview:         ManagedReviewV16;
    reviewApplyRequest:    ManagedReviewApplyRequestV16;
    reviewApplyResult:     ManagedReviewApplyResultV16;
    reviewIdRequest:       ManagedReviewIDRequestV16;
    run:                   TestGenerationRunV16;
    runIdRequest:          TestGenerationRunIDRequestV16;
    startRequest:          TestGenerationStartRequestV16;
    targetList:            TestGenerationTargetListV16;
    targetListRequest:     TestGenerationTargetListRequestV16;
}

/**
 * The service MUST resolve the authoritative stored candidate kind from runId and
 * candidateId, verify confirmationDigest against the current preview, and MUST reject a
 * characterization candidate unless confirmCharacterization is true. Caller-supplied
 * candidate classification is forbidden.
 */
export interface TestGenerationAcceptRequestV16 {
    candidateId:        string;
    confirmationDigest: string;
    /**
     * Explicit consent for a characterization candidate. The service checks this against the
     * stored kind resolved by runId and candidateId.
     */
    confirmCharacterization: boolean;
    runId:                   string;
}

export interface TestGenerationCandidateListRequestV16 {
    cursor?: string;
    limit?:  number;
    runId:   string;
}

export interface TestGenerationCandidatePageV16 {
    items:       TestGenerationCandidateV16[];
    nextCursor?: string;
}

export interface TestGenerationCandidateV16 {
    artifactDigest:            string;
    assertionProvenance:       TestGenerationAssertionProvenanceV16;
    baselineCoverage:          TestGenerationCoverageV16;
    candidateId:               string;
    characterizationConfirmed: boolean;
    codeDigest:                string;
    deltaCoverage:             TestGenerationCoverageV16;
    diagnostics:               TestGenerationDiagnosticV16[];
    kind:                      TestGenerationCandidateKindV16;
    plannedEdits:              TestGenerationPlannedEditV16[];
}

export interface TestGenerationAssertionProvenanceV16 {
    evidenceDigest: string;
    kind:           Kind;
}

export enum Kind {
    IndependentOracle = "independent-oracle",
    ObservedOutput = "observed-output",
}

export interface TestGenerationCoverageV16 {
    branchPercent:   number;
    functionPercent: number;
    linePercent:     number;
}

export interface TestGenerationDiagnosticV16 {
    code:     TestGenerationDiagnosticCodeV16;
    reason?:  TestGenerationDiagnosticReasonV16;
    severity: Severity;
}

export enum TestGenerationDiagnosticCodeV16 {
    BudgetExceeded = "BUDGET_EXCEEDED",
    CoverageGap = "COVERAGE_GAP",
    NoCandidate = "NO_CANDIDATE",
    OracleUnavailable = "ORACLE_UNAVAILABLE",
    TargetUnsupported = "TARGET_UNSUPPORTED",
    ValidationFailed = "VALIDATION_FAILED",
}

export enum TestGenerationDiagnosticReasonV16 {
    BudgetLimit = "budget-limit",
    OracleUnavailable = "oracle-unavailable",
    UncoveredBranch = "uncovered-branch",
    UncoveredFunction = "uncovered-function",
    UncoveredLine = "uncovered-line",
    UnsupportedTarget = "unsupported-target",
    ValidationFailed = "validation-failed",
}

export enum Severity {
    Error = "error",
    Info = "info",
    Warning = "warning",
}

export enum TestGenerationCandidateKindV16 {
    Characterization = "characterization",
    Verified = "verified",
}

export interface TestGenerationPlannedEditV16 {
    afterDigest:   string;
    beforeDigest?: string;
    operation:     Operation;
    path:          string;
}

export enum Operation {
    Create = "create",
    Modify = "modify",
}

export interface TestGenerationEventPageV16 {
    items:             TestGenerationProgressEventV16[];
    nextAfterSequence: number;
}

export interface TestGenerationProgressEventV16 {
    occurredAt: Date;
    sequence:   number;
    state:      TestGenerationStateV16;
}

export enum TestGenerationStateV16 {
    Accepted = "accepted",
    Analyzing = "analyzing",
    AwaitingConfirmation = "awaiting_confirmation",
    Baseline = "baseline",
    Cancelled = "cancelled",
    Failed = "failed",
    Minimizing = "minimizing",
    Queued = "queued",
    Rejected = "rejected",
    Rendering = "rendering",
    Solving = "solving",
    Validating = "validating",
}

export interface TestGenerationEventReplayRequestV16 {
    afterSequence: number;
    limit?:        number;
    runId:         string;
}

export interface ManagedTestRecordPageV16 {
    coverageReportId:    string;
    items:               ManagedTestRecordV16[];
    nextCursor?:         string;
    workspaceGeneration: string;
}

export interface ManagedTestRecordV16 {
    absentSides?:     string[];
    acceptedDigest:   string;
    caseId:           string;
    currentDigest:    string;
    fileId:           string;
    functionId:       string;
    generatedDigest?: string;
    reviewId?:        string;
    status:           ManagedTestStatusV16;
}

export enum ManagedTestStatusV16 {
    Conflicted = "conflicted",
    Current = "current",
    Invalid = "invalid",
    Orphaned = "orphaned",
    Stale = "stale",
}

export interface ManagedRecordsRequestV16 {
    coverageReportId:    string;
    cursor?:             string;
    fileId?:             string;
    limit?:              number;
    projectId:           string;
    status?:             ManagedTestStatusV16;
    workspaceGeneration: string;
}

/**
 * A bounded review page. The service MUST bind nextCursor to reviewId, reviewDigest and
 * limit, and reject pages exceeding the 512 KiB serialized payload budget; callers continue
 * with nextCursor rather than truncating cases.
 */
export interface ManagedReviewV16 {
    cases:            ManagedReviewCaseV16[];
    conflictKeys?:    string[];
    coverageReportId: string;
    nextCursor?:      string;
    /**
     * Optional SHA-256 of the exact page preview manifest: UTF-8 managed-review-preview-v1
     * newline, reviewDigest newline, sorted c:caseId:SHA256(diff) newline entries, then sorted
     * s:scaffoldKey:diffDigest newline entries. Without this verified field, diff text is
     * display-unavailable and cannot authorize Apply.
     */
    previewArtifactDigest?: string;
    reviewDigest:           string;
    reviewId:               string;
    scaffoldPreviews?:      ScaffoldPreview[];
    workspaceGeneration:    string;
}

export interface ManagedReviewCaseV16 {
    absentSides?:    string[];
    acceptedDigest:  string;
    caseId:          string;
    currentDigest:   string;
    diff?:           string;
    generatedDigest: string;
    status:          ManagedTestStatusV16;
}

export interface ScaffoldPreview {
    diff:       string;
    diffDigest: string;
    key:        string;
}

export interface ManagedReviewApplyRequestV16 {
    resolutions:  ManagedReviewResolutionV16[];
    reviewDigest: string;
    reviewId:     string;
}

export interface ManagedReviewResolutionV16 {
    caseId: string;
    choice: ManagedConflictChoiceV16;
}

export enum ManagedConflictChoiceV16 {
    ConvertToManual = "convert-to-manual",
    KeepCurrent = "keep-current",
    UseGenerated = "use-generated",
}

export interface ManagedReviewApplyResultV16 {
    applied:      boolean;
    reviewDigest: string;
    reviewId:     string;
}

export interface ManagedReviewIDRequestV16 {
    cursor?:  string;
    limit?:   number;
    reviewId: string;
}

/**
 * Managed runs bind exact changes to managedTests/reviews/get; preview is optional and must
 * never be fabricated from digests.
 */
export interface TestGenerationRunV16 {
    candidateCount?:     number;
    createdAt:           Date;
    finishedAt?:         Date;
    lastSequence:        number;
    preview?:            TestGenerationPreviewV16;
    projectId:           string;
    runId:               string;
    state:               TestGenerationStateV16;
    taskId:              string;
    workspaceGeneration: string;
}

export interface TestGenerationPreviewV16 {
    candidateSetDigest:      string;
    characterizationDigest?: string;
    confirmationDigest:      string;
    diff?:                   string;
    diffDigest:              string;
}

export interface TestGenerationRunIDRequestV16 {
    runId: string;
}

export interface TestGenerationStartRequestV16 {
    budgets:             TestGenerationBudgetsV16;
    coverageGapId?:      string;
    coverageReportId?:   string;
    fileId?:             string;
    framework:           TestGenerationFrameworkV16;
    functionId?:         string;
    goals:               TestGenerationGoalsV16;
    idempotencyKey:      string;
    projectId:           string;
    scope:               TestGenerationScopeV16;
    targetId?:           string;
    workspaceGeneration: string;
}

export interface TestGenerationBudgetsV16 {
    candidateCount: number;
    concurrency:    number;
    memoryMiB:      number;
    wallTimeMs:     number;
}

export enum TestGenerationFrameworkV16 {
    Auto = "auto",
    Cpputest = "cpputest",
    Unity = "unity",
}

export interface TestGenerationGoalsV16 {
    branchPercent:   number;
    functionPercent: number;
    linePercent:     number;
}

export enum TestGenerationScopeV16 {
    CoverageGap = "coverage-gap",
    File = "file",
    Symbol = "symbol",
    Target = "target",
    Workspace = "workspace",
}

export interface TestGenerationTargetListV16 {
    items:       TestGenerationTargetV16[];
    nextCursor?: string;
}

export interface TestGenerationTargetV16 {
    frameworks: Framework[];
    kind:       TestGenerationTargetKindV16;
    /**
     * Stable opaque target digest; no display label or source text is carried.
     */
    targetId: string;
}

export enum Framework {
    Cpputest = "cpputest",
    Unity = "unity",
}

export enum TestGenerationTargetKindV16 {
    BuildTarget = "build-target",
}

export interface TestGenerationTargetListRequestV16 {
    cursor?:             string;
    limit?:              number;
    projectId:           string;
    workspaceGeneration: string;
}
