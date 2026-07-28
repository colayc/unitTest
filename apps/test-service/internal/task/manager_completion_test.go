package task_test

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"unit-test-ide.local/test-service/internal/task"
)

func TestManagerStartErrorCloseFailureDefersTerminalVisibility(t *testing.T) {
	f := newManagerFixture(t)
	f.process.startErr = errors.New("start allocated resources before failing")
	f.process.closeErr = errors.New("close failed")
	t.Cleanup(func() {
		f.process.mu.Lock()
		f.process.closeErr = nil
		f.process.mu.Unlock()
	})

	started, err := f.manager.Start(context.Background(), task.StartRequest{
		IdempotencyKey: testID(140),
		Scenario:       task.ScenarioSuccess,
		Timeout:        time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	f.awaitTerminate(t, 1)
	f.awaitProcessClose(t, f.process)
	f.awaitUnhealthy(t)

	stored, err := f.store.Get(context.Background(), started.ID)
	if err != nil ||
		stored.Status != task.StatusRunning ||
		stored.Outcome != "" ||
		stored.Steps[0].Status != task.StepRunning {
		t.Fatalf("durable task before Close retry = %#v, %v", stored, err)
	}
	if lease := f.store.lease(started.ID); lease.TaskID != started.ID {
		t.Fatalf("durable lease = %#v", lease)
	}
	if got := eventTypes(f.store.eventsForTask(started.ID)); !reflect.DeepEqual(got, []task.EventType{
		task.EventTaskCreated,
		task.EventTaskStarted,
		task.EventTaskStepStarted,
	}) {
		t.Fatalf("events before Close retry = %v", got)
	}
	if artifacts := f.store.artifactsCopy(); len(artifacts) != 0 {
		t.Fatalf("terminal artifacts before Close retry = %#v", artifacts)
	}
}

func TestManagerFinalStepCloseFailureDefersTerminalVisibility(t *testing.T) {
	f := newManagerFixture(t)
	f.process.closeErr = errors.New("close failed after exit zero")
	t.Cleanup(func() {
		f.process.mu.Lock()
		f.process.closeErr = nil
		f.process.mu.Unlock()
	})

	started, err := f.manager.Start(context.Background(), task.StartRequest{
		IdempotencyKey: testID(141),
		Scenario:       task.ScenarioSuccess,
		Timeout:        time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	f.process.complete(task.ProcessResult{ExitCode: 0})
	f.awaitProcessClose(t, f.process)
	f.awaitUnhealthy(t)

	stored, err := f.store.Get(context.Background(), started.ID)
	if err != nil ||
		stored.Status != task.StatusRunning ||
		stored.Outcome != "" ||
		stored.Steps[0].Status != task.StepRunning {
		t.Fatalf("durable task before Close retry = %#v, %v", stored, err)
	}
	if lease := f.store.lease(started.ID); lease.TaskID != started.ID {
		t.Fatalf("durable lease = %#v", lease)
	}
	if len(f.store.artifactsCopy()) != 0 {
		t.Fatal("Close failure published a terminal artifact")
	}
}

func TestManagerIntermediateStepCloseFailureKeepsRunningStepAndLease(t *testing.T) {
	f := newManagerFixture(t)
	first := f.process
	second := newFakeProcess()
	f.processes.queue = []*fakeProcess{first, second}
	t.Cleanup(func() {
		second.completeOnce(task.ProcessResult{Err: errors.New("test cleanup")})
	})
	first.closeErr = errors.New("close failed after intermediate success")
	t.Cleanup(func() {
		first.mu.Lock()
		first.closeErr = nil
		first.mu.Unlock()
	})

	started, err := f.manager.Start(
		context.Background(),
		twoStepStartRequest(testID(142), time.Minute, fixedBoundary{}),
	)
	if err != nil {
		t.Fatal(err)
	}
	first.complete(task.ProcessResult{ExitCode: 0})
	f.awaitProcessClose(t, first)
	f.awaitUnhealthy(t)

	stored, err := f.store.Get(context.Background(), started.ID)
	if err != nil ||
		stored.Status != task.StatusRunning ||
		stored.ActiveStep != "first" ||
		stored.Steps[0].Status != task.StepRunning ||
		stored.Steps[1].Status != task.StepPending {
		t.Fatalf("durable task before Close retry = %#v, %v", stored, err)
	}
	if lease := f.store.lease(started.ID); lease.TaskID != started.ID {
		t.Fatalf("durable lease = %#v", lease)
	}
	if got := f.processes.prepareCount(); got != 1 || second.startCalls() != 0 {
		t.Fatalf("next Step started before cleanup: Prepare=%d Start=%d", got, second.startCalls())
	}
}

func TestManagerCloseBeforeTerminalizationOutcomeMatrix(t *testing.T) {
	tests := []struct {
		name        string
		result      task.ProcessResult
		claim       task.Outcome
		wantOutcome task.Outcome
	}{
		{
			name:        "success cleanup failure",
			result:      task.ProcessResult{ExitCode: 0},
			wantOutcome: task.OutcomeInfrastructureFailed,
		},
		{
			name:        "command failure cleanup failure",
			result:      task.ProcessResult{ExitCode: 7},
			wantOutcome: task.OutcomeInfrastructureFailed,
		},
		{
			name:        "cancel first cause",
			result:      task.ProcessResult{Err: context.Canceled},
			claim:       task.OutcomeCancelled,
			wantOutcome: task.OutcomeCancelled,
		},
		{
			name:        "timeout first cause",
			result:      task.ProcessResult{Err: context.DeadlineExceeded},
			claim:       task.OutcomeTimedOut,
			wantOutcome: task.OutcomeTimedOut,
		},
	}
	for index, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newManagerFixture(t)
			var timeoutAccepted chan struct{}
			if tt.claim == task.OutcomeTimedOut {
				timeoutAccepted = make(chan struct{})
				f.process.startCanceled = timeoutAccepted
			}
			releaseClose := make(chan struct{})
			f.process.closeBlock = releaseClose
			f.process.closeErr = errors.New("first Close failed")
			started, err := f.manager.Start(context.Background(), task.StartRequest{
				IdempotencyKey: testID(byte(143 + index)),
				Scenario:       task.ScenarioSuccess,
				Timeout:        time.Minute,
			})
			if err != nil {
				t.Fatal(err)
			}
			f.process.complete(tt.result)
			f.awaitProcessClose(t, f.process)
			switch tt.claim {
			case task.OutcomeCancelled:
				if _, err := f.manager.Cancel(context.Background(), started.ID); err != nil {
					t.Fatal(err)
				}
			case task.OutcomeTimedOut:
				f.clock.fire(t, time.Minute)
				awaitSignal(t, timeoutAccepted, "timeout first-cause was not accepted before Close returned")
			}
			close(releaseClose)
			f.awaitUnhealthy(t)

			beforeRetry, err := f.store.Get(context.Background(), started.ID)
			if err != nil || beforeRetry.Status == task.StatusFinished {
				t.Fatalf("Task before retry = %#v, %v", beforeRetry, err)
			}
			if lease := f.store.lease(started.ID); lease.TaskID != started.ID {
				t.Fatalf("lease before retry = %#v", lease)
			}
			f.process.mu.Lock()
			f.process.closeErr = nil
			f.process.mu.Unlock()
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if err := f.manager.Shutdown(ctx); err != nil {
				t.Fatal(err)
			}
			finished := f.awaitStoredTask(t, started.ID, task.StatusFinished)
			if finished.Outcome != tt.wantOutcome {
				t.Fatalf("outcome = %s, want %s", finished.Outcome, tt.wantOutcome)
			}
			if mutation := f.store.lastMutation(); !mutation.DeleteLease {
				t.Fatalf("terminal mutation did not delete lease: %#v", mutation)
			}
		})
	}
}
