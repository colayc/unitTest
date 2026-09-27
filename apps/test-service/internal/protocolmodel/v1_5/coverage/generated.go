package protocolmodelv15coverage

import (
	"time"

	protocolmodelv15test "unit-test-ide.local/test-service/internal/protocolmodel/v1_5/test"
)

type CoverageContractV15 struct {
	RunStartRequest CoverageRunStartRequest `json:"runStartRequest"`
	Run             CoverageRun             `json:"run"`
	RunPage         CoverageRunPage         `json:"runPage"`
	Report          CoverageReport          `json:"report"`
}
type CoverageRunStartRequest struct {
	IdempotencyKey      string                                `json:"idempotencyKey"`
	WorkspaceGeneration string                                `json:"workspaceGeneration"`
	ProjectID           string                                `json:"projectId"`
	CoverageProfileID   string                                `json:"coverageProfileId"`
	CatalogRevision     string                                `json:"catalogRevision"`
	Selection           protocolmodelv15test.TestSelectionV15 `json:"selection"`
	RepeatCount         int64                                 `json:"repeatCount"`
	TimeoutMS           int64                                 `json:"timeoutMs"`
}
type CoverageRun struct {
	CoverageRunID       string                                        `json:"coverageRunId"`
	TaskID              string                                        `json:"taskId"`
	TestRunID           string                                        `json:"testRunId"`
	WorkspaceGeneration string                                        `json:"workspaceGeneration"`
	ProjectID           string                                        `json:"projectId"`
	CoverageProfileID   string                                        `json:"coverageProfileId"`
	CatalogRevision     string                                        `json:"catalogRevision"`
	SelectionSnapshot   protocolmodelv15test.TestSelectionSnapshotV15 `json:"selectionSnapshot"`
	RepeatCount         int64                                         `json:"repeatCount"`
	TimeoutMS           int64                                         `json:"timeoutMs"`
	Status              CoverageRunStatusV15                          `json:"status"`
	Outcome             *CoverageRunOutcomeV15                        `json:"outcome,omitempty"`
	Reason              *CoverageRunReasonV15                         `json:"reason,omitempty"`
	CreatedAt           time.Time                                     `json:"createdAt"`
	StartedAt           *time.Time                                    `json:"startedAt,omitempty"`
	FinishedAt          *time.Time                                    `json:"finishedAt,omitempty"`
	ReportID            *string                                       `json:"reportId,omitempty"`
	LastSequence        int64                                         `json:"lastSequence"`
}
type CoverageRunPage struct {
	Items      []CoverageRun `json:"items"`
	NextCursor *string       `json:"nextCursor,omitempty"`
}
type CoverageReport struct {
	ReportID       string                      `json:"reportId"`
	CoverageRunID  string                      `json:"coverageRunId"`
	TestRunID      string                      `json:"testRunId"`
	SchemaVersion  CoverageSchemaVersionV15    `json:"schemaVersion"`
	CreatedAt      time.Time                   `json:"createdAt"`
	Completeness   CoverageCompletenessV15     `json:"completeness"`
	Summary        CoverageSummaryV15          `json:"summary"`
	ToolProvenance CoverageToolProvenanceV15   `json:"toolProvenance"`
	ArtifactID     string                      `json:"artifactId"`
	Sources        []CoverageSourceSnapshotV15 `json:"sources,omitempty"`
}
type CoverageSourceSnapshotV15 struct {
	URI    string `json:"uri"`
	SHA256 string `json:"sha256"`
}
type CoverageMetricV15 struct {
	Covered int64 `json:"covered"`
	Total   int64 `json:"total"`
}
type CoverageSummaryV15 struct {
	Lines     CoverageMetricV15 `json:"lines"`
	Branches  CoverageMetricV15 `json:"branches"`
	Functions CoverageMetricV15 `json:"functions"`
}
type CoverageCompilerV15 struct {
	Family  CoverageCompilerFamilyV15 `json:"family"`
	Version string                    `json:"version"`
}
type CoverageDriverV15 struct {
	Name    CoverageDriverNameV15 `json:"name"`
	Version string                `json:"version"`
}
type CoverageCollectorV15 struct {
	Name    CoverageCollectorNameV15 `json:"name"`
	Version string                   `json:"version"`
}
type CoverageToolProvenanceV15 struct {
	Platform                   CoveragePlatformV15     `json:"platform"`
	Architecture               CoverageArchitectureV15 `json:"architecture"`
	Compiler                   CoverageCompilerV15     `json:"compiler"`
	Driver                     CoverageDriverV15       `json:"driver"`
	Collector                  CoverageCollectorV15    `json:"collector"`
	NormalizerVersion          string                  `json:"normalizerVersion"`
	InstrumentationFingerprint string                  `json:"instrumentationFingerprint"`
}
type CoverageCompletenessV15 struct {
	Outcome CoverageCompletenessOutcomeV15 `json:"outcome"`
	Reasons []CoverageIncompleteReasonV15  `json:"reasons"`
}
type CoverageSchemaVersionV15 string

