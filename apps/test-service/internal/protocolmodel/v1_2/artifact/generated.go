package protocolmodelv12artifact

import "time"

type ArtifactMetadataV12 struct {
	ArtifactID string    `json:"artifactId"`
	CreatedAt  time.Time `json:"createdAt"`
	Kind       Kind      `json:"kind"`
	MIMEType   MIMEType  `json:"mimeType"`
	Sha256     string    `json:"sha256"`
	SizeBytes  int64     `json:"sizeBytes"`
	TaskID     string    `json:"taskId"`
	URI        string    `json:"uri"`
}

type Kind string

const (
	BuildSummary  Kind = "build-summary"
	Diagnostics   Kind = "diagnostics"
	ExecutionPlan Kind = "execution-plan"
	Stderr        Kind = "stderr"
	Stdout        Kind = "stdout"
	TaskSummary   Kind = "task-summary"
)

type MIMEType string

const (
	ApplicationJSON        MIMEType = "application/json"
	ApplicationOctetStream MIMEType = "application/octet-stream"
	ApplicationXNdjson     MIMEType = "application/x-ndjson"
)
