package task_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"unit-test-ide.local/test-service/internal/task"
)

func TestManagerResultInterpreterAcceptsValidatedNonzeroTestExit(t *testing.T) {
	interpreter := &recordingResultInterpreter{
		verdict: task.StepVerdictSucceeded,
	}
	f := newManagerFixture(t)
	request := oneStepBuildRequest(testID(141))
	request.ResultInterpreter = interpreter
	started, err := f.manager.Start(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	f.process.complete(task.ProcessResult{ExitCode: 17})
	finished := f.awaitTask(t, started.ID, task.StatusFinished)
	if finished.Outcome != task.OutcomeSucceeded ||
		len(finished.Steps) != 1 ||
		finished.Steps[0].Status != task.StepSucceeded ||
		finished.Steps[0].ExitCode == nil ||
		*finished.Steps[0].ExitCode != 17 {
		t.Fatalf("interpreted task = %#v", finished)
	}
	if calls, result := interpreter.state(); calls != 1 ||
		result.ExitCode != 17 {
		t.Fatalf("interpreter calls = %d, result = %#v", calls, result)
	}
}

func TestManagerDoesNotExpandExistingObserverToFinalStep(t *testing.T) {
	observer := &recordingStepObserver{}
	f := newManagerFixtureWithObserver(t, observer)
	request := oneStepBuildRequest(testID(152))
	started, err := f.manager.Start(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	f.process.complete(task.ProcessResult{ExitCode: 0})
	f.awaitTask(t, started.ID, task.StatusFinished)
	if observer.calls != 0 {
		t.Fatalf("final StepObserver calls = %d, want 0", observer.calls)
	}
}

func TestManagerResultInterpreterFailureIsInfrastructureFailure(t *testing.T) {
	interpreter := &recordingResultInterpreter{
		err: errors.New("parser persistence failed with secret"),
	}
	f := newManagerFixture(t)
	request := oneStepBuildRequest(testID(142))
	request.ResultInterpreter = interpreter
	started, err := f.manager.Start(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	f.process.complete(task.ProcessResult{ExitCode: 3})
	finished := f.awaitTask(t, started.ID, task.StatusFinished)
	if finished.Outcome != task.OutcomeInfrastructureFailed ||
		finished.ErrorCode != "infrastructure_failed" ||
		finished.Steps[0].Status != task.StepFailed {
		t.Fatalf("interpreter failure task = %#v", finished)
	}
}

func TestManagerResultOutputFailureTerminatesBeforeDomainCompletion(t *testing.T) {
	interpreter := &recordingResultInterpreter{
		observeErr: errors.New("result stream persistence failed"),
		verdict:    task.StepVerdictSucceeded,
	}
	f := newManagerFixture(t)
	request := oneStepBuildRequest(testID(143))
	request.ResultInterpreter = interpreter
	started, err := f.manager.Start(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	f.process.output(task.ProcessOutput{
		Source: "test-000001",
		Stream: "stdout",
		Data:   []byte("test output\n"),
	})
	f.awaitProcessTerminate(t, f.process, 1)
	finished := f.awaitTask(t, started.ID, task.StatusFinished)
	if finished.Outcome != task.OutcomeInfrastructureFailed ||
		finished.Steps[0].Status != task.StepFailed {
		t.Fatalf("output interpreter failure task = %#v", finished)
	}
	if observeCalls, interpretCalls := interpreter.outputState(); observeCalls != 1 ||
		interpretCalls != 0 {
		t.Fatalf(
			"observer/interpreter calls = %d/%d",
			observeCalls,
			interpretCalls,
		)
	}
	if output := interpreter.outputValue(); output.Source != "test-000001" {
		t.Fatalf("observed output = %#v", output)
	}
}

func TestManagerTimeoutPreventsInterpreterAndContinuation(t *testing.T) {
	interpreter := &recordingResultInterpreter{
		verdict: task.StepVerdictSucceeded,
	}
	continuation := &recordingContinuation{}
	f := newManagerFixture(t)
	request := oneStepBuildRequest(testID(144))
	request.Timeout = 250 * time.Millisecond
	request.ResultInterpreter = interpreter
	request.Continuation = continuation
	started, err := f.manager.Start(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	f.clock.fire(t, 250*time.Millisecond)
	f.awaitProcessTerminate(t, f.process, 1)
	f.process.complete(task.ProcessResult{ExitCode: 137})
	finished := f.awaitTask(t, started.ID, task.StatusFinished)
	if finished.Outcome != task.OutcomeTimedOut {
		t.Fatalf("timed out task = %#v", finished)
	}
	if calls, _ := interpreter.state(); calls != 0 {
		t.Fatalf("interpreter calls after timeout = %d", calls)
	}
	if calls := continuation.callCount(); calls != 0 {
		t.Fatalf("continuation calls after timeout = %d", calls)
	}
}

func TestManagerCancellationPreventsInterpreterAndContinuation(t *testing.T) {
	interpreter := &recordingResultInterpreter{
		verdict: task.StepVerdictSucceeded,
	}
	continuation := &recordingContinuation{}
	f := newManagerFixture(t)
	request := oneStepBuildRequest(testID(151))
	request.ResultInterpreter = interpreter
	request.Continuation = continuation
	started, err := f.manager.Start(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.manager.Cancel(context.Background(), started.ID); err != nil {
		t.Fatal(err)
	}
	f.awaitProcessTerminate(t, f.process, 1)
	f.process.complete(task.ProcessResult{ExitCode: 0})
	finished := f.awaitTask(t, started.ID, task.StatusFinished)
	if finished.Outcome != task.OutcomeCancelled {
		t.Fatalf("cancelled task = %#v", finished)
	}
	if calls, _ := interpreter.state(); calls != 0 {
		t.Fatalf("interpreter calls after cancellation = %d", calls)
	}
	if calls := continuation.callCount(); calls != 0 {
		t.Fatalf("continuation calls after cancellation = %d", calls)
	}
}

type recordingResultInterpreter struct {
	mu           sync.Mutex
	verdict      task.StepVerdict
	err          error
	observeErr   error
	calls        int
	observeCalls int
	lastResult   task.ProcessResult
	lastOutput   task.ProcessOutput
	lastTask     task.Task
	lastStep     task.ExecutionStep
}

func (interpreter *recordingResultInterpreter) Interpret(
	_ context.Context,
	current task.Task,
	step task.ExecutionStep,
	result task.ProcessResult,
) (task.StepVerdict, error) {
	interpreter.mu.Lock()
	defer interpreter.mu.Unlock()
	interpreter.calls++
	interpreter.lastTask = current
	interpreter.lastStep = step
	interpreter.lastResult = result
	return interpreter.verdict, interpreter.err
}

func (interpreter *recordingResultInterpreter) ObserveOutput(
	_ context.Context,
	current task.Task,
	step task.ExecutionStep,
	output task.ProcessOutput,
) error {
	interpreter.mu.Lock()
	defer interpreter.mu.Unlock()
	interpreter.observeCalls++
	interpreter.lastTask = current
	interpreter.lastStep = step
	interpreter.lastOutput = output
	return interpreter.observeErr
}

func (interpreter *recordingResultInterpreter) state() (
	int,
	task.ProcessResult,
) {
	interpreter.mu.Lock()
	defer interpreter.mu.Unlock()
	return interpreter.calls, interpreter.lastResult
}

func (interpreter *recordingResultInterpreter) outputState() (int, int) {
	interpreter.mu.Lock()
	defer interpreter.mu.Unlock()
	return interpreter.observeCalls, interpreter.calls
}

func (interpreter *recordingResultInterpreter) outputValue() task.ProcessOutput {
	interpreter.mu.Lock()
	defer interpreter.mu.Unlock()
	return interpreter.lastOutput
}

func oneStepBuildRequest(idempotencyKey string) task.StartRequest {
	request := twoStepCMakeStartRequest(
		idempotencyKey,
		time.Minute,
		fixedBoundary{},
	)
	request.Plan.Steps = request.Plan.Steps[1:]
	request.Plan.Fingerprint = task.FingerprintPlan(request.Plan)
	return request
}
