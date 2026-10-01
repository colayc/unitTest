export interface TestContractV16 {
    catalog:   TestCatalog;
    result:    TestItemResult;
    run:       TestRun;
    runPage:   TestRunPage;
    selection: TestSelectionV16;
}

export interface TestCatalog {
    containers:  TestContainer[];
    diagnostics: DiagnosticV16[];
    generatedAt: Date;
    items:       TestItem[];
    nextCursor?: string;
    partial:     boolean;
    profileId:   string;
    projectId:   string;
    revision:    string;
}

export interface TestContainer {
    capabilities:     TestCapabilitiesV16;
    ctestLogicalName: string;
    degradedReason?:  string;
    disabled:         boolean;
    displayName:      string;
    framework:        TestFrameworkV16;
    id:               string;
    labels:           string[];
    projectId:        string;
    sourceLocation?:  TestSourceLocationV16;
}

export interface TestCapabilitiesV16 {
    canDiscoverCases:        boolean;
    canReportMockDetails:    boolean;
    canReportSkipped:        boolean;
    canReportSourceLocation: boolean;
    canRunCase:              boolean;
}

export enum TestFrameworkV16 {
    Cpputest = "cpputest",
    OpaqueCtest = "opaque-ctest",
    Unity = "unity",
}

export interface TestSourceLocationV16 {
    column?:    number;
    line?:      number;
    navigable:  boolean;
    provenance: TestSourceProvenanceV16;
    uri:        string;
}

export enum TestSourceProvenanceV16 {
    CtestBacktrace = "ctest-backtrace",
    FrameworkManifest = "framework-manifest",
    FrameworkOutput = "framework-output",
    MockActualCall = "mock-actual-call",
    MockExpectation = "mock-expectation",
    TestDeclaration = "test-declaration",
}

export interface DiagnosticV16 {
    category:   CategoryV16;
    code:       string;
    column?:    number;
    line?:      number;
    message:    string;
    severity:   DiagnosticSeverityV16;
    sourceUri?: string;
}

export enum CategoryV16 {
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

export enum DiagnosticSeverityV16 {
    Error = "error",
    Info = "info",
    Warning = "warning",
}

export interface TestItem {
    containerId:     string;
    disabled:        boolean;
    displayName:     string;
    framework:       TestFrameworkV16;
    id:              string;
    kind:            TestItemKindV16;
    labels:          string[];
    logicalName:     string;
    parameters?:     TestParameterV16[];
    parentId?:       string;
    sourceLocation?: TestSourceLocationV16;
}

export enum TestItemKindV16 {
    Case = "case",
    Group = "group",
    Suite = "suite",
}

export interface TestParameterV16 {
    name:  string;
    value: boolean | number | null | string;
}

export interface TestItemResult {
    containerId:     string;
    durationMs?:     number;
    failureDetails:  TestFailureDetailV16[];
    itemId:          string;
    iteration:       number;
    outcome:         TestItemOutcomeV16;
    outputRefs:      string[];
    partial:         boolean;
    reason?:         TestResultReasonV16;
    sourceLocation?: TestSourceLocationV16;
}

export interface TestFailureDetailV16 {
    actual?:      string;
    category:     CategoryV16;
    evidenceRefs: string[];
    expected?:    string;
    locations:    TestSourceLocationV16[];
    message:      string;
    subtype?:     TestFailureSubtypeV16;
}

export enum TestFailureSubtypeV16 {
    MockFailure = "mock_failure",
    MockMissingCall = "mock_missing_call",
    MockParameterMismatch = "mock_parameter_mismatch",
    MockUnexpectedCall = "mock_unexpected_call",
}

export enum TestItemOutcomeV16 {
    Cancelled = "cancelled",
    Errored = "errored",
    Failed = "failed",
    NotRun = "not_run",
    Passed = "passed",
    Skipped = "skipped",
    TimedOut = "timed_out",
}

export enum TestResultReasonV16 {
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
    outcome?:          TestRunOutcomeV16;
    profileId:         string;
    projectId:         string;
    resultRevision:    string;
    runId:             string;
    selectionSnapshot: TestSelectionSnapshotV16;
    startedAt?:        Date;
    status:            TestRunStatusV16;
    summary:           TestRunSummaryV16;
    taskId:            string;
    toolchainId:       string;
}

export enum TestRunOutcomeV16 {
    Blocked = "blocked",
    Cancelled = "cancelled",
    Errored = "errored",
    Failed = "failed",
    Interrupted = "interrupted",
    Passed = "passed",
    TimedOut = "timed_out",
}

export interface TestSelectionSnapshotV16 {
    containerIds: string[];
    itemIds:      string[];
    mode:         TestSelectionModeV16;
}

export enum TestSelectionModeV16 {
    All = "all",
    Containers = "containers",
    FailedFromRun = "failedFromRun",
    Filter = "filter",
    Items = "items",
}

export enum TestRunStatusV16 {
    Completed = "completed",
    Queued = "queued",
    Running = "running",
}

export interface TestRunSummaryV16 {
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

export interface AllTestSelectionV16 { mode: TestSelectionModeV16.All; containerIds?: never; itemIds?: never; filter?: never; runId?: never; }
export interface ContainersTestSelectionV16 { mode: TestSelectionModeV16.Containers; containerIds: string[]; itemIds?: never; filter?: never; runId?: never; }
export interface ItemsTestSelectionV16 { mode: TestSelectionModeV16.Items; itemIds: string[]; containerIds?: never; filter?: never; runId?: never; }
export interface FilterTestSelectionV16 { mode: TestSelectionModeV16.Filter; filter: TestFilterV16; containerIds?: never; itemIds?: never; runId?: never; }
export interface FailedFromRunTestSelectionV16 { mode: TestSelectionModeV16.FailedFromRun; runId: string; containerIds?: never; itemIds?: never; filter?: never; }
export type TestSelectionV16 = AllTestSelectionV16 | ContainersTestSelectionV16 | ItemsTestSelectionV16 | FilterTestSelectionV16 | FailedFromRunTestSelectionV16;

export interface TestFilterV16 {
    excludeItemIds?: string[];
    group?:          string;
    includeItemIds?: string[];
    label?:          string;
    nameContains?:   string;
    suite?:          string;
}
