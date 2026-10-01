package protocolmodelv16coverage

import "time"

type CoverageContractV16 struct {
	DetailFilesRequest     CoverageDetailFilesRequestV16     `json:"detailFilesRequest"`
	DetailFunctionsRequest CoverageDetailFunctionsRequestV16 `json:"detailFunctionsRequest"`
	DetailLinesRequest     CoverageDetailLinesRequestV16     `json:"detailLinesRequest"`
	DetailProject          CoverageProjectV16                `json:"detailProject"`
	DetailProjectRequest   CoverageDetailProjectRequestV16   `json:"detailProjectRequest"`
	FilePage               CoverageFilePageV16               `json:"filePage"`
	FunctionPage           CoverageFunctionPageV16           `json:"functionPage"`
	LinePage               CoverageLinePageV16               `json:"linePage"`
	Report                 CoverageReportV16                 `json:"report"`
	Run                    CoverageRunV16                    `json:"run"`
	RunPage                CoverageRunPageV16                `json:"runPage"`
	RunStartRequest        CoverageRunStartRequestV16        `json:"runStartRequest"`
}

type CoverageDetailFilesRequestV16 struct {
	CoverageReportID    string  `json:"coverageReportId"`
	Cursor              *string `json:"cursor,omitempty"`
	Limit               *int64  `json:"limit,omitempty"`
	ProjectID           string  `json:"projectId"`
	WorkspaceGeneration string  `json:"workspaceGeneration"`
}

type CoverageDetailFunctionsRequestV16 struct {
	CoverageReportID    string  `json:"coverageReportId"`
	Cursor              *string `json:"cursor,omitempty"`
	FileID              string  `json:"fileId"`
	Limit               *int64  `json:"limit,omitempty"`
	WorkspaceGeneration string  `json:"workspaceGeneration"`
}

type CoverageDetailLinesRequestV16 struct {
	CoverageReportID    string  `json:"coverageReportId"`
	Cursor              *string `json:"cursor,omitempty"`
	FileID              *string `json:"fileId,omitempty"`
	FunctionID          *string `json:"functionId,omitempty"`
	Limit               *int64  `json:"limit,omitempty"`
	WorkspaceGeneration string  `json:"workspaceGeneration"`
}

type CoverageProjectV16 struct {
	CoverageReportID    string                    `json:"coverageReportId"`
	FileCount           int64                     `json:"fileCount"`
	ProjectID           string                    `json:"projectId"`
	Reasons             []CoverageDetailReasonV16 `json:"reasons"`
	Status              CoverageDetailStatusV16   `json:"status"`
	Summary             CoverageSummaryV16        `json:"summary"`
	WorkspaceGeneration string                    `json:"workspaceGeneration"`
}

type CoverageSummaryV16 struct {
	Branches  CoverageMetricV16 `json:"branches"`
	Functions CoverageMetricV16 `json:"functions"`
	Lines     CoverageMetricV16 `json:"lines"`
}

type CoverageMetricV16 struct {
	Covered      int64 `json:"covered"`
	CoveredDelta int64 `json:"coveredDelta"`
	Total        int64 `json:"total"`
}

type CoverageDetailProjectRequestV16 struct {
	CoverageReportID    string `json:"coverageReportId"`
	ProjectID           string `json:"projectId"`
	WorkspaceGeneration string `json:"workspaceGeneration"`
}

type CoverageFilePageV16 struct {
	CoverageReportID    string            `json:"coverageReportId"`
	Items               []CoverageFileV16 `json:"items"`
	NextCursor          *string           `json:"nextCursor,omitempty"`
	WorkspaceGeneration string            `json:"workspaceGeneration"`
}

type CoverageFileV16 struct {
	FileID        string                    `json:"fileId"`
	FunctionCount int64                     `json:"functionCount"`
	Reasons       []CoverageDetailReasonV16 `json:"reasons"`
	RelativePath  string                    `json:"relativePath"`
	SourceSha256  string                    `json:"sourceSha256"`
	Status        CoverageDetailStatusV16   `json:"status"`
	Summary       CoverageSummaryV16        `json:"summary"`
}

type CoverageFunctionPageV16 struct {
	CoverageReportID    string                `json:"coverageReportId"`
	Items               []CoverageFunctionV16 `json:"items"`
	NextCursor          *string               `json:"nextCursor,omitempty"`
	WorkspaceGeneration string                `json:"workspaceGeneration"`
}

