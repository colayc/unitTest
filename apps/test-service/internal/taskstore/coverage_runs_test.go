package taskstore

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"unit-test-ide.local/test-service/internal/coveragedomain"
	"unit-test-ide.local/test-service/internal/task"
	"unit-test-ide.local/test-service/internal/testdomain"
)

func TestCoverageRunRoundTrip(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	created := time.Date(2026, 8, 4, 9, 0, 0, 123456789, time.FixedZone("UTC+8", 8*60*60))
	cases := []struct {
		name    string
		status  coveragedomain.Status
		outcome coveragedomain.Outcome
		reason  coveragedomain.Reason
	}{
		{name: "queued", status: coveragedomain.StatusQueued},
		{name: "running", status: coveragedomain.StatusRunning},
		{name: "available", status: coveragedomain.StatusFinished, outcome: coveragedomain.OutcomeAvailable},
		{name: "partial", status: coveragedomain.StatusFinished, outcome: coveragedomain.OutcomePartial},
		{name: "unavailable", status: coveragedomain.StatusFinished, outcome: coveragedomain.OutcomeUnavailable, reason: coveragedomain.ReasonBuildFailed},
		{name: "cancelled", status: coveragedomain.StatusFinished, outcome: coveragedomain.OutcomeCancelled, reason: coveragedomain.ReasonUserCancelled},
	}
	for index, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			run := coverageRunFixture(t, index+1, created.Add(time.Duration(index)*time.Second), tc.status, tc.outcome, tc.reason)
			insertCoverageRunForTest(t, store, run)
			var storedRequest []byte
			if err := store.db.QueryRow(`SELECT request_json FROM coverage_runs WHERE coverage_run_id=?`, run.ID).Scan(&storedRequest); err != nil {
				t.Fatal(err)
			}
			canonical, err := run.Request.CanonicalJSON()
			if err != nil || !bytes.Equal(storedRequest, canonical) {
				t.Fatalf("stored request = %s, canonical = %s, err = %v", storedRequest, canonical, err)
			}
			got, err := store.GetCoverageRun(ctx, run.ID)
			if err != nil || !reflect.DeepEqual(got, run) {
				t.Fatalf("GetCoverageRun() = %#v, %v; want %#v", got, err, run)
			}
			if got.CreatedAt.Location() != time.UTC || got.StartedAt != nil && got.StartedAt.Location() != time.UTC || got.FinishedAt != nil && got.FinishedAt.Location() != time.UTC {
				t.Fatalf("returned times are not UTC: %#v", got)
			}
			got.SelectionSnapshot.ItemIDs[0] = stableID("f")
			if got.Summary != nil {
				got.Summary.Lines.Covered = 0
			}
			if got.StartedAt != nil {
				*got.StartedAt = got.StartedAt.Add(time.Hour)
			}
			again, err := store.GetCoverageRun(ctx, run.ID)
			if err != nil || !reflect.DeepEqual(again, run) {
				t.Fatalf("GetCoverageRun defensive clone = %#v, %v", again, err)
			}
		})
	}
}

func TestCoverageRunListAndCursor(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	base := time.Date(2026, 8, 4, 10, 0, 0, 0, time.UTC)
	first := coverageRunFixture(t, 10, base, coveragedomain.StatusQueued, "", "")
	second := coverageRunFixture(t, 11, base.Add(time.Second), coveragedomain.StatusRunning, "", "")
	third := coverageRunFixture(t, 12, base.Add(time.Second), coveragedomain.StatusFinished, coveragedomain.OutcomeAvailable, "")
	foreign := coverageRunFixture(t, 13, base.Add(2*time.Second), coveragedomain.StatusQueued, "", "")
	foreign.Request.WorkspaceGeneration = strings.Repeat("f", 64)
	foreign.ID, _ = coveragedomain.CoverageRunID(foreign.Request)
	foreign = validCoverageRun(t, foreign)
	for _, run := range []coveragedomain.Run{first, second, third, foreign} {
		insertCoverageRunForTest(t, store, run)
	}

	request := coveragedomain.RunPageRequest{WorkspaceGeneration: first.Request.WorkspaceGeneration, ProjectID: first.Request.ProjectID, CoverageProfileID: first.Request.CoverageProfileID, Limit: 1}
	expected := []coveragedomain.Run{first, second, third}
	sort.Slice(expected, func(i, j int) bool {
		if expected[i].CreatedAt.Equal(expected[j].CreatedAt) {
			return expected[i].ID > expected[j].ID
		}
		return expected[i].CreatedAt.After(expected[j].CreatedAt)
	})
	page, err := store.ListCoverageRuns(ctx, request)
	if err != nil || len(page.Items) != 1 || page.NextCursor == "" || page.Items[0].ID != expected[0].ID {
		t.Fatalf("first page = %#v, %v", page, err)
	}
	firstCursor := page.NextCursor
	page.Items[0].SelectionSnapshot.ItemIDs[0] = stableID("e")
	page.Items[0].Request.Selection.ItemIDs[0] = stableID("e")
	all := append([]string(nil), expected[0].ID)
	for page.NextCursor != "" {
		request.Cursor = page.NextCursor
		page, err = store.ListCoverageRuns(ctx, request)
		if err != nil {
			t.Fatal(err)
		}
		for _, run := range page.Items {
			all = append(all, run.ID)
		}
	}
	want := []string{expected[0].ID, expected[1].ID, expected[2].ID}
	if !reflect.DeepEqual(all, want) {
		t.Fatalf("stable traversal = %v, want %v", all, want)
	}
	if got, err := store.GetCoverageRun(ctx, expected[0].ID); err != nil || got.SelectionSnapshot.ItemIDs[0] != stableID("a") || got.Request.Selection.ItemIDs[0] != stableID("a") {
		t.Fatalf("page defensive clone = %#v, %v", got, err)
	}
	defaultPage, err := store.ListCoverageRuns(ctx, coveragedomain.RunPageRequest{WorkspaceGeneration: first.Request.WorkspaceGeneration})
	if err != nil || len(defaultPage.Items) != 3 || defaultPage.NextCursor != "" {
		t.Fatalf("default page = %#v, %v", defaultPage, err)
	}
	for _, limit := range []int{1, 200} {
		if _, err := store.ListCoverageRuns(ctx, coveragedomain.RunPageRequest{WorkspaceGeneration: first.Request.WorkspaceGeneration, Limit: limit}); err != nil {
			t.Fatalf("limit %d: %v", limit, err)
		}
	}
	for _, changed := range []coveragedomain.RunPageRequest{
		{WorkspaceGeneration: strings.Repeat("e", 64), ProjectID: request.ProjectID, CoverageProfileID: request.CoverageProfileID, Cursor: firstCursor, Limit: 1},
		{WorkspaceGeneration: request.WorkspaceGeneration, ProjectID: "other", CoverageProfileID: request.CoverageProfileID, Cursor: firstCursor, Limit: 1},
		{WorkspaceGeneration: request.WorkspaceGeneration, ProjectID: request.ProjectID, CoverageProfileID: "other", Cursor: firstCursor, Limit: 1},
	} {
		if _, err := store.ListCoverageRuns(ctx, changed); !errors.Is(err, task.ErrInvalidArgument) {
			t.Fatalf("mismatched cursor error = %v", err)
		}
	}
}

