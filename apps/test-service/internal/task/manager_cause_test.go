package task

import (
	"context"
	"errors"
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

func TestPersistCommittedCreateFailurePreservesClaimedCause(t *testing.T) {
	for _, want := range []Outcome{OutcomeCancelled, OutcomeTimedOut, OutcomeInterrupted} {
		t.Run(string(want), func(t *testing.T) {
			manager, current, active, store := newCauseBarrierFixture()
			current.task.Status = StatusQueued
			current.task.StartedAt = nil
			current.task.Steps = []StepSnapshot{
				{ID: "first", Kind: StepSimulation, Status: StepPending},
				{ID: "second", Kind: StepSimulation, Status: StepPending},
			}
			store.task = current.task
			manager.executionSignals.Store(current.task.ID, current.execution)
			current.execution.claim(want)

			finished, err := manager.persistCommittedCreateFailure(current, active)
			if err != nil {
				t.Fatal(err)
			}
			if finished.Outcome != want || store.task.Outcome != want {
				t.Fatalf("outcomes = memory:%s store:%s, want %s",
					finished.Outcome, store.task.Outcome, want)
			}
			if len(active) != 0 {
				t.Fatalf("active tasks = %d, want 0", len(active))
			}
		})
	}
}

func TestManagerPublisherFailurePreservesClaimedTimeout(t *testing.T) {
	const (
		taskID = "00000000000000000000000000000001"
		key    = "00000000000000000000000000000002"
	)
	const timeout = 17 * time.Second
	now := time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)
	store := &publisherFailureBarrierStore{}
	release := make(chan struct{})
	publisher := &publisherFailureBarrierPublisher{
		entered: make(chan Event, 1),
		release: release,
	}
	clock := &publisherFailureBarrierClock{
		now:  now,
		wait: make(chan time.Time),
	}
	manager := &Manager{
		store: store, publisher: publisher, processes: publisherFailureBarrierProcesses{},
		artifacts: causeBarrierArtifacts{}, clock: clock, newID: func() string { return taskID },
		serviceExecutable: "trusted-service", serviceInstanceID: "00000000000000000000000000000003",
		commands: make(chan any, 1), stopped: make(chan struct{}),
	}
	manager.healthy.Store(true)
	plan := ExecutionPlan{
		Version: 1,
		Steps: []ExecutionStep{{
			ID:   "simulate",
			Kind: StepSimulation,
			Process: ProcessSpec{
				Executable: "trusted-service",
				Dir:        "simulation-dir",
			},
		}},
	}
	plan.Fingerprint = FingerprintPlan(plan)
	request := StartRequest{
		IdempotencyKey: key,
		Kind:           KindSimulation,
		Request:        []byte(`{"scenario":"success","timeoutMs":17000}`),
		Scenario:       ScenarioSuccess,
		Timeout:        timeout,
		Plan:           plan,
		Boundary:       publisherFailureBarrierBoundary{},
	}
	active := make(map[string]*activeTask)
	startResult := make(chan taskResponse, 1)
	go func() { startResult <- manager.start(request, active) }()

	select {
	case <-publisher.entered:
	case <-time.After(time.Second):
		t.Fatal("task.created publisher was not entered")
	}
	if got := clock.afterCalls(timeout); got != 1 {
		t.Fatalf("plan-wide timeout waiters = %d, want 1", got)
	}
	value, ok := manager.executionSignals.Load(taskID)
	if !ok {
		t.Fatal("committed task has no execution decision")
	}
	signal := value.(*executionSignal)
	clock.fire(t, timeout)
	select {
	case <-signal.ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("timeout decision was not claimed before publisher release")
	}
	requested, _ := signal.state()
	if requested != OutcomeTimedOut {
		t.Fatalf("claimed cause = %s, want timed_out", requested)
	}
	close(release)

	select {
	case result := <-startResult:
		if !errors.Is(result.err, ErrStorageUnavailable) {
			t.Fatalf("Start error = %v, want ErrStorageUnavailable", result.err)
		}
	case <-time.After(time.Second):
		t.Fatal("Start did not return after publisher failure")
	}
	if store.task.Status != StatusFinished || store.task.Outcome != OutcomeTimedOut {
		t.Fatalf("stored task = %#v, want finished/timed_out", store.task)
	}
	for index, step := range store.task.Steps {
		if step.Status != StepSkipped {
			t.Fatalf("step[%d] = %s, want skipped", index, step.Status)
		}
	}
}

