package protocolmodelv16event

import (
	"time"

	protocolmodelv16coverage "unit-test-ide.local/test-service/internal/protocolmodel/v1_6/coverage"
	protocolmodelv16diagnostic "unit-test-ide.local/test-service/internal/protocolmodel/v1_6/diagnostic"
	protocolmodelv16test "unit-test-ide.local/test-service/internal/protocolmodel/v1_6/test"
	protocolmodelv16testgeneration "unit-test-ide.local/test-service/internal/protocolmodel/v1_6/testgeneration"
)

type TaskEventV16 interface{ isTaskEventV16() }
type CoverageEventV16 interface{ isCoverageEventV16() }
type TaskEventBaseV16 struct {
	ProtocolVersion EventProtocolVersionV16 `json:"protocolVersion"`
	Kind            EventKindV16            `json:"kind"`
	MessageID       string                  `json:"messageId"`
	SentAt          time.Time               `json:"sentAt"`
	Sequence        int64                   `json:"sequence"`
	TaskID          string                  `json:"taskId"`
	PayloadVersion  int64                   `json:"payloadVersion"`
}
type TaskCreatedEventV16 struct {
	TaskEventBaseV16
	Event   TaskEventNameV16      `json:"event"`
	Payload TaskCreatedPayloadV16 `json:"payload"`
}

func (TaskCreatedEventV16) isTaskEventV16() {}

type TaskStartedEventV16 struct {
	TaskEventBaseV16
	Event   TaskEventNameV16      `json:"event"`
	Payload TaskStartedPayloadV16 `json:"payload"`
}

func (TaskStartedEventV16) isTaskEventV16() {}

type TaskStepStartedEventV16 struct {
	TaskEventBaseV16
	Event   TaskEventNameV16          `json:"event"`
	Payload TaskStepStartedPayloadV16 `json:"payload"`
}

func (TaskStepStartedEventV16) isTaskEventV16() {}

type TaskOutputEventV16 struct {
	TaskEventBaseV16
	Event   TaskEventNameV16     `json:"event"`
	Payload TaskOutputPayloadV16 `json:"payload"`
}

func (TaskOutputEventV16) isTaskEventV16() {}

type TaskStepFinishedEventV16 struct {
	TaskEventBaseV16
	Event   TaskEventNameV16           `json:"event"`
	Payload TaskStepFinishedPayloadV16 `json:"payload"`
}

func (TaskStepFinishedEventV16) isTaskEventV16() {}

type TaskCancellationRequestedEventV16 struct {
	TaskEventBaseV16
	Event   TaskEventNameV16                    `json:"event"`
	Payload TaskCancellationRequestedPayloadV16 `json:"payload"`
}

func (TaskCancellationRequestedEventV16) isTaskEventV16() {}

type ArtifactCreatedEventV16 struct {
	TaskEventBaseV16
	Event   TaskEventNameV16          `json:"event"`
	Payload ArtifactCreatedPayloadV16 `json:"payload"`
}

func (ArtifactCreatedEventV16) isTaskEventV16() {}

type TaskFinishedEventV16 struct {
	TaskEventBaseV16
	Event   TaskEventNameV16       `json:"event"`
	Payload TaskFinishedPayloadV16 `json:"payload"`
}

func (TaskFinishedEventV16) isTaskEventV16() {}

type TaskDiagnosticEventV16 struct {
	TaskEventBaseV16
	Event   TaskEventNameV16         `json:"event"`
	Payload TaskDiagnosticPayloadV16 `json:"payload"`
}

func (TaskDiagnosticEventV16) isTaskEventV16() {}

type TestDiscoveryStartedEventV16 struct {
	TaskEventBaseV16
	Event   TaskEventNameV16               `json:"event"`
	Payload TestDiscoveryStartedPayloadV16 `json:"payload"`
}

func (TestDiscoveryStartedEventV16) isTaskEventV16() {}

