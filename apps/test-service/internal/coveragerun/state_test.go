package coveragerun

import (
	"errors"
	"testing"

	"unit-test-ide.local/test-service/internal/coveragedomain"
)

func TestStateAdvancesCoveragePipelineAndKeepsAssertionFailureCollectable(t *testing.T) {
	state := NewState()
	for _, phase := range []Phase{PhaseConfigure, PhaseBuild} {
		var err error
		state, err = state.Apply(StepResult{Phase: phase, Succeeded: true})
		if err != nil {
			t.Fatal(err)
		}
	}
	state, err := state.Apply(StepResult{Phase: PhaseTest, Succeeded: false, AssertionFailure: true})
	if err != nil {
		t.Fatal(err)
	}
	if state.Phase != PhaseMerge || !state.TestFailed || state.Terminal {
		t.Fatalf("assertion failure state = %#v", state)
	}
	for _, phase := range []Phase{PhaseMerge, PhaseNormalize, PhaseReport, PhasePublish} {
		state, err = state.Apply(StepResult{Phase: phase, Succeeded: true})
		if err != nil {
			t.Fatal(err)
		}
	}
	if !state.Terminal || state.Outcome != coveragedomain.OutcomeAvailable || state.Reason != "" {
		t.Fatalf("successful report state = %#v", state)
	}
}

func TestStateMarksCrashAndMissingProfileAsPartial(t *testing.T) {
	state := NewState()
	var err error
	for _, phase := range []Phase{PhaseConfigure, PhaseBuild} {
		state, err = state.Apply(StepResult{Phase: phase, Succeeded: true})
		if err != nil {
			t.Fatal(err)
		}
	}
	state, err = state.Apply(StepResult{Phase: PhaseTest, Succeeded: false, Crash: true, ProfileMissing: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, phase := range []Phase{PhaseMerge, PhaseNormalize, PhaseReport, PhasePublish} {
		state, err = state.Apply(StepResult{Phase: phase, Succeeded: true})
		if err != nil {
			t.Fatal(err)
		}
	}
	if state.Outcome != coveragedomain.OutcomePartial || len(state.PartialReasons) != 2 {
		t.Fatalf("partial state = %#v", state)
	}
}

func TestStateFailsClosedForInfrastructureFailureCancellationAndBadOrder(t *testing.T) {
	state := NewState()
	if _, err := state.Apply(StepResult{Phase: PhaseBuild, Succeeded: true}); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("out-of-order error = %v", err)
	}
	configureFailure, err := state.Apply(StepResult{Phase: PhaseConfigure, Succeeded: false, InfrastructureFailure: true})
	if err != nil {
		t.Fatal(err)
	}
	if configureFailure.Reason != coveragedomain.ReasonInstrumentationFailed {
		t.Fatalf("configure failure reason = %q", configureFailure.Reason)
	}

	state = NewState()
	state, err = state.Apply(StepResult{Phase: PhaseConfigure, Succeeded: true})
	if err != nil {
		t.Fatal(err)
	}
	state, err = state.Apply(StepResult{Phase: PhaseBuild, Succeeded: false, InfrastructureFailure: true})
	if err != nil {
		t.Fatal(err)
	}
	if !state.Terminal || state.Outcome != coveragedomain.OutcomeUnavailable || state.Reason != coveragedomain.ReasonBuildFailed {
		t.Fatalf("infrastructure state = %#v", state)
	}
	if _, err := state.Apply(StepResult{Phase: PhaseConfigure, Succeeded: true}); !errors.Is(err, ErrTerminalState) {
		t.Fatalf("terminal transition error = %v", err)
	}

	state = NewState()
	state, err = state.Apply(StepResult{Phase: PhaseConfigure, Cancelled: true})
	if err != nil {
		t.Fatal(err)
	}
	if !state.Terminal || state.Outcome != coveragedomain.OutcomeCancelled || state.Reason != coveragedomain.ReasonUserCancelled {
		t.Fatalf("cancelled state = %#v", state)
	}
}
