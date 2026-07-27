package task

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestManagerOrdinaryClosePathDoesNotRetryFailedClose(t *testing.T) {
	process := newPreparedLeaseCircuitProcess()
	manager := &Manager{
		commands:            make(chan any, 1),
		processCloseTimeout: time.Second,
	}
	current := &activeTask{
		task:             Task{ID: "00000000000000000000000000000001"},
		process:          process,
		processCompleted: true,
		closeFailed:      true,
	}

	manager.maybeStartClose(current)

	if current.closeStarted {
		t.Fatal("ordinary close path retried a failed Close")
	}
	if got := process.closeCalls(); got != 0 {
		t.Fatalf("Close calls = %d, want 0 before explicit Shutdown retry", got)
	}
}

func TestManagerRecordCloseFailureResolvesCauseBeforePublishingUnhealthy(t *testing.T) {
	manager := &Manager{}
	manager.healthy.Store(true)
	signal := newExecutionSignal()
	resolveEntered := make(chan struct{})
	releaseResolve := make(chan struct{})
	defer func() {
		select {
		case <-releaseResolve:
		default:
			close(releaseResolve)
		}
	}()
	originalCancel := signal.cancel
	signal.cancel = func() {
		close(resolveEntered)
		<-releaseResolve
		originalCancel()
	}
	current := &activeTask{execution: signal}
	recorded := make(chan struct{})
	go func() {
		manager.recordCloseFailure(current)
		close(recorded)
	}()

	awaitCauseSignal(t, resolveEntered, "Close failure did not enter cause resolution")
	if !manager.Healthy() {
		t.Fatal("Manager published unhealthy before Close failure cause resolution completed")
	}

	close(releaseResolve)
	awaitCauseSignal(t, recorded, "Close failure recording did not complete")
	if got := current.execution.currentCause(); got != OutcomeInfrastructureFailed {
		t.Fatalf("Close failure cause = %s, want %s", got, OutcomeInfrastructureFailed)
	}
	if !current.failPendingStep {
		t.Fatal("Close failure did not mark the pending step failed")
	}
	if manager.Healthy() {
		t.Fatal("Manager remained healthy after Close failure was recorded")
	}
}

