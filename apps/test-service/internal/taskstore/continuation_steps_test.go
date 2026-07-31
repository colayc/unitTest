package taskstore

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"unit-test-ide.local/test-service/internal/task"
)

func TestApplyAtomicallyCompletesCurrentStepAndAppendsContinuation(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	now := time.Date(2026, 7, 30, 10, 0, 0, 0, time.UTC)
	input := newCMakeTask(140, 141, now)
	initial := task.StepSnapshot{
		ID: "build", Kind: task.StepBuild, Status: task.StepPending,
	}
	created, _, err := store.Create(
		ctx,
		input,
		[]task.StepSnapshot{initial},
		draft(input.ID, task.EventTaskCreated, now),
	)
	if err != nil {
		t.Fatal(err)
	}
	running := startStoredStep(
		t,
		store,
		created,
		0,
		now.Add(time.Second),
		140,
	)

	finishedAt := now.Add(2 * time.Second)
	succeeded := running.Steps[0]
	succeeded.Status = task.StepSucceeded
	succeeded.FinishedAt = ptrTime(finishedAt)
	succeeded.ExitCode = ptrInt(0)
	next := running
	next.ActiveStep = ""
	next.PlanFingerprint = strings.Repeat("e", 64)
	appended := task.StepSnapshot{
		ID: "post-build", Kind: task.StepBuild, Status: task.StepPending,
	}
	stored, _, err := store.Apply(ctx, task.Mutation{
		Task:        next,
		Expected:    task.StatusRunning,
		Steps:       []task.StepMutation{{Step: succeeded, Expected: task.StepRunning}},
		AppendSteps: []task.StepSnapshot{appended},
		DeleteLease: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	assertContinuedSteps(t, stored, next.PlanFingerprint)

	reloaded, err := store.Get(ctx, input.ID)
	if err != nil {
		t.Fatal(err)
	}
	assertContinuedSteps(t, reloaded, next.PlanFingerprint)
}

func TestApplyRollsBackStepCompletionWhenContinuationIDAlreadyExists(
	t *testing.T,
) {
	ctx := context.Background()
	store := openTestStore(t)
	now := time.Date(2026, 7, 30, 10, 30, 0, 0, time.UTC)
	input := newCMakeTask(142, 143, now)
	initial := []task.StepSnapshot{
		{ID: "build", Kind: task.StepBuild, Status: task.StepPending},
		{ID: "package", Kind: task.StepBuild, Status: task.StepPending},
	}
	created, _, err := store.Create(
		ctx,
		input,
		initial,
		draft(input.ID, task.EventTaskCreated, now),
	)
	if err != nil {
		t.Fatal(err)
	}
	running := startStoredStep(
		t,
		store,
		created,
		0,
		now.Add(time.Second),
		142,
	)
	succeeded := running.Steps[0]
	succeeded.Status = task.StepSucceeded
	succeeded.FinishedAt = ptrTime(now.Add(2 * time.Second))
	next := running
	next.ActiveStep = ""
	next.PlanFingerprint = strings.Repeat("f", 64)

	_, _, err = store.Apply(ctx, task.Mutation{
		Task:     next,
		Expected: task.StatusRunning,
		Steps: []task.StepMutation{{
			Step: succeeded, Expected: task.StepRunning,
		}},
		AppendSteps: []task.StepSnapshot{{
			ID: "package", Kind: task.StepBuild, Status: task.StepPending,
		}},
		DeleteLease: true,
	})
	if !errors.Is(err, task.ErrConflict) {
		t.Fatalf("Apply() error = %v, want ErrConflict", err)
	}
	reloaded, err := store.Get(ctx, input.ID)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.ActiveStep != "build" ||
		reloaded.PlanFingerprint != running.PlanFingerprint ||
		len(reloaded.Steps) != 2 ||
		reloaded.Steps[0].Status != task.StepRunning ||
		reloaded.Steps[0].FinishedAt != nil ||
		reloaded.Steps[1].Status != task.StepPending {
		t.Fatalf("task after rolled-back continuation = %#v", reloaded)
	}
	var leaseCount int
	if err := store.db.QueryRow(
		`SELECT COUNT(*) FROM process_leases WHERE task_id=?`,
		input.ID,
	).Scan(&leaseCount); err != nil {
		t.Fatal(err)
	}
	if leaseCount != 1 {
		t.Fatalf("active process lease count = %d, want 1", leaseCount)
	}
}

func TestApplyRejectsContinuationOnTerminalTask(t *testing.T) {
	store := openTestStore(t)
	now := time.Date(2026, 7, 30, 11, 0, 0, 0, time.UTC)
	created := createTask(t, store, newCMakeTask(144, 145, now))
	finished := created
	finished.Status = task.StatusFinished
	finished.Outcome = task.OutcomeSucceeded
	finished.FinishedAt = ptrTime(now.Add(time.Second))

	_, _, err := store.Apply(context.Background(), task.Mutation{
		Task:     finished,
		Expected: task.StatusQueued,
		AppendSteps: []task.StepSnapshot{{
			ID: "late-step", Kind: task.StepBuild, Status: task.StepPending,
		}},
	})
	if !errors.Is(err, task.ErrInvalidArgument) {
		t.Fatalf("Apply() error = %v, want ErrInvalidArgument", err)
	}
}

func assertContinuedSteps(
	t *testing.T,
	value task.Task,
	fingerprint string,
) {
	t.Helper()
	if value.Status != task.StatusRunning ||
		value.ActiveStep != "" ||
		value.PlanFingerprint != fingerprint ||
		len(value.Steps) != 2 ||
		value.Steps[0].ID != "build" ||
		value.Steps[0].Status != task.StepSucceeded ||
		value.Steps[1].ID != "post-build" ||
		value.Steps[1].Status != task.StepPending {
		t.Fatalf("continued task = %#v", value)
	}
}

func ptrInt(value int) *int { return &value }
