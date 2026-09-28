package protocolmodelv16testgeneration

import "time"

type TestGenerationContractV16 struct {
	AcceptRequest         TestGenerationAcceptRequestV16        `json:"acceptRequest"`
	CandidateListRequest  TestGenerationCandidateListRequestV16 `json:"candidateListRequest"`
	CandidatePage         TestGenerationCandidatePageV16        `json:"candidatePage"`
	EventPage             TestGenerationEventPageV16            `json:"eventPage"`
	EventReplayRequest    TestGenerationEventReplayRequestV16   `json:"eventReplayRequest"`
	ManagedRecordPage     ManagedTestRecordPageV16              `json:"managedRecordPage"`
	ManagedRecordsRequest ManagedRecordsRequestV16              `json:"managedRecordsRequest"`
	ManagedReview         ManagedReviewV16                      `json:"managedReview"`
	ReviewApplyRequest    ManagedReviewApplyRequestV16          `json:"reviewApplyRequest"`
	ReviewApplyResult     ManagedReviewApplyResultV16           `json:"reviewApplyResult"`
	ReviewIDRequest       ManagedReviewIDRequestV16             `json:"reviewIdRequest"`
	Run                   TestGenerationRunV16                  `json:"run"`
	RunIDRequest          TestGenerationRunIDRequestV16         `json:"runIdRequest"`
	StartRequest          TestGenerationStartRequestV16         `json:"startRequest"`
	TargetList            TestGenerationTargetListV16           `json:"targetList"`
	TargetListRequest     TestGenerationTargetListRequestV16    `json:"targetListRequest"`
}

// The service MUST resolve the authoritative stored candidate kind from runId and
// candidateId, verify confirmationDigest against the current preview, and MUST reject a
// characterization candidate unless confirmCharacterization is true. Caller-supplied
// candidate classification is forbidden.
type TestGenerationAcceptRequestV16 struct {
	CandidateID        string `json:"candidateId"`
	ConfirmationDigest string `json:"confirmationDigest"`
	// Explicit consent for a characterization candidate. The service checks this against the
	// stored kind resolved by runId and candidateId.
	ConfirmCharacterization bool   `json:"confirmCharacterization"`
	RunID                   string `json:"runId"`
}

type TestGenerationCandidateListRequestV16 struct {
	Cursor *string `json:"cursor,omitempty"`
	Limit  *int64  `json:"limit,omitempty"`
	RunID  string  `json:"runId"`
}

type TestGenerationCandidatePageV16 struct {
	Items      []TestGenerationCandidateV16 `json:"items"`
	NextCursor *string                      `json:"nextCursor,omitempty"`
}

type TestGenerationCandidateV16 struct {
	ArtifactDigest            string                               `json:"artifactDigest"`
	AssertionProvenance       TestGenerationAssertionProvenanceV16 `json:"assertionProvenance"`
	BaselineCoverage          TestGenerationCoverageV16            `json:"baselineCoverage"`
	CandidateID               string                               `json:"candidateId"`
	CharacterizationConfirmed bool                                 `json:"characterizationConfirmed"`
	CodeDigest                string                               `json:"codeDigest"`
	DeltaCoverage             TestGenerationCoverageV16            `json:"deltaCoverage"`
	Diagnostics               []TestGenerationDiagnosticV16        `json:"diagnostics"`
	Kind                      TestGenerationCandidateKindV16       `json:"kind"`
	PlannedEdits              []TestGenerationPlannedEditV16       `json:"plannedEdits"`
}

type TestGenerationAssertionProvenanceV16 struct {
	EvidenceDigest string `json:"evidenceDigest"`
	Kind           Kind   `json:"kind"`
}

type TestGenerationCoverageV16 struct {
	BranchPercent   float64 `json:"branchPercent"`
	FunctionPercent float64 `json:"functionPercent"`
	LinePercent     float64 `json:"linePercent"`
}

type TestGenerationDiagnosticV16 struct {
	Code     TestGenerationDiagnosticCodeV16    `json:"code"`
	Reason   *TestGenerationDiagnosticReasonV16 `json:"reason,omitempty"`
	Severity Severity                           `json:"severity"`
}

type TestGenerationPlannedEditV16 struct {
	AfterDigest  string    `json:"afterDigest"`
	BeforeDigest *string   `json:"beforeDigest,omitempty"`
	Operation    Operation `json:"operation"`
	Path         string    `json:"path"`
}

type TestGenerationEventPageV16 struct {
	Items             []TestGenerationProgressEventV16 `json:"items"`
	NextAfterSequence int64                            `json:"nextAfterSequence"`
}

type TestGenerationProgressEventV16 struct {
	OccurredAt time.Time              `json:"occurredAt"`
	Sequence   int64                  `json:"sequence"`
	State      TestGenerationStateV16 `json:"state"`
}

type TestGenerationEventReplayRequestV16 struct {
	AfterSequence int64  `json:"afterSequence"`
	Limit         *int64 `json:"limit,omitempty"`
	RunID         string `json:"runId"`
}

