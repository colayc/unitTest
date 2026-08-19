package coveragerun

import (
	"context"
	"errors"
	"testing"

	"unit-test-ide.local/test-service/internal/processcontrol"
)

func TestMapProcessResultKeepsTestAssertionFailureCollectable(t *testing.T) {
	result, err := MapProcessResult(PhaseTest, ProcessEvidence{
		Result:         processcontrol.Result{ExitCode: 7},
		ProfileMissing: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Succeeded || !result.AssertionFailure || result.InfrastructureFailure || !result.ProfileMissing {
		t.Fatalf("mapped assertion result = %#v", result)
	}
}

func TestMapProcessResultDistinguishesTimeoutCancellationAndInfrastructure(t *testing.T) {
	tests := []struct {
		name          string
		err           error
		child         processcontrol.ChildResult
		wantCancelled bool
		wantTimedOut  bool
		wantInfra     bool
	}{
		{name: "cancelled", err: context.Canceled, wantCancelled: true},
		{name: "deadline", err: context.DeadlineExceeded, wantTimedOut: true},
		{name: "child cancelled", child: processcontrol.ChildResult{Err: context.Canceled}, wantCancelled: true},
		{name: "child deadline", child: processcontrol.ChildResult{Err: context.DeadlineExceeded}, wantTimedOut: true},
		{name: "child timeout", child: processcontrol.ChildResult{TimedOut: true}, wantTimedOut: true},
		{name: "host failure", err: errors.New("host unavailable"), wantInfra: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			mapped, err := MapProcessResult(PhaseTest, ProcessEvidence{
				Result: processcontrol.Result{Err: test.err, Children: []processcontrol.ChildResult{test.child}},
			})
			if err != nil {
				t.Fatal(err)
			}
			if mapped.Cancelled != test.wantCancelled || mapped.TimedOut != test.wantTimedOut || mapped.InfrastructureFailure != test.wantInfra {
				t.Fatalf("mapped = %#v", mapped)
			}
		})
	}
}

func TestMapProcessResultTreatsBuildExitAsInfrastructureAndRejectsOtherPhases(t *testing.T) {
	mapped, err := MapProcessResult(PhaseBuild, ProcessEvidence{Result: processcontrol.Result{ExitCode: 1}})
	if err != nil {
		t.Fatal(err)
	}
	if mapped.Succeeded || !mapped.InfrastructureFailure || mapped.AssertionFailure {
		t.Fatalf("mapped build result = %#v", mapped)
	}
	if _, err := MapProcessResult(PhaseMerge, ProcessEvidence{}); !errors.Is(err, ErrInvalidProcessPhase) {
		t.Fatalf("invalid phase error = %v", err)
	}
}
