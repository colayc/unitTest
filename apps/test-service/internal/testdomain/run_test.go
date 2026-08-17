package testdomain

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestTestRunDefensivelyCopiesSelectionResultsAndTimes(t *testing.T) {
	created := time.Date(2026, 7, 31, 4, 0, 0, 0, time.UTC)
	duration := int64(12)
	item := TestItemResult{
		ItemID:      runStableID("1"),
		ContainerID: runStableID("2"),
		Iteration:   1,
		Outcome:     ItemFailed,
		DurationMS:  &duration,
		FailureDetails: []FailureDetail{{
			Category: "assertion_failure",
			Message:  "expected true",
			Locations: []SourceLocation{{
				URI:        "file:///workspace/test.c",
				Line:       8,
				Navigable:  true,
				Provenance: "framework-output",
			}},
			EvidenceRefs: []string{strings.Repeat("e", 32)},
		}},
		OutputRefs: []string{strings.Repeat("f", 32)},
	}
	revision, err := ResultRevision([]TestItemResult{item})
	if err != nil {
		t.Fatal(err)
	}
	run, err := NewTestRun(TestRun{
		RunID:           strings.Repeat("a", 32),
		TaskID:          strings.Repeat("b", 32),
		IdempotencyKey:  strings.Repeat("c", 32),
		ProjectID:       "core",
		ProfileID:       strings.Repeat("d", 64),
		ToolchainID:     "clang-linux",
		CatalogRevision: strings.Repeat("e", 64),
		SelectionSnapshot: SelectionSnapshot{
			Mode:    SelectionItems,
			ItemIDs: []ID{item.ItemID},
		},
		Status: RunQueued,
		Summary: RunSummary{
			Iterations: 1,
		},
		ResultRevision: revision,
		Incomplete:     true,
		CreatedAt:      created,
		Results:        []TestItemResult{item},
	})
	if err != nil {
		t.Fatal(err)
	}

	item.FailureDetails[0].Locations[0].Line = 99
	item.OutputRefs[0] = strings.Repeat("0", 32)
	cloned := run.Clone()
	cloned.SelectionSnapshot.ItemIDs[0] = runStableID("3")
	cloned.Results[0].FailureDetails[0].Locations[0].Line = 100

	if run.SelectionSnapshot.ItemIDs[0] != runStableID("1") ||
		run.Results[0].FailureDetails[0].Locations[0].Line != 8 ||
		run.Results[0].OutputRefs[0] != strings.Repeat("f", 32) {
		t.Fatalf("TestRun retained mutable input: %#v", run)
	}
}

func TestResultRevisionIsOrderIndependentAndRejectsInvalidResult(t *testing.T) {
	first := TestItemResult{
		ItemID: runStableID("1"), ContainerID: runStableID("a"),
		Iteration: 1, Outcome: ItemPassed,
		FailureDetails: []FailureDetail{}, OutputRefs: []string{},
	}
	second := TestItemResult{
		ItemID: runStableID("2"), ContainerID: runStableID("a"),
		Iteration: 1, Outcome: ItemSkipped,
		FailureDetails: []FailureDetail{}, OutputRefs: []string{},
	}
	forward, err := ResultRevision([]TestItemResult{first, second})
	if err != nil {
		t.Fatal(err)
	}
	reverse, err := ResultRevision([]TestItemResult{second, first})
	if err != nil {
		t.Fatal(err)
	}
	if forward != reverse {
		t.Fatalf("revision depends on input order: %q != %q", forward, reverse)
	}
	first.Iteration = 0
	if _, err := ResultRevision([]TestItemResult{first}); !errors.Is(
		err,
		ErrInvalidResult,
	) {
		t.Fatalf("invalid result revision error = %v", err)
	}
}

