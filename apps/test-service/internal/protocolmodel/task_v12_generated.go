package protocolmodel

import "time"

type TaskSnapshotV12 struct {
	CreatedAt           time.Time  `json:"createdAt"`
	ErrorCode           *string    `json:"errorCode,omitempty"`
	ErrorMessage        *string    `json:"errorMessage,omitempty"`
	FinishedAt          *time.Time `json:"finishedAt,omitempty"`
	LastSequence        int64      `json:"lastSequence"`
	Outcome             *Outcome   `json:"outcome,omitempty"`
	StartedAt           *time.Time `json:"startedAt,omitempty"`
	Status              Status     `json:"status"`
	TaskID              string     `json:"taskId"`
	TimeoutMS           *int64     `json:"timeoutMs,omitempty"`
	BuildProfileID      *string    `json:"buildProfileId,omitempty"`
	Jobs                *int64     `json:"jobs,omitempty"`
	Kind                Kind       `json:"kind"`
	ProjectID           *string    `json:"projectId,omitempty"`
	TargetIDS           []string   `json:"targetIds,omitempty"`
	WorkspaceGeneration *string    `json:"workspaceGeneration,omitempty"`
	Scenario            *Scenario  `json:"scenario,omitempty"`
}

type Kind string

const (
	CmakeBuild Kind = "cmakeBuild"
	Simulation Kind = "simulation"
)

type Outcome string

const (
	Cancelled            Outcome = "cancelled"
	CommandFailed        Outcome = "command_failed"
	InfrastructureFailed Outcome = "infrastructure_failed"
	Interrupted          Outcome = "interrupted"
	Succeeded            Outcome = "succeeded"
	TimedOut             Outcome = "timed_out"
)

type Scenario string

const (
	EmitOutput  Scenario = "emit-output"
	ExitNonzero Scenario = "exit-nonzero"
	Hang        Scenario = "hang"
	SpawnChild  Scenario = "spawn-child"
	Success     Scenario = "success"
)

type Status string

const (
	Cancelling Status = "cancelling"
	Finished   Status = "finished"
	Queued     Status = "queued"
	Running    Status = "running"
)
