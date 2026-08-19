package coveragerun

import (
	"errors"
	"reflect"
	"testing"

	"unit-test-ide.local/test-service/internal/coveragedomain"
)

func TestFinalizeCompletenessCanonicalizesPartialReasons(t *testing.T) {
	state := State{
		Terminal: true,
		Outcome:  coveragedomain.OutcomePartial,
		PartialReasons: []coveragedomain.CompletenessReason{
			coveragedomain.CompletenessReasonProfileMissingForFailedInvocation,
			coveragedomain.CompletenessReasonTestCrashed,
		},
	}
	completeness, err := FinalizeCompleteness(state)
	if err != nil {
		t.Fatal(err)
	}
	want := []coveragedomain.CompletenessReason{
		coveragedomain.CompletenessReasonProfileMissingForFailedInvocation,
		coveragedomain.CompletenessReasonTestCrashed,
	}
	if completeness.Outcome != coveragedomain.OutcomePartial || !reflect.DeepEqual(completeness.Reasons, want) {
		t.Fatalf("completeness = %#v", completeness)
	}
	state.PartialReasons[0] = coveragedomain.CompletenessReasonTestTimedOut
	if completeness.Reasons[0] != want[0] {
		t.Fatal("completeness aliases state reasons")
	}
}

func TestFinalizeCompletenessRejectsNonterminalInvalidAndUnavailableStates(t *testing.T) {
	for name, state := range map[string]State{
		"nonterminal":             {Outcome: coveragedomain.OutcomeAvailable},
		"available with reasons":  {Terminal: true, Outcome: coveragedomain.OutcomeAvailable, PartialReasons: []coveragedomain.CompletenessReason{coveragedomain.CompletenessReasonTestCrashed}},
		"partial without reasons": {Terminal: true, Outcome: coveragedomain.OutcomePartial},
		"unknown reason":          {Terminal: true, Outcome: coveragedomain.OutcomePartial, PartialReasons: []coveragedomain.CompletenessReason{"unknown"}},
		"unavailable":             {Terminal: true, Outcome: coveragedomain.OutcomeUnavailable},
		"cancelled":               {Terminal: true, Outcome: coveragedomain.OutcomeCancelled},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := FinalizeCompleteness(state); !errors.Is(err, ErrCompletenessUnavailable) {
				t.Fatalf("error = %v", err)
			}
		})
	}
}
