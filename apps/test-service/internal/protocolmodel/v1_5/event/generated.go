package protocolmodelv15event

import (
	"time"

	protocolmodelv15coverage "unit-test-ide.local/test-service/internal/protocolmodel/v1_5/coverage"
	protocolmodelv15diagnostic "unit-test-ide.local/test-service/internal/protocolmodel/v1_5/diagnostic"
	protocolmodelv15test "unit-test-ide.local/test-service/internal/protocolmodel/v1_5/test"
	protocolmodelv15testgeneration "unit-test-ide.local/test-service/internal/protocolmodel/v1_5/testgeneration"
)

type TaskEventV15 interface{ isTaskEventV15() }
type CoverageEventV15 interface{ isCoverageEventV15() }
type TaskEventBaseV15 struct {
	ProtocolVersion EventProtocolVersionV15 `json:"protocolVersion"`
	Kind            EventKindV15            `json:"kind"`
	MessageID       string                  `json:"messageId"`
	SentAt          time.Time               `json:"sentAt"`
	Sequence        int64                   `json:"sequence"`
	TaskID          string                  `json:"taskId"`
	PayloadVersion  int64                   `json:"payloadVersion"`
}
type TaskCreatedEventV15 struct {
	TaskEventBaseV15
	Event   TaskEventNameV15      `json:"event"`
	Payload TaskCreatedPayloadV15 `json:"payload"`
}

func (TaskCreatedEventV15) isTaskEventV15() {}

type TaskStartedEventV15 struct {
	TaskEventBaseV15
	Event   TaskEventNameV15      `json:"event"`
	Payload TaskStartedPayloadV15 `json:"payload"`
}

func (TaskStartedEventV15) isTaskEventV15() {}

type TaskStepStartedEventV15 struct {
	TaskEventBaseV15
	Event   TaskEventNameV15          `json:"event"`
	Payload TaskStepStartedPayloadV15 `json:"payload"`
}

func (TaskStepStartedEventV15) isTaskEventV15() {}

type TaskOutputEventV15 struct {
	TaskEventBaseV15
	Event   TaskEventNameV15     `json:"event"`
	Payload TaskOutputPayloadV15 `json:"payload"`
}

func (TaskOutputEventV15) isTaskEventV15() {}

type TaskStepFinishedEventV15 struct {
	TaskEventBaseV15
	Event   TaskEventNameV15           `json:"event"`
	Payload TaskStepFinishedPayloadV15 `json:"payload"`
}

func (TaskStepFinishedEventV15) isTaskEventV15() {}

type TaskCancellationRequestedEventV15 struct {
	TaskEventBaseV15
	Event   TaskEventNameV15                    `json:"event"`
	Payload TaskCancellationRequestedPayloadV15 `json:"payload"`
}

func (TaskCancellationRequestedEventV15) isTaskEventV15() {}

type ArtifactCreatedEventV15 struct {
	TaskEventBaseV15
	Event   TaskEventNameV15          `json:"event"`
	Payload ArtifactCreatedPayloadV15 `json:"payload"`
}

func (ArtifactCreatedEventV15) isTaskEventV15() {}

type TaskFinishedEventV15 struct {
	TaskEventBaseV15
	Event   TaskEventNameV15       `json:"event"`
	Payload TaskFinishedPayloadV15 `json:"payload"`
}

func (TaskFinishedEventV15) isTaskEventV15() {}

type TaskDiagnosticEventV15 struct {
	TaskEventBaseV15
	Event   TaskEventNameV15         `json:"event"`
	Payload TaskDiagnosticPayloadV15 `json:"payload"`
}

func (TaskDiagnosticEventV15) isTaskEventV15() {}

type TestDiscoveryStartedEventV15 struct {
	TaskEventBaseV15
	Event   TaskEventNameV15               `json:"event"`
	Payload TestDiscoveryStartedPayloadV15 `json:"payload"`
}