func TestManagerTimeoutCommandDoesNotRetryFailedCloseBeforeShutdown(t *testing.T) {
	store := newPreparedLeaseCircuitStore()
	publisher := &preparedLeaseRecordingPublisher{}
	first := newPreparedLeaseCircuitProcess()
	second := newPreparedLeaseCircuitProcess()
	first.setErrors(nil, errors.New("first Close failed after timeout was claimed"))
	releaseClose := make(chan struct{})
	first.setCloseBlock(releaseClose)
	processes := &preparedLeaseQueueProcesses{queue: []*preparedLeaseCircuitProcess{first, second}}
	artifacts := &preparedLeaseCircuitArtifacts{}
	ids := []string{
		"00000000000000000000000000000051",
		"00000000000000000000000000000052",
	}
	nextID := 0
	manager, err := NewManager(ManagerConfig{
		Store:               store,
		Publisher:           publisher,
		Processes:           processes,
		Artifacts:           artifacts,
		Clock:               causeBarrierClock{now: time.Date(2026, 7, 27, 15, 0, 0, 0, time.UTC)},
		NewID:               func() string { id := ids[nextID]; nextID++; return id },
		ServiceExecutable:   "trusted-service",
		ServiceInstanceID:   "00000000000000000000000000000099",
		TerminationGrace:    time.Millisecond,
		ProcessCloseTimeout: time.Second,
		CommandQueue:        4,
	})
	if err != nil {
		t.Fatal(err)
	}
	var releaseCloseOnce sync.Once
	var releaseFirstGetOnce sync.Once
	var releaseSecondGetOnce sync.Once
	firstGetRelease := make(chan struct{})
	secondGetRelease := make(chan struct{})
	t.Cleanup(func() {
		releaseCloseOnce.Do(func() { close(releaseClose) })
		releaseFirstGetOnce.Do(func() { close(firstGetRelease) })
		releaseSecondGetOnce.Do(func() { close(secondGetRelease) })
		first.setCloseBlock(nil)
		first.setErrors(nil, nil)
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = manager.Shutdown(ctx)
	})

	started, err := manager.Start(
		context.Background(),
		preparedLeaseTwoStepRequest("00000000000000000000000000000053"),
	)
	if err != nil {
		t.Fatal(err)
	}
	first.complete(ProcessResult{ExitCode: 0})
	awaitPreparedLeaseCloseCalls(t, first, 1)

	firstGetEntered := make(chan struct{})
	secondGetEntered := make(chan struct{})
	store.setGetBarrier(1, firstGetEntered, firstGetRelease)
	store.setGetBarrier(2, secondGetEntered, secondGetRelease)
	firstGetReply := make(chan taskResponse, 1)
	manager.commands <- taskIDCommand{id: started.ID, reply: firstGetReply}
	awaitCauseSignal(t, firstGetEntered, "first command-loop barrier was not entered")

	value, ok := manager.executionSignals.Load(started.ID)
	if !ok {
		t.Fatal("active task has no execution signal")
	}
	if got := value.(*executionSignal).claim(OutcomeTimedOut); got != OutcomeTimedOut {
		t.Fatalf("claimed cause = %s, want %s", got, OutcomeTimedOut)
	}
	releaseCloseOnce.Do(func() { close(releaseClose) })
	awaitCommandQueueLength(t, manager.commands, 1, "Close result was not enqueued")
	manager.commands <- timeoutCommand(started.ID)
	secondGetReply := make(chan taskResponse, 1)
	manager.commands <- taskIDCommand{id: started.ID, reply: secondGetReply}
	releaseFirstGetOnce.Do(func() { close(firstGetRelease) })
	awaitCauseSignal(t, secondGetEntered, "timeout command was not consumed after Close result")

	if got := first.closeCalls(); got != 1 {
		t.Fatalf("Close calls before explicit Shutdown = %d, want 1", got)
	}
	durable, err := store.Get(context.Background(), started.ID)
	if err != nil {
		t.Fatal(err)
	}
	if durable.Status == StatusFinished || durable.Outcome != "" {
		t.Fatalf("task before explicit Shutdown = %#v, want nonterminal without outcome", durable)
	}
	if got := processes.prepareCount(); got != 1 {
		t.Fatalf("Prepare calls before explicit Shutdown = %d, want 1", got)
	}

	first.setCloseBlock(nil)
	first.setErrors(nil, nil)
	releaseSecondGetOnce.Do(func() { close(secondGetRelease) })
	for name, reply := range map[string]<-chan taskResponse{
		"first":  firstGetReply,
		"second": secondGetReply,
	} {
		select {
		case response := <-reply:
			if response.err != nil {
				t.Fatalf("%s command-loop barrier = %v", name, response.err)
			}
		case <-time.After(time.Second):
			t.Fatalf("%s command-loop barrier did not return", name)
		}
	}

	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), time.Second)
	defer cancelShutdown()
	if err := manager.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("explicit Shutdown retry = %v", err)
	}
	if got := first.closeCalls(); got != 2 {
		t.Fatalf("Close calls after explicit Shutdown = %d, want 2", got)
	}
	finished, err := store.Get(context.Background(), started.ID)
	if err != nil {
		t.Fatal(err)
	}
	if finished.Status != StatusFinished || finished.Outcome != OutcomeTimedOut {
		t.Fatalf("task after explicit Shutdown = %#v, want finished/timed_out", finished)
	}
	if got := []StepStatus{finished.Steps[0].Status, finished.Steps[1].Status}; got[0] != StepSucceeded || got[1] != StepSkipped {
		t.Fatalf("step statuses = %v, want [%s %s]", got, StepSucceeded, StepSkipped)
	}
	if got := processes.prepareCount(); got != 1 {
		t.Fatalf("Prepare calls after explicit Shutdown = %d, want 1", got)
	}
	if got := second.startCalls(); got != 0 {
		t.Fatalf("second Start calls = %d, want 0", got)
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
			current.execution.claim(OutcomeInterrupted)

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

func TestManagerPublisherFailureCloseErrorHandsOffQueuedPreparedLease(t *testing.T) {
	store := newPreparedLeaseCircuitStore()
	publisher := &preparedLeaseCircuitPublisher{}
	process := newPreparedLeaseCircuitProcess()
	process.setErrors(
		errors.New("terminate failed after Publisher circuit"),
		errors.New("close failed after Publisher circuit"),
	)
	releaseClose := make(chan struct{})
	var releaseCloseOnce sync.Once
	process.setCloseBlock(releaseClose)
	releasePrepare := make(chan struct{})
	var releaseOnce sync.Once
	processes := &preparedLeaseCircuitProcesses{
		process:  process,
		entered:  make(chan struct{}),
		canceled: make(chan struct{}),
		release:  releasePrepare,
	}
	artifacts := &preparedLeaseCircuitArtifacts{}
	ids := []string{
		"00000000000000000000000000000011",
		"00000000000000000000000000000012",
		"00000000000000000000000000000013",
	}
	nextID := 0
	manager, err := NewManager(ManagerConfig{
		Store:             store,
		Publisher:         publisher,
		Processes:         processes,
		Artifacts:         artifacts,
		Clock:             causeBarrierClock{now: time.Date(2026, 7, 27, 14, 0, 0, 0, time.UTC)},
		NewID:             func() string { id := ids[nextID]; nextID++; return id },
		ServiceExecutable: "trusted-service",
		ServiceInstanceID: "00000000000000000000000000000099",
		TerminationGrace:  time.Millisecond,
		CommandQueue:      4,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		releaseOnce.Do(func() { close(releasePrepare) })
		releaseCloseOnce.Do(func() { close(releaseClose) })
		process.setErrors(nil, nil)
		process.complete(ProcessResult{Err: errors.New("test cleanup")})
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := manager.Shutdown(ctx); err != nil {
			t.Errorf("cleanup Shutdown = %v", err)
		}
	})

	startA := make(chan taskResponse, 1)
	go func() {
		got, startErr := manager.Start(context.Background(), preparedLeaseCircuitRequest(
			"00000000000000000000000000000021",
		))
		startA <- taskResponse{task: got, err: startErr}
	}()
	awaitCauseSignal(t, processes.entered, "Task A Prepare was not entered")
	taskAID := processes.preparedTaskID()

	replyB := make(chan taskResponse, 1)
	manager.commands <- startCommand{
		request: preparedLeaseCircuitRequest("00000000000000000000000000000022"),
		reply:   replyB,
	}
	cancelA := make(chan taskResponse, 1)
	go func() {
		got, cancelErr := manager.Cancel(context.Background(), taskAID)
		cancelA <- taskResponse{task: got, err: cancelErr}
	}()
	awaitCauseSignal(t, processes.canceled, "Task A cancellation did not claim the blocked Prepare")
	releaseOnce.Do(func() { close(releasePrepare) })

	select {
	case response := <-startA:
		if response.err != nil || response.task.Status != StatusQueued {
			t.Fatalf("Task A Start = %#v, %v", response.task, response.err)
		}
	case <-time.After(time.Second):
		t.Fatal("Task A Start did not return")
	}
	select {
	case response := <-replyB:
		if !errors.Is(response.err, ErrStorageUnavailable) {
			t.Fatalf("Task B Start error = %v, want ErrStorageUnavailable", response.err)
		}
	case <-time.After(time.Second):
		t.Fatal("Task B Start did not trip Publisher circuit")
	}
	select {
	case <-cancelA:
	case <-time.After(time.Second):
		t.Fatal("Task A Cancel did not return after Publisher circuit")
	}
	awaitPreparedLeaseProcessCalls(t, process)

	firstGetEntered := make(chan struct{})
	firstGetRelease := make(chan struct{})
	secondGetEntered := make(chan struct{})
	secondGetRelease := make(chan struct{})
	store.setGetBarrier(1, firstGetEntered, firstGetRelease)
	store.setGetBarrier(2, secondGetEntered, secondGetRelease)
	firstGetReply := make(chan taskResponse, 1)
	manager.commands <- taskIDCommand{id: taskAID, reply: firstGetReply}
	awaitCauseSignal(t, firstGetEntered, "first command-loop barrier was not entered")
	releaseCloseOnce.Do(func() { close(releaseClose) })
	awaitCommandQueueLength(t, manager.commands, 1, "Close result was not enqueued")
	secondGetReply := make(chan taskResponse, 1)
	manager.commands <- taskIDCommand{id: taskAID, reply: secondGetReply}
	close(firstGetRelease)
	awaitCauseSignal(t, secondGetEntered, "Close result was not consumed before second barrier")
	close(secondGetRelease)
	for name, reply := range map[string]<-chan taskResponse{
		"first":  firstGetReply,
		"second": secondGetReply,
	} {
		select {
		case response := <-reply:
			if response.err != nil {
				t.Fatalf("%s command-loop barrier = %v", name, response.err)
			}
		case <-time.After(time.Second):
			t.Fatalf("%s command-loop barrier did not return", name)
		}
	}

	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancelShutdown()
	if err := manager.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("Shutdown after durable handoff = %v", err)
	}
	if got := process.closeCalls(); got != 1 {
		t.Fatalf("Close calls after durable handoff Shutdown = %d, want 1", got)
	}

	durableA, err := store.Get(context.Background(), taskAID)
	if err != nil ||
		durableA.Status != StatusQueued ||
		durableA.Outcome != "" {
		t.Fatalf("prepared task after handoff = %#v, %v", durableA, err)
	}
	lease := store.lease(taskAID)
	if lease.TaskID != taskAID || lease.HostPID == 0 {
		t.Fatalf("queued recovery lease = %#v", lease)
	}
	if got := preparedLeaseEventTypes(store.eventsForTask(taskAID)); !equalEventTypes(got, []EventType{
		EventTaskCreated,
	}) {
		t.Fatalf("prepared task durable events = %v", got)
	}
	var publishedA []Event
	for _, event := range publisher.events() {
		if event.TaskID == taskAID {
			publishedA = append(publishedA, event)
		}
	}
	if len(publishedA) != 1 || publishedA[0].Type != EventTaskCreated {
		t.Fatalf("prepared task published events = %#v", publishedA)
	}
	if artifacts.callCount() != 0 {
		t.Fatal("prepared recovery handoff created terminal artifact")
	}
	if process.startCalls() != 0 ||
		process.terminateCalls() != 1 ||
		process.closeCalls() != 1 {
		t.Fatalf("process calls: start=%d terminate=%d close=%d",
			process.startCalls(), process.terminateCalls(), process.closeCalls())
	}
}