func TestCoverageRunFractionalTimestampPaging(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	base := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	runs := []coveragedomain.Run{
		coverageRunFixture(t, 70, base, coveragedomain.StatusQueued, "", ""),
		coverageRunFixture(t, 71, base.Add(time.Nanosecond), coveragedomain.StatusQueued, "", ""),
		coverageRunFixture(t, 72, base.Add(100*time.Millisecond), coveragedomain.StatusQueued, "", ""),
		coverageRunFixture(t, 73, base.Add(900*time.Millisecond), coveragedomain.StatusQueued, "", ""),
	}
	for _, run := range runs {
		insertCoverageRunForTest(t, store, run)
	}
	for _, run := range runs {
		var stored string
		if err := store.db.QueryRow(`SELECT created_at FROM coverage_runs WHERE coverage_run_id=?`, run.ID).Scan(&stored); err != nil {
			t.Fatal(err)
		}
		if want := run.CreatedAt.UTC().Format("2006-01-02T15:04:05.000000000Z"); stored != want {
			t.Fatalf("stored timestamp = %q, want fixed-width %q", stored, want)
		}
	}
	sort.Slice(runs, func(i, j int) bool { return runs[i].CreatedAt.After(runs[j].CreatedAt) })
	request := coveragedomain.RunPageRequest{WorkspaceGeneration: runs[0].Request.WorkspaceGeneration, Limit: 1}
	var got []string
	for {
		page, err := store.ListCoverageRuns(ctx, request)
		if err != nil {
			t.Fatal(err)
		}
		if len(page.Items) > 0 {
			got = append(got, page.Items[0].ID)
		}
		if page.NextCursor == "" {
			break
		}
		request.Cursor = page.NextCursor
	}
	want := []string{runs[0].ID, runs[1].ID, runs[2].ID, runs[3].ID}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("fractional timestamp paging = %v, want %v", got, want)
	}
}

func TestCoverageRunNonCanonicalTimestampRowsReturnStorageUnavailable(t *testing.T) {
	ctx := context.Background()
	for _, column := range []string{"created_at", "started_at", "finished_at"} {
		t.Run(column, func(t *testing.T) {
			store := openTestStore(t)
			run := coverageRunFixture(t, 80, time.Date(2026, 8, 4, 13, 0, 0, 0, time.UTC), coveragedomain.StatusFinished, coveragedomain.OutcomeAvailable, "")
			insertCoverageRunForTest(t, store, run)
			value := map[string]string{
				"created_at":  "2026-08-04T13:00:00Z",
				"started_at":  "2026-08-04T13:00:01Z",
				"finished_at": "2026-08-04T13:00:02Z",
			}[column]
			if _, err := store.db.Exec(`UPDATE coverage_runs SET `+column+`=? WHERE coverage_run_id=?`, value, run.ID); err != nil {
				t.Fatal(err)
			}
			if _, err := store.GetCoverageRun(ctx, run.ID); !errors.Is(err, task.ErrStorageUnavailable) {
				t.Fatalf("GetCoverageRun non-canonical %s error = %v", column, err)
			}
			if _, err := store.ListCoverageRuns(ctx, coveragedomain.RunPageRequest{WorkspaceGeneration: run.Request.WorkspaceGeneration}); !errors.Is(err, task.ErrStorageUnavailable) {
				t.Fatalf("ListCoverageRuns non-canonical %s error = %v", column, err)
			}
		})
	}
}

func TestCoverageRunCursorAndArgumentValidation(t *testing.T) {
	ctx := context.Background()
	var nilStore *Store
	if _, err := nilStore.GetCoverageRun(ctx, coverageHex(1)); !errors.Is(err, task.ErrInvalidArgument) {
		t.Fatalf("nil GetCoverageRun error = %v", err)
	}
	if _, err := nilStore.ListCoverageRuns(ctx, coveragedomain.RunPageRequest{}); !errors.Is(err, task.ErrInvalidArgument) {
		t.Fatalf("nil ListCoverageRuns error = %v", err)
	}
	store := openTestStore(t)
	for _, runID := range []string{"", "ABC", strings.Repeat("a", 31), strings.Repeat("A", 32)} {
		if _, err := store.GetCoverageRun(ctx, runID); !errors.Is(err, task.ErrInvalidArgument) {
			t.Fatalf("GetCoverageRun(%q) error = %v", runID, err)
		}
	}
	for _, request := range []coveragedomain.RunPageRequest{
		{}, {WorkspaceGeneration: strings.Repeat("a", 63)}, {WorkspaceGeneration: strings.Repeat("A", 64)},
		{WorkspaceGeneration: strings.Repeat("a", 64), ProjectID: "-bad"},
		{WorkspaceGeneration: strings.Repeat("a", 64), CoverageProfileID: "-bad"},
		{WorkspaceGeneration: strings.Repeat("a", 64), Cursor: "not-a-cursor"},
		{WorkspaceGeneration: strings.Repeat("a", 64), Limit: -1}, {WorkspaceGeneration: strings.Repeat("a", 64), Limit: 201},
	} {
		if _, err := store.ListCoverageRuns(ctx, request); !errors.Is(err, task.ErrInvalidArgument) {
			t.Fatalf("ListCoverageRuns(%#v) error = %v", request, err)
		}
	}
	if _, err := store.GetCoverageRun(nil, coverageHex(2)); !errors.Is(err, task.ErrInvalidArgument) {
		t.Fatalf("nil context error = %v", err)
	}
	if _, err := store.GetCoverageRun(ctx, coverageHex(2)); !errors.Is(err, task.ErrNotFound) {
		t.Fatalf("missing error = %v", err)
	}
}

