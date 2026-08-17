package protocolmodelv13event

import (
	"time"

	protocolmodelv13diagnostic "unit-test-ide.local/test-service/internal/protocolmodel/v1_3/diagnostic"
	protocolmodelv13test "unit-test-ide.local/test-service/internal/protocolmodel/v1_3/test"
)

type TaskEventV13 interface{ isTaskEventV13() }
type TaskEventBaseV13 struct {
	ProtocolVersion EventProtocolVersionV13 `json:"protocolVersion"`
	Kind            EventKindV13            `json:"kind"`
	MessageID       string                  `json:"messageId"`
	SentAt          time.Time               `json:"sentAt"`
	Sequence        int64                   `json:"sequence"`
	TaskID          string                  `json:"taskId"`
	PayloadVersion  int64                   `json:"payloadVersion"`
}
type TaskCreatedEventV13 struct {
	TaskEventBaseV13
	Event   TaskEventNameV13 `json:"event"`
	Payload struct {
		Status string `json:"status"`
	} `json:"payload"`
}

func (TaskCreatedEventV13) isTaskEventV13() {}

type TaskStartedEventV13 struct {
	TaskEventBaseV13
	Event   TaskEventNameV13 `json:"event"`
	Payload struct {
		Status string `json:"status"`
	} `json:"payload"`
}

func (TaskStartedEventV13) isTaskEventV13() {}

type TaskStepStartedEventV13 struct {
	TaskEventBaseV13
	Event   TaskEventNameV13          `json:"event"`
	Payload TaskStepStartedPayloadV13 `json:"payload"`
}

func (TaskStepStartedEventV13) isTaskEventV13() {}

type TaskOutputEventV13 struct {
	TaskEventBaseV13
	Event   TaskEventNameV13     `json:"event"`
	Payload TaskOutputPayloadV13 `json:"payload"`
}

func (TaskOutputEventV13) isTaskEventV13() {}

type TaskStepFinishedEventV13 struct {
	TaskEventBaseV13
	Event   TaskEventNameV13           `json:"event"`
	Payload TaskStepFinishedPayloadV13 `json:"payload"`
}

func (TaskStepFinishedEventV13) isTaskEventV13() {}

type TaskCancellationRequestedEventV13 struct {
	TaskEventBaseV13
	Event   TaskEventNameV13 `json:"event"`
	Payload struct {
		Status string `json:"status"`
	} `json:"payload"`
}

func (TaskCancellationRequestedEventV13) isTaskEventV13() {}

type ArtifactCreatedEventV13 struct {
	TaskEventBaseV13
	Event   TaskEventNameV13          `json:"event"`
	Payload ArtifactCreatedPayloadV13 `json:"payload"`
}

func (ArtifactCreatedEventV13) isTaskEventV13() {}

type TaskFinishedEventV13 struct {
	TaskEventBaseV13
	Event   TaskEventNameV13 `json:"event"`
	Payload struct {
		Outcome TaskOutcomeV13 `json:"outcome"`
	} `json:"payload"`
}

func (TaskFinishedEventV13) isTaskEventV13() {}

type TaskDiagnosticEventV13 struct {
	TaskEventBaseV13
	Event   TaskEventNameV13 `json:"event"`
	Payload struct {
		Diagnostic protocolmodelv13diagnostic.DiagnosticV13 `json:"diagnostic"`
	} `json:"payload"`
}

func (TaskDiagnosticEventV13) isTaskEventV13() {}

type TestDiscoveryStartedEventV13 struct {
	TaskEventBaseV13
	Event   TaskEventNameV13               `json:"event"`
	Payload TestDiscoveryStartedPayloadV13 `json:"payload"`
}

func (TestDiscoveryStartedEventV13) isTaskEventV13() {}

type TestContainerDiscoveredEventV13 struct {
	TaskEventBaseV13
	Event   TaskEventNameV13                  `json:"event"`
	Payload TestContainerDiscoveredPayloadV13 `json:"payload"`
}

func (TestContainerDiscoveredEventV13) isTaskEventV13() {}

type TestCatalogPublishedEventV13 struct {
	TaskEventBaseV13
	Event   TaskEventNameV13               `json:"event"`
	Payload TestCatalogPublishedPayloadV13 `json:"payload"`
}

func (TestCatalogPublishedEventV13) isTaskEventV13() {}

type TestRunStartedEventV13 struct {
	TaskEventBaseV13
	Event   TaskEventNameV13         `json:"event"`
	Payload TestRunStartedPayloadV13 `json:"payload"`
}

