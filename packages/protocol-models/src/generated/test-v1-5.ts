export interface TestContractV15 {
    catalog:   TestCatalog;
    result:    TestItemResult;
    run:       TestRun;
    runPage:   TestRunPage;
    selection: TestSelectionV15;
}

export interface TestCatalog {
    containers:  TestContainer[];
    diagnostics: DiagnosticV15[];
    generatedAt: Date;
    items:       TestItem[];
    nextCursor?: string;
    partial:     boolean;
    profileId:   string;
    projectId:   string;
    revision:    string;
}

export interface TestContainer {
    capabilities:     TestCapabilitiesV15;
    ctestLogicalName: string;
    degradedReason?:  string;
    disabled:         boolean;
    displayName:      string;
    framework:        TestFrameworkV15;
    id:               string;
    labels:           string[];
    projectId:        string;
    sourceLocation?:  TestSourceLocationV15;
}

export interface TestCapabilitiesV15 {
    canDiscoverCases:        boolean;
    canReportMockDetails:    boolean;
    canReportSkipped:        boolean;
    canReportSourceLocation: boolean;
    canRunCase:              boolean;
}

export enum TestFrameworkV15 {
    Cpputest = "cpputest",
    OpaqueCtest = "opaque-ctest",
    Unity = "unity",
}

export interface TestSourceLocationV15 {
    column?:    number;
    line?:      number;
    navigable:  boolean;
    provenance: TestSourceProvenanceV15;
    uri:        string;
}

export enum TestSourceProvenanceV15 {
    CtestBacktrace = "ctest-backtrace",
    FrameworkManifest = "framework-manifest",
    FrameworkOutput = "framework-output",
    MockActualCall = "mock-actual-call",
    MockExpectation = "mock-expectation",
    TestDeclaration = "test-declaration",
}

export interface DiagnosticV15 {
    category:   CategoryV15;
    code:       string;
    column?:    number;
    line?:      number;
    message:    string;
    severity:   DiagnosticSeverityV15;
    sourceUri?: string;
}

export enum CategoryV15 {
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

export enum DiagnosticSeverityV15 {
    Error = "error",
    Info = "info",
    Warning = "warning",
}

export interface TestItem {
    containerId:     string;
    disabled:        boolean;
    displayName:     string;
    framework:       TestFrameworkV15;
    id:              string;
    kind:            TestItemKindV15;
    labels:          string[];
    logicalName:     string;
    parameters?:     TestParameterV15[];
    parentId?:       string;
    sourceLocation?: TestSourceLocationV15;
}

export enum TestItemKindV15 {
    Case = "case",
    Group = "group",
    Suite = "suite",
}

export interface TestParameterV15 {
    name:  string;
    value: boolean | number | null | string;
}

export interface TestItemResult {
    containerId:     string;
    durationMs?:     number;
    failureDetails:  TestFailureDetailV15[];
    itemId:          string;
    iteration:       number;
    outcome:         TestItemOutcomeV15;
    outputRefs:      string[];
    partial:         boolean;
    reason?:         TestResultReasonV15;
    sourceLocation?: TestSourceLocationV15;
}

export interface TestFailureDetailV15 {
    actual?:      string;
    category:     CategoryV15;
    evidenceRefs: string[];
    expected?:    string;
    locations:    TestSourceLocationV15[];
    message:      string;
    subtype?:     TestFailureSubtypeV15;
}

export enum TestFailureSubtypeV15 {
    MockFailure = "mock_failure",
    MockMissingCall = "mock_missing_call",
    MockParameterMismatch = "mock_parameter_mismatch",
    MockUnexpectedCall = "mock_unexpected_call",
}

export enum TestItemOutcomeV15 {
    Cancelled = "cancelled",
    Errored = "errored",
    Failed = "failed",
    NotRun = "not_run",
    Passed = "passed",
    Skipped = "skipped",
    TimedOut = "timed_out",
}

export enum TestResultReasonV15 {
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
    outcome?:          TestRunOutcomeV15;
    profileId:         string;
    projectId:         string;
    resultRevision:    string;
    runId:             string;
    selectionSnapshot: TestSelectionSnapshotV15;
    startedAt?:        Date;
    status:            TestRunStatusV15;
    summary:           TestRunSummaryV15;
    taskId:            string;
    toolchainId:       string;
}

export enum TestRunOutcomeV15 {
    Blocked = "blocked",
    Cancelled = "cancelled",
    Errored = "errored",
    Failed = "failed",
    Interrupted = "interrupted",
    Passed = "passed",
    TimedOut = "timed_out",
}

export interface TestSelectionSnapshotV15 {
    containerIds: string[];
    itemIds:      string[];
    mode:         TestSelectionModeV15;
}

export enum TestSelectionModeV15 {
    All = "all",
    Containers = "containers",
    FailedFromRun = "failedFromRun",
    Filter = "filter",
    Items = "items",
}

export enum TestRunStatusV15 {
    Completed = "completed",
    Queued = "queued",
    Running = "running",
}

export interface TestRunSummaryV15 {
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

export interface AllTestSelectionV15 { mode: TestSelectionModeV15.All; containerIds?: never; itemIds?: never; filter?: never; runId?: never; }
export interface ContainersTestSelectionV15 { mode: TestSelectionModeV15.Containers; containerIds: string[]; itemIds?: never; filter?: never; runId?: never; }
export interface ItemsTestSelectionV15 { mode: TestSelectionModeV15.Items; itemIds: string[]; containerIds?: never; filter?: never; runId?: never; }
export interface FilterTestSelectionV15 { mode: TestSelectionModeV15.Filter; filter: TestFilterV15; containerIds?: never; itemIds?: never; runId?: never; }
export interface FailedFromRunTestSelectionV15 { mode: TestSelectionModeV15.FailedFromRun; runId: string; containerIds?: never; itemIds?: never; filter?: never; }
export type TestSelectionV15 = AllTestSelectionV15 | ContainersTestSelectionV15 | ItemsTestSelectionV15 | FilterTestSelectionV15 | FailedFromRunTestSelectionV15;

export interface TestFilterV15 {
    excludeItemIds?: string[];
    group?:          string;
    includeItemIds?: string[];
    label?:          string;
    nameContains?:   string;
    suite?:          string;
}
