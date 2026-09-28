package testgendomain

import (
	"errors"
	"math"
	"strings"

	"unit-test-ide.local/test-service/internal/coveragedetail"
)

var ErrInvalid = errors.New("invalid test generation value")
var ErrStaleSnapshot = errors.New("stale test generation snapshot")

type Scope string

// ManagedSelector carries service-issued IDs only. The extra fields are
// deliberately rejected so internal callers cannot accidentally bind a
// display label, path, or coordinate supplied by a client.
type ManagedSelector struct {
	ProjectID, WorkspaceGeneration, CoverageReportID string
	Scope                                            Scope
	ID                                               string
	SymbolText, FilePath                             string
	GapLine, GapColumn, GapOrdinal                   int64
}

type ManagedTarget struct {
	FileID, FunctionID, GapID        string
	File, FunctionName, SourceDigest string
}

// ResolveManagedTarget requires an already source-attested current index from
// the trusted coverage provider. IDs alone do not authorize stale reads.
func ResolveManagedTarget(selector ManagedSelector, index coveragedetail.Index) (ManagedTarget, error) {
	if !validProjectID(selector.ProjectID) || !validDigest(selector.WorkspaceGeneration) ||
		!validID(selector.CoverageReportID) || !validID(selector.ID) ||
		selector.SymbolText != "" || selector.FilePath != "" || selector.GapLine != 0 || selector.GapColumn != 0 || selector.GapOrdinal != 0 ||
		selector.ProjectID != index.ProjectID || selector.WorkspaceGeneration != index.WorkspaceGeneration || selector.CoverageReportID != index.ReportID || index.Project.Status != coveragedetail.StatusCurrent {
		return ManagedTarget{}, ErrStaleSnapshot
	}
	var gap *coveragedetail.Gap
	if selector.Scope == ScopeCoverageGap {
		for i := range index.Gaps {
			candidate := &index.Gaps[i]
			if candidate.ID != selector.ID {
				continue
			}
			if gap != nil {
				return ManagedTarget{}, ErrStaleSnapshot
			}
			gap = candidate
		}
		if gap == nil || gap.Kind != "line" && gap.Kind != "branch" {
			return ManagedTarget{}, ErrStaleSnapshot
		}
		stable, err := coveragedetail.StableGapID(index.ReportID, gap.FunctionID, gap.Kind, gap.Location, gap.Ordinal)
		if err != nil || stable != gap.ID {
			return ManagedTarget{}, ErrStaleSnapshot
		}
	}
	var found *ManagedTarget
	for _, file := range index.Files {
		stable, err := coveragedetail.StableFileID(index.ProjectID, file.RelativePath)
		if err != nil || stable != file.ID {
			return ManagedTarget{}, ErrStaleSnapshot
		}
		if file.Status != coveragedetail.StatusCurrent || !validDigest(file.SourceSHA256) {
			continue
		}
		if selector.Scope == ScopeFile && file.ID == selector.ID {
			if found != nil {
				return ManagedTarget{}, ErrStaleSnapshot
			}
			found = &ManagedTarget{FileID: file.ID, File: file.RelativePath, SourceDigest: file.SourceSHA256}
		}
		for _, function := range file.Functions {
			match := selector.Scope == ScopeSymbol && function.ID == selector.ID || gap != nil && function.ID == gap.FunctionID && file.ID == gap.FileID
			if !match || function.Status != coveragedetail.StatusCurrent || !validID(function.ID) || function.Name == "" {
				continue
			}
			if found != nil {
				return ManagedTarget{}, ErrStaleSnapshot
			}
			value := ManagedTarget{FileID: file.ID, FunctionID: function.ID, File: file.RelativePath, FunctionName: function.Name, SourceDigest: file.SourceSHA256}
			if gap != nil {
				value.GapID = gap.ID
			}
			found = &value
		}
	}
	if found == nil || selector.Scope != ScopeFile && selector.Scope != ScopeSymbol && selector.Scope != ScopeCoverageGap {
		return ManagedTarget{}, ErrStaleSnapshot
	}
	return *found, nil
}

const (
	ScopeSymbol      Scope = "symbol"
	ScopeFile        Scope = "file"
	ScopeTarget      Scope = "target"
	ScopeWorkspace   Scope = "workspace"
	ScopeCoverageGap Scope = "coverage-gap"
)

type Framework string

const (
	FrameworkAuto     Framework = "auto"
	FrameworkCppUTest Framework = "cpputest"
	FrameworkUnity    Framework = "unity"
)

