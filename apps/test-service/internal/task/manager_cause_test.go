package task

import (
	"context"
	"testing"
	"time"
)

func TestManagerCloseFailureUsesClaimedTimeoutBeforeTimeoutCommand(t *testing.T) {
	manager, current, active, store := newCauseBarrierFixture()
	current.task.ActiveStep = ""
	current.task.Steps = []StepSnapshot{
		{ID: "first", Kind: StepSimulation, Status: StepSucceeded},
		{ID: "second", Kind: StepSimulation, Status: StepPending},
	}
	current.nextStep = 1
	store.task = current.task
	current.execution.claim(OutcomeTimedOut)

	finished, err := manager.finishAfterCloseFailure(current, active)
	if err != nil {
		t.Fatal(err)
	}
	if finished.Outcome != OutcomeTimedOut || store.task.Outcome != OutcomeTimedOut {
		t.Fatalf("terminal outcomes = memory:%s store:%s, want %s", finished.Outcome, store.task.Outcome, OutcomeTimedOut)
	}
	if got := store.task.Steps[1].Status; got != StepSkipped {
		t.Fatalf("unstarted step status = %s, want %s", got, StepSkipped)
	}
}

func TestManagerProcessDoneUsesClaimedTimeoutBeforeTimeoutCommand(t *testing.T) {
	manager, current, active, store := newCauseBarrierFixture()
	current.task.ActiveStep = "first"
	current.task.Steps = []StepSnapshot{
		{ID: "first", Kind: StepSimulation, Status: StepRunning},
		{ID: "second", Kind: StepSimulation, Status: StepPending},
	}
	current.plan = ExecutionPlan{Steps: []ExecutionStep{
		{ID: "first", Kind: StepSimulation},
		{ID: "second", Kind: StepSimulation},
	}}
	current.processCompleted = true
	store.task = current.task
	current.execution.claim(OutcomeTimedOut)

	manager.finish(current, ProcessResult{ExitCode: 0}, active)

	if current.task.Outcome != OutcomeTimedOut || store.task.Outcome != OutcomeTimedOut {
		t.Fatalf("terminal outcomes = memory:%s store:%s, want %s", current.task.Outcome, store.task.Outcome, OutcomeTimedOut)
	}
	if got := store.task.Steps[0].Status; got != StepFailed {
		t.Fatalf("completed-after-deadline first step status = %s, want %s", got, StepFailed)
	}
	if got := store.task.Steps[1].Status; got != StepSkipped {
		t.Fatalf("later step status = %s, want %s", got, StepSkipped)
	}
}

func newCauseBarrierFixture() (*Manager, *activeTask, map[string]*activeTask, *causeBarrierStore) {
	now := time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)
	store := &causeBarrierStore{}
	manager := &Manager{
		store: store, publisher: causeBarrierPublisher{}, artifacts: causeBarrierArtifacts{},
		clock: causeBarrierClock{now: now}, newID: func() string { return "00000000000000000000000000000002" },
	}
	current := &activeTask{
		task: Task{
			ID: "00000000000000000000000000000001", Status: StatusRunning,
			CreatedAt: now.Add(-time.Minute), StartedAt: timePointer(now.Add(-time.Minute)),
		},
		timerStop: make(chan struct{}), timeoutStop: make(chan struct{}),
		watcherStop: make(chan struct{}), execution: newExecutionSignal(),
	}
	store.task = current.task
	active := map[string]*activeTask{current.task.ID: current}
	return manager, current, active, store
}

type causeBarrierStore struct {
	Store
	task Task
}

func (s *causeBarrierStore) Apply(_ context.Context, mutation Mutation) (Task, []Event, error) {
	steps := append([]StepSnapshot(nil), s.task.Steps...)
	for _, change := range mutation.Steps {
		for index := range steps {
			if steps[index].ID == change.Step.ID {
				steps[index] = change.Step
				break
			}
		}
	}
	mutation.Task.Steps = steps
	s.task = mutation.Task
	return s.task, nil, nil
}

type causeBarrierArtifacts struct{}

func (causeBarrierArtifacts) CommitJSON(_ context.Context, taskID, artifactID string, at time.Time, _ any) (Artifact, error) {
	return Artifact{ID: artifactID, TaskID: taskID, Kind: "summary", CreatedAt: at}, nil
}

type causeBarrierPublisher struct{}

func (causeBarrierPublisher) Publish(Event) {}

type causeBarrierClock struct{ now time.Time }

func (c causeBarrierClock) Now() time.Time                     { return c.now }
func (causeBarrierClock) After(time.Duration) <-chan time.Time { return make(chan time.Time) }
