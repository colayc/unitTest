package task

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"time"
)

type Status string
type Outcome string
type Scenario string
type EventType string

const (
	StatusQueued     Status = "queued"
	StatusRunning    Status = "running"
	StatusCancelling Status = "cancelling"
	StatusFinished   Status = "finished"

	OutcomeSucceeded            Outcome = "succeeded"
	OutcomeCommandFailed        Outcome = "command_failed"
	OutcomeCancelled            Outcome = "cancelled"
	OutcomeTimedOut             Outcome = "timed_out"
	OutcomeInterrupted          Outcome = "interrupted"
	OutcomeInfrastructureFailed Outcome = "infrastructure_failed"

	ScenarioSuccess     Scenario = "success"
	ScenarioExitNonzero Scenario = "exit-nonzero"
	ScenarioHang        Scenario = "hang"
	ScenarioSpawnChild  Scenario = "spawn-child"
	ScenarioEmitOutput  Scenario = "emit-output"

	EventTaskCreated               EventType = "task.created"
	EventTaskStarted               EventType = "task.started"
	EventTaskStepStarted           EventType = "task.step_started"
	EventTaskStepFinished          EventType = "task.step_finished"
	EventTaskOutput                EventType = "task.output"
	EventTaskCancellationRequested EventType = "task.cancellation_requested"
	EventTaskFinished              EventType = "task.finished"
	EventArtifactCreated           EventType = "artifact.created"
	EventTaskDiagnostic            EventType = "task.diagnostic"
)

type Task struct {
	ID, IdempotencyKey, RequestHash string
	Kind                            Kind
	Request                         json.RawMessage
	WorkspaceGeneration             string
	PlanFingerprint                 string
	ActiveStep                      string
	Steps                           []StepSnapshot
	Scenario                        Scenario
	Timeout                         time.Duration
	Status                          Status
	Outcome                         Outcome
	CreatedAt                       time.Time
	StartedAt, FinishedAt           *time.Time
	LastSequence                    int64
	ErrorCode, ErrorMessage         string
}

type StepSnapshot struct {
	ID         string     `json:"id"`
	Kind       StepKind   `json:"kind"`
	Status     StepStatus `json:"status"`
	StartedAt  *time.Time `json:"startedAt"`
	FinishedAt *time.Time `json:"finishedAt"`
	ExitCode   *int       `json:"exitCode"`
	ErrorCode  string     `json:"errorCode"`
}

type TaskSummary struct {
	TaskID     string         `json:"taskId"`
	Kind       Kind           `json:"kind"`
	Outcome    Outcome        `json:"outcome"`
	FinishedAt time.Time      `json:"finishedAt"`
	Steps      []StepSnapshot `json:"steps"`
}

type EventDraft struct {
	TaskID  string
	Type    EventType
	At      time.Time
	Payload json.RawMessage
}

type Event struct {
	Sequence int64
	ID       string
	EventDraft
}

type Artifact struct {
	ID, TaskID, Kind, RelativePath, MIMEType, SHA256 string
	Size                                             int64
	CreatedAt                                        time.Time
}

type ProcessLease struct {
	TaskID, HostStartIdentity, ServiceInstanceID string
	HostPID, TargetProcessGroup                  int
	TargetProcessGroups                          []int
}

type Page[T any] struct {
	Items      []T
	NextCursor string
}

func NewID() string {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(value[:])
}

func ValidScenario(value Scenario) bool {
	switch value {
	case ScenarioSuccess, ScenarioExitNonzero, ScenarioHang, ScenarioSpawnChild, ScenarioEmitOutput:
		return true
	default:
		return false
	}
}
