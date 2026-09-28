package taskstore

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"unit-test-ide.local/test-service/internal/coveragedetail"
	"unit-test-ide.local/test-service/internal/coveragedomain"
	"unit-test-ide.local/test-service/internal/task"
)

func detailStoreFixture(t *testing.T, store *Store, number int) coveragedetail.Index {
	return detailStoreFixtureWithPrefix(t, store, number, "")
}

func detailStoreFixtureWithPrefix(t *testing.T, store *Store, number int, prefix string) coveragedetail.Index {
	t.Helper()
	mutation := coverageCompletionFixture(t, store, number, coveragedomain.OutcomeAvailable, "")
	for i := range mutation.Artifacts {
		mutation.Artifacts[i].RelativePath = prefix + mutation.Artifacts[i].RelativePath
	}
	if _, _, err := store.Apply(context.Background(), mutation); err != nil {
		t.Fatal(err)
	}
	report := *mutation.FinishCoverage.Report
	fileA, _ := coveragedetail.StableFileID(mutation.FinishCoverage.Run.Request.ProjectID, "src/a.cpp")
	fileB, _ := coveragedetail.StableFileID(mutation.FinishCoverage.Run.Request.ProjectID, "src/b.cpp")
	functionID, _ := coveragedetail.StableFunctionID(fileA, "linkage:_Z3foov")
	gapID, _ := coveragedetail.StableGapID(report.ID, functionID, "line", coveragedomain.SourceLocation{Line: 2}, 0)
	return coveragedetail.Index{
		WorkspaceGeneration: mutation.FinishCoverage.Run.Request.WorkspaceGeneration,
		ProjectID:           mutation.FinishCoverage.Run.Request.ProjectID,
		ReportID:            report.ID, RunID: report.RunID, Toolchain: report.Toolchain,
		Project: coveragedetail.Project{Summary: report.Summary, Status: coveragedetail.StatusIncomplete, Reasons: []string{"attribution_ambiguous"}},
		Files: []coveragedetail.File{
			{ID: fileA, RelativePath: "src/a.cpp", SourceSHA256: strings.Repeat("a", 64), Status: coveragedetail.StatusCurrent, Reasons: []string{}, Functions: []coveragedetail.Function{{ID: functionID, Name: "foo", LinkageName: "_Z3foov", Start: coveragedomain.SourceLocation{Line: 1}, End: coveragedomain.SourceLocation{Line: 3}, Status: coveragedetail.StatusCurrent, Reasons: []string{}, Lines: []coveragedetail.Line{{Line: 1, Count: 1}, {Line: 2, Count: 0}}, Branches: []coveragedetail.Branch{{Line: 2, Column: 4, Ordinal: 0, Count: 0}}}}, Lines: []coveragedetail.Line{{Line: 1, Count: 1}, {Line: 2, Count: 0}}},
			{ID: fileB, RelativePath: "src/b.cpp", SourceSHA256: strings.Repeat("b", 64), Status: coveragedetail.StatusIncomplete, Reasons: []string{"attribution_ambiguous"}},
		},
		Gaps: []coveragedetail.Gap{{ID: gapID, FileID: fileA, FunctionID: functionID, Kind: "line", Location: coveragedomain.SourceLocation{Line: 2}}},
	}
}

