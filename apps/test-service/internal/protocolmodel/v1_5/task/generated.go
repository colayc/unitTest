package protocolmodelv15task

import "time"

type TaskSnapshotV15 interface{ isTaskSnapshotV15() }
type CmakeBuildTaskSnapshotV15 struct {
	TaskID              string          `json:"taskId"`
	Kind                TaskKindV15     `json:"kind"`
	WorkspaceGeneration string          `json:"workspaceGeneration"`
	ProjectID           string          `json:"projectId"`
	BuildProfileID      string          `json:"buildProfileId"`
	TargetIDs           []string        `json:"targetIds"`
	Jobs                int64           `json:"jobs"`
	TimeoutMS           int64           `json:"timeoutMs"`
	Status              TaskStatusV15   `json:"status"`
	Outcome             *TaskOutcomeV15 `json:"outcome,omitempty"`
	CreatedAt           time.Time       `json:"createdAt"`
	StartedAt           *time.Time      `json:"startedAt,omitempty"`
	FinishedAt          *time.Time      `json:"finishedAt,omitempty"`
	LastSequence        int64           `json:"lastSequence"`
	ErrorCode           *string         `json:"errorCode,omitempty"`
	ErrorMessage        *string         `json:"errorMessage,omitempty"`
}

func (CmakeBuildTaskSnapshotV15) isTaskSnapshotV15() {}

type SimulationTaskSnapshotV15 struct {
	TaskID       string                `json:"taskId"`
	Kind         TaskKindV15           `json:"kind"`
	Scenario     SimulationScenarioV15 `json:"scenario"`
	TimeoutMS    *int64                `json:"timeoutMs,omitempty"`
	Status       TaskStatusV15         `json:"status"`
	Outcome      *TaskOutcomeV15       `json:"outcome,omitempty"`
	CreatedAt    time.Time             `json:"createdAt"`
	StartedAt    *time.Time            `json:"startedAt,omitempty"`
	FinishedAt   *time.Time            `json:"finishedAt,omitempty"`
	LastSequence int64                 `json:"lastSequence"`
	ErrorCode    *string               `json:"errorCode,omitempty"`
	ErrorMessage *string               `json:"errorMessage,omitempty"`
}

func (SimulationTaskSnapshotV15) isTaskSnapshotV15() {}

type TestDiscoveryTaskSnapshotV15 struct {
	TaskID          string          `json:"taskId"`
	Kind            TaskKindV15     `json:"kind"`
	ProjectID       string          `json:"projectId"`
	ProfileID       string          `json:"profileId"`
	CatalogRevision *string         `json:"catalogRevision,omitempty"`
	Status          TaskStatusV15   `json:"status"`
	Outcome         *TaskOutcomeV15 `json:"outcome,omitempty"`
	CreatedAt       time.Time       `json:"createdAt"`
	StartedAt       *time.Time      `json:"startedAt,omitempty"`
	FinishedAt      *time.Time      `json:"finishedAt,omitempty"`
	LastSequence    int64           `json:"lastSequence"`
	ErrorCode       *string         `json:"errorCode,omitempty"`
	ErrorMessage    *string         `json:"errorMessage,omitempty"`
}

func (TestDiscoveryTaskSnapshotV15) isTaskSnapshotV15() {}

type TestRunTaskSnapshotV15 struct {
	TaskID          string          `json:"taskId"`
	Kind            TaskKindV15     `json:"kind"`
	ProjectID       string          `json:"projectId"`
	ProfileID       string          `json:"profileId"`
	CatalogRevision string          `json:"catalogRevision"`
	RunID           string          `json:"runId"`
	RepeatCount     int64           `json:"repeatCount"`
	Status          TaskStatusV15   `json:"status"`
	Outcome         *TaskOutcomeV15 `json:"outcome,omitempty"`
	CreatedAt       time.Time       `json:"createdAt"`
	StartedAt       *time.Time      `json:"startedAt,omitempty"`
	FinishedAt      *time.Time      `json:"finishedAt,omitempty"`
	LastSequence    int64           `json:"lastSequence"`
	ErrorCode       *string         `json:"errorCode,omitempty"`
	ErrorMessage    *string         `json:"errorMessage,omitempty"`
}

