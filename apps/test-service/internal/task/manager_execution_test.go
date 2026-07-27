package task_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"unit-test-ide.local/test-service/internal/task"
)

func TestManagerRunsPlanStepsSequentially(t *testing.T) {
	f := newManagerFixture(t)
	first := f.process
	second := newFakeProcess()
	f.processes.queue = []*fakeProcess{first, second}
	t.Cleanup(func() { second.completeOnce(task.ProcessResult{Err: errors.New("test cleanup")}) })

	releaseClose := make(chan struct{})
	first.closeBlock = releaseClose
	released := false
	t.Cleanup(func() {
		if !released {
			close(releaseClose)
		}
	})

	started, err := f.manager.Start(context.Background(), twoStepStartRequest(testID(90), time.Minute, fixedBoundary{}))
	if err != nil {
		t.Fatal(err)
	}
	first.complete(task.ProcessResult{ExitCode: 0})
	f.awaitProcessClose(t, first)
	if got := f.processes.prepareCount(); got != 1 {
		t.Fatalf("Prepare calls before first Close returned = %d, want 1", got)
	}
	if got := second.startCalls(); got != 0 {
		t.Fatalf("second Start calls before first Close returned = %d, want 0", got)
	}

	close(releaseClose)
	released = true
	awaitProcessStart(t, second)
	second.complete(task.ProcessResult{ExitCode: 0})

	finished := f.awaitTask(t, started.ID, task.StatusFinished)
	if finished.Outcome != task.OutcomeSucceeded || finished.ActiveStep != "" {
		t.Fatalf("finished task = %#v", finished)
	}
	assertStepStatuses(t, finished, task.StepSucceeded, task.StepSucceeded)
}

func TestManagerStopsAfterStepFailure(t *testing.T) {
	tests := []struct {
		name    string
		stop    func(*testing.T, *managerFixture, task.Task)
		result  task.ProcessResult
		outcome task.Outcome
	}{
		{
			name:   "nonzero",
			stop:   func(_ *testing.T, _ *managerFixture, _ task.Task) {},
			result: task.ProcessResult{ExitCode: 17}, outcome: task.OutcomeCommandFailed,
		},
		{
			name: "cancel",
			stop: func(t *testing.T, f *managerFixture, started task.Task) {
				t.Helper()
				if _, err := f.manager.Cancel(context.Background(), started.ID); err != nil {
					t.Fatal(err)
				}
				f.awaitProcessTerminate(t, f.process, 1)
			},
			result: task.ProcessResult{ExitCode: 137}, outcome: task.OutcomeCancelled,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newManagerFixture(t)
			second := newFakeProcess()
			f.processes.queue = []*fakeProcess{f.process, second}
			t.Cleanup(func() { second.completeOnce(task.ProcessResult{Err: errors.New("test cleanup")}) })

			started, err := f.manager.Start(context.Background(), twoStepStartRequest(testID(91), time.Minute, fixedBoundary{}))
			if err != nil {
				t.Fatal(err)
			}
			tt.stop(t, f, started)
			f.process.complete(tt.result)
			finished := f.awaitTask(t, started.ID, task.StatusFinished)

			if finished.Outcome != tt.outcome {
				t.Fatalf("outcome = %s, want %s", finished.Outcome, tt.outcome)
			}
			assertStepStatuses(t, finished, task.StepFailed, task.StepSkipped)
			if got := f.processes.prepareCount(); got != 1 {
				t.Fatalf("Prepare calls = %d, want 1", got)
			}
			if got := second.startCalls(); got != 0 {
				t.Fatalf("second Start calls = %d, want 0", got)
			}
		})
	}
}

func TestManagerPlanTimeoutIsOneTotalBudget(t *testing.T) {
	f := newManagerFixture(t)
	second := newFakeProcess()
	f.processes.queue = []*fakeProcess{f.process, second}
	t.Cleanup(func() { second.completeOnce(task.ProcessResult{Err: errors.New("test cleanup")}) })

	const timeout = 7 * time.Second
	started, err := f.manager.Start(context.Background(), twoStepStartRequest(testID(92), timeout, fixedBoundary{}))
	if err != nil {
		t.Fatal(err)
	}
	f.process.complete(task.ProcessResult{ExitCode: 0})
	awaitProcessStart(t, second)
	if got := f.clock.afterCalls(timeout); got != 1 {
		t.Fatalf("plan timeout timers = %d, want 1", got)
	}

	f.clock.fire(t, timeout)
	f.awaitProcessTerminate(t, second, 1)
	second.complete(task.ProcessResult{ExitCode: 137})
	finished := f.awaitTask(t, started.ID, task.StatusFinished)
	if finished.Outcome != task.OutcomeTimedOut {
		t.Fatalf("outcome = %s, want %s", finished.Outcome, task.OutcomeTimedOut)
	}
	assertStepStatuses(t, finished, task.StepSucceeded, task.StepFailed)
}

func TestManagerTotalTimeoutDuringNextStepPreparePreventsStart(t *testing.T) {
	f := newManagerFixture(t)
	second := newFakeProcess()
	f.processes.queue = []*fakeProcess{f.process, second}
	t.Cleanup(func() { second.completeOnce(task.ProcessResult{Err: errors.New("test cleanup")}) })
	prepareEntered := make(chan struct{})
	prepareCanceled := make(chan struct{})
	releasePrepare := make(chan struct{})
	f.processes.prepareBlockAt = 2
	f.processes.prepareEntered = prepareEntered
	f.processes.prepareCanceled = prepareCanceled
	f.processes.prepareBlock = releasePrepare
	const timeout = 11 * time.Second

	started, err := f.manager.Start(context.Background(), twoStepStartRequest(testID(82), timeout, fixedBoundary{}))
	if err != nil {
		t.Fatal(err)
	}
	f.process.complete(task.ProcessResult{ExitCode: 0})
	select {
	case <-prepareEntered:
	case <-time.After(time.Second):
		t.Fatal("second Prepare was not entered")
	}
	f.clock.fire(t, timeout)
	awaitSignal(t, prepareCanceled, "second Prepare context was not cancelled by total timeout")
	close(releasePrepare)

	f.awaitProcessTerminate(t, second, 1)
	second.complete(task.ProcessResult{ExitCode: 137})
	finished := f.awaitTask(t, started.ID, task.StatusFinished)
	if finished.Outcome != task.OutcomeTimedOut {
		t.Fatalf("outcome = %s, want %s", finished.Outcome, task.OutcomeTimedOut)
	}
	assertStepStatuses(t, finished, task.StepSucceeded, task.StepSkipped)
	if got := second.startCalls(); got != 0 {
		t.Fatalf("second Start calls after total timeout = %d, want 0", got)
	}
}

