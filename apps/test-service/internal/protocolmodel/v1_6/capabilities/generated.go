package protocolmodelv16capabilities

type CapabilitiesV16 struct {
	CmakeBuild                  bool                            `json:"cmakeBuild"`
	CoverageDetails             bool                            `json:"coverageDetails"`
	CoverageReport              bool                            `json:"coverageReport"`
	CoverageRun                 bool                            `json:"coverageRun"`
	CtestJSON                   bool                            `json:"ctestJson"`
	FrameworkAdapters           []FrameworkAdapterCapabilityV16 `json:"frameworkAdapters"`
	ManagedTests                bool                            `json:"managedTests"`
	MaxCatalogPageSize          float64                         `json:"maxCatalogPageSize"`
	MaxCoverageDetailPageSize   float64                         `json:"maxCoverageDetailPageSize"`
	MaxCoverageLinePageSize     float64                         `json:"maxCoverageLinePageSize"`
	MaxCoveragePageSize         float64                         `json:"maxCoveragePageSize"`
	MaxCoverageTimeoutMS        float64                         `json:"maxCoverageTimeoutMs"`
	MaxManagedTestPageSize      float64                         `json:"maxManagedTestPageSize"`
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

type FrameworkAdapterCapabilityV16 struct {
	CanDiscoverCases        bool                  `json:"canDiscoverCases"`
	CanReportMockDetails    bool                  `json:"canReportMockDetails"`
	CanReportSkipped        bool                  `json:"canReportSkipped"`
	CanReportSourceLocation bool                  `json:"canReportSourceLocation"`
	CanRunCase              bool                  `json:"canRunCase"`
	ContractVersion         string                `json:"contractVersion"`
	DisplayName             string                `json:"displayName"`
	ID                      FrameworkAdapterIDV16 `json:"id"`
}

type FrameworkAdapterIDV16 string

const (
	Cpputest FrameworkAdapterIDV16 = "cpputest"
	Unity    FrameworkAdapterIDV16 = "unity"
)
