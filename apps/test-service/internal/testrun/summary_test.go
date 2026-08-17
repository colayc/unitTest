package testrun

import (
	"errors"
	"reflect"
	"testing"

	"unit-test-ide.local/test-service/internal/testdomain"
)

func TestSummarizeAggregatesEveryIterationWithoutMutatingResults(t *testing.T) {
	results := []testdomain.TestItemResult{
		summaryResult("1", 1, testdomain.ItemPassed),
		summaryResult("1", 2, testdomain.ItemFailed),
		summaryResult("2", 1, testdomain.ItemSkipped),
		summaryResult("2", 2, testdomain.ItemErrored),
		summaryResult("3", 1, testdomain.ItemCancelled),
		summaryResult("3", 2, testdomain.ItemTimedOut),
		summaryResult("4", 1, testdomain.ItemNotRun),
	}
	results[6].Reason = testdomain.ReasonContainerTerminated
	results[3].Partial = true
	before := make([]testdomain.TestItemResult, len(results))
	for index := range results {
		before[index] = results[index].Clone()
	}

	summary, incomplete, err := Summarize(results, 2)
	if err != nil {
		t.Fatal(err)
	}
	want := testdomain.RunSummary{
		Total: 7, Completed: 6, Passed: 1, Failed: 1, Skipped: 1,
		Errored: 1, Cancelled: 1, TimedOut: 1, NotRun: 1, Iterations: 2,
	}
	if !reflect.DeepEqual(summary, want) || !incomplete {
		t.Fatalf("Summarize() = %#v, %v", summary, incomplete)
	}
	if !reflect.DeepEqual(results, before) {
		t.Fatal("Summarize mutated results")
	}
}

func TestSummarizeRejectsInvalidRepeatDuplicateAndIteration(t *testing.T) {
	valid := summaryResult("1", 1, testdomain.ItemPassed)
	tests := []struct {
		name    string
		results []testdomain.TestItemResult
		repeat  int64
	}{
		{name: "zero repeat", repeat: 0},
		{name: "excess repeat", repeat: 101},
		{
			name:    "duplicate tuple",
			results: []testdomain.TestItemResult{valid, valid},
			repeat:  1,
		},
		{
			name: "iteration beyond repeat",
			results: []testdomain.TestItemResult{
				summaryResult("1", 2, testdomain.ItemPassed),
			},
			repeat: 1,
		},
		{
			name: "invalid result",
			results: []testdomain.TestItemResult{
				summaryResult("1", 1, testdomain.ItemOutcome("unknown")),
			},
			repeat: 1,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, _, err := Summarize(
				test.results,
				test.repeat,
			); !errors.Is(err, testdomain.ErrInvalidResult) {
				t.Fatalf("Summarize() error = %v", err)
			}
		})
	}
}

func TestSummarizeEmptyRunPreservesRepeatCount(t *testing.T) {
	summary, incomplete, err := Summarize(nil, 100)
	if err != nil || summary != (testdomain.RunSummary{Iterations: 100}) ||
		incomplete {
		t.Fatalf("Summarize(nil) = %#v, %v, %v", summary, incomplete, err)
	}
}

func summaryResult(
	itemCharacter string,
	iteration int64,
	outcome testdomain.ItemOutcome,
) testdomain.TestItemResult {
	return testdomain.TestItemResult{
		ItemID: stableTestID(itemCharacter), ContainerID: stableTestID("a"),
		Iteration: iteration, Outcome: outcome,
		FailureDetails: []testdomain.FailureDetail{}, OutputRefs: []string{},
	}
}
