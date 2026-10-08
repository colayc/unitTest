package taskstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"unit-test-ide.local/test-service/internal/coveragedetail"
	"unit-test-ide.local/test-service/internal/coveragedomain"
	"unit-test-ide.local/test-service/internal/task"
)

func TestCoverageDetailLinesProjectRealBranchArms(t *testing.T) {
	s := openTestStore(t)
	index := attestedDetailFixture(t, s, 7501)
	q := coveragedetail.LineQuery{ReportID: index.ReportID, WorkspaceGeneration: index.WorkspaceGeneration, FileID: index.Files[0].ID, Limit: 2}
	page, err := s.ListCoverageLines(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	want := coveragedetail.LineDetail{Line: 1, Count: 1, BranchesCovered: 3, BranchesTotal: 4}
	if len(page.Items) != 2 || page.Items[0] != want || page.Items[1] != (coveragedetail.LineDetail{Line: 2, Count: 1}) || page.NextCursor == "" {
		t.Fatalf("current branch evidence missing: page=%#v", page)
	}
}

func TestCoverageDetailLinesPagingMatchesCurrentSummaries(t *testing.T) {
	s := openTestStore(t)
	index := attestedDetailFixture(t, s, 7530)
	for _, parent := range []string{"file", "function"} {
		for _, order := range []string{"asc", "desc"} {
			for _, filter := range []string{"", "covered", "uncovered"} {
				for _, limit := range []int{1, 1000} {
					t.Run(fmt.Sprintf("%s/%s/%s/%d", parent, order, filter, limit), func(t *testing.T) {
						q := coveragedetail.LineQuery{ReportID: index.ReportID, WorkspaceGeneration: index.WorkspaceGeneration, Sort: order, Filter: filter, Limit: limit}
						wantSummary := index.Files[0].Summary
						if parent == "file" {
							q.FileID = index.Files[0].ID
						} else {
							q.FunctionID = index.Files[0].Functions[0].ID
							wantSummary = index.Files[0].Functions[0].Summary
						}
						want := []int64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}
						if filter == "covered" {
							want = []int64{1, 2, 3, 4, 5, 6, 7, 8}
						} else if filter == "uncovered" {
							want = []int64{9, 10}
						}
						if order == "desc" {
							for left, right := 0, len(want)-1; left < right; left, right = left+1, right-1 {
								want[left], want[right] = want[right], want[left]
							}
						}
						var got []int64
						var lines, branches coveragedomain.Metric
						for {
							page, err := s.ListCoverageLines(context.Background(), q)
							if err != nil || len(page.Items) == 0 || len(page.Items) > limit {
								t.Fatalf("page=%#v err=%v", page, err)
							}
							for _, item := range page.Items {
								if len(got) >= len(want) {
									t.Fatalf("duplicate/excess lines: %v", got)
								}
								got = append(got, item.Line)
								lines.Total++
								if item.Count > 0 {
									lines.Covered++
								}
								branches.Covered += item.BranchesCovered
								branches.Total += item.BranchesTotal
								if item.Line != 1 && (item.BranchesCovered != 0 || item.BranchesTotal != 0) {
									t.Fatalf("non-branch line gained evidence: %#v", item)
								}
							}
							if page.NextCursor == "" {
								break
							}
							if page.NextCursor == q.Cursor {
								t.Fatal("cursor did not advance")
							}
							q.Cursor = page.NextCursor
						}
						if !reflect.DeepEqual(got, want) {
							t.Fatalf("lines=%v want=%v", got, want)
						}
						if filter == "" && (lines != wantSummary.Lines || branches != wantSummary.Branches) {
							t.Fatalf("projected lines=%#v branches=%#v summary=%#v", lines, branches, wantSummary)
						}
						if filter == "covered" && (lines != (coveragedomain.Metric{Covered: 8, Total: 8}) || branches != (coveragedomain.Metric{Covered: 3, Total: 4})) {
							t.Fatalf("covered evidence: lines=%#v branches=%#v", lines, branches)
						}
						if filter == "uncovered" && (lines != (coveragedomain.Metric{Total: 2}) || branches != (coveragedomain.Metric{})) {
							t.Fatalf("uncovered evidence: lines=%#v branches=%#v", lines, branches)
						}
					})
				}
			}
		}
	}
	page, err := s.ListCoverageLines(context.Background(), coveragedetail.LineQuery{ReportID: index.ReportID, WorkspaceGeneration: index.WorkspaceGeneration, FileID: index.Files[1].ID, Limit: 1000})
	if err != nil || page.Items == nil || len(page.Items) != 0 || page.NextCursor != "" {
		t.Fatalf("empty file page=%#v err=%v", page, err)
	}
	raw, err := json.Marshal(page.Items)
	if err != nil || string(raw) != "[]" {
		t.Fatalf("empty items=%s err=%v", raw, err)
	}
}

