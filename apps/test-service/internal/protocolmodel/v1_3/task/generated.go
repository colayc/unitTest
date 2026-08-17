package protocolmodelv13task

import "time"

type TaskSnapshotV13 interface{ isTaskSnapshotV13() }
type CmakeBuildTaskSnapshotV13 struct {
	TaskID              string          `json:"taskId"`
	Kind                Kind            `json:"kind"`
	WorkspaceGeneration string          `json:"workspaceGeneration"`
	ProjectID           string          `json:"projectId"`
	BuildProfileID      string          `json:"buildProfileId"`
	TargetIDs           []string        `json:"targetIds"`
	Jobs                int64           `json:"jobs"`
	TimeoutMS           int64           `json:"timeoutMs"`
	Status              TaskStatusV13   `json:"status"`
	CreatedAt           time.Time       `json:"createdAt"`
	LastSequence        int64           `json:"lastSequence"`
	Outcome             *TaskOutcomeV13 `json:"outcome,omitempty"`
	StartedAt           *time.Time      `json:"startedAt,omitempty"`
	FinishedAt          *time.Time      `json:"finishedAt,omitempty"`
	ErrorCode           *string         `json:"errorCode,omitempty"`
	ErrorMessage        *string         `json:"errorMessage,omitempty"`
}

func (CmakeBuildTaskSnapshotV13) isTaskSnapshotV13() {}

type SimulationTaskSnapshotV13 struct {
	TaskID       string                `json:"taskId"`
	Kind         Kind                  `json:"kind"`
	Scenario     SimulationScenarioV13 `json:"scenario"`
	TimeoutMS    *int64                `json:"timeoutMs,omitempty"`
	Status       TaskStatusV13         `json:"status"`
	CreatedAt    time.Time             `json:"createdAt"`
	LastSequence int64                 `json:"lastSequence"`
	Outcome      *TaskOutcomeV13       `json:"outcome,omitempty"`
	StartedAt    *time.Time            `json:"startedAt,omitempty"`
	FinishedAt   *time.Time            `json:"finishedAt,omitempty"`
	ErrorCode    *string               `json:"errorCode,omitempty"`
	ErrorMessage *string               `json:"errorMessage,omitempty"`
}

func (SimulationTaskSnapshotV13) isTaskSnapshotV13() {}

type TestDiscoveryTaskSnapshotV13 struct {
	TaskID          string          `json:"taskId"`
	Kind            Kind            `json:"kind"`
	ProjectID       string          `json:"projectId"`
	ProfileID       string          `json:"profileId"`
	CatalogRevision *string         `json:"catalogRevision,omitempty"`
	Status          TaskStatusV13   `json:"status"`
	CreatedAt       time.Time       `json:"createdAt"`
	LastSequence    int64           `json:"lastSequence"`
	Outcome         *TaskOutcomeV13 `json:"outcome,omitempty"`
	StartedAt       *time.Time      `json:"startedAt,omitempty"`
	FinishedAt      *time.Time      `json:"finishedAt,omitempty"`
	ErrorCode       *string         `json:"errorCode,omitempty"`
	ErrorMessage    *string         `json:"errorMessage,omitempty"`
}

func (TestDiscoveryTaskSnapshotV13) isTaskSnapshotV13() {}

type TestRunTaskSnapshotV13 struct {
	TaskID          string          `json:"taskId"`
	Kind            Kind            `json:"kind"`
	ProjectID       string          `json:"projectId"`
	ProfileID       string          `json:"profileId"`
	CatalogRevision string          `json:"catalogRevision"`
	RunID           string          `json:"runId"`
	RepeatCount     int64           `json:"repeatCount"`
	Status          TaskStatusV13   `json:"status"`
	CreatedAt       time.Time       `json:"createdAt"`
	LastSequence    int64           `json:"lastSequence"`
	Outcome         *TaskOutcomeV13 `json:"outcome,omitempty"`
	StartedAt       *time.Time      `json:"startedAt,omitempty"`
	FinishedAt      *time.Time      `json:"finishedAt,omitempty"`
	ErrorCode       *string         `json:"errorCode,omitempty"`
	ErrorMessage    *string         `json:"errorMessage,omitempty"`
}

func (TestRunTaskSnapshotV13) isTaskSnapshotV13() {}

type Kind string

const (
	CmakeBuild    Kind = "cmakeBuild"
	Simulation    Kind = "simulation"
	TestDiscovery Kind = "testDiscovery"
	TestRun       Kind = "testRun"
)

type TaskOutcomeV13 string

const (
	Cancelled            TaskOutcomeV13 = "cancelled"
	CommandFailed        TaskOutcomeV13 = "command_failed"
	InfrastructureFailed TaskOutcomeV13 = "infrastructure_failed"
	Interrupted          TaskOutcomeV13 = "interrupted"
	Succeeded            TaskOutcomeV13 = "succeeded"
	TimedOut             TaskOutcomeV13 = "timed_out"
)

type SimulationScenarioV13 string

const (
	EmitOutput  SimulationScenarioV13 = "emit-output"
	ExitNonzero SimulationScenarioV13 = "exit-nonzero"
	Hang        SimulationScenarioV13 = "hang"
	SpawnChild  SimulationScenarioV13 = "spawn-child"
	Success     SimulationScenarioV13 = "success"
)

type TaskStatusV13 string

const (
	Cancelling TaskStatusV13 = "cancelling"
	Finished   TaskStatusV13 = "finished"
	Queued     TaskStatusV13 = "queued"
	Running    TaskStatusV13 = "running"
)