func TestCoverageRunCorruptRowsReturnStorageUnavailable(t *testing.T) {
	ctx := context.Background()
	for index, mutation := range []string{
		"request_json='{}'",
		"request_json=json_set(request_json, '$.unknown', true)",
		"toolchain_json='{}'",
		"summary_json='{\"lines\":{\"covered\":2,\"total\":1},\"branches\":{\"covered\":0,\"total\":0},\"functions\":{\"covered\":0,\"total\":0}}'",
		"status='queued'",
		"created_at='not-a-time'",
	} {
		t.Run(fmt.Sprintf("case-%d", index), func(t *testing.T) {
			store := openTestStore(t)
			run := coverageRunFixture(t, 30+index, time.Date(2026, 8, 4, 11, 0, 0, 0, time.UTC), coveragedomain.StatusFinished, coveragedomain.OutcomeAvailable, "")
			insertCoverageRunForTest(t, store, run)
			if _, err := store.db.Exec(`PRAGMA ignore_check_constraints=ON`); err != nil {
				t.Fatal(err)
			}
			if _, err := store.db.Exec(`UPDATE coverage_runs SET `+mutation+` WHERE coverage_run_id=?`, run.ID); err != nil {
				t.Fatal(err)
			}
			if _, err := store.db.Exec(`PRAGMA ignore_check_constraints=OFF`); err != nil {
				t.Fatal(err)
			}
			if _, err := store.GetCoverageRun(ctx, run.ID); !errors.Is(err, task.ErrStorageUnavailable) {
				t.Fatalf("GetCoverageRun corrupt row error = %v", err)
			}
			if _, err := store.ListCoverageRuns(ctx, coveragedomain.RunPageRequest{WorkspaceGeneration: run.Request.WorkspaceGeneration}); !errors.Is(err, task.ErrStorageUnavailable) {
				t.Fatalf("ListCoverageRuns corrupt row error = %v", err)
			}
		})
	}
}

func TestCoverageCompletionPersistsAggregateAtomically(t *testing.T) {
	ctx := context.Background()
	for index, outcome := range []coveragedomain.Outcome{
		coveragedomain.OutcomeAvailable,
		coveragedomain.OutcomePartial,
	} {
		t.Run(string(outcome), func(t *testing.T) {
			store := openTestStore(t)
			mutation := coverageCompletionFixture(t, store, 200+index, outcome, "")
			finished, events, err := store.Apply(ctx, mutation)
			if err != nil {
				t.Fatal(err)
			}
			persistedRun, err := store.GetCoverageRun(ctx, mutation.FinishCoverage.Run.ID)
			if err != nil {
				t.Fatal(err)
			}
			persistedTestRun, err := store.GetRun(ctx, mutation.FinishRun.RunID)
			if err != nil {
				t.Fatal(err)
			}
			if finished.Status != task.StatusFinished || persistedTestRun.Status != testdomain.RunCompleted ||
				persistedRun.Status != coveragedomain.StatusFinished || len(events) != 1 ||
				finished.LastSequence != events[0].Sequence || persistedRun.LastSequence != events[0].Sequence {
				t.Fatalf("terminal aggregate = task %#v, run %#v, test %#v, events %#v", finished, persistedRun, persistedTestRun, events)
			}
			for _, artifact := range mutation.Artifacts {
				got, err := store.GetArtifact(ctx, artifact.ID)
				if err != nil || !reflect.DeepEqual(got, artifact) {
					t.Fatalf("GetArtifact(%s) = %#v, %v; want %#v", artifact.ID, got, err, artifact)
				}
			}
			var reports, links int
			if err := store.db.QueryRow(`SELECT COUNT(*) FROM coverage_reports WHERE coverage_run_id=?`, persistedRun.ID).Scan(&reports); err != nil || reports != 1 {
				t.Fatalf("report count = %d, %v", reports, err)
			}
			if err := store.db.QueryRow(`SELECT COUNT(*) FROM test_run_artifacts WHERE run_id=?`, persistedTestRun.RunID).Scan(&links); err != nil || links != 3 {
				t.Fatalf("artifact links = %d, %v", links, err)
			}
		})
	}
}

func TestCoverageCompletionAcceptsZeroCallbackEmbeddedTestRun(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name            string
		coverageOutcome coveragedomain.Outcome
		reason          coveragedomain.Reason
		testOutcome     testdomain.RunOutcome
	}{
		{
			name:            "cancelled",
			coverageOutcome: coveragedomain.OutcomeCancelled,
			reason:          coveragedomain.ReasonUserCancelled,
			testOutcome:     testdomain.RunCancelled,
		},
		{
			name:            "infrastructure failure",
			coverageOutcome: coveragedomain.OutcomeUnavailable,
			reason:          coveragedomain.ReasonProfileCollectionFailed,
			testOutcome:     testdomain.RunErrored,
		},
	}
	for index, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := openTestStore(t)
			mutation := coverageCompletionFixture(
				t,
				store,
				210+index,
				test.coverageOutcome,
				test.reason,
			)
			// This is the canonical lifecycle returned by EmbeddedRun.Finish
			// when no result or output callback ever started the queued run.
			mutation.FinishRun.Outcome = test.testOutcome
			mutation.FinishRun.StartedAt = nil
			mutation.FinishRun.Incomplete = true
			if mutation.FinishRun.Summary != (testdomain.RunSummary{Iterations: 2}) {
				t.Fatalf("queued fixture summary = %#v", mutation.FinishRun.Summary)
			}
			if _, _, err := store.Apply(ctx, mutation); err != nil {
				t.Fatalf("atomic zero-callback completion = %v", err)
			}
			persisted, err := store.GetRun(ctx, mutation.FinishRun.RunID)
			if err != nil || persisted.Status != testdomain.RunCompleted ||
				persisted.Outcome != test.testOutcome ||
				persisted.StartedAt != nil || persisted.FinishedAt == nil ||
				!persisted.Incomplete {
				t.Fatalf("persisted zero-callback TestRun = %#v, %v", persisted, err)
			}
		})
	}
}

func TestCoverageOwnedTestRunRejectsPublicFinishRunWithoutChangingAggregate(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	mutation := coverageCompletionFixture(t, store, 219, coveragedomain.OutcomeAvailable, "")
	before := captureCoverageTerminalSnapshot(
		t,
		store,
		mutation.Task.ID,
		mutation.FinishCoverage.Run.ID,
		mutation.FinishRun.RunID,
	)

	err := store.FinishRun(ctx, mutation.FinishRun.Clone(), mutation.Artifacts)
	if !errors.Is(err, task.ErrInvalidArgument) {
		t.Fatalf("FinishRun(coverage-owned) error = %v, want %v", err, task.ErrInvalidArgument)
	}

	assertCoverageTerminalRolledBack(t, store, before)
	if violations := foreignKeyViolations(t, store.db); violations != 0 {
		t.Fatalf("foreign key violations = %d", violations)
	}
}