type ManagedTestRecordPageV16 struct {
	CoverageReportID    string                 `json:"coverageReportId"`
	Items               []ManagedTestRecordV16 `json:"items"`
	NextCursor          *string                `json:"nextCursor,omitempty"`
	WorkspaceGeneration string                 `json:"workspaceGeneration"`
}

type ManagedTestRecordV16 struct {
	AcceptedDigest  string               `json:"acceptedDigest"`
	CaseID          string               `json:"caseId"`
	CurrentDigest   string               `json:"currentDigest"`
	FileID          string               `json:"fileId"`
	FunctionID      string               `json:"functionId"`
	GeneratedDigest *string              `json:"generatedDigest,omitempty"`
	ReviewID        *string              `json:"reviewId,omitempty"`
	Status          ManagedTestStatusV16 `json:"status"`
}

type ManagedRecordsRequestV16 struct {
	CoverageReportID    string                `json:"coverageReportId"`
	Cursor              *string               `json:"cursor,omitempty"`
	FileID              *string               `json:"fileId,omitempty"`
	Limit               *int64                `json:"limit,omitempty"`
	ProjectID           string                `json:"projectId"`
	Status              *ManagedTestStatusV16 `json:"status,omitempty"`
	WorkspaceGeneration string                `json:"workspaceGeneration"`
}

type ManagedReviewV16 struct {
	Cases               []ManagedReviewCaseV16 `json:"cases"`
	CoverageReportID    string                 `json:"coverageReportId"`
	ReviewDigest        string                 `json:"reviewDigest"`
	ReviewID            string                 `json:"reviewId"`
	WorkspaceGeneration string                 `json:"workspaceGeneration"`
}

type ManagedReviewCaseV16 struct {
	AcceptedDigest  string               `json:"acceptedDigest"`
	CaseID          string               `json:"caseId"`
	CurrentDigest   string               `json:"currentDigest"`
	Diff            *string              `json:"diff,omitempty"`
	GeneratedDigest string               `json:"generatedDigest"`
	Status          ManagedTestStatusV16 `json:"status"`
}

type ManagedReviewApplyRequestV16 struct {
	Resolutions  []ManagedReviewResolutionV16 `json:"resolutions"`
	ReviewDigest string                       `json:"reviewDigest"`
	ReviewID     string                       `json:"reviewId"`
}

type ManagedReviewResolutionV16 struct {
	CaseID string                   `json:"caseId"`
	Choice ManagedConflictChoiceV16 `json:"choice"`
}

type ManagedReviewApplyResultV16 struct {
	Applied      bool   `json:"applied"`
	ReviewDigest string `json:"reviewDigest"`
	ReviewID     string `json:"reviewId"`
}

type ManagedReviewIDRequestV16 struct {
	ReviewID string `json:"reviewId"`
}

type TestGenerationRunV16 struct {
	CandidateCount      *int64                    `json:"candidateCount,omitempty"`
	CreatedAt           time.Time                 `json:"createdAt"`
	FinishedAt          *time.Time                `json:"finishedAt,omitempty"`
	LastSequence        int64                     `json:"lastSequence"`
	Preview             *TestGenerationPreviewV16 `json:"preview,omitempty"`
	ProjectID           string                    `json:"projectId"`
	RunID               string                    `json:"runId"`
	State               TestGenerationStateV16    `json:"state"`
	TaskID              string                    `json:"taskId"`
	WorkspaceGeneration string                    `json:"workspaceGeneration"`
}

type TestGenerationPreviewV16 struct {
	CandidateSetDigest     string  `json:"candidateSetDigest"`
	CharacterizationDigest *string `json:"characterizationDigest,omitempty"`
	ConfirmationDigest     string  `json:"confirmationDigest"`
	Diff                   *string `json:"diff,omitempty"`
	DiffDigest             string  `json:"diffDigest"`
}

type TestGenerationRunIDRequestV16 struct {
	RunID string `json:"runId"`
}

type TestGenerationStartRequestV16 struct {
	Budgets             TestGenerationBudgetsV16   `json:"budgets"`
	CoverageGapID       *string                    `json:"coverageGapId,omitempty"`
	CoverageReportID    *string                    `json:"coverageReportId,omitempty"`
	FileID              *string                    `json:"fileId,omitempty"`
	Framework           TestGenerationFrameworkV16 `json:"framework"`
	FunctionID          *string                    `json:"functionId,omitempty"`
	Goals               TestGenerationGoalsV16     `json:"goals"`
	IdempotencyKey      string                     `json:"idempotencyKey"`
	ProjectID           string                     `json:"projectId"`
	Scope               TestGenerationScopeV16     `json:"scope"`
	TargetID            *string                    `json:"targetId,omitempty"`
	WorkspaceGeneration string                     `json:"workspaceGeneration"`
}

type TestGenerationBudgetsV16 struct {
	CandidateCount int64 `json:"candidateCount"`
	Concurrency    int64 `json:"concurrency"`
	MemoryMiB      int64 `json:"memoryMiB"`
	WallTimeMS     int64 `json:"wallTimeMs"`
}

