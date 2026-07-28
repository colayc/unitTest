package diagnostic

type Position struct {
	Line      int
	Character int
}

type Range struct {
	Start Position
	End   Position
}

type Related struct {
	Message string
	FileURI string
	Range   *Range
}

type Diagnostic struct {
	ID          string
	TaskID      string
	StepID      string
	Source      string
	ToolchainID string
	Severity    string
	Code        string
	Message     string
	FileURI     string
	Range       *Range
	Related     []Related
	External    bool
}

type Parser interface {
	Feed(stream string, data []byte) []Diagnostic
	Close() []Diagnostic
}
