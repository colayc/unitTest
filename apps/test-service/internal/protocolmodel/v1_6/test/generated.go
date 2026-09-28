package protocolmodelv16test

import "time"

type TestContractV16 struct {
	Catalog   TestCatalog      `json:"catalog"`
	Result    TestItemResult   `json:"result"`
	Run       TestRun          `json:"run"`
	RunPage   TestRunPage      `json:"runPage"`
	Selection TestSelectionV16 `json:"selection"`
}

type TestCatalog struct {
	Containers  []TestContainer `json:"containers"`
	Diagnostics []DiagnosticV16 `json:"diagnostics"`
	GeneratedAt time.Time       `json:"generatedAt"`
	Items       []TestItem      `json:"items"`
	NextCursor  *string         `json:"nextCursor,omitempty"`
	Partial     bool            `json:"partial"`
	ProfileID   string          `json:"profileId"`
	ProjectID   string          `json:"projectId"`
	Revision    string          `json:"revision"`
}

type TestContainer struct {
	Capabilities     TestCapabilitiesV16    `json:"capabilities"`
	CtestLogicalName string                 `json:"ctestLogicalName"`
	DegradedReason   *string                `json:"degradedReason,omitempty"`
	Disabled         bool                   `json:"disabled"`
	DisplayName      string                 `json:"displayName"`
	Framework        TestFrameworkV16       `json:"framework"`
	ID               string                 `json:"id"`
	Labels           []string               `json:"labels"`
	ProjectID        string                 `json:"projectId"`
	SourceLocation   *TestSourceLocationV16 `json:"sourceLocation,omitempty"`
}

type TestCapabilitiesV16 struct {
	CanDiscoverCases        bool `json:"canDiscoverCases"`
	CanReportMockDetails    bool `json:"canReportMockDetails"`
	CanReportSkipped        bool `json:"canReportSkipped"`
	CanReportSourceLocation bool `json:"canReportSourceLocation"`
	CanRunCase              bool `json:"canRunCase"`
}

type TestSourceLocationV16 struct {
	Column     *int64                  `json:"column,omitempty"`
	Line       *int64                  `json:"line,omitempty"`
	Navigable  bool                    `json:"navigable"`
	Provenance TestSourceProvenanceV16 `json:"provenance"`
	URI        string                  `json:"uri"`
}

type DiagnosticV16 struct {
	Category  CategoryV16           `json:"category"`
	Code      string                `json:"code"`
	Column    *int64                `json:"column,omitempty"`
	Line      *int64                `json:"line,omitempty"`
	Message   string                `json:"message"`
	Severity  DiagnosticSeverityV16 `json:"severity"`
	SourceURI *string               `json:"sourceUri,omitempty"`
}

type TestItem struct {
	ContainerID    string                 `json:"containerId"`
	Disabled       bool                   `json:"disabled"`
	DisplayName    string                 `json:"displayName"`
	Framework      TestFrameworkV16       `json:"framework"`
	ID             string                 `json:"id"`
	Kind           TestItemKindV16        `json:"kind"`
	Labels         []string               `json:"labels"`
	LogicalName    string                 `json:"logicalName"`
	Parameters     []TestParameterV16     `json:"parameters,omitempty"`
	ParentID       *string                `json:"parentId,omitempty"`
	SourceLocation *TestSourceLocationV16 `json:"sourceLocation,omitempty"`
}

type TestParameterV16 struct {
	Name  string `json:"name"`
	Value *Value `json:"value"`
}

type TestItemResult struct {
	ContainerID    string                 `json:"containerId"`
	DurationMS     *int64                 `json:"durationMs,omitempty"`
	FailureDetails []TestFailureDetailV16 `json:"failureDetails"`
	ItemID         string                 `json:"itemId"`
	Iteration      int64                  `json:"iteration"`
	Outcome        TestItemOutcomeV16     `json:"outcome"`
	OutputRefs     []string               `json:"outputRefs"`
	Partial        bool                   `json:"partial"`
	Reason         *TestResultReasonV16   `json:"reason,omitempty"`
	SourceLocation *TestSourceLocationV16 `json:"sourceLocation,omitempty"`
}

