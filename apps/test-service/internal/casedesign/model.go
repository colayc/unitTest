// Package casedesign defines typed, mutable case drafts independently of
// accepted generated source and real execution results.
package casedesign

import (
	"errors"
	assertion "unit-test-ide.local/test-service/internal/testgenassert"
	solver "unit-test-ide.local/test-service/internal/testgensolver"
)

type Binding struct {
	ProjectID, SourceRelativePath, FileID, FunctionID, SymbolID string
	SourceDigest, WorkspaceGeneration, ToolchainID, TargetID    string
	ProgramDigest, BuildConfigDigest, Framework                 string
}
type Expectation struct {
	Target     assertion.TargetKind
	Name       string
	Value      solver.Value
	Rule       assertion.ComparisonRule
	Tolerance  string
	Provenance string
}
type Definition struct {
	SchemaVersion              int
	CaseID, ScenarioID, Origin string
	Revision                   int64
	Enabled                    bool
	Binding                    Binding
	Inputs                     []solver.Input
	Expectations               []Expectation
	Digest                     string
}
type Record struct {
	Draft                                                Definition
	AcceptedRevision                                     int64
	AcceptedDigest, TestPath, BlockDigest, BindingStatus string
	PendingAcceptance                                    bool
}
type Query struct {
	ProjectID, Cursor string
	Limit             int
}
type Page struct {
	Items                      []Record
	NextCursor, SnapshotDigest string
}
type DraftChange struct {
	Definition       Definition
	ExpectedRevision int64
}

const (
	SchemaVersion   = 1
	OriginAutomatic = "automatic"
	OriginManual    = "manual"
	OriginImport    = "import"
)

var (
	ErrInvalid     = errors.New("invalid case definition")
	ErrConflict    = errors.New("case definition conflict")
	ErrUnavailable = errors.New("case definitions unavailable")
	ErrNotFound    = errors.New("case definition not found")
	ErrUncertain   = errors.New("case definition state uncertain")
)
