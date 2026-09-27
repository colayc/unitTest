package protocolmodelv15testgeneration

import "time"

type TestGenerationContractV15 struct {
	AcceptRequest        TestGenerationAcceptRequestV15        `json:"acceptRequest"`
	CandidateListRequest TestGenerationCandidateListRequestV15 `json:"candidateListRequest"`
	CandidatePage        TestGenerationCandidatePageV15        `json:"candidatePage"`
	Run                  TestGenerationRunV15                  `json:"run"`
	RunIDRequest         TestGenerationRunIDRequestV15         `json:"runIdRequest"`
	StartRequest         TestGenerationStartRequestV15         `json:"startRequest"`
	TargetList           TestGenerationTargetListV15           `json:"targetList"`
	TargetListRequest    TestGenerationTargetListRequestV15    `json:"targetListRequest"`
}

// The service MUST resolve the authoritative stored candidate kind from runId and
// candidateId, verify confirmationDigest against the current preview, and MUST reject a
// characterization candidate unless confirmCharacterization is true. Caller-supplied
// candidate classification is forbidden.
type TestGenerationAcceptRequestV15 struct {
	CandidateID        string `json:"candidateId"`
	ConfirmationDigest string `json:"confirmationDigest"`
	// Explicit consent for a characterization candidate. The service checks this against the
	// stored kind resolved by runId and candidateId.
	ConfirmCharacterization bool   `json:"confirmCharacterization"`
	RunID                   string `json:"runId"`
}

type TestGenerationCandidateListRequestV15 struct {
	Cursor *string `json:"cursor,omitempty"`
	Limit  *int64  `json:"limit,omitempty"`
	RunID  string  `json:"runId"`
}

type TestGenerationCandidatePageV15 struct {
	Items      []TestGenerationCandidateV15 `json:"items"`
	NextCursor *string                      `json:"nextCursor,omitempty"`
}

type TestGenerationCandidateV15 struct {
	ArtifactDigest            string                               `json:"artifactDigest"`
	AssertionProvenance       TestGenerationAssertionProvenanceV15 `json:"assertionProvenance"`
	BaselineCoverage          TestGenerationCoverageV15            `json:"baselineCoverage"`
	CandidateID               string                               `json:"candidateId"`
	CharacterizationConfirmed bool                                 `json:"characterizationConfirmed"`
	CodeDigest                string                               `json:"codeDigest"`
	DeltaCoverage             TestGenerationCoverageV15            `json:"deltaCoverage"`
	Diagnostics               []TestGenerationDiagnosticV15        `json:"diagnostics"`
	Kind                      TestGenerationCandidateKindV15       `json:"kind"`
	PlannedEdits              []TestGenerationPlannedEditV15       `json:"plannedEdits"`
}

type TestGenerationAssertionProvenanceV15 struct {
	EvidenceDigest string `json:"evidenceDigest"`
	Kind           Kind   `json:"kind"`
}

type TestGenerationCoverageV15 struct {
	BranchPercent   float64 `json:"branchPercent"`
	FunctionPercent float64 `json:"functionPercent"`
	LinePercent     float64 `json:"linePercent"`
}

type TestGenerationDiagnosticV15 struct {
	Code     TestGenerationDiagnosticCodeV15    `json:"code"`
	Reason   *TestGenerationDiagnosticReasonV15 `json:"reason,omitempty"`
	Severity Severity                           `json:"severity"`
}

type TestGenerationPlannedEditV15 struct {
	AfterDigest  string    `json:"afterDigest"`
	BeforeDigest *string   `json:"beforeDigest,omitempty"`
	Operation    Operation `json:"operation"`
	Path         string    `json:"path"`
}

type TestGenerationRunV15 struct {
	CandidateCount      *int64                    `json:"candidateCount,omitempty"`
	CreatedAt           time.Time                 `json:"createdAt"`
	FinishedAt          *time.Time                `json:"finishedAt,omitempty"`
	LastSequence        int64                     `json:"lastSequence"`
	Preview             *TestGenerationPreviewV15 `json:"preview,omitempty"`
	ProjectID           string                    `json:"projectId"`
	RunID               string                    `json:"runId"`
	State               TestGenerationStateV15    `json:"state"`
	TaskID              string                    `json:"taskId"`
	WorkspaceGeneration string                    `json:"workspaceGeneration"`
}

type TestGenerationPreviewV15 struct {
	CandidateSetDigest     string  `json:"candidateSetDigest"`
	CharacterizationDigest *string `json:"characterizationDigest,omitempty"`
	ConfirmationDigest     string  `json:"confirmationDigest"`
	DiffDigest             string  `json:"diffDigest"`
}

type TestGenerationRunIDRequestV15 struct {
	RunID string `json:"runId"`
}

type TestGenerationStartRequestV15 struct {
	Budgets             TestGenerationBudgetsV15   `json:"budgets"`
	CoverageReportID    *string                    `json:"coverageReportId,omitempty"`
	File                *string                    `json:"file,omitempty"`
	Framework           TestGenerationFrameworkV15 `json:"framework"`
	Goals               TestGenerationGoalsV15     `json:"goals"`
	IdempotencyKey      string                     `json:"idempotencyKey"`
	ProjectID           string                     `json:"projectId"`
	Scope               TestGenerationScopeV15     `json:"scope"`
	SymbolID            *string                    `json:"symbolId,omitempty"`
	TargetID            *string                    `json:"targetId,omitempty"`
	WorkspaceGeneration string                     `json:"workspaceGeneration"`
}