func TestManagerCancellationDuringNextStepPreparePreventsStart(t *testing.T) {
	f := newManagerFixture(t)
	second := newFakeProcess()
	f.processes.queue = []*fakeProcess{f.process, second}
	t.Cleanup(func() { second.completeOnce(task.ProcessResult{Err: errors.New("test cleanup")}) })
	prepareEntered := make(chan struct{})
	prepareCanceled := make(chan struct{})
	releasePrepare := make(chan struct{})
	f.processes.prepareBlockAt = 2
	f.processes.prepareEntered = prepareEntered
	f.processes.prepareCanceled = prepareCanceled
	f.processes.prepareBlock = releasePrepare

	started, err := f.manager.Start(context.Background(), twoStepStartRequest(testID(83), time.Minute, fixedBoundary{}))
	if err != nil {
		t.Fatal(err)
	}
	f.process.complete(task.ProcessResult{ExitCode: 0})
	select {
	case <-prepareEntered:
	case <-time.After(time.Second):
		t.Fatal("second Prepare was not entered")
	}
	cancelResult := make(chan taskResponseResult, 1)
	go func() {
		got, cancelErr := f.manager.Cancel(context.Background(), started.ID)
		cancelResult <- taskResponseResult{task: got, err: cancelErr}
	}()
	awaitSignal(t, prepareCanceled, "second Prepare context was not cancelled by Cancel")
	close(releasePrepare)

	select {
	case result := <-cancelResult:
		if result.err != nil || result.task.Status != task.StatusCancelling {
			t.Fatalf("Cancel = %#v, %v", result.task, result.err)
		}
	case <-time.After(time.Second):
		t.Fatal("Cancel did not return")
	}
	f.awaitProcessTerminate(t, second, 1)
	second.complete(task.ProcessResult{ExitCode: 137})
	finished := f.awaitTask(t, started.ID, task.StatusFinished)
	if finished.Outcome != task.OutcomeCancelled {
		t.Fatalf("outcome = %s, want %s", finished.Outcome, task.OutcomeCancelled)
	}
	assertStepStatuses(t, finished, task.StepSucceeded, task.StepSkipped)
	if got := second.startCalls(); got != 0 {
		t.Fatalf("second Start calls after cancellation = %d, want 0", got)
	}
}

func TestManagerCancellationIsRegisteredBeforeCreatedEventVisibility(t *testing.T) {
	f := newManagerFixture(t)
	createdPublished := make(chan task.Event, 1)
	releasePublish := make(chan struct{})
	var releasePublishOnce sync.Once
	t.Cleanup(func() { releasePublishOnce.Do(func() { close(releasePublish) }) })
	f.publisher.blockType = task.EventTaskCreated
	f.publisher.blockEntered = createdPublished
	f.publisher.block = releasePublish
	prepareEntered := make(chan struct{})
	releasePrepare := make(chan struct{})
	var releasePrepareOnce sync.Once
	t.Cleanup(func() { releasePrepareOnce.Do(func() { close(releasePrepare) }) })
	f.processes.prepareBlockAt = 1
	f.processes.prepareEntered = prepareEntered
	f.processes.prepareBlock = releasePrepare

	startResult := make(chan taskResponseResult, 1)
	go func() {
		got, startErr := f.manager.Start(context.Background(), twoStepStartRequest(testID(78), time.Minute, fixedBoundary{}))
		startResult <- taskResponseResult{task: got, err: startErr}
	}()
	var created task.Event
	select {
	case created = <-createdPublished:
	case <-time.After(time.Second):
		t.Fatal("task.created was not published")
	}

	cancelCtx, stopCancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer stopCancel()
	if _, err := f.manager.Cancel(cancelCtx, created.TaskID); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Cancel during task.created publication error = %v, want context deadline", err)
	}
	releasePublishOnce.Do(func() { close(releasePublish) })

	select {
	case result := <-startResult:
		if result.err != nil {
			t.Fatalf("Start after visible cancellation = %#v, %v", result.task, result.err)
		}
	case <-time.After(time.Second):
		t.Fatal("Start did not return after task.created publication released")
	}
	finished := f.awaitTask(t, created.TaskID, task.StatusFinished)
	if finished.Outcome != task.OutcomeCancelled {
		t.Fatalf("outcome = %s, want %s", finished.Outcome, task.OutcomeCancelled)
	}
	assertStepStatuses(t, finished, task.StepSkipped, task.StepSkipped)
	select {
	case <-prepareEntered:
		t.Fatal("first Prepare started after cancellation of an externally visible task")
	default:
	}
}

func TestManagerCancellationConvergesWhileInitialPrepareIsBlocked(t *testing.T) {
	tests := []struct {
		name        string
		prepareErr  error
		wantProcess bool
	}{
		{name: "prepared process", wantProcess: true},
		{name: "prepare error without process", prepareErr: errors.New("prepare failed")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newManagerFixture(t)
			prepareEntered := make(chan struct{})
			prepareCanceled := make(chan struct{})
			releasePrepare := make(chan struct{})
			var releaseOnce sync.Once
			t.Cleanup(func() { releaseOnce.Do(func() { close(releasePrepare) }) })
			f.processes.prepareErr = tt.prepareErr
			f.processes.prepareBlockAt = 1
			f.processes.prepareEntered = prepareEntered
			f.processes.prepareCanceled = prepareCanceled
			f.processes.prepareBlock = releasePrepare

			startResult := make(chan taskResponseResult, 1)
			go func() {
				got, startErr := f.manager.Start(context.Background(), twoStepStartRequest(testID(77), time.Minute, fixedBoundary{}))
				startResult <- taskResponseResult{task: got, err: startErr}
			}()
			awaitSignal(t, prepareEntered, "initial Prepare was not entered")
			createdEvents := f.publisher.ofType(task.EventTaskCreated)
			if len(createdEvents) != 1 {
				t.Fatalf("task.created events = %d, want 1", len(createdEvents))
			}
			taskID := createdEvents[0].TaskID

			cancelResult := make(chan taskResponseResult, 1)
			go func() {
				got, cancelErr := f.manager.Cancel(context.Background(), taskID)
				cancelResult <- taskResponseResult{task: got, err: cancelErr}
			}()
			awaitSignal(t, prepareCanceled, "initial Prepare context was not cancelled")
			releaseOnce.Do(func() { close(releasePrepare) })

			select {
			case result := <-startResult:
				if result.err != nil {
					t.Fatalf("Start after queued cancellation = %#v, %v", result.task, result.err)
				}
			case <-time.After(time.Second):
				t.Fatal("Start did not return after initial Prepare was released")
			}
			select {
			case result := <-cancelResult:
				if result.err != nil {
					t.Fatalf("Cancel while task was queued = %#v, %v", result.task, result.err)
				}
			case <-time.After(time.Second):
				t.Fatal("Cancel did not return after initial Prepare was released")
			}

			if tt.wantProcess {
				f.awaitProcessTerminate(t, f.process, 1)
				f.awaitProcessClose(t, f.process)
			} else {
				if got := f.process.terminateCalls(); got != 0 {
					t.Fatalf("Terminate calls without a prepared process = %d, want 0", got)
				}
				if got := f.process.closeCalls(); got != 0 {
					t.Fatalf("Close calls without a prepared process = %d, want 0", got)
				}
			}
			finished := f.awaitStoredTask(t, taskID, task.StatusFinished)
			if finished.Outcome != task.OutcomeCancelled {
				t.Fatalf("outcome = %s, want %s", finished.Outcome, task.OutcomeCancelled)
			}
			assertStepStatuses(t, finished, task.StepSkipped, task.StepSkipped)
			if got := f.process.startCalls(); got != 0 {
				t.Fatalf("Start calls after queued cancellation = %d, want 0", got)
			}

			shutdownCtx, stopShutdown := context.WithTimeout(context.Background(), time.Second)
			defer stopShutdown()
			if err := f.manager.Shutdown(shutdownCtx); err != nil {
				t.Fatalf("Shutdown after queued cancellation = %v", err)
			}
		})
	}
}