func TestManagerPreparedLeaseStoreFailureKeepsOwnerWhenCloseFails(t *testing.T) {
	store := newPreparedLeaseCircuitStore()
	store.failApplyMatch = func(mutation Mutation) error {
		if isCausePreparedLeaseMutation(mutation) {
			return ErrStorageUnavailable
		}
		return nil
	}
	publisher := &preparedLeaseRecordingPublisher{}
	process := newPreparedLeaseCircuitProcess()
	process.setErrors(nil, errors.New("first Close failed without a durable lease"))
	releaseClose := make(chan struct{})
	process.setCloseBlock(releaseClose)
	releasePrepare := make(chan struct{})
	close(releasePrepare)
	processes := &preparedLeaseCircuitProcesses{
		process:  process,
		entered:  make(chan struct{}),
		canceled: make(chan struct{}),
		release:  releasePrepare,
	}
	artifacts := &preparedLeaseCircuitArtifacts{}
	ids := []string{
		"00000000000000000000000000000041",
		"00000000000000000000000000000042",
	}
	nextID := 0
	manager, err := NewManager(ManagerConfig{
		Store:             store,
		Publisher:         publisher,
		Processes:         processes,
		Artifacts:         artifacts,
		Clock:             causeBarrierClock{now: time.Date(2026, 7, 27, 14, 30, 0, 0, time.UTC)},
		NewID:             func() string { id := ids[nextID]; nextID++; return id },
		ServiceExecutable: "trusted-service",
		ServiceInstanceID: "00000000000000000000000000000099",
		TerminationGrace:  time.Millisecond,
		CommandQueue:      4,
	})
	if err != nil {
		t.Fatal(err)
	}
	var releaseCloseOnce sync.Once
	t.Cleanup(func() {
		process.setErrors(nil, nil)
		process.complete(ProcessResult{Err: errors.New("test cleanup")})
		releaseCloseOnce.Do(func() { close(releaseClose) })
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = manager.Shutdown(ctx)
	})

	accepted, err := manager.Start(context.Background(), preparedLeaseCircuitRequest(
		"00000000000000000000000000000043",
	))
	if !errors.Is(err, ErrStorageUnavailable) {
		t.Fatalf("Start error = %v, want ErrStorageUnavailable", err)
	}
	if accepted.ID == "" || accepted.Status != StatusQueued {
		t.Fatalf("accepted task = %#v, want durable queued task", accepted)
	}
	awaitPreparedLeaseProcessCalls(t, process)
	if manager.Healthy() {
		t.Fatal("manager remained healthy after prepared lease Store failure")
	}
	if lease := store.lease(accepted.ID); lease.TaskID != "" {
		t.Fatalf("failed pre-lease unexpectedly persisted %#v", lease)
	}
	if got := preparedLeaseEventTypes(store.eventsForTask(accepted.ID)); !equalEventTypes(got, []EventType{
		EventTaskCreated,
	}) {
		t.Fatalf("durable events before Close succeeds = %v", got)
	}
	if got := preparedLeaseEventTypes(publisher.events()); !equalEventTypes(got, []EventType{
		EventTaskCreated,
	}) {
		t.Fatalf("published events before Close succeeds = %v", got)
	}
	if artifacts.callCount() != 0 {
		t.Fatal("failed pre-lease created a terminal artifact")
	}

	baseShutdown, cancelShutdown := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancelShutdown()
	observedShutdown := &causeObservedContext{
		Context: baseShutdown,
		waiting: make(chan struct{}),
	}
	shutdownResult := make(chan error, 1)
	go func() { shutdownResult <- manager.Shutdown(observedShutdown) }()
	awaitCauseSignal(t, observedShutdown.waiting, "Shutdown did not enter its bounded wait")
	awaitAtomicFalse(t, &manager.shutdownPending, "Shutdown command was not consumed while Close was blocked")
	if err := <-shutdownResult; !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Shutdown error = %v, want deadline while no durable handoff exists", err)
	}
	if process.closeCalls() != 1 {
		t.Fatalf("Close calls = %d, want one bounded attempt", process.closeCalls())
	}

	firstGetEntered := make(chan struct{})
	firstGetRelease := make(chan struct{})
	secondGetEntered := make(chan struct{})
	secondGetRelease := make(chan struct{})
	store.setGetBarrier(1, firstGetEntered, firstGetRelease)
	store.setGetBarrier(2, secondGetEntered, secondGetRelease)
	firstGetReply := make(chan taskResponse, 1)
	manager.commands <- taskIDCommand{id: accepted.ID, reply: firstGetReply}
	awaitCauseSignal(t, firstGetEntered, "first command-loop barrier was not entered")
	releaseCloseOnce.Do(func() { close(releaseClose) })
	awaitCommandQueueLength(t, manager.commands, 1, "Close result was not enqueued")
	secondGetReply := make(chan taskResponse, 1)
	manager.commands <- taskIDCommand{id: accepted.ID, reply: secondGetReply}
	close(firstGetRelease)
	awaitCauseSignal(t, secondGetEntered, "Close result was not consumed before second barrier")

	process.setErrors(nil, nil)
	close(secondGetRelease)
	select {
	case <-firstGetReply:
	case <-time.After(time.Second):
		t.Fatal("first command-loop barrier did not return")
	}
	select {
	case <-secondGetReply:
	case <-time.After(time.Second):
		t.Fatal("second command-loop barrier did not return")
	}
	retry, cancelRetry := context.WithTimeout(context.Background(), time.Second)
	defer cancelRetry()
	if err := manager.Shutdown(retry); err != nil {
		t.Fatalf("retry Shutdown = %v", err)
	}
	if process.closeCalls() != 2 {
		t.Fatalf("Close calls after successful retry = %d, want 2", process.closeCalls())
	}
	durable, err := store.Get(context.Background(), accepted.ID)
	if err != nil || durable.Status != StatusQueued || durable.Outcome != "" {
		t.Fatalf("durable task after local cleanup = %#v, %v", durable, err)
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
			ID:   "00000000000000000000000000000001",
			Kind: KindSimulation, Scenario: ScenarioSuccess, Status: StatusRunning,
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

func preparedLeaseCircuitRequest(key string) StartRequest {
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
	return StartRequest{
		IdempotencyKey: key,
		Kind:           KindSimulation,
		Request:        []byte(`{"scenario":"success","timeoutMs":60000}`),
		Scenario:       ScenarioSuccess,
		Timeout:        time.Minute,
		Plan:           plan,
		Boundary:       publisherFailureBarrierBoundary{},
	}
}

func preparedLeaseTwoStepRequest(key string) StartRequest {
	request := preparedLeaseCircuitRequest(key)
	first := request.Plan.Steps[0]
	first.ID = "first"
	second := first
	second.ID = "second"
	request.Plan.Steps = []ExecutionStep{first, second}
	request.Plan.Fingerprint = FingerprintPlan(request.Plan)
	return request
}

func awaitCauseSignal(t *testing.T, signal <-chan struct{}, message string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(time.Second):
		t.Fatal(message)
	}
}

