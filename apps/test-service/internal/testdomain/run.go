package testdomain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"time"
)

type RunStatus string

const (
	RunQueued    RunStatus = "queued"
	RunRunning   RunStatus = "running"
	RunCompleted RunStatus = "completed"
)

type RunOutcome string

const (
	RunPassed      RunOutcome = "passed"
	RunFailed      RunOutcome = "failed"
	RunBlocked     RunOutcome = "blocked"
	RunErrored     RunOutcome = "errored"
	RunCancelled   RunOutcome = "cancelled"
	RunTimedOut    RunOutcome = "timed_out"
	RunInterrupted RunOutcome = "interrupted"
)

type ItemOutcome string

const (
	ItemPassed    ItemOutcome = "passed"
	ItemFailed    ItemOutcome = "failed"
	ItemSkipped   ItemOutcome = "skipped"
	ItemErrored   ItemOutcome = "errored"
	ItemCancelled ItemOutcome = "cancelled"
	ItemTimedOut  ItemOutcome = "timed_out"
	ItemNotRun    ItemOutcome = "not_run"
)

type ResultReason string

const (
	ReasonBuildBlocked        ResultReason = "build_blocked"
	ReasonContainerTerminated ResultReason = "container_terminated"
	ReasonSelectionAborted    ResultReason = "selection_aborted"
	ReasonServiceRestarted    ResultReason = "service_restarted"
	ReasonStaleCatalog        ResultReason = "stale_catalog"
	ReasonDisabled            ResultReason = "disabled"
)

type RunSummary struct {
	Total      int64 `json:"total"`
	Completed  int64 `json:"completed"`
	Passed     int64 `json:"passed"`
	Failed     int64 `json:"failed"`
	Skipped    int64 `json:"skipped"`
	Errored    int64 `json:"errored"`
	Cancelled  int64 `json:"cancelled"`
	TimedOut   int64 `json:"timedOut"`
	NotRun     int64 `json:"notRun"`
	Iterations int64 `json:"iterations"`
}

type TestRun struct {
	RunID             string            `json:"runId"`
	TaskID            string            `json:"taskId"`
	ProjectID         string            `json:"projectId"`
	ProfileID         string            `json:"profileId"`
	ToolchainID       string            `json:"toolchainId"`
	CatalogRevision   string            `json:"catalogRevision"`
	SelectionSnapshot SelectionSnapshot `json:"selectionSnapshot"`
	Status            RunStatus         `json:"status"`
	Outcome           RunOutcome        `json:"outcome,omitempty"`
	StartedAt         *time.Time        `json:"startedAt,omitempty"`
	FinishedAt        *time.Time        `json:"finishedAt,omitempty"`
	Summary           RunSummary        `json:"summary"`
	ResultRevision    string            `json:"resultRevision"`
	Incomplete        bool              `json:"incomplete"`

	IdempotencyKey string           `json:"-"`
	CreatedAt      time.Time        `json:"-"`
	Results        []TestItemResult `json:"-"`
}

type TestItemResult struct {
	ItemID         ID              `json:"itemId"`
	ContainerID    ID              `json:"containerId"`
	Iteration      int64           `json:"iteration"`
	Outcome        ItemOutcome     `json:"outcome"`
	DurationMS     *int64          `json:"durationMs,omitempty"`
	SourceLocation *SourceLocation `json:"sourceLocation,omitempty"`
	FailureDetails []FailureDetail `json:"failureDetails"`
	OutputRefs     []string        `json:"outputRefs"`
	Partial        bool            `json:"partial"`
	Reason         ResultReason    `json:"reason,omitempty"`
}

type RunPageRequest struct {
	ProjectID string
	ProfileID string
	Cursor    string
	Limit     int
}

type RunPage struct {
	Items      []TestRun
	NextCursor string
}

const (
	DefaultRunPageSize = 100
	MaxRunPageSize     = 1000
	maxSafeCount       = int64(9_007_199_254_740_991)
)

