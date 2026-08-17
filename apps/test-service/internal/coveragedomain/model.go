package coveragedomain

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"unit-test-ide.local/test-service/internal/testdomain"
)

var (
	ErrInvalidRun          = errors.New("invalid coverage run")
	ErrInvalidReport       = errors.New("invalid coverage report")
	ErrInvalidToolchain    = errors.New("invalid coverage toolchain")
	ErrInvalidCompleteness = errors.New("invalid coverage completeness")
)

type Status string

const (
	StatusQueued   Status = "queued"
	StatusRunning  Status = "running"
	StatusFinished Status = "finished"
)

type Outcome string

const (
	OutcomeAvailable   Outcome = "available"
	OutcomePartial     Outcome = "partial"
	OutcomeUnavailable Outcome = "unavailable"
	OutcomeCancelled   Outcome = "cancelled"
)

type Reason string

const (
	ReasonUserCancelled           Reason = "user_cancelled"
	ReasonTaskTimedOut            Reason = "task_timed_out"
	ReasonInstrumentationFailed   Reason = "instrumentation_failed"
	ReasonBuildFailed             Reason = "build_failed"
	ReasonProfileCollectionFailed Reason = "profile_collection_failed"
	ReasonMergeFailed             Reason = "merge_failed"
	ReasonNormalizationFailed     Reason = "normalization_failed"
	ReasonReportGenerationFailed  Reason = "report_generation_failed"
	ReasonPersistenceFailed       Reason = "persistence_failed"
	ReasonServiceRestarted        Reason = "service_restarted"
)

type CompletenessReason string

const (
	CompletenessReasonTestCrashed                       CompletenessReason = "test_crashed"
	CompletenessReasonTestTimedOut                      CompletenessReason = "test_timed_out"
	CompletenessReasonProfileMissingForFailedInvocation CompletenessReason = "profile_missing_for_failed_invocation"
)

type Platform string

const (
	PlatformWindows Platform = "windows"
	PlatformLinux   Platform = "linux"
)

type Architecture string

const (
	ArchitectureX86   Architecture = "x86"
	ArchitectureX64   Architecture = "x64"
	ArchitectureARM64 Architecture = "arm64"
)

type CompilerFamily string

const (
	CompilerFamilyGCC     CompilerFamily = "gcc"
	CompilerFamilyClang   CompilerFamily = "clang"
	CompilerFamilyClangCL CompilerFamily = "clang-cl"
)

type DriverName string

const (
	DriverGCov    DriverName = "gcov"
	DriverLLVMCov DriverName = "llvm-cov"
)

type CollectorName string

const (
	CollectorGCovr   CollectorName = "gcovr"
	CollectorLLVMCov CollectorName = "llvm-cov"
)

const SchemaVersion10 = "1.0"

type CompilerSnapshot struct {
	Family  CompilerFamily
	Version string
}

type DriverSnapshot struct {
	Name    DriverName
	Version string
}

type CollectorSnapshot struct {
	Name    CollectorName
	Version string
}

type ToolchainSnapshot struct {
	Platform                   Platform
	Architecture               Architecture
	Compiler                   CompilerSnapshot
	Driver                     DriverSnapshot
	Collector                  CollectorSnapshot
	NormalizerVersion          string
	InstrumentationFingerprint string
}

type ArtifactRefs struct {
	CoverageJSONID string
	JUnitXMLID     string
	CoverageHTMLID string
}

type Completeness struct {
	Outcome Outcome
	Reasons []CompletenessReason
}

type Run struct {
	ID                string
	TaskID            string
	TestRunID         string
	Status            Status
	Outcome           Outcome
	Reason            Reason
	Request           Request
	SelectionSnapshot testdomain.SelectionSnapshot
	Toolchain         ToolchainSnapshot
	Summary           *Summary
	ReportID          string
	Artifacts         ArtifactRefs
	CreatedAt         time.Time
	StartedAt         *time.Time
	FinishedAt        *time.Time
	LastSequence      int64
}

type Report struct {
	ID            string
	RunID         string
	TestRunID     string
	SchemaVersion string
	CreatedAt     time.Time
	Completeness  Completeness
	Summary       Summary
	Toolchain     ToolchainSnapshot
	ArtifactID    string
}

type RunPageRequest struct {
	WorkspaceGeneration string
	ProjectID           string
	CoverageProfileID   string
	Cursor              string
	Limit               int
}

type RunPage struct {
	Items      []Run
	NextCursor string
}

const (
	DefaultRunPageSize = 100
	MaxRunPageSize     = 200
)