func TestCoverageCompletionAdvancesRunningAggregate(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	mutation := coverageRunningCompletionFixture(t, store, 220)
	startedAt := *mutation.FinishCoverage.Run.StartedAt
	finished, events, err := store.Apply(ctx, mutation)
	if err != nil {
		t.Fatal(err)
	}
	persisted, err := store.GetCoverageRun(ctx, mutation.FinishCoverage.Run.ID)
	if err != nil || persisted.StartedAt == nil || !persisted.StartedAt.Equal(startedAt) ||
		persisted.LastSequence != finished.LastSequence || len(events) != 1 {
		t.Fatalf("running completion = task %#v, run %#v, events %#v, err %v", finished, persisted, events, err)
	}
}

func TestCoverageCompletionRejectsImmutableRunMutation(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name   string
		mutate func(*testing.T, *task.Mutation)
	}{
		{name: "request", mutate: func(t *testing.T, mutation *task.Mutation) {
			// CoverageRun ID is derived from Request, so a valid changed Request cannot retain
			// the persisted ID. Recompute the ID and align the Task/Report; the transaction
			// then rejects the absent immutable identity before any terminal write.
			request := mutation.FinishCoverage.Run.Request.Clone()
			request.CoverageProfileID = "coverage-changed"
			request, err := coveragedomain.NewRequest(request)
			if err != nil {
				t.Fatal(err)
			}
			mutation.FinishCoverage.Run.Request = request
			mutation.FinishCoverage.Run.ID, err = coveragedomain.CoverageRunID(request)
			if err != nil {
				t.Fatal(err)
			}
			mutation.FinishCoverage.Report.RunID = mutation.FinishCoverage.Run.ID
			mutation.Task.Request, err = request.CanonicalJSON()
			if err != nil {
				t.Fatal(err)
			}
		}},
		{name: "selection snapshot", mutate: func(_ *testing.T, mutation *task.Mutation) {
			snapshot := testdomain.SelectionSnapshot{Mode: testdomain.SelectionItems, ItemIDs: []testdomain.ID{stableID("b")}}
			mutation.FinishCoverage.Run.SelectionSnapshot = snapshot
			mutation.FinishRun.SelectionSnapshot = snapshot
		}},
		{name: "toolchain", mutate: func(_ *testing.T, mutation *task.Mutation) {
			mutation.FinishCoverage.Run.Toolchain.NormalizerVersion = "changed"
			mutation.FinishCoverage.Report.Toolchain = mutation.FinishCoverage.Run.Toolchain
		}},
		{name: "created time", mutate: func(_ *testing.T, mutation *task.Mutation) {
			changed := mutation.FinishCoverage.Run.CreatedAt.Add(time.Nanosecond)
			mutation.FinishCoverage.Run.CreatedAt = changed
			mutation.FinishRun.CreatedAt = changed
			mutation.Task.CreatedAt = changed
		}},
		{name: "last sequence", mutate: func(_ *testing.T, mutation *task.Mutation) {
			mutation.FinishCoverage.Run.LastSequence++
		}},
		{name: "running started time", mutate: func(_ *testing.T, mutation *task.Mutation) {
			changed := mutation.FinishCoverage.Run.StartedAt.Add(time.Nanosecond)
			mutation.FinishCoverage.Run.StartedAt = &changed
		}},
	}
	for index, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := openTestStore(t)
			mutation := coverageRunningCompletionFixture(t, store, 221+index)
			before := captureCoverageTerminalSnapshot(t, store, mutation.Task.ID, mutation.FinishCoverage.Run.ID, mutation.FinishRun.RunID)
			tc.mutate(t, &mutation)
			if _, _, err := store.Apply(ctx, mutation); !errors.Is(err, task.ErrConflict) {
				t.Fatalf("Apply() error = %v, want %v", err, task.ErrConflict)
			}
			assertCoverageTerminalRolledBack(t, store, before)
		})
	}
}

