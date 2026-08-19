package coveragerun

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"unit-test-ide.local/test-service/internal/coveragedomain"
)

type fakeStepExecutor struct {
	steps   []Phase
	results map[Phase]StepResult
	errors  map[Phase]error
}

func (executor *fakeStepExecutor) Execute(_ context.Context, phase Phase) (StepResult, error) {
	executor.steps = append(executor.steps, phase)
	if err := executor.errors[phase]; err != nil {
		return StepResult{}, err
	}
	return executor.results[phase], nil
}

func TestRunExecutesEveryPhaseInOrderAndRetainsAssertionFailure(t *testing.T) {
	executor := &fakeStepExecutor{
		results: map[Phase]StepResult{
			PhaseConfigure: {Phase: PhaseConfigure, Succeeded: true},
			PhaseBuild:     {Phase: PhaseBuild, Succeeded: true},
			PhaseTest:      {Phase: PhaseTest, AssertionFailure: true},
			PhaseMerge:     {Phase: PhaseMerge, Succeeded: true},
			PhaseNormalize: {Phase: PhaseNormalize, Succeeded: true},
			PhaseReport:    {Phase: PhaseReport, Succeeded: true},
			PhasePublish:   {Phase: PhasePublish, Succeeded: true},
		},
		errors: map[Phase]error{},
	}
	state, err := Run(context.Background(), executor)
	if err != nil {
		t.Fatal(err)
	}
	if state.Outcome != coveragedomain.OutcomeAvailable || !state.TestFailed || !state.Terminal {
		t.Fatalf("state = %#v", state)
	}
	want := []Phase{PhaseConfigure, PhaseBuild, PhaseTest, PhaseMerge, PhaseNormalize, PhaseReport, PhasePublish}
	if !reflect.DeepEqual(executor.steps, want) {
		t.Fatalf("steps = %#v, want %#v", executor.steps, want)
	}
}

func TestRunStopsOnExecutorErrorAndCancelsBeforeExecuting(t *testing.T) {
	failure := errors.New("compiler unavailable")
	executor := &fakeStepExecutor{
		results: map[Phase]StepResult{PhaseConfigure: {Phase: PhaseConfigure, Succeeded: true}},
		errors:  map[Phase]error{PhaseBuild: failure},
	}
	state, err := Run(context.Background(), executor)
	if !errors.Is(err, failure) || state.Reason != coveragedomain.ReasonBuildFailed || !state.Terminal {
		t.Fatalf("failure state = %#v, err = %v", state, err)
	}

	cancelled := &fakeStepExecutor{results: map[Phase]StepResult{}, errors: map[Phase]error{}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	state, err = Run(ctx, cancelled)
	if err != nil || state.Outcome != coveragedomain.OutcomeCancelled || len(cancelled.steps) != 0 {
		t.Fatalf("cancelled state = %#v, err = %v, steps = %#v", state, err, cancelled.steps)
	}
}
