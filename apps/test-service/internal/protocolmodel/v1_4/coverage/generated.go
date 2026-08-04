package protocolmodelv14coverage

import (
	"time"

	protocolmodelv14test "unit-test-ide.local/test-service/internal/protocolmodel/v1_4/test"
)

type CoverageContractV14 struct {
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
	Selection           protocolmodelv14test.TestSelectionV14 `json:"selection"`
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
	SelectionSnapshot   protocolmodelv14test.TestSelectionSnapshotV14 `json:"selectionSnapshot"`
	RepeatCount         int64                                         `json:"repeatCount"`
	TimeoutMS           int64                                         `json:"timeoutMs"`
	Status              CoverageRunStatusV14                          `json:"status"`
	Outcome             *CoverageRunOutcomeV14                        `json:"outcome,omitempty"`
	Reason              *CoverageRunReasonV14                         `json:"reason,omitempty"`
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
	ReportID       string                    `json:"reportId"`
	CoverageRunID  string                    `json:"coverageRunId"`
	TestRunID      string                    `json:"testRunId"`
	SchemaVersion  CoverageSchemaVersionV14  `json:"schemaVersion"`
	CreatedAt      time.Time                 `json:"createdAt"`
	Completeness   CoverageCompletenessV14   `json:"completeness"`
	Summary        CoverageSummaryV14        `json:"summary"`
	ToolProvenance CoverageToolProvenanceV14 `json:"toolProvenance"`
	ArtifactID     string                    `json:"artifactId"`
}
type CoverageMetricV14 struct {
	Covered int64 `json:"covered"`
	Total   int64 `json:"total"`
}
type CoverageSummaryV14 struct {
	Lines     CoverageMetricV14 `json:"lines"`
	Branches  CoverageMetricV14 `json:"branches"`
	Functions CoverageMetricV14 `json:"functions"`
}
type CoverageCompilerV14 struct {
	Family  CoverageCompilerFamilyV14 `json:"family"`
	Version string                    `json:"version"`
}
type CoverageDriverV14 struct {
	Name    CoverageDriverNameV14 `json:"name"`
	Version string                `json:"version"`
}
type CoverageCollectorV14 struct {
	Name    CoverageCollectorNameV14 `json:"name"`
	Version string                   `json:"version"`
}
type CoverageToolProvenanceV14 struct {
	Platform                   CoveragePlatformV14     `json:"platform"`
	Architecture               CoverageArchitectureV14 `json:"architecture"`
	Compiler                   CoverageCompilerV14     `json:"compiler"`
	Driver                     CoverageDriverV14       `json:"driver"`
	Collector                  CoverageCollectorV14    `json:"collector"`
	NormalizerVersion          string                  `json:"normalizerVersion"`
	InstrumentationFingerprint string                  `json:"instrumentationFingerprint"`
}
type CoverageCompletenessV14 struct {
	Outcome CoverageCompletenessOutcomeV14 `json:"outcome"`
	Reasons []CoverageIncompleteReasonV14  `json:"reasons"`
}
type CoverageSchemaVersionV14 string

const CoverageSchemaVersion10V14 CoverageSchemaVersionV14 = "1.0"

type CoverageCompletenessOutcomeV14 string

const (
	CoverageCompletenessAvailableV14 CoverageCompletenessOutcomeV14 = "available"
	CoverageCompletenessPartialV14   CoverageCompletenessOutcomeV14 = "partial"
)

type CoverageIncompleteReasonV14 string

const (
	CoverageTestCrashedV14                       CoverageIncompleteReasonV14 = "test_crashed"
	CoverageTestTimedOutV14                      CoverageIncompleteReasonV14 = "test_timed_out"
	CoverageProfileMissingForFailedInvocationV14 CoverageIncompleteReasonV14 = "profile_missing_for_failed_invocation"
)

type CoveragePlatformV14 string

const (
	CoverageWindowsV14 CoveragePlatformV14 = "windows"
	CoverageLinuxV14   CoveragePlatformV14 = "linux"
)

type CoverageArchitectureV14 string

const (
	CoverageX86V14   CoverageArchitectureV14 = "x86"
	CoverageX64V14   CoverageArchitectureV14 = "x64"
	CoverageArm64V14 CoverageArchitectureV14 = "arm64"
)

type CoverageCompilerFamilyV14 string

const (
	CoverageGCCV14     CoverageCompilerFamilyV14 = "gcc"
	CoverageClangV14   CoverageCompilerFamilyV14 = "clang"
	CoverageClangClV14 CoverageCompilerFamilyV14 = "clang-cl"
)

type CoverageDriverNameV14 string

const (
	CoverageGcovV14          CoverageDriverNameV14 = "gcov"
	CoverageDriverLlvmCovV14 CoverageDriverNameV14 = "llvm-cov"
)

type CoverageCollectorNameV14 string

const (
	CoverageGcovrV14            CoverageCollectorNameV14 = "gcovr"
	CoverageCollectorLlvmCovV14 CoverageCollectorNameV14 = "llvm-cov"
)

type CoverageRunStatusV14 string

const (
	CoverageRunQueuedV14   CoverageRunStatusV14 = "queued"
	CoverageRunRunningV14  CoverageRunStatusV14 = "running"
	CoverageRunFinishedV14 CoverageRunStatusV14 = "finished"
)

type CoverageRunOutcomeV14 string

const (
	CoverageAvailableV14   CoverageRunOutcomeV14 = "available"
	CoveragePartialV14     CoverageRunOutcomeV14 = "partial"
	CoverageUnavailableV14 CoverageRunOutcomeV14 = "unavailable"
	CoverageCancelledV14   CoverageRunOutcomeV14 = "cancelled"
)

type CoverageRunReasonV14 string

const (
	CoverageUserCancelledV14           CoverageRunReasonV14 = "user_cancelled"
	CoverageTaskTimedOutV14            CoverageRunReasonV14 = "task_timed_out"
	CoverageInstrumentationFailedV14   CoverageRunReasonV14 = "instrumentation_failed"
	CoverageBuildFailedV14             CoverageRunReasonV14 = "build_failed"
	CoverageProfileCollectionFailedV14 CoverageRunReasonV14 = "profile_collection_failed"
	CoverageMergeFailedV14             CoverageRunReasonV14 = "merge_failed"
	CoverageNormalizationFailedV14     CoverageRunReasonV14 = "normalization_failed"
	CoverageReportGenerationFailedV14  CoverageRunReasonV14 = "report_generation_failed"
	CoveragePersistenceFailedV14       CoverageRunReasonV14 = "persistence_failed"
	CoverageServiceRestartedV14        CoverageRunReasonV14 = "service_restarted"
)
