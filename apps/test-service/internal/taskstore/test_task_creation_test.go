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
	"unit-test-ide.local/test-service/internal/testdomain"
)

func TestMigration007PreservesTaskRelationsAndRollsBackFailure(
	t *testing.T,
) {
	ctx := context.Background()
	migrations, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	if len(migrations) < 7 || migrations[6].version != 7 {
		t.Fatalf("loaded migrations = %#v", migrations)
	}
	db := openConfiguredDatabase(
		t,
		filepath.Join(t.TempDir(), "migration.sqlite"),
	)
	t.Cleanup(func() { _ = db.Close() })
	store := &Store{db: db, newID: task.NewID}
	if _, err := db.Exec(`CREATE TABLE schema_migrations (
		version INTEGER PRIMARY KEY,
		sha256 TEXT NOT NULL,
		applied_at TEXT NOT NULL
	)`); err != nil {
		t.Fatal(err)
	}
	for _, migration := range migrations[:6] {
		if err := store.applyMigration(ctx, migration); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Date(2026, 7, 31, 7, 30, 0, 0, time.UTC)
	legacy := newCMakeTask(165, 166, now)
	legacy = createTask(t, store, legacy)
	run := runFixture(legacy, 167, stableID("1"))
	if err := store.CreateRun(ctx, run); err != nil {
		t.Fatal(err)
	}

	broken := migrations[6]
	broken.sql +=
		"\nINSERT INTO missing_test_task_table(value) VALUES(1);"
	if err := store.applyMigration(ctx, broken); !errors.Is(
		err,
		task.ErrStorageUnavailable,
	) {
		t.Fatalf("broken migration 007 error = %v", err)
	}
	var count int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM schema_migrations`,
	).Scan(&count); err != nil || count != 6 {
		t.Fatalf("migration count after rollback = %d, %v", count, err)
	}
	if persisted, err := store.GetRun(ctx, run.RunID); err != nil ||
		persisted.TaskID != legacy.ID {
		t.Fatalf("legacy TestRun after rollback = %#v, %v", persisted, err)
	}

	if err := store.applyMigration(ctx, migrations[6]); err != nil {
		t.Fatal(err)
	}
	if persisted, err := store.Get(ctx, legacy.ID); err != nil ||
		persisted.Kind != task.KindCMakeBuild {
		t.Fatalf("legacy Task after upgrade = %#v, %v", persisted, err)
	}
	if persisted, err := store.GetRun(ctx, run.RunID); err != nil ||
		persisted.TaskID != legacy.ID {
		t.Fatalf("legacy TestRun after upgrade = %#v, %v", persisted, err)
	}
}

func TestCreateTestTaskPersistsTaskAndRunAtomically(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	now := time.Date(2026, 7, 31, 8, 0, 0, 0, time.UTC)
	input := testRunTaskFixture(170, 171, now)
	run := runFixture(input, 172, stableID("1"))

	created, events, err := store.CreateTestTask(
		ctx,
		input,
		[]task.StepSnapshot{{
			ID:   "build",
			Kind: task.StepBuild, Status: task.StepPending,
		}},
		draft(input.ID, task.EventTaskCreated, now),
		run,
	)
	if err != nil {
		t.Fatal(err)
	}
	persisted, err := store.GetRun(ctx, run.RunID)
	if err != nil ||
		created.Kind != task.KindTestRun ||
		persisted.TaskID != created.ID ||
		persisted.SelectionSnapshot.ItemIDs[0] !=
			run.SelectionSnapshot.ItemIDs[0] ||
		len(events) != 1 {
		t.Fatalf(
			"created task/run = %#v / %#v / %#v / %v",
			created,
			persisted,
			events,
			err,
		)
	}

	replayed := input
	replayed.ID = id(173)
	replayedRun := run
	replayedRun.TaskID = replayed.ID
	got, replayEvents, err := store.CreateTestTask(
		ctx,
		replayed,
		[]task.StepSnapshot{{
			ID:   "build",
			Kind: task.StepBuild, Status: task.StepPending,
		}},
		draft(replayed.ID, task.EventTaskCreated, now),
		replayedRun,
	)
	if err != nil || got.ID != created.ID || len(replayEvents) != 0 {
		t.Fatalf(
			"idempotent CreateTestTask() = %#v, %#v, %v",
			got,
			replayEvents,
			err,
		)
	}
}

func TestCreateTestTaskRollsBackTaskWhenRunInsertFails(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	now := time.Date(2026, 7, 31, 8, 30, 0, 0, time.UTC)
	input := testRunTaskFixture(174, 175, now)
	run := runFixture(input, 176, stableID("1"))
	if _, err := store.db.Exec(`CREATE TRIGGER reject_test_run_create
		BEFORE INSERT ON test_runs
		BEGIN
			SELECT RAISE(ABORT, 'injected TestRun failure');
		END`); err != nil {
		t.Fatal(err)
	}

	if _, _, err := store.CreateTestTask(
		ctx,
		input,
		[]task.StepSnapshot{{
			ID:   "test-000001",
			Kind: task.StepTestRun, Status: task.StepPending,
		}},
		draft(input.ID, task.EventTaskCreated, now),
		run,
	); !errors.Is(err, task.ErrStorageUnavailable) {
		t.Fatalf("CreateTestTask() error = %v", err)
	}
	if _, err := store.Get(ctx, input.ID); !errors.Is(
		err,
		task.ErrNotFound,
	) {
		t.Fatalf("rolled-back task lookup error = %v", err)
	}
}

func TestRebindQueuedRunAdvancesCatalogBeforeAnyResult(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	now := time.Date(2026, 7, 31, 9, 30, 0, 0, time.UTC)
	input := testRunTaskFixture(177, 178, now)
	oldCatalog := catalogFixture(t, "8", "old")
	newCatalog := catalogFixture(t, "9", "new")
	run := runFixture(input, 179, oldCatalog.Items[0].ID)
	run.ProfileID = oldCatalog.ProfileID
	run.CatalogRevision = oldCatalog.Revision
	run.SelectionSnapshot.ItemIDs = []testdomain.ID{
		newCatalog.Items[0].ID,
	}
	if _, _, err := store.CreateTestTask(
		ctx,
		input,
		[]task.StepSnapshot{{
			ID: "build", Kind: task.StepBuild,
			Status: task.StepPending,
		}},
		draft(input.ID, task.EventTaskCreated, now),
		run,
	); err != nil {
		t.Fatal(err)
	}
	selection := testdomain.SelectionSnapshot{
		Mode: testdomain.SelectionItems,
		ItemIDs: []testdomain.ID{
			newCatalog.Items[0].ID,
		},
	}
	if err := store.RebindQueuedRun(
		ctx,
		run.RunID,
		oldCatalog.Revision,
		newCatalog,
		selection,
	); err != nil {
		t.Fatal(err)
	}
	persisted, err := store.GetRun(ctx, run.RunID)
	if err != nil ||
		persisted.CatalogRevision != newCatalog.Revision ||
		persisted.SelectionSnapshot.ItemIDs[0] !=
			newCatalog.Items[0].ID {
		t.Fatalf("rebound TestRun = %#v, %v", persisted, err)
	}
	if err := store.RebindQueuedRun(
		ctx,
		run.RunID,
		oldCatalog.Revision,
		newCatalog,
		selection,
	); err != nil {
		t.Fatalf("idempotent RebindQueuedRun() error = %v", err)
	}
}

func testRunTaskFixture(taskByte, keyByte byte, at time.Time) task.Task {
	return task.Task{
		ID: id(taskByte), IdempotencyKey: id(keyByte),
		RequestHash: strings.Repeat("a", 64),
		Kind:        task.KindTestRun,
		Request: json.RawMessage(
			`{"projectId":"core","buildProfileId":"` +
				strings.Repeat("b", 64) + `"}`,
		),
		WorkspaceGeneration: strings.Repeat("c", 64),
		PlanFingerprint:     strings.Repeat("d", 64),
		Timeout:             time.Minute,
		Status:              task.StatusQueued,
		CreatedAt:           at,
	}
}
