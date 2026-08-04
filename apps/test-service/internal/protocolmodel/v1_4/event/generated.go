package protocolmodelv14event

import (
	"time"

	protocolmodelv14coverage "unit-test-ide.local/test-service/internal/protocolmodel/v1_4/coverage"
	protocolmodelv14diagnostic "unit-test-ide.local/test-service/internal/protocolmodel/v1_4/diagnostic"
	protocolmodelv14test "unit-test-ide.local/test-service/internal/protocolmodel/v1_4/test"
)

type TaskEventV14 interface{ isTaskEventV14() }
type CoverageEventV14 interface{ isCoverageEventV14() }
type TaskEventBaseV14 struct {
	ProtocolVersion EventProtocolVersionV14 `json:"protocolVersion"`
	Kind            EventKindV14            `json:"kind"`
	MessageID       string                  `json:"messageId"`
	SentAt          time.Time               `json:"sentAt"`
	Sequence        int64                   `json:"sequence"`
	TaskID          string                  `json:"taskId"`
	PayloadVersion  int64                   `json:"payloadVersion"`
}
type TaskCreatedEventV14 struct {
	TaskEventBaseV14
	Event   TaskEventNameV14      `json:"event"`
	Payload TaskCreatedPayloadV14 `json:"payload"`
}

func (TaskCreatedEventV14) isTaskEventV14() {}

type TaskStartedEventV14 struct {
	TaskEventBaseV14
	Event   TaskEventNameV14      `json:"event"`
	Payload TaskStartedPayloadV14 `json:"payload"`
}

func (TaskStartedEventV14) isTaskEventV14() {}

type TaskStepStartedEventV14 struct {
	TaskEventBaseV14
	Event   TaskEventNameV14          `json:"event"`
	Payload TaskStepStartedPayloadV14 `json:"payload"`
}

func (TaskStepStartedEventV14) isTaskEventV14() {}

type TaskOutputEventV14 struct {
	TaskEventBaseV14
	Event   TaskEventNameV14     `json:"event"`
	Payload TaskOutputPayloadV14 `json:"payload"`
}

func (TaskOutputEventV14) isTaskEventV14() {}

type TaskStepFinishedEventV14 struct {
	TaskEventBaseV14
	Event   TaskEventNameV14           `json:"event"`
	Payload TaskStepFinishedPayloadV14 `json:"payload"`
}

func (TaskStepFinishedEventV14) isTaskEventV14() {}

type TaskCancellationRequestedEventV14 struct {
	TaskEventBaseV14
	Event   TaskEventNameV14                    `json:"event"`
	Payload TaskCancellationRequestedPayloadV14 `json:"payload"`
}

func (TaskCancellationRequestedEventV14) isTaskEventV14() {}

type ArtifactCreatedEventV14 struct {
	TaskEventBaseV14
	Event   TaskEventNameV14          `json:"event"`
	Payload ArtifactCreatedPayloadV14 `json:"payload"`
}

func (ArtifactCreatedEventV14) isTaskEventV14() {}

type TaskFinishedEventV14 struct {
	TaskEventBaseV14
	Event   TaskEventNameV14       `json:"event"`
	Payload TaskFinishedPayloadV14 `json:"payload"`
}

func (TaskFinishedEventV14) isTaskEventV14() {}

type TaskDiagnosticEventV14 struct {
	TaskEventBaseV14
	Event   TaskEventNameV14         `json:"event"`
	Payload TaskDiagnosticPayloadV14 `json:"payload"`
}

func (TaskDiagnosticEventV14) isTaskEventV14() {}

type TestDiscoveryStartedEventV14 struct {
	TaskEventBaseV14
	Event   TaskEventNameV14               `json:"event"`
	Payload TestDiscoveryStartedPayloadV14 `json:"payload"`
}

func (TestDiscoveryStartedEventV14) isTaskEventV14() {}

type TestContainerDiscoveredEventV14 struct {
	TaskEventBaseV14
	Event   TaskEventNameV14                  `json:"event"`
	Payload TestContainerDiscoveredPayloadV14 `json:"payload"`
}

func (TestContainerDiscoveredEventV14) isTaskEventV14() {}

type TestCatalogPublishedEventV14 struct {
	TaskEventBaseV14
	Event   TaskEventNameV14               `json:"event"`
	Payload TestCatalogPublishedPayloadV14 `json:"payload"`
}

func (TestCatalogPublishedEventV14) isTaskEventV14() {}

type TestRunStartedEventV14 struct {
	TaskEventBaseV14
	Event   TaskEventNameV14         `json:"event"`
	Payload TestRunStartedPayloadV14 `json:"payload"`
}

