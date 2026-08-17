package taskstore

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"unit-test-ide.local/test-service/internal/coveragedomain"
	"unit-test-ide.local/test-service/internal/task"
	"unit-test-ide.local/test-service/internal/testdomain"
)

func TestCreateCoverageTaskPersistsRelationsAtomically(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	input, steps, event, run, testRun := coverageCreationFixture(t, 1)

	created, events, err := store.CreateCoverageTask(ctx, input, steps, event, run, testRun)
	if err != nil {
		t.Fatal(err)
	}
	persistedRun, err := store.GetCoverageRun(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	persistedTestRun, err := store.GetRun(ctx, testRun.RunID)
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := run.Request.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	persistedCanonical, err := persistedRun.Request.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	if created.Kind != task.KindCoverageRun || len(created.Steps) != len(steps) ||
		len(events) != 1 || events[0].Type != task.EventTaskCreated ||
		created.LastSequence == 0 || created.LastSequence != persistedRun.LastSequence ||
		!bytes.Equal(input.Request, canonical) || !bytes.Equal(persistedCanonical, canonical) ||
		persistedRun.TaskID != created.ID || persistedRun.TestRunID != persistedTestRun.RunID ||
		persistedRun.Request.WorkspaceGeneration != created.WorkspaceGeneration ||
		persistedRun.Request.ProjectID != persistedTestRun.ProjectID ||
		persistedRun.Request.CatalogRevision != persistedTestRun.CatalogRevision ||
		persistedRun.Request.Timeout != created.Timeout ||
		persistedRun.Request.RepeatCount != persistedTestRun.Summary.Iterations ||
		!reflect.DeepEqual(persistedRun.SelectionSnapshot, persistedTestRun.SelectionSnapshot) {
		t.Fatalf("created coverage aggregate is misaligned: task=%#v run=%#v testRun=%#v events=%#v", created, persistedRun, persistedTestRun, events)
	}
}

func TestCreateCoverageTaskRejectsInvalidAlignmentWithoutRows(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name   string
		mutate func(*task.Task, *[]task.StepSnapshot, *task.EventDraft, *coveragedomain.Run, *testdomain.TestRun)
	}{
		{"task kind", func(input *task.Task, _ *[]task.StepSnapshot, _ *task.EventDraft, _ *coveragedomain.Run, _ *testdomain.TestRun) {
			input.Kind = task.KindTestRun
		}},
		{"task idempotency", func(input *task.Task, _ *[]task.StepSnapshot, _ *task.EventDraft, run *coveragedomain.Run, _ *testdomain.TestRun) {
			input.IdempotencyKey = coverageHex(900)
			run.Request.IdempotencyKey = input.IdempotencyKey
		}},
		{"task workspace", func(input *task.Task, _ *[]task.StepSnapshot, _ *task.EventDraft, _ *coveragedomain.Run, _ *testdomain.TestRun) {
			input.WorkspaceGeneration = strings.Repeat("f", 64)
		}},
		{"task request", func(input *task.Task, _ *[]task.StepSnapshot, _ *task.EventDraft, _ *coveragedomain.Run, _ *testdomain.TestRun) {
			input.Request = json.RawMessage(`{}`)
		}},
		{"task timeout", func(input *task.Task, _ *[]task.StepSnapshot, _ *task.EventDraft, _ *coveragedomain.Run, _ *testdomain.TestRun) {
			input.Timeout = time.Second
		}},
		{"coverage task", func(input *task.Task, _ *[]task.StepSnapshot, _ *task.EventDraft, run *coveragedomain.Run, _ *testdomain.TestRun) {
			run.TaskID = coverageHex(901)
			_ = input
		}},
		{"coverage test run", func(_ *task.Task, _ *[]task.StepSnapshot, _ *task.EventDraft, run *coveragedomain.Run, _ *testdomain.TestRun) {
			run.TestRunID = coverageHex(902)
		}},
		{"coverage status", func(_ *task.Task, _ *[]task.StepSnapshot, _ *task.EventDraft, run *coveragedomain.Run, _ *testdomain.TestRun) {
			run.Status = coveragedomain.StatusRunning
			now := run.CreatedAt.Add(time.Second)
			run.StartedAt = &now
		}},
		{"coverage sequence", func(_ *task.Task, _ *[]task.StepSnapshot, _ *task.EventDraft, run *coveragedomain.Run, _ *testdomain.TestRun) {
			run.LastSequence = 1
		}},
		{"test run task", func(_ *task.Task, _ *[]task.StepSnapshot, _ *task.EventDraft, _ *coveragedomain.Run, run *testdomain.TestRun) {
			run.TaskID = coverageHex(903)
		}},
		{"test run project", func(_ *task.Task, _ *[]task.StepSnapshot, _ *task.EventDraft, _ *coveragedomain.Run, run *testdomain.TestRun) {
			run.ProjectID = "other"
		}},
		{"test run catalog", func(_ *task.Task, _ *[]task.StepSnapshot, _ *task.EventDraft, _ *coveragedomain.Run, run *testdomain.TestRun) {
			run.CatalogRevision = strings.Repeat("e", 64)
		}},
		{"test run selection", func(_ *task.Task, _ *[]task.StepSnapshot, _ *task.EventDraft, _ *coveragedomain.Run, run *testdomain.TestRun) {
			run.SelectionSnapshot = testdomain.SelectionSnapshot{Mode: testdomain.SelectionItems, ItemIDs: []testdomain.ID{stableID("b")}}
		}},
		{"test run iterations", func(_ *task.Task, _ *[]task.StepSnapshot, _ *task.EventDraft, _ *coveragedomain.Run, run *testdomain.TestRun) {
			run.Summary.Iterations++
		}},
		{"test run nonzero queued summary", func(_ *task.Task, _ *[]task.StepSnapshot, _ *task.EventDraft, _ *coveragedomain.Run, run *testdomain.TestRun) {
			run.Summary.Total, run.Summary.Completed, run.Summary.Passed = 1, 1, 1
		}},
		{"test run status", func(_ *task.Task, _ *[]task.StepSnapshot, _ *task.EventDraft, _ *coveragedomain.Run, run *testdomain.TestRun) {
			run.Status = testdomain.RunRunning
			now := run.CreatedAt.Add(time.Second)
			run.StartedAt = &now
		}},
		{"event owner", func(input *task.Task, _ *[]task.StepSnapshot, event *task.EventDraft, _ *coveragedomain.Run, _ *testdomain.TestRun) {
			event.TaskID = coverageHex(904)
			_ = input
		}},
		{"event type", func(_ *task.Task, _ *[]task.StepSnapshot, event *task.EventDraft, _ *coveragedomain.Run, _ *testdomain.TestRun) {
			event.Type = task.EventTaskStarted
		}},
		{"invalid coverage step", func(_ *task.Task, steps *[]task.StepSnapshot, _ *task.EventDraft, _ *coveragedomain.Run, _ *testdomain.TestRun) {
			(*steps)[0].Kind = task.StepBuild
		}},
		{"duplicate coverage step", func(_ *task.Task, steps *[]task.StepSnapshot, _ *task.EventDraft, _ *coveragedomain.Run, _ *testdomain.TestRun) {
			*steps = append(*steps, (*steps)[0])
		}},
	}
	for index, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := openTestStore(t)
			input, steps, event, run, testRun := coverageCreationFixture(t, 10+index)
			tc.mutate(&input, &steps, &event, &run, &testRun)
			if _, _, err := store.CreateCoverageTask(ctx, input, steps, event, run, testRun); !errors.Is(err, task.ErrInvalidArgument) {
				t.Fatalf("CreateCoverageTask() error = %v", err)
			}
			assertCoverageCreationAbsent(t, store, input.ID, run.ID, testRun.RunID, input.Request)
			if watermark, err := store.Watermark(ctx); err != nil || watermark != 0 {
				t.Fatalf("invalid create watermark = %d, %v; want 0", watermark, err)
			}
		})
	}
}

