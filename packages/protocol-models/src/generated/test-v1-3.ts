export interface TestContractV13 {
    catalog:   TestCatalog;
    result:    TestItemResult;
    run:       TestRun;
    runPage:   TestRunPage;
    selection: TestSelection;
}

export interface TestCatalog {
    containers:  TestContainer[];
    diagnostics: DiagnosticV13[];
    generatedAt: Date;
    items:       TestItem[];
    nextCursor?: string;
    partial:     boolean;
    profileId:   string;
    projectId:   string;
    revision:    string;
}

export interface TestContainer {
    capabilities:     TestCapabilitiesV13;
    ctestLogicalName: string;
    degradedReason?:  string;
    disabled:         boolean;
    displayName:      string;
    framework:        TestFrameworkV13;
    id:               string;
    labels:           string[];
    projectId:        string;
    sourceLocation?:  TestSourceLocationV13;
}

export interface TestCapabilitiesV13 {
    canDiscoverCases:        boolean;
    canReportMockDetails:    boolean;
    canReportSkipped:        boolean;
    canReportSourceLocation: boolean;
    canRunCase:              boolean;
}

export enum TestFrameworkV13 {
    Cpputest = "cpputest",
    OpaqueCtest = "opaque-ctest",
    Unity = "unity",
}

export interface TestSourceLocationV13 {
    column?:    number;
    line?:      number;
    navigable:  boolean;
    provenance: TestSourceProvenanceV13;
    uri:        string;
}

export enum TestSourceProvenanceV13 {
    CtestBacktrace = "ctest-backtrace",
    FrameworkManifest = "framework-manifest",
    FrameworkOutput = "framework-output",
    MockActualCall = "mock-actual-call",
    MockExpectation = "mock-expectation",
    TestDeclaration = "test-declaration",
}

export interface DiagnosticV13 {
    category:   CategoryV13;
    code:       string;
    column?:    number;
    line?:      number;
    message:    string;
    severity:   DiagnosticSeverityV13;
    sourceUri?: string;
}

export enum CategoryV13 {
    AssertionFailure = "assertion_failure",
    BuildError = "build_error",
    Cancelled = "cancelled",
    ConfigurationError = "configuration_error",
    FrameworkOutputInvalid = "framework_output_invalid",
    InconsistentExitStatus = "inconsistent_exit_status",
    InfrastructureError = "infrastructure_error",
    TestProcessCrash = "test_process_crash",
    TestTimeout = "test_timeout",
    UnexpectedExit = "unexpected_exit",
}

export enum DiagnosticSeverityV13 {
    Error = "error",
    Info = "info",
    Warning = "warning",
}

export interface TestItem {
    containerId:     string;
    disabled:        boolean;
    displayName:     string;
    framework:       TestFrameworkV13;
    id:              string;
    kind:            TestItemKindV13;
    labels:          string[];
    logicalName:     string;
    parameters?:     TestParameterV13[];
    parentId?:       string;
    sourceLocation?: TestSourceLocationV13;
}

export enum TestItemKindV13 {
    Case = "case",
    Group = "group",
    Suite = "suite",
}

export interface TestParameterV13 {
    name:  string;
    value: boolean | number | null | string;
}

export interface TestItemResult {
    containerId:     string;
    durationMs?:     number;
    failureDetails:  TestFailureDetailV13[];
    itemId:          string;
    iteration:       number;
    outcome:         TestItemOutcomeV13;
    outputRefs:      string[];
    partial:         boolean;
    reason?:         TestResultReasonV13;
    sourceLocation?: TestSourceLocationV13;
}

export interface TestFailureDetailV13 {
    actual?:      string;
    category:     CategoryV13;
    evidenceRefs: string[];
    expected?:    string;
    locations:    TestSourceLocationV13[];
    message:      string;
    subtype?:     TestFailureSubtypeV13;
}

export enum TestFailureSubtypeV13 {
    MockFailure = "mock_failure",
    MockMissingCall = "mock_missing_call",
    MockParameterMismatch = "mock_parameter_mismatch",
    MockUnexpectedCall = "mock_unexpected_call",
}

export enum TestItemOutcomeV13 {
    Cancelled = "cancelled",
    Errored = "errored",
    Failed = "failed",
    NotRun = "not_run",
    Passed = "passed",
    Skipped = "skipped",
    TimedOut = "timed_out",
}

export enum TestResultReasonV13 {
    BuildBlocked = "build_blocked",
    ContainerTerminated = "container_terminated",
    Disabled = "disabled",
    SelectionAborted = "selection_aborted",
    ServiceRestarted = "service_restarted",
    StaleCatalog = "stale_catalog",
}

export interface TestRun {
    catalogRevision:   string;
    finishedAt?:       Date;
    incomplete:        boolean;
    outcome?:          TestRunOutcomeV13;
    profileId:         string;
    projectId:         string;
    resultRevision:    string;
    runId:             string;
    selectionSnapshot: TestSelectionSnapshotV13;
    startedAt?:        Date;
    status:            TestRunStatusV13;
    summary:           TestRunSummaryV13;
    taskId:            string;
    toolchainId:       string;
}

export enum TestRunOutcomeV13 {
    Blocked = "blocked",
    Cancelled = "cancelled",
    Errored = "errored",
    Failed = "failed",
    Interrupted = "interrupted",
    Passed = "passed",
    TimedOut = "timed_out",
}

export interface TestSelectionSnapshotV13 {
    containerIds: string[];
    itemIds:      string[];
    mode:         TestSelectionModeV13;
}

export enum TestSelectionModeV13 {
    All = "all",
    Containers = "containers",
    FailedFromRun = "failedFromRun",
    Filter = "filter",
    Items = "items",
}

export enum TestRunStatusV13 {
    Completed = "completed",
    Queued = "queued",
    Running = "running",
}

export interface TestRunSummaryV13 {
    cancelled:  number;
    completed:  number;
    errored:    number;
    failed:     number;
    iterations: number;
    notRun:     number;
    passed:     number;
    skipped:    number;
    timedOut:   number;
    total:      number;
}

export interface TestRunPage {
    items:       TestRun[];
    nextCursor?: string;
}

export interface AllTestSelectionV13 { mode: TestSelectionModeV13.All; containerIds?: never; itemIds?: never; filter?: never; runId?: never; }
export interface ContainersTestSelectionV13 { mode: TestSelectionModeV13.Containers; containerIds: string[]; itemIds?: never; filter?: never; runId?: never; }
export interface ItemsTestSelectionV13 { mode: TestSelectionModeV13.Items; itemIds: string[]; containerIds?: never; filter?: never; runId?: never; }
export interface FilterTestSelectionV13 { mode: TestSelectionModeV13.Filter; filter: TestFilterV13; containerIds?: never; itemIds?: never; runId?: never; }
export interface FailedFromRunTestSelectionV13 { mode: TestSelectionModeV13.FailedFromRun; runId: string; containerIds?: never; itemIds?: never; filter?: never; }
export type TestSelection = AllTestSelectionV13 | ContainersTestSelectionV13 | ItemsTestSelectionV13 | FilterTestSelectionV13 | FailedFromRunTestSelectionV13;

export interface TestFilterV13 {
    excludeItemIds?: string[];
    group?:          string;
    includeItemIds?: string[];
    label?:          string;
    nameContains?:   string;
    suite?:          string;
}
