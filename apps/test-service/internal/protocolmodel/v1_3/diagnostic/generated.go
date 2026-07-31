package protocolmodelv13diagnostic

type DiagnosticV13 struct {
	Category  DiagnosticCategoryV13 `json:"category"`
	Code      string                `json:"code"`
	Column    *int64                `json:"column,omitempty"`
	Line      *int64                `json:"line,omitempty"`
	Message   string                `json:"message"`
	Severity  DiagnosticSeverityV13 `json:"severity"`
	SourceURI *string               `json:"sourceUri,omitempty"`
}

type DiagnosticCategoryV13 string

const (
	AssertionFailure       DiagnosticCategoryV13 = "assertion_failure"
	BuildError             DiagnosticCategoryV13 = "build_error"
	Cancelled              DiagnosticCategoryV13 = "cancelled"
	ConfigurationError     DiagnosticCategoryV13 = "configuration_error"
	FrameworkOutputInvalid DiagnosticCategoryV13 = "framework_output_invalid"
	InconsistentExitStatus DiagnosticCategoryV13 = "inconsistent_exit_status"
	InfrastructureError    DiagnosticCategoryV13 = "infrastructure_error"
	TestProcessCrash       DiagnosticCategoryV13 = "test_process_crash"
	TestTimeout            DiagnosticCategoryV13 = "test_timeout"
	UnexpectedExit         DiagnosticCategoryV13 = "unexpected_exit"
)

type DiagnosticSeverityV13 string

const (
	Error   DiagnosticSeverityV13 = "error"
	Info    DiagnosticSeverityV13 = "info"
	Warning DiagnosticSeverityV13 = "warning"
)