type Goals struct {
	FunctionPercent float64 `json:"functionPercent"`
	LinePercent     float64 `json:"linePercent"`
	BranchPercent   float64 `json:"branchPercent"`
}
type Budgets struct {
	WallTimeMS     int64 `json:"wallTimeMs"`
	CandidateCount int64 `json:"candidateCount"`
	MemoryMiB      int64 `json:"memoryMiB"`
	Concurrency    int64 `json:"concurrency"`
}
type SnapshotIdentity struct {
	WorkspaceGeneration    string `json:"workspaceGeneration"`
	CompileSnapshotDigest  string `json:"compileSnapshotDigest"`
	CoverageSnapshotDigest string `json:"coverageSnapshotDigest"`
	SourceDigest           string `json:"sourceDigest"`
	CMakeTargetDigest      string `json:"cmakeTargetDigest"`
	FrameworkBundleDigest  string `json:"frameworkBundleDigest"`
	AnalyzerBundleDigest   string `json:"analyzerBundleDigest"`
	BaselineReportDigest   string `json:"baselineReportDigest"`
	ProcessOwnerDigest     string `json:"processOwnerDigest,omitempty"`
}

// Request contains only service-resolved identities and closed protocol inputs.
// No source, executable, host path, or process environment may be added here.
type Request struct {
	IdempotencyKey      string `json:"idempotencyKey"`
	WorkspaceGeneration string `json:"workspaceGeneration"`
	ProjectID           string `json:"projectId"`
	Scope               Scope  `json:"scope"`
	SymbolID            string `json:"symbolId,omitempty"`
	File                string `json:"file,omitempty"`
	TargetID            string `json:"targetId,omitempty"`
	CoverageReportID    string `json:"coverageReportId,omitempty"`
	// ManagedGapID binds a v1.6 report-gap selection to the durable run. It is
	// optional for legacy v1.5 coverage-gap requests and absent in other scopes.
	ManagedGapID           string    `json:"managedGapId,omitempty"`
	Framework              Framework `json:"framework"`
	Goals                  Goals     `json:"goals"`
	Budgets                Budgets   `json:"budgets"`
	CompileSnapshotDigest  string    `json:"compileSnapshotDigest"`
	CoverageSnapshotDigest string    `json:"coverageSnapshotDigest"`
	SourceDigest           string    `json:"sourceDigest"`
	CMakeTargetDigest      string    `json:"cmakeTargetDigest"`
	FrameworkBundleDigest  string    `json:"frameworkBundleDigest"`
	AnalyzerBundleDigest   string    `json:"analyzerBundleDigest"`
	BaselineReportDigest   string    `json:"baselineReportDigest"`
	ProcessOwnerDigest     string    `json:"processOwnerDigest,omitempty"`
	// Bound to the authenticated service credential at creation. Legacy rows
	// without an owner remain readable internally but are not route-accessible.
	SessionOwnerDigest string `json:"sessionOwnerDigest,omitempty"`
}

func (r Request) SnapshotIdentity() SnapshotIdentity {
	return SnapshotIdentity{WorkspaceGeneration: r.WorkspaceGeneration, CompileSnapshotDigest: r.CompileSnapshotDigest, CoverageSnapshotDigest: r.CoverageSnapshotDigest, SourceDigest: r.SourceDigest, CMakeTargetDigest: r.CMakeTargetDigest, FrameworkBundleDigest: r.FrameworkBundleDigest, AnalyzerBundleDigest: r.AnalyzerBundleDigest, BaselineReportDigest: r.BaselineReportDigest, ProcessOwnerDigest: r.ProcessOwnerDigest}
}

// IsLegacySnapshot recognizes only the pre-v11 closed request shape. Such
// rows may be displayed after upgrade but never resumed or published: the
// missing identities cannot be reconstructed from historical metadata.
func (r Request) IsLegacySnapshot() bool {
	if r.SourceDigest != "" || r.CMakeTargetDigest != "" || r.FrameworkBundleDigest != "" || r.AnalyzerBundleDigest != "" || r.BaselineReportDigest != "" || r.ProcessOwnerDigest != "" {
		return false
	}
	clone := r
	placeholder := strings.Repeat("0", 64)
	clone.SourceDigest, clone.CMakeTargetDigest, clone.FrameworkBundleDigest = placeholder, placeholder, placeholder
	clone.AnalyzerBundleDigest, clone.BaselineReportDigest, clone.ProcessOwnerDigest = placeholder, placeholder, placeholder
	return ValidateRequest(clone) == nil
}
func (r Request) SnapshotMatches(now SnapshotIdentity) error {
	if r.SnapshotIdentity() != now || !validDigest(now.WorkspaceGeneration) || !validDigest(now.CompileSnapshotDigest) || !validDigest(now.CoverageSnapshotDigest) || !validDigest(now.SourceDigest) || !validDigest(now.CMakeTargetDigest) || !validDigest(now.FrameworkBundleDigest) || !validDigest(now.AnalyzerBundleDigest) || !validDigest(now.BaselineReportDigest) || !validDigest(now.ProcessOwnerDigest) {
		return ErrStaleSnapshot
	}
	return nil
}

