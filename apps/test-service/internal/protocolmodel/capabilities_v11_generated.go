package protocolmodel

type CapabilitiesV11 struct {
	ArtifactRead       bool               `json:"artifactRead"`
	CoverageTools      []string           `json:"coverageTools"`
	EventReplay        bool               `json:"eventReplay"`
	Frameworks         []string           `json:"frameworks"`
	Platform           Platform           `json:"platform"`
	ProcessTreeControl ProcessTreeControl `json:"processTreeControl"`
	SqliteHistory      bool               `json:"sqliteHistory"`
	TaskExecution      bool               `json:"taskExecution"`
	Toolchains         []string           `json:"toolchains"`
	Transports         []Transport        `json:"transports"`
}

type Platform string

const (
	Linux   Platform = "linux"
	Windows Platform = "windows"
)

type ProcessTreeControl string

const (
	JobObject    ProcessTreeControl = "job-object"
	ProcessGroup ProcessTreeControl = "process-group"
)

type Transport string

const (
	NamedPipe  Transport = "named-pipe"
	UnixSocket Transport = "unix-socket"
)
