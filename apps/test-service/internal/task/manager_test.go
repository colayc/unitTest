package task_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"unit-test-ide.local/test-service/internal/task"
)

func TestNewManagerRejectsMissingDependencies(t *testing.T) {
	if _, err := task.NewManager(task.ManagerConfig{}); !errors.Is(err, task.ErrInvalidArgument) {
		t.Fatalf("NewManager error = %v", err)
	}
}

func TestSimulationTaskPersistsGeneratedPlanFingerprint(t *testing.T) {
	f := newManagerFixture(t)
	started, err := f.manager.Start(context.Background(), task.StartRequest{
		IdempotencyKey: testID(0), Scenario: task.ScenarioSuccess, Timeout: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if started.PlanFingerprint == "" {
		t.Fatal("simulation plan fingerprint is empty")
	}
}

func TestManagerStartsAndCancelsOneTask(t *testing.T) {
	f := newManagerFixture(t)
	started, err := f.manager.Start(context.Background(), task.StartRequest{
		IdempotencyKey: testID(1), Scenario: task.ScenarioSpawnChild, Timeout: 30 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if started.Status != task.StatusRunning {
		t.Fatalf("task = %#v", started)
	}
	if got := f.publisher.types(); !reflect.DeepEqual(got, []task.EventType{task.EventTaskCreated, task.EventTaskStarted}) {
		t.Fatalf("events = %v", got)
	}
	if got := f.processes.prepareCount(); got != 1 {
		t.Fatalf("Prepare calls = %d", got)
	}
	if spec := f.processes.lastSpec(); spec.Executable != "trusted-service" || !reflect.DeepEqual(spec.Args, []string{"--task-fixture", "spawn-child"}) || len(spec.Env) != 0 || spec.Dir != "simulation-dir" {
		t.Fatalf("trusted spec = %#v", spec)
	}

	cancelling, err := f.manager.Cancel(context.Background(), started.ID)
	if err != nil {
		t.Fatal(err)
	}
	if cancelling.Status != task.StatusCancelling {
		t.Fatalf("cancel = %#v", cancelling)
	}
	f.awaitTerminate(t, 1)
	f.process.complete(task.ProcessResult{ExitCode: 1})
	finished := f.awaitTask(t, started.ID, task.StatusFinished)
	if finished.Outcome != task.OutcomeCancelled {
		t.Fatalf("task = %#v", finished)
	}
	if _, err := f.manager.Cancel(context.Background(), started.ID); err != nil {
		t.Fatal(err)
	}
	if f.process.terminateCalls() != 1 {
		t.Fatalf("terminal cancel terminated %d times", f.process.terminateCalls())
	}
	if got := f.publisher.types(); !reflect.DeepEqual(got, []task.EventType{
		task.EventTaskCreated, task.EventTaskStarted, task.EventTaskCancellationRequested,
		task.EventArtifactCreated, task.EventTaskFinished,
	}) {
		t.Fatalf("events = %v", got)
	}
}

func TestManagerIdempotencyAndSerializedStarts(t *testing.T) {
	f := newManagerFixture(t)
	req := task.StartRequest{IdempotencyKey: testID(2), Scenario: task.ScenarioSuccess, Timeout: time.Second}
	first, err := f.manager.Start(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := f.manager.Start(context.Background(), req)
	if err != nil || replayed.ID != first.ID {
		t.Fatalf("replay = %#v, %v", replayed, err)
	}
	if f.processes.prepareCount() != 1 || len(f.publisher.events()) != 2 {
		t.Fatalf("replay performed side effects: prepare=%d events=%d", f.processes.prepareCount(), len(f.publisher.events()))
	}
	conflict := req
	conflict.Timeout = 2 * time.Second
	if _, err := f.manager.Start(context.Background(), conflict); !errors.Is(err, task.ErrIdempotencyConflict) {
		t.Fatalf("conflicting replay error = %v", err)
	}
}

func TestManagerPersistsPreparedLeaseBeforeStartAndRefreshesItAfterStart(t *testing.T) {
	f := newManagerFixture(t)
	started, err := f.manager.Start(context.Background(), task.StartRequest{
		IdempotencyKey: testID(26), Scenario: task.ScenarioSuccess, Timeout: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	mutation := f.store.firstMutation()
	if mutation.PutLease == nil || mutation.PutLease.TaskID != started.ID || mutation.PutLease.TargetProcessGroup != 0 {
		t.Fatalf("initial lease mutation = %#v", mutation)
	}
	lease := f.store.lease(started.ID)
	if lease.TargetProcessGroup != 42 || lease.HostPID != 41 || lease.ServiceInstanceID != testID(99) {
		t.Fatalf("refreshed lease = %#v", lease)
	}
}

func TestManagerProcessStartFailureIsPersistedAsInfrastructureFailure(t *testing.T) {
	f := newManagerFixture(t)
	f.process.startErr = errors.New("C:/private/program.exe failed with TOKEN")
	finished, err := f.manager.Start(context.Background(), task.StartRequest{
		IdempotencyKey: testID(27), Scenario: task.ScenarioSuccess, Timeout: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if finished.Status != task.StatusFinished || finished.Outcome != task.OutcomeInfrastructureFailed || strings.Contains(finished.ErrorMessage, "private") {
		t.Fatalf("finished = %#v", finished)
	}
	f.awaitTerminate(t, 1)
}

func TestManagerApplyFailureTripsCircuitWithoutStartingTarget(t *testing.T) {
	f := newManagerFixture(t)
	f.store.failApply = fmt.Errorf("dsn=private-token: %w", task.ErrStorageUnavailable)
	_, err := f.manager.Start(context.Background(), task.StartRequest{
		IdempotencyKey: testID(28), Scenario: task.ScenarioSuccess, Timeout: time.Second,
	})
	if !errors.Is(err, task.ErrStorageUnavailable) {
		t.Fatalf("Start error = %v", err)
	}
	if err.Error() != task.ErrStorageUnavailable.Error() {
		t.Fatalf("Start leaked storage details: %q", err)
	}
	if f.process.startCalls() != 0 || f.manager.Healthy() {
		t.Fatalf("starts = %d healthy = %v", f.process.startCalls(), f.manager.Healthy())
	}
	f.awaitTerminate(t, 1)
}

func TestManagerPrepareFailureFinishesQueuedTask(t *testing.T) {
	f := newManagerFixture(t)
	f.processes.prepareErr = errors.New("private executable path")
	finished, err := f.manager.Start(context.Background(), task.StartRequest{
		IdempotencyKey: testID(30), Scenario: task.ScenarioSuccess, Timeout: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if finished.Status != task.StatusFinished || finished.Outcome != task.OutcomeInfrastructureFailed || f.process.startCalls() != 0 {
		t.Fatalf("finished = %#v starts = %d", finished, f.process.startCalls())
	}
	if got := f.publisher.types(); !reflect.DeepEqual(got, []task.EventType{
		task.EventTaskCreated, task.EventArtifactCreated, task.EventTaskFinished,
	}) {
		t.Fatalf("events = %v", got)
	}
}

func TestManagerPrepareFailureArtifactFaultDoesNotCrashAndQuiescesOtherActiveTask(t *testing.T) {
	f := newManagerFixture(t)
	other, err := f.manager.Start(context.Background(), task.StartRequest{
		IdempotencyKey: testID(89), Scenario: task.ScenarioHang, Timeout: time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	f.processes.prepareErr = errors.New("prepare failed before process allocation")
	f.artifacts.fail = task.ErrStorageUnavailable

	if _, err := f.manager.Start(context.Background(), task.StartRequest{
		IdempotencyKey: testID(90), Scenario: task.ScenarioSuccess, Timeout: time.Hour,
	}); !errors.Is(err, task.ErrStorageUnavailable) {
		t.Fatalf("Start error = %v, want ErrStorageUnavailable", err)
	}
	f.awaitUnhealthy(t)
	f.awaitProcessTerminate(t, f.process, 1)
	f.process.complete(task.ProcessResult{Err: context.Canceled})
	f.awaitProcessClose(t, f.process)

	stored, err := f.store.Get(context.Background(), other.ID)
	if err != nil || stored.Status != task.StatusRunning || stored.Outcome != "" {
		t.Fatalf("other task after storage recovery = %#v, %v", stored, err)
	}
}

func TestManagerPrepareFailureTerminalStoreFaultDoesNotCrash(t *testing.T) {
	f := newManagerFixture(t)
	f.processes.prepareErr = errors.New("prepare failed before process allocation")
	f.store.failApply = task.ErrStorageUnavailable

	if _, err := f.manager.Start(context.Background(), task.StartRequest{
		IdempotencyKey: testID(91), Scenario: task.ScenarioSuccess, Timeout: time.Hour,
	}); !errors.Is(err, task.ErrStorageUnavailable) {
		t.Fatalf("Start error = %v, want ErrStorageUnavailable", err)
	}
	f.awaitUnhealthy(t)
	if got := f.process.terminateCalls(); got != 0 {
		t.Fatalf("Terminate calls without a prepared process = %d, want 0", got)
	}
}

func TestManagerPublisherPanicTripsCircuitAfterCommittedEvent(t *testing.T) {
	f := newManagerFixture(t)
	f.publisher.panicType = task.EventTaskCreated
	_, err := f.manager.Start(context.Background(), task.StartRequest{
		IdempotencyKey: testID(31), Scenario: task.ScenarioSuccess, Timeout: time.Second,
	})
	if !errors.Is(err, task.ErrStorageUnavailable) || f.manager.Healthy() || f.processes.prepareCount() != 0 {
		t.Fatalf("Start error = %v healthy = %v prepare = %d", err, f.manager.Healthy(), f.processes.prepareCount())
	}
}

func TestManagerBlockedTerminateDoesNotBlockTerminalPersistence(t *testing.T) {
	f := newManagerFixture(t)
	release := make(chan struct{})
	f.process.terminateBlock = release
	started, err := f.manager.Start(context.Background(), task.StartRequest{
		IdempotencyKey: testID(29), Scenario: task.ScenarioHang, Timeout: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.manager.Cancel(context.Background(), started.ID); err != nil {
		t.Fatal(err)
	}
	f.awaitTerminate(t, 1)
	f.process.complete(task.ProcessResult{ExitCode: 1})
	finished := f.awaitTask(t, started.ID, task.StatusFinished)
	if finished.Outcome != task.OutcomeCancelled {
		t.Fatalf("finished = %#v", finished)
	}
	if f.process.closeCalls() != 0 {
		t.Fatal("process closed before Terminate returned")
	}
	close(release)
	deadline := time.After(time.Second)
	for f.process.closeCalls() == 0 {
		select {
		case <-deadline:
			t.Fatal("process was not closed after Terminate returned")
		default:
		}
	}
}

func TestManagerTerminateErrorReturnsThroughLoopAndKeepsQueueResponsive(t *testing.T) {
	f := newManagerFixture(t)
	f.process.terminateErr = errors.New("terminate failed at C:/private")
	started, err := f.manager.Start(context.Background(), task.StartRequest{
		IdempotencyKey: testID(34), Scenario: task.ScenarioHang, Timeout: time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.manager.Cancel(context.Background(), started.ID); err != nil {
		t.Fatal(err)
	}
	f.awaitTerminate(t, 1)
	f.awaitUnhealthy(t)
	if got, err := f.manager.Get(context.Background(), started.ID); err != nil || got.Status != task.StatusCancelling {
		t.Fatalf("Get = %#v, %v", got, err)
	}
	if page, err := f.manager.List(context.Background(), "", 10); err != nil || len(page.Items) != 1 {
		t.Fatalf("List = %#v, %v", page, err)
	}
	if _, err := f.manager.Start(context.Background(), task.StartRequest{IdempotencyKey: testID(35), Scenario: task.ScenarioSuccess, Timeout: time.Second}); !errors.Is(err, task.ErrStorageUnavailable) {
		t.Fatalf("Start after Terminate error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := f.manager.Shutdown(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Shutdown error = %v", err)
	}
	f.process.complete(task.ProcessResult{ExitCode: 1})
	finished := f.awaitStoredTask(t, started.ID, task.StatusFinished)
	if finished.Outcome != task.OutcomeCancelled {
		t.Fatalf("first cause lost: %#v", finished)
	}
	f.awaitEventType(t, task.EventTaskFinished, 1)
	terminal := 0
	for _, kind := range f.publisher.types() {
		if kind == task.EventTaskFinished {
			terminal++
		}
	}
	if terminal != 1 {
		t.Fatalf("task.finished events = %d", terminal)
	}
}

func TestManagerFirstTerminationCauseWins(t *testing.T) {
	tests := []struct {
		name       string
		first      string
		want       task.Outcome
		wantStatus task.Status
	}{
		{name: "timeout before cancel", first: "timeout", want: task.OutcomeTimedOut, wantStatus: task.StatusRunning},
		{name: "cancel before timeout", first: "cancel", want: task.OutcomeCancelled, wantStatus: task.StatusCancelling},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newManagerFixture(t)
			started, err := f.manager.Start(context.Background(), task.StartRequest{IdempotencyKey: testID(3), Scenario: task.ScenarioHang, Timeout: time.Second})
			if err != nil {
				t.Fatal(err)
			}
			if tt.first == "timeout" {
				f.clock.fire(t, time.Second)
				f.awaitTerminate(t, 1)
				got, err := f.manager.Cancel(context.Background(), started.ID)
				if err != nil || got.Status != tt.wantStatus {
					t.Fatalf("Cancel() = %#v, %v", got, err)
				}
			} else {
				if _, err := f.manager.Cancel(context.Background(), started.ID); err != nil {
					t.Fatal(err)
				}
				f.clock.fire(t, time.Second)
			}
			f.process.complete(task.ProcessResult{ExitCode: 137})
			finished := f.awaitTask(t, started.ID, task.StatusFinished)
			if finished.Outcome != tt.want {
				t.Fatalf("outcome = %q, want %q", finished.Outcome, tt.want)
			}
		})
	}
}

func TestManagerCoalescesCapsAndOrdersOutputBeforeCompletion(t *testing.T) {
	f := newManagerFixture(t)
	started, err := f.manager.Start(context.Background(), task.StartRequest{IdempotencyKey: testID(4), Scenario: task.ScenarioEmitOutput, Timeout: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	f.process.output(task.ProcessOutput{Stream: "stdout", Data: []byte("one")})
	f.process.output(task.ProcessOutput{Stream: "stderr", Data: []byte("two")})
	f.awaitOutputEvents(t, 2)
	outputEvents := f.publisher.ofType(task.EventTaskOutput)
	if len(outputEvents) != 2 {
		t.Fatalf("output events = %d", len(outputEvents))
	}
	for i, want := range []struct{ stream, text string }{{"stdout", "one"}, {"stderr", "two"}} {
		var got struct {
			Stream    string `json:"stream"`
			Text      string `json:"text"`
			Truncated bool   `json:"truncated"`
		}
		if err := json.Unmarshal(outputEvents[i].Payload, &got); err != nil {
			t.Fatal(err)
		}
		if got.Stream != want.stream || got.Text != want.text || got.Truncated {
			t.Fatalf("payload[%d] = %#v", i, got)
		}
	}

	f.process.output(task.ProcessOutput{Stream: "stdout", Data: []byte(strings.Repeat("x", 16*1024+1))})
	f.awaitOutputEvents(t, 4)
	outputEvents = f.publisher.ofType(task.EventTaskOutput)
	if len(outputEvents[2].Payload) == 0 {
		t.Fatal("missing capped output payload")
	}
	var block struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(outputEvents[2].Payload, &block); err != nil || len(block.Text) > 16*1024 {
		t.Fatalf("block bytes = %d, error = %v", len(block.Text), err)
	}

	f.process.complete(task.ProcessResult{ExitCode: 0})
	finished := f.awaitTask(t, started.ID, task.StatusFinished)
	if finished.Outcome != task.OutcomeSucceeded {
		t.Fatalf("finished = %#v", finished)
	}
	types := f.publisher.types()
	if types[len(types)-2] != task.EventArtifactCreated || types[len(types)-1] != task.EventTaskFinished {
		t.Fatalf("final event order = %v", types)
	}
	f.store.assertStrictSequences(t)
}

func TestManagerOutputTotalLimitCreatesOneTruncationEvent(t *testing.T) {
	f := newManagerFixture(t)
	_, err := f.manager.Start(context.Background(), task.StartRequest{IdempotencyKey: testID(5), Scenario: task.ScenarioEmitOutput, Timeout: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	block := []byte(strings.Repeat("a", 16*1024))
	for range (4*1024*1024)/(16*1024) + 2 {
		f.process.output(task.ProcessOutput{Stream: "stdout", Data: block})
	}
	f.awaitOutputTruncation(t)
	truncated := 0
	persisted := 0
	for _, event := range f.publisher.ofType(task.EventTaskOutput) {
		if strings.Contains(string(event.Payload), `"truncated":true`) {
			truncated++
			continue
		}
		var payload struct {
			Text string `json:"text"`
		}
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			t.Fatal(err)
		}
		persisted += len(payload.Text)
	}
	if truncated != 1 {
		t.Fatalf("truncation events = %d", truncated)
	}
	if persisted != 4*1024*1024 {
		t.Fatalf("persisted output = %d", persisted)
	}
}

func TestManagerMapsProcessResultsAndCommitsSummaryAtomically(t *testing.T) {
	tests := []struct {
		name    string
		result  task.ProcessResult
		outcome task.Outcome
		errCode string
	}{
		{"zero", task.ProcessResult{ExitCode: 0}, task.OutcomeSucceeded, ""},
		{"nonzero", task.ProcessResult{ExitCode: 17}, task.OutcomeCommandFailed, "command_failed"},
		{"runner error", task.ProcessResult{Err: errors.New("C:/secret TOKEN=value")}, task.OutcomeInfrastructureFailed, "infrastructure_failed"},
	}
	for index, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newManagerFixture(t)
			started, err := f.manager.Start(context.Background(), task.StartRequest{IdempotencyKey: testID(byte(10 + index)), Scenario: task.ScenarioSuccess, Timeout: time.Hour})
			if err != nil {
				t.Fatal(err)
			}
			f.process.complete(tt.result)
			finished := f.awaitTask(t, started.ID, task.StatusFinished)
			if finished.Outcome != tt.outcome || finished.ErrorCode != tt.errCode {
				t.Fatalf("finished = %#v", finished)
			}
			if strings.Contains(finished.ErrorMessage, "secret") || strings.Contains(finished.ErrorMessage, "TOKEN") {
				t.Fatalf("sensitive error = %q", finished.ErrorMessage)
			}
			mutation := f.store.lastMutation()
			if !mutation.DeleteLease || len(mutation.Artifacts) != 1 || len(mutation.Events) != 2 || mutation.Events[0].Type != task.EventArtifactCreated || mutation.Events[1].Type != task.EventTaskFinished {
				t.Fatalf("terminal mutation = %#v", mutation)
			}
			summary := f.artifacts.lastSummary()
			if summary["taskId"] != started.ID || summary["scenario"] != string(task.ScenarioSuccess) || summary["outcome"] != string(tt.outcome) || summary["finishedAt"] == "" {
				t.Fatalf("summary = %#v", summary)
			}
		})
	}
}

func TestManagerStorageFailureTripsCircuitAndTerminates(t *testing.T) {
	f := newManagerFixture(t)
	started, err := f.manager.Start(context.Background(), task.StartRequest{IdempotencyKey: testID(20), Scenario: task.ScenarioHang, Timeout: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	f.store.failAppend = task.ErrStorageUnavailable
	f.process.output(task.ProcessOutput{Stream: "stdout", Data: []byte(strings.Repeat("x", 16*1024))})
	f.awaitTerminate(t, 1)
	if f.manager.Healthy() {
		t.Fatal("manager stayed healthy")
	}
	if _, err := f.manager.Start(context.Background(), task.StartRequest{IdempotencyKey: testID(21), Scenario: task.ScenarioSuccess, Timeout: time.Second}); !errors.Is(err, task.ErrStorageUnavailable) {
		t.Fatalf("Start after storage failure = %v", err)
	}
	if got, err := f.manager.Get(context.Background(), started.ID); err != nil || got.Status == task.StatusFinished {
		t.Fatalf("storage failure fabricated terminal state: %#v, %v", got, err)
	}
	f.process.complete(task.ProcessResult{ExitCode: 0})
	deadline := time.After(time.Second)
	for f.process.closeCalls() == 0 {
		select {
		case <-deadline:
			t.Fatal("failed process was not closed")
		default:
		}
	}
	if got, err := f.manager.Get(context.Background(), started.ID); err != nil || got.Status == task.StatusFinished {
		t.Fatalf("process exit after storage failure fabricated terminal state: %#v, %v", got, err)
	}
}

func TestManagerStorageFaultStillTripsAfterEarlierCleanupFault(t *testing.T) {
	f := newManagerFixture(t)
	first := f.process
	second := newFakeProcess()
	third := newFakeProcess()
	f.processes.queue = []*fakeProcess{first, second, third}
	t.Cleanup(func() {
		second.completeOnce(task.ProcessResult{Err: errors.New("cleanup")})
		third.completeOnce(task.ProcessResult{Err: errors.New("cleanup")})
	})
	first.closeErr = errors.New("close failed")
	firstTask, err := f.manager.Start(context.Background(), task.StartRequest{IdempotencyKey: testID(36), Scenario: task.ScenarioSuccess, Timeout: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.manager.Start(context.Background(), task.StartRequest{IdempotencyKey: testID(37), Scenario: task.ScenarioHang, Timeout: time.Hour}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.manager.Start(context.Background(), task.StartRequest{IdempotencyKey: testID(38), Scenario: task.ScenarioHang, Timeout: time.Hour}); err != nil {
		t.Fatal(err)
	}
	first.complete(task.ProcessResult{ExitCode: 0})
	f.awaitStoredTask(t, firstTask.ID, task.StatusFinished)
	f.awaitProcessClose(t, first)
	f.awaitUnhealthy(t)
	f.store.failAppend = task.ErrStorageUnavailable
	second.output(task.ProcessOutput{Stream: "stdout", Data: []byte(strings.Repeat("x", 16*1024))})
	f.awaitProcessTerminate(t, second, 1)
	f.awaitProcessTerminate(t, third, 1)
	if second.terminateCalls() != 1 || third.terminateCalls() != 1 {
		t.Fatalf("termination calls = second:%d third:%d", second.terminateCalls(), third.terminateCalls())
	}
}

func TestManagerCancelApplyFailureDoesNotRecordCancelledCause(t *testing.T) {
	f := newManagerFixture(t)
	started, err := f.manager.Start(context.Background(), task.StartRequest{IdempotencyKey: testID(39), Scenario: task.ScenarioHang, Timeout: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	f.store.failApply = task.ErrStorageUnavailable
	if _, err := f.manager.Cancel(context.Background(), started.ID); !errors.Is(err, task.ErrStorageUnavailable) {
		t.Fatalf("Cancel error = %v", err)
	}
	f.awaitTerminate(t, 1)
	f.process.complete(task.ProcessResult{ExitCode: 1})
	f.awaitProcessClose(t, f.process)
	stored, err := f.manager.Get(context.Background(), started.ID)
	if err != nil || stored.Status != task.StatusRunning || stored.Outcome != "" {
		t.Fatalf("stored = %#v, %v", stored, err)
	}
	for _, kind := range f.publisher.types() {
		if kind == task.EventTaskCancellationRequested || kind == task.EventTaskFinished {
			t.Fatalf("published uncommitted cancellation/terminal event %q", kind)
		}
	}
}

func TestManagerArtifactFailureTripsStorageCircuitWithoutTerminalPersistence(t *testing.T) {
	f := newManagerFixture(t)
	finishedProcess := f.process
	otherProcess := newFakeProcess()
	f.processes.queue = []*fakeProcess{finishedProcess, otherProcess}
	t.Cleanup(func() { otherProcess.completeOnce(task.ProcessResult{Err: errors.New("cleanup")}) })
	started, err := f.manager.Start(context.Background(), task.StartRequest{IdempotencyKey: testID(22), Scenario: task.ScenarioSuccess, Timeout: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.manager.Start(context.Background(), task.StartRequest{IdempotencyKey: testID(40), Scenario: task.ScenarioHang, Timeout: time.Hour}); err != nil {
		t.Fatal(err)
	}
	f.artifacts.fail = errors.New("artifact unavailable at C:/secret")
	finishedProcess.complete(task.ProcessResult{ExitCode: 0})
	f.awaitUnhealthy(t)
	f.awaitProcessClose(t, finishedProcess)
	f.awaitProcessTerminate(t, otherProcess, 1)
	stored, err := f.manager.Get(context.Background(), started.ID)
	if err != nil || stored.Status == task.StatusFinished || stored.Outcome != "" {
		t.Fatalf("stored = %#v, %v", stored, err)
	}
	if len(f.store.artifactsCopy()) != 0 {
		t.Fatalf("artifact metadata = %#v", f.store.artifactsCopy())
	}
	for _, kind := range f.publisher.types() {
		if kind == task.EventArtifactCreated || kind == task.EventTaskFinished {
			t.Fatalf("published terminal event %q", kind)
		}
	}
	if _, err := f.manager.Start(context.Background(), task.StartRequest{IdempotencyKey: testID(41), Scenario: task.ScenarioSuccess, Timeout: time.Second}); !errors.Is(err, task.ErrStorageUnavailable) {
		t.Fatalf("Start after artifact failure = %v", err)
	}
}

func TestManagerTerminalDBFailureLeavesCommittedArtifactOrphanAndNoTerminalEvent(t *testing.T) {
	f := newManagerFixture(t)
	started, err := f.manager.Start(context.Background(), task.StartRequest{IdempotencyKey: testID(32), Scenario: task.ScenarioSuccess, Timeout: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	f.store.failApply = task.ErrStorageUnavailable
	f.process.complete(task.ProcessResult{ExitCode: 0})
	deadline := time.After(time.Second)
	for f.manager.Healthy() || f.process.closeCalls() == 0 {
		select {
		case <-deadline:
			t.Fatal("terminal DB failure did not trip and clean up")
		default:
		}
	}
	if len(f.artifacts.summariesCopy()) != 1 {
		t.Fatalf("committed artifact summaries = %d", len(f.artifacts.summariesCopy()))
	}
	stored, err := f.manager.Get(context.Background(), started.ID)
	if err != nil || stored.Status == task.StatusFinished {
		t.Fatalf("DB unexpectedly committed terminal task: %#v, %v", stored, err)
	}
	for _, kind := range f.publisher.types() {
		if kind == task.EventArtifactCreated || kind == task.EventTaskFinished {
			t.Fatalf("published uncommitted event %q", kind)
		}
	}
}

func TestManagerSerializesConcurrentGetListAndCompletion(t *testing.T) {
	f := newManagerFixture(t)
	started, err := f.manager.Start(context.Background(), task.StartRequest{IdempotencyKey: testID(33), Scenario: task.ScenarioSuccess, Timeout: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	begin := make(chan struct{})
	errs := make(chan error, 64)
	var wait sync.WaitGroup
	for index := range 32 {
		wait.Add(2)
		go func() {
			defer wait.Done()
			<-begin
			got, err := f.manager.Get(context.Background(), started.ID)
			if err != nil || (got.Status != task.StatusRunning && got.Status != task.StatusFinished) {
				errs <- fmt.Errorf("Get = %#v, %w", got, err)
			}
		}()
		go func(limit int) {
			defer wait.Done()
			<-begin
			page, err := f.manager.List(context.Background(), "", limit)
			if err != nil || len(page.Items) != 1 {
				errs <- fmt.Errorf("List items = %d, %w", len(page.Items), err)
			}
		}(index%10 + 1)
	}
	close(begin)
	f.process.complete(task.ProcessResult{ExitCode: 0})
	wait.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	finished := f.awaitTask(t, started.ID, task.StatusFinished)
	if finished.Outcome != task.OutcomeSucceeded {
		t.Fatalf("finished = %#v", finished)
	}
}

func TestManagerShutdownInterruptsAndWaitsForPersistence(t *testing.T) {
	f := newManagerFixture(t)
	started, err := f.manager.Start(context.Background(), task.StartRequest{IdempotencyKey: testID(23), Scenario: task.ScenarioHang, Timeout: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	shutdownDone := make(chan error, 1)
	go func() { shutdownDone <- f.manager.Shutdown(context.Background()) }()
	f.awaitTerminate(t, 1)
	select {
	case err := <-shutdownDone:
		t.Fatalf("Shutdown returned before terminal persistence: %v", err)
	default:
	}
	f.process.complete(task.ProcessResult{ExitCode: 137})
	select {
	case err := <-shutdownDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("Shutdown did not return")
	}
	finished, err := f.store.Get(context.Background(), started.ID)
	if err != nil || finished.Outcome != task.OutcomeInterrupted || f.process.closeCalls() != 1 {
		t.Fatalf("finished = %#v, err = %v, closes = %d", finished, err, f.process.closeCalls())
	}
	if f.manager.Healthy() {
		t.Fatal("closed manager is healthy")
	}
	if _, err := f.manager.Start(context.Background(), task.StartRequest{IdempotencyKey: testID(24), Scenario: task.ScenarioSuccess, Timeout: time.Second}); !errors.Is(err, task.ErrStorageUnavailable) {
		t.Fatalf("Start during shutdown = %v", err)
	}
}

func TestManagerShutdownDeadlineLeavesClosingAndNeverCancels(t *testing.T) {
	f := newManagerFixture(t)
	started, err := f.manager.Start(context.Background(), task.StartRequest{IdempotencyKey: testID(25), Scenario: task.ScenarioHang, Timeout: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := f.manager.Shutdown(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Shutdown error = %v", err)
	}
	if f.manager.Healthy() {
		t.Fatal("timed-out shutdown remained healthy")
	}
	f.awaitTerminate(t, 1)
	f.process.complete(task.ProcessResult{ExitCode: 1})
	finished := f.awaitStoredTask(t, started.ID, task.StatusFinished)
	if finished.Outcome != task.OutcomeInterrupted {
		t.Fatalf("outcome = %q", finished.Outcome)
	}
}

func TestManagerShutdownRetriesContextBoundProcessClose(t *testing.T) {
	f := newManagerFixtureWithCloseTimeout(t, 20*time.Millisecond)
	release := make(chan struct{})
	f.process.closeBlock = release
	started, err := f.manager.Start(context.Background(), task.StartRequest{IdempotencyKey: testID(88), Scenario: task.ScenarioSuccess, Timeout: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	f.process.complete(task.ProcessResult{ExitCode: 0})
	f.awaitStoredTask(t, started.ID, task.StatusFinished)
	f.awaitProcessClose(t, f.process)

	short, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := f.manager.Shutdown(short); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Shutdown after timed-out process Close = %v, want deadline", err)
	}
	close(release)
	long, cancelLong := context.WithTimeout(context.Background(), time.Second)
	defer cancelLong()
	if err := f.manager.Shutdown(long); err != nil {
		t.Fatalf("retry Shutdown = %v", err)
	}
	if calls := f.process.closeCalls(); calls < 2 {
		t.Fatalf("process Close calls = %d, want retry", calls)
	}
}

type managerFixture struct {
	manager   *testManager
	store     *fakeStore
	publisher *recordingPublisher
	processes *fakeProcessFactory
	process   *fakeProcess
	artifacts *fakeArtifactWriter
	clock     *fakeClock
}

func newManagerFixture(t *testing.T) *managerFixture {
	return newManagerFixtureWithOptions(t, 0, 0)
}

func newManagerFixtureWithCloseTimeout(t *testing.T, closeTimeout time.Duration) *managerFixture {
	return newManagerFixtureWithOptions(t, closeTimeout, 0)
}

func newManagerFixtureWithCommandQueue(t *testing.T, commandQueue int) *managerFixture {
	return newManagerFixtureWithOptions(t, 0, commandQueue)
}

func newManagerFixtureWithOptions(t *testing.T, closeTimeout time.Duration, commandQueue int) *managerFixture {
	t.Helper()
	clock := newFakeClock()
	store := newFakeStore()
	publisher := &recordingPublisher{}
	process := newFakeProcess()
	processes := &fakeProcessFactory{next: process}
	artifacts := &fakeArtifactWriter{}
	var next byte = 100
	manager, err := task.NewManager(task.ManagerConfig{
		Store: store, Publisher: publisher, Processes: processes, Artifacts: artifacts,
		Clock: clock, NewID: func() string { next++; return testID(next) },
		ServiceExecutable: "trusted-service", ServiceInstanceID: testID(99),
		TerminationGrace: time.Second, OutputFlushInterval: 25 * time.Millisecond,
		ProcessCloseTimeout: closeTimeout, CommandQueue: commandQueue,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		process.completeOnce(task.ProcessResult{Err: errors.New("test cleanup")})
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = manager.Shutdown(ctx)
	})
	return &managerFixture{&testManager{Manager: manager}, store, publisher, processes, process, artifacts, clock}
}

type testManager struct{ *task.Manager }

func (m *testManager) Start(ctx context.Context, request task.StartRequest) (task.Task, error) {
	if len(request.Plan.Steps) == 0 {
		request = simulationManagerRequest(request.IdempotencyKey, request.Scenario, request.Timeout)
	}
	return m.Manager.Start(ctx, request)
}

func simulationManagerRequest(idempotencyKey string, scenario task.Scenario, timeout time.Duration) task.StartRequest {
	plan := task.ExecutionPlan{
		Version: 1,
		Steps: []task.ExecutionStep{{
			ID:   "simulate",
			Kind: task.StepSimulation,
			Process: task.ProcessSpec{
				Executable: "trusted-service",
				Args:       []string{"--task-fixture", string(scenario)},
				Dir:        "simulation-dir",
			},
		}},
	}
	plan.Fingerprint = task.FingerprintPlan(plan)
	request, _ := json.Marshal(map[string]any{"scenario": scenario, "timeoutMs": timeout.Milliseconds()})
	return task.StartRequest{
		IdempotencyKey: idempotencyKey,
		Kind:           task.KindSimulation,
		Request:        request,
		Scenario:       scenario,
		Timeout:        timeout,
		Plan:           plan,
		Boundary:       fixedBoundary{},
	}
}

func (f *managerFixture) awaitTask(t *testing.T, id string, status task.Status) task.Task {
	t.Helper()
	deadline := time.After(time.Second)
	for {
		got, err := f.manager.Get(context.Background(), id)
		if err == nil && got.Status == status {
			return got
		}
		select {
		case <-deadline:
			t.Fatalf("task %s did not reach %s; got %#v, %v", id, status, got, err)
		default:
		}
	}
}

func (f *managerFixture) awaitStoredTask(t *testing.T, id string, status task.Status) task.Task {
	t.Helper()
	deadline := time.After(time.Second)
	for {
		got, err := f.store.Get(context.Background(), id)
		if err == nil && got.Status == status {
			return got
		}
		select {
		case <-deadline:
			t.Fatalf("stored task %s did not reach %s; got %#v, %v", id, status, got, err)
		default:
		}
	}
}

func (f *managerFixture) awaitTerminate(t *testing.T, calls int) {
	t.Helper()
	deadline := time.After(time.Second)
	for f.process.terminateCalls() < calls {
		select {
		case <-deadline:
			t.Fatalf("Terminate calls = %d, want %d", f.process.terminateCalls(), calls)
		default:
		}
	}
}

func (f *managerFixture) awaitProcessTerminate(t *testing.T, process *fakeProcess, calls int) {
	t.Helper()
	deadline := time.After(time.Second)
	for process.terminateCalls() < calls {
		select {
		case <-deadline:
			t.Fatalf("Terminate calls = %d, want %d", process.terminateCalls(), calls)
		default:
		}
	}
}

func (f *managerFixture) awaitProcessClose(t *testing.T, process *fakeProcess) {
	t.Helper()
	deadline := time.After(time.Second)
	for process.closeCalls() == 0 {
		select {
		case <-deadline:
			t.Fatal("process was not closed")
		default:
		}
	}
}

func (f *managerFixture) awaitUnhealthy(t *testing.T) {
	t.Helper()
	deadline := time.After(time.Second)
	for f.manager.Healthy() {
		select {
		case <-deadline:
			t.Fatal("manager remained healthy")
		default:
		}
	}
}

func (f *managerFixture) awaitEvents(t *testing.T, count int) {
	t.Helper()
	deadline := time.After(time.Second)
	for len(f.publisher.events()) < count {
		select {
		case <-deadline:
			t.Fatalf("events = %d, want at least %d", len(f.publisher.events()), count)
		default:
		}
	}
}

func (f *managerFixture) awaitEventType(t *testing.T, kind task.EventType, count int) {
	t.Helper()
	deadline := time.After(time.Second)
	for len(f.publisher.ofType(kind)) < count {
		select {
		case <-deadline:
			t.Fatalf("%s events = %d, want at least %d", kind, len(f.publisher.ofType(kind)), count)
		default:
		}
	}
}

func (f *managerFixture) awaitOutputEvents(t *testing.T, count int) {
	t.Helper()
	deadline := time.After(time.Second)
	for len(f.publisher.ofType(task.EventTaskOutput)) < count {
		if f.clock.tryFire(25 * time.Millisecond) {
			continue
		}
		select {
		case <-deadline:
			t.Fatalf("output events = %d, want at least %d", len(f.publisher.ofType(task.EventTaskOutput)), count)
		default:
		}
	}
}

func (f *managerFixture) awaitOutputTruncation(t *testing.T) {
	t.Helper()
	deadline := time.After(3 * time.Second)
	for {
		for _, event := range f.publisher.ofType(task.EventTaskOutput) {
			if strings.Contains(string(event.Payload), `"truncated":true`) {
				return
			}
		}
		select {
		case <-deadline:
			t.Fatal("truncation event not published")
		default:
		}
	}
}

type fakeStore struct {
	mu           sync.Mutex
	tasks        map[string]task.Task
	keys         map[string]string
	eventsValue  []task.Event
	artifacts    []task.Artifact
	leases       map[string]task.ProcessLease
	mutations    []task.Mutation
	sequence     int64
	failAppend   error
	failApply    error
	failApplyAt  int
	failApplyFor int
	failApplyErr error
	applyCalls   int
}

func newFakeStore() *fakeStore {
	return &fakeStore{tasks: map[string]task.Task{}, keys: map[string]string{}, leases: map[string]task.ProcessLease{}}
}

func (s *fakeStore) Create(_ context.Context, value task.Task, steps []task.StepSnapshot, draft task.EventDraft) (task.Task, []task.Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if existingID, ok := s.keys[value.IdempotencyKey]; ok {
		existing := s.tasks[existingID]
		if existing.RequestHash != value.RequestHash {
			return task.Task{}, nil, task.ErrIdempotencyConflict
		}
		return existing, nil, nil
	}
	event := s.appendLocked(draft)
	value.LastSequence = event.Sequence
	value.Steps = append([]task.StepSnapshot(nil), steps...)
	s.tasks[value.ID] = value
	s.keys[value.IdempotencyKey] = value.ID
	return value, []task.Event{event}, nil
}

func (s *fakeStore) FindByIdempotencyKey(_ context.Context, key string) (task.Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id, ok := s.keys[key]
	if !ok {
		return task.Task{}, task.ErrNotFound
	}
	return s.tasks[id], nil
}

func (s *fakeStore) Get(_ context.Context, id string) (task.Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	value, ok := s.tasks[id]
	if !ok {
		return task.Task{}, task.ErrNotFound
	}
	return value, nil
}

func (s *fakeStore) List(_ context.Context, _ string, limit int) (task.Page[task.Task], error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := task.Page[task.Task]{}
	for _, value := range s.tasks {
		result.Items = append(result.Items, value)
		if len(result.Items) == limit {
			break
		}
	}
	return result, nil
}

func (s *fakeStore) Apply(_ context.Context, mutation task.Mutation) (task.Task, []task.Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.applyCalls++
	if s.failApply != nil {
		return task.Task{}, nil, s.failApply
	}
	if s.failApplyAt > 0 && s.applyCalls >= s.failApplyAt &&
		s.applyCalls < s.failApplyAt+max(s.failApplyFor, 1) {
		return task.Task{}, nil, s.failApplyErr
	}
	current, ok := s.tasks[mutation.Task.ID]
	if !ok {
		return task.Task{}, nil, task.ErrNotFound
	}
	if current.Status != mutation.Expected {
		return task.Task{}, nil, task.ErrConflict
	}
	steps := append([]task.StepSnapshot(nil), current.Steps...)
	for _, change := range mutation.Steps {
		found := false
		for index := range steps {
			if steps[index].ID != change.Step.ID {
				continue
			}
			if steps[index].Status != change.Expected {
				return task.Task{}, nil, task.ErrConflict
			}
			steps[index] = change.Step
			found = true
			break
		}
		if !found {
			return task.Task{}, nil, task.ErrConflict
		}
	}
	events := make([]task.Event, 0, len(mutation.Events))
	for _, draft := range mutation.Events {
		events = append(events, s.appendLocked(draft))
	}
	if len(events) > 0 {
		mutation.Task.LastSequence = events[len(events)-1].Sequence
	}
	mutation.Task.Steps = steps
	s.tasks[mutation.Task.ID] = mutation.Task
	if mutation.PutLease != nil {
		s.leases[mutation.Task.ID] = *mutation.PutLease
	}
	if mutation.DeleteLease {
		delete(s.leases, mutation.Task.ID)
	}
	s.artifacts = append(s.artifacts, mutation.Artifacts...)
	s.mutations = append(s.mutations, mutation)
	return mutation.Task, events, nil
}

func (s *fakeStore) AppendEvent(_ context.Context, _ string, draft task.EventDraft) (task.Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failAppend != nil {
		return task.Event{}, s.failAppend
	}
	event := s.appendLocked(draft)
	value := s.tasks[draft.TaskID]
	value.LastSequence = event.Sequence
	s.tasks[draft.TaskID] = value
	return event, nil
}

func (s *fakeStore) appendLocked(draft task.EventDraft) task.Event {
	s.sequence++
	event := task.Event{Sequence: s.sequence, ID: testID(byte(s.sequence)), EventDraft: draft}
	s.eventsValue = append(s.eventsValue, event)
	return event
}

func (s *fakeStore) UpdateLease(_ context.Context, lease task.ProcessLease) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.leases[lease.TaskID] = lease
	return nil
}
func (s *fakeStore) Watermark(context.Context) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sequence, nil
}
func (s *fakeStore) EventsAfter(context.Context, int64, int64, int) ([]task.Event, error) {
	return nil, nil
}
func (s *fakeStore) ListArtifacts(context.Context, string, string, int) (task.Page[task.Artifact], error) {
	return task.Page[task.Artifact]{}, nil
}
func (s *fakeStore) GetArtifact(context.Context, string) (task.Artifact, error) {
	return task.Artifact{}, task.ErrNotFound
}
func (s *fakeStore) ActiveLeases(context.Context) ([]task.ProcessLease, error) { return nil, nil }
func (s *fakeStore) RecoverInterrupted(context.Context, time.Time) ([]task.Event, error) {
	return nil, nil
}
func (s *fakeStore) ReferencedArtifactPaths(context.Context) (map[string]struct{}, error) {
	return nil, nil
}
func (s *fakeStore) Close() error { return nil }

func (s *fakeStore) lastMutation() task.Mutation {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.mutations) == 0 {
		return task.Mutation{}
	}
	return s.mutations[len(s.mutations)-1]
}

func (s *fakeStore) applyCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.applyCalls
}

func (s *fakeStore) firstMutation() task.Mutation {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.mutations) == 0 {
		return task.Mutation{}
	}
	return s.mutations[0]
}

func (s *fakeStore) lease(id string) task.ProcessLease {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.leases[id]
}

func (s *fakeStore) artifactsCopy() []task.Artifact {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]task.Artifact(nil), s.artifacts...)
}

func (s *fakeStore) assertStrictSequences(t *testing.T) {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	for index, event := range s.eventsValue {
		if event.Sequence != int64(index+1) {
			t.Fatalf("sequence[%d] = %d", index, event.Sequence)
		}
	}
}

type recordingPublisher struct {
	mu        sync.Mutex
	value     []task.Event
	panicType task.EventType
}

func (p *recordingPublisher) Publish(event task.Event) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if event.Type == p.panicType {
		panic("publisher failure")
	}
	p.value = append(p.value, event)
}
func (p *recordingPublisher) events() []task.Event {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]task.Event(nil), p.value...)
}
func (p *recordingPublisher) types() []task.EventType {
	events := p.events()
	result := make([]task.EventType, len(events))
	for index := range events {
		result[index] = events[index].Type
	}
	return result
}
func (p *recordingPublisher) ofType(kind task.EventType) []task.Event {
	var result []task.Event
	for _, event := range p.events() {
		if event.Type == kind {
			result = append(result, event)
		}
	}
	return result
}

type fakeProcessFactory struct {
	mu              sync.Mutex
	next            *fakeProcess
	specs           []task.ProcessSpec
	prepareErr      error
	queue           []*fakeProcess
	prepareBlockAt  int
	prepareBlock    <-chan struct{}
	prepareEntered  chan struct{}
	prepareCanceled chan struct{}
}

func (f *fakeProcessFactory) Prepare(ctx context.Context, spec task.ProcessSpec, taskID, serviceID string) (task.ManagedProcess, error) {
	f.mu.Lock()
	if taskID == "" || serviceID == "" {
		f.mu.Unlock()
		return nil, errors.New("missing ids")
	}
	f.specs = append(f.specs, spec)
	call := len(f.specs)
	prepareErr := f.prepareErr
	process := f.next
	if prepareErr == nil && len(f.queue) > 0 {
		process = f.queue[0]
		f.queue = f.queue[1:]
	}
	var block <-chan struct{}
	var entered chan struct{}
	var canceled chan struct{}
	if f.prepareBlockAt == call {
		block = f.prepareBlock
		entered = f.prepareEntered
		canceled = f.prepareCanceled
	}
	f.mu.Unlock()
	if entered != nil {
		close(entered)
	}
	if canceled != nil {
		go func() {
			<-ctx.Done()
			close(canceled)
		}()
	}
	if block != nil {
		<-block
	}
	if prepareErr != nil {
		return nil, prepareErr
	}
	if process == nil {
		return nil, errors.New("missing fake process")
	}
	process.mu.Lock()
	process.lease.TaskID = taskID
	process.lease.ServiceInstanceID = serviceID
	process.mu.Unlock()
	return process, nil
}
func (f *fakeProcessFactory) prepareCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.specs)
}
func (f *fakeProcessFactory) lastSpec() task.ProcessSpec {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.specs[len(f.specs)-1]
}

type fakeProcess struct {
	mu             sync.Mutex
	lease          task.ProcessLease
	outputs        chan task.ProcessOutput
	done           chan task.ProcessResult
	completeMu     sync.Once
	starts         int
	terminates     int
	closes         int
	startErr       error
	startBlock     <-chan struct{}
	startEntered   chan struct{}
	startCanceled  chan struct{}
	terminateBlock <-chan struct{}
	terminateErr   error
	closeErr       error
	closeBlock     <-chan struct{}
}

func newFakeProcess() *fakeProcess {
	return &fakeProcess{
		lease:   task.ProcessLease{HostPID: 41, HostStartIdentity: "host-start", TargetProcessGroup: 0},
		outputs: make(chan task.ProcessOutput, 1024), done: make(chan task.ProcessResult, 1),
	}
}
func (p *fakeProcess) Lease() task.ProcessLease { p.mu.Lock(); defer p.mu.Unlock(); return p.lease }
func (p *fakeProcess) Start(ctx context.Context) error {
	p.mu.Lock()
	p.starts++
	p.lease.TargetProcessGroup = 42
	block, entered, canceled, startErr := p.startBlock, p.startEntered, p.startCanceled, p.startErr
	p.mu.Unlock()
	if entered != nil {
		close(entered)
	}
	if canceled != nil {
		go func() {
			<-ctx.Done()
			close(canceled)
		}()
	}
	if block != nil {
		<-block
	}
	return startErr
}
func (p *fakeProcess) Output() <-chan task.ProcessOutput { return p.outputs }
func (p *fakeProcess) Done() <-chan task.ProcessResult   { return p.done }
func (p *fakeProcess) Terminate(context.Context, time.Duration) error {
	p.mu.Lock()
	p.terminates++
	block := p.terminateBlock
	p.mu.Unlock()
	if block != nil {
		<-block
	}
	return p.terminateErr
}
func (p *fakeProcess) Close(ctx context.Context) error {
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
func (p *fakeProcess) output(value task.ProcessOutput)   { p.outputs <- value }
func (p *fakeProcess) complete(value task.ProcessResult) { p.completeOnce(value) }
func (p *fakeProcess) completeOnce(value task.ProcessResult) {
	p.completeMu.Do(func() { close(p.outputs); p.done <- value; close(p.done) })
}
func (p *fakeProcess) terminateCalls() int { p.mu.Lock(); defer p.mu.Unlock(); return p.terminates }
func (p *fakeProcess) closeCalls() int     { p.mu.Lock(); defer p.mu.Unlock(); return p.closes }
func (p *fakeProcess) startCalls() int     { p.mu.Lock(); defer p.mu.Unlock(); return p.starts }

type fakeArtifactWriter struct {
	mu        sync.Mutex
	fail      error
	summaries []map[string]string
}

func (w *fakeArtifactWriter) CommitJSON(_ context.Context, taskID, artifactID string, at time.Time, value any) (task.Artifact, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.fail != nil {
		return task.Artifact{}, w.fail
	}
	raw, _ := json.Marshal(value)
	var summary map[string]string
	_ = json.Unmarshal(raw, &summary)
	w.summaries = append(w.summaries, summary)
	return task.Artifact{ID: artifactID, TaskID: taskID, Kind: "task-summary", RelativePath: fmt.Sprintf("tasks/%s/%s.json", taskID, artifactID), MIMEType: "application/json", Size: int64(len(raw)), SHA256: strings.Repeat("a", 64), CreatedAt: at}, nil
}
func (w *fakeArtifactWriter) lastSummary() map[string]string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.summaries[len(w.summaries)-1]
}
func (w *fakeArtifactWriter) summariesCopy() []map[string]string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]map[string]string(nil), w.summaries...)
}

type clockWaiter struct {
	delay time.Duration
	ch    chan time.Time
}
type fakeClock struct {
	mu      sync.Mutex
	now     time.Time
	waiters []clockWaiter
}

func newFakeClock() *fakeClock      { return &fakeClock{now: time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)} }
func (c *fakeClock) Now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.now }
func (c *fakeClock) After(delay time.Duration) <-chan time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	ch := make(chan time.Time, 1)
	c.waiters = append(c.waiters, clockWaiter{delay, ch})
	return ch
}
func (c *fakeClock) fire(t *testing.T, delay time.Duration) {
	t.Helper()
	deadline := time.After(time.Second)
	for {
		c.mu.Lock()
		for index, waiter := range c.waiters {
			if waiter.delay == delay {
				c.waiters = append(c.waiters[:index], c.waiters[index+1:]...)
				c.now = c.now.Add(delay)
				now := c.now
				c.mu.Unlock()
				waiter.ch <- now
				close(waiter.ch)
				return
			}
		}
		c.mu.Unlock()
		select {
		case <-deadline:
			t.Fatalf("no clock waiter for %s", delay)
		default:
		}
	}
}

func (c *fakeClock) tryFire(delay time.Duration) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for index, waiter := range c.waiters {
		if waiter.delay == delay {
			c.waiters = append(c.waiters[:index], c.waiters[index+1:]...)
			c.now = c.now.Add(delay)
			waiter.ch <- c.now
			close(waiter.ch)
			return true
		}
	}
	return false
}

func (c *fakeClock) afterCalls(delay time.Duration) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	count := 0
	for _, waiter := range c.waiters {
		if waiter.delay == delay {
			count++
		}
	}
	return count
}

func testID(value byte) string { return fmt.Sprintf("%032x", value) }