type TestGenerationGoalsV16 struct {
	BranchPercent   float64 `json:"branchPercent"`
	FunctionPercent float64 `json:"functionPercent"`
	LinePercent     float64 `json:"linePercent"`
}

type TestGenerationTargetListV16 struct {
	Items      []TestGenerationTargetV16 `json:"items"`
	NextCursor *string                   `json:"nextCursor,omitempty"`
}

type TestGenerationTargetV16 struct {
	Frameworks []Framework                 `json:"frameworks"`
	Kind       TestGenerationTargetKindV16 `json:"kind"`
	// Stable opaque target digest; no display label or source text is carried.
	TargetID string `json:"targetId"`
}

type TestGenerationTargetListRequestV16 struct {
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

type TestGenerationDiagnosticCodeV16 string

const (
	BudgetExceeded    TestGenerationDiagnosticCodeV16 = "BUDGET_EXCEEDED"
	CoverageGap       TestGenerationDiagnosticCodeV16 = "COVERAGE_GAP"
	NoCandidate       TestGenerationDiagnosticCodeV16 = "NO_CANDIDATE"
	OracleUnavailable TestGenerationDiagnosticCodeV16 = "ORACLE_UNAVAILABLE"
	TargetUnsupported TestGenerationDiagnosticCodeV16 = "TARGET_UNSUPPORTED"
	ValidationFailed  TestGenerationDiagnosticCodeV16 = "VALIDATION_FAILED"
)

type TestGenerationDiagnosticReasonV16 string

const (
	BudgetLimit                                        TestGenerationDiagnosticReasonV16 = "budget-limit"
	TestGenerationDiagnosticReasonV16OracleUnavailable TestGenerationDiagnosticReasonV16 = "oracle-unavailable"
	TestGenerationDiagnosticReasonV16ValidationFailed  TestGenerationDiagnosticReasonV16 = "validation-failed"
	UncoveredBranch                                    TestGenerationDiagnosticReasonV16 = "uncovered-branch"
	UncoveredFunction                                  TestGenerationDiagnosticReasonV16 = "uncovered-function"
	UncoveredLine                                      TestGenerationDiagnosticReasonV16 = "uncovered-line"
	UnsupportedTarget                                  TestGenerationDiagnosticReasonV16 = "unsupported-target"
)

type Severity string

const (
	Error   Severity = "error"
	Info    Severity = "info"
	Warning Severity = "warning"
)

type TestGenerationCandidateKindV16 string

const (
	Characterization TestGenerationCandidateKindV16 = "characterization"
	Verified         TestGenerationCandidateKindV16 = "verified"
)

type Operation string

const (
	Create Operation = "create"
	Modify Operation = "modify"
)

type TestGenerationStateV16 string

const (
	Accepted             TestGenerationStateV16 = "accepted"
	Analyzing            TestGenerationStateV16 = "analyzing"
	AwaitingConfirmation TestGenerationStateV16 = "awaiting_confirmation"
	Baseline             TestGenerationStateV16 = "baseline"
	Cancelled            TestGenerationStateV16 = "cancelled"
	Failed               TestGenerationStateV16 = "failed"
	Minimizing           TestGenerationStateV16 = "minimizing"
	Queued               TestGenerationStateV16 = "queued"
	Rejected             TestGenerationStateV16 = "rejected"
	Rendering            TestGenerationStateV16 = "rendering"
	Solving              TestGenerationStateV16 = "solving"
	Validating           TestGenerationStateV16 = "validating"
)

type ManagedTestStatusV16 string

const (
	Conflicted ManagedTestStatusV16 = "conflicted"
	Current    ManagedTestStatusV16 = "current"
	Invalid    ManagedTestStatusV16 = "invalid"
	Orphaned   ManagedTestStatusV16 = "orphaned"
	Stale      ManagedTestStatusV16 = "stale"
)

type ManagedConflictChoiceV16 string

const (
	ConvertToManual ManagedConflictChoiceV16 = "convert-to-manual"
	KeepCurrent     ManagedConflictChoiceV16 = "keep-current"
	UseGenerated    ManagedConflictChoiceV16 = "use-generated"
)

type TestGenerationFrameworkV16 string

const (
	Auto                               TestGenerationFrameworkV16 = "auto"
	TestGenerationFrameworkV16Cpputest TestGenerationFrameworkV16 = "cpputest"
	TestGenerationFrameworkV16Unity    TestGenerationFrameworkV16 = "unity"
)

type TestGenerationScopeV16 string

const (
	File                              TestGenerationScopeV16 = "file"
	Symbol                            TestGenerationScopeV16 = "symbol"
	Target                            TestGenerationScopeV16 = "target"
	TestGenerationScopeV16CoverageGap TestGenerationScopeV16 = "coverage-gap"
	Workspace                         TestGenerationScopeV16 = "workspace"
)

type Framework string

const (
	FrameworkCpputest Framework = "cpputest"
	FrameworkUnity    Framework = "unity"
)

type TestGenerationTargetKindV16 string

const (
	BuildTarget TestGenerationTargetKindV16 = "build-target"
)
