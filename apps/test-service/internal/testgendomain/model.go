package testgendomain

import "time"

type State string

const (
	StateQueued               State = "queued"
	StateBaseline             State = "baseline"
	StateAnalyzing            State = "analyzing"
	StateSolving              State = "solving"
	StateRendering            State = "rendering"
	StateValidating           State = "validating"
	StateMinimizing           State = "minimizing"
	StateAwaitingConfirmation State = "awaiting_confirmation"
	StateAccepted             State = "accepted"
	StateRejected             State = "rejected"
	StateCancelled            State = "cancelled"
	StateFailed               State = "failed"
)

type CandidateKind string

const (
	KindVerified         CandidateKind = "verified"
	KindCharacterization CandidateKind = "characterization"
)

type AssertionKind string

const (
	AssertionIndependentOracle AssertionKind = "independent-oracle"
	AssertionObservedOutput    AssertionKind = "observed-output"
)

type EditOperation string

const (
	EditCreate EditOperation = "create"
	EditModify EditOperation = "modify"
)

type DiagnosticCode string

const (
	DiagnosticCoverageGap       DiagnosticCode = "COVERAGE_GAP"
	DiagnosticTargetUnsupported DiagnosticCode = "TARGET_UNSUPPORTED"
	DiagnosticOracleUnavailable DiagnosticCode = "ORACLE_UNAVAILABLE"
	DiagnosticBudgetExceeded    DiagnosticCode = "BUDGET_EXCEEDED"
	DiagnosticValidationFailed  DiagnosticCode = "VALIDATION_FAILED"
	DiagnosticNoCandidate       DiagnosticCode = "NO_CANDIDATE"
)

type Severity string

const (
	SeverityInfo    Severity = "info"
	SeverityWarning Severity = "warning"
	SeverityError   Severity = "error"
)

type Assertion struct {
	Kind           AssertionKind `json:"kind"`
	EvidenceDigest string        `json:"evidenceDigest"`
}
type ArtifactRef struct {
	ID     string `json:"id"`
	Digest string `json:"digest"`
}
type Coverage struct {
	Functions int64 `json:"functions"`
	Lines     int64 `json:"lines"`
	Branches  int64 `json:"branches"`
}
type Diagnostic struct {
	Code     DiagnosticCode `json:"code"`
	Severity Severity       `json:"severity"`
}
type PlannedEdit struct {
	Path         string        `json:"path"`
	Operation    EditOperation `json:"operation"`
	BeforeDigest string        `json:"beforeDigest,omitempty"`
	AfterDigest  string        `json:"afterDigest"`
}
type Candidate struct {
	CaseID               string        `json:"caseId"`
	Kind                 CandidateKind `json:"kind"`
	TargetSymbol         string        `json:"targetSymbol"`
	Assertions           []Assertion   `json:"assertions"`
	StagedSourceArtifact ArtifactRef   `json:"stagedSourceArtifact"`
	CodeDigest           string        `json:"codeDigest"`
	CoverageDelta        Coverage      `json:"coverageDelta"`
	Diagnostics          []Diagnostic  `json:"diagnostics"`
	PlannedEdits         []PlannedEdit `json:"plannedEdits"`
}

type Run struct {
	ID              string           `json:"runId"`
	TaskID          string           `json:"taskId"`
	Request         Request          `json:"request"`
	State           State            `json:"state"`
	Revision        int64            `json:"revision"`
	CreatedAt       time.Time        `json:"createdAt"`
	FinishedAt      *time.Time       `json:"finishedAt,omitempty"`
	LastSequence    int64            `json:"lastSequence"`
	CandidateCount  int              `json:"candidateCount"`
	ArtifactDigests []ArtifactRef    `json:"artifactDigests"`
	Record          GenerationRecord `json:"record"`
}

func IsTerminal(v State) bool {
	switch v {
	case StateAccepted, StateRejected, StateCancelled, StateFailed:
		return true
	}
	return false
}
func ValidTransition(from, to State) bool {
	if to == StateCancelled || to == StateFailed {
		return !IsTerminal(from) && validState(from)
	}
	switch from {
	case StateQueued:
		return to == StateBaseline
	case StateBaseline:
		return to == StateAnalyzing
	case StateAnalyzing:
		return to == StateSolving
	case StateSolving:
		return to == StateRendering
	case StateRendering:
		return to == StateValidating
	case StateValidating:
		return to == StateMinimizing || to == StateRejected
	case StateMinimizing:
		return to == StateAwaitingConfirmation || to == StateRejected
	case StateAwaitingConfirmation:
		return to == StateAccepted || to == StateRejected
	}
	return false
}
func validState(v State) bool {
	switch v {
	case StateQueued, StateBaseline, StateAnalyzing, StateSolving, StateRendering, StateValidating, StateMinimizing, StateAwaitingConfirmation, StateAccepted, StateRejected, StateCancelled, StateFailed:
		return true
	}
	return false
}

