package protocolmodelv15capabilities

type CapabilitiesV15 struct {
	CmakeBuild                  bool                            `json:"cmakeBuild"`
	CoverageReport              bool                            `json:"coverageReport"`
	CoverageRun                 bool                            `json:"coverageRun"`
	CtestJSON                   bool                            `json:"ctestJson"`
	FrameworkAdapters           []FrameworkAdapterCapabilityV15 `json:"frameworkAdapters"`
	MaxCatalogPageSize          float64                         `json:"maxCatalogPageSize"`
	MaxCoveragePageSize         float64                         `json:"maxCoveragePageSize"`
	MaxCoverageTimeoutMS        float64                         `json:"maxCoverageTimeoutMs"`
	MaxRepeatCount              float64                         `json:"maxRepeatCount"`
	MaxSelectionSize            float64                         `json:"maxSelectionSize"`
	MaxTestGenerationCandidates float64                         `json:"maxTestGenerationCandidates"`
	OpaqueCTestFallback         bool                            `json:"opaqueCTestFallback"`
	TargetList                  bool                            `json:"targetList"`
	TestDiscovery               bool                            `json:"testDiscovery"`
	TestGeneration              bool                            `json:"testGeneration"`
	TestRun                     bool                            `json:"testRun"`
	UnityHelperContractVersion  string                          `json:"unityHelperContractVersion"`
	UnityRunnerContractVersion  string                          `json:"unityRunnerContractVersion"`
	WorkspaceInspect            bool                            `json:"workspaceInspect"`
}

type FrameworkAdapterCapabilityV15 struct {
	CanDiscoverCases        bool                  `json:"canDiscoverCases"`
	CanReportMockDetails    bool                  `json:"canReportMockDetails"`
	CanReportSkipped        bool                  `json:"canReportSkipped"`
	CanReportSourceLocation bool                  `json:"canReportSourceLocation"`
	CanRunCase              bool                  `json:"canRunCase"`
	ContractVersion         string                `json:"contractVersion"`
	DisplayName             string                `json:"displayName"`
	ID                      FrameworkAdapterIDV15 `json:"id"`
}

type FrameworkAdapterIDV15 string

const (
	Cpputest FrameworkAdapterIDV15 = "cpputest"
	Unity    FrameworkAdapterIDV15 = "unity"
)
