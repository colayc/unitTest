package protocolmodelv12event

import "time"

type TaskEventV12 interface{ isTaskEventV12() }
type TaskEventBaseV12 struct {
	ProtocolVersion EventProtocolVersionV12 `json:"protocolVersion"`
	Kind            EventKindV12            `json:"kind"`
	MessageID       string                  `json:"messageId"`
	SentAt          time.Time               `json:"sentAt"`
	Sequence        int64                   `json:"sequence"`
	TaskID          string                  `json:"taskId"`
	PayloadVersion  float64                 `json:"payloadVersion"`
}
type TaskCreatedEventV12 struct {
	TaskEventBaseV12
	Event   TaskEventNameV12 `json:"event"`
	Payload struct {
		Status string `json:"status"`
	} `json:"payload"`
}

func (TaskCreatedEventV12) isTaskEventV12() {}

type TaskStartedEventV12 struct {
	TaskEventBaseV12
	Event   TaskEventNameV12 `json:"event"`
	Payload struct {
		Status string `json:"status"`
	} `json:"payload"`
}

func (TaskStartedEventV12) isTaskEventV12() {}

type TaskStepStartedEventV12 struct {
	TaskEventBaseV12
	Event   TaskEventNameV12 `json:"event"`
	Payload struct {
		StepID string            `json:"stepId"`
		Kind   TaskStepKindV12   `json:"kind"`
		Status TaskStepStatusV12 `json:"status"`
	} `json:"payload"`
}

func (TaskStepStartedEventV12) isTaskEventV12() {}

type TaskOutputEventV12 struct {
	TaskEventBaseV12
	Event   TaskEventNameV12 `json:"event"`
	Payload struct {
		StepID    string              `json:"stepId"`
		Stream    TaskOutputStreamV12 `json:"stream"`
		Text      string              `json:"text"`
		Truncated bool                `json:"truncated"`
	} `json:"payload"`
}

func (TaskOutputEventV12) isTaskEventV12() {}

type TaskStepFinishedEventV12 struct {
	TaskEventBaseV12
	Event   TaskEventNameV12 `json:"event"`
	Payload struct {
		StepID    string            `json:"stepId"`
		Kind      TaskStepKindV12   `json:"kind"`
		Status    TaskStepStatusV12 `json:"status"`
		ExitCode  *int64            `json:"exitCode,omitempty"`
		ErrorCode *string           `json:"errorCode,omitempty"`
	} `json:"payload"`
}

func (TaskStepFinishedEventV12) isTaskEventV12() {}

type TaskCancellationRequestedEventV12 struct {
	TaskEventBaseV12
	Event   TaskEventNameV12 `json:"event"`
	Payload struct {
		Status string `json:"status"`
	} `json:"payload"`
}

func (TaskCancellationRequestedEventV12) isTaskEventV12() {}

type ArtifactCreatedEventV12 struct {
	TaskEventBaseV12
	Event   TaskEventNameV12 `json:"event"`
	Payload struct {
		ArtifactID string `json:"artifactId"`
		Kind       string `json:"kind"`
	} `json:"payload"`
}

func (ArtifactCreatedEventV12) isTaskEventV12() {}

type TaskFinishedEventV12 struct {
	TaskEventBaseV12
	Event   TaskEventNameV12 `json:"event"`
	Payload struct {
		Outcome TaskOutcomeV12 `json:"outcome"`
	} `json:"payload"`
}

func (TaskFinishedEventV12) isTaskEventV12() {}

type TaskDiagnosticEventV12 struct {
	TaskEventBaseV12
	Event   TaskEventNameV12 `json:"event"`
	Payload struct {
		Diagnostic TaskEventDiagnosticV12 `json:"diagnostic"`
	} `json:"payload"`
}

func (TaskDiagnosticEventV12) isTaskEventV12() {}

type TaskStepKindV12 string

const (
	StepSimulation TaskStepKindV12 = "simulation"
	StepConfigure  TaskStepKindV12 = "configure"
	StepBuild      TaskStepKindV12 = "build"
)

type TaskStepStatusV12 string

const (
	StepRunning   TaskStepStatusV12 = "running"
	StepSucceeded TaskStepStatusV12 = "succeeded"
	StepFailed    TaskStepStatusV12 = "failed"
	StepSkipped   TaskStepStatusV12 = "skipped"
)

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

const Event EventKindV12 = "event"

type TaskEventDiagnosticSeverityV12 string

const (
	Error   TaskEventDiagnosticSeverityV12 = "error"
	Info    TaskEventDiagnosticSeverityV12 = "info"
	Warning TaskEventDiagnosticSeverityV12 = "warning"
)

type TaskOutcomeV12 string

const (
	OutcomeCancelled            TaskOutcomeV12 = "cancelled"
	OutcomeCommandFailed        TaskOutcomeV12 = "command_failed"
	OutcomeInfrastructureFailed TaskOutcomeV12 = "infrastructure_failed"
	OutcomeInterrupted          TaskOutcomeV12 = "interrupted"
	OutcomeSucceeded            TaskOutcomeV12 = "succeeded"
	OutcomeTimedOut             TaskOutcomeV12 = "timed_out"
)

type TaskOutputStreamV12 string

const (
	OutputCombined TaskOutputStreamV12 = "combined"
	OutputStderr   TaskOutputStreamV12 = "stderr"
	OutputStdout   TaskOutputStreamV12 = "stdout"
)

type EventProtocolVersionV12 string

const The12 EventProtocolVersionV12 = "1.2"