func ValidateRequest(r Request) error {
	if !validID(r.IdempotencyKey) || !validDigest(r.WorkspaceGeneration) || !validDigest(r.CompileSnapshotDigest) || !validDigest(r.CoverageSnapshotDigest) || !validDigest(r.SourceDigest) || !validDigest(r.CMakeTargetDigest) || !validDigest(r.FrameworkBundleDigest) || !validDigest(r.AnalyzerBundleDigest) || !validDigest(r.BaselineReportDigest) || !validDigest(r.ProcessOwnerDigest) || r.SessionOwnerDigest != "" && !validDigest(r.SessionOwnerDigest) || !validProjectID(r.ProjectID) || !validGoals(r.Goals) || !validBudgets(r.Budgets) {
		return ErrInvalid
	}
	switch r.Framework {
	case FrameworkAuto, FrameworkCppUTest, FrameworkUnity:
	default:
		return ErrInvalid
	}
	switch r.Scope {
	case ScopeSymbol:
		if !validSymbol(r.SymbolID) || r.File != "" || r.TargetID != "" || r.CoverageReportID != "" || r.ManagedGapID != "" {
			return ErrInvalid
		}
	case ScopeFile:
		if !validRelativePath(r.File) || r.SymbolID != "" || r.TargetID != "" || r.CoverageReportID != "" || r.ManagedGapID != "" {
			return ErrInvalid
		}
	case ScopeTarget:
		if !validDigest(r.TargetID) || r.SymbolID != "" || r.File != "" || r.CoverageReportID != "" || r.ManagedGapID != "" {
			return ErrInvalid
		}
	case ScopeWorkspace:
		if r.SymbolID != "" || r.File != "" || r.TargetID != "" || r.CoverageReportID != "" || r.ManagedGapID != "" {
			return ErrInvalid
		}
	case ScopeCoverageGap:
		if !validID(r.CoverageReportID) || r.ManagedGapID != "" && !validID(r.ManagedGapID) || r.SymbolID != "" || r.File != "" || r.TargetID != "" {
			return ErrInvalid
		}
	default:
		return ErrInvalid
	}
	return nil
}

func validGoals(g Goals) bool {
	for _, v := range []float64{g.FunctionPercent, g.LinePercent, g.BranchPercent} {
		if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 || v > 100 {
			return false
		}
	}
	return true
}
func validBudgets(b Budgets) bool {
	return b.WallTimeMS >= 1 && b.WallTimeMS <= 86400000 && b.CandidateCount >= 1 && b.CandidateCount <= 1000 && b.MemoryMiB >= 64 && b.MemoryMiB <= 1048576 && b.Concurrency >= 1 && b.Concurrency <= 256 && b.Concurrency <= b.CandidateCount
}
func validID(v string) bool     { return validLowerHex(v, 32) }
func validDigest(v string) bool { return validLowerHex(v, 64) }
func validLowerHex(v string, n int) bool {
	if len(v) != n {
		return false
	}
	for _, c := range v {
		if c < '0' || c > '9' {
			if c < 'a' || c > 'f' {
				return false
			}
		}
	}
	return true
}
func validProjectID(v string) bool {
	if len(v) < 1 || len(v) > 64 {
		return false
	}
	for i, c := range v {
		if c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' {
			continue
		}
		if i > 0 && (c == '.' || c == '_' || c == '-') {
			continue
		}
		return false
	}
	return true
}
func validSymbol(v string) bool {
	if len(v) < 3 || len(v) > 256 || strings.Count(v, ":") != 1 || strings.ContainsAny(v, "/\\\n\r\x00") {
		return false
	}
	parts := strings.Split(v, ":")
	return parts[0] != "" && parts[1] != ""
}
func validRelativePath(v string) bool {
	if len(v) < 1 || len(v) > 1024 || strings.ContainsAny(v, "\\:\x00\n\r") || strings.HasPrefix(v, "/") {
		return false
	}
	for _, seg := range strings.Split(v, "/") {
		if seg == "" || seg == "." || seg == ".." || strings.HasSuffix(seg, ".") {
			return false
		}
		for _, c := range seg {
			if c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '_' || c == '.' || c == '-' {
				continue
			}
			return false
		}
	}
	return true
}
