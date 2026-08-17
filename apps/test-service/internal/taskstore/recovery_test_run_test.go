package taskstore

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"unit-test-ide.local/test-service/internal/artifactstore"
	"unit-test-ide.local/test-service/internal/task"
	"unit-test-ide.local/test-service/internal/testdomain"
)

func TestRecoverInterruptedTestRunPreservesResultsAndFillsSelection(
	t *testing.T,
) {
	ctx := context.Background()
	store, running, run, existing, recoveredAt :=
		runningRecoveryTestRun(t)

	events, err := store.RecoverInterrupted(ctx, recoveredAt)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 ||
		events[0].Type != task.EventTestRunFinished ||
		events[1].Type != task.EventTaskFinished ||
		events[0].Sequence >= events[1].Sequence {
		t.Fatalf("recovery events = %#v", events)
	}
	var payload struct {
		RunID          string                `json:"runId"`
		Outcome        testdomain.RunOutcome `json:"outcome"`
		Summary        testdomain.RunSummary `json:"summary"`
		ResultRevision string                `json:"resultRevision"`
		Incomplete     bool                  `json:"incomplete"`
	}
	if err := json.Unmarshal(events[0].Payload, &payload); err != nil {
		t.Fatal(err)
	}

	recovered, err := store.GetRun(ctx, run.RunID)
	if err != nil {
		t.Fatal(err)
	}
	wantSummary := testdomain.RunSummary{
		Total:      4,
		Completed:  1,
		Passed:     1,
		NotRun:     3,
		Iterations: 2,
	}
	if recovered.Status != testdomain.RunCompleted ||
		recovered.Outcome != testdomain.RunInterrupted ||
		recovered.FinishedAt == nil ||
		!recovered.FinishedAt.Equal(recoveredAt) ||
		!recovered.Incomplete ||
		!reflect.DeepEqual(recovered.Summary, wantSummary) ||
		len(recovered.Results) != 4 {
		t.Fatalf("recovered TestRun = %#v", recovered)
	}
	preserved := false
	for _, result := range recovered.Results {
		if result.ItemID == existing.ItemID &&
			result.Iteration == existing.Iteration {
			if !reflect.DeepEqual(result, existing) {
				t.Fatalf(
					"existing TestResult changed: %#v",
					result,
				)
			}
			preserved = true
			continue
		}
		if result.Outcome != testdomain.ItemNotRun ||
			result.Reason != testdomain.ReasonServiceRestarted ||
			result.Partial ||
			len(result.FailureDetails) != 0 ||
			len(result.OutputRefs) != 0 {
			t.Fatalf("recovered missing TestResult = %#v", result)
		}
	}
	if !preserved {
		t.Fatal("existing TestResult was not preserved")
	}
	if payload.RunID != run.RunID ||
		payload.Outcome != testdomain.RunInterrupted ||
		!reflect.DeepEqual(payload.Summary, recovered.Summary) ||
		payload.ResultRevision != recovered.ResultRevision ||
		!payload.Incomplete {
		t.Fatalf(
			"test.run.finished payload = %#v, run = %#v",
			payload,
			recovered,
		)
	}
	recoveredTask, err := store.Get(ctx, running.ID)
	if err != nil ||
		recoveredTask.Status != task.StatusFinished ||
		recoveredTask.Outcome != task.OutcomeInterrupted ||
		recoveredTask.LastSequence != events[1].Sequence {
		t.Fatalf("recovered Task = %#v, %v", recoveredTask, err)
	}
}