type preparedLeaseCircuitStore struct {
	Store
	mu             sync.Mutex
	tasks          map[string]Task
	keys           map[string]string
	leases         map[string]ProcessLease
	eventsLog      []Event
	sequence       int64
	failApplyMatch func(Mutation) error
	getCalls       int
	getBarriers    map[int]preparedLeaseGetBarrier
}

func newPreparedLeaseCircuitStore() *preparedLeaseCircuitStore {
	return &preparedLeaseCircuitStore{
		tasks:       make(map[string]Task),
		keys:        make(map[string]string),
		leases:      make(map[string]ProcessLease),
		getBarriers: make(map[int]preparedLeaseGetBarrier),
	}
}

func (s *preparedLeaseCircuitStore) Create(
	_ context.Context,
	value Task,
	steps []StepSnapshot,
	draft EventDraft,
) (Task, []Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if existingID, ok := s.keys[value.IdempotencyKey]; ok {
		return s.tasks[existingID], nil, nil
	}
	event := s.appendLocked(draft)
	value.Steps = append([]StepSnapshot(nil), steps...)
	value.LastSequence = event.Sequence
	s.tasks[value.ID] = value
	s.keys[value.IdempotencyKey] = value.ID
	return value, []Event{event}, nil
}

func (s *preparedLeaseCircuitStore) Get(_ context.Context, taskID string) (Task, error) {
	s.mu.Lock()
	s.getCalls++
	barrier := s.getBarriers[s.getCalls]
	value, ok := s.tasks[taskID]
	s.mu.Unlock()
	if barrier.entered != nil {
		close(barrier.entered)
	}
	if barrier.release != nil {
		<-barrier.release
	}
	if !ok {
		return Task{}, ErrNotFound
	}
	return value, nil
}