func NewRun(value Run) (Run, error) {
	request, err := NewRequest(value.Request)
	if err != nil {
		return Run{}, fmt.Errorf("%w: request: %w", ErrInvalidRun, err)
	}
	runID, err := CoverageRunID(request)
	if err != nil {
		return Run{}, fmt.Errorf("%w: identity: %w", ErrInvalidRun, err)
	}
	if value.ID != runID || !validHex(value.TaskID, 32) || !validHex(value.TestRunID, 32) {
		return Run{}, fmt.Errorf("%w: identity metadata", ErrInvalidRun)
	}
	snapshot, err := testdomain.NewSelectionSnapshot(value.SelectionSnapshot)
	if err != nil {
		return Run{}, fmt.Errorf("%w: selection snapshot: %w", ErrInvalidRun, err)
	}
	if err := validateToolchain(value.Toolchain); err != nil {
		return Run{}, fmt.Errorf("%w: %w", ErrInvalidRun, err)
	}
	if value.CreatedAt.IsZero() {
		return Run{}, fmt.Errorf("%w: created time", ErrInvalidRun)
	}
	if value.LastSequence < 0 || value.LastSequence > MaxSafeInteger {
		return Run{}, fmt.Errorf("%w: last sequence", ErrInvalidRun)
	}

	result := value.Clone()
	result.Request = request
	result.SelectionSnapshot = snapshot
	result.CreatedAt = value.CreatedAt.UTC()
	result.StartedAt = utcTimePointer(value.StartedAt)
	result.FinishedAt = utcTimePointer(value.FinishedAt)
	if err := validateRunLifecycle(&result); err != nil {
		return Run{}, err
	}
	return result, nil
}

func (value Run) Clone() Run {
	result := value
	result.Request = value.Request.Clone()
	result.SelectionSnapshot = value.SelectionSnapshot.Clone()
	result.StartedAt = cloneTimePointer(value.StartedAt)
	result.FinishedAt = cloneTimePointer(value.FinishedAt)
	if value.Summary != nil {
		summary := *value.Summary
		result.Summary = &summary
	}
	return result
}

func NewReport(value Report) (Report, error) {
	if !validHex(value.ID, 32) || !validHex(value.RunID, 32) || !validHex(value.TestRunID, 32) {
		return Report{}, fmt.Errorf("%w: identity metadata", ErrInvalidReport)
	}
	if value.SchemaVersion != SchemaVersion10 {
		return Report{}, fmt.Errorf("%w: schema version", ErrInvalidReport)
	}
	if value.CreatedAt.IsZero() {
		return Report{}, fmt.Errorf("%w: created time", ErrInvalidReport)
	}
	completeness, err := newCompleteness(value.Completeness)
	if err != nil {
		return Report{}, fmt.Errorf("%w: %w", ErrInvalidReport, err)
	}
	summary, err := NewSummary(value.Summary)
	if err != nil {
		return Report{}, fmt.Errorf("%w: %w", ErrInvalidReport, err)
	}
	if err := validateToolchain(value.Toolchain); err != nil {
		return Report{}, fmt.Errorf("%w: %w", ErrInvalidReport, err)
	}
	if !validHex(value.ArtifactID, 32) {
		return Report{}, fmt.Errorf("%w: coverage-json artifact ID", ErrInvalidReport)
	}
	result := value.Clone()
	result.CreatedAt = value.CreatedAt.UTC()
	result.Completeness = completeness
	result.Summary = summary
	return result, nil
}

func (value Report) Clone() Report {
	result := value
	result.Completeness.Reasons = append([]CompletenessReason(nil), value.Completeness.Reasons...)
	return result
}

func validateRunLifecycle(value *Run) error {
	if value.StartedAt != nil && value.StartedAt.Before(value.CreatedAt) ||
		value.FinishedAt != nil && value.FinishedAt.Before(value.CreatedAt) ||
		value.StartedAt != nil && value.FinishedAt != nil && value.FinishedAt.Before(*value.StartedAt) {
		return fmt.Errorf("%w: lifecycle time is not monotonic", ErrInvalidRun)
	}
	switch value.Status {
	case StatusQueued:
		if value.StartedAt != nil || value.FinishedAt != nil || value.Outcome != "" || value.Reason != "" || !emptyReportMetadata(*value) {
			return fmt.Errorf("%w: queued lifecycle", ErrInvalidRun)
		}
	case StatusRunning:
		if value.StartedAt == nil || value.FinishedAt != nil || value.Outcome != "" || value.Reason != "" || !emptyReportMetadata(*value) {
			return fmt.Errorf("%w: running lifecycle", ErrInvalidRun)
		}
	case StatusFinished:
		if value.FinishedAt == nil || !validOutcome(value.Outcome) {
			return fmt.Errorf("%w: finished lifecycle", ErrInvalidRun)
		}
		if err := validateFinishedOwnership(value); err != nil {
			return err
		}
	default:
		return fmt.Errorf("%w: status", ErrInvalidRun)
	}
	return nil
}