func (TestDiscoveryStartedEventV15) isTaskEventV15() {}

type TestContainerDiscoveredEventV15 struct {
	TaskEventBaseV15
	Event   TaskEventNameV15                  `json:"event"`
	Payload TestContainerDiscoveredPayloadV15 `json:"payload"`
}

func (TestContainerDiscoveredEventV15) isTaskEventV15() {}

type TestCatalogPublishedEventV15 struct {
	TaskEventBaseV15
	Event   TaskEventNameV15               `json:"event"`
	Payload TestCatalogPublishedPayloadV15 `json:"payload"`
}

func (TestCatalogPublishedEventV15) isTaskEventV15() {}

type TestRunStartedEventV15 struct {
	TaskEventBaseV15
	Event   TaskEventNameV15         `json:"event"`
	Payload TestRunStartedPayloadV15 `json:"payload"`
}

func (TestRunStartedEventV15) isTaskEventV15() {}

type TestContainerStartedEventV15 struct {
	TaskEventBaseV15
	Event   TaskEventNameV15               `json:"event"`
	Payload TestContainerStartedPayloadV15 `json:"payload"`
}

func (TestContainerStartedEventV15) isTaskEventV15() {}

type TestItemStartedEventV15 struct {
	TaskEventBaseV15
	Event   TaskEventNameV15          `json:"event"`
	Payload TestItemStartedPayloadV15 `json:"payload"`
}

func (TestItemStartedEventV15) isTaskEventV15() {}

type TestOutputEventV15 struct {
	TaskEventBaseV15
	Event   TaskEventNameV15     `json:"event"`
	Payload TestOutputPayloadV15 `json:"payload"`
}

func (TestOutputEventV15) isTaskEventV15() {}

type TestItemFinishedEventV15 struct {
	TaskEventBaseV15
	Event   TaskEventNameV15           `json:"event"`
	Payload TestItemFinishedPayloadV15 `json:"payload"`
}

func (TestItemFinishedEventV15) isTaskEventV15() {}

type TestContainerFinishedEventV15 struct {
	TaskEventBaseV15
	Event   TaskEventNameV15                `json:"event"`
	Payload TestContainerFinishedPayloadV15 `json:"payload"`
}

func (TestContainerFinishedEventV15) isTaskEventV15() {}

type TestRunFinishedEventV15 struct {
	TaskEventBaseV15
	Event   TaskEventNameV15          `json:"event"`
	Payload TestRunFinishedPayloadV15 `json:"payload"`
}

func (TestRunFinishedEventV15) isTaskEventV15() {}

type CoverageRunStartedEventV15 struct {
	TaskEventBaseV15
	Event   TaskEventNameV15             `json:"event"`
	Payload CoverageRunStartedPayloadV15 `json:"payload"`
}

func (CoverageRunStartedEventV15) isTaskEventV15()     {}
func (CoverageRunStartedEventV15) isCoverageEventV15() {}

type CoverageBuildFinishedEventV15 struct {
	TaskEventBaseV15
	Event   TaskEventNameV15                `json:"event"`
	Payload CoverageBuildFinishedPayloadV15 `json:"payload"`
}

func (CoverageBuildFinishedEventV15) isTaskEventV15()     {}
func (CoverageBuildFinishedEventV15) isCoverageEventV15() {}

type CoverageCollectionStartedEventV15 struct {
	TaskEventBaseV15
	Event   TaskEventNameV15                    `json:"event"`
	Payload CoverageCollectionStartedPayloadV15 `json:"payload"`
}

func (CoverageCollectionStartedEventV15) isTaskEventV15()     {}
func (CoverageCollectionStartedEventV15) isCoverageEventV15() {}

type CoverageReportAvailableEventV15 struct {
	TaskEventBaseV15
	Event   TaskEventNameV15                  `json:"event"`
	Payload CoverageReportAvailablePayloadV15 `json:"payload"`
}