type TestFailureDetailV16 struct {
	Actual       *string                 `json:"actual,omitempty"`
	Category     CategoryV16             `json:"category"`
	EvidenceRefs []string                `json:"evidenceRefs"`
	Expected     *string                 `json:"expected,omitempty"`
	Locations    []TestSourceLocationV16 `json:"locations"`
	Message      string                  `json:"message"`
	Subtype      *TestFailureSubtypeV16  `json:"subtype,omitempty"`
}

type TestRun struct {
	CatalogRevision   string                   `json:"catalogRevision"`
	FinishedAt        *time.Time               `json:"finishedAt,omitempty"`
	Incomplete        bool                     `json:"incomplete"`
	Outcome           *TestRunOutcomeV16       `json:"outcome,omitempty"`
	ProfileID         string                   `json:"profileId"`
	ProjectID         string                   `json:"projectId"`
	ResultRevision    string                   `json:"resultRevision"`
	RunID             string                   `json:"runId"`
	SelectionSnapshot TestSelectionSnapshotV16 `json:"selectionSnapshot"`
	StartedAt         *time.Time               `json:"startedAt,omitempty"`
	Status            TestRunStatusV16         `json:"status"`
	Summary           TestRunSummaryV16        `json:"summary"`
	TaskID            string                   `json:"taskId"`
	ToolchainID       string                   `json:"toolchainId"`
}

type TestSelectionSnapshotV16 struct {
	ContainerIDS []string             `json:"containerIds"`
	ItemIDS      []string             `json:"itemIds"`
	Mode         TestSelectionModeV16 `json:"mode"`
}