func validateFinishedOwnership(value *Run) error {
	switch value.Outcome {
	case OutcomeAvailable, OutcomePartial:
		if value.Reason != "" || value.Summary == nil || !validHex(value.ReportID, 32) || !validArtifactRefs(value.Artifacts) {
			return fmt.Errorf("%w: report-bearing outcome metadata", ErrInvalidRun)
		}
		summary, err := NewSummary(*value.Summary)
		if err != nil {
			return fmt.Errorf("%w: %w", ErrInvalidRun, err)
		}
		value.Summary = &summary
	case OutcomeUnavailable:
		if !unavailableReason(value.Reason) || !emptyReportMetadata(*value) {
			return fmt.Errorf("%w: unavailable outcome metadata", ErrInvalidRun)
		}
	case OutcomeCancelled:
		if !cancelledReason(value.Reason) || !emptyReportMetadata(*value) {
			return fmt.Errorf("%w: cancelled outcome metadata", ErrInvalidRun)
		}
	}
	return nil
}

func validateToolchain(value ToolchainSnapshot) error {
	if !validArchitecture(value.Architecture) ||
		!validVersion(value.Compiler.Version) || !validVersion(value.Driver.Version) ||
		!validVersion(value.Collector.Version) || !validVersion(value.NormalizerVersion) ||
		!validHex(value.InstrumentationFingerprint, 64) {
		return ErrInvalidToolchain
	}
	approved := value.Platform == PlatformWindows && value.Compiler.Family == CompilerFamilyClangCL &&
		value.Driver.Name == DriverLLVMCov && value.Collector.Name == CollectorLLVMCov ||
		value.Platform == PlatformLinux && value.Compiler.Family == CompilerFamilyGCC &&
			value.Driver.Name == DriverGCov && value.Collector.Name == CollectorGCovr ||
		value.Platform == PlatformLinux && value.Compiler.Family == CompilerFamilyClang &&
			value.Driver.Name == DriverLLVMCov && value.Collector.Name == CollectorLLVMCov
	if !approved {
		return ErrInvalidToolchain
	}
	return nil
}

func newCompleteness(value Completeness) (Completeness, error) {
	result := Completeness{Outcome: value.Outcome, Reasons: append([]CompletenessReason(nil), value.Reasons...)}
	switch result.Outcome {
	case OutcomeAvailable:
		if len(result.Reasons) != 0 {
			return Completeness{}, ErrInvalidCompleteness
		}
	case OutcomePartial:
		if len(result.Reasons) == 0 || len(result.Reasons) > 64 {
			return Completeness{}, ErrInvalidCompleteness
		}
		seen := make(map[CompletenessReason]struct{}, len(result.Reasons))
		for _, reason := range result.Reasons {
			if !validCompletenessReason(reason) {
				return Completeness{}, ErrInvalidCompleteness
			}
			if _, exists := seen[reason]; exists {
				return Completeness{}, ErrInvalidCompleteness
			}
			seen[reason] = struct{}{}
		}
		sort.Slice(result.Reasons, func(i, j int) bool { return result.Reasons[i] < result.Reasons[j] })
	default:
		return Completeness{}, ErrInvalidCompleteness
	}
	return result, nil
}

func validArtifactRefs(value ArtifactRefs) bool {
	return validHex(value.CoverageJSONID, 32) && validHex(value.JUnitXMLID, 32) && validHex(value.CoverageHTMLID, 32) &&
		value.CoverageJSONID != value.JUnitXMLID && value.CoverageJSONID != value.CoverageHTMLID && value.JUnitXMLID != value.CoverageHTMLID
}

func emptyReportMetadata(value Run) bool {
	return value.Summary == nil && value.ReportID == "" && value.Artifacts == (ArtifactRefs{})
}

func validOutcome(value Outcome) bool {
	switch value {
	case OutcomeAvailable, OutcomePartial, OutcomeUnavailable, OutcomeCancelled:
		return true
	default:
		return false
	}
}

func unavailableReason(value Reason) bool {
	switch value {
	case ReasonInstrumentationFailed, ReasonBuildFailed, ReasonProfileCollectionFailed, ReasonMergeFailed,
		ReasonNormalizationFailed, ReasonReportGenerationFailed, ReasonPersistenceFailed, ReasonServiceRestarted:
		return true
	default:
		return false
	}
}

func cancelledReason(value Reason) bool {
	return value == ReasonUserCancelled || value == ReasonTaskTimedOut
}

func validCompletenessReason(value CompletenessReason) bool {
	switch value {
	case CompletenessReasonTestCrashed, CompletenessReasonTestTimedOut, CompletenessReasonProfileMissingForFailedInvocation:
		return true
	default:
		return false
	}
}

func validArchitecture(value Architecture) bool {
	return value == ArchitectureX86 || value == ArchitectureX64 || value == ArchitectureARM64
}

func validVersion(value string) bool {
	return len(value) >= 1 && len(value) <= 128 && utf8.ValidString(value) && !strings.ContainsRune(value, '\x00')
}

func cloneTimePointer(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	result := *value
	return &result
}

func utcTimePointer(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	result := value.UTC()
	return &result
}
