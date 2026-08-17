export interface DiagnosticV13 {
    category:   DiagnosticCategoryV13;
    code:       string;
    column?:    number;
    line?:      number;
    message:    string;
    severity:   DiagnosticSeverityV13;
    sourceUri?: string;
}

export enum DiagnosticCategoryV13 {
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
