package protocolmodel

import "time"

type ArtifactMetadata struct {
	ArtifactID string       `json:"artifactId"`
	CreatedAt  time.Time    `json:"createdAt"`
	Kind       ArtifactKind `json:"kind"`
	MIMEType   MIMEType     `json:"mimeType"`
	Sha256     string       `json:"sha256"`
	SizeBytes  int64        `json:"sizeBytes"`
	TaskID     string       `json:"taskId"`
}

type ArtifactKind string

const (
	TaskSummary ArtifactKind = "task-summary"
)

type MIMEType string

const (
	ApplicationJSON MIMEType = "application/json"
)