type CoverageFunctionV16 struct {
	CoverageGapID *string                   `json:"coverageGapId,omitempty"`
	EndLine       int64                     `json:"endLine"`
	FileID        string                    `json:"fileId"`
	FunctionID    string                    `json:"functionId"`
	QualifiedName string                    `json:"qualifiedName"`
	Reasons       []CoverageDetailReasonV16 `json:"reasons"`
	StartLine     int64                     `json:"startLine"`
	Status        CoverageDetailStatusV16   `json:"status"`
	Summary       CoverageSummaryV16        `json:"summary"`
}

type CoverageLinePageV16 struct {
	CoverageReportID    string                  `json:"coverageReportId"`
	Items               []CoverageLineDetailV16 `json:"items"`
	NextCursor          *string                 `json:"nextCursor,omitempty"`
	WorkspaceGeneration string                  `json:"workspaceGeneration"`
}

type CoverageLineDetailV16 struct {
	BaselineBranchesCovered int64   `json:"baselineBranchesCovered"`
	BaselineBranchesTotal   int64   `json:"baselineBranchesTotal"`
	BaselineCount           int64   `json:"baselineCount"`
	BranchesCovered         int64   `json:"branchesCovered"`
	BranchesTotal           int64   `json:"branchesTotal"`
	Count                   int64   `json:"count"`
	CoverageGapID           *string `json:"coverageGapId,omitempty"`
	Line                    int64   `json:"line"`
}

type CoverageReportV16 struct {
	ArtifactID     string                      `json:"artifactId"`
	Completeness   CoverageCompletenessV16     `json:"completeness"`
	CoverageRunID  string                      `json:"coverageRunId"`
	CreatedAt      time.Time                   `json:"createdAt"`
	ReportID       string                      `json:"reportId"`
	SchemaVersion  SchemaVersion               `json:"schemaVersion"`
	Sources        []CoverageSourceSnapshotV16 `json:"sources,omitempty"`
	Summary        CoverageReportSummaryV16    `json:"summary"`
	TestRunID      string                      `json:"testRunId"`
	ToolProvenance CoverageToolProvenanceV16   `json:"toolProvenance"`
}

type CoverageCompletenessV16 struct {
	Outcome CompletenessOutcome `json:"outcome"`
	Reasons []ReasonElement     `json:"reasons"`
}

type CoverageSourceSnapshotV16 struct {
	Sha256 string `json:"sha256"`
	URI    string `json:"uri"`
}

type CoverageReportSummaryV16 struct {
	Branches  CoverageReportMetricV16 `json:"branches"`
	Functions CoverageReportMetricV16 `json:"functions"`
	Lines     CoverageReportMetricV16 `json:"lines"`
}

type CoverageReportMetricV16 struct {
	Covered int64 `json:"covered"`
	Total   int64 `json:"total"`
}

type CoverageToolProvenanceV16 struct {
	Architecture               Architecture         `json:"architecture"`
	Collector                  CoverageCollectorV16 `json:"collector"`
	Compiler                   CoverageCompilerV16  `json:"compiler"`
	Driver                     CoverageDriverV16    `json:"driver"`
	InstrumentationFingerprint string               `json:"instrumentationFingerprint"`
	NormalizerVersion          string               `json:"normalizerVersion"`
	Platform                   Platform             `json:"platform"`
}

type CoverageCollectorV16 struct {
	Name    CollectorName `json:"name"`
	Version string        `json:"version"`
}

type CoverageCompilerV16 struct {
	Family  Family `json:"family"`
	Version string `json:"version"`
}

type CoverageDriverV16 struct {
	Name    DriverName `json:"name"`
	Version string     `json:"version"`
}

type CoverageRunV16 struct {
	CatalogRevision     string                   `json:"catalogRevision"`
	CoverageProfileID   string                   `json:"coverageProfileId"`
	CoverageRunID       string                   `json:"coverageRunId"`
	CreatedAt           time.Time                `json:"createdAt"`
	FinishedAt          *time.Time               `json:"finishedAt,omitempty"`
	LastSequence        int64                    `json:"lastSequence"`
	Outcome             *CoverageRunOutcomeV16   `json:"outcome,omitempty"`
	ProjectID           string                   `json:"projectId"`
	Reason              *CoverageRunReasonV16    `json:"reason,omitempty"`
	RepeatCount         int64                    `json:"repeatCount"`
	ReportID            *string                  `json:"reportId,omitempty"`
	SelectionSnapshot   TestSelectionSnapshotV16 `json:"selectionSnapshot"`
	StartedAt           *time.Time               `json:"startedAt,omitempty"`
	Status              Status                   `json:"status"`
	TaskID              string                   `json:"taskId"`
	TestRunID           string                   `json:"testRunId"`
	TimeoutMS           int64                    `json:"timeoutMs"`
	WorkspaceGeneration string                   `json:"workspaceGeneration"`
}

