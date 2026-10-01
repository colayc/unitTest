package protocolmodelv15test

import "time"

type TestContractV15 struct {
	Catalog   TestCatalog      `json:"catalog"`
	Result    TestItemResult   `json:"result"`
	Run       TestRun          `json:"run"`
	RunPage   TestRunPage      `json:"runPage"`
	Selection TestSelectionV15 `json:"selection"`
}

type TestCatalog struct {
	Containers  []TestContainer `json:"containers"`
	Diagnostics []DiagnosticV15 `json:"diagnostics"`
	GeneratedAt time.Time       `json:"generatedAt"`
	Items       []TestItem      `json:"items"`
	NextCursor  *string         `json:"nextCursor,omitempty"`
	Partial     bool            `json:"partial"`
	ProfileID   string          `json:"profileId"`
	ProjectID   string          `json:"projectId"`
	Revision    string          `json:"revision"`
}

type TestContainer struct {
	Capabilities     TestCapabilitiesV15    `json:"capabilities"`
	CtestLogicalName string                 `json:"ctestLogicalName"`
	DegradedReason   *string                `json:"degradedReason,omitempty"`
	Disabled         bool                   `json:"disabled"`
	DisplayName      string                 `json:"displayName"`
	Framework        TestFrameworkV15       `json:"framework"`
	ID               string                 `json:"id"`
	Labels           []string               `json:"labels"`
	ProjectID        string                 `json:"projectId"`
	SourceLocation   *TestSourceLocationV15 `json:"sourceLocation,omitempty"`
}

type TestCapabilitiesV15 struct {
	CanDiscoverCases        bool `json:"canDiscoverCases"`
	CanReportMockDetails    bool `json:"canReportMockDetails"`
	CanReportSkipped        bool `json:"canReportSkipped"`
	CanReportSourceLocation bool `json:"canReportSourceLocation"`
	CanRunCase              bool `json:"canRunCase"`
}

type TestSourceLocationV15 struct {
	Column     *int64                  `json:"column,omitempty"`
	Line       *int64                  `json:"line,omitempty"`
	Navigable  bool                    `json:"navigable"`
	Provenance TestSourceProvenanceV15 `json:"provenance"`
	URI        string                  `json:"uri"`
}

type DiagnosticV15 struct {
	Category  CategoryV15           `json:"category"`
	Code      string                `json:"code"`
	Column    *int64                `json:"column,omitempty"`
	Line      *int64                `json:"line,omitempty"`
	Message   string                `json:"message"`
	Severity  DiagnosticSeverityV15 `json:"severity"`
	SourceURI *string               `json:"sourceUri,omitempty"`
}

type TestItem struct {
	ContainerID    string                 `json:"containerId"`
	Disabled       bool                   `json:"disabled"`
	DisplayName    string                 `json:"displayName"`
	Framework      TestFrameworkV15       `json:"framework"`
	ID             string                 `json:"id"`
	Kind           TestItemKindV15        `json:"kind"`
	Labels         []string               `json:"labels"`
	LogicalName    string                 `json:"logicalName"`
	Parameters     []TestParameterV15     `json:"parameters,omitempty"`
	ParentID       *string                `json:"parentId,omitempty"`
	SourceLocation *TestSourceLocationV15 `json:"sourceLocation,omitempty"`
}

type TestParameterV15 struct {
	Name  string `json:"name"`
	Value *Value `json:"value"`
}

type TestItemResult struct {
	ContainerID    string                 `json:"containerId"`
	DurationMS     *int64                 `json:"durationMs,omitempty"`
	FailureDetails []TestFailureDetailV15 `json:"failureDetails"`
	ItemID         string                 `json:"itemId"`
	Iteration      int64                  `json:"iteration"`
	Outcome        TestItemOutcomeV15     `json:"outcome"`
	OutputRefs     []string               `json:"outputRefs"`
	Partial        bool                   `json:"partial"`
	Reason         *TestResultReasonV15   `json:"reason,omitempty"`
	SourceLocation *TestSourceLocationV15 `json:"sourceLocation,omitempty"`
}

