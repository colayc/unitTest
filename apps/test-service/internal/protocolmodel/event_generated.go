package protocolmodel

import "time"

type TaskEvent struct {
	Event           EventEnum              `json:"event"`
	Kind            EventKind              `json:"kind"`
	MessageID       string                 `json:"messageId"`
	Payload         map[string]interface{} `json:"payload"`
	PayloadVersion  float64                `json:"payloadVersion"`
	ProtocolVersion ProtocolVersion        `json:"protocolVersion"`
	SentAt          time.Time              `json:"sentAt"`
	Sequence        int64                  `json:"sequence"`
	TaskID          string                 `json:"taskId"`
}

type EventEnum string

const (
	ArtifactCreated           EventEnum = "artifact.created"
	TaskCancellationRequested EventEnum = "task.cancellation_requested"
	TaskCreated               EventEnum = "task.created"
	TaskFinished              EventEnum = "task.finished"
	TaskOutput                EventEnum = "task.output"
	TaskStarted               EventEnum = "task.started"
)

type EventKind string

const (
	Event EventKind = "event"
)

type ProtocolVersion string

const (
	The11 ProtocolVersion = "1.1"
)
