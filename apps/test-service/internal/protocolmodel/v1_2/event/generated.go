package protocolmodelv12event

import "time"

type TaskEventV12 interface{ isTaskEventV12() }
type TaskDiagnosticEventV12 struct {
	ProtocolVersion EventProtocolVersionV12 `json:"protocolVersion"`
	Kind            EventKindV12            `json:"kind"`
	MessageID       string                  `json:"messageId"`
	SentAt          time.Time               `json:"sentAt"`
	Sequence        int64                   `json:"sequence"`
	Event           TaskEventNameV12        `json:"event"`
	TaskID          string                  `json:"taskId"`
	PayloadVersion  float64                 `json:"payloadVersion"`
	Payload         struct {
		Diagnostic TaskEventDiagnosticV12 `json:"diagnostic"`
	} `json:"payload"`
}

func (TaskDiagnosticEventV12) isTaskEventV12() {}

type TaskStepStartedEventV12 struct {
	ProtocolVersion EventProtocolVersionV12 `json:"protocolVersion"`
	Kind            EventKindV12            `json:"kind"`
	MessageID       string                  `json:"messageId"`
	SentAt          time.Time               `json:"sentAt"`
	Sequence        int64                   `json:"sequence"`
	Event           TaskEventNameV12        `json:"event"`
	TaskID          string                  `json:"taskId"`
	PayloadVersion  float64                 `json:"payloadVersion"`
	Payload         struct {
		Step TaskStepNameV12 `json:"step"`
	} `json:"payload"`
}

func (TaskStepStartedEventV12) isTaskEventV12() {}

type TaskStepFinishedEventV12 struct {
	ProtocolVersion EventProtocolVersionV12 `json:"protocolVersion"`
	Kind            EventKindV12            `json:"kind"`
	MessageID       string                  `json:"messageId"`
	SentAt          time.Time               `json:"sentAt"`
	Sequence        int64                   `json:"sequence"`
	Event           TaskEventNameV12        `json:"event"`
	TaskID          string                  `json:"taskId"`
	PayloadVersion  float64                 `json:"payloadVersion"`
	Payload         struct {
		Step    TaskStepNameV12    `json:"step"`
		Outcome TaskStepOutcomeV12 `json:"outcome"`
	} `json:"payload"`
}

func (TaskStepFinishedEventV12) isTaskEventV12() {}

type TaskPayloadV12 struct {
	Diagnostic *TaskEventDiagnosticV12 `json:"diagnostic,omitempty"`
	Step       *TaskStepNameV12        `json:"step,omitempty"`
	Outcome    *TaskStepOutcomeV12     `json:"outcome,omitempty"`
}

type TaskEventDiagnosticV12 struct {
	Code      string                         `json:"code"`
	Column    *int64                         `json:"column,omitempty"`
	Line      *int64                         `json:"line,omitempty"`
	Message   string                         `json:"message"`
	Severity  TaskEventDiagnosticSeverityV12 `json:"severity"`
	SourceURI *string                        `json:"sourceUri,omitempty"`
}

type TaskEventNameV12 string

const (
	ArtifactCreated           TaskEventNameV12 = "artifact.created"
	TaskCancellationRequested TaskEventNameV12 = "task.cancellation_requested"
	TaskCreated               TaskEventNameV12 = "task.created"
	TaskDiagnostic            TaskEventNameV12 = "task.diagnostic"
	TaskFinished              TaskEventNameV12 = "task.finished"
	TaskOutput                TaskEventNameV12 = "task.output"
	TaskStarted               TaskEventNameV12 = "task.started"
	TaskStepFinished          TaskEventNameV12 = "task.step_finished"
	TaskStepStarted           TaskEventNameV12 = "task.step_started"
)

type EventKindV12 string

const (
	Event EventKindV12 = "event"
)

type TaskEventDiagnosticSeverityV12 string

const (
	Error   TaskEventDiagnosticSeverityV12 = "error"
	Info    TaskEventDiagnosticSeverityV12 = "info"
	Warning TaskEventDiagnosticSeverityV12 = "warning"
)

type TaskStepOutcomeV12 string

const (
	Cancelled            TaskStepOutcomeV12 = "cancelled"
	CommandFailed        TaskStepOutcomeV12 = "command_failed"
	InfrastructureFailed TaskStepOutcomeV12 = "infrastructure_failed"
	Interrupted          TaskStepOutcomeV12 = "interrupted"
	Succeeded            TaskStepOutcomeV12 = "succeeded"
	TimedOut             TaskStepOutcomeV12 = "timed_out"
)

type TaskStepNameV12 string

const (
	Build     TaskStepNameV12 = "build"
	Configure TaskStepNameV12 = "configure"
)

type EventProtocolVersionV12 string

const (
	The12 EventProtocolVersionV12 = "1.2"
)
