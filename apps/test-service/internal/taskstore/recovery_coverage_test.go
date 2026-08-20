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
	"unit-test-ide.local/test-service/internal/coveragedomain"
	"unit-test-ide.local/test-service/internal/task"
	"unit-test-ide.local/test-service/internal/testdomain"
)

func TestRecoverInterruptedCoverageTerminalizesAggregateAndLeavesQueuedCoverage(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	queuedTask, queuedSteps, _, queuedRun, queuedTestRun := coverageCreationFixture(t, 1100)
	ensureCoverageRecoveryCatalog(t, store, queuedRun, queuedTestRun)
	queuedTask, _, err := store.CreateCoverageTask(ctx, queuedTask, queuedSteps, draft(queuedTask.ID, task.EventTaskCreated, queuedTask.CreatedAt), queuedRun, queuedTestRun)
	if err != nil {
		t.Fatal(err)
	}
	queuedRunBefore, err := store.GetCoverageRun(ctx, queuedRun.ID)
	if err != nil {
		t.Fatal(err)
	}
	queuedTestRunBefore, err := store.GetRun(ctx, queuedTestRun.RunID)
	if err != nil {
		t.Fatal(err)
	}
	runningTask, runningRun, runningTestRun := startCoverageForRecovery(t, store, 1101)
	recoveredAt := runningTask.StartedAt.Add(time.Minute)

	events, err := store.RecoverInterrupted(ctx, recoveredAt)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 3 ||
		events[0].Type != task.EventTestRunFinished ||
		events[1].Type != task.EventCoverageRunFinished ||
		events[2].Type != task.EventTaskFinished ||
		events[0].Sequence >= events[1].Sequence || events[1].Sequence >= events[2].Sequence {
		t.Fatalf("recovery events = %#v", events)
	}
	var coveragePayload struct {
		CoverageRunID string                 `json:"coverageRunId"`
		Outcome       coveragedomain.Outcome `json:"outcome"`
		Reason        coveragedomain.Reason  `json:"reason"`
	}
	if err := json.Unmarshal(events[1].Payload, &coveragePayload); err != nil ||
		coveragePayload.CoverageRunID != runningRun.ID ||
		coveragePayload.Outcome != coveragedomain.OutcomeUnavailable ||
		coveragePayload.Reason != coveragedomain.ReasonServiceRestarted {
		t.Fatalf("coverage.run.finished payload = %#v, %v", coveragePayload, err)
	}

	gotQueuedTask, err := store.Get(ctx, queuedTask.ID)
	if err != nil || gotQueuedTask.Status != task.StatusQueued || gotQueuedTask.LastSequence != queuedTask.LastSequence {
		t.Fatalf("queued Task = %#v, %v", gotQueuedTask, err)
	}
	gotQueuedRun, err := store.GetCoverageRun(ctx, queuedRun.ID)
	if err != nil || !reflect.DeepEqual(gotQueuedRun, queuedRunBefore) {
		t.Fatalf("queued CoverageRun = %#v, %v; want %#v", gotQueuedRun, err, queuedRunBefore)
	}
	gotQueuedTestRun, err := store.GetRun(ctx, queuedTestRun.RunID)
	if err != nil || !reflect.DeepEqual(gotQueuedTestRun, queuedTestRunBefore) {
		t.Fatalf("queued TestRun = %#v, %v; want %#v", gotQueuedTestRun, err, queuedTestRunBefore)
	}

	gotTask, err := store.Get(ctx, runningTask.ID)
	if err != nil || gotTask.Status != task.StatusFinished || gotTask.Outcome != task.OutcomeInterrupted ||
		gotTask.LastSequence != events[2].Sequence || gotTask.FinishedAt == nil || !gotTask.FinishedAt.Equal(recoveredAt) {
		t.Fatalf("recovered Task = %#v, %v", gotTask, err)
	}
	gotRun, err := store.GetCoverageRun(ctx, runningRun.ID)
	if err != nil || gotRun.Status != coveragedomain.StatusFinished ||
		gotRun.Outcome != coveragedomain.OutcomeUnavailable || gotRun.Reason != coveragedomain.ReasonServiceRestarted ||
		gotRun.ReportID != "" || gotRun.Summary != nil || gotRun.Artifacts != (coveragedomain.ArtifactRefs{}) ||
		gotRun.LastSequence != events[2].Sequence || gotRun.FinishedAt == nil || !gotRun.FinishedAt.Equal(recoveredAt) {
		t.Fatalf("recovered CoverageRun = %#v, %v", gotRun, err)
	}
	gotTestRun, err := store.GetRun(ctx, runningTestRun.RunID)
	if err != nil || gotTestRun.Status != testdomain.RunCompleted ||
		gotTestRun.Outcome != testdomain.RunInterrupted || !gotTestRun.Incomplete ||
		gotTestRun.FinishedAt == nil || !gotTestRun.FinishedAt.Equal(recoveredAt) {
		t.Fatalf("recovered TestRun = %#v, %v", gotTestRun, err)
	}
	var reports, publicArtifacts int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM coverage_reports WHERE coverage_run_id=?`, runningRun.ID).Scan(&reports); err != nil {
		t.Fatal(err)
	}
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM artifacts WHERE task_id=? AND kind IN ('coverage-json','junit-xml','coverage-html')`, runningTask.ID).Scan(&publicArtifacts); err != nil {
		t.Fatal(err)
	}
	if reports != 0 || publicArtifacts != 0 {
		t.Fatalf("restart invented reports=%d public artifacts=%d", reports, publicArtifacts)
	}
}

