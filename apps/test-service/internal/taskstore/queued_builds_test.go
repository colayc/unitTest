package taskstore

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"unit-test-ide.local/test-service/internal/artifactstore"
	"unit-test-ide.local/test-service/internal/task"
	"unit-test-ide.local/test-service/internal/testdomain"
)

func TestReplaceQueuedPlanChangesPendingStepsWithoutCreatingEvents(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	at := time.Date(2026, 7, 29, 5, 0, 0, 0, time.UTC)
	input := newCMakeTask(21, 31, at)
	created, _, err := store.Create(ctx, input, []task.StepSnapshot{
		{ID: "configure", Kind: task.StepConfigure, Status: task.StepPending},
		{ID: "build", Kind: task.StepBuild, Status: task.StepPending},
	}, draft(input.ID, task.EventTaskCreated, at))
	if err != nil {
		t.Fatal(err)
	}
	before, err := store.Watermark(ctx)
	if err != nil {
		t.Fatal(err)
	}
	fingerprint := strings.Repeat("e", 64)
	replaced, err := store.ReplaceQueuedPlan(
		ctx, created.ID, created.RequestHash, fingerprint,
		[]task.StepSnapshot{{ID: "build", Kind: task.StepBuild, Status: task.StepPending}},
	)
	if err != nil {
		t.Fatal(err)
	}
	after, err := store.Watermark(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if replaced.PlanFingerprint != fingerprint || len(replaced.Steps) != 1 ||
		replaced.Steps[0].ID != "build" || replaced.LastSequence != created.LastSequence ||
		after != before {
		t.Fatalf("replaced queued task = %#v, watermark %d -> %d", replaced, before, after)
	}
	if _, err := store.ReplaceQueuedPlan(
		ctx, created.ID, "wrong-request", fingerprint,
		[]task.StepSnapshot{{ID: "build", Kind: task.StepBuild, Status: task.StepPending}},
	); !errors.Is(err, task.ErrConflict) {
		t.Fatalf("mismatched replacement error = %v, want ErrConflict", err)
	}
}

func TestReplaceQueuedPlanAllowsQueuedCoverageRun(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	input, steps, event, run, testRun := coverageCreationFixture(t, 41)
	created, _, err := store.CreateCoverageTask(ctx, input, steps, event, run, testRun)
	if err != nil {
		t.Fatal(err)
	}
	fingerprint := strings.Repeat("f", 64)
	replaced, err := store.ReplaceQueuedPlan(ctx, created.ID, created.RequestHash, fingerprint, []task.StepSnapshot{{
		ID: "coverage-report", Kind: task.StepCoverageReport, Status: task.StepPending,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if replaced.Kind != task.KindCoverageRun || replaced.PlanFingerprint != fingerprint ||
		len(replaced.Steps) != 1 || replaced.Steps[0].Kind != task.StepCoverageReport {
		t.Fatalf("replaced coverage task = %#v", replaced)
	}
}

func TestFailQueuedBuildPersistsInterruptedErrorAndSkipsSteps(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	at := time.Date(2026, 7, 29, 6, 0, 0, 0, time.UTC)
	input := newCMakeTask(22, 32, at)
	created, _, err := store.Create(ctx, input, []task.StepSnapshot{
		{ID: "configure", Kind: task.StepConfigure, Status: task.StepPending},
		{ID: "build", Kind: task.StepBuild, Status: task.StepPending},
	}, draft(input.ID, task.EventTaskCreated, at))
	if err != nil {
		t.Fatal(err)
	}
	failedAt := at.Add(time.Minute)
	failed, events, err := store.FailQueuedBuild(
		ctx, created.ID, "WORKSPACE_CHANGED", failedAt,
	)
	if err != nil {
		t.Fatal(err)
	}
	if failed.Status != task.StatusFinished || failed.Outcome != task.OutcomeInterrupted ||
		failed.ErrorCode != "WORKSPACE_CHANGED" || failed.FinishedAt == nil ||
		!failed.FinishedAt.Equal(failedAt) || len(failed.Steps) != 2 ||
		failed.Steps[0].Status != task.StepSkipped ||
		failed.Steps[1].Status != task.StepSkipped ||
		len(events) != 1 || events[0].Type != task.EventTaskFinished ||
		string(events[0].Payload) != `{"outcome":"interrupted"}` {
		t.Fatalf("failed queued build = %#v, events = %#v", failed, events)
	}
}

func TestFailQueuedTestRunCompletesRunAndResolvesItByTask(
	t *testing.T,
) {
	store := openTestStore(t)
	ctx := context.Background()
	at := time.Date(2026, 7, 31, 11, 0, 0, 0, time.UTC)
	artifacts, err := artifactstore.New(
		filepath.Join(t.TempDir(), "artifacts"),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = artifacts.Close() })
	catalog := catalogFixture(t, "7", "fails")
	publishCatalogFixture(
		t,
		store,
		artifacts,
		catalog,
		90,
	)
	input := testRunTaskFixture(93, 94, at)
	run := runFixture(input, 95, catalog.Items[0].ID)
	run.ProfileID = catalog.ProfileID
	run.CatalogRevision = catalog.Revision
	created, _, err := store.CreateTestTask(
		ctx,
		input,
		[]task.StepSnapshot{{
			ID:     "build",
			Kind:   task.StepBuild,
			Status: task.StepPending,
		}},
		draft(input.ID, task.EventTaskCreated, at),
		run,
	)
	if err != nil {
		t.Fatal(err)
	}

	failedAt := at.Add(time.Minute)
	failed, events, err := store.FailQueuedTask(
		ctx,
		created.ID,
		"WORKSPACE_CHANGED",
		failedAt,
	)
	if err != nil {
		t.Fatal(err)
	}
	if failed.Status != task.StatusFinished ||
		failed.Outcome != task.OutcomeInterrupted ||
		failed.ErrorCode != "WORKSPACE_CHANGED" ||
		len(events) != 2 ||
		events[0].Type != task.EventTestRunFinished ||
		events[1].Type != task.EventTaskFinished {
		t.Fatalf(
			"failed queued TestRun task = %#v, events = %#v",
			failed,
			events,
		)
	}
	persisted, err := store.GetRunForTask(ctx, created.ID)
	if err != nil ||
		persisted.RunID != run.RunID ||
		persisted.Status != testdomain.RunCompleted ||
		persisted.Outcome != testdomain.RunInterrupted ||
		!persisted.Incomplete ||
		len(persisted.Results) != 1 ||
		persisted.Results[0].Outcome != testdomain.ItemNotRun ||
		persisted.Results[0].Reason !=
			testdomain.ReasonServiceRestarted {
		t.Fatalf(
			"recovered queued TestRun = %#v, %v",
			persisted,
			err,
		)
	}
}
