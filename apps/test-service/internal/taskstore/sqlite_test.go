package taskstore

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"unit-test-ide.local/test-service/internal/task"
)

func TestStoreCommitsSnapshotAndEventAtomically(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	now := time.Date(2026, 7, 22, 0, 0, 0, 0, time.UTC)
	created, events, err := store.Create(ctx, task.Task{
		ID: id(1), IdempotencyKey: id(2), RequestHash: strings.Repeat("a", 64),
		Scenario: task.ScenarioHang, Timeout: 30 * time.Second,
		Status: task.StatusQueued, CreatedAt: now,
	}, task.EventDraft{TaskID: id(1), Type: "task.created", At: now, Payload: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	if created.LastSequence != events[0].Sequence || events[0].Sequence != 1 {
		t.Fatalf("created = %#v, events = %#v", created, events)
	}

	running, started, err := store.Apply(ctx, task.Mutation{
		Task:     mustTransition(t, created, task.Transition{From: task.StatusQueued, To: task.StatusRunning, At: now.Add(time.Second)}),
		Expected: task.StatusQueued,
		Events:   []task.EventDraft{{TaskID: id(1), Type: "task.started", At: now.Add(time.Second), Payload: json.RawMessage(`{}`)}},
		PutLease: &task.ProcessLease{TaskID: id(1), HostPID: 42, HostStartIdentity: "100", ServiceInstanceID: id(3)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if running.Status != task.StatusRunning || started[0].Sequence != 2 || running.LastSequence != 2 {
		t.Fatalf("running = %#v, events = %#v", running, started)
	}
	got, err := store.Get(ctx, created.ID)
	if err != nil || got.Status != task.StatusRunning || got.StartedAt == nil || !got.StartedAt.Equal(now.Add(time.Second)) {
		t.Fatalf("Get() = %#v, %v", got, err)
	}
	leases, err := store.ActiveLeases(ctx)
	if err != nil || len(leases) != 1 || leases[0].HostPID != 42 {
		t.Fatalf("ActiveLeases() = %#v, %v", leases, err)
	}
}

func TestCreateIsIdempotentByKeyAndRequestHash(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	now := time.Date(2026, 7, 22, 1, 0, 0, 0, time.UTC)
	input := newTask(10, 11, now)
	created, firstEvents, err := store.Create(ctx, input, draft(input.ID, task.EventTaskCreated, now))
	if err != nil {
		t.Fatal(err)
	}
	replayed := input
	replayed.ID = id(12)
	got, events, err := store.Create(ctx, replayed, draft(replayed.ID, task.EventTaskCreated, now))
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != created.ID || len(events) != 0 || len(firstEvents) != 1 {
		t.Fatalf("idempotent Create() = %#v, %#v", got, events)
	}
	replayed.RequestHash = strings.Repeat("b", 64)
	_, _, err = store.Create(ctx, replayed, draft(replayed.ID, task.EventTaskCreated, now))
	if !errors.Is(err, task.ErrIdempotencyConflict) {
		t.Fatalf("Create() error = %v, want ErrIdempotencyConflict", err)
	}
	watermark, err := store.Watermark(ctx)
	if err != nil || watermark != 1 {
		t.Fatalf("Watermark() = %d, %v", watermark, err)
	}
}

func TestApplyWrongExpectedStatusRollsBackEverything(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	now := time.Date(2026, 7, 22, 2, 0, 0, 0, time.UTC)
	created := createTask(t, store, newTask(20, 21, now))
	finished := created
	finished.Status = task.StatusFinished
	finished.Outcome = task.OutcomeInterrupted
	finished.FinishedAt = ptrTime(now.Add(time.Second))
	_, _, err := store.Apply(ctx, task.Mutation{
		Task: finished, Expected: task.StatusRunning,
		Events:    []task.EventDraft{draft(created.ID, task.EventTaskFinished, now.Add(time.Second))},
		Artifacts: []task.Artifact{{ID: id(22), TaskID: created.ID, Kind: "stdout", RelativePath: "task/stdout.txt", MIMEType: "text/plain", Size: 3, SHA256: strings.Repeat("c", 64), CreatedAt: now}},
	})
	if !errors.Is(err, task.ErrConflict) {
		t.Fatalf("Apply() error = %v, want ErrConflict", err)
	}
	got, err := store.Get(ctx, created.ID)
	if err != nil || got.Status != task.StatusQueued || got.LastSequence != 1 {
		t.Fatalf("Get() = %#v, %v", got, err)
	}
	watermark, _ := store.Watermark(ctx)
	artifacts, listErr := store.ListArtifacts(ctx, created.ID, "", 10)
	if watermark != 1 || listErr != nil || len(artifacts.Items) != 0 {
		t.Fatalf("rollback watermark=%d artifacts=%#v err=%v", watermark, artifacts, listErr)
	}
}

func TestListUsesStableOpaqueCursor(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	base := time.Date(2026, 7, 22, 3, 0, 0, 0, time.UTC)
	createTask(t, store, newTask(30, 40, base))
	createTask(t, store, newTask(31, 41, base.Add(time.Second)))
	createTask(t, store, newTask(32, 42, base.Add(2*time.Second)))
	first, err := store.List(ctx, "", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Items) != 2 || first.Items[0].ID != id(32) || first.Items[1].ID != id(31) || first.NextCursor == "" {
		t.Fatalf("first page = %#v", first)
	}
	createTask(t, store, newTask(33, 43, base.Add(3*time.Second)))
	second, err := store.List(ctx, first.NextCursor, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Items) != 1 || second.Items[0].ID != id(30) || second.NextCursor != "" {
		t.Fatalf("second page = %#v", second)
	}
	for _, limit := range []int{0, 201} {
		if _, err := store.List(ctx, "", limit); !errors.Is(err, task.ErrInvalidArgument) {
			t.Fatalf("List(limit=%d) error = %v", limit, err)
		}
	}
	if _, err := store.List(ctx, "not-base64", 10); !errors.Is(err, task.ErrInvalidArgument) {
		t.Fatalf("List(invalid cursor) error = %v", err)
	}
}

func TestEventsAfterUsesExclusiveAfterInclusiveThrough(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	now := time.Date(2026, 7, 22, 4, 0, 0, 0, time.UTC)
	createdA := createTask(t, store, newTask(50, 51, now))
	createdB := createTask(t, store, newTask(52, 53, now.Add(time.Second)))
	if _, err := store.AppendEvent(ctx, createdA.ID, draft(createdA.ID, task.EventTaskOutput, now.Add(2*time.Second))); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AppendEvent(ctx, createdB.ID, draft(createdB.ID, task.EventTaskOutput, now.Add(3*time.Second))); err != nil {
		t.Fatal(err)
	}
	events, err := store.EventsAfter(ctx, 1, 3, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 || events[0].Sequence != 2 || events[1].Sequence != 3 {
		t.Fatalf("EventsAfter() = %#v", events)
	}
	got, err := store.Get(ctx, createdA.ID)
	if err != nil || got.LastSequence != 3 {
		t.Fatalf("Get() after AppendEvent = %#v, %v", got, err)
	}
}

func TestArtifactsPersistMetadataAndExposeCleanupReferences(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	now := time.Date(2026, 7, 22, 5, 0, 0, 123, time.FixedZone("offset", 8*60*60))
	created := createTask(t, store, newTask(60, 61, now))
	running := mustTransition(t, created, task.Transition{From: task.StatusQueued, To: task.StatusRunning, At: now.Add(time.Second)})
	artifactA := task.Artifact{ID: id(62), TaskID: created.ID, Kind: "stdout", RelativePath: "60/stdout.txt", MIMEType: "text/plain", Size: 10, SHA256: strings.Repeat("d", 64), CreatedAt: now.Add(2 * time.Second)}
	artifactB := task.Artifact{ID: id(63), TaskID: created.ID, Kind: "stderr", RelativePath: "60/stderr.txt", MIMEType: "text/plain", Size: 0, SHA256: strings.Repeat("e", 64), CreatedAt: now.Add(3 * time.Second)}
	_, _, err := store.Apply(ctx, task.Mutation{Task: running, Expected: task.StatusQueued, Artifacts: []task.Artifact{artifactA, artifactB}})
	if err != nil {
		t.Fatal(err)
	}
	page, err := store.ListArtifacts(ctx, created.ID, "", 1)
	if err != nil || len(page.Items) != 1 || page.Items[0].ID != artifactA.ID || page.NextCursor == "" {
		t.Fatalf("ListArtifacts() = %#v, %v", page, err)
	}
	next, err := store.ListArtifacts(ctx, created.ID, page.NextCursor, 1)
	if err != nil || len(next.Items) != 1 || next.Items[0].ID != artifactB.ID || next.NextCursor != "" {
		t.Fatalf("ListArtifacts(next) = %#v, %v", next, err)
	}
	got, err := store.GetArtifact(ctx, artifactA.ID)
	if err != nil || got.RelativePath != artifactA.RelativePath || !got.CreatedAt.Equal(artifactA.CreatedAt) {
		t.Fatalf("GetArtifact() = %#v, %v", got, err)
	}
	paths, err := store.ReferencedArtifactPaths(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := paths[artifactA.RelativePath]; !ok || len(paths) != 2 {
		t.Fatalf("ReferencedArtifactPaths() = %#v", paths)
	}
}

func TestUpdateLeaseRequiresExistingLeaseOnActiveTask(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	now := time.Date(2026, 7, 22, 6, 0, 0, 0, time.UTC)
	created := createTask(t, store, newTask(70, 71, now))
	missing := task.ProcessLease{TaskID: created.ID, HostPID: 10, HostStartIdentity: "one", ServiceInstanceID: id(72)}
	if err := store.UpdateLease(ctx, missing); !errors.Is(err, task.ErrConflict) {
		t.Fatalf("UpdateLease(missing) error = %v", err)
	}
	running := mustTransition(t, created, task.Transition{From: task.StatusQueued, To: task.StatusRunning, At: now.Add(time.Second)})
	_, _, err := store.Apply(ctx, task.Mutation{Task: running, Expected: task.StatusQueued, PutLease: &missing})
	if err != nil {
		t.Fatal(err)
	}
	missing.HostPID = 11
	missing.TargetProcessGroup = 12
	if err := store.UpdateLease(ctx, missing); err != nil {
		t.Fatal(err)
	}
	leases, _ := store.ActiveLeases(ctx)
	if len(leases) != 1 || leases[0].HostPID != 11 || leases[0].TargetProcessGroup != 12 {
		t.Fatalf("ActiveLeases() = %#v", leases)
	}
	finished := mustTransition(t, running, task.Transition{From: task.StatusRunning, To: task.StatusFinished, Outcome: task.OutcomeSucceeded, At: now.Add(2 * time.Second)})
	if _, _, err := store.Apply(ctx, task.Mutation{Task: finished, Expected: task.StatusRunning}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateLease(ctx, missing); !errors.Is(err, task.ErrConflict) {
		t.Fatalf("UpdateLease(finished) error = %v", err)
	}
}

func TestRecoverInterruptedFinishesAllActiveTasksAndDeletesLeases(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	base := time.Date(2026, 7, 22, 7, 0, 0, 0, time.UTC)
	queued := createTask(t, store, newTask(80, 90, base))
	running := createTask(t, store, newTask(81, 91, base.Add(time.Second)))
	runningState := mustTransition(t, running, task.Transition{From: task.StatusQueued, To: task.StatusRunning, At: base.Add(2 * time.Second)})
	lease := task.ProcessLease{TaskID: running.ID, HostPID: 42, HostStartIdentity: "start", ServiceInstanceID: id(92)}
	if _, _, err := store.Apply(ctx, task.Mutation{Task: runningState, Expected: task.StatusQueued, PutLease: &lease}); err != nil {
		t.Fatal(err)
	}
	cancelling := mustTransition(t, runningState, task.Transition{From: task.StatusRunning, To: task.StatusCancelling, At: base.Add(3 * time.Second)})
	if _, _, err := store.Apply(ctx, task.Mutation{Task: cancelling, Expected: task.StatusRunning}); err != nil {
		t.Fatal(err)
	}
	terminal := createTask(t, store, newTask(82, 93, base.Add(4*time.Second)))
	terminalState := terminal
	terminalState.Status = task.StatusFinished
	terminalState.Outcome = task.OutcomeSucceeded
	terminalState.FinishedAt = ptrTime(base.Add(5 * time.Second))
	if _, _, err := store.Apply(ctx, task.Mutation{Task: terminalState, Expected: task.StatusQueued}); err != nil {
		t.Fatal(err)
	}

	recoveredAt := base.Add(10 * time.Second)
	events, err := store.RecoverInterrupted(ctx, recoveredAt)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 || events[0].Sequence >= events[1].Sequence {
		t.Fatalf("RecoverInterrupted() = %#v", events)
	}
	for _, taskID := range []string{queued.ID, running.ID} {
		got, err := store.Get(ctx, taskID)
		if err != nil || got.Status != task.StatusFinished || got.Outcome != task.OutcomeInterrupted || got.FinishedAt == nil || !got.FinishedAt.Equal(recoveredAt) {
			t.Fatalf("recovered task %s = %#v, %v", taskID, got, err)
		}
	}
	gotTerminal, _ := store.Get(ctx, terminal.ID)
	if gotTerminal.Outcome != task.OutcomeSucceeded {
		t.Fatalf("terminal task changed: %#v", gotTerminal)
	}
	leases, err := store.ActiveLeases(ctx)
	if err != nil || len(leases) != 0 {
		t.Fatalf("ActiveLeases() = %#v, %v", leases, err)
	}
	again, err := store.RecoverInterrupted(ctx, recoveredAt.Add(time.Second))
	if err != nil || len(again) != 0 {
		t.Fatalf("second RecoverInterrupted() = %#v, %v", again, err)
	}
}

func TestRecoverInterruptedRollsBackAllTasksWhenEventInsertFails(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	base := time.Date(2026, 7, 22, 7, 30, 0, 0, time.UTC)
	first := createTask(t, store, newTask(84, 94, base))
	second := createTask(t, store, newTask(85, 95, base.Add(time.Second)))
	var existingEventID string
	if err := store.db.QueryRow(`SELECT event_id FROM task_events ORDER BY sequence LIMIT 1`).Scan(&existingEventID); err != nil {
		t.Fatal(err)
	}
	store.newID = func() string { return existingEventID }
	if _, err := store.RecoverInterrupted(ctx, base.Add(time.Minute)); !errors.Is(err, task.ErrStorageUnavailable) {
		t.Fatalf("RecoverInterrupted() error = %v", err)
	}
	for _, taskID := range []string{first.ID, second.ID} {
		got, err := store.Get(ctx, taskID)
		if err != nil || got.Status != task.StatusQueued || got.Outcome != "" || got.FinishedAt != nil || got.LastSequence != 1 && got.LastSequence != 2 {
			t.Fatalf("task after failed recovery = %#v, %v", got, err)
		}
	}
	watermark, err := store.Watermark(ctx)
	if err != nil || watermark != 2 {
		t.Fatalf("Watermark() = %d, %v", watermark, err)
	}
}

func TestReopenDoesNotReapplyMigrationAndDetectsChecksumTampering(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.sqlite")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	var count int
	var checksum string
	if err := store.db.QueryRow(`SELECT COUNT(*), MIN(sha256) FROM schema_migrations`).Scan(&count, &checksum); err != nil {
		t.Fatal(err)
	}
	if count != 1 || len(checksum) != 64 {
		t.Fatalf("schema_migrations count=%d checksum=%q", count, checksum)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM schema_migrations`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("reopen count=%d err=%v", count, err)
	}
	if _, err := store.db.Exec(`UPDATE schema_migrations SET sha256=? WHERE version=1`, strings.Repeat("0", 64)); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(path); err == nil || !errors.Is(err, task.ErrStorageUnavailable) {
		t.Fatalf("Open(tampered migration) error = %v", err)
	}
}

func TestReopenRejectsUnknownAppliedMigrationVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.sqlite")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`INSERT INTO schema_migrations(version, sha256, applied_at) VALUES(999, ?, ?)`, strings.Repeat("f", 64), formatTime(time.Now())); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path)
	if reopened != nil {
		_ = reopened.Close()
	}
	if err == nil || !errors.Is(err, task.ErrStorageUnavailable) {
		t.Fatalf("Open(unknown migration version) error = %v", err)
	}
}

func TestRowsRoundTripAndNotFoundErrorsAreStable(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	now := time.Date(2026, 7, 22, 8, 0, 0, 987654321, time.FixedZone("west", -7*60*60))
	input := newTask(100, 101, now)
	created := createTask(t, store, input)
	got, err := store.FindByIdempotencyKey(ctx, input.IdempotencyKey)
	if err != nil || got.ID != created.ID || !got.CreatedAt.Equal(now) || got.StartedAt != nil || got.FinishedAt != nil {
		t.Fatalf("FindByIdempotencyKey() = %#v, %v", got, err)
	}
	if _, err := store.Get(ctx, id(199)); !errors.Is(err, task.ErrNotFound) {
		t.Fatalf("Get(missing) error = %v", err)
	}
	if _, err := store.FindByIdempotencyKey(ctx, id(198)); !errors.Is(err, task.ErrNotFound) {
		t.Fatalf("FindByIdempotencyKey(missing) error = %v", err)
	}
	if _, err := store.GetArtifact(ctx, id(197)); !errors.Is(err, task.ErrNotFound) {
		t.Fatalf("GetArtifact(missing) error = %v", err)
	}
}

func TestDatabaseIntegrityAndStorageErrors(t *testing.T) {
	store := openTestStore(t)
	var integrity string
	if err := store.db.QueryRow(`PRAGMA integrity_check`).Scan(&integrity); err != nil || integrity != "ok" {
		t.Fatalf("integrity_check = %q, %v", integrity, err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Get(context.Background(), id(1)); !errors.Is(err, task.ErrStorageUnavailable) {
		t.Fatalf("Get(closed) error = %v", err)
	}
}

func openTestStore(t *testing.T) *Store {
	t.Helper()
	store, err := Open(filepath.Join(t.TempDir(), "history.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func newTask(taskByte, keyByte byte, at time.Time) task.Task {
	return task.Task{
		ID: id(taskByte), IdempotencyKey: id(keyByte), RequestHash: strings.Repeat("a", 64),
		Scenario: task.ScenarioSuccess, Timeout: 30 * time.Second, Status: task.StatusQueued, CreatedAt: at,
	}
}

func createTask(t *testing.T, store *Store, input task.Task) task.Task {
	t.Helper()
	created, _, err := store.Create(context.Background(), input, draft(input.ID, task.EventTaskCreated, input.CreatedAt))
	if err != nil {
		t.Fatal(err)
	}
	return created
}

func draft(taskID string, eventType task.EventType, at time.Time) task.EventDraft {
	return task.EventDraft{TaskID: taskID, Type: eventType, At: at, Payload: json.RawMessage(`{}`)}
}

func mustTransition(t *testing.T, current task.Task, change task.Transition) task.Task {
	t.Helper()
	next, err := task.ApplyTransition(current, change)
	if err != nil {
		t.Fatal(err)
	}
	return next
}

func id(value byte) string { return strings.Repeat(string([]byte{'a' + value%6}), 32) }

func ptrTime(value time.Time) *time.Time { return &value }

var _ task.Store = (*Store)(nil)