func TestCoverageDetailLinesBindParentsCursorsAndLimits(t *testing.T) {
	s := openTestStore(t)
	index := attestedDetailFixture(t, s, 7531)
	other := attestedDetailFixture(t, s, 7532)
	q := coveragedetail.LineQuery{ReportID: index.ReportID, WorkspaceGeneration: index.WorkspaceGeneration, FileID: index.Files[0].ID, Limit: 1}
	first, err := s.ListCoverageLines(context.Background(), q)
	if err != nil || first.NextCursor == "" {
		t.Fatalf("first=%#v err=%v", first, err)
	}
	for _, mutate := range []func(*coveragedetail.LineQuery){
		func(q *coveragedetail.LineQuery) { q.Filter = "covered" },
		func(q *coveragedetail.LineQuery) { q.FunctionID = index.Files[0].Functions[0].ID },
		func(q *coveragedetail.LineQuery) { q.FileID = index.Files[1].ID },
		func(q *coveragedetail.LineQuery) { q.ReportID = other.ReportID },
		func(q *coveragedetail.LineQuery) { q.Sort = "desc" },
		func(q *coveragedetail.LineQuery) { q.Limit = 1000 },
		func(q *coveragedetail.LineQuery) { q.WorkspaceGeneration = strings.Repeat("f", 64) },
	} {
		bad := q
		bad.Cursor = first.NextCursor
		mutate(&bad)
		if _, err := s.ListCoverageLines(context.Background(), bad); !errors.Is(err, task.ErrInvalidArgument) {
			t.Fatalf("cross-scope cursor accepted: %#v err=%v", bad, err)
		}
	}
	for _, bad := range []coveragedetail.LineQuery{
		{ReportID: q.ReportID, WorkspaceGeneration: q.WorkspaceGeneration, FileID: strings.Repeat("f", 32), Limit: 1},
		{ReportID: q.ReportID, WorkspaceGeneration: q.WorkspaceGeneration, FunctionID: strings.Repeat("f", 32), Limit: 1},
		{ReportID: q.ReportID, WorkspaceGeneration: q.WorkspaceGeneration, FileID: index.Files[1].ID, FunctionID: index.Files[0].Functions[0].ID, Limit: 1},
	} {
		if _, err := s.ListCoverageLines(context.Background(), bad); !errors.Is(err, task.ErrNotFound) {
			t.Fatalf("wrong parent accepted: %#v err=%v", bad, err)
		}
	}
	for _, limit := range []int{0, 1001} {
		bad := q
		bad.Limit = limit
		if _, err := s.ListCoverageLines(context.Background(), bad); !errors.Is(err, task.ErrInvalidArgument) {
			t.Fatalf("limit=%d err=%v", limit, err)
		}
	}
}