type TestContainerDiscoveredEventV16 struct {
	TaskEventBaseV16
	Event   TaskEventNameV16                  `json:"event"`
	Payload TestContainerDiscoveredPayloadV16 `json:"payload"`
}

func (TestContainerDiscoveredEventV16) isTaskEventV16() {}

type TestCatalogPublishedEventV16 struct {
	TaskEventBaseV16
	Event   TaskEventNameV16               `json:"event"`
	Payload TestCatalogPublishedPayloadV16 `json:"payload"`
}

func (TestCatalogPublishedEventV16) isTaskEventV16() {}

type TestRunStartedEventV16 struct {
	TaskEventBaseV16
	Event   TaskEventNameV16         `json:"event"`
	Payload TestRunStartedPayloadV16 `json:"payload"`
}

func (TestRunStartedEventV16) isTaskEventV16() {}

type TestContainerStartedEventV16 struct {
	TaskEventBaseV16
	Event   TaskEventNameV16               `json:"event"`
	Payload TestContainerStartedPayloadV16 `json:"payload"`
}

func (TestContainerStartedEventV16) isTaskEventV16() {}

type TestItemStartedEventV16 struct {
	TaskEventBaseV16
	Event   TaskEventNameV16          `json:"event"`
	Payload TestItemStartedPayloadV16 `json:"payload"`
}

func (TestItemStartedEventV16) isTaskEventV16() {}

type TestOutputEventV16 struct {
	TaskEventBaseV16
	Event   TaskEventNameV16     `json:"event"`
	Payload TestOutputPayloadV16 `json:"payload"`
}

func (TestOutputEventV16) isTaskEventV16() {}

type TestItemFinishedEventV16 struct {
	TaskEventBaseV16
	Event   TaskEventNameV16           `json:"event"`
	Payload TestItemFinishedPayloadV16 `json:"payload"`
}

func (TestItemFinishedEventV16) isTaskEventV16() {}

type TestContainerFinishedEventV16 struct {
	TaskEventBaseV16
	Event   TaskEventNameV16                `json:"event"`
	Payload TestContainerFinishedPayloadV16 `json:"payload"`
}

func (TestContainerFinishedEventV16) isTaskEventV16() {}

type TestRunFinishedEventV16 struct {
	TaskEventBaseV16
	Event   TaskEventNameV16          `json:"event"`
	Payload TestRunFinishedPayloadV16 `json:"payload"`
}

func (TestRunFinishedEventV16) isTaskEventV16() {}

type CoverageRunStartedEventV16 struct {
	TaskEventBaseV16
	Event   TaskEventNameV16             `json:"event"`
	Payload CoverageRunStartedPayloadV16 `json:"payload"`
}

func (CoverageRunStartedEventV16) isTaskEventV16()     {}
func (CoverageRunStartedEventV16) isCoverageEventV16() {}

type CoverageBuildFinishedEventV16 struct {
	TaskEventBaseV16
	Event   TaskEventNameV16                `json:"event"`
	Payload CoverageBuildFinishedPayloadV16 `json:"payload"`
}

func (CoverageBuildFinishedEventV16) isTaskEventV16()     {}
func (CoverageBuildFinishedEventV16) isCoverageEventV16() {}

type CoverageCollectionStartedEventV16 struct {
	TaskEventBaseV16
	Event   TaskEventNameV16                    `json:"event"`
	Payload CoverageCollectionStartedPayloadV16 `json:"payload"`
}

func (CoverageCollectionStartedEventV16) isTaskEventV16()     {}
func (CoverageCollectionStartedEventV16) isCoverageEventV16() {}

type CoverageReportAvailableEventV16 struct {
	TaskEventBaseV16
	Event   TaskEventNameV16                  `json:"event"`
	Payload CoverageReportAvailablePayloadV16 `json:"payload"`
}

