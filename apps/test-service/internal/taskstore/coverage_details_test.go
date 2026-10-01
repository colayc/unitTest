package taskstore

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

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
	index := coveragedetail.Index{
		WorkspaceGeneration: mutation.FinishCoverage.Run.Request.WorkspaceGeneration,
		ProjectID:           mutation.FinishCoverage.Run.Request.ProjectID,
		ReportID:            report.ID, RunID: report.RunID, Toolchain: report.Toolchain,
		Project: coveragedetail.Project{Summary: report.Summary, Status: coveragedetail.StatusCurrent, Reasons: []string{}},
		Files: []coveragedetail.File{
			{ID: fileA, RelativePath: "src/a.cpp", SourceSHA256: strings.Repeat("a", 64), Summary: report.Summary, Status: coveragedetail.StatusCurrent, Reasons: []string{}, Functions: []coveragedetail.Function{{ID: functionID, Name: "foo", LinkageName: "_Z3foov", Start: coveragedomain.SourceLocation{Line: 1}, End: coveragedomain.SourceLocation{Line: 12}, Summary: report.Summary, Status: coveragedetail.StatusCurrent, Reasons: []string{}}}},
			{ID: fileB, RelativePath: "src/b.cpp", SourceSHA256: strings.Repeat("b", 64), Status: coveragedetail.StatusCurrent, Reasons: []string{}},
		},
	}
	for line := int64(1); line <= 10; line++ {
		count := int64(1)
		if line > 8 {
			count = 0
		}
		item := coveragedetail.Line{Line: line, Count: count}
		index.Files[0].Lines = append(index.Files[0].Lines, item)
		index.Files[0].Functions[0].Lines = append(index.Files[0].Functions[0].Lines, item)
		if count == 0 {
			id, _ := coveragedetail.StableGapID(report.ID, functionID, "line", coveragedomain.SourceLocation{Line: line}, 0)
			index.Gaps = append(index.Gaps, coveragedetail.Gap{ID: id, FileID: fileA, FunctionID: functionID, Kind: "line", Location: coveragedomain.SourceLocation{Line: line}})
		}
	}
	for ordinal := int64(0); ordinal < 4; ordinal++ {
		count := int64(1)
		if ordinal == 3 {
			count = 0
		}
		branch := coveragedetail.Branch{Line: 1, Column: 1, Ordinal: ordinal, Count: count}
		index.Files[0].Functions[0].Branches = append(index.Files[0].Functions[0].Branches, branch)
		if count == 0 {
			id, _ := coveragedetail.StableGapID(report.ID, functionID, "branch", coveragedomain.SourceLocation{Line: 1, Column: 1}, ordinal)
			index.Gaps = append(index.Gaps, coveragedetail.Gap{ID: id, FileID: fileA, FunctionID: functionID, Kind: "branch", Location: coveragedomain.SourceLocation{Line: 1, Column: 1}, Ordinal: ordinal})
		}
	}
	return index
}

func copyDetailIndex(value coveragedetail.Index) coveragedetail.Index {
	copy := value
	copy.Project.Reasons = append([]string{}, value.Project.Reasons...)
	copy.Files = append([]coveragedetail.File{}, value.Files...)
	for i := range copy.Files {
		copy.Files[i].Reasons = append([]string{}, value.Files[i].Reasons...)
		copy.Files[i].Lines = append([]coveragedetail.Line{}, value.Files[i].Lines...)
		copy.Files[i].Functions = append([]coveragedetail.Function{}, value.Files[i].Functions...)
		for j := range copy.Files[i].Functions {
			copy.Files[i].Functions[j].Reasons = append([]string{}, value.Files[i].Functions[j].Reasons...)
			copy.Files[i].Functions[j].Lines = append([]coveragedetail.Line{}, value.Files[i].Functions[j].Lines...)
			copy.Files[i].Functions[j].Branches = append([]coveragedetail.Branch{}, value.Files[i].Functions[j].Branches...)
		}
	}
	copy.Gaps = append([]coveragedetail.Gap{}, value.Gaps...)
	return copy
}

