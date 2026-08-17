package protocolmodelv14diagnostic

type DiagnosticV14 struct {
	Category  DiagnosticCategoryV14 `json:"category"`
	Code      string                `json:"code"`
	Column    *int64                `json:"column,omitempty"`
	Line      *int64                `json:"line,omitempty"`
	Message   string                `json:"message"`
	Severity  DiagnosticSeverityV14 `json:"severity"`
	SourceURI *string               `json:"sourceUri,omitempty"`
}

type DiagnosticCategoryV14 string

const (
	AssertionFailure       DiagnosticCategoryV14 = "assertion_failure"
	BuildError             DiagnosticCategoryV14 = "build_error"
	Cancelled              DiagnosticCategoryV14 = "cancelled"
	ConfigurationError     DiagnosticCategoryV14 = "configuration_error"
	FrameworkOutputInvalid DiagnosticCategoryV14 = "framework_output_invalid"
	InconsistentExitStatus DiagnosticCategoryV14 = "inconsistent_exit_status"
	InfrastructureError    DiagnosticCategoryV14 = "infrastructure_error"
	TestProcessCrash       DiagnosticCategoryV14 = "test_process_crash"
	TestTimeout            DiagnosticCategoryV14 = "test_timeout"
	UnexpectedExit         DiagnosticCategoryV14 = "unexpected_exit"
)

type DiagnosticSeverityV14 string

const (
	Error   DiagnosticSeverityV14 = "error"
	Info    DiagnosticSeverityV14 = "info"
	Warning DiagnosticSeverityV14 = "warning"
)