type TestGenerationBudgetsV15 struct {
	CandidateCount int64 `json:"candidateCount"`
	Concurrency    int64 `json:"concurrency"`
	MemoryMiB      int64 `json:"memoryMiB"`
	WallTimeMS     int64 `json:"wallTimeMs"`
}

type TestGenerationGoalsV15 struct {
	BranchPercent   float64 `json:"branchPercent"`
	FunctionPercent float64 `json:"functionPercent"`
	LinePercent     float64 `json:"linePercent"`
}

type TestGenerationTargetListV15 struct {
	Items      []TestGenerationTargetV15 `json:"items"`
	NextCursor *string                   `json:"nextCursor,omitempty"`
}

type TestGenerationTargetV15 struct {
	Frameworks []Framework                 `json:"frameworks"`
	Kind       TestGenerationTargetKindV15 `json:"kind"`
	// Stable opaque target digest; no display label or source text is carried.
	TargetID string `json:"targetId"`
}

type TestGenerationTargetListRequestV15 struct {
	Cursor              *string `json:"cursor,omitempty"`
	Limit               *int64  `json:"limit,omitempty"`
	ProjectID           string  `json:"projectId"`
	WorkspaceGeneration string  `json:"workspaceGeneration"`
}

type Kind string

const (
	IndependentOracle Kind = "independent-oracle"
	ObservedOutput    Kind = "observed-output"
)

type TestGenerationDiagnosticCodeV15 string

const (
	BudgetExceeded    TestGenerationDiagnosticCodeV15 = "BUDGET_EXCEEDED"
	CoverageGap       TestGenerationDiagnosticCodeV15 = "COVERAGE_GAP"
	NoCandidate       TestGenerationDiagnosticCodeV15 = "NO_CANDIDATE"
	OracleUnavailable TestGenerationDiagnosticCodeV15 = "ORACLE_UNAVAILABLE"
	TargetUnsupported TestGenerationDiagnosticCodeV15 = "TARGET_UNSUPPORTED"
	ValidationFailed  TestGenerationDiagnosticCodeV15 = "VALIDATION_FAILED"
)

type TestGenerationDiagnosticReasonV15 string

const (
	BudgetLimit                                        TestGenerationDiagnosticReasonV15 = "budget-limit"
	TestGenerationDiagnosticReasonV15OracleUnavailable TestGenerationDiagnosticReasonV15 = "oracle-unavailable"
	TestGenerationDiagnosticReasonV15ValidationFailed  TestGenerationDiagnosticReasonV15 = "validation-failed"
	UncoveredBranch                                    TestGenerationDiagnosticReasonV15 = "uncovered-branch"
	UncoveredFunction                                  TestGenerationDiagnosticReasonV15 = "uncovered-function"
	UncoveredLine                                      TestGenerationDiagnosticReasonV15 = "uncovered-line"
	UnsupportedTarget                                  TestGenerationDiagnosticReasonV15 = "unsupported-target"
)

type Severity string

const (
	Error   Severity = "error"
	Info    Severity = "info"
	Warning Severity = "warning"
)

type TestGenerationCandidateKindV15 string

const (
	Characterization TestGenerationCandidateKindV15 = "characterization"
	Verified         TestGenerationCandidateKindV15 = "verified"
)

type Operation string

const (
	Create Operation = "create"
	Modify Operation = "modify"
)

type TestGenerationStateV15 string

const (
	Accepted             TestGenerationStateV15 = "accepted"
	Analyzing            TestGenerationStateV15 = "analyzing"
	AwaitingConfirmation TestGenerationStateV15 = "awaiting_confirmation"
	Baseline             TestGenerationStateV15 = "baseline"
	Cancelled            TestGenerationStateV15 = "cancelled"
	Failed               TestGenerationStateV15 = "failed"
	Minimizing           TestGenerationStateV15 = "minimizing"
	Queued               TestGenerationStateV15 = "queued"
	Rejected             TestGenerationStateV15 = "rejected"
	Rendering            TestGenerationStateV15 = "rendering"
	Solving              TestGenerationStateV15 = "solving"
	Validating           TestGenerationStateV15 = "validating"
)

type TestGenerationFrameworkV15 string

const (
	Auto                               TestGenerationFrameworkV15 = "auto"
	TestGenerationFrameworkV15Cpputest TestGenerationFrameworkV15 = "cpputest"
	TestGenerationFrameworkV15Unity    TestGenerationFrameworkV15 = "unity"
)

type TestGenerationScopeV15 string

const (
	File                              TestGenerationScopeV15 = "file"
	Symbol                            TestGenerationScopeV15 = "symbol"
	Target                            TestGenerationScopeV15 = "target"
	TestGenerationScopeV15CoverageGap TestGenerationScopeV15 = "coverage-gap"
	Workspace                         TestGenerationScopeV15 = "workspace"
)

type Framework string

const (
	FrameworkCpputest Framework = "cpputest"
	FrameworkUnity    Framework = "unity"
)

type TestGenerationTargetKindV15 string

const (
	BuildTarget TestGenerationTargetKindV15 = "build-target"
)