func (CoverageReportAvailableEventV16) isTaskEventV16()     {}
func (CoverageReportAvailableEventV16) isCoverageEventV16() {}

type CoverageRunFinishedEventV16 struct {
	TaskEventBaseV16
	Event   TaskEventNameV16              `json:"event"`
	Payload CoverageRunFinishedPayloadV16 `json:"payload"`
}

func (CoverageRunFinishedEventV16) isTaskEventV16()     {}
func (CoverageRunFinishedEventV16) isCoverageEventV16() {}

type TaskCreatedPayloadV16 struct {
	Status string `json:"status"`
}
type TaskStartedPayloadV16 struct {
	Status string `json:"status"`
}
type TaskStepStartedPayloadV16 struct {
	StepID string          `json:"stepId"`
	Kind   TaskStepKindV16 `json:"kind"`
	Status string          `json:"status"`
}
type TaskOutputPayloadV16 struct {
	StepID    string              `json:"stepId"`
	Stream    TaskOutputStreamV16 `json:"stream"`
	Text      string              `json:"text"`
	Truncated bool                `json:"truncated"`
}
type TaskStepFinishedPayloadV16 struct {
	StepID    string                    `json:"stepId"`
	Kind      TaskStepKindV16           `json:"kind"`
	Status    TaskStepFinishedStatusV16 `json:"status"`
	ExitCode  *int64                    `json:"exitCode,omitempty"`
	ErrorCode *string                   `json:"errorCode,omitempty"`
}
type TaskCancellationRequestedPayloadV16 struct {
	Status string `json:"status"`
}
type ArtifactCreatedPayloadV16 struct {
	ArtifactID string `json:"artifactId"`
	Kind       string `json:"kind"`
}
type TaskFinishedPayloadV16 struct {
	Outcome TaskOutcomeV16 `json:"outcome"`
}
type TaskDiagnosticPayloadV16 struct {
	Diagnostic protocolmodelv16diagnostic.DiagnosticV16 `json:"diagnostic"`
}
type TestDiscoveryStartedPayloadV16 struct {
	ProjectID string `json:"projectId"`
	ProfileID string `json:"profileId"`
}
type TestContainerDiscoveredPayloadV16 struct {
	ContainerID string                `json:"containerId"`
	Framework   TestEventFrameworkV16 `json:"framework"`
	DisplayName string                `json:"displayName"`
}
type TestCatalogPublishedPayloadV16 struct {
	ProjectID      string `json:"projectId"`
	ProfileID      string `json:"profileId"`
	Revision       string `json:"revision"`
	ContainerCount int64  `json:"containerCount"`
	ItemCount      int64  `json:"itemCount"`
}
type TestRunStartedPayloadV16 struct {
	RunID           string `json:"runId"`
	CatalogRevision string `json:"catalogRevision"`
	Total           int64  `json:"total"`
}
type TestContainerStartedPayloadV16 struct {
	RunID       string `json:"runId"`
	ContainerID string `json:"containerId"`
	Iteration   int64  `json:"iteration"`
}
type TestItemStartedPayloadV16 struct {
	RunID       string `json:"runId"`
	ItemID      string `json:"itemId"`
	ContainerID string `json:"containerId"`
	Iteration   int64  `json:"iteration"`
}
type TestOutputPayloadV16 struct {
	RunID       string              `json:"runId"`
	ContainerID string              `json:"containerId"`
	ItemID      *string             `json:"itemId,omitempty"`
	Iteration   int64               `json:"iteration"`
	Stream      TaskOutputStreamV16 `json:"stream"`
	Text        string              `json:"text"`
	Truncated   bool                `json:"truncated"`
}
type TestItemFinishedPayloadV16 struct {
	RunID  string                              `json:"runId"`
	Result protocolmodelv16test.TestItemResult `json:"result"`
}
type TestContainerFinishedPayloadV16 struct {
	RunID       string                  `json:"runId"`
	ContainerID string                  `json:"containerId"`
	Iteration   int64                   `json:"iteration"`
	Outcome     TestContainerOutcomeV16 `json:"outcome"`
}
type TestRunFinishedPayloadV16 struct {
	RunID          string                                 `json:"runId"`
	Outcome        protocolmodelv16test.TestRunOutcomeV16 `json:"outcome"`
	Summary        protocolmodelv16test.TestRunSummaryV16 `json:"summary"`
	ResultRevision string                                 `json:"resultRevision"`
	Incomplete     bool                                   `json:"incomplete"`
}
type CoverageRunStartedPayloadV16 struct {
	CoverageRunID   string `json:"coverageRunId"`
	TestRunID       string `json:"testRunId"`
	CatalogRevision string `json:"catalogRevision"`
	RepeatCount     int64  `json:"repeatCount"`
}
type CoverageBuildFinishedPayloadV16 struct {
	CoverageRunID string `json:"coverageRunId"`
}
type CoverageCollectionStartedPayloadV16 struct {
	CoverageRunID string `json:"coverageRunId"`
	TestRunID     string `json:"testRunId"`
}
type CoverageReportAvailablePayloadV16 struct {
	CoverageRunID string                                           `json:"coverageRunId"`
	ReportID      string                                           `json:"reportId"`
	ArtifactID    string                                           `json:"artifactId"`
	Completeness  protocolmodelv16coverage.CoverageCompletenessV16 `json:"completeness"`
	Summary       protocolmodelv16coverage.CoverageSummaryV16      `json:"summary"`
}
type CoverageRunFinishedPayloadV16 struct {
	CoverageRunID string                                         `json:"coverageRunId"`
	Outcome       protocolmodelv16coverage.CoverageRunOutcomeV16 `json:"outcome"`
	Reason        *protocolmodelv16coverage.CoverageRunReasonV16 `json:"reason,omitempty"`
	ReportID      *string                                        `json:"reportId,omitempty"`
}
type TestGenerationStateChangedEventV16 struct {
	TaskEventBaseV16
	Event   TaskEventNameV16                     `json:"event"`
	Payload TestGenerationStateChangedPayloadV16 `json:"payload"`
}

