package taskstore

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"unit-test-ide.local/test-service/internal/task"
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