func TestManagerPersistsPreparedLeaseBeforeHandlingClaimedCause(t *testing.T) {
	tests := []struct {
		name  string
		want  task.Outcome
		claim func(*testing.T, *managerFixture, string) <-chan error
	}{
		{
			name: "cancel",
			want: task.OutcomeCancelled,
			claim: func(_ *testing.T, f *managerFixture, taskID string) <-chan error {
				done := make(chan error, 1)
				go func() {
					_, err := f.manager.Cancel(context.Background(), taskID)
					done <- err
				}()
				return done
			},
		},
		{
			name: "timeout",
			want: task.OutcomeTimedOut,
			claim: func(t *testing.T, f *managerFixture, _ string) <-chan error {
				f.clock.fire(t, time.Second)
				return nil
			},
		},
		{
			name: "shutdown",
			want: task.OutcomeInterrupted,
			claim: func(_ *testing.T, f *managerFixture, _ string) <-chan error {
				done := make(chan error, 1)
				go func() { done <- f.manager.Shutdown(context.Background()) }()
				return done
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newManagerFixture(t)
			prepareEntered := make(chan struct{})
			prepareCanceled := make(chan struct{})
			releasePrepare := make(chan struct{})
			var releaseOnce sync.Once
			t.Cleanup(func() { releaseOnce.Do(func() { close(releasePrepare) }) })
			f.processes.prepareBlockAt = 1
			f.processes.prepareEntered = prepareEntered
			f.processes.prepareCanceled = prepareCanceled
			f.processes.prepareBlock = releasePrepare

			startResult := make(chan taskResponseResult, 1)
			go func() {
				got, err := f.manager.Start(context.Background(), task.StartRequest{
					IdempotencyKey: testID(76),
					Scenario:       task.ScenarioSuccess,
					Timeout:        time.Second,
				})
				startResult <- taskResponseResult{task: got, err: err}
			}()
			awaitSignal(t, prepareEntered, "initial Prepare was not entered")
			createdEvents := f.publisher.ofType(task.EventTaskCreated)
			if len(createdEvents) != 1 {
				t.Fatalf("task.created events = %d, want 1", len(createdEvents))
			}
			taskID := createdEvents[0].TaskID
			claimDone := tt.claim(t, f, taskID)
			awaitSignal(t, prepareCanceled, "claimed cause did not cancel blocked Prepare")
			releaseOnce.Do(func() { close(releasePrepare) })

			select {
			case result := <-startResult:
				if result.err != nil {
					t.Fatalf("Start after claimed cause = %#v, %v", result.task, result.err)
				}
			case <-time.After(time.Second):
				t.Fatal("Start did not return after initial Prepare was released")
			}
			if claimDone != nil {
				select {
				case err := <-claimDone:
					if err != nil {
						t.Fatalf("%s claim = %v", tt.name, err)
					}
				case <-time.After(time.Second):
					t.Fatalf("%s claim did not return", tt.name)
				}
			}

			first := f.store.firstMutation()
			if first.Task.Status != task.StatusQueued ||
				first.Expected != task.StatusQueued ||
				len(first.Steps) != 0 ||
				len(first.Events) != 0 ||
				first.PutLease == nil ||
				first.PutLease.TaskID != taskID {
				t.Fatalf("first mutation after %s claim = %#v, want queued pre-lease", tt.name, first)
			}
			if got := f.process.startCalls(); got != 0 {
				t.Fatalf("Start calls after %s claim = %d, want 0", tt.name, got)
			}
			f.awaitProcessTerminate(t, f.process, 1)
			f.awaitProcessClose(t, f.process)
			finished := f.awaitStoredTask(t, taskID, task.StatusFinished)
			if finished.Outcome != tt.want {
				t.Fatalf("outcome after %s claim = %s, want %s", tt.name, finished.Outcome, tt.want)
			}
			if lease := f.store.lease(taskID); lease.TaskID != "" {
				t.Fatalf("%s cleanup retained lease %#v", tt.name, lease)
			}
		})
	}
}

