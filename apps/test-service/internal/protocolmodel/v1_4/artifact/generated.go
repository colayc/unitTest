package protocolmodelv14artifact

import "time"

type ArtifactMetadataV14 struct {
	ArtifactID string              `json:"artifactId"`
	CreatedAt  time.Time           `json:"createdAt"`
	Kind       ArtifactKindV14     `json:"kind"`
	MIMEType   ArtifactMIMETypeV14 `json:"mimeType"`
	Sha256     string              `json:"sha256"`
	SizeBytes  int64               `json:"sizeBytes"`
	TaskID     string              `json:"taskId"`
	URI        string              `json:"uri"`
}

type ArtifactKindV14 string

const (
	BuildSummary    ArtifactKindV14 = "build-summary"
	CoverageHTML    ArtifactKindV14 = "coverage-html"
	CoverageJSON    ArtifactKindV14 = "coverage-json"
	Diagnostics     ArtifactKindV14 = "diagnostics"
	ExecutionPlan   ArtifactKindV14 = "execution-plan"
	JunitXML        ArtifactKindV14 = "junit-xml"
	Stderr          ArtifactKindV14 = "stderr"
	Stdout          ArtifactKindV14 = "stdout"
	TaskSummary     ArtifactKindV14 = "task-summary"
	TestCatalog     ArtifactKindV14 = "test-catalog"
	TestDiagnostics ArtifactKindV14 = "test-diagnostics"
	TestOutput      ArtifactKindV14 = "test-output"
	TestResults     ArtifactKindV14 = "test-results"
	TestRunSummary  ArtifactKindV14 = "test-run-summary"
	TestSelection   ArtifactKindV14 = "test-selection"
)

type ArtifactMIMETypeV14 string

const (
	ApplicationJSON        ArtifactMIMETypeV14 = "application/json"
	ApplicationOctetStream ArtifactMIMETypeV14 = "application/octet-stream"
	ApplicationXML         ArtifactMIMETypeV14 = "application/xml"
	ApplicationXNdjson     ArtifactMIMETypeV14 = "application/x-ndjson"
	TextHTML               ArtifactMIMETypeV14 = "text/html"
)
