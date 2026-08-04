export interface TestContractV14 {
    catalog:   TestCatalog;
    result:    TestItemResult;
    run:       TestRun;
    runPage:   TestRunPage;
    selection: TestSelectionV14;
}

export interface TestCatalog {
    containers:  TestContainer[];
    diagnostics: DiagnosticV14[];
    generatedAt: Date;
    items:       TestItem[];
    nextCursor?: string;
    partial:     boolean;
    profileId:   string;
    projectId:   string;
    revision:    string;
}

export interface TestContainer {
    capabilities:     TestCapabilitiesV14;
    ctestLogicalName: string;
    degradedReason?:  string;
    disabled:         boolean;
    displayName:      string;
    framework:        TestFrameworkV14;
    id:               string;
    labels:           string[];
    projectId:        string;
    sourceLocation?:  TestSourceLocationV14;
}

export interface TestCapabilitiesV14 {
    canDiscoverCases:        boolean;
    canReportMockDetails:    boolean;
    canReportSkipped:        boolean;
    canReportSourceLocation: boolean;
    canRunCase:              boolean;
}

export enum TestFrameworkV14 {
    Cpputest = "cpputest",
    OpaqueCtest = "opaque-ctest",
    Unity = "unity",
}

export interface TestSourceLocationV14 {
    column?:    number;
    line?:      number;
    navigable:  boolean;
    provenance: TestSourceProvenanceV14;
    uri:        string;
}

export enum TestSourceProvenanceV14 {
    CtestBacktrace = "ctest-backtrace",
    FrameworkManifest = "framework-manifest",
    FrameworkOutput = "framework-output",
    MockActualCall = "mock-actual-call",
    MockExpectation = "mock-expectation",
    TestDeclaration = "test-declaration",
}

export interface DiagnosticV14 {
    category:   CategoryV14;
    code:       string;
    column?:    number;
    line?:      number;
    message:    string;
    severity:   DiagnosticSeverityV14;
    sourceUri?: string;
}

export enum CategoryV14 {
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

export enum DiagnosticSeverityV14 {
    Error = "error",
    Info = "info",
    Warning = "warning",
}

export interface TestItem {
    containerId:     string;
    disabled:        boolean;
    displayName:     string;
    framework:       TestFrameworkV14;
    id:              string;
    kind:            TestItemKindV14;
    labels:          string[];
    logicalName:     string;
    parameters?:     TestParameterV14[];
    parentId?:       string;
    sourceLocation?: TestSourceLocationV14;
}

export enum TestItemKindV14 {
    Case = "case",
    Group = "group",
    Suite = "suite",
}

export interface TestParameterV14 {
    name:  string;
    value: boolean | number | null | string;
}

export interface TestItemResult {
    containerId:     string;
    durationMs?:     number;
    failureDetails:  TestFailureDetailV14[];
    itemId:          string;
    iteration:       number;
    outcome:         TestItemOutcomeV14;
    outputRefs:      string[];
    partial:         boolean;
    reason?:         TestResultReasonV14;
    sourceLocation?: TestSourceLocationV14;
}

export interface TestFailureDetailV14 {
    actual?:      string;
    category:     CategoryV14;
    evidenceRefs: string[];
    expected?:    string;
    locations:    TestSourceLocationV14[];
    message:      string;
    subtype?:     TestFailureSubtypeV14;
}

export enum TestFailureSubtypeV14 {
    MockFailure = "mock_failure",
    MockMissingCall = "mock_missing_call",
    MockParameterMismatch = "mock_parameter_mismatch",
    MockUnexpectedCall = "mock_unexpected_call",
}

export enum TestItemOutcomeV14 {
    Cancelled = "cancelled",
    Errored = "errored",
    Failed = "failed",
    NotRun = "not_run",
    Passed = "passed",
    Skipped = "skipped",
    TimedOut = "timed_out",
}

export enum TestResultReasonV14 {
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
    outcome?:          TestRunOutcomeV14;
    profileId:         string;
    projectId:         string;
    resultRevision:    string;
    runId:             string;
    selectionSnapshot: TestSelectionSnapshotV14;
    startedAt?:        Date;
    status:            TestRunStatusV14;
    summary:           TestRunSummaryV14;
    taskId:            string;
    toolchainId:       string;
}

export enum TestRunOutcomeV14 {
    Blocked = "blocked",
    Cancelled = "cancelled",
    Errored = "errored",
    Failed = "failed",
    Interrupted = "interrupted",
    Passed = "passed",
    TimedOut = "timed_out",
}

export interface TestSelectionSnapshotV14 {
    containerIds: string[];
    itemIds:      string[];
    mode:         TestSelectionModeV14;
}

export enum TestSelectionModeV14 {
    All = "all",
    Containers = "containers",
    FailedFromRun = "failedFromRun",
    Filter = "filter",
    Items = "items",
}

export enum TestRunStatusV14 {
    Completed = "completed",
    Queued = "queued",
    Running = "running",
}

export interface TestRunSummaryV14 {
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

export interface AllTestSelectionV14 { mode: TestSelectionModeV14.All; containerIds?: never; itemIds?: never; filter?: never; runId?: never; }
export interface ContainersTestSelectionV14 { mode: TestSelectionModeV14.Containers; containerIds: string[]; itemIds?: never; filter?: never; runId?: never; }
export interface ItemsTestSelectionV14 { mode: TestSelectionModeV14.Items; itemIds: string[]; containerIds?: never; filter?: never; runId?: never; }
export interface FilterTestSelectionV14 { mode: TestSelectionModeV14.Filter; filter: TestFilterV14; containerIds?: never; itemIds?: never; runId?: never; }
export interface FailedFromRunTestSelectionV14 { mode: TestSelectionModeV14.FailedFromRun; runId: string; containerIds?: never; itemIds?: never; filter?: never; }
export type TestSelectionV14 = AllTestSelectionV14 | ContainersTestSelectionV14 | ItemsTestSelectionV14 | FilterTestSelectionV14 | FailedFromRunTestSelectionV14;

export interface TestFilterV14 {
    excludeItemIds?: string[];
    group?:          string;
    includeItemIds?: string[];
    label?:          string;
    nameContains?:   string;
    suite?:          string;
}