func TestCoverageCompletionRejectsMismatchedTerminalGraph(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name   string
		mutate func(*task.Mutation)
	}{
		{name: "wrong report run", mutate: func(m *task.Mutation) { m.FinishCoverage.Report.RunID = coverageHex(9910) }},
		{name: "wrong report test run", mutate: func(m *task.Mutation) { m.FinishCoverage.Report.TestRunID = coverageHex(9911) }},
		{name: "wrong coverage run owner", mutate: func(m *task.Mutation) { m.FinishCoverage.Run.TaskID = coverageHex(9908) }},
		{name: "wrong coverage test run", mutate: func(m *task.Mutation) { m.FinishCoverage.Run.TestRunID = coverageHex(9909) }},
		{name: "wrong report ID", mutate: func(m *task.Mutation) { m.FinishCoverage.Report.ID = coverageHex(9912) }},
		{name: "wrong report summary", mutate: func(m *task.Mutation) { m.FinishCoverage.Report.Summary.Lines.Covered-- }},
		{name: "wrong report toolchain", mutate: func(m *task.Mutation) { m.FinishCoverage.Report.Toolchain = coverageToolchain(999) }},
		{name: "wrong completeness", mutate: func(m *task.Mutation) {
			m.FinishCoverage.Report.Completeness = coveragedomain.Completeness{Outcome: coveragedomain.OutcomePartial, Reasons: []coveragedomain.CompletenessReason{coveragedomain.CompletenessReasonTestCrashed}}
		}},
		{name: "wrong report artifact", mutate: func(m *task.Mutation) { m.FinishCoverage.Report.ArtifactID = m.FinishCoverage.Run.Artifacts.JUnitXMLID }},
		{name: "missing coverage JSON ref", mutate: func(m *task.Mutation) { m.FinishCoverage.Run.Artifacts.CoverageJSONID = "" }},
		{name: "missing JUnit XML ref", mutate: func(m *task.Mutation) { m.FinishCoverage.Run.Artifacts.JUnitXMLID = "" }},
		{name: "missing coverage HTML ref", mutate: func(m *task.Mutation) { m.FinishCoverage.Run.Artifacts.CoverageHTMLID = "" }},
		{name: "report before run", mutate: func(m *task.Mutation) {
			m.FinishCoverage.Report.CreatedAt = m.FinishCoverage.Run.CreatedAt.Add(-time.Nanosecond)
		}},
		{name: "report after run", mutate: func(m *task.Mutation) {
			m.FinishCoverage.Report.CreatedAt = m.FinishCoverage.Run.FinishedAt.Add(time.Nanosecond)
		}},
		{name: "missing report", mutate: func(m *task.Mutation) { m.FinishCoverage.Report = nil }},
		{name: "wrong task outcome", mutate: func(m *task.Mutation) { m.Task.Outcome = task.OutcomeInfrastructureFailed }},
		{name: "missing task finished event", mutate: func(m *task.Mutation) { m.Events = nil }},
		{name: "duplicate task finished event", mutate: func(m *task.Mutation) {
			m.Events = append(append([]task.EventDraft(nil), m.Events...), m.Events[0])
		}},
		{name: "event after task finished", mutate: func(m *task.Mutation) {
			m.Events = append(append([]task.EventDraft(nil), m.Events...), task.EventDraft{
				TaskID:  m.Task.ID,
				Type:    task.EventTaskDiagnostic,
				At:      *m.Task.FinishedAt,
				Payload: json.RawMessage(`{"diagnostic":"late"}`),
			})
		}},
		{name: "coverage without test completion", mutate: func(m *task.Mutation) { m.FinishRun = nil }},
		{name: "coverage task finished without aggregate completion", mutate: func(m *task.Mutation) {
			m.FinishRun, m.FinishCoverage = nil, nil
		}},
		{name: "nonterminal test run", mutate: func(m *task.Mutation) {
			m.FinishRun.Status = testdomain.RunQueued
			m.FinishRun.Outcome = ""
			m.FinishRun.FinishedAt = nil
		}},
		{name: "wrong test run identity", mutate: func(m *task.Mutation) { m.FinishRun.RunID = coverageHex(9913) }},
		{name: "terminal test run without coverage", mutate: func(m *task.Mutation) { m.FinishCoverage = nil }},
		{name: "unavailable with report", mutate: func(m *task.Mutation) {
			m.FinishCoverage.Run.Outcome, m.FinishCoverage.Run.Reason = coveragedomain.OutcomeUnavailable, coveragedomain.ReasonBuildFailed
			m.FinishCoverage.Run.Summary, m.FinishCoverage.Run.ReportID, m.FinishCoverage.Run.Artifacts = nil, "", coveragedomain.ArtifactRefs{}
			m.Task.Outcome = task.OutcomeCommandFailed
		}},
		{name: "unavailable with summary", mutate: func(m *task.Mutation) {
			m.FinishCoverage.Run.Outcome, m.FinishCoverage.Run.Reason = coveragedomain.OutcomeUnavailable, coveragedomain.ReasonBuildFailed
			m.FinishCoverage.Run.ReportID, m.FinishCoverage.Run.Artifacts = "", coveragedomain.ArtifactRefs{}
			m.FinishCoverage.Report, m.Artifacts, m.Task.Outcome = nil, nil, task.OutcomeCommandFailed
		}},
		{name: "unavailable with public artifacts", mutate: func(m *task.Mutation) {
			m.FinishCoverage.Run.Outcome, m.FinishCoverage.Run.Reason = coveragedomain.OutcomeUnavailable, coveragedomain.ReasonBuildFailed
			m.FinishCoverage.Run.Summary, m.FinishCoverage.Run.ReportID, m.FinishCoverage.Run.Artifacts = nil, "", coveragedomain.ArtifactRefs{}
			m.FinishCoverage.Report, m.Task.Outcome = nil, task.OutcomeCommandFailed
		}},
	}
	for index, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := openTestStore(t)
			mutation := coverageCompletionFixture(t, store, 230+index, coveragedomain.OutcomeAvailable, "")
			coverageRunID, testRunID := mutation.FinishCoverage.Run.ID, mutation.FinishRun.RunID
			before := captureCoverageTerminalSnapshot(t, store, mutation.Task.ID, coverageRunID, testRunID)
			tc.mutate(&mutation)
			if _, _, err := store.Apply(ctx, mutation); !errors.Is(err, task.ErrInvalidArgument) {
				t.Fatalf("Apply() error = %v", err)
			}
			assertCoverageTerminalRolledBack(t, store, before)
		})
	}
}

