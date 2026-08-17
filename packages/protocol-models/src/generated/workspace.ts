export interface WorkspaceSnapshot {
    capabilities:        WorkspaceSnapshotCapabilities;
    diagnostics:         DiagnosticElement[];
    projects:            ProjectElement[];
    toolchains:          ToolchainElement[];
    workspaceGeneration: string;
    workspaceUri:        string;
}

export interface WorkspaceSnapshotCapabilities {
    cmakeBuild:       boolean;
    targetList:       boolean;
    workspaceInspect: boolean;
}

export interface DiagnosticElement {
    code:       string;
    column?:    number;
    line?:      number;
    message:    string;
    severity:   Severity;
    sourceUri?: string;
}

export enum Severity {
    Error = "error",
    Info = "info",
    Warning = "warning",
}

export interface ProjectElement {
    buildProfiles: BuildProfileElement[];
    projectId:     string;
    sourceUri:     string;
}

export interface BuildProfileElement {
    buildProfileId: string;
    configuration?: string;
    generator:      Generator;
    name:           string;
    origin:         Origin;
    toolchainId?:   string;
}

export enum Generator {
    NMakeMakefiles = "NMake Makefiles",
    Ninja = "Ninja",
    UnixMakefiles = "Unix Makefiles",
    VisualStudio172022 = "Visual Studio 17 2022",
    VisualStudio182026 = "Visual Studio 18 2026",
}

export enum Origin {
    Generated = "generated",
    Preset = "preset",
}

export interface ToolchainElement {
    capabilities:       ToolchainCapabilities;
    family:             Family;
    generators:         Generator[];
    hostArchitecture:   TArchitecture;
    targetArchitecture: TArchitecture;
    targetTriple:       string;
    toolchainId:        string;
    version:            string;
}

export interface ToolchainCapabilities {
    coverageDrivers: CoverageDriver[];
}

export enum CoverageDriver {
    Gcov = "gcov",
    LlvmCov = "llvm-cov",
}

export enum Family {
    Clang = "clang",
    ClangCl = "clang-cl",
    GCC = "gcc",
    Msvc = "msvc",
}

export enum TArchitecture {
    Arm64 = "arm64",
    X64 = "x64",
    X86 = "x86",
}
