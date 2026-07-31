export interface CapabilitiesV13 {
    cmakeBuild:                 boolean;
    ctestJson:                  boolean;
    frameworkAdapters:          FrameworkAdapterCapabilityV13[];
    maxCatalogPageSize:         number;
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

export interface FrameworkAdapterCapabilityV13 {
    canDiscoverCases:        boolean;
    canReportMockDetails:    boolean;
    canReportSkipped:        boolean;
    canReportSourceLocation: boolean;
    canRunCase:              boolean;
    contractVersion:         string;
    displayName:             string;
    id:                      FrameworkAdapterIDV13;
}

export enum FrameworkAdapterIDV13 {
    Cpputest = "cpputest",
    Unity = "unity",
}