func TestManagerConcurrentCancellationDeliversOneQueuedCommand(t *testing.T) {
	f := newManagerFixtureWithCommandQueue(t, 1)
	prepareEntered := make(chan struct{})
	prepareCanceled := make(chan struct{})
	releasePrepare := make(chan struct{})
	var releaseOnce sync.Once
	t.Cleanup(func() { releaseOnce.Do(func() { close(releasePrepare) }) })
	f.processes.prepareBlockAt = 1
	f.processes.prepareEntered = prepareEntered
	f.processes.prepareCanceled = prepareCanceled
	f.processes.prepareBlock = releasePrepare

	startResult := make(chan taskResponseResult, 1)
	go func() {
		got, startErr := f.manager.Start(context.Background(), twoStepStartRequest(testID(76), time.Minute, fixedBoundary{}))
		startResult <- taskResponseResult{task: got, err: startErr}
	}()
	awaitSignal(t, prepareEntered, "initial Prepare was not entered")
	createdEvents := f.publisher.ofType(task.EventTaskCreated)
	if len(createdEvents) != 1 {
		t.Fatalf("task.created events = %d, want 1", len(createdEvents))
	}
	taskID := createdEvents[0].TaskID

	queueContext := newObservedCancelContext()
	listResult := make(chan error, 1)
	go func() {
		_, listErr := f.manager.List(queueContext, "", 1)
		listResult <- listErr
	}()
	awaitSignal(t, queueContext.waiting, "queue-filling List did not enqueue")
	queueContext.cancel()
	select {
	case err := <-listResult:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("queue-filling List error = %v, want context canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("queue-filling List did not return")
	}

	const callers = 64
	cancelContexts := make([]*observedCancelContext, callers)
	cancelResults := make(chan error, callers)
	for index := range cancelContexts {
		cancelContexts[index] = newObservedCancelContext()
		ctx := cancelContexts[index]
		go func() {
			_, cancelErr := f.manager.Cancel(ctx, taskID)
			cancelResults <- cancelErr
		}()
	}
	for _, ctx := range cancelContexts {
		awaitSignal(t, ctx.waiting, "concurrent Cancel was not accepted before caller cancellation")
	}
	awaitSignal(t, prepareCanceled, "accepted concurrent Cancel did not cancel initial Prepare")
	for _, ctx := range cancelContexts {
		ctx.cancel()
	}
	for range callers {
		select {
		case err := <-cancelResults:
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("concurrent Cancel error = %v, want context canceled", err)
			}
		case <-time.After(time.Second):
			t.Fatal("concurrent Cancel caller did not return")
		}
	}

	releaseOnce.Do(func() { close(releasePrepare) })
	select {
	case result := <-startResult:
		if result.err != nil {
			t.Fatalf("Start after concurrent queued cancellation = %#v, %v", result.task, result.err)
		}
	case <-time.After(time.Second):
		t.Fatal("Start did not return after initial Prepare was released")
	}

	deadline := time.After(time.Second)
	var finished task.Task
	for {
		if got := f.store.getCount(taskID); got > 1 {
			t.Fatalf("Store.Get calls from concurrent Cancel delivery = %d, want at most 1", got)
		}
		var ok bool
		finished, ok = f.store.peekTask(taskID)
		if ok && finished.Status == task.StatusFinished {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("queued task did not finish; got %#v", finished)
		default:
		}
	}
	if got := f.store.getCount(taskID); got != 1 {
		t.Fatalf("Store.Get calls from concurrent Cancel delivery = %d, want 1", got)
	}
	if got := f.store.mutationCountFor(taskID); got != 2 {
		t.Fatalf("successful task mutations from concurrent Cancel = %d, want 2 including pre-lease", got)
	}
	assertPreparedLeaseMutationCount(t, f.store, taskID, 1)
	if finished.Outcome != task.OutcomeCancelled {
		t.Fatalf("outcome = %s, want %s", finished.Outcome, task.OutcomeCancelled)
	}
	assertStepStatuses(t, finished, task.StepSkipped, task.StepSkipped)
	f.awaitProcessTerminate(t, f.process, 1)
	f.awaitProcessClose(t, f.process)
	if got := f.process.startCalls(); got != 0 {
		t.Fatalf("Start calls after concurrent queued cancellation = %d, want 0", got)
	}

	shutdownCtx, stopShutdown := context.WithTimeout(context.Background(), time.Second)
	defer stopShutdown()
	if err := f.manager.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("Shutdown after concurrent queued cancellation = %v", err)
	}
}

func TestManagerCancelPreemptsBlockedPrepareWhenCommandQueueIsFull(t *testing.T) {
	f := newManagerFixtureWithCommandQueue(t, 1)
	second := newFakeProcess()
	f.processes.queue = []*fakeProcess{f.process, second}
	t.Cleanup(func() { second.completeOnce(task.ProcessResult{Err: errors.New("test cleanup")}) })
	prepareEntered := make(chan struct{})
	prepareCanceled := make(chan struct{})
	releasePrepare := make(chan struct{})
	var releaseOnce sync.Once
	t.Cleanup(func() { releaseOnce.Do(func() { close(releasePrepare) }) })
	f.processes.prepareBlockAt = 2
	f.processes.prepareEntered = prepareEntered
	f.processes.prepareCanceled = prepareCanceled
	f.processes.prepareBlock = releasePrepare

	started, err := f.manager.Start(context.Background(), twoStepStartRequest(testID(80), time.Minute, fixedBoundary{}))
	if err != nil {
		t.Fatal(err)
	}
	f.process.complete(task.ProcessResult{ExitCode: 0})
	awaitSignal(t, prepareEntered, "second Prepare was not entered")

	probeCtx, stopProbe := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer stopProbe()
	if _, err := f.manager.Get(probeCtx, started.ID); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("queue-filling Get error = %v, want context deadline", err)
	}

	cancelCtx, stopCancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer stopCancel()
	if _, err := f.manager.Cancel(cancelCtx, started.ID); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Cancel error = %v, want context deadline after cancellation acceptance", err)
	}
	awaitSignal(t, prepareCanceled, "accepted Cancel did not preempt Prepare while command queue was full")
	releaseOnce.Do(func() { close(releasePrepare) })

	f.awaitProcessTerminate(t, second, 1)
	f.awaitProcessClose(t, second)
	finished := f.awaitTask(t, started.ID, task.StatusFinished)
	if finished.Outcome != task.OutcomeCancelled {
		t.Fatalf("outcome = %s, want %s", finished.Outcome, task.OutcomeCancelled)
	}
	assertStepStatuses(t, finished, task.StepSucceeded, task.StepSkipped)
	if got := second.startCalls(); got != 0 {
		t.Fatalf("second Start calls after accepted Cancel = %d, want 0", got)
	}
}

