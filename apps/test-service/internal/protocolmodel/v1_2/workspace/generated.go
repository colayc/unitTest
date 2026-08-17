package protocolmodelv12workspace

type WorkspaceSnapshot struct {
	Capabilities        WorkspaceSnapshotCapabilities `json:"capabilities"`
	Diagnostics         []DiagnosticElement           `json:"diagnostics"`
	Projects            []ProjectElement              `json:"projects"`
	Toolchains          []ToolchainElement            `json:"toolchains"`
	WorkspaceGeneration string                        `json:"workspaceGeneration"`
	WorkspaceURI        string                        `json:"workspaceUri"`
}

type WorkspaceSnapshotCapabilities struct {
	CmakeBuild       bool `json:"cmakeBuild"`
	TargetList       bool `json:"targetList"`
	WorkspaceInspect bool `json:"workspaceInspect"`
}

type DiagnosticElement struct {
	Code      string   `json:"code"`
	Column    *int64   `json:"column,omitempty"`
	Line      *int64   `json:"line,omitempty"`
	Message   string   `json:"message"`
	Severity  Severity `json:"severity"`
	SourceURI *string  `json:"sourceUri,omitempty"`
}

type ProjectElement struct {
	BuildProfiles []BuildProfileElement `json:"buildProfiles"`
	ProjectID     string                `json:"projectId"`
	SourceURI     string                `json:"sourceUri"`
}

type BuildProfileElement struct {
	BuildProfileID string    `json:"buildProfileId"`
	Configuration  *string   `json:"configuration,omitempty"`
	Generator      Generator `json:"generator"`
	Name           string    `json:"name"`
	Origin         Origin    `json:"origin"`
	ToolchainID    *string   `json:"toolchainId,omitempty"`
}

type ToolchainElement struct {
	Capabilities       ToolchainCapabilities `json:"capabilities"`
	Family             Family                `json:"family"`
	Generators         []Generator           `json:"generators"`
	HostArchitecture   TArchitecture         `json:"hostArchitecture"`
	TargetArchitecture TArchitecture         `json:"targetArchitecture"`
	TargetTriple       string                `json:"targetTriple"`
	ToolchainID        string                `json:"toolchainId"`
	Version            string                `json:"version"`
}

type ToolchainCapabilities struct {
	CoverageDrivers []CoverageDriver `json:"coverageDrivers"`
}

type Severity string

const (
	Error   Severity = "error"
	Info    Severity = "info"
	Warning Severity = "warning"
)

type Generator string

const (
	NMakeMakefiles     Generator = "NMake Makefiles"
	Ninja              Generator = "Ninja"
	UnixMakefiles      Generator = "Unix Makefiles"
	VisualStudio172022 Generator = "Visual Studio 17 2022"
	VisualStudio182026 Generator = "Visual Studio 18 2026"
)

type Origin string

const (
	Generated Origin = "generated"
	Preset    Origin = "preset"
)

type CoverageDriver string

const (
	Gcov    CoverageDriver = "gcov"
	LlvmCov CoverageDriver = "llvm-cov"
)

type Family string

const (
	Clang   Family = "clang"
	ClangCl Family = "clang-cl"
	GCC     Family = "gcc"
	Msvc    Family = "msvc"
)

type TArchitecture string

const (
	Arm64 TArchitecture = "arm64"
	X64   TArchitecture = "x64"
	X86   TArchitecture = "x86"
)
