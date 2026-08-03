package coveragemodelv1

type CoverageDocumentV1 struct {
	Completeness  CoverageCompletenessV1 `json:"completeness"`
	Files         []CoverageFileV1       `json:"files"`
	Provenance    CoverageProvenanceV1   `json:"provenance"`
	SchemaVersion SchemaVersion          `json:"schemaVersion"`
	Summary       CoverageSummaryV1      `json:"summary"`
}

type CoverageCompletenessV1 struct {
	Outcome Outcome  `json:"outcome"`
	Reasons []Reason `json:"reasons"`
}

type CoverageFileV1 struct {
	Lines   []CoverageLineV1  `json:"lines"`
	Sha256  string            `json:"sha256"`
	Summary CoverageSummaryV1 `json:"summary"`
	URI     string            `json:"uri"`
}

type CoverageLineV1 struct {
	Branches CoverageMetricV1 `json:"branches"`
	Count    int64            `json:"count"`
	Line     int64            `json:"line"`
}

type CoverageMetricV1 struct {
	Covered int64 `json:"covered"`
	Total   int64 `json:"total"`
}

type CoverageSummaryV1 struct {
	Branches  CoverageMetricV1 `json:"branches"`
	Functions CoverageMetricV1 `json:"functions"`
	Lines     CoverageMetricV1 `json:"lines"`
}

type CoverageProvenanceV1 struct {
	Architecture               Architecture        `json:"architecture"`
	Collector                  CoverageCollectorV1 `json:"collector"`
	Compiler                   CoverageCompilerV1  `json:"compiler"`
	Driver                     CoverageDriverV1    `json:"driver"`
	InstrumentationFingerprint string              `json:"instrumentationFingerprint"`
	NormalizerVersion          string              `json:"normalizerVersion"`
	Platform                   Platform            `json:"platform"`
}

type CoverageCollectorV1 struct {
	Name    CollectorName `json:"name"`
	Version string        `json:"version"`
}

type CoverageCompilerV1 struct {
	Family  Family `json:"family"`
	Version string `json:"version"`
}

type CoverageDriverV1 struct {
	Name    DriverName `json:"name"`
	Version string     `json:"version"`
}

type Outcome string

const (
	Available Outcome = "available"
	Partial   Outcome = "partial"
)

type Reason string

const (
	ProfileMissingForFailedInvocation Reason = "profile_missing_for_failed_invocation"
	TestCrashed                       Reason = "test_crashed"
	TestTimedOut                      Reason = "test_timed_out"
)

type Architecture string

const (
	Arm64 Architecture = "arm64"
	X64   Architecture = "x64"
	X86   Architecture = "x86"
)

type CollectorName string

const (
	Gcovr         CollectorName = "gcovr"
	PurpleLlvmCov CollectorName = "llvm-cov"
)

type Family string

const (
	Clang   Family = "clang"
	ClangCl Family = "clang-cl"
	GCC     Family = "gcc"
)

type DriverName string

const (
	FluffyLlvmCov DriverName = "llvm-cov"
	Gcov          DriverName = "gcov"
)

type Platform string

const (
	Linux   Platform = "linux"
	Windows Platform = "windows"
)

type SchemaVersion string

const (
	The10 SchemaVersion = "1.0"
)
