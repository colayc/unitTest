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

type TestGenerationAcceptRequestV15 struct {
	CandidateID             string                         `json:"candidateId"`
	CandidateKind           TestGenerationCandidateKindV15 `json:"candidateKind"`
	ConfirmCharacterization bool                           `json:"confirmCharacterization"`
	RunID                   string                         `json:"runId"`
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
	Code     string   `json:"code"`
	Message  string   `json:"message"`
	Severity Severity `json:"severity"`
}

type TestGenerationPlannedEditV15 struct {
	AfterDigest  string    `json:"afterDigest"`
	BeforeDigest *string   `json:"beforeDigest,omitempty"`
	Operation    Operation `json:"operation"`
	Path         string    `json:"path"`
}

type TestGenerationRunV15 struct {
	CandidateCount      *int64                 `json:"candidateCount,omitempty"`
	CreatedAt           time.Time              `json:"createdAt"`
	FinishedAt          *time.Time             `json:"finishedAt,omitempty"`
	LastSequence        int64                  `json:"lastSequence"`
	ProjectID           string                 `json:"projectId"`
	RunID               string                 `json:"runId"`
	State               TestGenerationStateV15 `json:"state"`
	TaskID              string                 `json:"taskId"`
	WorkspaceGeneration string                 `json:"workspaceGeneration"`
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
	DisplayName string      `json:"displayName"`
	Frameworks  []Framework `json:"frameworks"`
	TargetID    string      `json:"targetId"`
}

type TestGenerationTargetListRequestV15 struct {
	Cursor              *string `json:"cursor,omitempty"`
	Limit               *int64  `json:"limit,omitempty"`
	ProjectID           string  `json:"projectId"`
	WorkspaceGeneration string  `json:"workspaceGeneration"`
}

type TestGenerationCandidateKindV15 string

const (
	Characterization TestGenerationCandidateKindV15 = "characterization"
	Verified         TestGenerationCandidateKindV15 = "verified"
)

type Kind string

const (
	IndependentOracle Kind = "independent-oracle"
	ObservedOutput    Kind = "observed-output"
)

type Severity string

const (
	Error   Severity = "error"
	Info    Severity = "info"
	Warning Severity = "warning"
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
	CoverageGap TestGenerationScopeV15 = "coverage-gap"
	File        TestGenerationScopeV15 = "file"
	Symbol      TestGenerationScopeV15 = "symbol"
	Target      TestGenerationScopeV15 = "target"
	Workspace   TestGenerationScopeV15 = "workspace"
)

type Framework string

const (
	FrameworkCpputest Framework = "cpputest"
	FrameworkUnity    Framework = "unity"
)