type TestFailureDetailV15 struct {
	Actual       *string                 `json:"actual,omitempty"`
	Category     CategoryV15             `json:"category"`
	EvidenceRefs []string                `json:"evidenceRefs"`
	Expected     *string                 `json:"expected,omitempty"`
	Locations    []TestSourceLocationV15 `json:"locations"`
	Message      string                  `json:"message"`
	Subtype      *TestFailureSubtypeV15  `json:"subtype,omitempty"`
}

type TestRun struct {
	CatalogRevision   string                   `json:"catalogRevision"`
	FinishedAt        *time.Time               `json:"finishedAt,omitempty"`
	Incomplete        bool                     `json:"incomplete"`
	Outcome           *TestRunOutcomeV15       `json:"outcome,omitempty"`
	ProfileID         string                   `json:"profileId"`
	ProjectID         string                   `json:"projectId"`
	ResultRevision    string                   `json:"resultRevision"`
	RunID             string                   `json:"runId"`
	SelectionSnapshot TestSelectionSnapshotV15 `json:"selectionSnapshot"`
	StartedAt         *time.Time               `json:"startedAt,omitempty"`
	Status            TestRunStatusV15         `json:"status"`
	Summary           TestRunSummaryV15        `json:"summary"`
	TaskID            string                   `json:"taskId"`
	ToolchainID       string                   `json:"toolchainId"`
}

type TestSelectionSnapshotV15 struct {
	ContainerIDS []string             `json:"containerIds"`
	ItemIDS      []string             `json:"itemIds"`
	Mode         TestSelectionModeV15 `json:"mode"`
}

type TestRunSummaryV15 struct {
	Cancelled  int64 `json:"cancelled"`
	Completed  int64 `json:"completed"`
	Errored    int64 `json:"errored"`
	Failed     int64 `json:"failed"`
	Iterations int64 `json:"iterations"`
	NotRun     int64 `json:"notRun"`
	Passed     int64 `json:"passed"`
	Skipped    int64 `json:"skipped"`
	TimedOut   int64 `json:"timedOut"`
	Total      int64 `json:"total"`
}

type TestRunPage struct {
	Items      []TestRun `json:"items"`
	NextCursor *string   `json:"nextCursor,omitempty"`
}

type TestSelectionV15 interface{ isTestSelectionV15() }
type AllTestSelectionV15 struct {
	Mode TestSelectionModeV15 `json:"mode"`
}

func (AllTestSelectionV15) isTestSelectionV15() {}

type ContainersTestSelectionV15 struct {
	Mode         TestSelectionModeV15 `json:"mode"`
	ContainerIDs []string             `json:"containerIds"`
}

func (ContainersTestSelectionV15) isTestSelectionV15() {}

type ItemsTestSelectionV15 struct {
	Mode    TestSelectionModeV15 `json:"mode"`
	ItemIDs []string             `json:"itemIds"`
}

func (ItemsTestSelectionV15) isTestSelectionV15() {}

type FilterTestSelectionV15 struct {
	Mode   TestSelectionModeV15 `json:"mode"`
	Filter TestFilterV15        `json:"filter"`
}

func (FilterTestSelectionV15) isTestSelectionV15() {}

type FailedFromRunTestSelectionV15 struct {
	Mode  TestSelectionModeV15 `json:"mode"`
	RunID string               `json:"runId"`
}

func (FailedFromRunTestSelectionV15) isTestSelectionV15() {}

type TestFilterV15 struct {
	ExcludeItemIDS []string `json:"excludeItemIds,omitempty"`
	Group          *string  `json:"group,omitempty"`
	IncludeItemIDS []string `json:"includeItemIds,omitempty"`
	Label          *string  `json:"label,omitempty"`
	NameContains   *string  `json:"nameContains,omitempty"`
	Suite          *string  `json:"suite,omitempty"`
}

type TestFrameworkV15 string

const (
	Cpputest    TestFrameworkV15 = "cpputest"
	OpaqueCtest TestFrameworkV15 = "opaque-ctest"
	Unity       TestFrameworkV15 = "unity"
)

type TestSourceProvenanceV15 string

