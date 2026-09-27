export interface TestGenerationContractV15 {
    acceptRequest:        TestGenerationAcceptRequestV15;
    candidateListRequest: TestGenerationCandidateListRequestV15;
    candidatePage:        TestGenerationCandidatePageV15;
    eventPage:            TestGenerationEventPageV15;
    eventReplayRequest:   TestGenerationEventReplayRequestV15;
    run:                  TestGenerationRunV15;
    runIdRequest:         TestGenerationRunIDRequestV15;
    startRequest:         TestGenerationStartRequestV15;
    targetList:           TestGenerationTargetListV15;
    targetListRequest:    TestGenerationTargetListRequestV15;
}

/**
 * The service MUST resolve the authoritative stored candidate kind from runId and
 * candidateId, verify confirmationDigest against the current preview, and MUST reject a
 * characterization candidate unless confirmCharacterization is true. Caller-supplied
 * candidate classification is forbidden.
 */
export interface TestGenerationAcceptRequestV15 {
    candidateId:        string;
    confirmationDigest: string;
    /**
     * Explicit consent for a characterization candidate. The service checks this against the
     * stored kind resolved by runId and candidateId.
     */
    confirmCharacterization: boolean;
    runId:                   string;
}

export interface TestGenerationCandidateListRequestV15 {
    cursor?: string;
    limit?:  number;
    runId:   string;
}

export interface TestGenerationCandidatePageV15 {
    items:       TestGenerationCandidateV15[];
    nextCursor?: string;
}

export interface TestGenerationCandidateV15 {
    artifactDigest:            string;
    assertionProvenance:       TestGenerationAssertionProvenanceV15;
    baselineCoverage:          TestGenerationCoverageV15;
    candidateId:               string;
    characterizationConfirmed: boolean;
    codeDigest:                string;
    deltaCoverage:             TestGenerationCoverageV15;
    diagnostics:               TestGenerationDiagnosticV15[];
    kind:                      TestGenerationCandidateKindV15;
    plannedEdits:              TestGenerationPlannedEditV15[];
}

export interface TestGenerationAssertionProvenanceV15 {
    evidenceDigest: string;
    kind:           Kind;
}

export enum Kind {
    IndependentOracle = "independent-oracle",
    ObservedOutput = "observed-output",
}

export interface TestGenerationCoverageV15 {
    branchPercent:   number;
    functionPercent: number;
    linePercent:     number;
}

export interface TestGenerationDiagnosticV15 {
    code:     TestGenerationDiagnosticCodeV15;
    reason?:  TestGenerationDiagnosticReasonV15;
    severity: Severity;
}

export enum TestGenerationDiagnosticCodeV15 {
    BudgetExceeded = "BUDGET_EXCEEDED",
    CoverageGap = "COVERAGE_GAP",
    NoCandidate = "NO_CANDIDATE",
    OracleUnavailable = "ORACLE_UNAVAILABLE",
    TargetUnsupported = "TARGET_UNSUPPORTED",
    ValidationFailed = "VALIDATION_FAILED",
}

export enum TestGenerationDiagnosticReasonV15 {
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

export enum TestGenerationCandidateKindV15 {
    Characterization = "characterization",
    Verified = "verified",
}

export interface TestGenerationPlannedEditV15 {
    afterDigest:   string;
    beforeDigest?: string;
    operation:     Operation;
    path:          string;
}

export enum Operation {
    Create = "create",
    Modify = "modify",
}

export interface TestGenerationEventPageV15 {
    items:             TestGenerationProgressEventV15[];
    nextAfterSequence: number;
}

export interface TestGenerationProgressEventV15 {
    occurredAt: Date;
    sequence:   number;
    state:      TestGenerationStateV15;
}

export enum TestGenerationStateV15 {
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

export interface TestGenerationEventReplayRequestV15 {
    afterSequence: number;
    limit?:        number;
    runId:         string;
}

export interface TestGenerationRunV15 {
    candidateCount?:     number;
    createdAt:           Date;
    finishedAt?:         Date;
    lastSequence:        number;
    preview?:            TestGenerationPreviewV15;
    projectId:           string;
    runId:               string;
    state:               TestGenerationStateV15;
    taskId:              string;
    workspaceGeneration: string;
}

export interface TestGenerationPreviewV15 {
    candidateSetDigest:      string;
    characterizationDigest?: string;
    confirmationDigest:      string;
    diffDigest:              string;
}

export interface TestGenerationRunIDRequestV15 {
    runId: string;
}

export interface TestGenerationStartRequestV15 {
    budgets:             TestGenerationBudgetsV15;
    coverageReportId?:   string;
    file?:               string;
    framework:           TestGenerationFrameworkV15;
    goals:               TestGenerationGoalsV15;
    idempotencyKey:      string;
    projectId:           string;
    scope:               TestGenerationScopeV15;
    symbolId?:           string;
    targetId?:           string;
    workspaceGeneration: string;
}

export interface TestGenerationBudgetsV15 {
    candidateCount: number;
    concurrency:    number;
    memoryMiB:      number;
    wallTimeMs:     number;
}

export enum TestGenerationFrameworkV15 {
    Auto = "auto",
    Cpputest = "cpputest",
    Unity = "unity",
}

export interface TestGenerationGoalsV15 {
    branchPercent:   number;
    functionPercent: number;
    linePercent:     number;
}

export enum TestGenerationScopeV15 {
    CoverageGap = "coverage-gap",
    File = "file",
    Symbol = "symbol",
    Target = "target",
    Workspace = "workspace",
}

export interface TestGenerationTargetListV15 {
    items:       TestGenerationTargetV15[];
    nextCursor?: string;
}

export interface TestGenerationTargetV15 {
    frameworks: Framework[];
    kind:       TestGenerationTargetKindV15;
    /**
     * Stable opaque target digest; no display label or source text is carried.
     */
    targetId: string;
}

export enum Framework {
    Cpputest = "cpputest",
    Unity = "unity",
}

export enum TestGenerationTargetKindV15 {
    BuildTarget = "build-target",
}

export interface TestGenerationTargetListRequestV15 {
    cursor?:             string;
    limit?:              number;
    projectId:           string;
    workspaceGeneration: string;
}
