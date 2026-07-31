package testrun

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"unit-test-ide.local/test-service/internal/testdomain"
)

func TestResolveFailedFromRunKeepsAssertionsAndWidensContainerFailures(t *testing.T) {
	catalog, ids := retryCatalog()
	runID := strings.Repeat("a", 32)
	previous := retryRun(runID, catalog, []testdomain.TestItemResult{
		retryResult(ids.assertionItem, ids.assertionContainer, testdomain.ItemFailed,
			"assertion_failure", false),
		retryResult(ids.crashedItem, ids.crashedContainer, testdomain.ItemErrored,
			"test_process_crash", true),
		retryResult(ids.skippedItem, ids.skippedContainer, testdomain.ItemSkipped,
			"", false),
		retryResult(ids.cancelledItem, ids.skippedContainer, testdomain.ItemCancelled,
			"cancelled", false),
	})
	reader := &fakeRunReader{run: previous}
	snapshot, err := Resolve(
		context.Background(),
		catalog,
		testdomain.Selection{
			Mode:  testdomain.SelectionFailedFromRun,
			RunID: runID,
		},
		reader,
		testdomain.Limits{MaxSelectionSize: 100},
	)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Mode != testdomain.SelectionFailedFromRun ||
		snapshot.SourceRunID != runID ||
		!reflect.DeepEqual(
			snapshot.ItemIDs,
			[]testdomain.ID{ids.assertionItem},
		) ||
		!reflect.DeepEqual(
			snapshot.ContainerIDs,
			[]testdomain.ID{ids.crashedContainer},
		) {
		t.Fatalf("failedFromRun snapshot = %#v", snapshot)
	}
	snapshot.ItemIDs[0] = stableTestID("f")
	if previous.Results[0].ItemID != ids.assertionItem {
		t.Fatal("Resolve mutated persisted TestRun results")
	}
}

func TestResolveFailedFromRunContainerScopeSupersedesCaseScope(t *testing.T) {
	catalog, ids := retryCatalog()
	runID := strings.Repeat("b", 32)
	previous := retryRun(runID, catalog, []testdomain.TestItemResult{
		retryResult(ids.assertionItem, ids.assertionContainer, testdomain.ItemFailed,
			"assertion_failure", false),
		retryResult(ids.secondAssertionItem, ids.assertionContainer, testdomain.ItemTimedOut,
			"test_timeout", false),
	})
	snapshot, err := Resolve(
		context.Background(),
		catalog,
		testdomain.Selection{
			Mode:  testdomain.SelectionFailedFromRun,
			RunID: runID,
		},
		&fakeRunReader{run: previous},
		testdomain.Limits{MaxSelectionSize: 100},
	)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(
		snapshot.ContainerIDs,
		[]testdomain.ID{ids.assertionContainer},
	) || len(snapshot.ItemIDs) != 0 {
		t.Fatalf("superseding container snapshot = %#v", snapshot)
	}
}