type TestSelectionSnapshotV16 struct {
	ContainerIDS []string             `json:"containerIds"`
	ItemIDS      []string             `json:"itemIds"`
	Mode         TestSelectionModeV16 `json:"mode"`
}

type CoverageRunPageV16 struct {
	Items      []CoverageRunV16 `json:"items"`
	NextCursor *string          `json:"nextCursor,omitempty"`
}

type CoverageRunStartRequestV16 struct {
	CatalogRevision     string        `json:"catalogRevision"`
	CoverageProfileID   string        `json:"coverageProfileId"`
	IdempotencyKey      string        `json:"idempotencyKey"`
	ProjectID           string        `json:"projectId"`
	RepeatCount         int64         `json:"repeatCount"`
	Selection           TestSelection `json:"selection"`
	TimeoutMS           int64         `json:"timeoutMs"`
	WorkspaceGeneration string        `json:"workspaceGeneration"`
}

type TestSelection struct {
	Mode         TestSelectionModeV16 `json:"mode"`
	ContainerIDS []string             `json:"containerIds,omitempty"`
	ItemIDS      []string             `json:"itemIds,omitempty"`
	Filter       *TestFilterV16       `json:"filter,omitempty"`
	RunID        *string              `json:"runId,omitempty"`
}

type TestFilterV16 struct {
	ExcludeItemIDS []string `json:"excludeItemIds,omitempty"`
	Group          *string  `json:"group,omitempty"`
	IncludeItemIDS []string `json:"includeItemIds,omitempty"`
	Label          *string  `json:"label,omitempty"`
	NameContains   *string  `json:"nameContains,omitempty"`
	Suite          *string  `json:"suite,omitempty"`
}

type CoverageDetailReasonV16 string

const (
	AttributionAmbiguous CoverageDetailReasonV16 = "attribution_ambiguous"
	BaselineUnavailable  CoverageDetailReasonV16 = "baseline_unavailable"
	ReportPartial        CoverageDetailReasonV16 = "report_partial"
	SourceChanged        CoverageDetailReasonV16 = "source_changed"
	SourceMissing        CoverageDetailReasonV16 = "source_missing"
	ToolIdentityChanged  CoverageDetailReasonV16 = "tool_identity_changed"
)

type CoverageDetailStatusV16 string

const (
	Current    CoverageDetailStatusV16 = "current"
	Incomplete CoverageDetailStatusV16 = "incomplete"
	Stale      CoverageDetailStatusV16 = "stale"
)

type CompletenessOutcome string

const (
	PurpleAvailable CompletenessOutcome = "available"
	PurplePartial   CompletenessOutcome = "partial"
)

type ReasonElement string

const (
	ProfileMissingForFailedInvocation ReasonElement = "profile_missing_for_failed_invocation"
	TestCrashed                       ReasonElement = "test_crashed"
	TestTimedOut                      ReasonElement = "test_timed_out"
)

type SchemaVersion string

const (
	The10 SchemaVersion = "1.0"
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

type CoverageRunOutcomeV16 string

const (
	Cancelled       CoverageRunOutcomeV16 = "cancelled"
	FluffyAvailable CoverageRunOutcomeV16 = "available"
	FluffyPartial   CoverageRunOutcomeV16 = "partial"
	Unavailable     CoverageRunOutcomeV16 = "unavailable"
)

type CoverageRunReasonV16 string

const (
	BuildFailed             CoverageRunReasonV16 = "build_failed"
	InstrumentationFailed   CoverageRunReasonV16 = "instrumentation_failed"
	MergeFailed             CoverageRunReasonV16 = "merge_failed"
	NormalizationFailed     CoverageRunReasonV16 = "normalization_failed"
	PersistenceFailed       CoverageRunReasonV16 = "persistence_failed"
	ProfileCollectionFailed CoverageRunReasonV16 = "profile_collection_failed"
	ReportGenerationFailed  CoverageRunReasonV16 = "report_generation_failed"
	ServiceRestarted        CoverageRunReasonV16 = "service_restarted"
	TaskTimedOut            CoverageRunReasonV16 = "task_timed_out"
	UserCancelled           CoverageRunReasonV16 = "user_cancelled"
)

type TestSelectionModeV16 string

const (
	All           TestSelectionModeV16 = "all"
	Containers    TestSelectionModeV16 = "containers"
	FailedFromRun TestSelectionModeV16 = "failedFromRun"
	Filter        TestSelectionModeV16 = "filter"
	Items         TestSelectionModeV16 = "items"
)

type Status string

const (
	Finished Status = "finished"
	Queued   Status = "queued"
	Running  Status = "running"
)
