package taskstore

import (
	"bytes"
	"context"
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
