package protocolmodelv16task

import "time"

type TaskSnapshotV16 interface{ isTaskSnapshotV16() }
type CmakeBuildTaskSnapshotV16 struct {
	TaskID              string          `json:"taskId"`
	Kind                TaskKindV16     `json:"kind"`
	WorkspaceGeneration string          `json:"workspaceGeneration"`
	ProjectID           string          `json:"projectId"`
	BuildProfileID      string          `json:"buildProfileId"`
	TargetIDs           []string        `json:"targetIds"`
	Jobs                int64           `json:"jobs"`
	TimeoutMS           int64           `json:"timeoutMs"`
	Status              TaskStatusV16   `json:"status"`
	Outcome             *TaskOutcomeV16 `json:"outcome,omitempty"`
	CreatedAt           time.Time       `json:"createdAt"`
	StartedAt           *time.Time      `json:"startedAt,omitempty"`
	FinishedAt          *time.Time      `json:"finishedAt,omitempty"`
	LastSequence        int64           `json:"lastSequence"`
	ErrorCode           *string         `json:"errorCode,omitempty"`
	ErrorMessage        *string         `json:"errorMessage,omitempty"`
}

func (CmakeBuildTaskSnapshotV16) isTaskSnapshotV16() {}

type SimulationTaskSnapshotV16 struct {
	TaskID       string                `json:"taskId"`
	Kind         TaskKindV16           `json:"kind"`
	Scenario     SimulationScenarioV16 `json:"scenario"`
	TimeoutMS    *int64                `json:"timeoutMs,omitempty"`
	Status       TaskStatusV16         `json:"status"`
	Outcome      *TaskOutcomeV16       `json:"outcome,omitempty"`
	CreatedAt    time.Time             `json:"createdAt"`
	StartedAt    *time.Time            `json:"startedAt,omitempty"`
	FinishedAt   *time.Time            `json:"finishedAt,omitempty"`
	LastSequence int64                 `json:"lastSequence"`
	ErrorCode    *string               `json:"errorCode,omitempty"`
	ErrorMessage *string               `json:"errorMessage,omitempty"`
}

func (SimulationTaskSnapshotV16) isTaskSnapshotV16() {}

type TestDiscoveryTaskSnapshotV16 struct {
	TaskID          string          `json:"taskId"`
	Kind            TaskKindV16     `json:"kind"`
	ProjectID       string          `json:"projectId"`
	ProfileID       string          `json:"profileId"`
	CatalogRevision *string         `json:"catalogRevision,omitempty"`
	Status          TaskStatusV16   `json:"status"`
	Outcome         *TaskOutcomeV16 `json:"outcome,omitempty"`
	CreatedAt       time.Time       `json:"createdAt"`
	StartedAt       *time.Time      `json:"startedAt,omitempty"`
	FinishedAt      *time.Time      `json:"finishedAt,omitempty"`
	LastSequence    int64           `json:"lastSequence"`
	ErrorCode       *string         `json:"errorCode,omitempty"`
	ErrorMessage    *string         `json:"errorMessage,omitempty"`
}

func (TestDiscoveryTaskSnapshotV16) isTaskSnapshotV16() {}

type TestRunTaskSnapshotV16 struct {
	TaskID          string          `json:"taskId"`
	Kind            TaskKindV16     `json:"kind"`
	ProjectID       string          `json:"projectId"`
	ProfileID       string          `json:"profileId"`
	CatalogRevision string          `json:"catalogRevision"`
	RunID           string          `json:"runId"`
	RepeatCount     int64           `json:"repeatCount"`
	Status          TaskStatusV16   `json:"status"`
	Outcome         *TaskOutcomeV16 `json:"outcome,omitempty"`
	CreatedAt       time.Time       `json:"createdAt"`
	StartedAt       *time.Time      `json:"startedAt,omitempty"`
	FinishedAt      *time.Time      `json:"finishedAt,omitempty"`
	LastSequence    int64           `json:"lastSequence"`
	ErrorCode       *string         `json:"errorCode,omitempty"`
	ErrorMessage    *string         `json:"errorMessage,omitempty"`
}

