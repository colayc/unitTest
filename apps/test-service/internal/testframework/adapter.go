package testframework

import (
	"context"

	"unit-test-ide.local/test-service/internal/ctest"
	"unit-test-ide.local/test-service/internal/testdomain"
)

// Adapter is the stable boundary between generic CTest discovery and a
// framework-specific implementation. It deliberately exposes neither a
// process handle nor Protocol-generated types.
type Adapter interface {
	Framework() testdomain.Framework
	ContractVersion() string
	Verify(context.Context, ctest.ExecutionDescriptor) (Capabilities, error)
	Discover(context.Context, ctest.ExecutionDescriptor) (DiscoveryResult, error)
	PlanRun(context.Context, RunInput) (RunPlan, error)
	NewParser(ParseInput) (ResultParser, error)
}

type DiscoveredItem struct {
	Kind              testdomain.ItemKind
	ParentKind        testdomain.ItemKind
	ParentLogicalName string
	LogicalName       string
	DisplayName       string
	Labels            []string
	SourceLocation    *testdomain.SourceLocation
	Disabled          bool
	Parameters        []testdomain.Parameter
}

type DiscoveryResult struct {
	Items       []DiscoveredItem
	Diagnostics []testdomain.Diagnostic
	Partial     bool
}

type RunItem struct {
	ItemID            testdomain.ID
	ParentLogicalName string
	LogicalName       string
	Parameters        []testdomain.Parameter
}

type RunInput struct {
	Descriptor ctest.ExecutionDescriptor
	Items      []RunItem
}

type ExpectedCase struct {
	ItemID            testdomain.ID
	ParentLogicalName string
	LogicalName       string
}

type RunInvocation struct {
	Arguments     []string
	ExpectedCases []ExpectedCase
}

// RunPlan contains framework-owned invocations for a Service-owned execution
// plan. The executable remains fixed by the verified CTest descriptor, and
// every invocation records the stable items whose output it may complete.
type RunPlan struct {
	Invocations        []RunInvocation
	Environment        []ctest.EnvironmentEntry
	EnvironmentChanges []ctest.EnvironmentModification
	WorkingDirectory   string
	TimeoutSeconds     *float64
}

type Stream string

const (
	StreamStdout Stream = "stdout"
	StreamStderr Stream = "stderr"
)

type ParseInput struct {
	Descriptor ctest.ExecutionDescriptor
	Items      []RunItem
}

type ProcessResult struct {
	ExitCode    int
	Termination ProcessTermination
}

type ProcessTermination string

const (
	ProcessExited    ProcessTermination = "exited"
	ProcessTimedOut  ProcessTermination = "timed_out"
	ProcessCrashed   ProcessTermination = "crashed"
	ProcessCancelled ProcessTermination = "cancelled"
)

type CaseStatus string

const (
	CasePassed  CaseStatus = "passed"
	CaseFailed  CaseStatus = "failed"
	CaseSkipped CaseStatus = "skipped"
	CaseNotRun  CaseStatus = "not_run"
)

type ParsedSourceLocation struct {
	Path   string
	Line   int
	Column int
}

type ParsedCaseResult struct {
	ItemID            testdomain.ID
	ParentLogicalName string
	LogicalName       string
	Status            CaseStatus
	DurationMS        int64
	Category          string
	Message           string
	SourceLocation    *ParsedSourceLocation
	Partial           bool
}

type ResultEvent struct {
	Case ParsedCaseResult
}

type ParseResult struct {
	Cases       []ParsedCaseResult
	Diagnostics []testdomain.Diagnostic
	Complete    bool
}

type ResultParser interface {
	Feed(Stream, []byte) ([]ResultEvent, error)
	Finish(ProcessResult) (ParseResult, error)
}
