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
	LogicalName string
	Parameters  []testdomain.Parameter
}

type RunInput struct {
	Descriptor ctest.ExecutionDescriptor
	Items      []RunItem
}

// RunPlan contains framework-owned additions to a Service-owned execution
// plan. The executable remains fixed by the verified CTest descriptor.
type RunPlan struct {
	Arguments          []string
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
	ExitCode int
	TimedOut bool
}

type ParsedCaseResult struct {
	LogicalName string
	Status      string
	DurationMS  int64
	Message     string
}

type ParseResult struct {
	Cases       []ParsedCaseResult
	Diagnostics []testdomain.Diagnostic
	Complete    bool
}

type ResultParser interface {
	Write(Stream, []byte) error
	Finish(ProcessResult) (ParseResult, error)
}
