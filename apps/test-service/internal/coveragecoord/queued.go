// Package coveragecoord owns the protocol-to-storage handoff for coverage runs.
// It deliberately stops at a queued aggregate: execution and report publication
// are separate stages and must not be faked by this package.
package coveragecoord

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"unit-test-ide.local/test-service/internal/coveragedomain"
	"unit-test-ide.local/test-service/internal/task"
	"unit-test-ide.local/test-service/internal/testdomain"
)

var ErrInvalidQueuedInput = errors.New("invalid queued coverage input")

type QueuedInput struct {
	Request        coveragedomain.Request
	Selection      testdomain.SelectionSnapshot
	BuildProfileID string
	ToolchainID    string
	Toolchain      coveragedomain.ToolchainSnapshot
	CreatedAt      time.Time
	NewID          task.IDGenerator
}

type QueuedAggregate struct {
	Task     task.Task
	Run      coveragedomain.Run
	TestRun  testdomain.TestRun
	Steps    []task.StepSnapshot
	Event    task.EventDraft
}

func NewQueuedAggregate(input QueuedInput) (QueuedAggregate, error) {
	request, err := coveragedomain.NewRequest(input.Request)
	if err != nil || input.CreatedAt.IsZero() || input.NewID == nil || input.BuildProfileID == "" || input.ToolchainID == "" {
		return QueuedAggregate{}, ErrInvalidQueuedInput
	}
	selection, err := testdomain.NewSelectionSnapshot(input.Selection)
	if err != nil {
		return QueuedAggregate{}, ErrInvalidQueuedInput
	}
	createdAt := input.CreatedAt.UTC()
	runID, err := coveragedomain.CoverageRunID(request)
	if err != nil {
		return QueuedAggregate{}, ErrInvalidQueuedInput
	}
	taskID, testRunID := input.NewID(), input.NewID()
	if len(taskID) != 32 || len(testRunID) != 32 {
		return QueuedAggregate{}, ErrInvalidQueuedInput
	}
	steps := queuedSteps()
	planFingerprint := fingerprintSteps(steps)
	requestJSON, err := request.CanonicalJSON()
	if err != nil {
		return QueuedAggregate{}, ErrInvalidQueuedInput
	}
	requestHash := sha256.Sum256(requestJSON)
	taskValue := task.Task{
		ID: taskID, IdempotencyKey: request.IdempotencyKey,
		RequestHash: hex.EncodeToString(requestHash[:]), Kind: task.KindCoverageRun,
		Request: requestJSON, WorkspaceGeneration: request.WorkspaceGeneration,
		PlanFingerprint: planFingerprint, Timeout: request.Timeout,
		Status: task.StatusQueued, CreatedAt: createdAt,
	}
	run := coveragedomain.Run{
		ID: runID, TaskID: taskID, TestRunID: testRunID,
		Status: coveragedomain.StatusQueued, Request: request,
		SelectionSnapshot: selection, Toolchain: input.Toolchain,
		CreatedAt: createdAt,
	}
	testRun := testdomain.TestRun{
		RunID: testRunID, TaskID: taskID, ProjectID: request.ProjectID,
		ProfileID: input.BuildProfileID, ToolchainID: input.ToolchainID,
		CatalogRevision: request.CatalogRevision, SelectionSnapshot: selection,
		Status: testdomain.RunQueued, Summary: testdomain.RunSummary{Iterations: request.RepeatCount},
		ResultRevision: testdomain.EmptyResultRevision(), IdempotencyKey: request.IdempotencyKey,
		CreatedAt: createdAt, Incomplete: true,
	}
	run, err = coveragedomain.NewRun(run)
	if err != nil {
		return QueuedAggregate{}, ErrInvalidQueuedInput
	}
	testRun, err = testdomain.NewTestRun(testRun)
	if err != nil {
		return QueuedAggregate{}, ErrInvalidQueuedInput
	}
	eventPayload, _ := json.Marshal(struct{ Kind task.Kind `json:"kind"` }{Kind: task.KindCoverageRun})
	return QueuedAggregate{Task: taskValue, Run: run, TestRun: testRun, Steps: steps, Event: task.EventDraft{
		TaskID: taskID, Type: task.EventTaskCreated, At: createdAt, Payload: eventPayload,
	}}, nil
}

func (aggregate QueuedAggregate) Persist(ctx context.Context, store task.CoverageTaskStore) (task.Task, []task.Event, error) {
	if store == nil || ctx == nil || aggregate.Task.ID == "" {
		return task.Task{}, nil, ErrInvalidQueuedInput
	}
	return store.CreateCoverageTask(ctx, aggregate.Task, aggregate.Steps, aggregate.Event, aggregate.Run, aggregate.TestRun)
}

func queuedSteps() []task.StepSnapshot {
	kinds := []task.StepKind{
		task.StepCoverageConfigure, task.StepCoverageBuild, task.StepCoverageTest,
		task.StepCoverageMerge, task.StepCoverageNormalize, task.StepCoverageReport,
		task.StepCoveragePublish,
	}
	steps := make([]task.StepSnapshot, len(kinds))
	for index, kind := range kinds {
		steps[index] = task.StepSnapshot{ID: string(kind), Kind: kind, Status: task.StepPending}
	}
	return steps
}

func fingerprintSteps(steps []task.StepSnapshot) string {
	plan := task.ExecutionPlan{Version: 1, Steps: make([]task.ExecutionStep, len(steps))}
	for index, step := range steps {
		plan.Steps[index] = task.ExecutionStep{ID: step.ID, Kind: step.Kind}
	}
	return task.FingerprintPlan(plan)
}