func TestCoverageTerminalReplayIsIdempotentAndImmutable(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	mutation := coverageCompletionFixture(t, store, 260, coveragedomain.OutcomeAvailable, "")
	finishedStep := mutation.Task.Steps[0]
	finishedStep.Status = task.StepSucceeded
	finishedStep.FinishedAt = mutation.Task.FinishedAt
	mutation.Steps = []task.StepMutation{{Step: finishedStep, Expected: task.StepPending}}
	first, events, err := store.Apply(ctx, mutation)
	if err != nil || len(events) != 1 {
		t.Fatalf("first Apply() = %#v, %#v, %v", first, events, err)
	}
	before := coverageTerminalCounts(t, store)
	beforeTask, _ := store.Get(ctx, mutation.Task.ID)
	beforeRun, _ := store.GetCoverageRun(ctx, mutation.FinishCoverage.Run.ID)
	beforeTestRun, _ := store.GetRun(ctx, mutation.FinishRun.RunID)
	beforeReport, _ := store.GetCoverageReport(ctx, mutation.FinishCoverage.Report.ID)
	replayed, replayEvents, err := store.Apply(ctx, mutation)
	if err != nil || len(replayEvents) != 0 || !reflect.DeepEqual(replayed, first) {
		t.Fatalf("identical replay = %#v, %#v, %v; want %#v", replayed, replayEvents, err, first)
	}
	if after := coverageTerminalCounts(t, store); !reflect.DeepEqual(after, before) {
		t.Fatalf("counts after replay = %v, want %v", after, before)
	}

	eventReplayCases := []struct {
		name   string
		mutate func(*task.Mutation)
	}{
		{name: "changed payload", mutate: func(candidate *task.Mutation) {
			candidate.Events[0].Payload = json.RawMessage(`{"changed":true}`)
		}},
		{name: "changed time", mutate: func(candidate *task.Mutation) {
			candidate.Events[0].At = candidate.Events[0].At.Add(time.Nanosecond)
		}},
		{name: "extra event", mutate: func(candidate *task.Mutation) {
			candidate.Events = append([]task.EventDraft{{
				TaskID:  candidate.Task.ID,
				Type:    task.EventTaskDiagnostic,
				At:      candidate.Events[0].At.Add(-time.Nanosecond),
				Payload: json.RawMessage(`{"diagnostic":"extra"}`),
			}}, candidate.Events...)
		}},
	}
	for _, tc := range eventReplayCases {
		t.Run(tc.name, func(t *testing.T) {
			candidate := mutation
			candidate.Events = append([]task.EventDraft(nil), mutation.Events...)
			for index := range candidate.Events {
				candidate.Events[index].Payload = append(json.RawMessage(nil), candidate.Events[index].Payload...)
			}
			tc.mutate(&candidate)
			if _, _, err := store.Apply(ctx, candidate); !errors.Is(err, task.ErrConflict) {
				t.Fatalf("event replay error = %v, want %v", err, task.ErrConflict)
			}
			if after := coverageTerminalCounts(t, store); !reflect.DeepEqual(after, before) {
				t.Fatalf("counts after event replay = %v, want %v", after, before)
			}
		})
	}

	changed := mutation
	changed.Task = mutation.Task
	changed.Task.StartedAt = ptrTime(changed.Task.CreatedAt.Add(time.Second))
	changed.Expected = task.StatusRunning
	changed.FinishCoverage = &task.CoverageCompletion{Run: mutation.FinishCoverage.Run.Clone(), Expected: coveragedomain.StatusRunning, Report: ptrReport(mutation.FinishCoverage.Report.Clone())}
	changed.FinishCoverage.Run.StartedAt = ptrTime(changed.Task.CreatedAt.Add(time.Second))
	changed.FinishCoverage.Run.Outcome = coveragedomain.OutcomePartial
	changed.FinishCoverage.Report.Completeness = coveragedomain.Completeness{Outcome: coveragedomain.OutcomePartial, Reasons: []coveragedomain.CompletenessReason{coveragedomain.CompletenessReasonTestCrashed}}
	if _, _, err := store.Apply(ctx, changed); !errors.Is(err, task.ErrConflict) {
		t.Fatalf("changed replay error = %v", err)
	}
	if after := coverageTerminalCounts(t, store); !reflect.DeepEqual(after, before) {
		t.Fatalf("counts after changed replay = %v, want %v", after, before)
	}
	afterTask, _ := store.Get(ctx, mutation.Task.ID)
	afterRun, _ := store.GetCoverageRun(ctx, mutation.FinishCoverage.Run.ID)
	afterTestRun, _ := store.GetRun(ctx, mutation.FinishRun.RunID)
	afterReport, _ := store.GetCoverageReport(ctx, mutation.FinishCoverage.Report.ID)
	if !reflect.DeepEqual(afterTask, beforeTask) || !reflect.DeepEqual(afterRun, beforeRun) ||
		!reflect.DeepEqual(afterTestRun, beforeTestRun) || !reflect.DeepEqual(afterReport, beforeReport) {
		t.Fatalf("durable terminal graph changed after conflict")
	}
}

func TestCoverageCompletionFaultsRollBackEveryTerminalRow(t *testing.T) {
	ctx := context.Background()
	cases := []struct{ name, trigger string }{
		{name: "final event", trigger: `CREATE TRIGGER reject_coverage_terminal BEFORE INSERT ON task_events BEGIN SELECT RAISE(ABORT, 'event'); END`},
		{name: "artifact metadata", trigger: `CREATE TRIGGER reject_coverage_terminal BEFORE INSERT ON artifacts BEGIN SELECT RAISE(ABORT, 'artifact'); END`},
		{name: "artifact link", trigger: `CREATE TRIGGER reject_coverage_terminal BEFORE INSERT ON test_run_artifacts BEGIN SELECT RAISE(ABORT, 'link'); END`},
		{name: "test run", trigger: `CREATE TRIGGER reject_coverage_terminal BEFORE UPDATE OF status ON test_runs WHEN NEW.status='completed' BEGIN SELECT RAISE(ABORT, 'test-run'); END`},
		{name: "report", trigger: `CREATE TRIGGER reject_coverage_terminal BEFORE INSERT ON coverage_reports BEGIN SELECT RAISE(ABORT, 'report'); END`},
		{name: "coverage run", trigger: `CREATE TRIGGER reject_coverage_terminal BEFORE UPDATE OF status ON coverage_runs WHEN NEW.status='finished' BEGIN SELECT RAISE(ABORT, 'coverage-run'); END`},
	}
	for index, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := openTestStore(t)
			mutation := coverageCompletionFixture(t, store, 280+index, coveragedomain.OutcomeAvailable, "")
			before := captureCoverageTerminalSnapshot(t, store, mutation.Task.ID, mutation.FinishCoverage.Run.ID, mutation.FinishRun.RunID)
			if _, err := store.db.Exec(tc.trigger); err != nil {
				t.Fatal(err)
			}
			if _, _, err := store.Apply(ctx, mutation); !errors.Is(err, task.ErrStorageUnavailable) {
				t.Fatalf("Apply() error = %v", err)
			}
			assertCoverageTerminalRolledBack(t, store, before)
			if violations := foreignKeyViolations(t, store.db); violations != 0 {
				t.Fatalf("foreign key violations = %d", violations)
			}
		})
	}
}

func TestCoverageTerminalUnavailableAndCancelledReasonsAreImmutable(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		outcome         coveragedomain.Outcome
		reason, changed coveragedomain.Reason
	}{
		{coveragedomain.OutcomeUnavailable, coveragedomain.ReasonBuildFailed, coveragedomain.ReasonInstrumentationFailed},
		{coveragedomain.OutcomeCancelled, coveragedomain.ReasonUserCancelled, coveragedomain.ReasonTaskTimedOut},
	}
	for index, tc := range cases {
		t.Run(string(tc.outcome), func(t *testing.T) {
			store := openTestStore(t)
			mutation := coverageCompletionFixture(t, store, 400+index, tc.outcome, tc.reason)
			first, _, err := store.Apply(ctx, mutation)
			if err != nil || len(mutation.Artifacts) != 0 || mutation.FinishCoverage.Report != nil {
				t.Fatalf("Apply() = %#v, %v", first, err)
			}
			persisted, err := store.GetCoverageRun(ctx, mutation.FinishCoverage.Run.ID)
			if err != nil || persisted.Reason != tc.reason {
				t.Fatalf("persisted reason = %q, %v", persisted.Reason, err)
			}
			changed := mutation
			changed.Task = mutation.Task
			changed.FinishCoverage = &task.CoverageCompletion{Run: mutation.FinishCoverage.Run.Clone(), Expected: mutation.FinishCoverage.Expected}
			changed.FinishCoverage.Run.Reason = tc.changed
			changed.Task.Outcome = task.CoverageTaskOutcome(tc.outcome, tc.changed)
			if _, _, err := store.Apply(ctx, changed); !errors.Is(err, task.ErrConflict) {
				t.Fatalf("changed reason replay error = %v", err)
			}
			again, _ := store.GetCoverageRun(ctx, mutation.FinishCoverage.Run.ID)
			if again.Reason != tc.reason {
				t.Fatalf("reason changed to %q", again.Reason)
			}
			if _, err := store.GetCoverageReport(ctx, coverageHex(9997)); !errors.Is(err, task.ErrNotFound) {
				t.Fatalf("unexpected report error = %v", err)
			}
		})
	}
}

