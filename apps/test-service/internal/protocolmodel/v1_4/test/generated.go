package protocolmodelv14test

import "time"

type TestContractV14 struct {
	Catalog   TestCatalog      `json:"catalog"`
	Result    TestItemResult   `json:"result"`
	Run       TestRun          `json:"run"`
	RunPage   TestRunPage      `json:"runPage"`
	Selection TestSelectionV14 `json:"selection"`
}

type TestCatalog struct {
	Containers  []TestContainer `json:"containers"`
	Diagnostics []DiagnosticV14 `json:"diagnostics"`
	GeneratedAt time.Time       `json:"generatedAt"`
	Items       []TestItem      `json:"items"`
	NextCursor  *string         `json:"nextCursor,omitempty"`
	Partial     bool            `json:"partial"`
	ProfileID   string          `json:"profileId"`
	ProjectID   string          `json:"projectId"`
	Revision    string          `json:"revision"`
}

type TestContainer struct {
	Capabilities     TestCapabilitiesV14    `json:"capabilities"`
	CtestLogicalName string                 `json:"ctestLogicalName"`
	DegradedReason   *string                `json:"degradedReason,omitempty"`
	Disabled         bool                   `json:"disabled"`
	DisplayName      string                 `json:"displayName"`
	Framework        TestFrameworkV14       `json:"framework"`
	ID               string                 `json:"id"`
	Labels           []string               `json:"labels"`
	ProjectID        string                 `json:"projectId"`
	SourceLocation   *TestSourceLocationV14 `json:"sourceLocation,omitempty"`
}

type TestCapabilitiesV14 struct {
	CanDiscoverCases        bool `json:"canDiscoverCases"`
	CanReportMockDetails    bool `json:"canReportMockDetails"`
	CanReportSkipped        bool `json:"canReportSkipped"`
	CanReportSourceLocation bool `json:"canReportSourceLocation"`
	CanRunCase              bool `json:"canRunCase"`
}

type TestSourceLocationV14 struct {
	Column     *int64                  `json:"column,omitempty"`
	Line       *int64                  `json:"line,omitempty"`
	Navigable  bool                    `json:"navigable"`
	Provenance TestSourceProvenanceV14 `json:"provenance"`
	URI        string                  `json:"uri"`
}

type DiagnosticV14 struct {
	Category  CategoryV14           `json:"category"`
	Code      string                `json:"code"`
	Column    *int64                `json:"column,omitempty"`
	Line      *int64                `json:"line,omitempty"`
	Message   string                `json:"message"`
	Severity  DiagnosticSeverityV14 `json:"severity"`
	SourceURI *string               `json:"sourceUri,omitempty"`
}

type TestItem struct {
	ContainerID    string                 `json:"containerId"`
	Disabled       bool                   `json:"disabled"`
	DisplayName    string                 `json:"displayName"`
	Framework      TestFrameworkV14       `json:"framework"`
	ID             string                 `json:"id"`
	Kind           TestItemKindV14        `json:"kind"`
	Labels         []string               `json:"labels"`
	LogicalName    string                 `json:"logicalName"`
	Parameters     []TestParameterV14     `json:"parameters,omitempty"`
	ParentID       *string                `json:"parentId,omitempty"`
	SourceLocation *TestSourceLocationV14 `json:"sourceLocation,omitempty"`
}

type TestParameterV14 struct {
	Name  string `json:"name"`
	Value *Value `json:"value"`
}

type TestItemResult struct {
	ContainerID    string                 `json:"containerId"`
	DurationMS     *int64                 `json:"durationMs,omitempty"`
	FailureDetails []TestFailureDetailV14 `json:"failureDetails"`
	ItemID         string                 `json:"itemId"`
	Iteration      int64                  `json:"iteration"`
	Outcome        TestItemOutcomeV14     `json:"outcome"`
	OutputRefs     []string               `json:"outputRefs"`
	Partial        bool                   `json:"partial"`
	Reason         *TestResultReasonV14   `json:"reason,omitempty"`
	SourceLocation *TestSourceLocationV14 `json:"sourceLocation,omitempty"`
}

