package protocolmodelv13test

import "time"

type TestContractV13 struct {
	Catalog   TestCatalog    `json:"catalog"`
	Result    TestItemResult `json:"result"`
	Run       TestRun        `json:"run"`
	RunPage   TestRunPage    `json:"runPage"`
	Selection TestSelection  `json:"selection"`
}

type TestCatalog struct {
	Containers  []TestContainer `json:"containers"`
	Diagnostics []DiagnosticV13 `json:"diagnostics"`
	GeneratedAt time.Time       `json:"generatedAt"`
	Items       []TestItem      `json:"items"`
	NextCursor  *string         `json:"nextCursor,omitempty"`
	Partial     bool            `json:"partial"`
	ProfileID   string          `json:"profileId"`
	ProjectID   string          `json:"projectId"`
	Revision    string          `json:"revision"`
}

type TestContainer struct {
	Capabilities     TestCapabilitiesV13    `json:"capabilities"`
	CtestLogicalName string                 `json:"ctestLogicalName"`
	DegradedReason   *string                `json:"degradedReason,omitempty"`
	Disabled         bool                   `json:"disabled"`
	DisplayName      string                 `json:"displayName"`
	Framework        TestFrameworkV13       `json:"framework"`
	ID               string                 `json:"id"`
	Labels           []string               `json:"labels"`
	ProjectID        string                 `json:"projectId"`
	SourceLocation   *TestSourceLocationV13 `json:"sourceLocation,omitempty"`
}

type TestCapabilitiesV13 struct {
	CanDiscoverCases        bool `json:"canDiscoverCases"`
	CanReportMockDetails    bool `json:"canReportMockDetails"`
	CanReportSkipped        bool `json:"canReportSkipped"`
	CanReportSourceLocation bool `json:"canReportSourceLocation"`
	CanRunCase              bool `json:"canRunCase"`
}

type TestSourceLocationV13 struct {
	Column     *int64                  `json:"column,omitempty"`
	Line       *int64                  `json:"line,omitempty"`
	Navigable  bool                    `json:"navigable"`
	Provenance TestSourceProvenanceV13 `json:"provenance"`
	URI        string                  `json:"uri"`
}

type DiagnosticV13 struct {
	Category  CategoryV13           `json:"category"`
	Code      string                `json:"code"`
	Column    *int64                `json:"column,omitempty"`
	Line      *int64                `json:"line,omitempty"`
	Message   string                `json:"message"`
	Severity  DiagnosticSeverityV13 `json:"severity"`
	SourceURI *string               `json:"sourceUri,omitempty"`
}

type TestItem struct {
	ContainerID    string                 `json:"containerId"`
	Disabled       bool                   `json:"disabled"`
	DisplayName    string                 `json:"displayName"`
	Framework      TestFrameworkV13       `json:"framework"`
	ID             string                 `json:"id"`
	Kind           TestItemKindV13        `json:"kind"`
	Labels         []string               `json:"labels"`
	LogicalName    string                 `json:"logicalName"`
	Parameters     []TestParameterV13     `json:"parameters,omitempty"`
	ParentID       *string                `json:"parentId,omitempty"`
	SourceLocation *TestSourceLocationV13 `json:"sourceLocation,omitempty"`
}

type TestParameterV13 struct {
	Name  string `json:"name"`
	Value *Value `json:"value"`
}

type TestItemResult struct {
	ContainerID    string                 `json:"containerId"`
	DurationMS     *int64                 `json:"durationMs,omitempty"`
	FailureDetails []TestFailureDetailV13 `json:"failureDetails"`
	ItemID         string                 `json:"itemId"`
	Iteration      int64                  `json:"iteration"`
	Outcome        TestItemOutcomeV13     `json:"outcome"`
	OutputRefs     []string               `json:"outputRefs"`
	Partial        bool                   `json:"partial"`
	Reason         *TestResultReasonV13   `json:"reason,omitempty"`
	SourceLocation *TestSourceLocationV13 `json:"sourceLocation,omitempty"`
}

type TestFailureDetailV13 struct {
	Actual       *string                 `json:"actual,omitempty"`
	Category     CategoryV13             `json:"category"`
	EvidenceRefs []string                `json:"evidenceRefs"`
	Expected     *string                 `json:"expected,omitempty"`
	Locations    []TestSourceLocationV13 `json:"locations"`
	Message      string                  `json:"message"`
}

type TestRun struct {
	CatalogRevision   string                   `json:"catalogRevision"`
	FinishedAt        *time.Time               `json:"finishedAt,omitempty"`
	Incomplete        bool                     `json:"incomplete"`
	Outcome           TestRunOutcomeV13        `json:"outcome"`
	ProfileID         string                   `json:"profileId"`
	ProjectID         string                   `json:"projectId"`
	ResultRevision    string                   `json:"resultRevision"`
	RunID             string                   `json:"runId"`
	SelectionSnapshot TestSelectionSnapshotV13 `json:"selectionSnapshot"`
	StartedAt         *time.Time               `json:"startedAt,omitempty"`
	Status            TestRunStatusV13         `json:"status"`
	Summary           TestRunSummaryV13        `json:"summary"`
	TaskID            string                   `json:"taskId"`
	ToolchainID       string                   `json:"toolchainId"`
}

type TestSelectionSnapshotV13 struct {
	ContainerIDS []string             `json:"containerIds"`
	ItemIDS      []string             `json:"itemIds"`
	Mode         TestSelectionModeV13 `json:"mode"`
}