func (TestRunStartedEventV13) isTaskEventV13() {}

type TestContainerStartedEventV13 struct {
	TaskEventBaseV13
	Event   TaskEventNameV13               `json:"event"`
	Payload TestContainerStartedPayloadV13 `json:"payload"`
}

func (TestContainerStartedEventV13) isTaskEventV13() {}

type TestItemStartedEventV13 struct {
	TaskEventBaseV13
	Event   TaskEventNameV13          `json:"event"`
	Payload TestItemStartedPayloadV13 `json:"payload"`
}

func (TestItemStartedEventV13) isTaskEventV13() {}

type TestOutputEventV13 struct {
	TaskEventBaseV13
	Event   TaskEventNameV13     `json:"event"`
	Payload TestOutputPayloadV13 `json:"payload"`
}

func (TestOutputEventV13) isTaskEventV13() {}

type TestItemFinishedEventV13 struct {
	TaskEventBaseV13
	Event   TaskEventNameV13 `json:"event"`
	Payload struct {
		RunID  string                              `json:"runId"`
		Result protocolmodelv13test.TestItemResult `json:"result"`
	} `json:"payload"`
}

func (TestItemFinishedEventV13) isTaskEventV13() {}

type TestContainerFinishedEventV13 struct {
	TaskEventBaseV13
	Event   TaskEventNameV13                `json:"event"`
	Payload TestContainerFinishedPayloadV13 `json:"payload"`
}

func (TestContainerFinishedEventV13) isTaskEventV13() {}

type TestRunFinishedEventV13 struct {
	TaskEventBaseV13
	Event   TaskEventNameV13          `json:"event"`
	Payload TestRunFinishedPayloadV13 `json:"payload"`
}

func (TestRunFinishedEventV13) isTaskEventV13() {}

type TaskStepStartedPayloadV13 struct {
	StepID string            `json:"stepId"`
	Kind   TaskStepKindV13   `json:"kind"`
	Status TaskStepStatusV13 `json:"status"`
}
type TaskOutputPayloadV13 struct {
	StepID    string              `json:"stepId"`
	Stream    TaskOutputStreamV13 `json:"stream"`
	Text      string              `json:"text"`
	Truncated bool                `json:"truncated"`
}
type TaskStepFinishedPayloadV13 struct {
	StepID    string            `json:"stepId"`
	Kind      TaskStepKindV13   `json:"kind"`
	Status    TaskStepStatusV13 `json:"status"`
	ExitCode  *int64            `json:"exitCode,omitempty"`
	ErrorCode *string           `json:"errorCode,omitempty"`
}
type ArtifactCreatedPayloadV13 struct {
	ArtifactID string `json:"artifactId"`
	Kind       string `json:"kind"`
}
type TestDiscoveryStartedPayloadV13 struct {
	ProjectID string `json:"projectId"`
	ProfileID string `json:"profileId"`
}
type TestContainerDiscoveredPayloadV13 struct {
	ContainerID string                `json:"containerId"`
	Framework   TestEventFrameworkV13 `json:"framework"`
	DisplayName string                `json:"displayName"`
}
type TestCatalogPublishedPayloadV13 struct {
	ProjectID      string `json:"projectId"`
	ProfileID      string `json:"profileId"`
	Revision       string `json:"revision"`
	ContainerCount int64  `json:"containerCount"`
	ItemCount      int64  `json:"itemCount"`
}
type TestRunStartedPayloadV13 struct {
	RunID           string `json:"runId"`
	CatalogRevision string `json:"catalogRevision"`
	Total           int64  `json:"total"`
}
type TestContainerStartedPayloadV13 struct {
	RunID       string `json:"runId"`
	ContainerID string `json:"containerId"`
	Iteration   int64  `json:"iteration"`
}
type TestItemStartedPayloadV13 struct {
	RunID       string `json:"runId"`
	ItemID      string `json:"itemId"`
	ContainerID string `json:"containerId"`
	Iteration   int64  `json:"iteration"`
}
type TestOutputPayloadV13 struct {
	RunID       string              `json:"runId"`
	ContainerID string              `json:"containerId"`
	ItemID      *string             `json:"itemId,omitempty"`
	Iteration   int64               `json:"iteration"`
	Stream      TaskOutputStreamV13 `json:"stream"`
	Text        string              `json:"text"`
	Truncated   bool                `json:"truncated"`
}
type TestContainerFinishedPayloadV13 struct {
	RunID       string                  `json:"runId"`
	ContainerID string                  `json:"containerId"`
	Iteration   int64                   `json:"iteration"`
	Outcome     TestContainerOutcomeV13 `json:"outcome"`
}
type TestRunFinishedPayloadV13 struct {
	RunID          string                                 `json:"runId"`
	Outcome        protocolmodelv13test.TestRunOutcomeV13 `json:"outcome"`
	Summary        protocolmodelv13test.TestRunSummaryV13 `json:"summary"`
	ResultRevision string                                 `json:"resultRevision"`
	Incomplete     bool                                   `json:"incomplete"`
}
type EventProtocolVersionV13 string