func (TestRunStartedEventV14) isTaskEventV14() {}

type TestContainerStartedEventV14 struct {
	TaskEventBaseV14
	Event   TaskEventNameV14               `json:"event"`
	Payload TestContainerStartedPayloadV14 `json:"payload"`
}

func (TestContainerStartedEventV14) isTaskEventV14() {}

type TestItemStartedEventV14 struct {
	TaskEventBaseV14
	Event   TaskEventNameV14          `json:"event"`
	Payload TestItemStartedPayloadV14 `json:"payload"`
}

func (TestItemStartedEventV14) isTaskEventV14() {}

type TestOutputEventV14 struct {
	TaskEventBaseV14
	Event   TaskEventNameV14     `json:"event"`
	Payload TestOutputPayloadV14 `json:"payload"`
}

func (TestOutputEventV14) isTaskEventV14() {}

type TestItemFinishedEventV14 struct {
	TaskEventBaseV14
	Event   TaskEventNameV14           `json:"event"`
	Payload TestItemFinishedPayloadV14 `json:"payload"`
}

func (TestItemFinishedEventV14) isTaskEventV14() {}

type TestContainerFinishedEventV14 struct {
	TaskEventBaseV14
	Event   TaskEventNameV14                `json:"event"`
	Payload TestContainerFinishedPayloadV14 `json:"payload"`
}

func (TestContainerFinishedEventV14) isTaskEventV14() {}

type TestRunFinishedEventV14 struct {
	TaskEventBaseV14
	Event   TaskEventNameV14          `json:"event"`
	Payload TestRunFinishedPayloadV14 `json:"payload"`
}

func (TestRunFinishedEventV14) isTaskEventV14() {}

type CoverageRunStartedEventV14 struct {
	TaskEventBaseV14
	Event   TaskEventNameV14             `json:"event"`
	Payload CoverageRunStartedPayloadV14 `json:"payload"`
}

func (CoverageRunStartedEventV14) isTaskEventV14()     {}
func (CoverageRunStartedEventV14) isCoverageEventV14() {}

type CoverageBuildFinishedEventV14 struct {
	TaskEventBaseV14
	Event   TaskEventNameV14                `json:"event"`
	Payload CoverageBuildFinishedPayloadV14 `json:"payload"`
}

func (CoverageBuildFinishedEventV14) isTaskEventV14()     {}
func (CoverageBuildFinishedEventV14) isCoverageEventV14() {}

type CoverageCollectionStartedEventV14 struct {
	TaskEventBaseV14
	Event   TaskEventNameV14                    `json:"event"`
	Payload CoverageCollectionStartedPayloadV14 `json:"payload"`
}

func (CoverageCollectionStartedEventV14) isTaskEventV14()     {}
func (CoverageCollectionStartedEventV14) isCoverageEventV14() {}

type CoverageReportAvailableEventV14 struct {
	TaskEventBaseV14
	Event   TaskEventNameV14                  `json:"event"`
	Payload CoverageReportAvailablePayloadV14 `json:"payload"`
}

func (CoverageReportAvailableEventV14) isTaskEventV14()     {}
func (CoverageReportAvailableEventV14) isCoverageEventV14() {}

type CoverageRunFinishedEventV14 struct {
	TaskEventBaseV14
	Event   TaskEventNameV14              `json:"event"`
	Payload CoverageRunFinishedPayloadV14 `json:"payload"`
}

func (CoverageRunFinishedEventV14) isTaskEventV14()     {}
func (CoverageRunFinishedEventV14) isCoverageEventV14() {}