func TestManagerCancelWithAlreadyDoneContextIsNotAccepted(t *testing.T) {
	f := newManagerFixture(t)
	startCanceled := make(chan struct{})
	f.process.startCanceled = startCanceled

	started, err := f.manager.Start(context.Background(), task.StartRequest{
		IdempotencyKey: testID(79), Scenario: task.ScenarioHang, Timeout: time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	cancelCtx, stopCancel := context.WithCancel(context.Background())
	stopCancel()
	if _, err := f.manager.Cancel(cancelCtx, started.ID); !errors.Is(err, context.Canceled) {
		t.Fatalf("Cancel error = %v, want context canceled", err)
	}
	select {
	case <-startCanceled:
		t.Fatal("already-done caller context was accepted as a cancellation request")
	case <-time.After(50 * time.Millisecond):
	}
	if got := f.process.terminateCalls(); got != 0 {
		t.Fatalf("Terminate calls after rejected cancellation = %d, want 0", got)
	}
}

func TestManagerTotalTimeoutWinsWhenStepStartReturnsErrorAfterDeadline(t *testing.T) {
	f := newManagerFixture(t)
	second := newFakeProcess()
	f.processes.queue = []*fakeProcess{f.process, second}
	t.Cleanup(func() { second.completeOnce(task.ProcessResult{Err: errors.New("test cleanup")}) })
	startEntered := make(chan struct{})
	startCanceled := make(chan struct{})
	releaseStart := make(chan struct{})
	second.startEntered = startEntered
	second.startCanceled = startCanceled
	second.startBlock = releaseStart
	second.startErr = errors.New("start returned after deadline")
	const timeout = 13 * time.Second

	started, err := f.manager.Start(context.Background(), twoStepStartRequest(testID(84), timeout, fixedBoundary{}))
	if err != nil {
		t.Fatal(err)
	}
	f.process.complete(task.ProcessResult{ExitCode: 0})
	select {
	case <-startEntered:
	case <-time.After(time.Second):
		t.Fatal("second Start was not entered")
	}
	f.clock.fire(t, timeout)
	awaitSignal(t, startCanceled, "second Start context was not cancelled by total timeout")
	close(releaseStart)

	finished := f.awaitTask(t, started.ID, task.StatusFinished)
	if finished.Outcome != task.OutcomeTimedOut {
		t.Fatalf("outcome = %s, want %s", finished.Outcome, task.OutcomeTimedOut)
	}
	assertStepStatuses(t, finished, task.StepSucceeded, task.StepFailed)
}

func TestManagerTotalTimeoutWinsWhenStepStartSucceedsAfterDeadline(t *testing.T) {
	f := newManagerFixture(t)
	second := newFakeProcess()
	f.processes.queue = []*fakeProcess{f.process, second}
	t.Cleanup(func() { second.completeOnce(task.ProcessResult{Err: errors.New("test cleanup")}) })
	startEntered := make(chan struct{})
	startCanceled := make(chan struct{})
	releaseStart := make(chan struct{})
	second.startEntered = startEntered
	second.startCanceled = startCanceled
	second.startBlock = releaseStart
	const timeout = 15 * time.Second

	started, err := f.manager.Start(context.Background(), twoStepStartRequest(testID(81), timeout, fixedBoundary{}))
	if err != nil {
		t.Fatal(err)
	}
	f.process.complete(task.ProcessResult{ExitCode: 0})
	select {
	case <-startEntered:
	case <-time.After(time.Second):
		t.Fatal("second Start was not entered")
	}
	f.clock.fire(t, timeout)
	awaitSignal(t, startCanceled, "second Start context was not cancelled by total timeout")
	close(releaseStart)

	f.awaitProcessTerminate(t, second, 1)
	second.complete(task.ProcessResult{ExitCode: 137})
	finished := f.awaitTask(t, started.ID, task.StatusFinished)
	if finished.Outcome != task.OutcomeTimedOut {
		t.Fatalf("outcome = %s, want %s", finished.Outcome, task.OutcomeTimedOut)
	}
	assertStepStatuses(t, finished, task.StepSucceeded, task.StepFailed)
	if got := second.startCalls(); got != 1 {
		t.Fatalf("Start calls that crossed the deadline = %d, want 1", got)
	}
}

type taskResponseResult struct {
	task task.Task
	err  error
}

type observedCancelContext struct {
	context.Context
	done        chan struct{}
	waiting     chan struct{}
	waitingOnce sync.Once
	cancelOnce  sync.Once
}

func newObservedCancelContext() *observedCancelContext {
	return &observedCancelContext{
		Context: context.Background(),
		done:    make(chan struct{}),
		waiting: make(chan struct{}),
	}
}

func (c *observedCancelContext) Done() <-chan struct{} {
	c.waitingOnce.Do(func() { close(c.waiting) })
	return c.done
}

func (c *observedCancelContext) Err() error {
	select {
	case <-c.done:
		return context.Canceled
	default:
		return nil
	}
}

func (c *observedCancelContext) cancel() {
	c.cancelOnce.Do(func() { close(c.done) })
}

func awaitSignal(t *testing.T, signal <-chan struct{}, message string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(time.Second):
		t.Fatal(message)
	}
}

func TestManagerRejectsInvalidPlanBeforeCreation(t *testing.T) {
	f := newManagerFixture(t)
	request := twoStepStartRequest(testID(93), time.Minute, fixedBoundary{})
	request.Plan.Fingerprint = "caller-controlled"

	if _, err := f.manager.Start(context.Background(), request); !errors.Is(err, task.ErrInvalidArgument) {
		t.Fatalf("Start error = %v, want ErrInvalidArgument", err)
	}
	if got := f.processes.prepareCount(); got != 0 {
		t.Fatalf("Prepare calls = %d, want 0", got)
	}
	if page, err := f.store.List(context.Background(), "", 10); err != nil || len(page.Items) != 0 {
		t.Fatalf("stored tasks = %#v, %v", page.Items, err)
	}
}

func TestManagerRevalidatesBoundaryBeforePreparingNextStep(t *testing.T) {
	f := newManagerFixture(t)
	second := newFakeProcess()
	f.processes.queue = []*fakeProcess{f.process, second}
	t.Cleanup(func() { second.completeOnce(task.ProcessResult{Err: errors.New("test cleanup")}) })
	boundary := &switchableBoundary{}

	releaseClose := make(chan struct{})
	f.process.closeBlock = releaseClose
	released := false
	t.Cleanup(func() {
		if !released {
			close(releaseClose)
		}
	})

	started, err := f.manager.Start(context.Background(), twoStepStartRequest(testID(94), time.Minute, boundary))
	if err != nil {
		t.Fatal(err)
	}
	f.process.complete(task.ProcessResult{ExitCode: 0})
	f.awaitProcessClose(t, f.process)
	boundary.reject()
	close(releaseClose)
	released = true

	finished := f.awaitTask(t, started.ID, task.StatusFinished)
	if finished.Outcome != task.OutcomeInfrastructureFailed {
		t.Fatalf("outcome = %s, want %s", finished.Outcome, task.OutcomeInfrastructureFailed)
	}
	assertStepStatuses(t, finished, task.StepSucceeded, task.StepFailed)
	if got := f.processes.prepareCount(); got != 1 {
		t.Fatalf("Prepare calls after boundary changed = %d, want 1", got)
	}
}

func TestManagerCloseFailurePreservesCancellationCause(t *testing.T) {
	f := newManagerFixture(t)
	second := newFakeProcess()
	f.processes.queue = []*fakeProcess{f.process, second}
	t.Cleanup(func() { second.completeOnce(task.ProcessResult{Err: errors.New("test cleanup")}) })
	releaseClose := make(chan struct{})
	f.process.closeBlock = releaseClose
	f.process.closeErr = errors.New("first process close failed")

	started, err := f.manager.Start(context.Background(), twoStepStartRequest(testID(98), time.Minute, fixedBoundary{}))
	if err != nil {
		t.Fatal(err)
	}
	f.process.complete(task.ProcessResult{ExitCode: 0})
	f.awaitProcessClose(t, f.process)
	cancelling, err := f.manager.Cancel(context.Background(), started.ID)
	if err != nil || cancelling.Status != task.StatusCancelling {
		t.Fatalf("Cancel = %#v, %v", cancelling, err)
	}
	close(releaseClose)

	f.awaitUnhealthy(t)
	f.process.mu.Lock()
	f.process.closeErr = nil
	f.process.mu.Unlock()
	retry, cancelRetry := context.WithTimeout(context.Background(), time.Second)
	defer cancelRetry()
	if err := f.manager.Shutdown(retry); err != nil {
		t.Fatalf("Shutdown retry after lease-free Close failure = %v", err)
	}
	finished := f.awaitStoredTask(t, started.ID, task.StatusFinished)
	if finished.Outcome != task.OutcomeCancelled {
		t.Fatalf("outcome = %s, want %s", finished.Outcome, task.OutcomeCancelled)
	}
	assertStepStatuses(t, finished, task.StepSucceeded, task.StepSkipped)
	if got := f.processes.prepareCount(); got != 1 {
		t.Fatalf("Prepare calls = %d, want 1", got)
	}
	if got := f.process.closeCalls(); got != 2 {
		t.Fatalf("Close calls = %d, want retained-owner retry", got)
	}
}

func TestManagerCloseFailurePreservesTotalTimeoutCause(t *testing.T) {
	f := newManagerFixture(t)
	second := newFakeProcess()
	f.processes.queue = []*fakeProcess{f.process, second}
	t.Cleanup(func() { second.completeOnce(task.ProcessResult{Err: errors.New("test cleanup")}) })
	releaseClose := make(chan struct{})
	f.process.closeBlock = releaseClose
	f.process.closeErr = errors.New("first process close failed")
	startCanceled := make(chan struct{})
	f.process.startCanceled = startCanceled
	const timeout = 9 * time.Second

	started, err := f.manager.Start(context.Background(), twoStepStartRequest(testID(99), timeout, fixedBoundary{}))
	if err != nil {
		t.Fatal(err)
	}
	f.process.complete(task.ProcessResult{ExitCode: 0})
	f.awaitProcessClose(t, f.process)
	f.clock.fire(t, timeout)
	awaitSignal(t, startCanceled, "total timeout was not accepted before Close returned")
	close(releaseClose)

	f.awaitUnhealthy(t)
	f.process.mu.Lock()
	f.process.closeErr = nil
	f.process.mu.Unlock()
	retry, cancelRetry := context.WithTimeout(context.Background(), time.Second)
	defer cancelRetry()
	if err := f.manager.Shutdown(retry); err != nil {
		t.Fatalf("Shutdown retry after lease-free Close failure = %v", err)
	}
	finished := f.awaitStoredTask(t, started.ID, task.StatusFinished)
	if finished.Outcome != task.OutcomeTimedOut {
		t.Fatalf("outcome = %s, want %s", finished.Outcome, task.OutcomeTimedOut)
	}
	assertStepStatuses(t, finished, task.StepSucceeded, task.StepSkipped)
	if got := f.processes.prepareCount(); got != 1 {
		t.Fatalf("Prepare calls = %d, want 1", got)
	}
	if got := f.process.closeCalls(); got != 2 {
		t.Fatalf("Close calls = %d, want retained-owner retry", got)
	}
}

func TestManagerStoreConflictFinishesWithoutStartingLaterSteps(t *testing.T) {
	tests := []struct {
		name              string
		failApplyAt       int
		wantStatuses      []task.StepStatus
		wantPreparedLease int
	}{
		{
			name:              "persist completed step",
			failApplyAt:       3,
			wantStatuses:      []task.StepStatus{task.StepFailed, task.StepSkipped},
			wantPreparedLease: 1,
		},
		{
			name:              "persist next step start",
			failApplyAt:       5,
			wantStatuses:      []task.StepStatus{task.StepSucceeded, task.StepFailed},
			wantPreparedLease: 2,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newManagerFixture(t)
			second := newFakeProcess()
			f.processes.queue = []*fakeProcess{f.process, second}
			t.Cleanup(func() { second.completeOnce(task.ProcessResult{Err: errors.New("test cleanup")}) })
			f.store.failApplyAt = tt.failApplyAt
			f.store.failApplyErr = task.ErrConflict

			started, err := f.manager.Start(context.Background(), twoStepStartRequest(testID(96), time.Minute, fixedBoundary{}))
			if err != nil {
				t.Fatal(err)
			}
			f.process.complete(task.ProcessResult{ExitCode: 0})
			finished := f.awaitTask(t, started.ID, task.StatusFinished)

			if finished.Outcome != task.OutcomeInfrastructureFailed {
				t.Fatalf("outcome = %s, want %s", finished.Outcome, task.OutcomeInfrastructureFailed)
			}
			assertStepStatuses(t, finished, tt.wantStatuses...)
			assertPreparedLeaseMutationCount(t, f.store, started.ID, tt.wantPreparedLease)
			if got := second.startCalls(); got != 0 {
				t.Fatalf("second Start calls = %d, want 0", got)
			}
		})
	}
}