func (TestRunTaskSnapshotV16) isTaskSnapshotV16() {}

type CoverageRunTaskSnapshotV16 struct {
	TaskID              string          `json:"taskId"`
	Kind                TaskKindV16     `json:"kind"`
	WorkspaceGeneration string          `json:"workspaceGeneration"`
	ProjectID           string          `json:"projectId"`
	CoverageProfileID   string          `json:"coverageProfileId"`
	CatalogRevision     string          `json:"catalogRevision"`
	CoverageRunID       string          `json:"coverageRunId"`
	TestRunID           string          `json:"testRunId"`
	RepeatCount         int64           `json:"repeatCount"`
	TimeoutMS           int64           `json:"timeoutMs"`
	Status              TaskStatusV16   `json:"status"`
	Outcome             *TaskOutcomeV16 `json:"outcome,omitempty"`
	CreatedAt           time.Time       `json:"createdAt"`
	StartedAt           *time.Time      `json:"startedAt,omitempty"`
	FinishedAt          *time.Time      `json:"finishedAt,omitempty"`
	LastSequence        int64           `json:"lastSequence"`
	ErrorCode           *string         `json:"errorCode,omitempty"`
	ErrorMessage        *string         `json:"errorMessage,omitempty"`
}

func (CoverageRunTaskSnapshotV16) isTaskSnapshotV16() {}

type TestGenerationTaskSnapshotV16 struct {
	TaskID              string          `json:"taskId"`
	Kind                TaskKindV16     `json:"kind"`
	RunID               string          `json:"runId"`
	WorkspaceGeneration string          `json:"workspaceGeneration"`
	ProjectID           string          `json:"projectId"`
	Status              TaskStatusV16   `json:"status"`
	Outcome             *TaskOutcomeV16 `json:"outcome,omitempty"`
	CreatedAt           time.Time       `json:"createdAt"`
	StartedAt           *time.Time      `json:"startedAt,omitempty"`
	FinishedAt          *time.Time      `json:"finishedAt,omitempty"`
	LastSequence        int64           `json:"lastSequence"`
	ErrorCode           *string         `json:"errorCode,omitempty"`
	ErrorMessage        *string         `json:"errorMessage,omitempty"`
}

func (TestGenerationTaskSnapshotV16) isTaskSnapshotV16() {}

type TaskKindV16 string

const (
	TaskKindCmakeBuildV16     TaskKindV16 = "cmakeBuild"
	TaskKindSimulationV16     TaskKindV16 = "simulation"
	TaskKindTestDiscoveryV16  TaskKindV16 = "testDiscovery"
	TaskKindTestRunV16        TaskKindV16 = "testRun"
	TaskKindCoverageRunV16    TaskKindV16 = "coverageRun"
	TaskKindTestGenerationV16 TaskKindV16 = "testGeneration"
)

type TaskStatusV16 string

const (
	TaskQueuedV16     TaskStatusV16 = "queued"
	TaskRunningV16    TaskStatusV16 = "running"
	TaskCancellingV16 TaskStatusV16 = "cancelling"
	TaskFinishedV16   TaskStatusV16 = "finished"
)

type TaskOutcomeV16 string

const (
	TaskSucceededV16            TaskOutcomeV16 = "succeeded"
	TaskCommandFailedV16        TaskOutcomeV16 = "command_failed"
	TaskCancelledV16            TaskOutcomeV16 = "cancelled"
	TaskTimedOutV16             TaskOutcomeV16 = "timed_out"
	TaskInterruptedV16          TaskOutcomeV16 = "interrupted"
	TaskInfrastructureFailedV16 TaskOutcomeV16 = "infrastructure_failed"
)

type SimulationScenarioV16 string

const (
	SimulationSuccessV16     SimulationScenarioV16 = "success"
	SimulationExitNonzeroV16 SimulationScenarioV16 = "exit-nonzero"
	SimulationHangV16        SimulationScenarioV16 = "hang"
	SimulationSpawnChildV16  SimulationScenarioV16 = "spawn-child"
	SimulationEmitOutputV16  SimulationScenarioV16 = "emit-output"
)