type TestFailureDetailV14 struct {
	Actual       *string                 `json:"actual,omitempty"`
	Category     CategoryV14             `json:"category"`
	EvidenceRefs []string                `json:"evidenceRefs"`
	Expected     *string                 `json:"expected,omitempty"`
	Locations    []TestSourceLocationV14 `json:"locations"`
	Message      string                  `json:"message"`
	Subtype      *TestFailureSubtypeV14  `json:"subtype,omitempty"`
}

type TestRun struct {
	CatalogRevision   string                   `json:"catalogRevision"`
	FinishedAt        *time.Time               `json:"finishedAt,omitempty"`
	Incomplete        bool                     `json:"incomplete"`
	Outcome           *TestRunOutcomeV14       `json:"outcome,omitempty"`
	ProfileID         string                   `json:"profileId"`
	ProjectID         string                   `json:"projectId"`
	ResultRevision    string                   `json:"resultRevision"`
	RunID             string                   `json:"runId"`
	SelectionSnapshot TestSelectionSnapshotV14 `json:"selectionSnapshot"`
	StartedAt         *time.Time               `json:"startedAt,omitempty"`
	Status            TestRunStatusV14         `json:"status"`
	Summary           TestRunSummaryV14        `json:"summary"`
	TaskID            string                   `json:"taskId"`
	ToolchainID       string                   `json:"toolchainId"`
}

type TestSelectionSnapshotV14 struct {
	ContainerIDS []string             `json:"containerIds"`
	ItemIDS      []string             `json:"itemIds"`
	Mode         TestSelectionModeV14 `json:"mode"`
}