type TestRunSummaryV13 struct {
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

type TestSelection interface{ isTestSelection() }
type AllTestSelectionV13 struct {
	Mode TestSelectionModeV13 `json:"mode"`
}

func (AllTestSelectionV13) isTestSelection() {}

type ContainersTestSelectionV13 struct {
	Mode         TestSelectionModeV13 `json:"mode"`
	ContainerIDs []string             `json:"containerIds"`
}

func (ContainersTestSelectionV13) isTestSelection() {}

type ItemsTestSelectionV13 struct {
	Mode    TestSelectionModeV13 `json:"mode"`
	ItemIDs []string             `json:"itemIds"`
}

func (ItemsTestSelectionV13) isTestSelection() {}

type FilterTestSelectionV13 struct {
	Mode   TestSelectionModeV13 `json:"mode"`
	Filter TestFilterV13        `json:"filter"`
}

func (FilterTestSelectionV13) isTestSelection() {}

type FailedFromRunTestSelectionV13 struct {
	Mode  TestSelectionModeV13 `json:"mode"`
	RunID string               `json:"runId"`
}

func (FailedFromRunTestSelectionV13) isTestSelection() {}

type TestFilterV13 struct {
	ExcludeItemIDS []string `json:"excludeItemIds,omitempty"`
	Group          *string  `json:"group,omitempty"`
	IncludeItemIDS []string `json:"includeItemIds,omitempty"`
	Label          *string  `json:"label,omitempty"`
	NameContains   *string  `json:"nameContains,omitempty"`
	Suite          *string  `json:"suite,omitempty"`
}

type TestFrameworkV13 string

const (
	Cpputest    TestFrameworkV13 = "cpputest"
	OpaqueCtest TestFrameworkV13 = "opaque-ctest"
	Unity       TestFrameworkV13 = "unity"
)

type TestSourceProvenanceV13 string

const (
	CtestBacktrace    TestSourceProvenanceV13 = "ctest-backtrace"
	FrameworkManifest TestSourceProvenanceV13 = "framework-manifest"
	FrameworkOutput   TestSourceProvenanceV13 = "framework-output"
	MockActualCall    TestSourceProvenanceV13 = "mock-actual-call"
	MockExpectation   TestSourceProvenanceV13 = "mock-expectation"
	TestDeclaration   TestSourceProvenanceV13 = "test-declaration"
)

type CategoryV13 string

const (
	AssertionFailure       CategoryV13 = "assertion_failure"
	BuildError             CategoryV13 = "build_error"
	CategoryV13Cancelled   CategoryV13 = "cancelled"
	ConfigurationError     CategoryV13 = "configuration_error"
	FrameworkOutputInvalid CategoryV13 = "framework_output_invalid"
	InconsistentExitStatus CategoryV13 = "inconsistent_exit_status"
	InfrastructureError    CategoryV13 = "infrastructure_error"
	TestProcessCrash       CategoryV13 = "test_process_crash"
	TestTimeout            CategoryV13 = "test_timeout"
	UnexpectedExit         CategoryV13 = "unexpected_exit"
)

type DiagnosticSeverityV13 string

const (
	Error   DiagnosticSeverityV13 = "error"
	Info    DiagnosticSeverityV13 = "info"
	Warning DiagnosticSeverityV13 = "warning"
)

type TestItemKindV13 string

const (
	Case  TestItemKindV13 = "case"
	Group TestItemKindV13 = "group"
	Suite TestItemKindV13 = "suite"
)

type TestItemOutcomeV13 string

const (
	NotRun                      TestItemOutcomeV13 = "not_run"
	Skipped                     TestItemOutcomeV13 = "skipped"
	TestItemOutcomeV13Cancelled TestItemOutcomeV13 = "cancelled"
	TestItemOutcomeV13Errored   TestItemOutcomeV13 = "errored"
	TestItemOutcomeV13Failed    TestItemOutcomeV13 = "failed"
	TestItemOutcomeV13Passed    TestItemOutcomeV13 = "passed"
	TestItemOutcomeV13TimedOut  TestItemOutcomeV13 = "timed_out"
)

type TestResultReasonV13 string

const (
	BuildBlocked        TestResultReasonV13 = "build_blocked"
	ContainerTerminated TestResultReasonV13 = "container_terminated"
	Disabled            TestResultReasonV13 = "disabled"
	SelectionAborted    TestResultReasonV13 = "selection_aborted"
	ServiceRestarted    TestResultReasonV13 = "service_restarted"
	StaleCatalog        TestResultReasonV13 = "stale_catalog"
)

type TestRunOutcomeV13 string

const (
	Blocked                    TestRunOutcomeV13 = "blocked"
	Interrupted                TestRunOutcomeV13 = "interrupted"
	TestRunOutcomeV13Cancelled TestRunOutcomeV13 = "cancelled"
	TestRunOutcomeV13Errored   TestRunOutcomeV13 = "errored"
	TestRunOutcomeV13Failed    TestRunOutcomeV13 = "failed"
	TestRunOutcomeV13Passed    TestRunOutcomeV13 = "passed"
	TestRunOutcomeV13TimedOut  TestRunOutcomeV13 = "timed_out"
)

type TestSelectionModeV13 string

const (
	All           TestSelectionModeV13 = "all"
	Containers    TestSelectionModeV13 = "containers"
	FailedFromRun TestSelectionModeV13 = "failedFromRun"
	Filter        TestSelectionModeV13 = "filter"
	Items         TestSelectionModeV13 = "items"
)

type TestRunStatusV13 string

const (
	Completed TestRunStatusV13 = "completed"
	Queued    TestRunStatusV13 = "queued"
	Running   TestRunStatusV13 = "running"
)

type Value struct {
	Bool   *bool
	Double *float64
	String *string
}