func NewTestRun(value TestRun) (TestRun, error) {
	resultsProvided := value.Results != nil
	result := cloneTestRun(value)
	if !validHex(result.RunID, 32) || !validHex(result.TaskID, 32) ||
		!validHex(result.IdempotencyKey, 32) ||
		!validProjectID(result.ProjectID) ||
		!validProjectID(result.ToolchainID) ||
		!validHex(result.ProfileID, 64) ||
		!validHex(result.CatalogRevision, 64) ||
		result.CreatedAt.IsZero() {
		return TestRun{}, invalid(ErrInvalidResult, "run", "contains invalid identity metadata")
	}
	snapshot, err := newSelectionSnapshot(result.SelectionSnapshot)
	if err != nil {
		return TestRun{}, err
	}
	result.SelectionSnapshot = snapshot
	if result.ResultRevision == "" {
		result.ResultRevision = EmptyResultRevision()
	}
	if !validHex(result.ResultRevision, 64) ||
		!validRunSummary(result.Summary) {
		return TestRun{}, invalid(ErrInvalidResult, "run", "contains invalid summary metadata")
	}
	switch result.Status {
	case RunQueued:
		if result.Outcome != "" || result.StartedAt != nil ||
			result.FinishedAt != nil {
			return TestRun{}, invalid(ErrInvalidResult, "run.status", "queued lifecycle is inconsistent")
		}
	case RunRunning:
		if result.Outcome != "" || result.StartedAt == nil ||
			result.FinishedAt != nil {
			return TestRun{}, invalid(ErrInvalidResult, "run.status", "running lifecycle is inconsistent")
		}
	case RunCompleted:
		if !result.Outcome.valid() || result.FinishedAt == nil {
			return TestRun{}, invalid(ErrInvalidResult, "run.status", "completed lifecycle is inconsistent")
		}
	default:
		return TestRun{}, invalid(ErrInvalidResult, "run.status", "unsupported value")
	}
	if result.StartedAt != nil &&
		(result.StartedAt.Before(result.CreatedAt) ||
			result.FinishedAt != nil && result.FinishedAt.Before(*result.StartedAt)) {
		return TestRun{}, invalid(ErrInvalidResult, "run.time", "lifecycle time is not monotonic")
	}
	result.Results = make([]TestItemResult, len(value.Results))
	for index, candidate := range value.Results {
		item, err := NewTestItemResult(candidate)
		if err != nil {
			return TestRun{}, err
		}
		result.Results[index] = item
	}
	sortResults(result.Results)
	if resultsProvided {
		if revision, err := ResultRevision(result.Results); err != nil ||
			revision != result.ResultRevision {
			return TestRun{}, invalid(ErrInvalidResult, "run.resultRevision", "does not match results")
		}
	}
	return result, nil
}

func NewTestItemResult(value TestItemResult) (TestItemResult, error) {
	result := cloneTestItemResult(value)
	if !ValidID(result.ItemID) || !ValidID(result.ContainerID) ||
		result.Iteration < 1 || result.Iteration > 100 ||
		!result.Outcome.valid() {
		return TestItemResult{}, invalid(ErrInvalidResult, "result", "contains invalid identity or outcome")
	}
	if result.DurationMS != nil &&
		(*result.DurationMS < 0 || *result.DurationMS > maxSafeCount) {
		return TestItemResult{}, invalid(ErrInvalidResult, "result.durationMs", "outside safe range")
	}
	location, err := cloneAndValidateLocation(result.SourceLocation)
	if err != nil {
		return TestItemResult{}, invalid(ErrInvalidResult, "result.sourceLocation", err.Error())
	}
	result.SourceLocation = location
	if len(result.FailureDetails) > 256 {
		return TestItemResult{}, invalid(ErrInvalidResult, "result.failureDetails", "exceeds limit")
	}
	for index, detail := range result.FailureDetails {
		validated, err := NewFailureDetail(detail)
		if err != nil {
			return TestItemResult{}, err
		}
		result.FailureDetails[index] = validated
	}
	if len(result.OutputRefs) > 64 {
		return TestItemResult{}, invalid(ErrInvalidResult, "result.outputRefs", "exceeds limit")
	}
	seen := make(map[string]struct{}, len(result.OutputRefs))
	for _, reference := range result.OutputRefs {
		if !validHex(reference, 32) {
			return TestItemResult{}, invalid(ErrInvalidResult, "result.outputRefs", "contains invalid artifact ID")
		}
		if _, duplicate := seen[reference]; duplicate {
			return TestItemResult{}, invalid(ErrDuplicateIdentity, "result.outputRefs", "contains duplicate")
		}
		seen[reference] = struct{}{}
	}
	if result.Outcome == ItemNotRun {
		if !result.Reason.valid() {
			return TestItemResult{}, invalid(ErrInvalidResult, "result.reason", "not_run requires a reason")
		}
	} else if result.Reason != "" {
		return TestItemResult{}, invalid(ErrInvalidResult, "result.reason", "only not_run may have a reason")
	}
	return result, nil
}

func ResultRevision(results []TestItemResult) (string, error) {
	canonical := make([]TestItemResult, len(results))
	for index, candidate := range results {
		validated, err := NewTestItemResult(candidate)
		if err != nil {
			return "", err
		}
		canonical[index] = validated
	}
	sortResults(canonical)
	encoded, err := json.Marshal(canonical)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:]), nil
}