func coverageCompletionFixture(t *testing.T, store *Store, seed int, outcome coveragedomain.Outcome, reason coveragedomain.Reason) task.Mutation {
	t.Helper()
	input, steps, event, run, testRun := coverageCreationFixture(t, seed)
	created, _, err := store.CreateCoverageTask(context.Background(), input, steps, event, run, testRun)
	if err != nil {
		t.Fatal(err)
	}
	finishedAt := created.CreatedAt.Add(2 * time.Second)
	run.Status, run.Outcome, run.Reason = coveragedomain.StatusFinished, outcome, reason
	run.LastSequence = created.LastSequence
	run.FinishedAt = ptrTime(finishedAt)
	if outcome == coveragedomain.OutcomeAvailable || outcome == coveragedomain.OutcomePartial {
		run.Summary = &coveragedomain.Summary{Lines: coveragedomain.Metric{Covered: 8, Total: 10}, Branches: coveragedomain.Metric{Covered: 3, Total: 4}, Functions: coveragedomain.Metric{Covered: 1, Total: 1}}
		run.ReportID = coverageHex(5000 + seed)
		run.Artifacts = coveragedomain.ArtifactRefs{CoverageJSONID: coverageHex(6000 + seed), JUnitXMLID: coverageHex(7000 + seed), CoverageHTMLID: coverageHex(8000 + seed)}
	}
	run = validCoverageRun(t, run)
	testRun.Status, testRun.Outcome = testdomain.RunCompleted, testdomain.RunPassed
	testRun.FinishedAt = ptrTime(finishedAt)
	validatedTestRun, err := testdomain.NewTestRun(testRun)
	if err != nil {
		t.Fatal(err)
	}
	terminalTask := mustTransition(t, created, task.Transition{From: task.StatusQueued, To: task.StatusFinished, Outcome: task.CoverageTaskOutcome(outcome, reason), At: finishedAt})
	completion := &task.CoverageCompletion{Run: run, Expected: coveragedomain.StatusQueued}
	artifacts := []task.Artifact(nil)
	if run.Summary != nil {
		report := coverageReportFixture(t, run)
		completion.Report = &report
		artifacts = coverageArtifactsFixture(run)
	}
	return task.Mutation{Task: terminalTask, Expected: task.StatusQueued, Events: []task.EventDraft{draft(created.ID, task.EventTaskFinished, finishedAt)}, Artifacts: artifacts, FinishRun: &validatedTestRun, FinishCoverage: completion}
}

