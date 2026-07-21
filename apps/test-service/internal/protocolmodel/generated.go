package protocolmodel

type Capabilities struct {
	CoverageTools []string `json:"coverageTools"`
	Frameworks    []string `json:"frameworks"`
	Platform      string   `json:"platform"`
	Toolchains    []string `json:"toolchains"`
	Transports    []string `json:"transports"`
}