func TestCreateCoverageTaskIdempotencyReplaysAndFailsClosed(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	input, steps, event, run, testRun := coverageCreationFixture(t, 50)
	created, events, err := store.CreateCoverageTask(ctx, input, steps, event, run, testRun)
	if err != nil || len(events) != 1 {
		t.Fatalf("initial CreateCoverageTask() = %#v, %#v, %v", created, events, err)
	}

	replayed, replaySteps, replayEvent, replayRun, replayTestRun := coverageCreationFixture(t, 50)
	replayed.ID = coverageHex(950)
	replayed.CreatedAt = replayed.CreatedAt.Add(time.Minute)
	replayRun.TaskID = replayed.ID
	replayRun.CreatedAt = replayed.CreatedAt
	replayTestRun.RunID = coverageHex(951)
	replayTestRun.TaskID = replayed.ID
	replayTestRun.CreatedAt = replayed.CreatedAt
	replayRun.TestRunID = replayTestRun.RunID
	replayEvent.TaskID = replayed.ID
	replayEvent.At = replayed.CreatedAt
	got, replayEvents, err := store.CreateCoverageTask(ctx, replayed, replaySteps, replayEvent, replayRun, replayTestRun)
	if err != nil || got.ID != created.ID || len(replayEvents) != 0 {
		t.Fatalf("idempotent replay = %#v, %#v, %v", got, replayEvents, err)
	}

	mutations := []struct {
		name   string
		mutate func(*task.Task, *coveragedomain.Run, *testdomain.TestRun)
	}{
		{"canonical request", func(input *task.Task, run *coveragedomain.Run, testRun *testdomain.TestRun) {
			run.Request.ProjectID, testRun.ProjectID = "other", "other"
			syncCoverageTaskRequest(t, input, run)
		}},
		{"coverage snapshot", func(_ *task.Task, run *coveragedomain.Run, testRun *testdomain.TestRun) {
			snapshot := testdomain.SelectionSnapshot{Mode: testdomain.SelectionItems, ItemIDs: []testdomain.ID{stableID("b")}}
			run.SelectionSnapshot, testRun.SelectionSnapshot = snapshot, snapshot
		}},
		{"coverage toolchain", func(_ *task.Task, run *coveragedomain.Run, _ *testdomain.TestRun) {
			run.Toolchain = coverageToolchain(52)
		}},
		{"test project", func(input *task.Task, run *coveragedomain.Run, testRun *testdomain.TestRun) {
			run.Request.ProjectID, testRun.ProjectID = "other", "other"
			syncCoverageTaskRequest(t, input, run)
		}},
		{"test profile", func(_ *task.Task, _ *coveragedomain.Run, testRun *testdomain.TestRun) {
			testRun.ProfileID = strings.Repeat("f", 64)
		}},
		{"test toolchain", func(_ *task.Task, _ *coveragedomain.Run, testRun *testdomain.TestRun) {
			testRun.ToolchainID = "other-toolchain"
		}},
		{"test catalog", func(input *task.Task, run *coveragedomain.Run, testRun *testdomain.TestRun) {
			run.Request.CatalogRevision, testRun.CatalogRevision = strings.Repeat("e", 64), strings.Repeat("e", 64)
			syncCoverageTaskRequest(t, input, run)
		}},
		{"test selection", func(_ *task.Task, run *coveragedomain.Run, testRun *testdomain.TestRun) {
			snapshot := testdomain.SelectionSnapshot{Mode: testdomain.SelectionItems, ItemIDs: []testdomain.ID{stableID("b")}}
			run.SelectionSnapshot, testRun.SelectionSnapshot = snapshot, snapshot
		}},
		{"test iterations", func(input *task.Task, run *coveragedomain.Run, testRun *testdomain.TestRun) {
			run.Request.RepeatCount++
			testRun.Summary.Iterations++
			syncCoverageTaskRequest(t, input, run)
		}},
	}
	for _, tc := range mutations {
		t.Run(tc.name, func(t *testing.T) {
			candidate := replayed
			candidateRun := replayRun.Clone()
			candidateTestRun := replayTestRun.Clone()
			tc.mutate(&candidate, &candidateRun, &candidateTestRun)
			if _, _, err := store.CreateCoverageTask(ctx, candidate, replaySteps, replayEvent, candidateRun, candidateTestRun); !errors.Is(err, task.ErrIdempotencyConflict) {
				t.Fatalf("CreateCoverageTask() error = %v", err)
			}
			assertCoverageCreationCounts(t, store, 1, 1, 1, len(steps), 1)
		})
	}

	if _, err := store.db.Exec(`DELETE FROM coverage_runs WHERE coverage_run_id=?`, run.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.CreateCoverageTask(ctx, replayed, replaySteps, replayEvent, replayRun, replayTestRun); !errors.Is(err, task.ErrIdempotencyConflict) {
		t.Fatalf("replay with missing CoverageRun error = %v", err)
	}

	store = openTestStore(t)
	input, steps, event, run, testRun = coverageCreationFixture(t, 51)
	if _, _, err := store.CreateCoverageTask(ctx, input, steps, event, run, testRun); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`UPDATE test_runs SET project_id='corrupt' WHERE run_id=?`, testRun.RunID); err != nil {
		t.Fatal(err)
	}
	replayed, replaySteps, replayEvent, replayRun, replayTestRun = coverageCreationFixture(t, 51)
	replayed.ID, replayRun.TaskID, replayTestRun.TaskID = coverageHex(952), coverageHex(952), coverageHex(952)
	replayTestRun.RunID, replayRun.TestRunID = coverageHex(953), coverageHex(953)
	replayEvent.TaskID = replayed.ID
	if _, _, err := store.CreateCoverageTask(ctx, replayed, replaySteps, replayEvent, replayRun, replayTestRun); !errors.Is(err, task.ErrIdempotencyConflict) {
		t.Fatalf("replay with corrupt TestRun error = %v", err)
	}
}