func TestCoverageDetailLinesRejectCorruptedCurrentGraph(t *testing.T) {
	cases := []struct{ name, statement string }{
		{"deleted branch", `DELETE FROM coverage_detail_branches WHERE report_id=? AND ordinal=3`},
		{"branch summary drift", `UPDATE coverage_detail_branches SET count=0 WHERE report_id=? AND ordinal=0`},
		{"function moved to another file", `UPDATE coverage_detail_functions SET file_id=(SELECT file_id FROM coverage_detail_files WHERE report_id=? AND relative_path='src/b.cpp') WHERE report_id=?`},
		{"source manifest drift", `UPDATE coverage_detail_manifest_files SET source_sha256=printf('%064d',0) WHERE report_id=? AND relative_path='src/b.cpp'`},
		{"toolchain drift", `UPDATE coverage_detail_reports SET toolchain_json='{}' WHERE report_id=?`},
		{"branch without attributed line", `UPDATE coverage_detail_branches SET line=12 WHERE report_id=? AND ordinal=0`},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := openTestStore(t)
			index := attestedDetailFixture(t, s, 7510+i)
			args := []any{index.ReportID}
			if tc.name == "function moved to another file" {
				args = append(args, index.ReportID)
			}
			if _, err := s.db.Exec(tc.statement, args...); err != nil {
				t.Fatal(err)
			}
			q := coveragedetail.LineQuery{ReportID: index.ReportID, WorkspaceGeneration: index.WorkspaceGeneration, FileID: index.Files[0].ID, Limit: 2}
			if _, err := s.ListCoverageLines(context.Background(), q); !errors.Is(err, task.ErrStorageUnavailable) {
				t.Fatalf("corrupted graph exposed as exact lines: %v", err)
			}
		})
	}
	t.Run("duplicate branch owner", func(t *testing.T) {
		s := openTestStore(t)
		index := attestedDetailFixture(t, s, 7520)
		file, fn := index.Files[0].ID, index.Files[0].Functions[0].ID
		linkage := "_Z3barv"
		other, err := coveragedetail.StableFunctionID(file, fmt.Sprintf("linkage:%d:%s:signature:", len(linkage), linkage))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.db.Exec(`INSERT INTO coverage_detail_functions SELECT report_id,file_id,?,'bar',?,'',start_line,start_column,end_line,end_column,summary_json,delta_json,status FROM coverage_detail_functions WHERE report_id=? AND function_id=?`, other, linkage, index.ReportID, fn); err != nil {
			t.Fatal(err)
		}
		if _, err := s.db.Exec(`INSERT INTO coverage_detail_function_lines SELECT report_id,?,line,count FROM coverage_detail_function_lines WHERE report_id=? AND function_id=?`, other, index.ReportID, fn); err != nil {
			t.Fatal(err)
		}
		if _, err := s.db.Exec(`INSERT INTO coverage_detail_branches SELECT report_id,?,line,column,ordinal,count FROM coverage_detail_branches WHERE report_id=? AND function_id=?`, other, index.ReportID, fn); err != nil {
			t.Fatal(err)
		}
		q := coveragedetail.LineQuery{ReportID: index.ReportID, WorkspaceGeneration: index.WorkspaceGeneration, FileID: file, Limit: 2}
		if _, err := s.ListCoverageLines(context.Background(), q); !errors.Is(err, task.ErrStorageUnavailable) {
			t.Fatalf("duplicate branch owner exposed: %v", err)
		}
	})
}

