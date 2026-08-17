export interface CapabilitiesV14 {
    cmakeBuild:                 boolean;
    coverageReport:             boolean;
    coverageRun:                boolean;
    ctestJson:                  boolean;
    frameworkAdapters:          FrameworkAdapterCapabilityV14[];
    maxCatalogPageSize:         number;
    maxCoveragePageSize:        number;
    maxCoverageTimeoutMs:       number;
    maxRepeatCount:             number;
    maxSelectionSize:           number;
    opaqueCTestFallback:        boolean;
    targetList:                 boolean;
    testDiscovery:              boolean;
    testRun:                    boolean;
    unityHelperContractVersion: string;
    unityRunnerContractVersion: string;
    workspaceInspect:           boolean;
}

export interface FrameworkAdapterCapabilityV14 {
    canDiscoverCases:        boolean;
    canReportMockDetails:    boolean;
    canReportSkipped:        boolean;
    canReportSourceLocation: boolean;
    canRunCase:              boolean;
    contractVersion:         string;
    displayName:             string;
    id:                      FrameworkAdapterIDV14;
}

export enum FrameworkAdapterIDV14 {
    Cpputest = "cpputest",
    Unity = "unity",
}
