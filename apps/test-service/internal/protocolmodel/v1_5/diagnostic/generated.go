package protocolmodelv15diagnostic

type DiagnosticV15 struct {
	Category  DiagnosticCategoryV15 `json:"category"`
	Code      string                `json:"code"`
	Column    *int64                `json:"column,omitempty"`
	Line      *int64                `json:"line,omitempty"`
	Message   string                `json:"message"`
	Severity  DiagnosticSeverityV15 `json:"severity"`
	SourceURI *string               `json:"sourceUri,omitempty"`
}

type DiagnosticCategoryV15 string

const (
	AssertionFailure       DiagnosticCategoryV15 = "assertion_failure"
	BuildError             DiagnosticCategoryV15 = "build_error"
	Cancelled              DiagnosticCategoryV15 = "cancelled"
	ConfigurationError     DiagnosticCategoryV15 = "configuration_error"
	FrameworkOutputInvalid DiagnosticCategoryV15 = "framework_output_invalid"
	InconsistentExitStatus DiagnosticCategoryV15 = "inconsistent_exit_status"
	InfrastructureError    DiagnosticCategoryV15 = "infrastructure_error"
	TestProcessCrash       DiagnosticCategoryV15 = "test_process_crash"
	TestTimeout            DiagnosticCategoryV15 = "test_timeout"
	UnexpectedExit         DiagnosticCategoryV15 = "unexpected_exit"
)

type DiagnosticSeverityV15 string

const (
	Error   DiagnosticSeverityV15 = "error"
	Info    DiagnosticSeverityV15 = "info"
	Warning DiagnosticSeverityV15 = "warning"
)