func TestManagerTerminalStoreConflictIsTaskLocal(t *testing.T) {
	f := newManagerFixture(t)
	other := newFakeProcess()
	f.processes.queue = []*fakeProcess{f.process, other}
	t.Cleanup(func() { other.completeOnce(task.ProcessResult{Err: errors.New("test cleanup")}) })

	conflicted, err := f.manager.Start(context.Background(), task.StartRequest{
		IdempotencyKey: testID(85), Scenario: task.ScenarioSuccess, Timeout: time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	unaffected, err := f.manager.Start(context.Background(), task.StartRequest{
		IdempotencyKey: testID(86), Scenario: task.ScenarioSuccess, Timeout: time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	assertPreparedLeaseMutationCount(t, f.store, conflicted.ID, 1)
	assertPreparedLeaseMutationCount(t, f.store, unaffected.ID, 1)
	f.store.failApplyAt = f.store.applyCount() + 1
	f.store.failApplyErr = task.ErrConflict

	f.process.complete(task.ProcessResult{ExitCode: 0})
	f.awaitProcessClose(t, f.process)
	if !f.manager.Healthy() {
		t.Fatal("manager became unhealthy after a task-local terminal conflict")
	}
	if got := other.terminateCalls(); got != 0 {
		t.Fatalf("unaffected task Terminate calls = %d, want 0", got)
	}
	finishedConflict := f.awaitTask(t, conflicted.ID, task.StatusFinished)
	if finishedConflict.Outcome != task.OutcomeInfrastructureFailed {
		t.Fatalf("conflicted outcome = %s, want %s", finishedConflict.Outcome, task.OutcomeInfrastructureFailed)
	}

	other.complete(task.ProcessResult{ExitCode: 0})
	finishedOther := f.awaitTask(t, unaffected.ID, task.StatusFinished)
	if finishedOther.Outcome != task.OutcomeSucceeded {
		t.Fatalf("unaffected outcome = %s, want %s", finishedOther.Outcome, task.OutcomeSucceeded)
	}
}

func TestManagerRepeatedTerminalStoreConflictStopsAfterInfrastructureRetry(t *testing.T) {
	f := newManagerFixture(t)
	next := newFakeProcess()
	f.processes.queue = []*fakeProcess{f.process, next}
	t.Cleanup(func() { next.completeOnce(task.ProcessResult{Err: errors.New("test cleanup")}) })

	conflicted, err := f.manager.Start(context.Background(), task.StartRequest{
		IdempotencyKey: testID(87), Scenario: task.ScenarioSuccess, Timeout: time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	assertPreparedLeaseMutationCount(t, f.store, conflicted.ID, 1)
	f.store.failApplyAt = f.store.applyCount() + 1
	f.store.failApplyFor = 2
	f.store.failApplyErr = task.ErrConflict

	f.process.complete(task.ProcessResult{ExitCode: 0})
	f.awaitProcessClose(t, f.process)
	deadline := time.Now().Add(time.Second)
	for f.store.applyCount() < 4 && time.Now().Before(deadline) {
	}
	if got := f.store.applyCount(); got != 4 {
		t.Fatalf("Apply calls after repeated terminal conflict = %d, want 4 including pre-lease", got)
	}
	if _, err := f.manager.Get(context.Background(), conflicted.ID); err != nil {
		t.Fatalf("Get after repeated terminal conflict = %v", err)
	}
	if got := f.store.applyCount(); got != 4 {
		t.Fatalf("Apply calls kept retrying after repeated terminal conflict: %d", got)
	}
	if !f.manager.Healthy() {
		t.Fatal("manager became unhealthy after repeated task-local terminal conflicts")
	}
	stored, err := f.store.Get(context.Background(), conflicted.ID)
	if err != nil || stored.Status != task.StatusRunning {
		t.Fatalf("conflicted stored task = %#v, %v; want controlled recovery state", stored, err)
	}

	startedNext, err := f.manager.Start(context.Background(), task.StartRequest{
		IdempotencyKey: testID(88), Scenario: task.ScenarioSuccess, Timeout: time.Minute,
	})
	if err != nil {
		t.Fatalf("Start after repeated task-local conflict: %v", err)
	}
	next.complete(task.ProcessResult{ExitCode: 0})
	finishedNext := f.awaitTask(t, startedNext.ID, task.StatusFinished)
	if finishedNext.Outcome != task.OutcomeSucceeded {
		t.Fatalf("next outcome = %s, want %s", finishedNext.Outcome, task.OutcomeSucceeded)
	}
}

func TestManagerCancelConflictCleansPreemptedPreparedProcessLocally(t *testing.T) {
	f := newManagerFixture(t)
	targetFirst := newFakeProcess()
	targetSecond := newFakeProcess()
	f.processes.queue = []*fakeProcess{f.process, targetFirst, targetSecond}
	t.Cleanup(func() {
		targetFirst.completeOnce(task.ProcessResult{Err: errors.New("test cleanup")})
		targetSecond.completeOnce(task.ProcessResult{Err: errors.New("test cleanup")})
	})
	prepareEntered := make(chan struct{})
	prepareCanceled := make(chan struct{})
	releasePrepare := make(chan struct{})
	var releaseOnce sync.Once
	t.Cleanup(func() { releaseOnce.Do(func() { close(releasePrepare) }) })
	f.processes.prepareBlockAt = 3
	f.processes.prepareEntered = prepareEntered
	f.processes.prepareCanceled = prepareCanceled
	f.processes.prepareBlock = releasePrepare

	unaffected, err := f.manager.Start(context.Background(), task.StartRequest{
		IdempotencyKey: testID(76), Scenario: task.ScenarioHang, Timeout: time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	const targetTimeout = 17 * time.Second
	target, err := f.manager.Start(context.Background(), twoStepStartRequest(testID(77), targetTimeout, fixedBoundary{}))
	if err != nil {
		t.Fatal(err)
	}
	targetFirst.complete(task.ProcessResult{ExitCode: 0})
	awaitSignal(t, prepareEntered, "target second Prepare was not entered")
	f.store.failApplyAt = f.store.applyCount() + 2
	f.store.failApplyErr = task.ErrConflict

	cancelResult := make(chan taskResponseResult, 1)
	go func() {
		got, cancelErr := f.manager.Cancel(context.Background(), target.ID)
		cancelResult <- taskResponseResult{task: got, err: cancelErr}
	}()
	awaitSignal(t, prepareCanceled, "Cancel did not preempt target second Prepare")
	releaseOnce.Do(func() { close(releasePrepare) })

	select {
	case result := <-cancelResult:
		if !errors.Is(result.err, task.ErrConflict) {
			t.Fatalf("Cancel error = %v, want task-local ErrConflict", result.err)
		}
	case <-time.After(time.Second):
		t.Fatal("Cancel did not return after Prepare released")
	}
	f.awaitProcessTerminate(t, targetSecond, 1)
	f.awaitProcessClose(t, targetSecond)
	finishedTarget := f.awaitTask(t, target.ID, task.StatusFinished)
	assertPreparedLeaseMutationCount(t, f.store, target.ID, 2)
	if finishedTarget.Outcome != task.OutcomeInfrastructureFailed {
		t.Fatalf("target outcome = %s, want %s", finishedTarget.Outcome, task.OutcomeInfrastructureFailed)
	}
	assertStepStatuses(t, finishedTarget, task.StepSucceeded, task.StepSkipped)
	if got := targetSecond.startCalls(); got != 0 {
		t.Fatalf("target second Start calls = %d, want 0", got)
	}
	if !f.manager.Healthy() {
		t.Fatal("manager became unhealthy after task-local cancellation conflict")
	}
	if got := f.process.terminateCalls(); got != 0 {
		t.Fatalf("unaffected task Terminate calls = %d, want 0", got)
	}

	f.clock.fire(t, targetTimeout)
	afterTimeout := f.awaitTask(t, target.ID, task.StatusFinished)
	if afterTimeout.Outcome != task.OutcomeInfrastructureFailed {
		t.Fatalf("target outcome after total timeout = %s, want stable %s", afterTimeout.Outcome, task.OutcomeInfrastructureFailed)
	}
	f.process.complete(task.ProcessResult{ExitCode: 0})
	finishedOther := f.awaitTask(t, unaffected.ID, task.StatusFinished)
	if finishedOther.Outcome != task.OutcomeSucceeded {
		t.Fatalf("unaffected outcome = %s, want %s", finishedOther.Outcome, task.OutcomeSucceeded)
	}
}

func TestManagerStorageFailureNeverPreparesNextStep(t *testing.T) {
	f := newManagerFixture(t)
	second := newFakeProcess()
	f.processes.queue = []*fakeProcess{f.process, second}
	t.Cleanup(func() { second.completeOnce(task.ProcessResult{Err: errors.New("test cleanup")}) })
	f.store.failApplyAt = 3
	f.store.failApplyErr = task.ErrStorageUnavailable

	started, err := f.manager.Start(context.Background(), twoStepStartRequest(testID(97), time.Minute, fixedBoundary{}))
	if err != nil {
		t.Fatal(err)
	}
	f.process.complete(task.ProcessResult{ExitCode: 0})
	f.awaitProcessClose(t, f.process)
	f.awaitUnhealthy(t)
	assertPreparedLeaseMutationCount(t, f.store, started.ID, 1)

	deadline := time.Now().Add(200 * time.Millisecond)
	for time.Now().Before(deadline) {
		if f.processes.prepareCount() > 1 || second.startCalls() > 0 {
			t.Fatalf("later step reached process factory: Prepare=%d Start=%d", f.processes.prepareCount(), second.startCalls())
		}
	}
}

func assertPreparedLeaseMutationCount(
	t *testing.T,
	store *fakeStore,
	taskID string,
	want int,
) {
	t.Helper()
	count := 0
	for _, mutation := range store.mutationsFor(taskID) {
		if !isPreparedLeaseMutation(mutation) {
			continue
		}
		count++
		if mutation.Task.Status != mutation.Expected ||
			len(mutation.Steps) != 0 ||
			len(mutation.Events) != 0 ||
			len(mutation.Artifacts) != 0 ||
			mutation.DeleteLease {
			t.Fatalf("prepared lease mutation = %#v", mutation)
		}
	}
	if count != want {
		t.Fatalf("prepared lease mutations for %s = %d, want %d", taskID, count, want)
	}
}

func TestNewSimulationStartRequestBuildsServiceOwnedPlan(t *testing.T) {
	executable, err := filepath.Abs(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	simulationDirectory := t.TempDir()

	request, err := task.NewSimulationStartRequest(
		testID(95), task.ScenarioHang, 2500*time.Millisecond, executable, simulationDirectory,
	)
	if err != nil {
		t.Fatal(err)
	}
	if request.Kind != task.KindSimulation || request.Scenario != task.ScenarioHang || request.Timeout != 2500*time.Millisecond {
		t.Fatalf("request = %#v", request)
	}
	if len(request.Plan.Steps) != 1 {
		t.Fatalf("plan steps = %#v", request.Plan.Steps)
	}
	step := request.Plan.Steps[0]
	if step.ID != "simulate" || step.Kind != task.StepSimulation ||
		step.Process.Executable != executable || step.Process.Dir != simulationDirectory {
		t.Fatalf("simulation step = %#v", step)
	}
	if got := request.Plan.Fingerprint; got == "" || got != task.FingerprintPlan(request.Plan) {
		t.Fatalf("plan fingerprint = %q", got)
	}
	if err := task.ValidatePlan(request.Plan, request.Boundary); err != nil {
		t.Fatalf("generated plan validation = %v", err)
	}
	var persisted struct {
		Scenario  task.Scenario `json:"scenario"`
		TimeoutMS int64         `json:"timeoutMs"`
	}
	if err := json.Unmarshal(request.Request, &persisted); err != nil ||
		persisted.Scenario != task.ScenarioHang || persisted.TimeoutMS != 2500 {
		t.Fatalf("persisted request = %#v, %v", persisted, err)
	}
}

func twoStepStartRequest(idempotencyKey string, timeout time.Duration, boundary task.ExecutionBoundary) task.StartRequest {
	plan := task.ExecutionPlan{
		Version: 1,
		Steps: []task.ExecutionStep{
			{
				ID: "first", Kind: task.StepSimulation,
				Process: task.ProcessSpec{
					Executable: "trusted-service",
					Args:       []string{"--task-fixture", "success"},
					Dir:        "simulation-dir",
				},
			},
			{
				ID: "second", Kind: task.StepSimulation,
				Process: task.ProcessSpec{
					Executable: "trusted-service",
					Args:       []string{"--task-fixture", "success"},
					Dir:        "simulation-dir",
				},
			},
		},
	}
	plan.Fingerprint = task.FingerprintPlan(plan)
	request, _ := json.Marshal(map[string]any{"scenario": task.ScenarioSuccess, "timeoutMs": timeout.Milliseconds()})
	return task.StartRequest{
		IdempotencyKey: idempotencyKey,
		Kind:           task.KindSimulation,
		Request:        request,
		Scenario:       task.ScenarioSuccess,
		Timeout:        timeout,
		Plan:           plan,
		Boundary:       boundary,
	}
}

type fixedBoundary struct{}

func (fixedBoundary) ValidateExecutable(path string) error {
	if path != "trusted-service" {
		return fmt.Errorf("unexpected executable %q", path)
	}
	return nil
}

func (fixedBoundary) ValidateWorkingDirectory(path string) error {
	if path != "simulation-dir" {
		return fmt.Errorf("unexpected working directory %q", path)
	}
	return nil
}

type switchableBoundary struct {
	mu       sync.Mutex
	rejected bool
}

func (b *switchableBoundary) reject() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.rejected = true
}

func (b *switchableBoundary) ValidateExecutable(string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.rejected {
		return errors.New("executable identity changed")
	}
	return nil
}

func (b *switchableBoundary) ValidateWorkingDirectory(string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.rejected {
		return errors.New("working directory identity changed")
	}
	return nil
}

func awaitProcessStart(t *testing.T, process *fakeProcess) {
	t.Helper()
	deadline := time.After(time.Second)
	for process.startCalls() == 0 {
		select {
		case <-deadline:
			t.Fatal("process was not started")
		default:
		}
	}
}

func assertStepStatuses(t *testing.T, got task.Task, want ...task.StepStatus) {
	t.Helper()
	if len(got.Steps) != len(want) {
		t.Fatalf("steps = %#v, want %d steps", got.Steps, len(want))
	}
	for index := range want {
		if got.Steps[index].Status != want[index] {
			t.Fatalf("step[%d] status = %s, want %s; steps = %#v", index, got.Steps[index].Status, want[index], got.Steps)
		}
	}
}
