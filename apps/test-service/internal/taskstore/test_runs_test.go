package taskstore

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"unit-test-ide.local/test-service/internal/task"
	"unit-test-ide.local/test-service/internal/testdomain"
)

func TestMigration006UpgradeAndFailureRollback(t *testing.T) {
	ctx := context.Background()
	migrations, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	if len(migrations) < 6 || migrations[5].version != 6 {
		t.Fatalf("loaded migrations = %#v", migrations)
	}
	db := openConfiguredDatabase(t, filepath.Join(t.TempDir(), "migration.sqlite"))
	t.Cleanup(func() { _ = db.Close() })
	store := &Store{db: db, newID: task.NewID}
	if _, err := db.Exec(`CREATE TABLE schema_migrations (
		version INTEGER PRIMARY KEY,
		sha256 TEXT NOT NULL,
		applied_at TEXT NOT NULL
	)`); err != nil {
		t.Fatal(err)
	}
	for _, migration := range migrations[:5] {
		if err := store.applyMigration(ctx, migration); err != nil {
			t.Fatal(err)
		}
	}
	broken := migrations[5]
	broken.sql += "\nINSERT INTO missing_test_run_table(value) VALUES(1);"
	if err := store.applyMigration(ctx, broken); !errors.Is(
		err,
		task.ErrStorageUnavailable,
	) {
		t.Fatalf("broken migration 006 error = %v", err)
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM schema_migrations`).
		Scan(&count); err != nil || count != 5 {
		t.Fatalf("migration count after rollback = %d, %v", count, err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master
		WHERE type='table' AND name IN (
			'test_runs','test_run_results','test_run_artifacts'
		)`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("TestRun tables after rollback = %d, %v", count, err)
	}
	if err := store.applyMigration(ctx, migrations[5]); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master
		WHERE type='table' AND name IN (
			'test_runs','test_run_results','test_run_artifacts'
		)`).Scan(&count); err != nil || count != 3 {
		t.Fatalf("TestRun tables after upgrade = %d, %v", count, err)
	}
}

func TestRunAppendFinishAndRestartAreAtomic(t *testing.T) {
	ctx := context.Background()
	database := filepath.Join(t.TempDir(), "history.sqlite")
	store, err := Open(database)
	if err != nil {
		t.Fatal(err)
	}
	createdTask := createTask(
		t,
		store,
		newTask(100, 101, time.Date(2026, 7, 31, 1, 0, 0, 0, time.UTC)),
	)
	run := runFixture(createdTask, 102, stableID("1"))
	if err := store.CreateRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	run.SelectionSnapshot.ItemIDs[0] = stableID("9")
	persisted, err := store.GetRun(ctx, run.RunID)
	if err != nil || persisted.SelectionSnapshot.ItemIDs[0] != stableID("1") {
		t.Fatalf("immutable selection = %#v, %v", persisted, err)
	}

	partial := resultFixture(stableID("1"), stableID("2"))
	partial.Outcome = testdomain.ItemFailed
	partial.Partial = true
	partial.FailureDetails = []testdomain.FailureDetail{failureFixture()}
	if err := store.AppendResult(ctx, run.RunID, partial); err != nil {
		t.Fatal(err)
	}
	final := resultFixture(stableID("1"), stableID("2"))
	wrongContainer := final
	wrongContainer.ContainerID = stableID("3")
	if err := store.AppendResult(
		ctx,
		run.RunID,
		wrongContainer,
	); !errors.Is(err, task.ErrConflict) {
		t.Fatalf("changed result container error = %v", err)
	}
	if err := store.AppendResult(ctx, run.RunID, final); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendResult(ctx, run.RunID, final); err != nil {
		t.Fatalf("idempotent AppendResult() error = %v", err)
	}
	conflict := final
	conflict.Outcome = testdomain.ItemSkipped
	if err := store.AppendResult(ctx, run.RunID, conflict); !errors.Is(
		err,
		task.ErrConflict,
	) {
		t.Fatalf("non-monotonic AppendResult() error = %v", err)
	}

	persisted, err = store.GetRun(ctx, run.RunID)
	if err != nil || len(persisted.Results) != 1 ||
		persisted.Results[0].Outcome != testdomain.ItemPassed ||
		persisted.ResultRevision == testdomain.EmptyResultRevision() {
		t.Fatalf("persisted results = %#v, %v", persisted, err)
	}
	started := run.CreatedAt.Add(time.Second)
	finished := started.Add(time.Second)
	terminal := persisted
	terminal.Results = nil
	terminal.Status = testdomain.RunCompleted
	terminal.Outcome = testdomain.RunPassed
	terminal.StartedAt = &started
	terminal.FinishedAt = &finished
	terminal.Summary = testdomain.RunSummary{
		Total: 1, Completed: 1, Passed: 1, Iterations: 1,
	}
	terminal.Incomplete = false
	artifact := task.Artifact{
		ID: id(103), TaskID: run.TaskID, Kind: "test-run-summary",
		RelativePath: "test/run-summary.json",
		MIMEType:     "application/json",
		Size:         2,
		SHA256:       strings.Repeat("a", 64),
		CreatedAt:    finished,
	}
	if err := store.FinishRun(ctx, terminal, []task.Artifact{artifact}); err != nil {
		t.Fatal(err)
	}
	if err := store.FinishRun(
		ctx,
		terminal,
		[]task.Artifact{artifact},
	); err != nil {
		t.Fatalf("idempotent FinishRun() error = %v", err)
	}
	if err := store.FinishRun(ctx, terminal, nil); !errors.Is(
		err,
		task.ErrConflict,
	) {
		t.Fatalf("changed terminal artifact set error = %v", err)
	}
	gotArtifact, err := store.GetArtifact(ctx, artifact.ID)
	if err != nil || gotArtifact.ID != artifact.ID {
		t.Fatalf("GetArtifact() = %#v, %v", gotArtifact, err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = Open(database)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	wantResult, err := testdomain.NewTestItemResult(final)
	if err != nil {
		t.Fatal(err)
	}
	restarted, err := store.GetRun(ctx, run.RunID)
	if err != nil || restarted.Status != testdomain.RunCompleted ||
		restarted.Outcome != testdomain.RunPassed ||
		!reflect.DeepEqual(
			restarted.Results,
			[]testdomain.TestItemResult{wantResult},
		) {
		t.Fatalf("restarted TestRun = %#v, %v", restarted, err)
	}
	if err := store.AppendResult(ctx, run.RunID, final); !errors.Is(
		err,
		task.ErrConflict,
	) {
		t.Fatalf("terminal append error = %v", err)
	}
}

func TestRunFinishArtifactFailureKeepsRunNonTerminal(t *testing.T) {
	store := openTestStore(t)
	input := createTask(
		t,
		store,
		newTask(110, 111, time.Date(2026, 7, 31, 2, 0, 0, 0, time.UTC)),
	)
	run := runFixture(input, 112, stableID("3"))
	if err := store.CreateRun(context.Background(), run); err != nil {
		t.Fatal(err)
	}
	result := resultFixture(stableID("3"), stableID("4"))
	if err := store.AppendResult(context.Background(), run.RunID, result); err != nil {
		t.Fatal(err)
	}
	current, err := store.GetRun(context.Background(), run.RunID)
	if err != nil {
		t.Fatal(err)
	}
	started, finished := run.CreatedAt.Add(time.Second), run.CreatedAt.Add(2*time.Second)
	current.Results = nil
	current.Status = testdomain.RunCompleted
	current.Outcome = testdomain.RunPassed
	current.StartedAt, current.FinishedAt = &started, &finished
	current.Summary = testdomain.RunSummary{
		Total: 1, Completed: 1, Passed: 1, Iterations: 1,
	}
	current.Incomplete = false
	invalidArtifact := task.Artifact{
		ID: id(113), TaskID: run.TaskID, Kind: "test-run-summary",
		RelativePath: "../escape.json",
		MIMEType:     "application/json",
		SHA256:       strings.Repeat("a", 64),
		CreatedAt:    finished,
	}
	if err := store.FinishRun(
		context.Background(),
		current,
		[]task.Artifact{invalidArtifact},
	); !errors.Is(err, task.ErrInvalidArgument) {
		t.Fatalf("FinishRun() error = %v", err)
	}
	persisted, err := store.GetRun(context.Background(), run.RunID)
	if err != nil || persisted.Status == testdomain.RunCompleted {
		t.Fatalf("run after failed finish = %#v, %v", persisted, err)
	}
}

func TestRunFinishRequiresIncompleteForPartialOrNotRunResults(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	input := createTask(
		t,
		store,
		newTask(114, 115, time.Date(2026, 7, 31, 2, 30, 0, 0, time.UTC)),
	)
	run := runFixture(input, 116, stableID("4"))
	if err := store.CreateRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	partial := resultFixture(stableID("4"), stableID("5"))
	partial.Outcome = testdomain.ItemErrored
	partial.Partial = true
	if err := store.AppendResult(ctx, run.RunID, partial); err != nil {
		t.Fatal(err)
	}
	current, err := store.GetRun(ctx, run.RunID)
	if err != nil {
		t.Fatal(err)
	}
	started := run.CreatedAt.Add(time.Second)
	finished := started.Add(time.Second)
	current.Results = nil
	current.Status = testdomain.RunCompleted
	current.Outcome = testdomain.RunErrored
	current.StartedAt, current.FinishedAt = &started, &finished
	current.Summary = testdomain.RunSummary{
		Total: 1, Completed: 1, Errored: 1, Iterations: 1,
	}
	current.Incomplete = false
	if err := store.FinishRun(ctx, current, nil); !errors.Is(
		err,
		task.ErrInvalidArgument,
	) {
		t.Fatalf("complete terminal with partial result error = %v", err)
	}
	persisted, err := store.GetRun(ctx, run.RunID)
	if err != nil || persisted.Status == testdomain.RunCompleted {
		t.Fatalf("run after inconsistent finish = %#v, %v", persisted, err)
	}
	current.Incomplete = true
	if err := store.FinishRun(ctx, current, nil); err != nil {
		t.Fatalf("incomplete terminal error = %v", err)
	}
}

func TestRunIdempotencyPaginationAndFailureDetails(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	base := time.Date(2026, 7, 31, 3, 0, 0, 0, time.UTC)
	firstTask := createTask(t, store, newTask(120, 121, base))
	first := runFixture(firstTask, 122, stableID("5"), stableID("6"))
	if err := store.CreateRun(ctx, first); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateRun(ctx, first); err != nil {
		t.Fatalf("idempotent CreateRun() error = %v", err)
	}
	conflictTask := createTask(t, store, newTask(123, 124, base.Add(time.Second)))
	duplicateRunID := first
	duplicateRunID.TaskID = conflictTask.ID
	duplicateRunID.IdempotencyKey = strings.Repeat("0", 32)
	duplicateRunID.CreatedAt = conflictTask.CreatedAt
	if err := store.CreateRun(ctx, duplicateRunID); !errors.Is(
		err,
		task.ErrConflict,
	) {
		t.Fatalf("duplicate runId error = %v", err)
	}
	duplicateTaskID := first
	duplicateTaskID.RunID = strings.Repeat("1", 32)
	duplicateTaskID.IdempotencyKey = strings.Repeat("2", 32)
	if err := store.CreateRun(ctx, duplicateTaskID); !errors.Is(
		err,
		task.ErrConflict,
	) {
		t.Fatalf("duplicate taskId error = %v", err)
	}
	conflict := runFixture(conflictTask, 125, stableID("7"))
	conflict.IdempotencyKey = first.IdempotencyKey
	if err := store.CreateRun(ctx, conflict); !errors.Is(
		err,
		task.ErrIdempotencyConflict,
	) {
		t.Fatalf("idempotency conflict error = %v", err)
	}

	secondTask := createTask(t, store, newTask(122, 125, base.Add(2*time.Second)))
	second := runFixture(secondTask, 124, stableID("8"))
	if err := store.CreateRun(ctx, second); err != nil {
		t.Fatal(err)
	}
	page, err := store.ListRuns(ctx, testdomain.RunPageRequest{
		ProjectID: first.ProjectID, ProfileID: first.ProfileID, Limit: 1,
	})
	if err != nil || len(page.Items) != 1 || page.NextCursor == "" ||
		page.Items[0].RunID != second.RunID {
		t.Fatalf("first run page = %#v, %v", page, err)
	}
	if _, err := store.ListRuns(ctx, testdomain.RunPageRequest{
		ProjectID: "other", ProfileID: first.ProfileID,
		Cursor: page.NextCursor, Limit: 1,
	}); !errors.Is(err, task.ErrInvalidArgument) {
		t.Fatalf("cursor query mismatch error = %v", err)
	}

	failed := resultFixture(stableID("5"), stableID("a"))
	failed.Outcome = testdomain.ItemFailed
	failed.FailureDetails = []testdomain.FailureDetail{failureFixture()}
	crashed := resultFixture(stableID("6"), stableID("a"))
	crashed.Outcome = testdomain.ItemErrored
	crashed.FailureDetails = []testdomain.FailureDetail{{
		Category:  "test_process_crash",
		Message:   "test process exited before the case finished",
		Locations: []testdomain.SourceLocation{},
		EvidenceRefs: []string{
			id(131),
		},
	}}
	if err := store.AppendResult(ctx, first.RunID, failed); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendResult(ctx, first.RunID, crashed); err != nil {
		t.Fatal(err)
	}
	current, err := store.GetRun(ctx, first.RunID)
	if err != nil || len(current.Results) != 2 ||
		len(current.Results[0].FailureDetails) != 1 ||
		len(current.Results[0].FailureDetails[0].Locations) != 2 ||
		current.Results[0].FailureDetails[0].Expected != "7" ||
		current.Results[1].FailureDetails[0].Category != "test_process_crash" {
		t.Fatalf("persisted failure details = %#v, %v", current.Results, err)
	}
	started, finished := base.Add(time.Second), base.Add(2*time.Second)
	current.Results = nil
	current.Status = testdomain.RunCompleted
	current.Outcome = testdomain.RunFailed
	current.StartedAt, current.FinishedAt = &started, &finished
	current.Summary = testdomain.RunSummary{
		Total: 2, Completed: 2, Failed: 1, Errored: 1, Iterations: 1,
	}
	current.Incomplete = false
	if err := store.FinishRun(ctx, current, nil); err != nil {
		t.Fatal(err)
	}
}

func runFixture(
	input task.Task,
	seed byte,
	itemIDs ...testdomain.ID,
) testdomain.TestRun {
	return testdomain.TestRun{
		RunID:           id(seed),
		TaskID:          input.ID,
		IdempotencyKey:  id(seed + 1),
		ProjectID:       "core",
		ProfileID:       strings.Repeat("b", 64),
		ToolchainID:     "msvc",
		CatalogRevision: strings.Repeat("c", 64),
		SelectionSnapshot: testdomain.SelectionSnapshot{
			Mode:    testdomain.SelectionItems,
			ItemIDs: append([]testdomain.ID(nil), itemIDs...),
		},
		Status:         testdomain.RunQueued,
		Summary:        testdomain.RunSummary{Iterations: 1},
		ResultRevision: testdomain.EmptyResultRevision(),
		Incomplete:     true,
		CreatedAt:      input.CreatedAt,
	}
}

func resultFixture(
	itemID testdomain.ID,
	containerID testdomain.ID,
) testdomain.TestItemResult {
	duration := int64(5)
	return testdomain.TestItemResult{
		ItemID: itemID, ContainerID: containerID, Iteration: 1,
		Outcome: testdomain.ItemPassed, DurationMS: &duration,
		FailureDetails: []testdomain.FailureDetail{},
		OutputRefs:     []string{},
	}
}

func failureFixture() testdomain.FailureDetail {
	return testdomain.FailureDetail{
		Category: "assertion_failure",
		Subtype:  testdomain.FailureSubtypeMockParameterMismatch,
		Message:  "Expected 7 Was 20",
		Expected: "7",
		Actual:   "20",
		Locations: []testdomain.SourceLocation{
			{
				URI: "file:///workspace/tests/test.c", Line: 12,
				Navigable: true, Provenance: "framework-output",
			},
			{
				URI: "file:///workspace/mocks/mock.c", Line: 30,
				Navigable: true, Provenance: "mock-actual-call",
			},
		},
		EvidenceRefs: []string{id(130)},
	}
}

func stableID(character string) testdomain.ID {
	return testdomain.ID("utid-v1-" + strings.Repeat(character, 64))
}