func TestResolveFailedFromRunRejectsStaleNonterminalAndEmptyHistory(t *testing.T) {
	catalog, ids := retryCatalog()
	runID := strings.Repeat("c", 32)
	assertion := retryResult(
		ids.assertionItem,
		ids.assertionContainer,
		testdomain.ItemFailed,
		"assertion_failure",
		false,
	)
	tests := []struct {
		name   string
		mutate func(*testdomain.Catalog, *testdomain.TestRun)
		want   error
	}{
		{
			name: "deleted exact item",
			mutate: func(catalog *testdomain.Catalog, _ *testdomain.TestRun) {
				catalog.Items = catalog.Items[1:]
			},
			want: testdomain.ErrUnknownSelectionID,
		},
		{
			name: "nonterminal run",
			mutate: func(_ *testdomain.Catalog, run *testdomain.TestRun) {
				run.Status = testdomain.RunRunning
			},
			want: testdomain.ErrInvalidSelection,
		},
		{
			name: "different project",
			mutate: func(catalog *testdomain.Catalog, _ *testdomain.TestRun) {
				catalog.ProjectID = "other"
			},
			want: testdomain.ErrCatalogStale,
		},
		{
			name: "cancelled and skipped are empty",
			mutate: func(_ *testdomain.Catalog, run *testdomain.TestRun) {
				run.Results = []testdomain.TestItemResult{
					retryResult(ids.assertionItem, ids.assertionContainer,
						testdomain.ItemCancelled, "cancelled", false),
					retryResult(ids.crashedItem, ids.crashedContainer,
						testdomain.ItemSkipped, "", false),
				}
			},
			want: testdomain.ErrEmptySelection,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			currentCatalog := catalog
			currentCatalog.Containers = append(
				[]testdomain.Container(nil),
				catalog.Containers...,
			)
			currentCatalog.Items = append([]testdomain.Item(nil), catalog.Items...)
			run := retryRun(runID, catalog, []testdomain.TestItemResult{assertion})
			test.mutate(&currentCatalog, &run)
			_, err := Resolve(
				context.Background(),
				currentCatalog,
				testdomain.Selection{
					Mode:  testdomain.SelectionFailedFromRun,
					RunID: runID,
				},
				&fakeRunReader{run: run},
				testdomain.Limits{MaxSelectionSize: 100},
			)
			if !errors.Is(err, test.want) {
				t.Fatalf("Resolve() error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestResolveFailedFromRunPropagatesReaderAndLimitFailures(t *testing.T) {
	catalog, ids := retryCatalog()
	runID := strings.Repeat("d", 32)
	readErr := errors.New("history unavailable")
	if _, err := Resolve(
		context.Background(),
		catalog,
		testdomain.Selection{
			Mode:  testdomain.SelectionFailedFromRun,
			RunID: runID,
		},
		&fakeRunReader{err: readErr},
		testdomain.Limits{MaxSelectionSize: 100},
	); !errors.Is(err, readErr) {
		t.Fatalf("reader error = %v", err)
	}
	previous := retryRun(runID, catalog, []testdomain.TestItemResult{
		retryResult(ids.assertionItem, ids.assertionContainer, testdomain.ItemFailed,
			"assertion_failure", false),
		retryResult(ids.crashedItem, ids.crashedContainer, testdomain.ItemErrored,
			"framework_output_invalid", false),
	})
	if _, err := Resolve(
		context.Background(),
		catalog,
		testdomain.Selection{
			Mode:  testdomain.SelectionFailedFromRun,
			RunID: runID,
		},
		&fakeRunReader{run: previous},
		testdomain.Limits{MaxSelectionSize: 1},
	); !errors.Is(err, testdomain.ErrSelectionTooLarge) {
		t.Fatalf("retry selection limit error = %v", err)
	}
}

type fakeRunReader struct {
	run   testdomain.TestRun
	err   error
	calls int
}

func (reader *fakeRunReader) GetRun(
	_ context.Context,
	_ string,
) (testdomain.TestRun, error) {
	reader.calls++
	return reader.run.Clone(), reader.err
}

type retryIDs struct {
	assertionContainer, crashedContainer, skippedContainer testdomain.ID
	assertionItem, secondAssertionItem                     testdomain.ID
	crashedItem, skippedItem, cancelledItem                testdomain.ID
}

func retryCatalog() (testdomain.Catalog, retryIDs) {
	ids := retryIDs{
		assertionContainer: stableTestID("a"),
		crashedContainer:   stableTestID("b"),
		skippedContainer:   stableTestID("c"),
		assertionItem:      stableTestID("1"),
		secondAssertionItem: stableTestID(
			"2",
		),
		crashedItem:   stableTestID("3"),
		skippedItem:   stableTestID("4"),
		cancelledItem: stableTestID("5"),
	}
	catalog := testdomain.Catalog{
		ProjectID: "core",
		ProfileID: strings.Repeat("6", 64),
		Revision:  strings.Repeat("7", 64),
		Containers: []testdomain.Container{
			{ID: ids.assertionContainer, ProjectID: "core"},
			{ID: ids.crashedContainer, ProjectID: "core"},
			{ID: ids.skippedContainer, ProjectID: "core"},
		},
		Items: []testdomain.Item{
			{ID: ids.assertionItem, ContainerID: ids.assertionContainer, Kind: testdomain.ItemCase},
			{ID: ids.secondAssertionItem, ContainerID: ids.assertionContainer, Kind: testdomain.ItemCase},
			{ID: ids.crashedItem, ContainerID: ids.crashedContainer, Kind: testdomain.ItemCase},
			{ID: ids.skippedItem, ContainerID: ids.skippedContainer, Kind: testdomain.ItemCase},
			{ID: ids.cancelledItem, ContainerID: ids.skippedContainer, Kind: testdomain.ItemCase},
		},
	}
	return catalog, ids
}

func retryRun(
	runID string,
	catalog testdomain.Catalog,
	results []testdomain.TestItemResult,
) testdomain.TestRun {
	return testdomain.TestRun{
		RunID: runID, ProjectID: catalog.ProjectID, ProfileID: catalog.ProfileID,
		CatalogRevision: catalog.Revision, Status: testdomain.RunCompleted,
		Results: append([]testdomain.TestItemResult(nil), results...),
	}
}

func retryResult(
	itemID testdomain.ID,
	containerID testdomain.ID,
	outcome testdomain.ItemOutcome,
	category string,
	partial bool,
) testdomain.TestItemResult {
	result := testdomain.TestItemResult{
		ItemID: itemID, ContainerID: containerID, Iteration: 1,
		Outcome: outcome, Partial: partial,
	}
	if category != "" {
		result.FailureDetails = []testdomain.FailureDetail{{
			Category: category,
			Message:  "failure",
		}}
	}
	return result
}