type TaskCreatedPayloadV14 struct {
	Status string `json:"status"`
}
type TaskStartedPayloadV14 struct {
	Status string `json:"status"`
}
type TaskStepStartedPayloadV14 struct {
	StepID string          `json:"stepId"`
	Kind   TaskStepKindV14 `json:"kind"`
	Status string          `json:"status"`
}
type TaskOutputPayloadV14 struct {
	StepID    string              `json:"stepId"`
	Stream    TaskOutputStreamV14 `json:"stream"`
	Text      string              `json:"text"`
	Truncated bool                `json:"truncated"`
}
type TaskStepFinishedPayloadV14 struct {
	StepID    string                    `json:"stepId"`
	Kind      TaskStepKindV14           `json:"kind"`
	Status    TaskStepFinishedStatusV14 `json:"status"`
	ExitCode  *int64                    `json:"exitCode,omitempty"`
	ErrorCode *string                   `json:"errorCode,omitempty"`
}
type TaskCancellationRequestedPayloadV14 struct {
	Status string `json:"status"`
}
type ArtifactCreatedPayloadV14 struct {
	ArtifactID string `json:"artifactId"`
	Kind       string `json:"kind"`
}
type TaskFinishedPayloadV14 struct {
	Outcome TaskOutcomeV14 `json:"outcome"`
}
type TaskDiagnosticPayloadV14 struct {
	Diagnostic protocolmodelv14diagnostic.DiagnosticV14 `json:"diagnostic"`
}
type TestDiscoveryStartedPayloadV14 struct {
	ProjectID string `json:"projectId"`
	ProfileID string `json:"profileId"`
}
type TestContainerDiscoveredPayloadV14 struct {
	ContainerID string                `json:"containerId"`
	Framework   TestEventFrameworkV14 `json:"framework"`
	DisplayName string                `json:"displayName"`
}
type TestCatalogPublishedPayloadV14 struct {
	ProjectID      string `json:"projectId"`
	ProfileID      string `json:"profileId"`
	Revision       string `json:"revision"`
	ContainerCount int64  `json:"containerCount"`
	ItemCount      int64  `json:"itemCount"`
}
type TestRunStartedPayloadV14 struct {
	RunID           string `json:"runId"`
	CatalogRevision string `json:"catalogRevision"`
	Total           int64  `json:"total"`
}
type TestContainerStartedPayloadV14 struct {
	RunID       string `json:"runId"`
	ContainerID string `json:"containerId"`
	Iteration   int64  `json:"iteration"`
}
type TestItemStartedPayloadV14 struct {
	RunID       string `json:"runId"`
	ItemID      string `json:"itemId"`
	ContainerID string `json:"containerId"`
	Iteration   int64  `json:"iteration"`
}
type TestOutputPayloadV14 struct {
	RunID       string              `json:"runId"`
	ContainerID string              `json:"containerId"`
	ItemID      *string             `json:"itemId,omitempty"`
	Iteration   int64               `json:"iteration"`
	Stream      TaskOutputStreamV14 `json:"stream"`
	Text        string              `json:"text"`
	Truncated   bool                `json:"truncated"`
}
type TestItemFinishedPayloadV14 struct {
	RunID  string                              `json:"runId"`
	Result protocolmodelv14test.TestItemResult `json:"result"`
}
type TestContainerFinishedPayloadV14 struct {
	RunID       string                  `json:"runId"`
	ContainerID string                  `json:"containerId"`
	Iteration   int64                   `json:"iteration"`
	Outcome     TestContainerOutcomeV14 `json:"outcome"`
}
type TestRunFinishedPayloadV14 struct {
	RunID          string                                 `json:"runId"`
	Outcome        protocolmodelv14test.TestRunOutcomeV14 `json:"outcome"`
	Summary        protocolmodelv14test.TestRunSummaryV14 `json:"summary"`
	ResultRevision string                                 `json:"resultRevision"`
	Incomplete     bool                                   `json:"incomplete"`
}
type CoverageRunStartedPayloadV14 struct {
	CoverageRunID   string `json:"coverageRunId"`
	TestRunID       string `json:"testRunId"`
	CatalogRevision string `json:"catalogRevision"`
	RepeatCount     int64  `json:"repeatCount"`
}
type CoverageBuildFinishedPayloadV14 struct {
	CoverageRunID string `json:"coverageRunId"`
}
type CoverageCollectionStartedPayloadV14 struct {
	CoverageRunID string `json:"coverageRunId"`
	TestRunID     string `json:"testRunId"`
}
type CoverageReportAvailablePayloadV14 struct {
	CoverageRunID string                                           `json:"coverageRunId"`
	ReportID      string                                           `json:"reportId"`
	ArtifactID    string                                           `json:"artifactId"`
	Completeness  protocolmodelv14coverage.CoverageCompletenessV14 `json:"completeness"`
	Summary       protocolmodelv14coverage.CoverageSummaryV14      `json:"summary"`
}
type CoverageRunFinishedPayloadV14 struct {
	CoverageRunID string                                         `json:"coverageRunId"`
	Outcome       protocolmodelv14coverage.CoverageRunOutcomeV14 `json:"outcome"`
	Reason        *protocolmodelv14coverage.CoverageRunReasonV14 `json:"reason,omitempty"`
	ReportID      *string                                        `json:"reportId,omitempty"`
}
type EventProtocolVersionV14 string

const EventProtocol14V14 EventProtocolVersionV14 = "1.4"

type EventKindV14 string

const EventV14 EventKindV14 = "event"

type TaskEventNameV14 string