func (s *preparedLeaseCircuitStore) Apply(
	_ context.Context,
	mutation Mutation,
) (Task, []Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failApplyMatch != nil {
		if err := s.failApplyMatch(mutation); err != nil {
			return Task{}, nil, err
		}
	}
	current, ok := s.tasks[mutation.Task.ID]
	if !ok {
		return Task{}, nil, ErrNotFound
	}
	if current.Status != mutation.Expected {
		return Task{}, nil, ErrConflict
	}
	steps := append([]StepSnapshot(nil), current.Steps...)
	for _, change := range mutation.Steps {
		found := false
		for index := range steps {
			if steps[index].ID != change.Step.ID {
				continue
			}
			if steps[index].Status != change.Expected {
				return Task{}, nil, ErrConflict
			}
			steps[index] = change.Step
			found = true
			break
		}
		if !found {
			return Task{}, nil, ErrConflict
		}
	}
	events := make([]Event, 0, len(mutation.Events))
	for _, draft := range mutation.Events {
		events = append(events, s.appendLocked(draft))
	}
	mutation.Task.Steps = steps
	if len(events) != 0 {
		mutation.Task.LastSequence = events[len(events)-1].Sequence
	}
	s.tasks[mutation.Task.ID] = mutation.Task
	if mutation.PutLease != nil {
		s.leases[mutation.Task.ID] = *mutation.PutLease
	}
	if mutation.DeleteLease {
		delete(s.leases, mutation.Task.ID)
	}
	return mutation.Task, events, nil
}