func (TestGenerationStateChangedEventV16) isTaskEventV16() {}

type TestGenerationStateChangedPayloadV16 struct {
	RunID string                                                `json:"runId"`
	From  protocolmodelv16testgeneration.TestGenerationStateV16 `json:"from"`
	To    protocolmodelv16testgeneration.TestGenerationStateV16 `json:"to"`
}
type EventProtocolVersionV16 string

const EventProtocol16V16 EventProtocolVersionV16 = "1.6"

type EventKindV16 string

const EventV16 EventKindV16 = "event"

type TaskEventNameV16 string

const (
	TaskCreatedV16                TaskEventNameV16 = "task.created"
	TaskStartedV16                TaskEventNameV16 = "task.started"
	TaskStepStartedV16            TaskEventNameV16 = "task.step_started"
	TaskOutputV16                 TaskEventNameV16 = "task.output"
	TaskStepFinishedV16           TaskEventNameV16 = "task.step_finished"
	TaskCancellationRequestedV16  TaskEventNameV16 = "task.cancellation_requested"
	ArtifactCreatedV16            TaskEventNameV16 = "artifact.created"
	TaskFinishedV16               TaskEventNameV16 = "task.finished"
	TaskDiagnosticV16             TaskEventNameV16 = "task.diagnostic"
	TestDiscoveryStartedV16       TaskEventNameV16 = "test.discovery.started"
	TestContainerDiscoveredV16    TaskEventNameV16 = "test.container.discovered"
	TestCatalogPublishedV16       TaskEventNameV16 = "test.catalog.published"
	TestRunStartedV16             TaskEventNameV16 = "test.run.started"
	TestContainerStartedV16       TaskEventNameV16 = "test.container.started"
	TestItemStartedV16            TaskEventNameV16 = "test.item.started"
	TestOutputV16                 TaskEventNameV16 = "test.output"
	TestItemFinishedV16           TaskEventNameV16 = "test.item.finished"
	TestContainerFinishedV16      TaskEventNameV16 = "test.container.finished"
	TestRunFinishedV16            TaskEventNameV16 = "test.run.finished"
	CoverageRunStartedV16         TaskEventNameV16 = "coverage.run.started"
	CoverageBuildFinishedV16      TaskEventNameV16 = "coverage.build.finished"
	CoverageCollectionStartedV16  TaskEventNameV16 = "coverage.collection.started"
	CoverageReportAvailableV16    TaskEventNameV16 = "coverage.report.available"
	CoverageRunFinishedV16        TaskEventNameV16 = "coverage.run.finished"
	TestGenerationStateChangedV16 TaskEventNameV16 = "testGeneration.state.changed"
)