func TestCoverageDetailPersistsAndPages(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	index := detailStoreFixture(t, store, 7001)
	if err := store.PutCoverageDetail(ctx, index); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		table string
		want  int
	}{{"coverage_detail_branches", 1}, {"coverage_detail_gaps", 1}, {"coverage_detail_reasons", 2}} {
		var count int
		if err := store.db.QueryRow(`SELECT COUNT(*) FROM `+tc.table+` WHERE report_id=?`, index.ReportID).Scan(&count); err != nil || count != tc.want {
			t.Fatalf("%s count = %d, %v", tc.table, count, err)
		}
	}
	project, err := store.GetCoverageProject(ctx, index.ReportID)
	if err != nil || !reflect.DeepEqual(project, index.Project) {
		t.Fatalf("project = %#v, %v", project, err)
	}
	q := coveragedetail.FileQuery{ReportID: index.ReportID, WorkspaceGeneration: index.WorkspaceGeneration, Limit: 1}
	first, err := store.ListCoverageFiles(ctx, q)
	if err != nil || len(first.Items) != 1 || first.Items[0].RelativePath != "src/a.cpp" || first.NextCursor == "" {
		t.Fatalf("first page = %#v, %v", first, err)
	}
	q.Cursor = first.NextCursor
	second, err := store.ListCoverageFiles(ctx, q)
	if err != nil || len(second.Items) != 1 || second.Items[0].RelativePath != "src/b.cpp" || second.NextCursor != "" {
		t.Fatalf("second page = %#v, %v", second, err)
	}
	fq := coveragedetail.FunctionQuery{ReportID: index.ReportID, WorkspaceGeneration: index.WorkspaceGeneration, FileID: index.Files[0].ID, Limit: 1}
	functions, err := store.ListCoverageFunctions(ctx, fq)
	if err != nil || len(functions.Items) != 1 || functions.Items[0].ID != index.Files[0].Functions[0].ID {
		t.Fatalf("functions = %#v, %v", functions, err)
	}
	lq := coveragedetail.LineQuery{ReportID: index.ReportID, WorkspaceGeneration: index.WorkspaceGeneration, FileID: index.Files[0].ID, FunctionID: index.Files[0].Functions[0].ID, Limit: 1}
	lines, err := store.ListCoverageLines(ctx, lq)
	if err != nil || len(lines.Items) != 1 || lines.Items[0].Line != 1 || lines.NextCursor == "" {
		t.Fatalf("lines = %#v, %v", lines, err)
	}
	lq.Cursor = lines.NextCursor
	lines, err = store.ListCoverageLines(ctx, lq)
	if err != nil || len(lines.Items) != 1 || lines.Items[0].Line != 2 || lines.NextCursor != "" {
		t.Fatalf("next lines = %#v, %v", lines, err)
	}
	lq.Cursor = ""
	lq.Filter = "uncovered"
	lines, err = store.ListCoverageLines(ctx, lq)
	if err != nil || len(lines.Items) != 1 || lines.Items[0].Line != 2 {
		t.Fatalf("uncovered line filter = %#v, %v", lines, err)
	}
	q.Cursor = ""
	q.Sort = "desc"
	page, err := store.ListCoverageFiles(ctx, q)
	if err != nil || len(page.Items) != 1 || page.Items[0].RelativePath != "src/b.cpp" || page.NextCursor == "" {
		t.Fatalf("descending first page = %#v, %v", page, err)
	}
	q.Cursor = page.NextCursor
	page, err = store.ListCoverageFiles(ctx, q)
	if err != nil || len(page.Items) != 1 || page.Items[0].RelativePath != "src/a.cpp" || page.NextCursor != "" {
		t.Fatalf("descending second page = %#v, %v", page, err)
	}
}