const (
	TaskCreatedV14               TaskEventNameV14 = "task.created"
	TaskStartedV14               TaskEventNameV14 = "task.started"
	TaskStepStartedV14           TaskEventNameV14 = "task.step_started"
	TaskOutputV14                TaskEventNameV14 = "task.output"
	TaskStepFinishedV14          TaskEventNameV14 = "task.step_finished"
	TaskCancellationRequestedV14 TaskEventNameV14 = "task.cancellation_requested"
	ArtifactCreatedV14           TaskEventNameV14 = "artifact.created"
	TaskFinishedV14              TaskEventNameV14 = "task.finished"
	TaskDiagnosticV14            TaskEventNameV14 = "task.diagnostic"
	TestDiscoveryStartedV14      TaskEventNameV14 = "test.discovery.started"
	TestContainerDiscoveredV14   TaskEventNameV14 = "test.container.discovered"
	TestCatalogPublishedV14      TaskEventNameV14 = "test.catalog.published"
	TestRunStartedV14            TaskEventNameV14 = "test.run.started"
	TestContainerStartedV14      TaskEventNameV14 = "test.container.started"
	TestItemStartedV14           TaskEventNameV14 = "test.item.started"
	TestOutputV14                TaskEventNameV14 = "test.output"
	TestItemFinishedV14          TaskEventNameV14 = "test.item.finished"
	TestContainerFinishedV14     TaskEventNameV14 = "test.container.finished"
	TestRunFinishedV14           TaskEventNameV14 = "test.run.finished"
	CoverageRunStartedV14        TaskEventNameV14 = "coverage.run.started"
	CoverageBuildFinishedV14     TaskEventNameV14 = "coverage.build.finished"
	CoverageCollectionStartedV14 TaskEventNameV14 = "coverage.collection.started"
	CoverageReportAvailableV14   TaskEventNameV14 = "coverage.report.available"
	CoverageRunFinishedV14       TaskEventNameV14 = "coverage.run.finished"
)

type TaskStepKindV14 string

const (
	StepSimulationV14        TaskStepKindV14 = "simulation"
	StepConfigureV14         TaskStepKindV14 = "configure"
	StepBuildV14             TaskStepKindV14 = "build"
	StepTestDiscoveryV14     TaskStepKindV14 = "test-discovery"
	StepTestRunV14           TaskStepKindV14 = "test-run"
	StepCoverageConfigureV14 TaskStepKindV14 = "coverage-configure"
	StepCoverageBuildV14     TaskStepKindV14 = "coverage-build"
	StepCoverageTestV14      TaskStepKindV14 = "coverage-test"
	StepCoverageMergeV14     TaskStepKindV14 = "coverage-merge"
	StepCoverageNormalizeV14 TaskStepKindV14 = "coverage-normalize"
	StepCoverageReportV14    TaskStepKindV14 = "coverage-report"
	StepCoveragePublishV14   TaskStepKindV14 = "coverage-publish"
)

type TaskStepFinishedStatusV14 string

const (
	StepSucceededV14 TaskStepFinishedStatusV14 = "succeeded"
	StepFailedV14    TaskStepFinishedStatusV14 = "failed"
	StepSkippedV14   TaskStepFinishedStatusV14 = "skipped"
)

type TaskOutputStreamV14 string

const (
	OutputStdoutV14   TaskOutputStreamV14 = "stdout"
	OutputStderrV14   TaskOutputStreamV14 = "stderr"
	OutputCombinedV14 TaskOutputStreamV14 = "combined"
)

type TaskOutcomeV14 string

const (
	OutcomeSucceededV14            TaskOutcomeV14 = "succeeded"
	OutcomeCommandFailedV14        TaskOutcomeV14 = "command_failed"
	OutcomeCancelledV14            TaskOutcomeV14 = "cancelled"
	OutcomeTimedOutV14             TaskOutcomeV14 = "timed_out"
	OutcomeInterruptedV14          TaskOutcomeV14 = "interrupted"
	OutcomeInfrastructureFailedV14 TaskOutcomeV14 = "infrastructure_failed"
)

type TestEventFrameworkV14 string

const (
	FrameworkCppUTestV14    TestEventFrameworkV14 = "cpputest"
	FrameworkUnityV14       TestEventFrameworkV14 = "unity"
	FrameworkOpaqueCTestV14 TestEventFrameworkV14 = "opaque-ctest"
)

type TestContainerOutcomeV14 string

const (
	ContainerPassedV14    TestContainerOutcomeV14 = "passed"
	ContainerFailedV14    TestContainerOutcomeV14 = "failed"
	ContainerErroredV14   TestContainerOutcomeV14 = "errored"
	ContainerCancelledV14 TestContainerOutcomeV14 = "cancelled"
	ContainerTimedOutV14  TestContainerOutcomeV14 = "timed_out"
	ContainerNotRunV14    TestContainerOutcomeV14 = "not_run"
)
