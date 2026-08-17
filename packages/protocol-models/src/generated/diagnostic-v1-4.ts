export interface DiagnosticV14 {
    category:   DiagnosticCategoryV14;
    code:       string;
    column?:    number;
    line?:      number;
    message:    string;
    severity:   DiagnosticSeverityV14;
    sourceUri?: string;
}

export enum DiagnosticCategoryV14 {
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
