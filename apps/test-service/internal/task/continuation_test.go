package task_test

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"

	"unit-test-ide.local/test-service/internal/task"
)

func TestManagerPersistsValidatedContinuationBeforeStartingIt(t *testing.T) {
	second := newFakeProcess()
	f := newManagerFixture(t)
	f.processes.queue = []*fakeProcess{f.process, second}
	t.Cleanup(func() {
		second.completeOnce(task.ProcessResult{Err: errors.New("test cleanup")})
	})
	continuation := &recordingContinuation{
		byStep: map[string]task.Continuation{
			"build": {
				Steps: []task.ExecutionStep{continuationStep("test-run")},
			},
		},
	}
	request := oneStepBuildRequest(testID(145))
	request.Continuation = continuation
	started, err := f.manager.Start(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	f.process.complete(task.ProcessResult{ExitCode: 0})
	awaitProcessStart(t, second)

	running, err := f.manager.Get(context.Background(), started.ID)
	if err != nil {
		t.Fatal(err)
	}
	if running.Status != task.StatusRunning ||
		running.ActiveStep != "test-run" ||
		len(running.Steps) != 2 ||
		running.Steps[0].Status != task.StepSucceeded ||
		running.Steps[1].Status != task.StepRunning {
		t.Fatalf("continued task = %#v", running)
	}
	var appendMutation task.Mutation
	for _, mutation := range f.store.mutationsFor(started.ID) {
		if len(mutation.AppendSteps) != 0 {
			appendMutation = mutation
			break
		}
	}
	if len(appendMutation.AppendSteps) != 1 ||
		appendMutation.AppendSteps[0].ID != "test-run" ||
		appendMutation.AppendSteps[0].Status != task.StepPending {
		t.Fatalf("continuation mutation = %#v", appendMutation)
	}
	if got := f.processes.lastSpec(); !reflect.DeepEqual(
		got.Args,
		[]string{"--test-run"},
	) {
		t.Fatalf("continued ProcessSpec = %#v", got)
	}
	second.complete(task.ProcessResult{ExitCode: 0})
	finished := f.awaitTask(t, started.ID, task.StatusFinished)
	if finished.Outcome != task.OutcomeSucceeded {
		t.Fatalf("finished continuation task = %#v", finished)
	}
}

func TestManagerRejectsUnvalidatedContinuationWithoutStartingProcess(t *testing.T) {
	tests := []struct {
		name string
		step task.ExecutionStep
	}{
		{
			name: "duplicate ID",
			step: continuationStep("build"),
		},
		{
			name: "boundary rejected executable",
			step: func() task.ExecutionStep {
				step := continuationStep("test-run")
				step.Process.Executable = "untrusted"
				return step
			}(),
		},
		{
			name: "service token environment",
			step: func() task.ExecutionStep {
				step := continuationStep("test-run")
				step.Process.Env = []string{
					"UNIT_TEST_SERVICE_TOKEN=secret",
				}
				return step
			}(),
		},
	}
	for index, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			second := newFakeProcess()
			f := newManagerFixture(t)
			f.processes.queue = []*fakeProcess{f.process, second}
			t.Cleanup(func() {
				second.completeOnce(task.ProcessResult{
					Err: errors.New("test cleanup"),
				})
			})
			request := oneStepBuildRequest(testID(byte(146 + index)))
			request.Continuation = &recordingContinuation{
				byStep: map[string]task.Continuation{
					"build": {Steps: []task.ExecutionStep{test.step}},
				},
			}
			started, err := f.manager.Start(context.Background(), request)
			if err != nil {
				t.Fatal(err)
			}
			f.process.complete(task.ProcessResult{ExitCode: 0})
			finished := f.awaitTask(t, started.ID, task.StatusFinished)
			if finished.Outcome != task.OutcomeInfrastructureFailed ||
				finished.Steps[0].Status != task.StepFailed ||
				second.startCalls() != 0 {
				t.Fatalf("invalid continuation task = %#v", finished)
			}
		})
	}
}

func TestManagerContinuationFailureDoesNotAppendPartialPlan(t *testing.T) {
	f := newManagerFixture(t)
	continuation := &recordingContinuation{
		err: errors.New("catalog refresh failed"),
	}
	request := oneStepBuildRequest(testID(149))
	request.Continuation = continuation
	started, err := f.manager.Start(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	f.process.complete(task.ProcessResult{ExitCode: 0})
	finished := f.awaitTask(t, started.ID, task.StatusFinished)
	if finished.Outcome != task.OutcomeInfrastructureFailed ||
		len(finished.Steps) != 1 ||
		len(f.store.lastMutation().AppendSteps) != 0 {
		t.Fatalf("failed continuation task = %#v", finished)
	}
}

func TestManagerAllowsRuntimePlanToGrowPastInitialPlanLimit(t *testing.T) {
	second := newFakeProcess()
	f := newManagerFixture(t)
	f.processes.queue = []*fakeProcess{f.process, second}
	continuation := &recordingContinuation{
		byStep: map[string]task.Continuation{
			"build": {
				Steps: []task.ExecutionStep{
					continuationStep("test-1"),
					continuationStep("test-2"),
					continuationStep("test-3"),
					continuationStep("test-4"),
					continuationStep("test-5"),
					continuationStep("test-6"),
					continuationStep("test-7"),
					continuationStep("test-8"),
				},
			},
		},
	}
	request := oneStepBuildRequest(testID(150))
	request.Continuation = continuation
	started, err := f.manager.Start(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	f.process.complete(task.ProcessResult{ExitCode: 0})
	awaitProcessStart(t, second)

	running, err := f.manager.Get(context.Background(), started.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(running.Steps) != 9 ||
		running.Steps[0].Status != task.StepSucceeded ||
		running.Steps[1].Status != task.StepRunning {
		t.Fatalf("expanded runtime plan = %#v", running)
	}
	if _, err := f.manager.Cancel(context.Background(), started.ID); err != nil {
		t.Fatal(err)
	}
	second.complete(task.ProcessResult{Err: context.Canceled})
	f.awaitTask(t, started.ID, task.StatusFinished)
}

type recordingContinuation struct {
	mu     sync.Mutex
	byStep map[string]task.Continuation
	err    error
	calls  int
}

func (continuation *recordingContinuation) AfterStep(
	_ context.Context,
	_ task.Task,
	step task.ExecutionStep,
	_ task.StepResult,
) (task.Continuation, error) {
	continuation.mu.Lock()
	defer continuation.mu.Unlock()
	continuation.calls++
	return continuation.byStep[step.ID], continuation.err
}

func (continuation *recordingContinuation) callCount() int {
	continuation.mu.Lock()
	defer continuation.mu.Unlock()
	return continuation.calls
}

func continuationStep(id string) task.ExecutionStep {
	return task.ExecutionStep{
		ID:   id,
		Kind: task.StepTestRun,
		Process: task.ProcessSpec{
			Executable: "trusted-service",
			Args:       []string{"--test-run"},
			Dir:        "simulation-dir",
		},
		Public: task.CommandSummary{
			Executable: "cmake",
			Args:       []string{"--test-run"},
		},
	}
}