func syncCoverageTaskRequest(t *testing.T, input *task.Task, run *coveragedomain.Run) {
	t.Helper()
	canonical, err := run.Request.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	runID, err := coveragedomain.CoverageRunID(run.Request)
	if err != nil {
		t.Fatal(err)
	}
	input.Request, run.ID = canonical, runID
}

func TestCreateCoverageTaskRollsBackInjectedFailures(t *testing.T) {
	ctx := context.Background()
	for index, table := range []string{"coverage_runs", "test_runs", "task_steps", "task_events"} {
		t.Run(table, func(t *testing.T) {
			store := openTestStore(t)
			input, steps, event, run, testRun := coverageCreationFixture(t, 100+index)
			before, err := store.Watermark(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := store.db.Exec(fmt.Sprintf(`CREATE TRIGGER reject_coverage_create BEFORE INSERT ON %s BEGIN SELECT RAISE(ABORT, 'injected failure'); END`, table)); err != nil {
				t.Fatal(err)
			}
			if _, _, err := store.CreateCoverageTask(ctx, input, steps, event, run, testRun); !errors.Is(err, task.ErrStorageUnavailable) {
				t.Fatalf("CreateCoverageTask() error = %v", err)
			}
			assertCoverageCreationAbsent(t, store, input.ID, run.ID, testRun.RunID, input.Request)
			after, err := store.Watermark(ctx)
			if err != nil || after != before {
				t.Fatalf("watermark = %d, %v; want %d", after, err, before)
			}
			rows, err := store.db.Query(`PRAGMA foreign_key_check`)
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			if rows.Next() {
				t.Fatal("foreign_key_check returned a violation")
			}
		})
	}
}

func TestGenericCreateRejectsCoverageTaskAndListFiltersIt(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	input, steps, event, run, testRun := coverageCreationFixture(t, 150)
	var nilStore *Store
	if _, _, err := nilStore.CreateCoverageTask(ctx, input, steps, event, run, testRun); !errors.Is(err, task.ErrInvalidArgument) {
		t.Fatalf("nil CreateCoverageTask() error = %v", err)
	}
	if _, _, err := store.CreateCoverageTask(nil, input, steps, event, run, testRun); !errors.Is(err, task.ErrInvalidArgument) {
		t.Fatalf("CreateCoverageTask(nil) error = %v", err)
	}
	if _, _, err := store.Create(ctx, input, steps, event); !errors.Is(err, task.ErrInvalidArgument) {
		t.Fatalf("generic Create coverage task error = %v", err)
	}
	assertCoverageCreationAbsent(t, store, input.ID, run.ID, testRun.RunID, input.Request)
	if _, _, err := store.CreateCoverageTask(ctx, input, steps, event, run, testRun); err != nil {
		t.Fatal(err)
	}
	page, err := store.List(ctx, "", 10, task.KindCoverageRun)
	if err != nil || len(page.Items) != 1 || page.Items[0].ID != input.ID {
		t.Fatalf("List(coverage_run) = %#v, %v", page, err)
	}
	if _, err := store.List(ctx, "", 10,
		task.KindSimulation,
		task.KindCMakeBuild,
		task.KindTestDiscovery,
		task.KindTestRun,
		task.KindCoverageRun,
	); err != nil {
		t.Fatalf("List(all task kinds) error = %v", err)
	}
}

func coverageCreationFixture(t *testing.T, seed int) (task.Task, []task.StepSnapshot, task.EventDraft, coveragedomain.Run, testdomain.TestRun) {
	t.Helper()
	now := time.Date(2026, 8, 4, 9, 0, seed%60, 0, time.UTC)
	request, err := coveragedomain.NewRequest(coveragedomain.Request{
		IdempotencyKey: coverageHex(700 + seed), WorkspaceGeneration: strings.Repeat("a", 64), ProjectID: "core", CoverageProfileID: "coverage-default", CatalogRevision: strings.Repeat("b", 64),
		Selection: testdomain.Selection{Mode: testdomain.SelectionItems, ItemIDs: []testdomain.ID{stableID("a")}}, RepeatCount: 2, Timeout: 15 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := request.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	runID, err := coveragedomain.CoverageRunID(request)
	if err != nil {
		t.Fatal(err)
	}
	input := task.Task{ID: coverageHex(800 + seed), IdempotencyKey: request.IdempotencyKey, RequestHash: strings.Repeat("c", 64), Kind: task.KindCoverageRun, Request: canonical, WorkspaceGeneration: request.WorkspaceGeneration, PlanFingerprint: strings.Repeat("d", 64), Timeout: request.Timeout, Status: task.StatusQueued, CreatedAt: now}
	testRun := testdomain.TestRun{RunID: coverageHex(900 + seed), TaskID: input.ID, IdempotencyKey: request.IdempotencyKey, ProjectID: request.ProjectID, ProfileID: strings.Repeat("e", 64), ToolchainID: "workspace-toolchain", CatalogRevision: request.CatalogRevision, SelectionSnapshot: testdomain.SelectionSnapshot{Mode: testdomain.SelectionItems, ItemIDs: []testdomain.ID{stableID("a")}}, Status: testdomain.RunQueued, Summary: testdomain.RunSummary{Iterations: request.RepeatCount}, CreatedAt: now}
	validatedTestRun, err := testdomain.NewTestRun(testRun)
	if err != nil {
		t.Fatal(err)
	}
	run := coveragedomain.Run{ID: runID, TaskID: input.ID, TestRunID: validatedTestRun.RunID, Status: coveragedomain.StatusQueued, Request: request, SelectionSnapshot: validatedTestRun.SelectionSnapshot, Toolchain: coverageToolchain(seed), CreatedAt: now}
	validatedRun, err := coveragedomain.NewRun(run)
	if err != nil {
		t.Fatal(err)
	}
	steps := []task.StepSnapshot{{ID: "coverage-configure", Kind: task.StepCoverageConfigure, Status: task.StepPending}, {ID: "coverage-build", Kind: task.StepCoverageBuild, Status: task.StepPending}, {ID: "coverage-test", Kind: task.StepCoverageTest, Status: task.StepPending}, {ID: "coverage-merge", Kind: task.StepCoverageMerge, Status: task.StepPending}, {ID: "coverage-normalize", Kind: task.StepCoverageNormalize, Status: task.StepPending}, {ID: "coverage-report", Kind: task.StepCoverageReport, Status: task.StepPending}, {ID: "coverage-publish", Kind: task.StepCoveragePublish, Status: task.StepPending}}
	return input, steps, draft(input.ID, task.EventTaskCreated, now), validatedRun, validatedTestRun
}

func assertCoverageCreationAbsent(t *testing.T, store *Store, taskID, coverageRunID, testRunID string, request []byte) {
	t.Helper()
	assertCoverageCreationCounts(t, store, 0, 0, 0, 0, 0)
}

func assertCoverageCreationCounts(t *testing.T, store *Store, tasks, coverageRuns, testRuns, steps, events int) {
	t.Helper()
	for _, expected := range []struct {
		table string
		want  int
	}{{"tasks", tasks}, {"coverage_runs", coverageRuns}, {"test_runs", testRuns}, {"task_steps", steps}, {"task_events", events}} {
		var got int
		if err := store.db.QueryRow(`SELECT COUNT(*) FROM ` + expected.table).Scan(&got); err != nil || got != expected.want {
			t.Fatalf("%s count = %d, %v; want %d", expected.table, got, err, expected.want)
		}
	}
}
