export interface CapabilitiesV15 {
    cmakeBuild:                  boolean;
    coverageReport:              boolean;
    coverageRun:                 boolean;
    ctestJson:                   boolean;
    frameworkAdapters:           FrameworkAdapterCapabilityV15[];
    maxCatalogPageSize:          number;
    maxCoveragePageSize:         number;
    maxCoverageTimeoutMs:        number;
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

export interface FrameworkAdapterCapabilityV15 {
    canDiscoverCases:        boolean;
    canReportMockDetails:    boolean;
    canReportSkipped:        boolean;
    canReportSourceLocation: boolean;
    canRunCase:              boolean;
    contractVersion:         string;
    displayName:             string;
    id:                      FrameworkAdapterIDV15;
}

export enum FrameworkAdapterIDV15 {
    Cpputest = "cpputest",
    Unity = "unity",
}
