package protocolmodelv12task

import "time"

type TaskSnapshotV12 interface{ isTaskSnapshotV12() }
type CmakeBuildTaskSnapshotV12 struct {
	TaskID              string          `json:"taskId"`
	Kind                TaskKindV12     `json:"kind"`
	WorkspaceGeneration string          `json:"workspaceGeneration"`
	ProjectID           string          `json:"projectId"`
	BuildProfileID      string          `json:"buildProfileId"`
	TargetIDs           []string        `json:"targetIds"`
	Jobs                int64           `json:"jobs"`
	TimeoutMS           int64           `json:"timeoutMs"`
	Status              TaskStatusV12   `json:"status"`
	CreatedAt           time.Time       `json:"createdAt"`
	LastSequence        int64           `json:"lastSequence"`
	Outcome             *TaskOutcomeV12 `json:"outcome,omitempty"`
	StartedAt           *time.Time      `json:"startedAt,omitempty"`
	FinishedAt          *time.Time      `json:"finishedAt,omitempty"`
	ErrorCode           *string         `json:"errorCode,omitempty"`
	ErrorMessage        *string         `json:"errorMessage,omitempty"`
}

func (CmakeBuildTaskSnapshotV12) isTaskSnapshotV12() {}

type SimulationTaskSnapshotV12 struct {
	TaskID       string                `json:"taskId"`
	Kind         TaskKindV12           `json:"kind"`
	Scenario     SimulationScenarioV12 `json:"scenario"`
	Status       TaskStatusV12         `json:"status"`
	CreatedAt    time.Time             `json:"createdAt"`
	LastSequence int64                 `json:"lastSequence"`
	TimeoutMS    *int64                `json:"timeoutMs,omitempty"`
	Outcome      *TaskOutcomeV12       `json:"outcome,omitempty"`
	StartedAt    *time.Time            `json:"startedAt,omitempty"`
	FinishedAt   *time.Time            `json:"finishedAt,omitempty"`
	ErrorCode    *string               `json:"errorCode,omitempty"`
	ErrorMessage *string               `json:"errorMessage,omitempty"`
}

func (SimulationTaskSnapshotV12) isTaskSnapshotV12() {}

type TaskKindV12 string

const (
	CmakeBuild TaskKindV12 = "cmakeBuild"
	Simulation TaskKindV12 = "simulation"
)

type TaskOutcomeV12 string

const (
	Cancelled            TaskOutcomeV12 = "cancelled"
	CommandFailed        TaskOutcomeV12 = "command_failed"
	InfrastructureFailed TaskOutcomeV12 = "infrastructure_failed"
	Interrupted          TaskOutcomeV12 = "interrupted"
	Succeeded            TaskOutcomeV12 = "succeeded"
	TimedOut             TaskOutcomeV12 = "timed_out"
)

type SimulationScenarioV12 string

const (
	EmitOutput  SimulationScenarioV12 = "emit-output"
	ExitNonzero SimulationScenarioV12 = "exit-nonzero"
	Hang        SimulationScenarioV12 = "hang"
	SpawnChild  SimulationScenarioV12 = "spawn-child"
	Success     SimulationScenarioV12 = "success"
)

type TaskStatusV12 string

const (
	Cancelling TaskStatusV12 = "cancelling"
	Finished   TaskStatusV12 = "finished"
	Queued     TaskStatusV12 = "queued"
	Running    TaskStatusV12 = "running"
)
