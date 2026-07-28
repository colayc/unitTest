package protocolmodel

import "time"

type TaskEventV12 struct {
	Event           EventEnum       `json:"event"`
	Kind            Kind            `json:"kind"`
	MessageID       string          `json:"messageId"`
	Payload         Payload         `json:"payload"`
	PayloadVersion  float64         `json:"payloadVersion"`
	ProtocolVersion ProtocolVersion `json:"protocolVersion"`
	SentAt          time.Time       `json:"sentAt"`
	Sequence        int64           `json:"sequence"`
	TaskID          string          `json:"taskId"`
}

type Payload struct {
	Diagnostic *Diagnostic `json:"diagnostic,omitempty"`
}

type Diagnostic struct {
	Code      string   `json:"code"`
	Column    *int64   `json:"column,omitempty"`
	Line      *int64   `json:"line,omitempty"`
	Message   string   `json:"message"`
	Severity  Severity `json:"severity"`
	SourceURI *string  `json:"sourceUri,omitempty"`
}

type EventEnum string

const (
	ArtifactCreated           EventEnum = "artifact.created"
	TaskCancellationRequested EventEnum = "task.cancellation_requested"
	TaskCreated               EventEnum = "task.created"
	TaskDiagnostic            EventEnum = "task.diagnostic"
	TaskFinished              EventEnum = "task.finished"
	TaskOutput                EventEnum = "task.output"
	TaskStarted               EventEnum = "task.started"
)

type Kind string

const (
	Event Kind = "event"
)

type Severity string

const (
	Error   Severity = "error"
	Info    Severity = "info"
	Warning Severity = "warning"
)

type ProtocolVersion string

const (
	The12 ProtocolVersion = "1.2"
)