func ValidateRun(r Run) error {
	if !validID(r.ID) || !validID(r.TaskID) || r.ID == r.TaskID || ValidateRequest(r.Request) != nil || !validState(r.State) || r.Revision < 1 || r.Revision > 10000 || r.CreatedAt.IsZero() || r.LastSequence < 0 || r.CandidateCount < 0 || int64(r.CandidateCount) > r.Request.Budgets.CandidateCount || len(r.ArtifactDigests) > 1000 {
		return ErrInvalid
	}
	if IsTerminal(r.State) != (r.FinishedAt != nil) {
		return ErrInvalid
	}
	if r.FinishedAt != nil && (r.FinishedAt.IsZero() || r.FinishedAt.Before(r.CreatedAt)) {
		return ErrInvalid
	}
	seen := map[string]bool{}
	for _, a := range r.ArtifactDigests {
		if !validID(a.ID) || !validDigest(a.Digest) || seen[a.ID] {
			return ErrInvalid
		}
		seen[a.ID] = true
	}
	if !r.Record.IsZero() && !r.Record.ValidFor(r.Request, r.CandidateCount) {
		return ErrInvalid
	}
	return nil
}
func ValidateCandidate(c Candidate) error {
	if !validID(c.CaseID) || !validSymbol(c.TargetSymbol) || !validID(c.StagedSourceArtifact.ID) || !validDigest(c.StagedSourceArtifact.Digest) || !validDigest(c.CodeDigest) || len(c.Assertions) < 1 || len(c.Assertions) > 128 || len(c.PlannedEdits) < 1 || len(c.PlannedEdits) > 128 || len(c.Diagnostics) > 1000 || c.CoverageDelta.Functions < 0 || c.CoverageDelta.Lines < 0 || c.CoverageDelta.Branches < 0 {
		return ErrInvalid
	}
	switch c.Kind {
	case KindVerified, KindCharacterization:
	default:
		return ErrInvalid
	}
	for _, a := range c.Assertions {
		if !validDigest(a.EvidenceDigest) {
			return ErrInvalid
		}
		if c.Kind == KindVerified && a.Kind != AssertionIndependentOracle || c.Kind == KindCharacterization && a.Kind != AssertionObservedOutput {
			return ErrInvalid
		}
	}
	seen := map[string]bool{}
	for _, e := range c.PlannedEdits {
		if !validRelativePath(e.Path) || !validDigest(e.AfterDigest) || seen[e.Path] {
			return ErrInvalid
		}
		seen[e.Path] = true
		if e.Operation == EditCreate && e.BeforeDigest != "" || e.Operation == EditModify && !validDigest(e.BeforeDigest) || e.Operation != EditCreate && e.Operation != EditModify {
			return ErrInvalid
		}
	}
	for _, d := range c.Diagnostics {
		switch d.Code {
		case DiagnosticCoverageGap, DiagnosticTargetUnsupported, DiagnosticOracleUnavailable, DiagnosticBudgetExceeded, DiagnosticValidationFailed, DiagnosticNoCandidate:
		default:
			return ErrInvalid
		}
		switch d.Severity {
		case SeverityInfo, SeverityWarning, SeverityError:
		default:
			return ErrInvalid
		}
	}
	return nil
}
func ValidateCandidates(cs []Candidate) error {
	if len(cs) > 1000 {
		return ErrInvalid
	}
	seen := map[string]bool{}
	for _, c := range cs {
		if ValidateCandidate(c) != nil || seen[c.CaseID] {
			return ErrInvalid
		}
		seen[c.CaseID] = true
	}
	return nil
}
func CloneCandidate(c Candidate) Candidate {
	c.Assertions = append([]Assertion(nil), c.Assertions...)
	c.Diagnostics = append([]Diagnostic(nil), c.Diagnostics...)
	c.PlannedEdits = append([]PlannedEdit(nil), c.PlannedEdits...)
	return c
}
func CloneRun(r Run) Run {
	r.ArtifactDigests = append([]ArtifactRef(nil), r.ArtifactDigests...)
	r.Record.MinimizedCaseIDs = append([]string(nil), r.Record.MinimizedCaseIDs...)
	if r.FinishedAt != nil {
		t := *r.FinishedAt
		r.FinishedAt = &t
	}
	return r
}
