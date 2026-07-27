package taskstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
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
		Kind: task.KindSimulation, Request: json.RawMessage(`{"scenario":"hang"}`),
		Scenario: task.ScenarioHang, Timeout: 30 * time.Second,
		Status: task.StatusQueued, CreatedAt: now,
	}, nil, task.EventDraft{TaskID: id(1), Type: "task.created", At: now, Payload: json.RawMessage(`{}`)})
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
	created, firstEvents, err := store.Create(ctx, input, nil, draft(input.ID, task.EventTaskCreated, now))
	if err != nil {
		t.Fatal(err)
	}
	replayed := input
	replayed.ID = id(12)
	got, events, err := store.Create(ctx, replayed, nil, draft(replayed.ID, task.EventTaskCreated, now))
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != created.ID || len(events) != 0 || len(firstEvents) != 1 {
		t.Fatalf("idempotent Create() = %#v, %#v", got, events)
	}
	replayed.RequestHash = strings.Repeat("b", 64)
	_, _, err = store.Create(ctx, replayed, nil, draft(replayed.ID, task.EventTaskCreated, now))
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

func TestApplyWithoutEventsPreservesNewerPersistedSequence(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	now := time.Date(2026, 7, 22, 2, 10, 0, 0, time.UTC)
	stale := createTask(t, store, newTask(23, 24, now))
	appended, err := store.AppendEvent(ctx, stale.ID, draft(stale.ID, task.EventTaskOutput, now.Add(time.Second)))
	if err != nil {
		t.Fatal(err)
	}
	if appended.Sequence != 2 {
		t.Fatalf("AppendEvent() sequence = %d, want 2", appended.Sequence)
	}

	updated, events, err := store.Apply(ctx, task.Mutation{Task: stale, Expected: task.StatusQueued})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 0 || updated.LastSequence != appended.Sequence {
		t.Fatalf("Apply() = %#v, events=%#v", updated, events)
	}
	persisted, err := store.Get(ctx, stale.ID)
	if err != nil || persisted.LastSequence != appended.Sequence {
		t.Fatalf("Get() = %#v, %v", persisted, err)
	}
}