func TestCoverageDetailCursorBindingAndLimits(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	index := detailStoreFixture(t, store, 7002)
	if err := store.PutCoverageDetail(ctx, index); err != nil {
		t.Fatal(err)
	}
	q := coveragedetail.FileQuery{ReportID: index.ReportID, WorkspaceGeneration: index.WorkspaceGeneration, Limit: 1}
	page, err := store.ListCoverageFiles(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []coveragedetail.FileQuery{
		{ReportID: index.ReportID, WorkspaceGeneration: index.WorkspaceGeneration, Limit: 0},
		{ReportID: index.ReportID, WorkspaceGeneration: index.WorkspaceGeneration, Limit: 201},
		{ReportID: index.ReportID, WorkspaceGeneration: index.WorkspaceGeneration, Limit: 2, Cursor: page.NextCursor},
		{ReportID: index.ReportID, WorkspaceGeneration: index.WorkspaceGeneration, Limit: 1, Filter: "src", Cursor: page.NextCursor},
		{ReportID: index.ReportID, WorkspaceGeneration: strings.Repeat("e", 64), Limit: 1, Cursor: page.NextCursor},
		{ReportID: index.ReportID, WorkspaceGeneration: index.WorkspaceGeneration, Limit: 1, Cursor: page.NextCursor + "a"},
	} {
		if _, err := store.ListCoverageFiles(ctx, bad); !errors.Is(err, task.ErrInvalidArgument) {
			t.Fatalf("query %#v error = %v", bad, err)
		}
	}
	other := detailStoreFixtureWithPrefix(t, store, 7008, "other/")
	if err := store.PutCoverageDetail(ctx, other); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ListCoverageFiles(ctx, coveragedetail.FileQuery{ReportID: other.ReportID, WorkspaceGeneration: other.WorkspaceGeneration, Limit: 1, Cursor: page.NextCursor}); !errors.Is(err, task.ErrInvalidArgument) {
		t.Fatalf("cross-report cursor = %v", err)
	}
	if _, err := store.ListCoverageLines(ctx, coveragedetail.LineQuery{ReportID: index.ReportID, WorkspaceGeneration: index.WorkspaceGeneration, FileID: index.Files[0].ID, FunctionID: index.Files[0].Functions[0].ID, Limit: 1001}); !errors.Is(err, task.ErrInvalidArgument) {
		t.Fatalf("line limit error = %v", err)
	}
}

func TestCoverageDetailRejectsDuplicateAndCorruptRowsAtomically(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	index := detailStoreFixture(t, store, 7003)
	bad := index
	bad.Files = append(append([]coveragedetail.File{}, index.Files...), index.Files[0])
	if err := store.PutCoverageDetail(ctx, bad); !errors.Is(err, task.ErrInvalidArgument) {
		t.Fatalf("duplicate file error = %v", err)
	}
	bad = index
	bad.Project.Status = coveragedetail.StatusCurrent
	if err := store.PutCoverageDetail(ctx, bad); !errors.Is(err, task.ErrInvalidArgument) {
		t.Fatalf("current project with incomplete file = %v", err)
	}
	if _, err := store.GetCoverageProject(ctx, index.ReportID); !errors.Is(err, task.ErrNotFound) {
		t.Fatalf("partial insert survived = %v", err)
	}
	bad = index
	bad.Files = append([]coveragedetail.File{}, index.Files...)
	bad.Files[0].Reasons = []string{"duplicate_reason", "duplicate_reason"}
	if err := store.PutCoverageDetail(ctx, bad); !errors.Is(err, task.ErrInvalidArgument) {
		t.Fatalf("late reason failure = %v", err)
	}
	if _, err := store.GetCoverageProject(ctx, index.ReportID); !errors.Is(err, task.ErrNotFound) {
		t.Fatalf("transaction left partial rows = %v", err)
	}
	bad = index
	bad.Files = append([]coveragedetail.File{}, index.Files...)
	bad.Files[0].Summary.Lines = coveragedomain.Metric{Covered: 2, Total: 1}
	if err := store.PutCoverageDetail(ctx, bad); !errors.Is(err, task.ErrInvalidArgument) {
		t.Fatalf("invalid summary error = %v", err)
	}
	if err := store.PutCoverageDetail(ctx, index); err != nil {
		t.Fatal(err)
	}
	if err := store.PutCoverageDetail(ctx, index); !errors.Is(err, task.ErrStorageUnavailable) {
		t.Fatalf("duplicate report error = %v", err)
	}
	if _, err := store.db.Exec(`UPDATE coverage_detail_reports SET project_summary_json='{}' WHERE report_id=?`, index.ReportID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetCoverageProject(ctx, index.ReportID); !errors.Is(err, task.ErrStorageUnavailable) {
		t.Fatalf("corrupt project error = %v", err)
	}
}

func TestCoverageDetailPartialReportRemainsIncomplete(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	mutation := coverageCompletionFixture(t, store, 7006, coveragedomain.OutcomePartial, "")
	if _, _, err := store.Apply(ctx, mutation); err != nil {
		t.Fatal(err)
	}
	report := mutation.FinishCoverage.Report
	index := coveragedetail.Index{WorkspaceGeneration: mutation.FinishCoverage.Run.Request.WorkspaceGeneration, ProjectID: mutation.FinishCoverage.Run.Request.ProjectID, ReportID: report.ID, RunID: report.RunID, Toolchain: report.Toolchain, Project: coveragedetail.Project{Summary: report.Summary, Status: coveragedetail.StatusIncomplete, Reasons: []string{"test_crashed", "test_timed_out"}}}
	if err := store.PutCoverageDetail(ctx, index); err != nil {
		t.Fatal(err)
	}
	got, err := store.GetCoverageProject(ctx, index.ReportID)
	if err != nil || got.Status != coveragedetail.StatusIncomplete || !reflect.DeepEqual(got.Reasons, index.Project.Reasons) {
		t.Fatalf("partial project = %#v, %v", got, err)
	}
	index.ReportID = mutation.FinishCoverage.Report.ID
	index.Project.Status = coveragedetail.StatusCurrent
	if err := store.PutCoverageDetail(ctx, index); !errors.Is(err, task.ErrInvalidArgument) {
		t.Fatalf("partial report promoted to current: %v", err)
	}
}

func TestCoverageDetailFunctionKeysetAndPageBounds(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	index := detailStoreFixture(t, store, 7007)
	fileID := index.Files[0].ID
	for _, semantic := range []string{"linkage:_Z3barv", "linkage:_Z3bazv"} {
		id, err := coveragedetail.StableFunctionID(fileID, semantic)
		if err != nil {
			t.Fatal(err)
		}
		index.Files[0].Functions = append(index.Files[0].Functions, coveragedetail.Function{ID: id, Name: "foo", Status: coveragedetail.StatusCurrent})
	}
	if err := store.PutCoverageDetail(ctx, index); err != nil {
		t.Fatal(err)
	}
	q := coveragedetail.FunctionQuery{ReportID: index.ReportID, WorkspaceGeneration: index.WorkspaceGeneration, FileID: fileID, Limit: 1}
	seen := map[string]bool{}
	for {
		page, err := store.ListCoverageFunctions(ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		for _, item := range page.Items {
			if seen[item.ID] {
				t.Fatalf("duplicate function %s", item.ID)
			}
			seen[item.ID] = true
		}
		if page.NextCursor == "" {
			break
		}
		q.Cursor = page.NextCursor
	}
	if len(seen) != 3 {
		t.Fatalf("paged %d functions, want 3", len(seen))
	}
	for _, limit := range []int{0, 201} {
		q.Limit = limit
		q.Cursor = ""
		if _, err := store.ListCoverageFunctions(ctx, q); !errors.Is(err, task.ErrInvalidArgument) {
			t.Fatalf("limit %d: %v", limit, err)
		}
	}
	lq := coveragedetail.LineQuery{ReportID: index.ReportID, WorkspaceGeneration: index.WorkspaceGeneration, FileID: fileID, Limit: 0}
	if _, err := store.ListCoverageLines(ctx, lq); !errors.Is(err, task.ErrInvalidArgument) {
		t.Fatalf("line limit zero = %v", err)
	}
}

func TestCoverageDetailIncompleteDeltaAndForeignKeyCleanup(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	index := detailStoreFixture(t, store, 7004)
	index.Project.Delta.Lines = coveragedetail.DeltaMetric{Covered: -1, Total: 2}
	index.Files[0].Delta.Functions = coveragedetail.DeltaMetric{Covered: 1, Total: 0}
	if err := store.PutCoverageDetail(ctx, index); err != nil {
		t.Fatal(err)
	}
	project, err := store.GetCoverageProject(ctx, index.ReportID)
	if err != nil || project.Status != coveragedetail.StatusIncomplete || project.Delta.Lines != index.Project.Delta.Lines {
		t.Fatalf("incomplete delta = %#v, %v", project, err)
	}
	if _, ok := project.Percent("lines"); ok {
		t.Fatal("incomplete project returned exact percent")
	}
	page, err := store.ListCoverageFiles(ctx, coveragedetail.FileQuery{ReportID: index.ReportID, WorkspaceGeneration: index.WorkspaceGeneration, Limit: 200})
	if err != nil || len(page.Items) != 2 || page.Items[0].Delta.Functions != index.Files[0].Delta.Functions {
		t.Fatalf("file delta = %#v, %v", page, err)
	}
	if _, err := store.db.Exec(`DELETE FROM coverage_reports WHERE report_id=?`, index.ReportID); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"coverage_detail_reports", "coverage_detail_files", "coverage_detail_functions", "coverage_detail_file_lines", "coverage_detail_function_lines", "coverage_detail_branches", "coverage_detail_gaps", "coverage_detail_reasons"} {
		var count int
		if err := store.db.QueryRow(`SELECT COUNT(*) FROM `+table+` WHERE report_id=?`, index.ReportID).Scan(&count); err != nil || count != 0 {
			t.Fatalf("%s rows after cascade = %d, %v", table, count, err)
		}
	}
}

func TestCoverageDetailCursorRejectsReportDriftDeletedParentAndChangedSort(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	index := detailStoreFixture(t, store, 7005)
	if err := store.PutCoverageDetail(ctx, index); err != nil {
		t.Fatal(err)
	}
	q := coveragedetail.FileQuery{ReportID: index.ReportID, WorkspaceGeneration: index.WorkspaceGeneration, Limit: 1}
	page, err := store.ListCoverageFiles(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	q.Cursor = page.NextCursor
	q.Sort = "desc"
	if _, err := store.ListCoverageFiles(ctx, q); !errors.Is(err, task.ErrInvalidArgument) {
		t.Fatalf("sort replay error = %v", err)
	}
	fnq := coveragedetail.FunctionQuery{ReportID: index.ReportID, WorkspaceGeneration: index.WorkspaceGeneration, FileID: index.Files[0].ID, Limit: 1}
	if _, err := store.db.Exec(`DELETE FROM coverage_detail_files WHERE report_id=? AND file_id=?`, index.ReportID, index.Files[0].ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ListCoverageFunctions(ctx, fnq); !errors.Is(err, task.ErrNotFound) {
		t.Fatalf("deleted parent error = %v", err)
	}
	if _, err := store.db.Exec(`UPDATE coverage_detail_reports SET workspace_generation=? WHERE report_id=?`, strings.Repeat("e", 64), index.ReportID); err != nil {
		t.Fatal(err)
	}
	q.Sort = ""
	if _, err := store.ListCoverageFiles(ctx, q); !errors.Is(err, task.ErrStorageUnavailable) {
		t.Fatalf("workspace drift error = %v", err)
	}
}