func TestCoverageDetailRejectsContradictoryCanonicalIndex(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name   string
		mutate func(*coveragedetail.Index)
	}{
		{"project total contradicts files", func(v *coveragedetail.Index) {
			v.Project.Status = coveragedetail.StatusIncomplete
			v.Project.Reasons = []string{"detail_aggregate_mismatch"}
			v.Project.Summary.Lines.Total--
			v.Gaps = nil
		}},
		{"file count contradicts lines", func(v *coveragedetail.Index) { v.Files[0].Summary.Lines.Covered-- }},
		{"function count contradicts observations", func(v *coveragedetail.Index) { v.Files[0].Functions[0].Summary.Branches.Covered-- }},
		{"file line contradicts function line", func(v *coveragedetail.Index) { v.Files[0].Lines[0].Count = 0 }},
		{"current file has incomplete function", func(v *coveragedetail.Index) {
			v.Files[0].Functions[0].Status = coveragedetail.StatusIncomplete
			v.Files[0].Functions[0].Reasons = []string{"attribution_ambiguous"}
		}},
		{"incomplete project publishes gaps", func(v *coveragedetail.Index) {
			v.Project.Status = coveragedetail.StatusIncomplete
			v.Project.Reasons = []string{"attribution_ambiguous"}
		}},
		{"gap points at covered line", func(v *coveragedetail.Index) {
			v.Gaps[0].Location = coveragedomain.SourceLocation{Line: 1}
			v.Gaps[0].ID, _ = coveragedetail.StableGapID(v.ReportID, v.Gaps[0].FunctionID, "line", v.Gaps[0].Location, 0)
		}},
		{"gap points at missing branch", func(v *coveragedetail.Index) {
			v.Gaps[2].Ordinal = 9
			v.Gaps[2].ID, _ = coveragedetail.StableGapID(v.ReportID, v.Gaps[2].FunctionID, "branch", v.Gaps[2].Location, 9)
		}},
		{"missing uncovered gap", func(v *coveragedetail.Index) { v.Gaps = v.Gaps[1:] }},
		{"child reason not propagated", func(v *coveragedetail.Index) {
			v.Project.Status = coveragedetail.StatusIncomplete
			v.Project.Reasons = []string{"other_reason"}
			v.Files[0].Status = coveragedetail.StatusIncomplete
			v.Files[0].Reasons = []string{"attribution_ambiguous"}
			v.Gaps = nil
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := openTestStore(t)
			index := detailStoreFixture(t, store, 7011)
			bad := copyDetailIndex(index)
			tc.mutate(&bad)
			if err := store.PutCoverageDetail(ctx, bad); !errors.Is(err, task.ErrInvalidArgument) {
				t.Fatalf("contradictory index accepted: %v", err)
			}
			if _, err := store.GetCoverageProject(ctx, index.ReportID); !errors.Is(err, task.ErrNotFound) {
				t.Fatalf("invalid index persisted: %v", err)
			}
		})
	}
}