func TestRecoverInterruptedTestRunRollsBackEveryDomainWrite(
	t *testing.T,
) {
	ctx := context.Background()
	store, running, run, existing, recoveredAt :=
		runningRecoveryTestRun(t)
	beforeWatermark, err := store.Watermark(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`CREATE TRIGGER reject_recovered_test_run
		BEFORE UPDATE OF status ON test_runs
		WHEN NEW.status='completed'
		BEGIN
			SELECT RAISE(ABORT, 'injected recovery failure');
		END`); err != nil {
		t.Fatal(err)
	}

	if _, err := store.RecoverInterrupted(
		ctx,
		recoveredAt,
	); !errors.Is(err, task.ErrStorageUnavailable) {
		t.Fatalf(
			"RecoverInterrupted() error = %v, want storage error",
			err,
		)
	}
	persistedTask, err := store.Get(ctx, running.ID)
	if err != nil ||
		persistedTask.Status != task.StatusRunning ||
		persistedTask.Outcome != "" ||
		persistedTask.Steps[0].Status != task.StepRunning {
		t.Fatalf("Task after rollback = %#v, %v", persistedTask, err)
	}
	persistedRun, err := store.GetRun(ctx, run.RunID)
	if err != nil ||
		persistedRun.Status != testdomain.RunRunning ||
		persistedRun.Outcome != "" ||
		len(persistedRun.Results) != 1 ||
		!reflect.DeepEqual(persistedRun.Results[0], existing) {
		t.Fatalf("TestRun after rollback = %#v, %v", persistedRun, err)
	}
	afterWatermark, err := store.Watermark(ctx)
	if err != nil || afterWatermark != beforeWatermark {
		t.Fatalf(
			"watermark after rollback = %d, %v; want %d",
			afterWatermark,
			err,
			beforeWatermark,
		)
	}
	leases, err := store.ActiveLeases(ctx)
	if err != nil || len(leases) != 1 ||
		leases[0].TaskID != running.ID {
		t.Fatalf("leases after rollback = %#v, %v", leases, err)
	}
}

func runningRecoveryTestRun(
	t *testing.T,
) (
	*Store,
	task.Task,
	testdomain.TestRun,
	testdomain.TestItemResult,
	time.Time,
) {
	t.Helper()
	ctx := context.Background()
	store := openTestStore(t)
	root := t.TempDir()
	artifacts, err := artifactstore.New(filepath.Join(root, "artifacts"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = artifacts.Close() })
	catalog := catalogFixture(t, "8", "alpha", "beta")
	publishCatalogFixture(t, store, artifacts, catalog, 220)
	catalogOwner, err := store.Get(ctx, id(220))
	if err != nil {
		t.Fatal(err)
	}
	catalogOwnerFinishedAt := catalog.GeneratedAt.Add(time.Second)
	catalogOwner = mustTransition(t, catalogOwner, task.Transition{
		From:    task.StatusQueued,
		To:      task.StatusFinished,
		Outcome: task.OutcomeSucceeded,
		At:      catalogOwnerFinishedAt,
	})
	if _, _, err := store.Apply(ctx, task.Mutation{
		Task:     catalogOwner,
		Expected: task.StatusQueued,
		Events: []task.EventDraft{{
			TaskID: catalogOwner.ID,
			Type:   task.EventTaskFinished,
			At:     catalogOwnerFinishedAt,
			Payload: json.RawMessage(
				`{"outcome":"succeeded"}`,
			),
		}},
	}); err != nil {
		t.Fatal(err)
	}

	createdAt := catalog.GeneratedAt.Add(time.Minute)
	input := testRunTaskFixture(224, 225, createdAt)
	run := runFixture(input, 226)
	run.ProfileID = catalog.ProfileID
	run.CatalogRevision = catalog.Revision
	run.SelectionSnapshot = testdomain.SelectionSnapshot{
		Mode:         testdomain.SelectionContainers,
		ContainerIDs: []testdomain.ID{catalog.Containers[0].ID},
	}
	run.Summary.Iterations = 2
	created, _, err := store.CreateTestTask(
		ctx,
		input,
		[]task.StepSnapshot{{
			ID:     "test-000001",
			Kind:   task.StepTestRun,
			Status: task.StepPending,
		}},
		draft(input.ID, task.EventTaskCreated, createdAt),
		run,
	)
	if err != nil {
		t.Fatal(err)
	}
	startedAt := createdAt.Add(time.Second)
	running := startStoredStep(
		t,
		store,
		created,
		0,
		startedAt,
		3201,
	)
	if err := store.StartRun(ctx, run.RunID, startedAt); err != nil {
		t.Fatal(err)
	}
	existing := resultFixture(
		catalog.Items[0].ID,
		catalog.Containers[0].ID,
	)
	if err := store.AppendResult(
		ctx,
		run.RunID,
		existing,
	); err != nil {
		t.Fatal(err)
	}
	return store, running, run, existing, startedAt.Add(time.Minute)
}