func TestCoverageDetailLinesRejectUnattributedBranchAcrossParents(t *testing.T) {
	s := openTestStore(t)
	index := attestedDetailFixture(t, s, 7540)
	// This covered arm needs no gap. Moving it preserves every metric and gap,
	// so the existing full attestation does not mask the missing attribution check.
	if _, err := s.db.Exec(`UPDATE coverage_detail_branches SET line=12 WHERE report_id=? AND ordinal=0`, index.ReportID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReadValidatedCoverageIndex(context.Background(), coveragedetail.CurrentIndexQuery{
		ProjectID: index.ProjectID, ReportID: index.ReportID, WorkspaceGeneration: index.WorkspaceGeneration,
	}); err != nil {
		t.Fatalf("counterexample masked by another attestation guard: %v", err)
	}
	q := coveragedetail.LineQuery{ReportID: index.ReportID, WorkspaceGeneration: index.WorkspaceGeneration, FileID: index.Files[1].ID, Limit: 2}
	page, err := s.ListCoverageLines(context.Background(), q)
	if !errors.Is(err, task.ErrStorageUnavailable) {
		t.Fatalf("empty sibling file exposed a corrupt current graph: page=%#v err=%v", page, err)
	}
}

// A coherent current graph with two functions sharing one positive branch arm.
// Function metrics are 1/1, file unions are 1/1, and there are no uncovered gaps.
// Existing graph validation accepts it, isolating branch ownership from drift.
func currentLineOwnershipFixture(t *testing.T) coveragedetail.Index {
	t.Helper()
	index := coveragedetail.Index{ProjectID: "core", ReportID: strings.Repeat("a", 32), WorkspaceGeneration: strings.Repeat("b", 64), Project: coveragedetail.Project{Status: coveragedetail.StatusCurrent}}
	for _, path := range []string{"src/a.cpp", "src/b.cpp"} {
		id, err := coveragedetail.StableFileID(index.ProjectID, path)
		if err != nil {
			t.Fatal(err)
		}
		index.Files = append(index.Files, coveragedetail.File{ID: id, RelativePath: path, SourceSHA256: strings.Repeat("c", 64), Status: coveragedetail.StatusCurrent})
	}
	metric := coveragedomain.Metric{Covered: 1, Total: 1}
	for _, name := range []string{"first", "second"} {
		id, err := coveragedetail.StableFunctionID(index.Files[0].ID, fmt.Sprintf("qualified:%d:%s:signature:", len(name), name))
		if err != nil {
			t.Fatal(err)
		}
		index.Files[0].Functions = append(index.Files[0].Functions, coveragedetail.Function{
			ID: id, Name: name, Start: coveragedomain.SourceLocation{Line: 1}, End: coveragedomain.SourceLocation{Line: 1}, Status: coveragedetail.StatusCurrent,
			Summary: coveragedomain.Summary{Functions: metric, Lines: metric, Branches: metric},
			Lines:   []coveragedetail.Line{{Line: 1, Count: 1}}, Branches: []coveragedetail.Branch{{Line: 1, Column: 1, Ordinal: 0, Count: 1}},
		})
	}
	index.Files[0].Lines = []coveragedetail.Line{{Line: 1, Count: 1}}
	index.Files[0].Summary = coveragedomain.Summary{Functions: coveragedomain.Metric{Covered: 2, Total: 2}, Lines: metric, Branches: metric}
	index.Project.Summary = index.Files[0].Summary
	return index
}

func TestCoverageDetailLinesRejectSiblingBranchOwnershipBeforeNarrowing(t *testing.T) {
	index := currentLineOwnershipFixture(t)
	if err := validateDetailIndex(index); err != nil {
		t.Fatalf("ownership counterexample has unrelated graph drift: %v", err)
	}
	for _, parent := range []struct{ name, file, function string }{
		{"first function", index.Files[0].ID, index.Files[0].Functions[0].ID},
		{"second function", index.Files[0].ID, index.Files[0].Functions[1].ID},
		{"empty sibling file", index.Files[1].ID, ""},
	} {
		t.Run(parent.name, func(t *testing.T) {
			lines, err := projectCurrentLines(index, parent.file, parent.function)
			if !errors.Is(err, task.ErrStorageUnavailable) {
				t.Fatalf("sibling branch owner discarded before validation: lines=%#v err=%v", lines, err)
			}
		})
	}
}

func TestCoverageDetailLinesRejectSiblingBranchAttributionBeforeNarrowing(t *testing.T) {
	index := currentLineOwnershipFixture(t)
	// Make the functions disjoint, but move the sibling's positive arm away
	// from its attributed line. Numeric summaries and the gap set stay coherent.
	index.Files[0].Functions[1].Lines[0].Line = 2
	index.Files[0].Functions[1].Branches[0].Line = 12
	index.Files[0].Lines = append(index.Files[0].Lines, coveragedetail.Line{Line: 2, Count: 1})
	index.Files[0].Summary.Lines = coveragedomain.Metric{Covered: 2, Total: 2}
	index.Files[0].Summary.Branches = coveragedomain.Metric{Covered: 2, Total: 2}
	index.Project.Summary = index.Files[0].Summary
	if err := validateDetailIndex(index); err != nil {
		t.Fatalf("attribution counterexample has unrelated graph drift: %v", err)
	}
	lines, err := projectCurrentLines(index, index.Files[0].ID, index.Files[0].Functions[0].ID)
	if !errors.Is(err, task.ErrStorageUnavailable) {
		t.Fatalf("unattributed sibling arm discarded before validation: lines=%#v err=%v", lines, err)
	}
}

func TestCoverageDetailLinesAllowSameArmKeyInDifferentFiles(t *testing.T) {
	index := currentLineOwnershipFixture(t)
	second := index.Files[0].Functions[1]
	var err error
	second.ID, err = coveragedetail.StableFunctionID(index.Files[1].ID, "qualified:6:second:signature:")
	if err != nil {
		t.Fatal(err)
	}
	index.Files[0].Functions = index.Files[0].Functions[:1]
	index.Files[0].Summary = index.Files[0].Functions[0].Summary
	index.Files[1].Functions = []coveragedetail.Function{second}
	index.Files[1].Lines = []coveragedetail.Line{{Line: 1, Count: 1}}
	index.Files[1].Summary = second.Summary
	index.Project.Summary = coveragedomain.Summary{Functions: coveragedomain.Metric{Covered: 2, Total: 2}, Lines: coveragedomain.Metric{Covered: 2, Total: 2}, Branches: coveragedomain.Metric{Covered: 2, Total: 2}}
	if err := validateDetailIndex(index); err != nil {
		t.Fatal(err)
	}
	for _, file := range index.Files {
		lines, err := projectCurrentLines(index, file.ID, file.Functions[0].ID)
		if err != nil || lines[1] != (coveragedetail.LineDetail{Line: 1, Count: 1, BranchesCovered: 1, BranchesTotal: 1}) {
			t.Fatalf("unrelated files' arm keys collided: lines=%#v err=%v", lines, err)
		}
	}
}