const The13 EventProtocolVersionV13 = "1.3"

type EventKindV13 string

const Event EventKindV13 = "event"

type TaskEventNameV13 string

const (
	TaskCreated               TaskEventNameV13 = "task.created"
	TaskStarted               TaskEventNameV13 = "task.started"
	TaskStepStarted           TaskEventNameV13 = "task.step_started"
	TaskOutput                TaskEventNameV13 = "task.output"
	TaskStepFinished          TaskEventNameV13 = "task.step_finished"
	TaskCancellationRequested TaskEventNameV13 = "task.cancellation_requested"
	ArtifactCreated           TaskEventNameV13 = "artifact.created"
	TaskFinished              TaskEventNameV13 = "task.finished"
	TaskDiagnostic            TaskEventNameV13 = "task.diagnostic"
	TestDiscoveryStarted      TaskEventNameV13 = "test.discovery.started"
	TestContainerDiscovered   TaskEventNameV13 = "test.container.discovered"
	TestCatalogPublished      TaskEventNameV13 = "test.catalog.published"
	TestRunStarted            TaskEventNameV13 = "test.run.started"
	TestContainerStarted      TaskEventNameV13 = "test.container.started"
	TestItemStarted           TaskEventNameV13 = "test.item.started"
	TestOutput                TaskEventNameV13 = "test.output"
	TestItemFinished          TaskEventNameV13 = "test.item.finished"
	TestContainerFinished     TaskEventNameV13 = "test.container.finished"
	TestRunFinished           TaskEventNameV13 = "test.run.finished"
)

type TaskStepKindV13 string

const (
	StepSimulationV13    TaskStepKindV13 = "simulation"
	StepConfigureV13     TaskStepKindV13 = "configure"
	StepBuildV13         TaskStepKindV13 = "build"
	StepTestDiscoveryV13 TaskStepKindV13 = "test-discovery"
	StepTestRunV13       TaskStepKindV13 = "test-run"
)

type TaskStepStatusV13 string

const (
	StepRunningV13   TaskStepStatusV13 = "running"
	StepSucceededV13 TaskStepStatusV13 = "succeeded"
	StepFailedV13    TaskStepStatusV13 = "failed"
	StepSkippedV13   TaskStepStatusV13 = "skipped"
)

type TaskOutputStreamV13 string

const (
	OutputStdoutV13   TaskOutputStreamV13 = "stdout"
	OutputStderrV13   TaskOutputStreamV13 = "stderr"
	OutputCombinedV13 TaskOutputStreamV13 = "combined"
)

type TaskOutcomeV13 string

const (
	OutcomeSucceededV13            TaskOutcomeV13 = "succeeded"
	OutcomeCommandFailedV13        TaskOutcomeV13 = "command_failed"
	OutcomeCancelledV13            TaskOutcomeV13 = "cancelled"
	OutcomeTimedOutV13             TaskOutcomeV13 = "timed_out"
	OutcomeInterruptedV13          TaskOutcomeV13 = "interrupted"
	OutcomeInfrastructureFailedV13 TaskOutcomeV13 = "infrastructure_failed"
)

type TestEventFrameworkV13 string

const (
	FrameworkCppUTestV13    TestEventFrameworkV13 = "cpputest"
	FrameworkUnityV13       TestEventFrameworkV13 = "unity"
	FrameworkOpaqueCTestV13 TestEventFrameworkV13 = "opaque-ctest"
)

type TestContainerOutcomeV13 string

const (
	ContainerPassedV13    TestContainerOutcomeV13 = "passed"
	ContainerFailedV13    TestContainerOutcomeV13 = "failed"
	ContainerErroredV13   TestContainerOutcomeV13 = "errored"
	ContainerCancelledV13 TestContainerOutcomeV13 = "cancelled"
	ContainerTimedOutV13  TestContainerOutcomeV13 = "timed_out"
	ContainerNotRunV13    TestContainerOutcomeV13 = "not_run"
)
