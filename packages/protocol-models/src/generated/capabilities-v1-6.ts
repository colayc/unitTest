export interface CapabilitiesV16 {
    cmakeBuild:                  boolean;
    coverageDetails:             boolean;
    coverageReport:              boolean;
    coverageRun:                 boolean;
    ctestJson:                   boolean;
    frameworkAdapters:           FrameworkAdapterCapabilityV16[];
    managedTests:                boolean;
    maxCatalogPageSize:          number;
    maxCoverageDetailPageSize:   number;
    maxCoverageLinePageSize:     number;
    maxCoveragePageSize:         number;
    maxCoverageTimeoutMs:        number;
    maxManagedTestPageSize:      number;
    maxRepeatCount:              number;
    maxSelectionSize:            number;
    maxTestGenerationCandidates: number;
    opaqueCTestFallback:         boolean;
    targetList:                  boolean;
    testDiscovery:               boolean;
    testGeneration:              boolean;
    testRun:                     boolean;
    unityHelperContractVersion:  string;
    unityRunnerContractVersion:  string;
    workspaceInspect:            boolean;
}

export interface FrameworkAdapterCapabilityV16 {
    canDiscoverCases:        boolean;
    canReportMockDetails:    boolean;
    canReportSkipped:        boolean;
    canReportSourceLocation: boolean;
    canRunCase:              boolean;
    contractVersion:         string;
    displayName:             string;
    id:                      FrameworkAdapterIDV16;
}

export enum FrameworkAdapterIDV16 {
    Cpputest = "cpputest",
    Unity = "unity",
}