type TaskStepKindV16 string

const (
	StepSimulationV16        TaskStepKindV16 = "simulation"
	StepConfigureV16         TaskStepKindV16 = "configure"
	StepBuildV16             TaskStepKindV16 = "build"
	StepTestDiscoveryV16     TaskStepKindV16 = "test-discovery"
	StepTestRunV16           TaskStepKindV16 = "test-run"
	StepCoverageConfigureV16 TaskStepKindV16 = "coverage-configure"
	StepCoverageBuildV16     TaskStepKindV16 = "coverage-build"
	StepCoverageTestV16      TaskStepKindV16 = "coverage-test"
	StepCoverageMergeV16     TaskStepKindV16 = "coverage-merge"
	StepCoverageNormalizeV16 TaskStepKindV16 = "coverage-normalize"
	StepCoverageReportV16    TaskStepKindV16 = "coverage-report"
	StepCoveragePublishV16   TaskStepKindV16 = "coverage-publish"
)

type TaskStepFinishedStatusV16 string

const (
	StepSucceededV16 TaskStepFinishedStatusV16 = "succeeded"
	StepFailedV16    TaskStepFinishedStatusV16 = "failed"
	StepSkippedV16   TaskStepFinishedStatusV16 = "skipped"
)

type TaskOutputStreamV16 string

const (
	OutputStdoutV16   TaskOutputStreamV16 = "stdout"
	OutputStderrV16   TaskOutputStreamV16 = "stderr"
	OutputCombinedV16 TaskOutputStreamV16 = "combined"
)

type TaskOutcomeV16 string

const (
	OutcomeSucceededV16            TaskOutcomeV16 = "succeeded"
	OutcomeCommandFailedV16        TaskOutcomeV16 = "command_failed"
	OutcomeCancelledV16            TaskOutcomeV16 = "cancelled"
	OutcomeTimedOutV16             TaskOutcomeV16 = "timed_out"
	OutcomeInterruptedV16          TaskOutcomeV16 = "interrupted"
	OutcomeInfrastructureFailedV16 TaskOutcomeV16 = "infrastructure_failed"
)

type TestEventFrameworkV16 string

const (
	FrameworkCppUTestV16    TestEventFrameworkV16 = "cpputest"
	FrameworkUnityV16       TestEventFrameworkV16 = "unity"
	FrameworkOpaqueCTestV16 TestEventFrameworkV16 = "opaque-ctest"
)

type TestContainerOutcomeV16 string

const (
	ContainerPassedV16    TestContainerOutcomeV16 = "passed"
	ContainerFailedV16    TestContainerOutcomeV16 = "failed"
	ContainerErroredV16   TestContainerOutcomeV16 = "errored"
	ContainerCancelledV16 TestContainerOutcomeV16 = "cancelled"
	ContainerTimedOutV16  TestContainerOutcomeV16 = "timed_out"
	ContainerNotRunV16    TestContainerOutcomeV16 = "not_run"
)