const (
	CtestBacktrace    TestSourceProvenanceV15 = "ctest-backtrace"
	FrameworkManifest TestSourceProvenanceV15 = "framework-manifest"
	FrameworkOutput   TestSourceProvenanceV15 = "framework-output"
	MockActualCall    TestSourceProvenanceV15 = "mock-actual-call"
	MockExpectation   TestSourceProvenanceV15 = "mock-expectation"
	TestDeclaration   TestSourceProvenanceV15 = "test-declaration"
)

type CategoryV15 string

const (
	AssertionFailure       CategoryV15 = "assertion_failure"
	BuildError             CategoryV15 = "build_error"
	CategoryV15Cancelled   CategoryV15 = "cancelled"
	ConfigurationError     CategoryV15 = "configuration_error"
	FrameworkOutputInvalid CategoryV15 = "framework_output_invalid"
	InconsistentExitStatus CategoryV15 = "inconsistent_exit_status"
	InfrastructureError    CategoryV15 = "infrastructure_error"
	TestProcessCrash       CategoryV15 = "test_process_crash"
	TestTimeout            CategoryV15 = "test_timeout"
	UnexpectedExit         CategoryV15 = "unexpected_exit"
)

type DiagnosticSeverityV15 string

const (
	Error   DiagnosticSeverityV15 = "error"
	Info    DiagnosticSeverityV15 = "info"
	Warning DiagnosticSeverityV15 = "warning"
)

type TestItemKindV15 string

const (
	Case  TestItemKindV15 = "case"
	Group TestItemKindV15 = "group"
	Suite TestItemKindV15 = "suite"
)

type TestFailureSubtypeV15 string

const (
	MockFailure           TestFailureSubtypeV15 = "mock_failure"
	MockMissingCall       TestFailureSubtypeV15 = "mock_missing_call"
	MockParameterMismatch TestFailureSubtypeV15 = "mock_parameter_mismatch"
	MockUnexpectedCall    TestFailureSubtypeV15 = "mock_unexpected_call"
)

type TestItemOutcomeV15 string

const (
	NotRun                      TestItemOutcomeV15 = "not_run"
	Skipped                     TestItemOutcomeV15 = "skipped"
	TestItemOutcomeV15Cancelled TestItemOutcomeV15 = "cancelled"
	TestItemOutcomeV15Errored   TestItemOutcomeV15 = "errored"
	TestItemOutcomeV15Failed    TestItemOutcomeV15 = "failed"
	TestItemOutcomeV15Passed    TestItemOutcomeV15 = "passed"
	TestItemOutcomeV15TimedOut  TestItemOutcomeV15 = "timed_out"
)

type TestResultReasonV15 string

const (
	BuildBlocked        TestResultReasonV15 = "build_blocked"
	ContainerTerminated TestResultReasonV15 = "container_terminated"
	Disabled            TestResultReasonV15 = "disabled"
	SelectionAborted    TestResultReasonV15 = "selection_aborted"
	ServiceRestarted    TestResultReasonV15 = "service_restarted"
	StaleCatalog        TestResultReasonV15 = "stale_catalog"
)

type TestRunOutcomeV15 string

const (
	Blocked                    TestRunOutcomeV15 = "blocked"
	Interrupted                TestRunOutcomeV15 = "interrupted"
	TestRunOutcomeV15Cancelled TestRunOutcomeV15 = "cancelled"
	TestRunOutcomeV15Errored   TestRunOutcomeV15 = "errored"
	TestRunOutcomeV15Failed    TestRunOutcomeV15 = "failed"
	TestRunOutcomeV15Passed    TestRunOutcomeV15 = "passed"
	TestRunOutcomeV15TimedOut  TestRunOutcomeV15 = "timed_out"
)

type TestSelectionModeV15 string

const (
	All           TestSelectionModeV15 = "all"
	Containers    TestSelectionModeV15 = "containers"
	FailedFromRun TestSelectionModeV15 = "failedFromRun"
	Filter        TestSelectionModeV15 = "filter"
	Items         TestSelectionModeV15 = "items"
)

type TestRunStatusV15 string

const (
	Completed TestRunStatusV15 = "completed"
	Queued    TestRunStatusV15 = "queued"
	Running   TestRunStatusV15 = "running"
)

type Value struct {
	Bool   *bool
	Double *float64
	String *string
}
