package testrun

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"unit-test-ide.local/test-service/internal/diagnostic"
	"unit-test-ide.local/test-service/internal/task"
	"unit-test-ide.local/test-service/internal/testdomain"
)

func TestPrepareCompletionProjectsRunArtifactsAndTerminalEvent(
	t *testing.T,
) {
	createdAt := time.Date(2026, 7, 31, 10, 0, 0, 0, time.UTC)
	startedAt := createdAt.Add(time.Second)
	finishedAt := startedAt.Add(time.Second)
	itemID := stableTestID("1")
	containerID := stableTestID("2")
	result := testdomain.TestItemResult{
		ItemID:         itemID,
		ContainerID:    containerID,
		Iteration:      1,
		Outcome:        testdomain.ItemPassed,
		FailureDetails: []testdomain.FailureDetail{},
		OutputRefs:     []string{},
	}
	revision, err := testdomain.ResultRevision(
		[]testdomain.TestItemResult{result},
	)
	if err != nil {
		t.Fatal(err)
	}
	run := testdomain.TestRun{
		RunID:           strings.Repeat("1", 32),
		TaskID:          strings.Repeat("2", 32),
		IdempotencyKey:  strings.Repeat("3", 32),
		ProjectID:       "core",
		ProfileID:       strings.Repeat("4", 64),
		ToolchainID:     "msvc",
		CatalogRevision: strings.Repeat("5", 64),
		SelectionSnapshot: testdomain.SelectionSnapshot{
			Mode:    testdomain.SelectionItems,
			ItemIDs: []testdomain.ID{itemID},
		},
		Status:         testdomain.RunQueued,
		Summary:        testdomain.RunSummary{Iterations: 1},
		ResultRevision: revision,
		Incomplete:     true,
		CreatedAt:      createdAt,
		Results:        []testdomain.TestItemResult{result},
	}
	execution := &runExecution{
		runID: run.RunID,
		runs: &coordinatorRunStore{
			run: run,
		},
		expectedResults: 1,
	}
	sink := &completionArtifactSink{
		json:  make(map[string]any),
		lines: make(map[string][]json.RawMessage),
	}
	next := 0
	completion, err := execution.PrepareCompletion(
		context.Background(),
		task.Task{
			ID:        run.TaskID,
			Kind:      task.KindTestRun,
			StartedAt: &startedAt,
		},
		finishedAt,
		task.OutcomeSucceeded,
		sink,
		func() string {
			next++
			return fmt.Sprintf("%032x", next)
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if completion.TestRun == nil ||
		completion.TestRun.Status != testdomain.RunCompleted ||
		completion.TestRun.Outcome != testdomain.RunPassed ||
		completion.TestRun.Incomplete ||
		completion.TestRun.Summary.Passed != 1 ||
		len(completion.Events) != 1 ||
		completion.Events[0].Type != task.EventTestRunFinished {
		t.Fatalf("completion = %#v", completion)
	}
	if len(sink.json) != 2 ||
		sink.json["test-selection"] == nil ||
		sink.json["test-run-summary"] == nil ||
		len(sink.lines["test-results"]) != 1 {
		t.Fatalf(
			"domain artifacts = json:%#v lines:%#v",
			sink.json,
			sink.lines,
		)
	}
}

type completionArtifactSink struct {
	json  map[string]any
	lines map[string][]json.RawMessage
	err   error
}

func (*completionArtifactSink) AppendOutput(
	context.Context,
	string,
	string,
	[]byte,
) error {
	return nil
}

func (*completionArtifactSink) AppendDiagnostic(
	context.Context,
	diagnostic.Diagnostic,
) error {
	return nil
}

func (sink *completionArtifactSink) CommitJSON(
	_ context.Context,
	_ string,
	kind string,
	value any,
) error {
	if sink.err != nil {
		return sink.err
	}
	sink.json[kind] = value
	return nil
}

func (sink *completionArtifactSink) CommitJSONLines(
	_ context.Context,
	_ string,
	kind string,
	values []json.RawMessage,
) error {
	if sink.err != nil {
		return sink.err
	}
	sink.lines[kind] = append(
		[]json.RawMessage(nil),
		values...,
	)
	return nil
}

func (*completionArtifactSink) Finalize(
	context.Context,
	time.Time,
) ([]task.Artifact, error) {
	return nil, nil
}

func (*completionArtifactSink) Abort(context.Context) error {
	return nil
}