func TestRecoverInterruptedCoverageRollsBackEveryDomainWrite(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	runningTask, runningRun, runningTestRun := startCoverageForRecovery(t, store, 1200)
	beforeTask, _ := store.Get(ctx, runningTask.ID)
	beforeRun, _ := store.GetCoverageRun(ctx, runningRun.ID)
	beforeTestRun, _ := store.GetRun(ctx, runningTestRun.RunID)
	beforeWatermark, _ := store.Watermark(ctx)
	if _, err := store.db.Exec(`CREATE TRIGGER reject_recovered_coverage
		BEFORE UPDATE OF status ON coverage_runs
		WHEN NEW.status='finished'
		BEGIN SELECT RAISE(ABORT, 'injected coverage recovery failure'); END`); err != nil {
		t.Fatal(err)
	}

	if _, err := store.RecoverInterrupted(ctx, runningTask.StartedAt.Add(time.Minute)); !errors.Is(err, task.ErrStorageUnavailable) {
		t.Fatalf("RecoverInterrupted() error = %v, want storage unavailable", err)
	}
	afterTask, taskErr := store.Get(ctx, runningTask.ID)
	afterRun, runErr := store.GetCoverageRun(ctx, runningRun.ID)
	afterTestRun, testRunErr := store.GetRun(ctx, runningTestRun.RunID)
	afterWatermark, watermarkErr := store.Watermark(ctx)
	if taskErr != nil || runErr != nil || testRunErr != nil || watermarkErr != nil ||
		!reflect.DeepEqual(afterTask, beforeTask) || !reflect.DeepEqual(afterRun, beforeRun) ||
		!reflect.DeepEqual(afterTestRun, beforeTestRun) || afterWatermark != beforeWatermark {
		t.Fatalf("coverage recovery leaked writes: Task=%#v (%v), Run=%#v (%v), TestRun=%#v (%v), watermark=%d (%v)",
			afterTask, taskErr, afterRun, runErr, afterTestRun, testRunErr, afterWatermark, watermarkErr)
	}
}

func startCoverageForRecovery(t *testing.T, store *Store, seed int) (task.Task, coveragedomain.Run, testdomain.TestRun) {
	t.Helper()
	ctx := context.Background()
	input, steps, event, run, testRun := coverageCreationFixture(t, seed)
	ensureCoverageRecoveryCatalog(t, store, run, testRun)
	created, _, err := store.CreateCoverageTask(ctx, input, steps, event, run, testRun)
	if err != nil {
		t.Fatal(err)
	}
	startedAt := created.CreatedAt.Add(time.Second)
	running := mustTransition(t, created, task.Transition{From: task.StatusQueued, To: task.StatusRunning, At: startedAt})
	running, _, err = store.Apply(ctx, task.Mutation{
		Task: running, Expected: task.StatusQueued,
		Events: []task.EventDraft{draft(running.ID, task.EventTaskStarted, startedAt)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`UPDATE coverage_runs SET status='running', started_at=? WHERE coverage_run_id=?`, formatCoverageTime(startedAt), run.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.StartRun(ctx, testRun.RunID, startedAt); err != nil {
		t.Fatal(err)
	}
	run.Status, run.StartedAt = coveragedomain.StatusRunning, &startedAt
	run = validCoverageRun(t, run)
	testRun.Status, testRun.StartedAt = testdomain.RunRunning, &startedAt
	validatedTestRun, err := testdomain.NewTestRun(testRun)
	if err != nil {
		t.Fatal(err)
	}
	return running, run, validatedTestRun
}

func ensureCoverageRecoveryCatalog(t *testing.T, store *Store, run coveragedomain.Run, testRun testdomain.TestRun) {
	t.Helper()
	var count int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM test_catalogs WHERE project_id=? AND profile_id=? AND revision=?`,
		run.Request.ProjectID, testRun.ProfileID, run.Request.CatalogRevision).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		return
	}
	containerID, err := testdomain.ContainerID(run.Request.ProjectID, "coverage.tests")
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := testdomain.NewCatalog(testdomain.Catalog{
		ProjectID: run.Request.ProjectID, ProfileID: testRun.ProfileID,
		Revision: run.Request.CatalogRevision, GeneratedAt: run.CreatedAt.Add(-time.Minute),
		Containers: []testdomain.Container{{
			ID: containerID, ProjectID: run.Request.ProjectID, CTestLogicalName: "coverage.tests",
			DisplayName: "Coverage Tests", Framework: testdomain.FrameworkCppUTest, Labels: []string{},
		}},
		Items: []testdomain.Item{{
			ID: run.SelectionSnapshot.ItemIDs[0], ContainerID: containerID, Kind: testdomain.ItemCase,
			Framework: testdomain.FrameworkCppUTest, LogicalName: "coverage", DisplayName: "coverage", Labels: []string{},
		}},
		Diagnostics: []testdomain.Diagnostic{},
	})
	if err != nil {
		t.Fatal(err)
	}
	artifacts, err := artifactstore.New(filepath.Join(t.TempDir(), "artifacts"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = artifacts.Close() })
	publishCatalogFixture(t, store, artifacts, catalog, 200)
	owner, err := store.Get(context.Background(), id(200))
	if err != nil {
		t.Fatal(err)
	}
	finishedAt := catalog.GeneratedAt.Add(time.Second)
	owner = mustTransition(t, owner, task.Transition{
		From: task.StatusQueued, To: task.StatusFinished, Outcome: task.OutcomeSucceeded, At: finishedAt,
	})
	if _, _, err := store.Apply(context.Background(), task.Mutation{
		Task: owner, Expected: task.StatusQueued,
		Events: []task.EventDraft{draft(owner.ID, task.EventTaskFinished, finishedAt)},
	}); err != nil {
		t.Fatal(err)
	}
}