func TestApplyRejectsLeaseForTerminalResult(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	now := time.Date(2026, 7, 22, 2, 20, 0, 0, time.UTC)
	created := createTask(t, store, newTask(25, 26, now))
	finished := created
	finished.Status = task.StatusFinished
	finished.Outcome = task.OutcomeSucceeded
	finished.FinishedAt = ptrTime(now.Add(time.Second))
	lease := task.ProcessLease{TaskID: created.ID, HostPID: 42, HostStartIdentity: "start", ServiceInstanceID: id(27)}
	_, _, err := store.Apply(ctx, task.Mutation{Task: finished, Expected: task.StatusQueued, PutLease: &lease})
	if !errors.Is(err, task.ErrInvalidArgument) {
		t.Fatalf("Apply(terminal lease) error = %v, want ErrInvalidArgument", err)
	}
	var leaseCount int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM process_leases WHERE task_id=?`, created.ID).Scan(&leaseCount); err != nil {
		t.Fatal(err)
	}
	persisted, getErr := store.Get(ctx, created.ID)
	if leaseCount != 0 || getErr != nil || persisted.Status != task.StatusQueued {
		t.Fatalf("leaseCount=%d persisted=%#v err=%v", leaseCount, persisted, getErr)
	}
}

func TestApplyRollsBackSnapshotWhenEventInsertFails(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	now := time.Date(2026, 7, 22, 2, 30, 0, 0, time.UTC)
	input := newTask(28, 29, now)
	initialStep := task.StepSnapshot{ID: "simulate", Kind: task.StepSimulation, Status: task.StepPending}
	created, _, err := store.Create(ctx, input, []task.StepSnapshot{initialStep}, draft(input.ID, task.EventTaskCreated, now))
	if err != nil {
		t.Fatal(err)
	}
	var existingEventID string
	if err := store.db.QueryRow(`SELECT event_id FROM task_events WHERE task_id=?`, created.ID).Scan(&existingEventID); err != nil {
		t.Fatal(err)
	}
	store.newID = func() string { return existingEventID }
	running := mustTransition(t, created, task.Transition{From: task.StatusQueued, To: task.StatusRunning, At: now.Add(time.Second)})
	running.ActiveStep = initialStep.ID
	runningStep := initialStep
	runningStep.Status = task.StepRunning
	runningStep.StartedAt = running.StartedAt
	lease := task.ProcessLease{TaskID: created.ID, HostPID: 50, HostStartIdentity: "start", ServiceInstanceID: id(30)}
	_, _, err = store.Apply(ctx, task.Mutation{
		Task: running, Expected: task.StatusQueued,
		Steps:    []task.StepMutation{{Step: runningStep, Expected: task.StepPending}},
		Events:   []task.EventDraft{draft(created.ID, task.EventTaskStarted, now.Add(time.Second))},
		PutLease: &lease,
	})
	if !errors.Is(err, task.ErrStorageUnavailable) {
		t.Fatalf("Apply() error = %v, want ErrStorageUnavailable", err)
	}
	persisted, err := store.Get(ctx, created.ID)
	if err != nil || persisted.Status != task.StatusQueued || persisted.StartedAt != nil || persisted.ActiveStep != "" ||
		len(persisted.Steps) != 1 || persisted.Steps[0].Status != task.StepPending ||
		persisted.LastSequence != created.LastSequence {
		t.Fatalf("Get() after rollback = %#v, %v", persisted, err)
	}
	var leaseCount int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM process_leases WHERE task_id=?`, created.ID).Scan(&leaseCount); err != nil || leaseCount != 0 {
		t.Fatalf("lease count = %d, %v", leaseCount, err)
	}
	watermark, err := store.Watermark(ctx)
	if err != nil || watermark != created.LastSequence {
		t.Fatalf("Watermark() = %d, %v", watermark, err)
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

func TestArtifactPathsRequireCanonicalPortableRelativeForm(t *testing.T) {
	base := task.Artifact{
		ID: id(64), TaskID: id(65), Kind: "stdout", MIMEType: "text/plain",
		Size: 1, SHA256: strings.Repeat("a", 64), CreatedAt: time.Date(2026, 7, 22, 5, 30, 0, 0, time.UTC),
	}
	valid := []string{
		"stdout.txt",
		"task/stdout.txt",
		"task-1/output_0.json",
		"console.txt",
		"com10.txt",
		"lpt10.log",
		"com0/auxiliary.data/nulled",
		"safe nested/file name.with.dots",
	}
	for _, relativePath := range valid {
		t.Run("valid_"+strings.ReplaceAll(relativePath, "/", "_"), func(t *testing.T) {
			artifact := base
			artifact.RelativePath = relativePath
			if !validArtifact(artifact) {
				t.Fatalf("validArtifact(%q) = false", relativePath)
			}
		})
	}
	invalid := []struct {
		name string
		path string
	}{
		{"dot", "."},
		{"leading_dot", "./task/stdout.txt"},
		{"duplicate_slash", "task//stdout.txt"},
		{"dot_segment", "task/./stdout.txt"},
		{"cleaned_traversal", "task/sub/../stdout.txt"},
		{"trailing_slash", "task/stdout.txt/"},
		{"parent", "../stdout.txt"},
		{"nested_parent", "task/../../stdout.txt"},
		{"slash_rooted", "/root/stdout.txt"},
		{"unc_slash", "//server/share/stdout.txt"},
		{"backslash_rooted", `\root\stdout.txt`},
		{"backslash_separator", `task\stdout.txt`},
		{"drive_absolute", "C:/task/stdout.txt"},
		{"drive_backslash", `C:\task\stdout.txt`},
		{"drive_relative", "C:task/stdout.txt"},
		{"trailing_dot", "task/stdout."},
		{"nested_trailing_dot", "task./stdout.txt"},
		{"trailing_space", "task/stdout.txt "},
		{"nested_trailing_space", "task /stdout.txt"},
	}
	forbidden := []struct {
		name string
		char byte
	}{
		{"less_than", '<'},
		{"greater_than", '>'},
		{"colon", ':'},
		{"quote", '"'},
		{"pipe", '|'},
		{"question", '?'},
		{"asterisk", '*'},
		{"backslash", '\\'},
	}
	for _, entry := range forbidden {
		invalid = append(invalid, struct {
			name string
			path string
		}{"forbidden_" + entry.name, "task/a" + string(entry.char) + "b.txt"})
	}
	for control := byte(0); control <= 0x1f; control++ {
		invalid = append(invalid, struct {
			name string
			path string
		}{fmt.Sprintf("control_%02x", control), "task/a" + string(control) + "b.txt"})
	}
	reserved := []string{"CON", "PRN", "AUX", "NUL", "COM1", "COM2", "COM3", "COM4", "COM5", "COM6", "COM7", "COM8", "COM9", "LPT1", "LPT2", "LPT3", "LPT4", "LPT5", "LPT6", "LPT7", "LPT8", "LPT9"}
	for _, name := range reserved {
		invalid = append(invalid,
			struct {
				name string
				path string
			}{"reserved_" + strings.ToLower(name), name},
			struct {
				name string
				path string
			}{"reserved_extension_" + strings.ToLower(name), "safe/" + strings.ToLower(name) + ".txt"},
		)
	}
	for _, entry := range invalid {
		t.Run("invalid_"+entry.name, func(t *testing.T) {
			artifact := base
			artifact.RelativePath = entry.path
			if validArtifact(artifact) {
				t.Fatalf("validArtifact(%q) = true", entry.path)
			}
		})
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
	queuedLease := task.ProcessLease{
		TaskID:            queued.ID,
		HostPID:           43,
		HostStartIdentity: "queued-prepared",
		ServiceInstanceID: id(94),
	}
	if _, _, err := store.Apply(ctx, task.Mutation{
		Task:     queued,
		Expected: task.StatusQueued,
		PutLease: &queuedLease,
	}); err != nil {
		t.Fatal(err)
	}
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
	leases, err := store.ActiveLeases(ctx)
	if err != nil || len(leases) != 2 {
		t.Fatalf("ActiveLeases before recovery = %#v, %v", leases, err)
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
	leases, err = store.ActiveLeases(ctx)
	if err != nil || len(leases) != 0 {
		t.Fatalf("ActiveLeases() = %#v, %v", leases, err)
	}
	for _, taskID := range []string{queued.ID, running.ID} {
		var physicalLeaseCount int
		if err := store.db.QueryRow(
			`SELECT COUNT(*) FROM process_leases WHERE task_id=?`,
			taskID,
		).Scan(&physicalLeaseCount); err != nil || physicalLeaseCount != 0 {
			t.Fatalf("physical lease count for %s = %d, %v", taskID, physicalLeaseCount, err)
		}
	}
	again, err := store.RecoverInterrupted(ctx, recoveredAt.Add(time.Second))
	if err != nil || len(again) != 0 {
		t.Fatalf("second RecoverInterrupted() = %#v, %v", again, err)
	}
}

func TestRecoverInterruptedDistinguishesTaskKindsAndReconcilesSteps(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	base := time.Date(2026, 7, 27, 2, 0, 0, 0, time.UTC)

	queuedSimulationInput := newTask(0, 10, base)
	queuedSimulation, _, err := store.Create(ctx, queuedSimulationInput, []task.StepSnapshot{{
		ID: "simulate", Kind: task.StepSimulation, Status: task.StepPending,
	}}, draft(queuedSimulationInput.ID, task.EventTaskCreated, base))
	if err != nil {
		t.Fatal(err)
	}
	putTestLease(t, store, queuedSimulation, 100)

	runningSimulationInput := newTask(1, 11, base.Add(time.Second))
	runningSimulation, _, err := store.Create(ctx, runningSimulationInput, []task.StepSnapshot{{
		ID: "simulate", Kind: task.StepSimulation, Status: task.StepPending,
	}}, draft(runningSimulationInput.ID, task.EventTaskCreated, runningSimulationInput.CreatedAt))
	if err != nil {
		t.Fatal(err)
	}
	runningSimulation = startStoredStep(t, store, runningSimulation, 0, base.Add(2*time.Second), 101)

	runningBuildInput := newCMakeTask(2, 12, base.Add(3*time.Second))
	runningBuild, _, err := store.Create(ctx, runningBuildInput, []task.StepSnapshot{
		{ID: "configure", Kind: task.StepConfigure, Status: task.StepPending},
		{ID: "build", Kind: task.StepBuild, Status: task.StepPending},
	}, draft(runningBuildInput.ID, task.EventTaskCreated, runningBuildInput.CreatedAt))
	if err != nil {
		t.Fatal(err)
	}
	runningBuild = startStoredStep(t, store, runningBuild, 0, base.Add(4*time.Second), 102)
	cancellingBuild := mustTransition(t, runningBuild, task.Transition{
		From: task.StatusRunning, To: task.StatusCancelling, At: base.Add(5 * time.Second),
	})
	if runningBuild, _, err = store.Apply(ctx, task.Mutation{
		Task: cancellingBuild, Expected: task.StatusRunning,
		Events: []task.EventDraft{{
			TaskID: runningBuild.ID,
			Type:   task.EventTaskCancellationRequested,
			At:     base.Add(5 * time.Second),
			Payload: json.RawMessage(
				`{"status":"cancelling"}`,
			),
		}},
	}); err != nil {
		t.Fatal(err)
	}

	queuedBuildInput := newCMakeTask(3, 13, base.Add(6*time.Second))
	queuedBuild, _, err := store.Create(ctx, queuedBuildInput, []task.StepSnapshot{
		{ID: "configure", Kind: task.StepConfigure, Status: task.StepPending},
		{ID: "build", Kind: task.StepBuild, Status: task.StepPending},
	}, draft(queuedBuildInput.ID, task.EventTaskCreated, queuedBuildInput.CreatedAt))
	if err != nil {
		t.Fatal(err)
	}
	putTestLease(t, store, queuedBuild, 103)
	queuedBuildSequence := queuedBuild.LastSequence

	recoveredAt := base.Add(time.Minute)
	events, err := store.RecoverInterrupted(ctx, recoveredAt)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 3 {
		t.Fatalf("recovery events = %#v, want one for each interrupted Task", events)
	}
	for index, event := range events {
		if event.Type != task.EventTaskFinished ||
			string(event.Payload) != `{"outcome":"interrupted"}` ||
			(index > 0 && events[index-1].Sequence >= event.Sequence) {
			t.Fatalf("recovery event[%d] = %#v", index, event)
		}
	}

	gotQueuedSimulation, err := store.Get(ctx, queuedSimulation.ID)
	if err != nil || gotQueuedSimulation.Status != task.StatusFinished ||
		gotQueuedSimulation.Outcome != task.OutcomeInterrupted ||
		len(gotQueuedSimulation.Steps) != 1 ||
		gotQueuedSimulation.Steps[0].Status != task.StepSkipped ||
		gotQueuedSimulation.Steps[0].FinishedAt == nil ||
		!gotQueuedSimulation.Steps[0].FinishedAt.Equal(recoveredAt) {
		t.Fatalf("recovered queued simulation = %#v, %v", gotQueuedSimulation, err)
	}
	gotRunningSimulation, err := store.Get(ctx, runningSimulation.ID)
	if err != nil || gotRunningSimulation.Status != task.StatusFinished ||
		gotRunningSimulation.Outcome != task.OutcomeInterrupted ||
		gotRunningSimulation.ActiveStep != "" ||
		len(gotRunningSimulation.Steps) != 1 ||
		gotRunningSimulation.Steps[0].Status != task.StepFailed ||
		gotRunningSimulation.Steps[0].ErrorCode != "SERVICE_RESTARTED" ||
		gotRunningSimulation.Steps[0].FinishedAt == nil ||
		!gotRunningSimulation.Steps[0].FinishedAt.Equal(recoveredAt) {
		t.Fatalf("recovered running simulation = %#v, %v", gotRunningSimulation, err)
	}
	gotRunningBuild, err := store.Get(ctx, runningBuild.ID)
	if err != nil || gotRunningBuild.Status != task.StatusFinished ||
		gotRunningBuild.Outcome != task.OutcomeInterrupted ||
		gotRunningBuild.ActiveStep != "" ||
		len(gotRunningBuild.Steps) != 2 ||
		gotRunningBuild.Steps[0].Status != task.StepFailed ||
		gotRunningBuild.Steps[0].ErrorCode != "SERVICE_RESTARTED" ||
		gotRunningBuild.Steps[1].Status != task.StepSkipped ||
		gotRunningBuild.Steps[1].FinishedAt == nil ||
		!gotRunningBuild.Steps[1].FinishedAt.Equal(recoveredAt) {
		t.Fatalf("recovered running cmake_build = %#v, %v", gotRunningBuild, err)
	}
	gotQueuedBuild, err := store.Get(ctx, queuedBuild.ID)
	if err != nil || gotQueuedBuild.Status != task.StatusQueued ||
		gotQueuedBuild.Outcome != "" || gotQueuedBuild.FinishedAt != nil ||
		gotQueuedBuild.LastSequence != queuedBuildSequence ||
		len(gotQueuedBuild.Steps) != 2 ||
		gotQueuedBuild.Steps[0].Status != task.StepPending ||
		gotQueuedBuild.Steps[1].Status != task.StepPending {
		t.Fatalf("preserved queued cmake_build = %#v, %v", gotQueuedBuild, err)
	}

	leases, err := store.ActiveLeases(ctx)
	if err != nil || len(leases) != 0 {
		t.Fatalf("ActiveLeases after recovery = %#v, %v", leases, err)
	}
	var queuedBuildLeaseCount int
	if err := store.db.QueryRow(
		`SELECT COUNT(*) FROM process_leases WHERE task_id=?`,
		queuedBuild.ID,
	).Scan(&queuedBuildLeaseCount); err != nil || queuedBuildLeaseCount != 0 {
		t.Fatalf("queued cmake_build physical lease count = %d, %v", queuedBuildLeaseCount, err)
	}
}

func TestRecoverInterruptedRollsBackTaskStepsEventsAndLeaseWhenStepUpdateFails(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	base := time.Date(2026, 7, 27, 3, 0, 0, 0, time.UTC)
	input := newTask(0, 10, base)
	created, _, err := store.Create(ctx, input, []task.StepSnapshot{{
		ID: "simulate", Kind: task.StepSimulation, Status: task.StepPending,
	}}, draft(input.ID, task.EventTaskCreated, base))
	if err != nil {
		t.Fatal(err)
	}
	running := startStoredStep(t, store, created, 0, base.Add(time.Second), 110)
	beforeWatermark, err := store.Watermark(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`CREATE TRIGGER fail_recovery_step_update
		BEFORE UPDATE ON task_steps
		WHEN OLD.task_id = '` + running.ID + `' AND NEW.status = 'failed'
		BEGIN
			SELECT RAISE(ABORT, 'forced recovery step failure');
		END`); err != nil {
		t.Fatal(err)
	}

	if _, err := store.RecoverInterrupted(ctx, base.Add(time.Minute)); !errors.Is(err, task.ErrStorageUnavailable) {
		t.Fatalf("RecoverInterrupted() error = %v, want ErrStorageUnavailable", err)
	}
	got, err := store.Get(ctx, running.ID)
	if err != nil || got.Status != task.StatusRunning || got.Outcome != "" ||
		got.ActiveStep != "simulate" || got.FinishedAt != nil ||
		len(got.Steps) != 1 || got.Steps[0].Status != task.StepRunning ||
		got.Steps[0].FinishedAt != nil || got.Steps[0].ErrorCode != "" {
		t.Fatalf("Task after failed recovery = %#v, %v", got, err)
	}
	afterWatermark, err := store.Watermark(ctx)
	if err != nil || afterWatermark != beforeWatermark {
		t.Fatalf("watermark after failed recovery = %d, %v; want %d", afterWatermark, err, beforeWatermark)
	}
	leases, err := store.ActiveLeases(ctx)
	if err != nil || len(leases) != 1 || leases[0].TaskID != running.ID {
		t.Fatalf("lease after failed recovery = %#v, %v", leases, err)
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

func TestMigration002UpgradesInitialTasksWithoutLosingRequests(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "history.sqlite")
	createMigration001Database(t, path)

	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	for _, taskID := range []string{id(120), id(121)} {
		got, err := store.Get(ctx, taskID)
		if err != nil {
			t.Fatal(err)
		}
		if got.Kind != task.KindSimulation || string(got.Request) != `{"scenario":"success"}` {
			t.Fatalf("migration lost task request: %#v", got)
		}
		if got.WorkspaceGeneration != "" || got.Scenario != task.ScenarioSuccess {
			t.Fatalf("migration lost simulation compatibility fields: %#v", got)
		}
	}
	if rows := foreignKeyViolations(t, store.db); rows != 0 {
		t.Fatalf("foreign key violations: %d", rows)
	}
	var foreignKeys int
	if err := store.db.QueryRow(`PRAGMA foreign_keys`).Scan(&foreignKeys); err != nil || foreignKeys != 1 {
		t.Fatalf("foreign_keys after migration = %d, %v", foreignKeys, err)
	}

	var eventTaskID string
	if err := store.db.QueryRow(`SELECT task_id FROM task_events WHERE event_id=?`, id(124)).Scan(&eventTaskID); err != nil || eventTaskID != id(120) {
		t.Fatalf("migrated event task = %q, %v", eventTaskID, err)
	}
	var leasePID int
	if err := store.db.QueryRow(`SELECT host_pid FROM process_leases WHERE task_id=?`, id(121)).Scan(&leasePID); err != nil || leasePID != 42 {
		t.Fatalf("migrated lease pid = %d, %v", leasePID, err)
	}
	var artifactTaskID, artifactPath string
	if err := store.db.QueryRow(`SELECT task_id, relative_path FROM artifacts WHERE artifact_id=?`, id(126)).
		Scan(&artifactTaskID, &artifactPath); err != nil || artifactTaskID != id(120) || artifactPath != "migration/stdout.txt" {
		t.Fatalf("migrated artifact = task %q path %q, %v", artifactTaskID, artifactPath, err)
	}
	var indexCount int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name IN (
		'task_events_task_sequence','tasks_history_order','artifacts_task_order'
	)`).Scan(&indexCount); err != nil || indexCount != 3 {
		t.Fatalf("preserved index count = %d, %v", indexCount, err)
	}

	stepTask := newTask(130, 131, time.Date(2026, 7, 22, 8, 2, 0, 0, time.UTC))
	step := task.StepSnapshot{ID: "simulate", Kind: task.StepSimulation, Status: task.StepPending}
	if _, _, err := store.Create(ctx, stepTask, []task.StepSnapshot{step}, draft(stepTask.ID, task.EventTaskCreated, stepTask.CreatedAt)); err != nil {
		t.Fatal(err)
	}
	gotStepTask, err := store.Get(ctx, stepTask.ID)
	if err != nil || len(gotStepTask.Steps) != 1 || gotStepTask.Steps[0] != step {
		t.Fatalf("post-migration task steps = %#v, %v", gotStepTask.Steps, err)
	}
}

func TestMigration002FailureRollsBackAndRestoresForeignKeys(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "history.sqlite")
	createMigration001Database(t, path)
	db := openConfiguredDatabase(t, path)
	store := &Store{db: db, newID: task.NewID}

	err := store.applyMigration(ctx, migration{
		version:  2,
		checksum: strings.Repeat("f", 64),
		sql: `CREATE TABLE migration_probe(value TEXT);
ALTER TABLE tasks RENAME TO tasks_broken;
SELECT * FROM;`,
	})
	if !errors.Is(err, task.ErrStorageUnavailable) {
		t.Fatalf("applyMigration() error = %v", err)
	}
	var foreignKeys int
	if err := db.QueryRow(`PRAGMA foreign_keys`).Scan(&foreignKeys); err != nil || foreignKeys != 1 {
		t.Fatalf("foreign_keys after failed migration = %d, %v", foreignKeys, err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	reopened := openConfiguredDatabase(t, path)
	defer reopened.Close()
	var scenario string
	if err := reopened.QueryRow(`SELECT scenario FROM tasks WHERE task_id=?`, id(120)).Scan(&scenario); err != nil || scenario != "success" {
		t.Fatalf("reopen v1 task scenario = %q, %v", scenario, err)
	}
	var migrationCount int
	if err := reopened.QueryRow(`SELECT COUNT(*) FROM schema_migrations`).Scan(&migrationCount); err != nil || migrationCount != 1 {
		t.Fatalf("migration count after rollback = %d, %v", migrationCount, err)
	}
	var probeCount int
	if err := reopened.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='migration_probe'`).Scan(&probeCount); err != nil || probeCount != 0 {
		t.Fatalf("migration probe survived rollback = %d, %v", probeCount, err)
	}
}

func TestMigrationCancellationRestoresForeignKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.sqlite")
	createMigration001Database(t, path)
	db := openConfiguredDatabase(t, path)
	defer db.Close()
	store := &Store{db: db, newID: task.NewID}

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	err := store.applyMigration(ctx, migration{
		version:  2,
		checksum: strings.Repeat("e", 64),
		sql: `WITH RECURSIVE counter(value) AS (
			SELECT 1
			UNION ALL
			SELECT value + 1 FROM counter WHERE value < 1000000000
		)
		SELECT sum(value) FROM counter;`,
	})
	if !errors.Is(err, task.ErrStorageUnavailable) || !errors.Is(ctx.Err(), context.DeadlineExceeded) {
		t.Fatalf("applyMigration(deadline) error = %v, context = %v", err, ctx.Err())
	}
	var foreignKeys int
	if err := db.QueryRow(`PRAGMA foreign_keys`).Scan(&foreignKeys); err != nil || foreignKeys != 1 {
		t.Fatalf("foreign_keys after cancelled migration = %d, %v", foreignKeys, err)
	}
}

func TestMigrationForeignKeyCheckViolationRollsBack(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "history.sqlite")
	createMigration001Database(t, path)
	db := openConfiguredDatabase(t, path)
	defer db.Close()
	store := &Store{db: db, newID: task.NewID}

	err := store.applyMigration(ctx, migration{
		version:  2,
		checksum: strings.Repeat("d", 64),
		sql: `CREATE TABLE migration_probe(value TEXT);
		INSERT INTO task_events(event_id, task_id, event_type, occurred_at, payload_json)
		VALUES('orphan-event','missing-task','task.created','2026-07-22T08:00:00Z','{}');`,
	})
	if !errors.Is(err, task.ErrStorageUnavailable) {
		t.Fatalf("applyMigration(foreign key violation) error = %v", err)
	}
	var migrationCount, probeCount, orphanCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM schema_migrations`).Scan(&migrationCount); err != nil || migrationCount != 1 {
		t.Fatalf("migration count after foreign-key rollback = %d, %v", migrationCount, err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='migration_probe'`).Scan(&probeCount); err != nil || probeCount != 0 {
		t.Fatalf("migration probe survived foreign-key rollback = %d, %v", probeCount, err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM task_events WHERE event_id='orphan-event'`).Scan(&orphanCount); err != nil || orphanCount != 0 {
		t.Fatalf("orphan event survived foreign-key rollback = %d, %v", orphanCount, err)
	}
	var foreignKeys int
	if err := db.QueryRow(`PRAGMA foreign_keys`).Scan(&foreignKeys); err != nil || foreignKeys != 1 {
		t.Fatalf("foreign_keys after foreign-key rollback = %d, %v", foreignKeys, err)
	}
}

func TestStepPersistenceAndAtomicMutation(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	now := time.Date(2026, 7, 22, 8, 30, 0, 0, time.UTC)
	input := newTask(122, 123, now)
	initialSteps := []task.StepSnapshot{{
		ID: "simulate", Kind: task.StepSimulation, Status: task.StepPending,
	}}
	created, _, err := store.Create(ctx, input, initialSteps, draft(input.ID, task.EventTaskCreated, now))
	if err != nil {
		t.Fatal(err)
	}
	if len(created.Steps) != 1 || created.Steps[0] != initialSteps[0] {
		t.Fatalf("Create() steps = %#v", created.Steps)
	}
	persisted, err := store.Get(ctx, input.ID)
	if err != nil || len(persisted.Steps) != 1 || persisted.Steps[0] != initialSteps[0] {
		t.Fatalf("Get() steps = %#v, %v", persisted.Steps, err)
	}

	startedAt := now.Add(time.Second)
	running := created
	running.Status = task.StatusRunning
	running.StartedAt = ptrTime(startedAt)
	running.ActiveStep = "simulate"
	runningStep := initialSteps[0]
	runningStep.Status = task.StepRunning
	runningStep.StartedAt = ptrTime(startedAt)
	running, events, err := store.Apply(ctx, task.Mutation{
		Task: running, Expected: task.StatusQueued,
		Steps:  []task.StepMutation{{Step: runningStep, Expected: task.StepPending}},
		Events: []task.EventDraft{draft(input.ID, task.EventTaskStarted, startedAt)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || len(running.Steps) != 1 || running.Steps[0].Status != task.StepRunning {
		t.Fatalf("Apply() = %#v, %#v", running, events)
	}

	conflictingTask := running
	conflictingTask.Status = task.StatusFinished
	conflictingTask.Outcome = task.OutcomeSucceeded
	conflictingTask.FinishedAt = ptrTime(now.Add(2 * time.Second))
	conflictingTask.ActiveStep = ""
	succeededStep := runningStep
	succeededStep.Status = task.StepSucceeded
	succeededStep.FinishedAt = conflictingTask.FinishedAt
	_, _, err = store.Apply(ctx, task.Mutation{
		Task: conflictingTask, Expected: task.StatusRunning,
		Steps:  []task.StepMutation{{Step: succeededStep, Expected: task.StepPending}},
		Events: []task.EventDraft{draft(input.ID, task.EventTaskFinished, *conflictingTask.FinishedAt)},
	})
	if !errors.Is(err, task.ErrConflict) {
		t.Fatalf("Apply(step conflict) error = %v", err)
	}
	afterConflict, err := store.Get(ctx, input.ID)
	if err != nil || afterConflict.Status != task.StatusRunning || afterConflict.ActiveStep != "simulate" ||
		len(afterConflict.Steps) != 1 || afterConflict.Steps[0].Status != task.StepRunning ||
		afterConflict.LastSequence != events[0].Sequence {
		t.Fatalf("task after step conflict = %#v, %v", afterConflict, err)
	}
}

func TestStepPersistencePreservesCMakeTaskAndStepOrder(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	now := time.Date(2026, 7, 22, 8, 45, 0, 0, time.UTC)
	input := task.Task{
		ID: id(124), IdempotencyKey: id(125), RequestHash: strings.Repeat("c", 64),
		Kind: task.KindCMakeBuild, Request: json.RawMessage(`{"sourceRoot":"src","buildRoot":"build"}`),
		WorkspaceGeneration: strings.Repeat("d", 64), PlanFingerprint: strings.Repeat("e", 64),
		Timeout: time.Minute, Status: task.StatusQueued, CreatedAt: now,
	}
	steps := []task.StepSnapshot{
		{ID: "configure", Kind: task.StepConfigure, Status: task.StepPending},
		{ID: "build", Kind: task.StepBuild, Status: task.StepPending},
	}
	if _, _, err := store.Create(ctx, input, steps, draft(input.ID, task.EventTaskCreated, now)); err != nil {
		t.Fatal(err)
	}
	got, err := store.Get(ctx, input.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Kind != task.KindCMakeBuild || string(got.Request) != string(input.Request) ||
		got.WorkspaceGeneration != input.WorkspaceGeneration || got.PlanFingerprint != input.PlanFingerprint ||
		got.Scenario != "" || len(got.Steps) != 2 ||
		got.Steps[0].ID != "configure" || got.Steps[0].Kind != task.StepConfigure ||
		got.Steps[1].ID != "build" || got.Steps[1].Kind != task.StepBuild {
		t.Fatalf("persisted cmake task = %#v", got)
	}
}

func TestReopenDoesNotReapplyMigrationAndDetectsChecksumTampering(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.sqlite")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if store != nil {
			_ = store.Close()
		}
	})
	var count int
	var checksum string
	if err := store.db.QueryRow(`SELECT COUNT(*), MIN(sha256) FROM schema_migrations`).Scan(&count, &checksum); err != nil {
		t.Fatal(err)
	}
	if count != 2 || len(checksum) != 64 {
		t.Fatalf("schema_migrations count=%d checksum=%q", count, checksum)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM schema_migrations`).Scan(&count); err != nil || count != 2 {
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

func TestMigrationHistoryMustBeContiguousPrefix(t *testing.T) {
	migrations := []migration{{version: 1}, {version: 2}, {version: 3}}
	if err := validateMigrationPrefix(migrations, map[int]bool{1: true, 3: true}); err == nil {
		t.Fatal("validateMigrationPrefix() error = nil, want gap rejection")
	}
	if err := validateMigrationPrefix(migrations, map[int]bool{1: true, 2: true}); err != nil {
		t.Fatalf("validateMigrationPrefix(contiguous) error = %v", err)
	}
}

func TestReopenRoundTripsCompleteTaskHistory(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "history.sqlite")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	base := time.Date(2026, 7, 22, 9, 0, 0, 123456789, time.FixedZone("east", 8*60*60))
	created := createTask(t, store, newTask(110, 111, base))
	startedAt := base.Add(time.Second)
	running := mustTransition(t, created, task.Transition{From: task.StatusQueued, To: task.StatusRunning, At: startedAt})
	lease := task.ProcessLease{TaskID: created.ID, HostPID: 88, HostStartIdentity: "identity", ServiceInstanceID: id(115)}
	running, startedEvents, err := store.Apply(ctx, task.Mutation{
		Task: running, Expected: task.StatusQueued,
		Events:   []task.EventDraft{{TaskID: created.ID, Type: task.EventTaskStarted, At: startedAt, Payload: json.RawMessage(`{"phase":"running"}`)}},
		PutLease: &lease,
	})
	if err != nil {
		t.Fatal(err)
	}
	finishedAt := base.Add(2 * time.Second)
	finished := mustTransition(t, running, task.Transition{
		From: task.StatusRunning, To: task.StatusFinished, Outcome: task.OutcomeCommandFailed, At: finishedAt,
		ErrorCode: "exit_nonzero", ErrorMessage: "simulated exit 7",
	})
	artifact := task.Artifact{
		ID: id(112), TaskID: created.ID, Kind: "stdout", RelativePath: "history/stdout.txt", MIMEType: "text/plain",
		Size: 7, SHA256: strings.Repeat("b", 64), CreatedAt: finishedAt,
	}
	finishedPayload := json.RawMessage(`{"outcome":"command_failed","exitCode":7}`)
	finished, finishedEvents, err := store.Apply(ctx, task.Mutation{
		Task: finished, Expected: task.StatusRunning,
		Events:      []task.EventDraft{{TaskID: created.ID, Type: task.EventTaskFinished, At: finishedAt, Payload: finishedPayload}},
		DeleteLease: true,
		Artifacts:   []task.Artifact{artifact},
	})
	if err != nil {
		t.Fatal(err)
	}
	queued := createTask(t, store, newTask(113, 114, base.Add(3*time.Second)))
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	gotFinished, err := reopened.Get(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if gotFinished.Status != task.StatusFinished || gotFinished.Outcome != task.OutcomeCommandFailed ||
		gotFinished.StartedAt == nil || !gotFinished.StartedAt.Equal(startedAt) ||
		gotFinished.FinishedAt == nil || !gotFinished.FinishedAt.Equal(finishedAt) ||
		!gotFinished.CreatedAt.Equal(base) || gotFinished.ErrorCode != "exit_nonzero" ||
		gotFinished.ErrorMessage != "simulated exit 7" || gotFinished.LastSequence != finishedEvents[0].Sequence {
		t.Fatalf("reopened finished task = %#v", gotFinished)
	}
	gotQueued, err := reopened.Get(ctx, queued.ID)
	if err != nil || gotQueued.Outcome != "" || gotQueued.StartedAt != nil || gotQueued.FinishedAt != nil ||
		gotQueued.ErrorCode != "" || gotQueued.ErrorMessage != "" {
		t.Fatalf("reopened queued task = %#v, %v", gotQueued, err)
	}
	watermark, err := reopened.Watermark(ctx)
	if err != nil {
		t.Fatal(err)
	}
	events, err := reopened.EventsAfter(ctx, 0, watermark, 10)
	if err != nil || len(events) != 4 || events[1].Sequence != startedEvents[0].Sequence ||
		!events[1].At.Equal(startedAt) || string(events[1].Payload) != `{"phase":"running"}` ||
		events[2].Sequence != finishedEvents[0].Sequence || !events[2].At.Equal(finishedAt) ||
		string(events[2].Payload) != string(finishedPayload) {
		t.Fatalf("reopened events = %#v, %v", events, err)
	}
	gotArtifact, err := reopened.GetArtifact(ctx, artifact.ID)
	if err != nil || gotArtifact.ID != artifact.ID || gotArtifact.TaskID != artifact.TaskID ||
		gotArtifact.Kind != artifact.Kind || gotArtifact.RelativePath != artifact.RelativePath ||
		gotArtifact.MIMEType != artifact.MIMEType || gotArtifact.Size != artifact.Size ||
		gotArtifact.SHA256 != artifact.SHA256 || !gotArtifact.CreatedAt.Equal(artifact.CreatedAt) {
		t.Fatalf("reopened artifact = %#v, %v", gotArtifact, err)
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
		Kind: task.KindSimulation, Request: json.RawMessage(`{"scenario":"success"}`),
		Scenario: task.ScenarioSuccess, Timeout: 30 * time.Second, Status: task.StatusQueued, CreatedAt: at,
	}
}

func newCMakeTask(taskByte, keyByte byte, at time.Time) task.Task {
	return task.Task{
		ID: id(taskByte), IdempotencyKey: id(keyByte), RequestHash: strings.Repeat("b", 64),
		Kind: task.KindCMakeBuild, Request: json.RawMessage(`{"sourceRoot":"src","buildRoot":"build"}`),
		WorkspaceGeneration: strings.Repeat("c", 64), PlanFingerprint: strings.Repeat("d", 64),
		Timeout: time.Minute, Status: task.StatusQueued, CreatedAt: at,
	}
}

func putTestLease(t *testing.T, store *Store, value task.Task, pid int) {
	t.Helper()
	lease := task.ProcessLease{
		TaskID: value.ID, HostPID: pid, HostStartIdentity: fmt.Sprintf("start-%d", pid),
		ServiceInstanceID: id(20),
	}
	if _, _, err := store.Apply(context.Background(), task.Mutation{
		Task: value, Expected: value.Status, PutLease: &lease,
	}); err != nil {
		t.Fatal(err)
	}
}

func startStoredStep(t *testing.T, store *Store, value task.Task, ordinal int, at time.Time, pid int) task.Task {
	t.Helper()
	running := mustTransition(t, value, task.Transition{
		From: task.StatusQueued, To: task.StatusRunning, At: at,
	})
	step := value.Steps[ordinal]
	step.Status = task.StepRunning
	step.StartedAt = ptrTime(at)
	running.ActiveStep = step.ID
	lease := task.ProcessLease{
		TaskID: value.ID, HostPID: pid, HostStartIdentity: fmt.Sprintf("start-%d", pid),
		ServiceInstanceID: id(21),
	}
	stored, _, err := store.Apply(context.Background(), task.Mutation{
		Task: running, Expected: task.StatusQueued,
		Steps:    []task.StepMutation{{Step: step, Expected: task.StepPending}},
		Events:   []task.EventDraft{draft(value.ID, task.EventTaskStarted, at)},
		PutLease: &lease,
	})
	if err != nil {
		t.Fatal(err)
	}
	return stored
}

func createTask(t *testing.T, store *Store, input task.Task) task.Task {
	t.Helper()
	created, _, err := store.Create(context.Background(), input, nil, draft(input.ID, task.EventTaskCreated, input.CreatedAt))
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

func createMigration001Database(t *testing.T, path string) {
	t.Helper()
	db := openConfiguredDatabase(t, path)
	migrations, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE schema_migrations (
		version INTEGER PRIMARY KEY,
		sha256 TEXT NOT NULL,
		applied_at TEXT NOT NULL
	)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(migrations[0].sql); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO schema_migrations(version, sha256, applied_at) VALUES(1,?,?)`,
		migrations[0].checksum, formatTime(time.Date(2026, 7, 22, 8, 0, 0, 0, time.UTC))); err != nil {
		t.Fatal(err)
	}
	insert := `INSERT INTO tasks(
		task_id, idempotency_key, request_hash, kind, scenario, timeout_ms, status, outcome,
		created_at, started_at, finished_at, last_sequence, error_code, error_message
	) VALUES(?,?,?,'simulation','success',30000,?,?,?,?,?,0,'','')`
	if _, err := db.Exec(insert, id(120), id(122), strings.Repeat("a", 64), "finished", "succeeded",
		"2026-07-22T08:00:00Z", "2026-07-22T08:00:01Z", "2026-07-22T08:00:02Z"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(insert, id(121), id(123), strings.Repeat("b", 64), "queued", nil,
		"2026-07-22T08:01:00Z", nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO task_events(
		event_id, task_id, event_type, occurred_at, payload_json
	) VALUES(?,?,'task.created','2026-07-22T08:00:00Z','{}')`, id(124), id(120)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO process_leases(
		task_id, host_pid, host_start_identity, service_instance_id
	) VALUES(?,42,'start',?)`, id(121), id(125)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO artifacts(
		artifact_id, task_id, kind, relative_path, mime_type, size_bytes, sha256, created_at, complete
	) VALUES(?,?,'stdout','migration/stdout.txt','text/plain',7,?,'2026-07-22T08:00:02Z',1)`,
		id(126), id(120), strings.Repeat("c", 64)); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
}

func openConfiguredDatabase(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	for _, pragma := range []string{"PRAGMA journal_mode=WAL", "PRAGMA foreign_keys=ON", "PRAGMA synchronous=FULL"} {
		if _, err := db.Exec(pragma); err != nil {
			_ = db.Close()
			t.Fatal(err)
		}
	}
	return db
}

func foreignKeyViolations(t *testing.T, db *sql.DB) int {
	t.Helper()
	rows, err := db.Query(`PRAGMA foreign_key_check`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		count++
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return count
}

var _ task.Store = (*Store)(nil)