func (s *preparedLeaseCircuitStore) UpdateLease(_ context.Context, lease ProcessLease) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.leases[lease.TaskID] = lease
	return nil
}

func (s *preparedLeaseCircuitStore) appendLocked(draft EventDraft) Event {
	s.sequence++
	event := Event{
		Sequence:   s.sequence,
		ID:         "00000000000000000000000000000031",
		EventDraft: draft,
	}
	s.eventsLog = append(s.eventsLog, event)
	return event
}

func (s *preparedLeaseCircuitStore) lease(taskID string) ProcessLease {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.leases[taskID]
}

func (s *preparedLeaseCircuitStore) eventsForTask(taskID string) []Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	var result []Event
	for _, event := range s.eventsLog {
		if event.TaskID == taskID {
			result = append(result, event)
		}
	}
	return result
}

type preparedLeaseGetBarrier struct {
	entered chan struct{}
	release <-chan struct{}
}

func (s *preparedLeaseCircuitStore) setGetBarrier(
	call int,
	entered chan struct{},
	release <-chan struct{},
) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.getBarriers[call] = preparedLeaseGetBarrier{entered: entered, release: release}
}

type preparedLeaseCircuitPublisher struct {
	mu           sync.Mutex
	createdCalls int
	value        []Event
}

func (p *preparedLeaseCircuitPublisher) Publish(event Event) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if event.Type == EventTaskCreated {
		p.createdCalls++
		if p.createdCalls == 2 {
			panic("Task B publisher failure")
		}
	}
	p.value = append(p.value, event)
}

