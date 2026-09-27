package testgendomain

import (
	"errors"
	"math"
	"strings"
)

var ErrInvalid = errors.New("invalid test generation value")
var ErrStaleSnapshot = errors.New("stale test generation snapshot")

type Scope string

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
}

// Request contains only service-resolved identities and closed protocol inputs.
// No source, executable, host path, or process environment may be added here.
type Request struct {
	IdempotencyKey         string    `json:"idempotencyKey"`
	WorkspaceGeneration    string    `json:"workspaceGeneration"`
	ProjectID              string    `json:"projectId"`
	Scope                  Scope     `json:"scope"`
	SymbolID               string    `json:"symbolId,omitempty"`
	File                   string    `json:"file,omitempty"`
	TargetID               string    `json:"targetId,omitempty"`
	CoverageReportID       string    `json:"coverageReportId,omitempty"`
	Framework              Framework `json:"framework"`
	Goals                  Goals     `json:"goals"`
	Budgets                Budgets   `json:"budgets"`
	CompileSnapshotDigest  string    `json:"compileSnapshotDigest"`
	CoverageSnapshotDigest string    `json:"coverageSnapshotDigest"`
}

func (r Request) SnapshotIdentity() SnapshotIdentity {
	return SnapshotIdentity{r.WorkspaceGeneration, r.CompileSnapshotDigest, r.CoverageSnapshotDigest}
}
func (r Request) SnapshotMatches(now SnapshotIdentity) error {
	if r.SnapshotIdentity() != now || !validDigest(now.WorkspaceGeneration) || !validDigest(now.CompileSnapshotDigest) || !validDigest(now.CoverageSnapshotDigest) {
		return ErrStaleSnapshot
	}
	return nil
}

func ValidateRequest(r Request) error {
	if !validID(r.IdempotencyKey) || !validDigest(r.WorkspaceGeneration) || !validDigest(r.CompileSnapshotDigest) || !validDigest(r.CoverageSnapshotDigest) || !validProjectID(r.ProjectID) || !validGoals(r.Goals) || !validBudgets(r.Budgets) {
		return ErrInvalid
	}
	switch r.Framework {
	case FrameworkAuto, FrameworkCppUTest, FrameworkUnity:
	default:
		return ErrInvalid
	}
	switch r.Scope {
	case ScopeSymbol:
		if !validSymbol(r.SymbolID) || r.File != "" || r.TargetID != "" || r.CoverageReportID != "" {
			return ErrInvalid
		}
	case ScopeFile:
		if !validRelativePath(r.File) || r.SymbolID != "" || r.TargetID != "" || r.CoverageReportID != "" {
			return ErrInvalid
		}
	case ScopeTarget:
		if !validDigest(r.TargetID) || r.SymbolID != "" || r.File != "" || r.CoverageReportID != "" {
			return ErrInvalid
		}
	case ScopeWorkspace:
		if r.SymbolID != "" || r.File != "" || r.TargetID != "" || r.CoverageReportID != "" {
			return ErrInvalid
		}
	case ScopeCoverageGap:
		if !validID(r.CoverageReportID) || r.SymbolID != "" || r.File != "" || r.TargetID != "" {
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