func TestTestRunRejectsNonCanonicalSelectionAndInconsistentSummary(t *testing.T) {
	input := validRunInput()
	input.SelectionSnapshot.ItemIDs = []ID{
		runStableID("2"),
		runStableID("1"),
	}
	if _, err := NewTestRun(input); !errors.Is(err, ErrInvalidSelection) {
		t.Fatalf("unsorted selection error = %v", err)
	}

	input = validRunInput()
	input.Summary = RunSummary{
		Total: 2, Completed: 1, Passed: 1, Iterations: 1,
	}
	if _, err := NewTestRun(input); !errors.Is(err, ErrInvalidResult) {
		t.Fatalf("inconsistent summary error = %v", err)
	}
}

func TestTestRunJSONUsesArraysForEmptySnapshotAndResultCollections(t *testing.T) {
	run, err := NewTestRun(validRunInput())
	if err != nil {
		t.Fatal(err)
	}
	selectionJSON, err := json.Marshal(run.SelectionSnapshot)
	if err != nil {
		t.Fatal(err)
	}
	if string(selectionJSON) != `{"mode":"items","containerIds":[],"itemIds":["`+
		runStableID("1").String()+`"]}` {
		t.Fatalf("selection JSON = %s", selectionJSON)
	}
	result, err := NewTestItemResult(TestItemResult{
		ItemID: runStableID("1"), ContainerID: runStableID("2"),
		Iteration: 1, Outcome: ItemPassed,
	})
	if err != nil {
		t.Fatal(err)
	}
	resultJSON, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(resultJSON), `"failureDetails":[]`) ||
		!strings.Contains(string(resultJSON), `"outputRefs":[]`) {
		t.Fatalf("result JSON = %s", resultJSON)
	}
}

func TestTestRunClonePreservesUnloadedResults(t *testing.T) {
	run := validRunInput()
	run.ResultRevision = strings.Repeat("a", 64)
	run.Results = nil
	cloned := run.Clone()
	if cloned.Results != nil {
		t.Fatalf(
			"Clone() changed unloaded Results to %#v",
			cloned.Results,
		)
	}
}

func TestTestItemResultRequiresNotRunReasonAndUniqueArtifacts(t *testing.T) {
	input := TestItemResult{
		ItemID: runStableID("1"), ContainerID: runStableID("2"),
		Iteration: 1, Outcome: ItemNotRun,
		FailureDetails: []FailureDetail{}, OutputRefs: []string{},
	}
	if _, err := NewTestItemResult(input); !errors.Is(err, ErrInvalidResult) {
		t.Fatalf("missing not_run reason error = %v", err)
	}
	input.Reason = ReasonBuildBlocked
	input.OutputRefs = []string{
		strings.Repeat("a", 32),
		strings.Repeat("a", 32),
	}
	if _, err := NewTestItemResult(input); !errors.Is(
		err,
		ErrDuplicateIdentity,
	) {
		t.Fatalf("duplicate output refs error = %v", err)
	}
	input.OutputRefs = nil
	got, err := NewTestItemResult(input)
	if err != nil || got.Reason != ReasonBuildBlocked ||
		!reflect.DeepEqual(got.FailureDetails, []FailureDetail{}) {
		t.Fatalf("valid not_run result = %#v, %v", got, err)
	}
}

func validRunInput() TestRun {
	return TestRun{
		RunID:           strings.Repeat("a", 32),
		TaskID:          strings.Repeat("b", 32),
		IdempotencyKey:  strings.Repeat("c", 32),
		ProjectID:       "core",
		ProfileID:       strings.Repeat("d", 64),
		ToolchainID:     "gcc-linux",
		CatalogRevision: strings.Repeat("e", 64),
		SelectionSnapshot: SelectionSnapshot{
			Mode:    SelectionItems,
			ItemIDs: []ID{runStableID("1")},
		},
		Status:         RunQueued,
		Summary:        RunSummary{Iterations: 1},
		ResultRevision: EmptyResultRevision(),
		Incomplete:     true,
		CreatedAt:      time.Date(2026, 7, 31, 5, 0, 0, 0, time.UTC),
	}
}

func runStableID(character string) ID {
	return ID("utid-v1-" + strings.Repeat(character, 64))
}