type TestRunSummaryV14 struct {
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

type TestSelectionV14 interface{ isTestSelectionV14() }
type AllTestSelectionV14 struct {
	Mode TestSelectionModeV14 `json:"mode"`
}

func (AllTestSelectionV14) isTestSelectionV14() {}

type ContainersTestSelectionV14 struct {
	Mode         TestSelectionModeV14 `json:"mode"`
	ContainerIDs []string             `json:"containerIds"`
}

func (ContainersTestSelectionV14) isTestSelectionV14() {}

type ItemsTestSelectionV14 struct {
	Mode    TestSelectionModeV14 `json:"mode"`
	ItemIDs []string             `json:"itemIds"`
}

func (ItemsTestSelectionV14) isTestSelectionV14() {}

type FilterTestSelectionV14 struct {
	Mode   TestSelectionModeV14 `json:"mode"`
	Filter TestFilterV14        `json:"filter"`
}

func (FilterTestSelectionV14) isTestSelectionV14() {}

type FailedFromRunTestSelectionV14 struct {
	Mode  TestSelectionModeV14 `json:"mode"`
	RunID string               `json:"runId"`
}

func (FailedFromRunTestSelectionV14) isTestSelectionV14() {}

type TestFilterV14 struct {
	ExcludeItemIDS []string `json:"excludeItemIds,omitempty"`
	Group          *string  `json:"group,omitempty"`
	IncludeItemIDS []string `json:"includeItemIds,omitempty"`
	Label          *string  `json:"label,omitempty"`
	NameContains   *string  `json:"nameContains,omitempty"`
	Suite          *string  `json:"suite,omitempty"`
}

type TestFrameworkV14 string

const (
	Cpputest    TestFrameworkV14 = "cpputest"
	OpaqueCtest TestFrameworkV14 = "opaque-ctest"
	Unity       TestFrameworkV14 = "unity"
)

type TestSourceProvenanceV14 string

const (
	CtestBacktrace    TestSourceProvenanceV14 = "ctest-backtrace"
	FrameworkManifest TestSourceProvenanceV14 = "framework-manifest"
	FrameworkOutput   TestSourceProvenanceV14 = "framework-output"
	MockActualCall    TestSourceProvenanceV14 = "mock-actual-call"
	MockExpectation   TestSourceProvenanceV14 = "mock-expectation"
	TestDeclaration   TestSourceProvenanceV14 = "test-declaration"
)

type CategoryV14 string

const (
	AssertionFailure       CategoryV14 = "assertion_failure"
	BuildError             CategoryV14 = "build_error"
	CategoryV14Cancelled   CategoryV14 = "cancelled"
	ConfigurationError     CategoryV14 = "configuration_error"
	FrameworkOutputInvalid CategoryV14 = "framework_output_invalid"
	InconsistentExitStatus CategoryV14 = "inconsistent_exit_status"
	InfrastructureError    CategoryV14 = "infrastructure_error"
	TestProcessCrash       CategoryV14 = "test_process_crash"
	TestTimeout            CategoryV14 = "test_timeout"
	UnexpectedExit         CategoryV14 = "unexpected_exit"
)

type DiagnosticSeverityV14 string

const (
	Error   DiagnosticSeverityV14 = "error"
	Info    DiagnosticSeverityV14 = "info"
	Warning DiagnosticSeverityV14 = "warning"
)

type TestItemKindV14 string

const (
	Case  TestItemKindV14 = "case"
	Group TestItemKindV14 = "group"
	Suite TestItemKindV14 = "suite"
)

type TestFailureSubtypeV14 string

const (
	MockFailure           TestFailureSubtypeV14 = "mock_failure"
	MockMissingCall       TestFailureSubtypeV14 = "mock_missing_call"
	MockParameterMismatch TestFailureSubtypeV14 = "mock_parameter_mismatch"
	MockUnexpectedCall    TestFailureSubtypeV14 = "mock_unexpected_call"
)

type TestItemOutcomeV14 string

const (
	NotRun                      TestItemOutcomeV14 = "not_run"
	Skipped                     TestItemOutcomeV14 = "skipped"
	TestItemOutcomeV14Cancelled TestItemOutcomeV14 = "cancelled"
	TestItemOutcomeV14Errored   TestItemOutcomeV14 = "errored"
	TestItemOutcomeV14Failed    TestItemOutcomeV14 = "failed"
	TestItemOutcomeV14Passed    TestItemOutcomeV14 = "passed"
	TestItemOutcomeV14TimedOut  TestItemOutcomeV14 = "timed_out"
)

type TestResultReasonV14 string

const (
	BuildBlocked        TestResultReasonV14 = "build_blocked"
	ContainerTerminated TestResultReasonV14 = "container_terminated"
	Disabled            TestResultReasonV14 = "disabled"
	SelectionAborted    TestResultReasonV14 = "selection_aborted"
	ServiceRestarted    TestResultReasonV14 = "service_restarted"
	StaleCatalog        TestResultReasonV14 = "stale_catalog"
)

type TestRunOutcomeV14 string

const (
	Blocked                    TestRunOutcomeV14 = "blocked"
	Interrupted                TestRunOutcomeV14 = "interrupted"
	TestRunOutcomeV14Cancelled TestRunOutcomeV14 = "cancelled"
	TestRunOutcomeV14Errored   TestRunOutcomeV14 = "errored"
	TestRunOutcomeV14Failed    TestRunOutcomeV14 = "failed"
	TestRunOutcomeV14Passed    TestRunOutcomeV14 = "passed"
	TestRunOutcomeV14TimedOut  TestRunOutcomeV14 = "timed_out"
)

type TestSelectionModeV14 string

const (
	All           TestSelectionModeV14 = "all"
	Containers    TestSelectionModeV14 = "containers"
	FailedFromRun TestSelectionModeV14 = "failedFromRun"
	Filter        TestSelectionModeV14 = "filter"
	Items         TestSelectionModeV14 = "items"
)

type TestRunStatusV14 string

const (
	Completed TestRunStatusV14 = "completed"
	Queued    TestRunStatusV14 = "queued"
	Running   TestRunStatusV14 = "running"
)

type Value struct {
	Bool   *bool
	Double *float64
	String *string
}
