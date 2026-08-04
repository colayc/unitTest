package protocolmodelv14task

import "time"

type TaskSnapshotV14 interface{ isTaskSnapshotV14() }
type CmakeBuildTaskSnapshotV14 struct {
	TaskID              string          `json:"taskId"`
	Kind                TaskKindV14     `json:"kind"`
	WorkspaceGeneration string          `json:"workspaceGeneration"`
	ProjectID           string          `json:"projectId"`
	BuildProfileID      string          `json:"buildProfileId"`
	TargetIDs           []string        `json:"targetIds"`
	Jobs                int64           `json:"jobs"`
	TimeoutMS           int64           `json:"timeoutMs"`
	Status              TaskStatusV14   `json:"status"`
	Outcome             *TaskOutcomeV14 `json:"outcome,omitempty"`
	CreatedAt           time.Time       `json:"createdAt"`
	StartedAt           *time.Time      `json:"startedAt,omitempty"`
	FinishedAt          *time.Time      `json:"finishedAt,omitempty"`
	LastSequence        int64           `json:"lastSequence"`
	ErrorCode           *string         `json:"errorCode,omitempty"`
	ErrorMessage        *string         `json:"errorMessage,omitempty"`
}

func (CmakeBuildTaskSnapshotV14) isTaskSnapshotV14() {}

type SimulationTaskSnapshotV14 struct {
	TaskID       string                `json:"taskId"`
	Kind         TaskKindV14           `json:"kind"`
	Scenario     SimulationScenarioV14 `json:"scenario"`
	TimeoutMS    *int64                `json:"timeoutMs,omitempty"`
	Status       TaskStatusV14         `json:"status"`
	Outcome      *TaskOutcomeV14       `json:"outcome,omitempty"`
	CreatedAt    time.Time             `json:"createdAt"`
	StartedAt    *time.Time            `json:"startedAt,omitempty"`
	FinishedAt   *time.Time            `json:"finishedAt,omitempty"`
	LastSequence int64                 `json:"lastSequence"`
	ErrorCode    *string               `json:"errorCode,omitempty"`
	ErrorMessage *string               `json:"errorMessage,omitempty"`
}

func (SimulationTaskSnapshotV14) isTaskSnapshotV14() {}

type TestDiscoveryTaskSnapshotV14 struct {
	TaskID          string          `json:"taskId"`
	Kind            TaskKindV14     `json:"kind"`
	ProjectID       string          `json:"projectId"`
	ProfileID       string          `json:"profileId"`
	CatalogRevision *string         `json:"catalogRevision,omitempty"`
	Status          TaskStatusV14   `json:"status"`
	Outcome         *TaskOutcomeV14 `json:"outcome,omitempty"`
	CreatedAt       time.Time       `json:"createdAt"`
	StartedAt       *time.Time      `json:"startedAt,omitempty"`
	FinishedAt      *time.Time      `json:"finishedAt,omitempty"`
	LastSequence    int64           `json:"lastSequence"`
	ErrorCode       *string         `json:"errorCode,omitempty"`
	ErrorMessage    *string         `json:"errorMessage,omitempty"`
}

func (TestDiscoveryTaskSnapshotV14) isTaskSnapshotV14() {}

type TestRunTaskSnapshotV14 struct {
	TaskID          string          `json:"taskId"`
	Kind            TaskKindV14     `json:"kind"`
	ProjectID       string          `json:"projectId"`
	ProfileID       string          `json:"profileId"`
	CatalogRevision string          `json:"catalogRevision"`
	RunID           string          `json:"runId"`
	RepeatCount     int64           `json:"repeatCount"`
	Status          TaskStatusV14   `json:"status"`
	Outcome         *TaskOutcomeV14 `json:"outcome,omitempty"`
	CreatedAt       time.Time       `json:"createdAt"`
	StartedAt       *time.Time      `json:"startedAt,omitempty"`
	FinishedAt      *time.Time      `json:"finishedAt,omitempty"`
	LastSequence    int64           `json:"lastSequence"`
	ErrorCode       *string         `json:"errorCode,omitempty"`
	ErrorMessage    *string         `json:"errorMessage,omitempty"`
}

func (TestRunTaskSnapshotV14) isTaskSnapshotV14() {}

type CoverageRunTaskSnapshotV14 struct {
	TaskID              string          `json:"taskId"`
	Kind                TaskKindV14     `json:"kind"`
	WorkspaceGeneration string          `json:"workspaceGeneration"`
	ProjectID           string          `json:"projectId"`
	CoverageProfileID   string          `json:"coverageProfileId"`
	CatalogRevision     string          `json:"catalogRevision"`
	CoverageRunID       string          `json:"coverageRunId"`
	TestRunID           string          `json:"testRunId"`
	RepeatCount         int64           `json:"repeatCount"`
	TimeoutMS           int64           `json:"timeoutMs"`
	Status              TaskStatusV14   `json:"status"`
	Outcome             *TaskOutcomeV14 `json:"outcome,omitempty"`
	CreatedAt           time.Time       `json:"createdAt"`
	StartedAt           *time.Time      `json:"startedAt,omitempty"`
	FinishedAt          *time.Time      `json:"finishedAt,omitempty"`
	LastSequence        int64           `json:"lastSequence"`
	ErrorCode           *string         `json:"errorCode,omitempty"`
	ErrorMessage        *string         `json:"errorMessage,omitempty"`
}

func (CoverageRunTaskSnapshotV14) isTaskSnapshotV14() {}

type TaskKindV14 string

const (
	TaskKindCmakeBuildV14    TaskKindV14 = "cmakeBuild"
	TaskKindSimulationV14    TaskKindV14 = "simulation"
	TaskKindTestDiscoveryV14 TaskKindV14 = "testDiscovery"
	TaskKindTestRunV14       TaskKindV14 = "testRun"
	TaskKindCoverageRunV14   TaskKindV14 = "coverageRun"
)

type TaskStatusV14 string

const (
	TaskQueuedV14     TaskStatusV14 = "queued"
	TaskRunningV14    TaskStatusV14 = "running"
	TaskCancellingV14 TaskStatusV14 = "cancelling"
	TaskFinishedV14   TaskStatusV14 = "finished"
)

type TaskOutcomeV14 string

const (
	TaskSucceededV14            TaskOutcomeV14 = "succeeded"
	TaskCommandFailedV14        TaskOutcomeV14 = "command_failed"
	TaskCancelledV14            TaskOutcomeV14 = "cancelled"
	TaskTimedOutV14             TaskOutcomeV14 = "timed_out"
	TaskInterruptedV14          TaskOutcomeV14 = "interrupted"
	TaskInfrastructureFailedV14 TaskOutcomeV14 = "infrastructure_failed"
)

type SimulationScenarioV14 string

const (
	SimulationSuccessV14     SimulationScenarioV14 = "success"
	SimulationExitNonzeroV14 SimulationScenarioV14 = "exit-nonzero"
	SimulationHangV14        SimulationScenarioV14 = "hang"
	SimulationSpawnChildV14  SimulationScenarioV14 = "spawn-child"
	SimulationEmitOutputV14  SimulationScenarioV14 = "emit-output"
)
