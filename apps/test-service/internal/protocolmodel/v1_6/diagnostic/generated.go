package protocolmodelv16diagnostic

type DiagnosticV16 struct {
	Category  DiagnosticCategoryV16 `json:"category"`
	Code      string                `json:"code"`
	Column    *int64                `json:"column,omitempty"`
	Line      *int64                `json:"line,omitempty"`
	Message   string                `json:"message"`
	Severity  DiagnosticSeverityV16 `json:"severity"`
	SourceURI *string               `json:"sourceUri,omitempty"`
}

type DiagnosticCategoryV16 string

const (
	AssertionFailure       DiagnosticCategoryV16 = "assertion_failure"
	BuildError             DiagnosticCategoryV16 = "build_error"
	Cancelled              DiagnosticCategoryV16 = "cancelled"
	ConfigurationError     DiagnosticCategoryV16 = "configuration_error"
	FrameworkOutputInvalid DiagnosticCategoryV16 = "framework_output_invalid"
	InconsistentExitStatus DiagnosticCategoryV16 = "inconsistent_exit_status"
	InfrastructureError    DiagnosticCategoryV16 = "infrastructure_error"
	TestProcessCrash       DiagnosticCategoryV16 = "test_process_crash"
	TestTimeout            DiagnosticCategoryV16 = "test_timeout"
	UnexpectedExit         DiagnosticCategoryV16 = "unexpected_exit"
)

type DiagnosticSeverityV16 string

const (
	Error   DiagnosticSeverityV16 = "error"
	Info    DiagnosticSeverityV16 = "info"
	Warning DiagnosticSeverityV16 = "warning"
)