func (TestRunTaskSnapshotV15) isTaskSnapshotV15() {}

type CoverageRunTaskSnapshotV15 struct {
	TaskID              string          `json:"taskId"`
	Kind                TaskKindV15     `json:"kind"`
	WorkspaceGeneration string          `json:"workspaceGeneration"`
	ProjectID           string          `json:"projectId"`
	CoverageProfileID   string          `json:"coverageProfileId"`
	CatalogRevision     string          `json:"catalogRevision"`
	CoverageRunID       string          `json:"coverageRunId"`
	TestRunID           string          `json:"testRunId"`
	RepeatCount         int64           `json:"repeatCount"`
	TimeoutMS           int64           `json:"timeoutMs"`
	Status              TaskStatusV15   `json:"status"`
	Outcome             *TaskOutcomeV15 `json:"outcome,omitempty"`
	CreatedAt           time.Time       `json:"createdAt"`
	StartedAt           *time.Time      `json:"startedAt,omitempty"`
	FinishedAt          *time.Time      `json:"finishedAt,omitempty"`
	LastSequence        int64           `json:"lastSequence"`
	ErrorCode           *string         `json:"errorCode,omitempty"`
	ErrorMessage        *string         `json:"errorMessage,omitempty"`
}

func (CoverageRunTaskSnapshotV15) isTaskSnapshotV15() {}

type TestGenerationTaskSnapshotV15 struct {
	TaskID              string          `json:"taskId"`
	Kind                TaskKindV15     `json:"kind"`
	RunID               string          `json:"runId"`
	WorkspaceGeneration string          `json:"workspaceGeneration"`
	ProjectID           string          `json:"projectId"`
	Status              TaskStatusV15   `json:"status"`
	Outcome             *TaskOutcomeV15 `json:"outcome,omitempty"`
	CreatedAt           time.Time       `json:"createdAt"`
	StartedAt           *time.Time      `json:"startedAt,omitempty"`
	FinishedAt          *time.Time      `json:"finishedAt,omitempty"`
	LastSequence        int64           `json:"lastSequence"`
	ErrorCode           *string         `json:"errorCode,omitempty"`
	ErrorMessage        *string         `json:"errorMessage,omitempty"`
}

func (TestGenerationTaskSnapshotV15) isTaskSnapshotV15() {}

type TaskKindV15 string

const (
	TaskKindCmakeBuildV15     TaskKindV15 = "cmakeBuild"
	TaskKindSimulationV15     TaskKindV15 = "simulation"
	TaskKindTestDiscoveryV15  TaskKindV15 = "testDiscovery"
	TaskKindTestRunV15        TaskKindV15 = "testRun"
	TaskKindCoverageRunV15    TaskKindV15 = "coverageRun"
	TaskKindTestGenerationV15 TaskKindV15 = "testGeneration"
)

type TaskStatusV15 string

const (
	TaskQueuedV15     TaskStatusV15 = "queued"
	TaskRunningV15    TaskStatusV15 = "running"
	TaskCancellingV15 TaskStatusV15 = "cancelling"
	TaskFinishedV15   TaskStatusV15 = "finished"
)

type TaskOutcomeV15 string

const (
	TaskSucceededV15            TaskOutcomeV15 = "succeeded"
	TaskCommandFailedV15        TaskOutcomeV15 = "command_failed"
	TaskCancelledV15            TaskOutcomeV15 = "cancelled"
	TaskTimedOutV15             TaskOutcomeV15 = "timed_out"
	TaskInterruptedV15          TaskOutcomeV15 = "interrupted"
	TaskInfrastructureFailedV15 TaskOutcomeV15 = "infrastructure_failed"
)

type SimulationScenarioV15 string

const (
	SimulationSuccessV15     SimulationScenarioV15 = "success"
	SimulationExitNonzeroV15 SimulationScenarioV15 = "exit-nonzero"
	SimulationHangV15        SimulationScenarioV15 = "hang"
	SimulationSpawnChildV15  SimulationScenarioV15 = "spawn-child"
	SimulationEmitOutputV15  SimulationScenarioV15 = "emit-output"
)
