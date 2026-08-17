package task_test

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"unit-test-ide.local/test-service/internal/task"
	"unit-test-ide.local/test-service/internal/testdomain"
)

func TestManagerCommitsDomainCompletionBeforePublishingRunFinished(
	t *testing.T,
) {
	f := newManagerFixture(t)
	interpreter := &domainCompletionInterpreter{
		runID: strings.Repeat("1", 32),
	}
	request := oneStepBuildRequest(testID(231))
	request.Kind = task.KindTestRun
	request.TestRun = &testdomain.TestRun{
		RunID:           interpreter.runID,
		IdempotencyKey:  request.IdempotencyKey,
		ProjectID:       "core",
		ProfileID:       strings.Repeat("2", 64),
		ToolchainID:     "msvc",
		CatalogRevision: strings.Repeat("3", 64),
		SelectionSnapshot: testdomain.SelectionSnapshot{
			Mode: testdomain.SelectionItems,
			ItemIDs: []testdomain.ID{
				testdomain.ID(
					"utid-v1-" + strings.Repeat("4", 64),
				),
			},
		},
		Status:         testdomain.RunQueued,
		Summary:        testdomain.RunSummary{Iterations: 1},
		ResultRevision: testdomain.EmptyResultRevision(),
		Incomplete:     true,
	}
	request.ResultInterpreter = interpreter
	started, err := f.manager.Start(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	f.process.complete(task.ProcessResult{})
	finished := f.awaitTask(t, started.ID, task.StatusFinished)
	if finished.Outcome != task.OutcomeSucceeded {
		t.Fatalf("finished Task = %#v", finished)
	}
	mutation := f.store.lastMutation()
	if mutation.FinishRun == nil ||
		mutation.FinishRun.Status != testdomain.RunCompleted ||
		mutation.FinishRun.Outcome != testdomain.RunErrored {
		t.Fatalf("terminal TestRun mutation = %#v", mutation.FinishRun)
	}
	eventTypes := make([]task.EventType, len(mutation.Events))
	for index, event := range mutation.Events {
		eventTypes[index] = event.Type
	}
	wantTail := []task.EventType{
		task.EventTestRunFinished,
		task.EventTaskFinished,
	}
	if len(eventTypes) < len(wantTail) ||
		!reflect.DeepEqual(
			eventTypes[len(eventTypes)-len(wantTail):],
			wantTail,
		) {
		t.Fatalf("terminal event order = %#v", eventTypes)
	}
	for index := range len(eventTypes) - len(wantTail) {
		if eventTypes[index] != task.EventArtifactCreated {
			t.Fatalf("event %d = %q", index, eventTypes[index])
		}
	}
	persistedRun := f.store.testRun(interpreter.runID)
	if persistedRun.Status != testdomain.RunCompleted {
		t.Fatalf(
			"test.run.finished published before durable TestRun: %#v",
			persistedRun,
		)
	}
}

type domainCompletionInterpreter struct {
	runID string
}

func (*domainCompletionInterpreter) Interpret(
	context.Context,
	task.Task,
	task.ExecutionStep,
	task.ProcessResult,
) (task.StepVerdict, error) {
	return task.StepVerdictSucceeded, nil
}

func (interpreter *domainCompletionInterpreter) PrepareCompletion(
	ctx context.Context,
	current task.Task,
	finishedAt time.Time,
	_ task.Outcome,
	sink task.ArtifactSink,
	newID task.IDGenerator,
) (task.DomainCompletion, error) {
	if err := sink.CommitJSON(
		ctx,
		newID(),
		"test-selection",
		map[string]any{"mode": "items"},
	); err != nil {
		return task.DomainCompletion{}, err
	}
	if err := sink.CommitJSONLines(
		ctx,
		newID(),
		"test-results",
		[]json.RawMessage{},
	); err != nil {
		return task.DomainCompletion{}, err
	}
	if err := sink.CommitJSON(
		ctx,
		newID(),
		"test-run-summary",
		map[string]any{"outcome": "errored"},
	); err != nil {
		return task.DomainCompletion{}, err
	}
	run := testdomain.TestRun{
		RunID:           interpreter.runID,
		TaskID:          current.ID,
		IdempotencyKey:  current.IdempotencyKey,
		ProjectID:       "core",
		ProfileID:       strings.Repeat("2", 64),
		ToolchainID:     "msvc",
		CatalogRevision: strings.Repeat("3", 64),
		SelectionSnapshot: testdomain.SelectionSnapshot{
			Mode: testdomain.SelectionItems,
			ItemIDs: []testdomain.ID{
				testdomain.ID(
					"utid-v1-" + strings.Repeat("4", 64),
				),
			},
		},
		Status:         testdomain.RunCompleted,
		Outcome:        testdomain.RunErrored,
		StartedAt:      current.StartedAt,
		FinishedAt:     &finishedAt,
		Summary:        testdomain.RunSummary{Iterations: 1},
		ResultRevision: testdomain.EmptyResultRevision(),
		Incomplete:     true,
		CreatedAt:      current.CreatedAt,
	}
	eventJSON, err := json.Marshal(map[string]any{
		"runId":   interpreter.runID,
		"outcome": testdomain.RunErrored,
	})
	if err != nil {
		return task.DomainCompletion{}, err
	}
	return task.DomainCompletion{
		TestRun: &run,
		Events: []task.DomainEvent{{
			Type:    task.EventTestRunFinished,
			Payload: eventJSON,
		}},
	}, nil
}