type TestRunSummaryV16 struct {
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

type TestSelectionV16 interface{ isTestSelectionV16() }
type AllTestSelectionV16 struct {
	Mode TestSelectionModeV16 `json:"mode"`
}

func (AllTestSelectionV16) isTestSelectionV16() {}

type ContainersTestSelectionV16 struct {
	Mode         TestSelectionModeV16 `json:"mode"`
	ContainerIDs []string             `json:"containerIds"`
}

func (ContainersTestSelectionV16) isTestSelectionV16() {}

type ItemsTestSelectionV16 struct {
	Mode    TestSelectionModeV16 `json:"mode"`
	ItemIDs []string             `json:"itemIds"`
}

func (ItemsTestSelectionV16) isTestSelectionV16() {}

type FilterTestSelectionV16 struct {
	Mode   TestSelectionModeV16 `json:"mode"`
	Filter TestFilterV16        `json:"filter"`
}

func (FilterTestSelectionV16) isTestSelectionV16() {}

type FailedFromRunTestSelectionV16 struct {
	Mode  TestSelectionModeV16 `json:"mode"`
	RunID string               `json:"runId"`
}

func (FailedFromRunTestSelectionV16) isTestSelectionV16() {}

type TestFilterV16 struct {
	ExcludeItemIDS []string `json:"excludeItemIds,omitempty"`
	Group          *string  `json:"group,omitempty"`
	IncludeItemIDS []string `json:"includeItemIds,omitempty"`
	Label          *string  `json:"label,omitempty"`
	NameContains   *string  `json:"nameContains,omitempty"`
	Suite          *string  `json:"suite,omitempty"`
}

type TestFrameworkV16 string

const (
	Cpputest    TestFrameworkV16 = "cpputest"
	OpaqueCtest TestFrameworkV16 = "opaque-ctest"
	Unity       TestFrameworkV16 = "unity"
)

type TestSourceProvenanceV16 string

const (
	CtestBacktrace    TestSourceProvenanceV16 = "ctest-backtrace"
	FrameworkManifest TestSourceProvenanceV16 = "framework-manifest"
	FrameworkOutput   TestSourceProvenanceV16 = "framework-output"
	MockActualCall    TestSourceProvenanceV16 = "mock-actual-call"
	MockExpectation   TestSourceProvenanceV16 = "mock-expectation"
	TestDeclaration   TestSourceProvenanceV16 = "test-declaration"
)

type CategoryV16 string

const (
	AssertionFailure       CategoryV16 = "assertion_failure"
	BuildError             CategoryV16 = "build_error"
	CategoryV16Cancelled   CategoryV16 = "cancelled"
	ConfigurationError     CategoryV16 = "configuration_error"
	FrameworkOutputInvalid CategoryV16 = "framework_output_invalid"
	InconsistentExitStatus CategoryV16 = "inconsistent_exit_status"
	InfrastructureError    CategoryV16 = "infrastructure_error"
	TestProcessCrash       CategoryV16 = "test_process_crash"
	TestTimeout            CategoryV16 = "test_timeout"
	UnexpectedExit         CategoryV16 = "unexpected_exit"
)

type DiagnosticSeverityV16 string

const (
	Error   DiagnosticSeverityV16 = "error"
	Info    DiagnosticSeverityV16 = "info"
	Warning DiagnosticSeverityV16 = "warning"
)

type TestItemKindV16 string

const (
	Case  TestItemKindV16 = "case"
	Group TestItemKindV16 = "group"
	Suite TestItemKindV16 = "suite"
)

type TestFailureSubtypeV16 string

const (
	MockFailure           TestFailureSubtypeV16 = "mock_failure"
	MockMissingCall       TestFailureSubtypeV16 = "mock_missing_call"
	MockParameterMismatch TestFailureSubtypeV16 = "mock_parameter_mismatch"
	MockUnexpectedCall    TestFailureSubtypeV16 = "mock_unexpected_call"
)

type TestItemOutcomeV16 string

const (
	NotRun                      TestItemOutcomeV16 = "not_run"
	Skipped                     TestItemOutcomeV16 = "skipped"
	TestItemOutcomeV16Cancelled TestItemOutcomeV16 = "cancelled"
	TestItemOutcomeV16Errored   TestItemOutcomeV16 = "errored"
	TestItemOutcomeV16Failed    TestItemOutcomeV16 = "failed"
	TestItemOutcomeV16Passed    TestItemOutcomeV16 = "passed"
	TestItemOutcomeV16TimedOut  TestItemOutcomeV16 = "timed_out"
)

type TestResultReasonV16 string

const (
	BuildBlocked        TestResultReasonV16 = "build_blocked"
	ContainerTerminated TestResultReasonV16 = "container_terminated"
	Disabled            TestResultReasonV16 = "disabled"
	SelectionAborted    TestResultReasonV16 = "selection_aborted"
	ServiceRestarted    TestResultReasonV16 = "service_restarted"
	StaleCatalog        TestResultReasonV16 = "stale_catalog"
)

type TestRunOutcomeV16 string

const (
	Blocked                    TestRunOutcomeV16 = "blocked"
	Interrupted                TestRunOutcomeV16 = "interrupted"
	TestRunOutcomeV16Cancelled TestRunOutcomeV16 = "cancelled"
	TestRunOutcomeV16Errored   TestRunOutcomeV16 = "errored"
	TestRunOutcomeV16Failed    TestRunOutcomeV16 = "failed"
	TestRunOutcomeV16Passed    TestRunOutcomeV16 = "passed"
	TestRunOutcomeV16TimedOut  TestRunOutcomeV16 = "timed_out"
)

type TestSelectionModeV16 string

const (
	All           TestSelectionModeV16 = "all"
	Containers    TestSelectionModeV16 = "containers"
	FailedFromRun TestSelectionModeV16 = "failedFromRun"
	Filter        TestSelectionModeV16 = "filter"
	Items         TestSelectionModeV16 = "items"
)

type TestRunStatusV16 string

const (
	Completed TestRunStatusV16 = "completed"
	Queued    TestRunStatusV16 = "queued"
	Running   TestRunStatusV16 = "running"
)

type Value struct {
	Bool   *bool
	Double *float64
	String *string
}