func (CoverageReportAvailableEventV15) isTaskEventV15()     {}
func (CoverageReportAvailableEventV15) isCoverageEventV15() {}

type CoverageRunFinishedEventV15 struct {
	TaskEventBaseV15
	Event   TaskEventNameV15              `json:"event"`
	Payload CoverageRunFinishedPayloadV15 `json:"payload"`
}

func (CoverageRunFinishedEventV15) isTaskEventV15()     {}
func (CoverageRunFinishedEventV15) isCoverageEventV15() {}

type TaskCreatedPayloadV15 struct {
	Status string `json:"status"`
}
type TaskStartedPayloadV15 struct {
	Status string `json:"status"`
}
type TaskStepStartedPayloadV15 struct {
	StepID string          `json:"stepId"`
	Kind   TaskStepKindV15 `json:"kind"`
	Status string          `json:"status"`
}
type TaskOutputPayloadV15 struct {
	StepID    string              `json:"stepId"`
	Stream    TaskOutputStreamV15 `json:"stream"`
	Text      string              `json:"text"`
	Truncated bool                `json:"truncated"`
}
type TaskStepFinishedPayloadV15 struct {
	StepID    string                    `json:"stepId"`
	Kind      TaskStepKindV15           `json:"kind"`
	Status    TaskStepFinishedStatusV15 `json:"status"`
	ExitCode  *int64                    `json:"exitCode,omitempty"`
	ErrorCode *string                   `json:"errorCode,omitempty"`
}
type TaskCancellationRequestedPayloadV15 struct {
	Status string `json:"status"`
}
type ArtifactCreatedPayloadV15 struct {
	ArtifactID string `json:"artifactId"`
	Kind       string `json:"kind"`
}
type TaskFinishedPayloadV15 struct {
	Outcome TaskOutcomeV15 `json:"outcome"`
}
type TaskDiagnosticPayloadV15 struct {
	Diagnostic protocolmodelv15diagnostic.DiagnosticV15 `json:"diagnostic"`
}
type TestDiscoveryStartedPayloadV15 struct {
	ProjectID string `json:"projectId"`
	ProfileID string `json:"profileId"`
}
type TestContainerDiscoveredPayloadV15 struct {
	ContainerID string                `json:"containerId"`
	Framework   TestEventFrameworkV15 `json:"framework"`
	DisplayName string                `json:"displayName"`
}
type TestCatalogPublishedPayloadV15 struct {
	ProjectID      string `json:"projectId"`
	ProfileID      string `json:"profileId"`
	Revision       string `json:"revision"`
	ContainerCount int64  `json:"containerCount"`
	ItemCount      int64  `json:"itemCount"`
}
type TestRunStartedPayloadV15 struct {
	RunID           string `json:"runId"`
	CatalogRevision string `json:"catalogRevision"`
	Total           int64  `json:"total"`
}
type TestContainerStartedPayloadV15 struct {
	RunID       string `json:"runId"`
	ContainerID string `json:"containerId"`
	Iteration   int64  `json:"iteration"`
}
type TestItemStartedPayloadV15 struct {
	RunID       string `json:"runId"`
	ItemID      string `json:"itemId"`
	ContainerID string `json:"containerId"`
	Iteration   int64  `json:"iteration"`
}
type TestOutputPayloadV15 struct {
	RunID       string              `json:"runId"`
	ContainerID string              `json:"containerId"`
	ItemID      *string             `json:"itemId,omitempty"`
	Iteration   int64               `json:"iteration"`
	Stream      TaskOutputStreamV15 `json:"stream"`
	Text        string              `json:"text"`
	Truncated   bool                `json:"truncated"`
}
type TestItemFinishedPayloadV15 struct {
	RunID  string                              `json:"runId"`
	Result protocolmodelv15test.TestItemResult `json:"result"`
}
type TestContainerFinishedPayloadV15 struct {
	RunID       string                  `json:"runId"`
	ContainerID string                  `json:"containerId"`
	Iteration   int64                   `json:"iteration"`
	Outcome     TestContainerOutcomeV15 `json:"outcome"`
}
type TestRunFinishedPayloadV15 struct {
	RunID          string                                 `json:"runId"`
	Outcome        protocolmodelv15test.TestRunOutcomeV15 `json:"outcome"`
	Summary        protocolmodelv15test.TestRunSummaryV15 `json:"summary"`
	ResultRevision string                                 `json:"resultRevision"`
	Incomplete     bool                                   `json:"incomplete"`
}
type CoverageRunStartedPayloadV15 struct {
	CoverageRunID   string `json:"coverageRunId"`
	TestRunID       string `json:"testRunId"`
	CatalogRevision string `json:"catalogRevision"`
	RepeatCount     int64  `json:"repeatCount"`
}
type CoverageBuildFinishedPayloadV15 struct {
	CoverageRunID string `json:"coverageRunId"`
}
type CoverageCollectionStartedPayloadV15 struct {
	CoverageRunID string `json:"coverageRunId"`
	TestRunID     string `json:"testRunId"`
}
type CoverageReportAvailablePayloadV15 struct {
	CoverageRunID string                                           `json:"coverageRunId"`
	ReportID      string                                           `json:"reportId"`
	ArtifactID    string                                           `json:"artifactId"`
	Completeness  protocolmodelv15coverage.CoverageCompletenessV15 `json:"completeness"`
	Summary       protocolmodelv15coverage.CoverageSummaryV15      `json:"summary"`
}
type CoverageRunFinishedPayloadV15 struct {
	CoverageRunID string                                         `json:"coverageRunId"`
	Outcome       protocolmodelv15coverage.CoverageRunOutcomeV15 `json:"outcome"`
	Reason        *protocolmodelv15coverage.CoverageRunReasonV15 `json:"reason,omitempty"`
	ReportID      *string                                        `json:"reportId,omitempty"`
}
type TestGenerationStateChangedEventV15 struct {
	TaskEventBaseV15
	Event   TaskEventNameV15                     `json:"event"`
	Payload TestGenerationStateChangedPayloadV15 `json:"payload"`
}