func EmptyResultRevision() string {
	value, _ := ResultRevision(nil)
	return value
}

func (value TestRun) Clone() TestRun {
	return cloneTestRun(value)
}

func (value TestItemResult) Clone() TestItemResult {
	return cloneTestItemResult(value)
}

func (outcome RunOutcome) valid() bool {
	switch outcome {
	case RunPassed, RunFailed, RunBlocked, RunErrored,
		RunCancelled, RunTimedOut, RunInterrupted:
		return true
	default:
		return false
	}
}

func (outcome ItemOutcome) valid() bool {
	switch outcome {
	case ItemPassed, ItemFailed, ItemSkipped, ItemErrored,
		ItemCancelled, ItemTimedOut, ItemNotRun:
		return true
	default:
		return false
	}
}

func (reason ResultReason) valid() bool {
	switch reason {
	case ReasonBuildBlocked, ReasonContainerTerminated,
		ReasonSelectionAborted, ReasonServiceRestarted,
		ReasonStaleCatalog, ReasonDisabled:
		return true
	default:
		return false
	}
}

func validRunSummary(value RunSummary) bool {
	counts := []int64{
		value.Total, value.Completed, value.Passed, value.Failed,
		value.Skipped, value.Errored, value.Cancelled, value.TimedOut,
		value.NotRun,
	}
	for _, count := range counts {
		if count < 0 || count > maxSafeCount {
			return false
		}
	}
	terminal := value.Passed + value.Failed + value.Skipped +
		value.Errored + value.Cancelled + value.TimedOut
	return value.Iterations >= 1 && value.Iterations <= 100 &&
		value.Completed == terminal &&
		value.Total == value.Completed+value.NotRun
}

func newSelectionSnapshot(value SelectionSnapshot) (SelectionSnapshot, error) {
	result := value.Clone()
	if !result.Mode.Valid() ||
		len(result.ContainerIDs) > 10_000 ||
		len(result.ItemIDs) > 100_000 ||
		!strictlySortedIDs(result.ContainerIDs) ||
		!strictlySortedIDs(result.ItemIDs) {
		return SelectionSnapshot{}, invalid(ErrInvalidSelection, "selectionSnapshot", "is not canonical")
	}
	if result.Mode == SelectionFailedFromRun {
		if !validHex(result.SourceRunID, 32) {
			return SelectionSnapshot{}, invalid(ErrInvalidSelection, "sourceRunId", "is invalid")
		}
	} else if result.SourceRunID != "" {
		return SelectionSnapshot{}, invalid(ErrInvalidSelection, "sourceRunId", "requires failedFromRun")
	}
	return result, nil
}

func strictlySortedIDs(values []ID) bool {
	for index, value := range values {
		if !ValidID(value) || index > 0 && values[index-1] >= value {
			return false
		}
	}
	return true
}

func sortResults(values []TestItemResult) {
	sort.Slice(values, func(first, second int) bool {
		if values[first].ItemID != values[second].ItemID {
			return values[first].ItemID < values[second].ItemID
		}
		return values[first].Iteration < values[second].Iteration
	})
}

func cloneTestRun(value TestRun) TestRun {
	result := value
	result.SelectionSnapshot = value.SelectionSnapshot.Clone()
	if value.StartedAt != nil {
		started := *value.StartedAt
		result.StartedAt = &started
	}
	if value.FinishedAt != nil {
		finished := *value.FinishedAt
		result.FinishedAt = &finished
	}
	if value.Results != nil {
		result.Results = make(
			[]TestItemResult,
			len(value.Results),
		)
		for index, item := range value.Results {
			result.Results[index] = cloneTestItemResult(item)
		}
	}
	return result
}

func cloneTestItemResult(value TestItemResult) TestItemResult {
	result := value
	if value.DurationMS != nil {
		duration := *value.DurationMS
		result.DurationMS = &duration
	}
	if value.SourceLocation != nil {
		location := *value.SourceLocation
		result.SourceLocation = &location
	}
	result.FailureDetails = make([]FailureDetail, len(value.FailureDetails))
	for index, detail := range value.FailureDetails {
		result.FailureDetails[index] = detail
		result.FailureDetails[index].Locations = append(
			[]SourceLocation(nil),
			detail.Locations...,
		)
		result.FailureDetails[index].EvidenceRefs = append(
			[]string(nil),
			detail.EvidenceRefs...,
		)
	}
	result.OutputRefs = append([]string{}, value.OutputRefs...)
	return result
}
