package protocolmodelv14capabilities

type CapabilitiesV14 struct {
	CmakeBuild                 bool                            `json:"cmakeBuild"`
	CoverageReport             bool                            `json:"coverageReport"`
	CoverageRun                bool                            `json:"coverageRun"`
	CtestJSON                  bool                            `json:"ctestJson"`
	FrameworkAdapters          []FrameworkAdapterCapabilityV14 `json:"frameworkAdapters"`
	MaxCatalogPageSize         float64                         `json:"maxCatalogPageSize"`
	MaxCoveragePageSize        float64                         `json:"maxCoveragePageSize"`
	MaxCoverageTimeoutMS       float64                         `json:"maxCoverageTimeoutMs"`
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

type FrameworkAdapterCapabilityV14 struct {
	CanDiscoverCases        bool                  `json:"canDiscoverCases"`
	CanReportMockDetails    bool                  `json:"canReportMockDetails"`
	CanReportSkipped        bool                  `json:"canReportSkipped"`
	CanReportSourceLocation bool                  `json:"canReportSourceLocation"`
	CanRunCase              bool                  `json:"canRunCase"`
	ContractVersion         string                `json:"contractVersion"`
	DisplayName             string                `json:"displayName"`
	ID                      FrameworkAdapterIDV14 `json:"id"`
}

type FrameworkAdapterIDV14 string

const (
	Cpputest FrameworkAdapterIDV14 = "cpputest"
	Unity    FrameworkAdapterIDV14 = "unity"
)