func TestCoverageDetailValidatorAcceptsCanonicalBuildIndex(t *testing.T) {
	report := coveragedomain.Report{
		ID: strings.Repeat("a", 32), RunID: strings.Repeat("b", 32), TestRunID: strings.Repeat("c", 32), ArtifactID: strings.Repeat("d", 32),
		SchemaVersion: coveragedomain.SchemaVersion10, CreatedAt: time.Now().UTC(),
		Completeness: coveragedomain.Completeness{Outcome: coveragedomain.OutcomeAvailable},
		Summary:      coveragedomain.Summary{Functions: coveragedomain.Metric{Covered: 1, Total: 1}, Lines: coveragedomain.Metric{Covered: 1, Total: 2}, Branches: coveragedomain.Metric{Covered: 0, Total: 1}},
		Toolchain:    coverageToolchain(7012), Sources: []coveragedomain.SourceSnapshot{{URI: "src/a.cpp", SHA256: strings.Repeat("e", 64)}},
	}
	index, err := coveragedetail.Build(coveragedetail.BuildInput{WorkspaceGeneration: strings.Repeat("f", 64), ProjectID: "core", Report: report, Sources: report.Sources, Functions: []coveragedomain.FunctionObservation{{QualifiedName: "foo", File: "src/a.cpp", ExecutionCount: 1, Lines: []coveragedomain.LineObservation{{Line: 1, Count: 1}, {Line: 2, Count: 0}}, Branches: []coveragedomain.BranchObservation{{Line: 2, Column: 1, Ordinal: 0, HasOrdinal: true, Count: 0}}}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := validateDetailIndex(index); err != nil {
		t.Fatalf("canonical Build index rejected: %v", err)
	}
	if !validDetailReportSemantics(index, report) {
		t.Fatal("canonical current Build status rejected")
	}
	report.Sources = append(report.Sources, coveragedomain.SourceSnapshot{URI: "src/b.cpp", SHA256: strings.Repeat("9", 64)})
	input := coveragedetail.BuildInput{WorkspaceGeneration: strings.Repeat("f", 64), ProjectID: "core", Report: report, Sources: report.Sources, Functions: []coveragedomain.FunctionObservation{{QualifiedName: "foo", File: "src/a.cpp", ExecutionCount: 1, Lines: []coveragedomain.LineObservation{{Line: 1, Count: 1}, {Line: 2, Count: 0}}, Branches: []coveragedomain.BranchObservation{{Line: 2, Column: 1, Ordinal: 0, HasOrdinal: true, Count: 0}}}, {QualifiedName: "bar", File: "src/b.cpp", IncompleteReason: coveragedomain.ObservationIncompleteAttributionAmbiguous}}}
	index, err = coveragedetail.Build(input)
	if err != nil {
		t.Fatal(err)
	}
	if index.Project.Status != coveragedetail.StatusIncomplete || index.Files[0].Status != coveragedetail.StatusCurrent || index.Files[1].Status != coveragedetail.StatusIncomplete {
		t.Fatalf("Build sibling status = %#v", index)
	}
	if err := validateDetailIndex(index); err != nil || !validDetailReportSemantics(index, report) {
		t.Fatalf("valid incomplete sibling rejected: %v", err)
	}
	report.Summary.Lines.Covered = 2
	input.Report = report
	index, err = coveragedetail.Build(input)
	if err != nil {
		t.Fatal(err)
	}
	if index.Files[0].Status != coveragedetail.StatusIncomplete || index.Files[0].Functions[0].Status != coveragedetail.StatusCurrent {
		t.Fatalf("Build mismatch status = %#v", index.Files[0])
	}
	if err := validateDetailIndex(index); err != nil || !validDetailReportSemantics(index, report) {
		t.Fatalf("valid aggregate mismatch rejected: %v", err)
	}
}

func TestCoverageDetailRejectsMissingReportMismatchReason(t *testing.T) {
	store := openTestStore(t)
	index := detailStoreFixture(t, store, 7013)
	index.Files = nil
	index.Gaps = nil
	index.Project.Summary = coveragedomain.Summary{}
	index.Project.Status = coveragedetail.StatusIncomplete
	index.Project.Reasons = []string{"other_reason"}
	if err := store.PutCoverageDetail(context.Background(), index); !errors.Is(err, task.ErrInvalidArgument) {
		t.Fatalf("missing aggregate mismatch reason = %v", err)
	}
}

func TestCoverageDetailRejectsCurrentChildOfPartialReport(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	mutation := coverageCompletionFixture(t, store, 7014, coveragedomain.OutcomePartial, "")
	if _, _, err := store.Apply(ctx, mutation); err != nil {
		t.Fatal(err)
	}
	report := mutation.FinishCoverage.Report
	fileID, err := coveragedetail.StableFileID(mutation.FinishCoverage.Run.Request.ProjectID, "src/a.cpp")
	if err != nil {
		t.Fatal(err)
	}
	index := coveragedetail.Index{WorkspaceGeneration: mutation.FinishCoverage.Run.Request.WorkspaceGeneration, ProjectID: mutation.FinishCoverage.Run.Request.ProjectID, ReportID: report.ID, RunID: report.RunID, Toolchain: report.Toolchain, Project: coveragedetail.Project{Status: coveragedetail.StatusIncomplete, Reasons: []string{"detail_aggregate_mismatch", "test_crashed", "test_timed_out"}}, Files: []coveragedetail.File{{ID: fileID, RelativePath: "src/a.cpp", SourceSHA256: strings.Repeat("a", 64), Status: coveragedetail.StatusCurrent}}}
	if err := store.PutCoverageDetail(ctx, index); !errors.Is(err, task.ErrInvalidArgument) {
		t.Fatalf("partial report current child = %v", err)
	}
}

func TestCoverageDetailRejectsUncausedParentStatusAndUnknownReasons(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name   string
		mutate func(*coveragedetail.Index)
	}{
		{"incomplete project without incomplete child", func(v *coveragedetail.Index) {
			v.Project.Status = coveragedetail.StatusIncomplete
			v.Project.Reasons = []string{"attribution_ambiguous"}
			v.Gaps = nil
		}},
		{"incomplete project with arbitrary reason", func(v *coveragedetail.Index) {
			v.Project.Status = coveragedetail.StatusIncomplete
			v.Project.Reasons = []string{"invented_reason"}
			v.Gaps = nil
		}},
		{"stale project with arbitrary reason", func(v *coveragedetail.Index) {
			v.Project.Status = coveragedetail.StatusStale
			v.Project.Reasons = []string{"invented_reason"}
			v.Gaps = nil
		}},
		{"incomplete file without incomplete function", func(v *coveragedetail.Index) {
			v.Project.Status = coveragedetail.StatusIncomplete
			v.Project.Reasons = []string{"attribution_ambiguous"}
			v.Files[0].Status = coveragedetail.StatusIncomplete
			v.Files[0].Reasons = []string{"attribution_ambiguous"}
			v.Gaps = nil
		}},
		{"unknown function reason", func(v *coveragedetail.Index) {
			v.Project.Status = coveragedetail.StatusIncomplete
			v.Project.Reasons = []string{"invented_reason"}
			v.Files[0].Status = coveragedetail.StatusIncomplete
			v.Files[0].Reasons = []string{"invented_reason"}
			v.Files[0].Functions[0].Status = coveragedetail.StatusIncomplete
			v.Files[0].Functions[0].Reasons = []string{"invented_reason"}
			v.Gaps = nil
		}},
		{"mismatch reason without mismatch", func(v *coveragedetail.Index) {
			v.Project.Status = coveragedetail.StatusIncomplete
			v.Project.Reasons = []string{"detail_aggregate_mismatch"}
			v.Gaps = nil
		}},
		{"partial reason on available report", func(v *coveragedetail.Index) {
			v.Project.Status = coveragedetail.StatusIncomplete
			v.Project.Reasons = []string{"test_crashed"}
			v.Gaps = nil
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := openTestStore(t)
			index := detailStoreFixture(t, store, 7021)
			bad := copyDetailIndex(index)
			tc.mutate(&bad)
			if err := store.PutCoverageDetail(ctx, bad); !errors.Is(err, task.ErrInvalidArgument) {
				t.Fatalf("uncausal status/reason accepted: %v", err)
			}
			if _, err := store.GetCoverageProject(ctx, index.ReportID); !errors.Is(err, task.ErrNotFound) {
				t.Fatalf("invalid index persisted: %v", err)
			}
		})
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
	}{{"coverage_detail_branches", 4}, {"coverage_detail_gaps", 3}, {"coverage_detail_reasons", 0}} {
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
	if err != nil || len(lines.Items) != 1 || lines.Items[0].Line != 2 || lines.NextCursor == "" {
		t.Fatalf("next lines = %#v, %v", lines, err)
	}
	lq.Cursor = ""
	lq.FileID = ""
	byFunction, err := store.ListCoverageLines(ctx, lq)
	if err != nil || len(byFunction.Items) != 1 || byFunction.Items[0].Line != 1 {
		t.Fatalf("function-only line query = %#v, %v", byFunction, err)
	}
	lq.FileID = index.Files[0].ID
	lq.Filter = "uncovered"
	lines, err = store.ListCoverageLines(ctx, lq)
	if err != nil || len(lines.Items) != 1 || lines.Items[0].Line != 9 {
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
	bad.Files = append([]coveragedetail.File{}, index.Files...)
	bad.Files[1].Status = coveragedetail.StatusIncomplete
	bad.Files[1].Reasons = []string{"attribution_ambiguous"}
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
	index := coveragedetail.Index{WorkspaceGeneration: mutation.FinishCoverage.Run.Request.WorkspaceGeneration, ProjectID: mutation.FinishCoverage.Run.Request.ProjectID, ReportID: report.ID, RunID: report.RunID, Toolchain: report.Toolchain, Project: coveragedetail.Project{Status: coveragedetail.StatusIncomplete, Reasons: []string{"detail_aggregate_mismatch", "test_crashed", "test_timed_out"}}}
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
		index.Files[0].Functions = append(index.Files[0].Functions, coveragedetail.Function{ID: id, Name: "foo", Status: coveragedetail.StatusIncomplete, Reasons: []string{"attribution_ambiguous"}})
	}
	index.Project.Status = coveragedetail.StatusIncomplete
	index.Project.Reasons = []string{"attribution_ambiguous"}
	index.Files[0].Status = coveragedetail.StatusIncomplete
	index.Files[0].Reasons = []string{"attribution_ambiguous"}
	index.Gaps = nil
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
	index.Project.Status = coveragedetail.StatusIncomplete
	index.Project.Reasons = []string{"attribution_ambiguous"}
	index.Files[1].Status = coveragedetail.StatusIncomplete
	index.Files[1].Reasons = []string{"attribution_ambiguous"}
	missingID, err := coveragedetail.StableFunctionID(index.Files[1].ID, "linkage:_Z3barv")
	if err != nil {
		t.Fatal(err)
	}
	index.Files[1].Functions = []coveragedetail.Function{{ID: missingID, Name: "bar", Status: coveragedetail.StatusIncomplete, Reasons: []string{"attribution_ambiguous"}}}
	index.Gaps = nil
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
