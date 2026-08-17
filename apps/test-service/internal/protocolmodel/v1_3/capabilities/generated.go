package protocolmodelv13capabilities

type CapabilitiesV13 struct {
	CmakeBuild                 bool                            `json:"cmakeBuild"`
	CtestJSON                  bool                            `json:"ctestJson"`
	FrameworkAdapters          []FrameworkAdapterCapabilityV13 `json:"frameworkAdapters"`
	MaxCatalogPageSize         float64                         `json:"maxCatalogPageSize"`
	MaxRepeatCount             float64                         `json:"maxRepeatCount"`
	MaxSelectionSize           float64                         `json:"maxSelectionSize"`
	OpaqueCTestFallback        bool                            `json:"opaqueCTestFallback"`
	TargetList                 bool                            `json:"targetList"`
	TestDiscovery              bool                            `json:"testDiscovery"`
	TestRun                    bool                            `json:"testRun"`
	UnityHelperContractVersion string                          `json:"unityHelperContractVersion"`
	UnityRunnerContractVersion string                          `json:"unityRunnerContractVersion"`
	WorkspaceInspect           bool                            `json:"workspaceInspect"`
}

type FrameworkAdapterCapabilityV13 struct {
	CanDiscoverCases        bool                  `json:"canDiscoverCases"`
	CanReportMockDetails    bool                  `json:"canReportMockDetails"`
	CanReportSkipped        bool                  `json:"canReportSkipped"`
	CanReportSourceLocation bool                  `json:"canReportSourceLocation"`
	CanRunCase              bool                  `json:"canRunCase"`
	ContractVersion         string                `json:"contractVersion"`
	DisplayName             string                `json:"displayName"`
	ID                      FrameworkAdapterIDV13 `json:"id"`
}

type FrameworkAdapterIDV13 string

const (
	Cpputest FrameworkAdapterIDV13 = "cpputest"
	Unity    FrameworkAdapterIDV13 = "unity"
)
