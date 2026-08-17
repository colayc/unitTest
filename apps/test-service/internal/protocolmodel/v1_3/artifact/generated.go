package protocolmodelv13artifact

import "time"

type ArtifactMetadataV13 struct {
	ArtifactID string              `json:"artifactId"`
	CreatedAt  time.Time           `json:"createdAt"`
	Kind       ArtifactKindV13     `json:"kind"`
	MIMEType   ArtifactMIMETypeV13 `json:"mimeType"`
	Sha256     string              `json:"sha256"`
	SizeBytes  int64               `json:"sizeBytes"`
	TaskID     string              `json:"taskId"`
	URI        string              `json:"uri"`
}

type ArtifactKindV13 string

const (
	BuildSummary    ArtifactKindV13 = "build-summary"
	Diagnostics     ArtifactKindV13 = "diagnostics"
	ExecutionPlan   ArtifactKindV13 = "execution-plan"
	Stderr          ArtifactKindV13 = "stderr"
	Stdout          ArtifactKindV13 = "stdout"
	TaskSummary     ArtifactKindV13 = "task-summary"
	TestCatalog     ArtifactKindV13 = "test-catalog"
	TestDiagnostics ArtifactKindV13 = "test-diagnostics"
	TestOutput      ArtifactKindV13 = "test-output"
	TestResults     ArtifactKindV13 = "test-results"
	TestRunSummary  ArtifactKindV13 = "test-run-summary"
	TestSelection   ArtifactKindV13 = "test-selection"
)

type ArtifactMIMETypeV13 string

const (
	ApplicationJSON        ArtifactMIMETypeV13 = "application/json"
	ApplicationOctetStream ArtifactMIMETypeV13 = "application/octet-stream"
	ApplicationXNdjson     ArtifactMIMETypeV13 = "application/x-ndjson"
)