func coverageRunningCompletionFixture(t *testing.T, store *Store, seed int) task.Mutation {
	t.Helper()
	ctx := context.Background()
	mutation := coverageCompletionFixture(t, store, seed, coveragedomain.OutcomeAvailable, "")
	queued := mutation.Task
	queued.Status, queued.Outcome, queued.FinishedAt = task.StatusQueued, "", nil
	startedAt := queued.CreatedAt.Add(time.Second)
	running := mustTransition(t, queued, task.Transition{From: task.StatusQueued, To: task.StatusRunning, At: startedAt})
	running, _, err := store.Apply(ctx, task.Mutation{
		Task: running, Expected: task.StatusQueued,
		Events: []task.EventDraft{draft(running.ID, task.EventTaskStarted, startedAt)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`UPDATE coverage_runs SET status='running', started_at=? WHERE coverage_run_id=?`, formatCoverageTime(startedAt), mutation.FinishCoverage.Run.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.StartRun(ctx, mutation.FinishRun.RunID, startedAt); err != nil {
		t.Fatal(err)
	}
	mutation.Task = mustTransition(t, running, task.Transition{
		From: task.StatusRunning, To: task.StatusFinished,
		Outcome: task.OutcomeSucceeded, At: *mutation.Task.FinishedAt,
	})
	mutation.Expected = task.StatusRunning
	mutation.FinishCoverage.Expected = coveragedomain.StatusRunning
	mutation.FinishCoverage.Run.StartedAt = ptrTime(startedAt)
	mutation.FinishRun.StartedAt = ptrTime(startedAt)
	return mutation
}

func ptrReport(value coveragedomain.Report) *coveragedomain.Report { return &value }

func coverageTerminalCounts(t *testing.T, store *Store) []int {
	t.Helper()
	result := make([]int, 0, 5)
	for _, table := range []string{"task_events", "artifacts", "test_run_artifacts", "coverage_reports", "coverage_runs"} {
		var count int
		if err := store.db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&count); err != nil {
			t.Fatal(err)
		}
		result = append(result, count)
	}
	return result
}

type coverageTerminalSnapshot struct {
	task      task.Task
	run       coveragedomain.Run
	testRun   testdomain.TestRun
	events    []task.Event
	watermark int64
}

func captureCoverageTerminalSnapshot(t *testing.T, store *Store, taskID, coverageRunID, testRunID string) coverageTerminalSnapshot {
	t.Helper()
	ctx := context.Background()
	value := coverageTerminalSnapshot{}
	var err error
	if value.task, err = store.Get(ctx, taskID); err != nil {
		t.Fatal(err)
	}
	if value.run, err = store.GetCoverageRun(ctx, coverageRunID); err != nil {
		t.Fatal(err)
	}
	if value.testRun, err = store.GetRun(ctx, testRunID); err != nil {
		t.Fatal(err)
	}
	if value.watermark, err = store.Watermark(ctx); err != nil {
		t.Fatal(err)
	}
	if value.events, err = store.EventsAfter(ctx, 0, value.watermark, int(value.watermark)+1); err != nil {
		t.Fatal(err)
	}
	return value
}

func assertCoverageTerminalRolledBack(t *testing.T, store *Store, before coverageTerminalSnapshot) {
	t.Helper()
	ctx := context.Background()
	currentTask, taskErr := store.Get(ctx, before.task.ID)
	currentRun, runErr := store.GetCoverageRun(ctx, before.run.ID)
	currentTestRun, testRunErr := store.GetRun(ctx, before.testRun.RunID)
	if taskErr != nil || runErr != nil || testRunErr != nil ||
		!reflect.DeepEqual(currentTask, before.task) ||
		!reflect.DeepEqual(currentRun, before.run) ||
		!reflect.DeepEqual(currentTestRun, before.testRun) {
		t.Fatalf("aggregate changed after rollback: Task=%#v (%v), CoverageRun=%#v (%v), TestRun=%#v (%v); want Task=%#v, CoverageRun=%#v, TestRun=%#v",
			currentTask, taskErr, currentRun, runErr, currentTestRun, testRunErr,
			before.task, before.run, before.testRun)
	}
	for _, table := range []string{"coverage_reports", "artifacts", "test_run_artifacts"} {
		var count int
		if err := store.db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&count); err != nil || count != 0 {
			t.Fatalf("%s count = %d, %v; want 0", table, count, err)
		}
	}
	if got, err := store.Watermark(ctx); err != nil || got != before.watermark {
		t.Fatalf("watermark = %d, %v; want %d", got, err, before.watermark)
	}
	if events, err := store.EventsAfter(ctx, 0, before.watermark, int(before.watermark)+1); err != nil || !reflect.DeepEqual(events, before.events) {
		t.Fatalf("events after rollback = %#v, %v; want %#v", events, err, before.events)
	}
}

func insertCoverageRunForTest(t *testing.T, store *Store, run coveragedomain.Run) {
	t.Helper()
	if _, err := store.db.Exec(`PRAGMA foreign_keys=OFF`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = store.db.Exec(`PRAGMA foreign_keys=ON`) })
	tx, err := store.db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := insertCoverageRun(context.Background(), tx, run); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func coverageRunFixture(t *testing.T, seed int, created time.Time, status coveragedomain.Status, outcome coveragedomain.Outcome, reason coveragedomain.Reason) coveragedomain.Run {
	t.Helper()
	request, err := coveragedomain.NewRequest(coveragedomain.Request{
		IdempotencyKey: coverageHex(seed), WorkspaceGeneration: strings.Repeat("a", 64), ProjectID: "core", CoverageProfileID: "coverage-default", CatalogRevision: strings.Repeat("b", 64),
		Selection: testdomain.Selection{Mode: testdomain.SelectionItems, ItemIDs: []testdomain.ID{stableID("a")}}, RepeatCount: 2, Timeout: 15 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	runID, err := coveragedomain.CoverageRunID(request)
	if err != nil {
		t.Fatal(err)
	}
	run := coveragedomain.Run{
		ID: runID, TaskID: coverageHex(1000 + seed), TestRunID: coverageHex(2000 + seed), Request: request,
		SelectionSnapshot: testdomain.SelectionSnapshot{Mode: testdomain.SelectionItems, ItemIDs: []testdomain.ID{stableID("a")}},
		Toolchain:         coverageToolchain(seed), Status: status, Outcome: outcome, Reason: reason, CreatedAt: created, LastSequence: int64(seed),
	}
	if status == coveragedomain.StatusRunning || status == coveragedomain.StatusFinished {
		run.StartedAt = ptrTime(created.Add(time.Second))
	}
	if status == coveragedomain.StatusFinished {
		run.FinishedAt = ptrTime(created.Add(2 * time.Second))
		if outcome == coveragedomain.OutcomeAvailable || outcome == coveragedomain.OutcomePartial {
			run.Summary = &coveragedomain.Summary{Lines: coveragedomain.Metric{Covered: 8, Total: 10}, Branches: coveragedomain.Metric{Covered: 3, Total: 4}, Functions: coveragedomain.Metric{Covered: 1, Total: 1}}
			run.ReportID = coverageHex(3000 + seed)
			run.Artifacts = coveragedomain.ArtifactRefs{CoverageJSONID: coverageHex(4000 + seed), JUnitXMLID: coverageHex(5000 + seed), CoverageHTMLID: coverageHex(6000 + seed)}
		}
	}
	return validCoverageRun(t, run)
}

func validCoverageRun(t *testing.T, value coveragedomain.Run) coveragedomain.Run {
	t.Helper()
	validated, err := coveragedomain.NewRun(value)
	if err != nil {
		t.Fatal(err)
	}
	return validated
}

func coverageToolchain(seed int) coveragedomain.ToolchainSnapshot {
	fingerprint := strings.Repeat(string(rune('a'+seed%6)), 64)
	switch seed % 3 {
	case 0:
		return coveragedomain.ToolchainSnapshot{Platform: coveragedomain.PlatformWindows, Architecture: coveragedomain.ArchitectureX64, Compiler: coveragedomain.CompilerSnapshot{Family: coveragedomain.CompilerFamilyClangCL, Version: "18.1.8"}, Driver: coveragedomain.DriverSnapshot{Name: coveragedomain.DriverLLVMCov, Version: "18.1.8"}, Collector: coveragedomain.CollectorSnapshot{Name: coveragedomain.CollectorLLVMCov, Version: "18.1.8"}, NormalizerVersion: "1.0.0", InstrumentationFingerprint: fingerprint}
	case 1:
		return coveragedomain.ToolchainSnapshot{Platform: coveragedomain.PlatformLinux, Architecture: coveragedomain.ArchitectureX64, Compiler: coveragedomain.CompilerSnapshot{Family: coveragedomain.CompilerFamilyGCC, Version: "14.2.0"}, Driver: coveragedomain.DriverSnapshot{Name: coveragedomain.DriverGCov, Version: "14.2.0"}, Collector: coveragedomain.CollectorSnapshot{Name: coveragedomain.CollectorGCovr, Version: "8.3"}, NormalizerVersion: "1.0.0", InstrumentationFingerprint: fingerprint}
	default:
		return coveragedomain.ToolchainSnapshot{Platform: coveragedomain.PlatformLinux, Architecture: coveragedomain.ArchitectureARM64, Compiler: coveragedomain.CompilerSnapshot{Family: coveragedomain.CompilerFamilyClang, Version: "18.1.8"}, Driver: coveragedomain.DriverSnapshot{Name: coveragedomain.DriverLLVMCov, Version: "18.1.8"}, Collector: coveragedomain.CollectorSnapshot{Name: coveragedomain.CollectorLLVMCov, Version: "18.1.8"}, NormalizerVersion: "1.0.0", InstrumentationFingerprint: fingerprint}
	}
}

func coverageHex(value int) string { return fmt.Sprintf("%032x", value) }