func (p *preparedLeaseCircuitPublisher) events() []Event {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]Event(nil), p.value...)
}

type preparedLeaseRecordingPublisher struct {
	mu    sync.Mutex
	value []Event
}

func (p *preparedLeaseRecordingPublisher) Publish(event Event) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.value = append(p.value, event)
}

func (p *preparedLeaseRecordingPublisher) events() []Event {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]Event(nil), p.value...)
}

type preparedLeaseCircuitArtifacts struct {
	mu    sync.Mutex
	calls int
}

func (a *preparedLeaseCircuitArtifacts) CommitJSON(
	_ context.Context,
	taskID string,
	artifactID string,
	at time.Time,
	_ any,
) (Artifact, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.calls++
	return Artifact{ID: artifactID, TaskID: taskID, Kind: "summary", CreatedAt: at}, nil
}

func (a *preparedLeaseCircuitArtifacts) callCount() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.calls
}

type preparedLeaseCircuitProcesses struct {
	mu       sync.Mutex
	process  *preparedLeaseCircuitProcess
	entered  chan struct{}
	canceled chan struct{}
	release  <-chan struct{}
	taskID   string
}

type preparedLeaseQueueProcesses struct {
	mu       sync.Mutex
	queue    []*preparedLeaseCircuitProcess
	prepares int
}

func (p *preparedLeaseQueueProcesses) Prepare(
	_ context.Context,
	_ ProcessSpec,
	taskID string,
	serviceID string,
) (ManagedProcess, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.queue) == 0 {
		return nil, errors.New("no prepared process queued")
	}
	process := p.queue[0]
	p.queue = p.queue[1:]
	p.prepares++
	process.setLeaseIDs(taskID, serviceID)
	return process, nil
}

func (p *preparedLeaseQueueProcesses) prepareCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.prepares
}

func (p *preparedLeaseCircuitProcesses) Prepare(
	ctx context.Context,
	_ ProcessSpec,
	taskID string,
	serviceID string,
) (ManagedProcess, error) {
	p.mu.Lock()
	p.taskID = taskID
	p.process.setLeaseIDs(taskID, serviceID)
	close(p.entered)
	p.mu.Unlock()
	go func() {
		<-ctx.Done()
		close(p.canceled)
	}()
	<-p.release
	return p.process, nil
}

func (p *preparedLeaseCircuitProcesses) preparedTaskID() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.taskID
}

type preparedLeaseCircuitProcess struct {
	mu           sync.Mutex
	leaseValue   ProcessLease
	outputs      chan ProcessOutput
	done         chan ProcessResult
	completeOnce sync.Once
	starts       int
	terminates   int
	closes       int
	terminateErr error
	closeErr     error
	closeBlock   <-chan struct{}
}