func (TestGenerationStateChangedEventV15) isTaskEventV15() {}

type TestGenerationStateChangedPayloadV15 struct {
	RunID string                                                `json:"runId"`
	From  protocolmodelv15testgeneration.TestGenerationStateV15 `json:"from"`
	To    protocolmodelv15testgeneration.TestGenerationStateV15 `json:"to"`
}
type EventProtocolVersionV15 string

const EventProtocol15V15 EventProtocolVersionV15 = "1.5"

type EventKindV15 string

const EventV15 EventKindV15 = "event"

type TaskEventNameV15 string

const (
	TaskCreatedV15                TaskEventNameV15 = "task.created"
	TaskStartedV15                TaskEventNameV15 = "task.started"
	TaskStepStartedV15            TaskEventNameV15 = "task.step_started"
	TaskOutputV15                 TaskEventNameV15 = "task.output"
	TaskStepFinishedV15           TaskEventNameV15 = "task.step_finished"
	TaskCancellationRequestedV15  TaskEventNameV15 = "task.cancellation_requested"
	ArtifactCreatedV15            TaskEventNameV15 = "artifact.created"
	TaskFinishedV15               TaskEventNameV15 = "task.finished"
	TaskDiagnosticV15             TaskEventNameV15 = "task.diagnostic"
	TestDiscoveryStartedV15       TaskEventNameV15 = "test.discovery.started"
	TestContainerDiscoveredV15    TaskEventNameV15 = "test.container.discovered"
	TestCatalogPublishedV15       TaskEventNameV15 = "test.catalog.published"
	TestRunStartedV15             TaskEventNameV15 = "test.run.started"
	TestContainerStartedV15       TaskEventNameV15 = "test.container.started"
	TestItemStartedV15            TaskEventNameV15 = "test.item.started"
	TestOutputV15                 TaskEventNameV15 = "test.output"
	TestItemFinishedV15           TaskEventNameV15 = "test.item.finished"
	TestContainerFinishedV15      TaskEventNameV15 = "test.container.finished"
	TestRunFinishedV15            TaskEventNameV15 = "test.run.finished"
	CoverageRunStartedV15         TaskEventNameV15 = "coverage.run.started"
	CoverageBuildFinishedV15      TaskEventNameV15 = "coverage.build.finished"
	CoverageCollectionStartedV15  TaskEventNameV15 = "coverage.collection.started"
	CoverageReportAvailableV15    TaskEventNameV15 = "coverage.report.available"
	CoverageRunFinishedV15        TaskEventNameV15 = "coverage.run.finished"
	TestGenerationStateChangedV15 TaskEventNameV15 = "testGeneration.state.changed"
)

