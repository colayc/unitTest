package protocolmodel

type Diagnostic struct {
	Code      string   `json:"code"`
	Column    *int64   `json:"column,omitempty"`
	Line      *int64   `json:"line,omitempty"`
	Message   string   `json:"message"`
	Severity  Severity `json:"severity"`
	SourceURI *string  `json:"sourceUri,omitempty"`
}

type Severity string

const (
	Error   Severity = "error"
	Info    Severity = "info"
	Warning Severity = "warning"
)
