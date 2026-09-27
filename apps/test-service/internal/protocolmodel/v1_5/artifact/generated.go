package protocolmodelv15artifact

import "time"

type ArtifactMetadataV15 struct {
	ArtifactID string              `json:"artifactId"`
	CreatedAt  time.Time           `json:"createdAt"`
	Kind       ArtifactKindV15     `json:"kind"`
	MIMEType   ArtifactMIMETypeV15 `json:"mimeType"`
	Sha256     string              `json:"sha256"`
	SizeBytes  int64               `json:"sizeBytes"`
	TaskID     string              `json:"taskId"`
	URI        string              `json:"uri"`
}

type ArtifactKindV15 string

const (
	BuildSummary    ArtifactKindV15 = "build-summary"
	CoverageHTML    ArtifactKindV15 = "coverage-html"
	CoverageJSON    ArtifactKindV15 = "coverage-json"
	Diagnostics     ArtifactKindV15 = "diagnostics"
	ExecutionPlan   ArtifactKindV15 = "execution-plan"
	JunitXML        ArtifactKindV15 = "junit-xml"
	Stderr          ArtifactKindV15 = "stderr"
	Stdout          ArtifactKindV15 = "stdout"
	TaskSummary     ArtifactKindV15 = "task-summary"
	TestCatalog     ArtifactKindV15 = "test-catalog"
	TestDiagnostics ArtifactKindV15 = "test-diagnostics"
	TestOutput      ArtifactKindV15 = "test-output"
	TestResults     ArtifactKindV15 = "test-results"
	TestRunSummary  ArtifactKindV15 = "test-run-summary"
	TestSelection   ArtifactKindV15 = "test-selection"
)

type ArtifactMIMETypeV15 string

const (
	ApplicationJSON        ArtifactMIMETypeV15 = "application/json"
	ApplicationOctetStream ArtifactMIMETypeV15 = "application/octet-stream"
	ApplicationXML         ArtifactMIMETypeV15 = "application/xml"
	ApplicationXNdjson     ArtifactMIMETypeV15 = "application/x-ndjson"
	TextHTML               ArtifactMIMETypeV15 = "text/html"
)