func newPreparedLeaseCircuitProcess() *preparedLeaseCircuitProcess {
	return &preparedLeaseCircuitProcess{
		leaseValue: ProcessLease{
			HostPID:           401,
			HostStartIdentity: "prepared-host",
		},
		outputs: make(chan ProcessOutput),
		done:    make(chan ProcessResult, 1),
	}
}

func (p *preparedLeaseCircuitProcess) setLeaseIDs(taskID, serviceID string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.leaseValue.TaskID = taskID
	p.leaseValue.ServiceInstanceID = serviceID
}

func (p *preparedLeaseCircuitProcess) setErrors(terminateErr, closeErr error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.terminateErr = terminateErr
	p.closeErr = closeErr
}

func (p *preparedLeaseCircuitProcess) setCloseBlock(block <-chan struct{}) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.closeBlock = block
}

func (p *preparedLeaseCircuitProcess) Lease() ProcessLease {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.leaseValue
}

func (p *preparedLeaseCircuitProcess) Start(context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.starts++
	return nil
}

func (p *preparedLeaseCircuitProcess) Output() <-chan ProcessOutput {
	return p.outputs
}

func (p *preparedLeaseCircuitProcess) Done() <-chan ProcessResult {
	return p.done
}

func (p *preparedLeaseCircuitProcess) Terminate(context.Context, time.Duration) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.terminates++
	return p.terminateErr
}

func (p *preparedLeaseCircuitProcess) Close(ctx context.Context) error {
	p.mu.Lock()
	p.closes++
	block, closeErr := p.closeBlock, p.closeErr
	p.mu.Unlock()
	if block != nil {
		select {
		case <-block:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return closeErr
}

func (p *preparedLeaseCircuitProcess) startCalls() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.starts
}

func (p *preparedLeaseCircuitProcess) terminateCalls() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.terminates
}

func (p *preparedLeaseCircuitProcess) closeCalls() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.closes
}

func (p *preparedLeaseCircuitProcess) complete(result ProcessResult) {
	p.completeOnce.Do(func() {
		close(p.outputs)
		p.done <- result
		close(p.done)
	})
}

func awaitPreparedLeaseProcessCalls(t *testing.T, process *preparedLeaseCircuitProcess) {
	t.Helper()
	deadline := time.After(time.Second)
	for process.terminateCalls() < 1 || process.closeCalls() < 1 {
		select {
		case <-deadline:
			t.Fatalf("process calls: terminate=%d close=%d",
				process.terminateCalls(), process.closeCalls())
		default:
		}
	}
}

func awaitPreparedLeaseCloseCalls(t *testing.T, process *preparedLeaseCircuitProcess, want int) {
	t.Helper()
	deadline := time.After(time.Second)
	for process.closeCalls() < want {
		select {
		case <-deadline:
			t.Fatalf("Close calls = %d, want at least %d", process.closeCalls(), want)
		default:
		}
	}
}

func preparedLeaseEventTypes(events []Event) []EventType {
	result := make([]EventType, len(events))
	for index := range events {
		result[index] = events[index].Type
	}
	return result
}

func equalEventTypes(left, right []EventType) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func isCausePreparedLeaseMutation(mutation Mutation) bool {
	return mutation.PutLease != nil &&
		mutation.Task.Status == mutation.Expected &&
		len(mutation.Steps) == 0 &&
		len(mutation.Events) == 0 &&
		len(mutation.Artifacts) == 0 &&
		!mutation.DeleteLease
}

type causeObservedContext struct {
	context.Context
	waiting     chan struct{}
	waitingOnce sync.Once
}

func (c *causeObservedContext) Done() <-chan struct{} {
	c.waitingOnce.Do(func() { close(c.waiting) })
	return c.Context.Done()
}

func awaitAtomicFalse(t *testing.T, value *atomic.Bool, message string) {
	t.Helper()
	deadline := time.After(time.Second)
	for value.Load() {
		select {
		case <-deadline:
			t.Fatal(message)
		default:
		}
	}
}

func awaitCommandQueueLength(
	t *testing.T,
	commands chan any,
	want int,
	message string,
) {
	t.Helper()
	deadline := time.After(time.Second)
	for len(commands) < want {
		select {
		case <-deadline:
			t.Fatal(message)
		default:
		}
	}
}