func TestManagerShutdownClaimsVisibleDecisionsBeforeCommandDelivery(t *testing.T) {
	manager := &Manager{
		shutdownSignal: make(chan struct{}, 1),
		stopped:        make(chan struct{}),
	}
	manager.healthy.Store(true)
	signal := newExecutionSignal()
	manager.executionSignals.Store("task", signal)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- manager.Shutdown(ctx) }()

	select {
	case <-signal.ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("Shutdown did not claim visible execution decision")
	}
	requested, _ := signal.state()
	if requested != OutcomeInterrupted {
		t.Fatalf("shutdown cause = %s, want interrupted", requested)
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("Shutdown error = %v, want context canceled", err)
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

type publisherFailureBarrierStore struct {
	Store
	task     Task
	events   []Event
	sequence int64
}

func (s *publisherFailureBarrierStore) Create(
	_ context.Context,
	value Task,
	steps []StepSnapshot,
	draft EventDraft,
) (Task, []Event, error) {
	event := s.append(draft)
	value.Steps = append([]StepSnapshot(nil), steps...)
	value.LastSequence = event.Sequence
	s.task = value
	return value, []Event{event}, nil
}

func (s *publisherFailureBarrierStore) Apply(_ context.Context, mutation Mutation) (Task, []Event, error) {
	if s.task.Status != mutation.Expected {
		return Task{}, nil, ErrConflict
	}
	steps := append([]StepSnapshot(nil), s.task.Steps...)
	for _, change := range mutation.Steps {
		for index := range steps {
			if steps[index].ID == change.Step.ID {
				if steps[index].Status != change.Expected {
					return Task{}, nil, ErrConflict
				}
				steps[index] = change.Step
				break
			}
		}
	}
	events := make([]Event, len(mutation.Events))
	for index, draft := range mutation.Events {
		events[index] = s.append(draft)
	}
	mutation.Task.Steps = steps
	if len(events) > 0 {
		mutation.Task.LastSequence = events[len(events)-1].Sequence
	}
	s.task = mutation.Task
	return s.task, events, nil
}

func (s *publisherFailureBarrierStore) append(draft EventDraft) Event {
	s.sequence++
	event := Event{
		Sequence:   s.sequence,
		ID:         "00000000000000000000000000000004",
		EventDraft: draft,
	}
	s.events = append(s.events, event)
	return event
}

type publisherFailureBarrierPublisher struct {
	entered chan Event
	release <-chan struct{}
}

func (p *publisherFailureBarrierPublisher) Publish(event Event) {
	if event.Type != EventTaskCreated {
		return
	}
	p.entered <- event
	<-p.release
	panic("publisher failure after block")
}

type publisherFailureBarrierProcesses struct{}

func (publisherFailureBarrierProcesses) Prepare(context.Context, ProcessSpec, string, string) (ManagedProcess, error) {
	panic("Prepare called after publisher failure")
}

type publisherFailureBarrierBoundary struct{}

func (publisherFailureBarrierBoundary) ValidateExecutable(string) error       { return nil }
func (publisherFailureBarrierBoundary) ValidateWorkingDirectory(string) error { return nil }

type publisherFailureBarrierClock struct {
	now   time.Time
	wait  chan time.Time
	calls []time.Duration
}

func (c *publisherFailureBarrierClock) Now() time.Time {
	return c.now
}

func (c *publisherFailureBarrierClock) After(delay time.Duration) <-chan time.Time {
	c.calls = append(c.calls, delay)
	return c.wait
}

func (c *publisherFailureBarrierClock) fire(t *testing.T, delay time.Duration) {
	t.Helper()
	c.now = c.now.Add(delay)
	select {
	case c.wait <- c.now:
	case <-time.After(time.Second):
		t.Fatal("timeout watcher did not receive clock fire")
	}
}

func (c *publisherFailureBarrierClock) afterCalls(delay time.Duration) int {
	count := 0
	for _, call := range c.calls {
		if call == delay {
			count++
		}
	}
	return count
}