type TaskStepKindV15 string

const (
	StepSimulationV15        TaskStepKindV15 = "simulation"
	StepConfigureV15         TaskStepKindV15 = "configure"
	StepBuildV15             TaskStepKindV15 = "build"
	StepTestDiscoveryV15     TaskStepKindV15 = "test-discovery"
	StepTestRunV15           TaskStepKindV15 = "test-run"
	StepCoverageConfigureV15 TaskStepKindV15 = "coverage-configure"
	StepCoverageBuildV15     TaskStepKindV15 = "coverage-build"
	StepCoverageTestV15      TaskStepKindV15 = "coverage-test"
	StepCoverageMergeV15     TaskStepKindV15 = "coverage-merge"
	StepCoverageNormalizeV15 TaskStepKindV15 = "coverage-normalize"
	StepCoverageReportV15    TaskStepKindV15 = "coverage-report"
	StepCoveragePublishV15   TaskStepKindV15 = "coverage-publish"
)

type TaskStepFinishedStatusV15 string

const (
	StepSucceededV15 TaskStepFinishedStatusV15 = "succeeded"
	StepFailedV15    TaskStepFinishedStatusV15 = "failed"
	StepSkippedV15   TaskStepFinishedStatusV15 = "skipped"
)

type TaskOutputStreamV15 string

const (
	OutputStdoutV15   TaskOutputStreamV15 = "stdout"
	OutputStderrV15   TaskOutputStreamV15 = "stderr"
	OutputCombinedV15 TaskOutputStreamV15 = "combined"
)

type TaskOutcomeV15 string

const (
	OutcomeSucceededV15            TaskOutcomeV15 = "succeeded"
	OutcomeCommandFailedV15        TaskOutcomeV15 = "command_failed"
	OutcomeCancelledV15            TaskOutcomeV15 = "cancelled"
	OutcomeTimedOutV15             TaskOutcomeV15 = "timed_out"
	OutcomeInterruptedV15          TaskOutcomeV15 = "interrupted"
	OutcomeInfrastructureFailedV15 TaskOutcomeV15 = "infrastructure_failed"
)

type TestEventFrameworkV15 string

const (
	FrameworkCppUTestV15    TestEventFrameworkV15 = "cpputest"
	FrameworkUnityV15       TestEventFrameworkV15 = "unity"
	FrameworkOpaqueCTestV15 TestEventFrameworkV15 = "opaque-ctest"
)

type TestContainerOutcomeV15 string

const (
	ContainerPassedV15    TestContainerOutcomeV15 = "passed"
	ContainerFailedV15    TestContainerOutcomeV15 = "failed"
	ContainerErroredV15   TestContainerOutcomeV15 = "errored"
	ContainerCancelledV15 TestContainerOutcomeV15 = "cancelled"
	ContainerTimedOutV15  TestContainerOutcomeV15 = "timed_out"
	ContainerNotRunV15    TestContainerOutcomeV15 = "not_run"
)