const CoverageSchemaVersion10V15 CoverageSchemaVersionV15 = "1.0"

type CoverageCompletenessOutcomeV15 string

const (
	CoverageCompletenessAvailableV15 CoverageCompletenessOutcomeV15 = "available"
	CoverageCompletenessPartialV15   CoverageCompletenessOutcomeV15 = "partial"
)

type CoverageIncompleteReasonV15 string

const (
	CoverageTestCrashedV15                       CoverageIncompleteReasonV15 = "test_crashed"
	CoverageTestTimedOutV15                      CoverageIncompleteReasonV15 = "test_timed_out"
	CoverageProfileMissingForFailedInvocationV15 CoverageIncompleteReasonV15 = "profile_missing_for_failed_invocation"
)

type CoveragePlatformV15 string

const (
	CoverageWindowsV15 CoveragePlatformV15 = "windows"
	CoverageLinuxV15   CoveragePlatformV15 = "linux"
)

type CoverageArchitectureV15 string

const (
	CoverageX86V15   CoverageArchitectureV15 = "x86"
	CoverageX64V15   CoverageArchitectureV15 = "x64"
	CoverageArm64V15 CoverageArchitectureV15 = "arm64"
)

type CoverageCompilerFamilyV15 string

const (
	CoverageGCCV15     CoverageCompilerFamilyV15 = "gcc"
	CoverageClangV15   CoverageCompilerFamilyV15 = "clang"
	CoverageClangClV15 CoverageCompilerFamilyV15 = "clang-cl"
)

type CoverageDriverNameV15 string

const (
	CoverageGcovV15          CoverageDriverNameV15 = "gcov"
	CoverageDriverLlvmCovV15 CoverageDriverNameV15 = "llvm-cov"
)

type CoverageCollectorNameV15 string

const (
	CoverageGcovrV15            CoverageCollectorNameV15 = "gcovr"
	CoverageCollectorLlvmCovV15 CoverageCollectorNameV15 = "llvm-cov"
)

type CoverageRunStatusV15 string

const (
	CoverageRunQueuedV15   CoverageRunStatusV15 = "queued"
	CoverageRunRunningV15  CoverageRunStatusV15 = "running"
	CoverageRunFinishedV15 CoverageRunStatusV15 = "finished"
)

type CoverageRunOutcomeV15 string

const (
	CoverageAvailableV15   CoverageRunOutcomeV15 = "available"
	CoveragePartialV15     CoverageRunOutcomeV15 = "partial"
	CoverageUnavailableV15 CoverageRunOutcomeV15 = "unavailable"
	CoverageCancelledV15   CoverageRunOutcomeV15 = "cancelled"
)

type CoverageRunReasonV15 string

const (
	CoverageUserCancelledV15           CoverageRunReasonV15 = "user_cancelled"
	CoverageTaskTimedOutV15            CoverageRunReasonV15 = "task_timed_out"
	CoverageInstrumentationFailedV15   CoverageRunReasonV15 = "instrumentation_failed"
	CoverageBuildFailedV15             CoverageRunReasonV15 = "build_failed"
	CoverageProfileCollectionFailedV15 CoverageRunReasonV15 = "profile_collection_failed"
	CoverageMergeFailedV15             CoverageRunReasonV15 = "merge_failed"
	CoverageNormalizationFailedV15     CoverageRunReasonV15 = "normalization_failed"
	CoverageReportGenerationFailedV15  CoverageRunReasonV15 = "report_generation_failed"
	CoveragePersistenceFailedV15       CoverageRunReasonV15 = "persistence_failed"
	CoverageServiceRestartedV15        CoverageRunReasonV15 = "service_restarted"
)
