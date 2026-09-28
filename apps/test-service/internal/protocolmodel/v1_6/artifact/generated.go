package protocolmodelv16artifact

import "time"

type ArtifactMetadataV16 struct {
	ArtifactID string              `json:"artifactId"`
	CreatedAt  time.Time           `json:"createdAt"`
	Kind       ArtifactKindV16     `json:"kind"`
	MIMEType   ArtifactMIMETypeV16 `json:"mimeType"`
	Sha256     string              `json:"sha256"`
	SizeBytes  int64               `json:"sizeBytes"`
	TaskID     string              `json:"taskId"`
	// Opaque service artifact URI only; filesystem and network URIs are forbidden in protocol
	// v1.6.
	URI string `json:"uri"`
}

type ArtifactKindV16 string

const (
	BuildSummary    ArtifactKindV16 = "build-summary"
	CoverageHTML    ArtifactKindV16 = "coverage-html"
	CoverageJSON    ArtifactKindV16 = "coverage-json"
	Diagnostics     ArtifactKindV16 = "diagnostics"
	ExecutionPlan   ArtifactKindV16 = "execution-plan"
	JunitXML        ArtifactKindV16 = "junit-xml"
	Stderr          ArtifactKindV16 = "stderr"
	Stdout          ArtifactKindV16 = "stdout"
	TaskSummary     ArtifactKindV16 = "task-summary"
	TestCatalog     ArtifactKindV16 = "test-catalog"
	TestDiagnostics ArtifactKindV16 = "test-diagnostics"
	TestOutput      ArtifactKindV16 = "test-output"
	TestResults     ArtifactKindV16 = "test-results"
	TestRunSummary  ArtifactKindV16 = "test-run-summary"
	TestSelection   ArtifactKindV16 = "test-selection"
)

type ArtifactMIMETypeV16 string

const (
	ApplicationJSON        ArtifactMIMETypeV16 = "application/json"
	ApplicationOctetStream ArtifactMIMETypeV16 = "application/octet-stream"
	ApplicationXML         ArtifactMIMETypeV16 = "application/xml"
	ApplicationXNdjson     ArtifactMIMETypeV16 = "application/x-ndjson"
	TextHTML               ArtifactMIMETypeV16 = "text/html"
)
