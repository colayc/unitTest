package taskstore

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"unit-test-ide.local/test-service/internal/coveragedetail"
	"unit-test-ide.local/test-service/internal/coveragedomain"
	"unit-test-ide.local/test-service/internal/task"
)

func attestedDetailFixture(t *testing.T, s *Store, number int) coveragedetail.Index {
	t.Helper()
	index := detailStoreFixture(t, s, number)
	fn := &index.Files[0].Functions[0]
	oldID := fn.ID
	semantic := fmt.Sprintf("linkage:%d:%s:signature:", len(fn.LinkageName), fn.LinkageName)
	var err error
	fn.ID, err = coveragedetail.StableFunctionID(index.Files[0].ID, semantic)
	if err != nil {
		t.Fatal(err)
	}
	for i := range index.Gaps {
		if index.Gaps[i].FunctionID != oldID {
			continue
		}
		index.Gaps[i].FunctionID = fn.ID
		index.Gaps[i].ID, err = coveragedetail.StableGapID(index.ReportID, fn.ID, index.Gaps[i].Kind, index.Gaps[i].Location, index.Gaps[i].Ordinal)
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := s.PutCoverageDetail(context.Background(), index); err != nil {
		t.Fatal(err)
	}
	return index
}

func TestValidatedCoverageIndexRejectsReportWorkspaceProjectMismatch(t *testing.T) {
	s := openTestStore(t)
	index := attestedDetailFixture(t, s, 7201)
	ctx := context.Background()
	q := coveragedetail.CurrentIndexQuery{ProjectID: index.ProjectID, ReportID: index.ReportID, WorkspaceGeneration: index.WorkspaceGeneration}
	got, err := s.ReadValidatedCoverageIndex(ctx, q)
	if err != nil || got.ProjectID != index.ProjectID || len(got.Files) != 2 || len(got.Gaps) != 3 {
		t.Fatalf("current index = %#v, %v", got, err)
	}
	for _, bad := range []coveragedetail.CurrentIndexQuery{
		{ProjectID: "wrong", ReportID: q.ReportID, WorkspaceGeneration: q.WorkspaceGeneration},
		{ProjectID: q.ProjectID, ReportID: strings.Repeat("f", 32), WorkspaceGeneration: q.WorkspaceGeneration},
		{ProjectID: q.ProjectID, ReportID: q.ReportID, WorkspaceGeneration: strings.Repeat("f", 64)},
	} {
		if _, err := s.ReadValidatedCoverageIndex(ctx, bad); err == nil {
			t.Fatalf("mismatched binding accepted: %#v", bad)
		}
	}
}

func TestValidatedCoverageIndexRejectsTamperedIdentityAndGapDrift(t *testing.T) {
	cases := []struct {
		name, statement string
	}{
		{"file ID", `UPDATE coverage_detail_files SET relative_path='src/c.cpp' WHERE relative_path='src/a.cpp'`},
		{"function ID", `UPDATE coverage_detail_functions SET linkage_name='_Z3barv' WHERE name='foo'`},
		{"noncanonical function identity", `UPDATE coverage_detail_functions SET linkage_name=' _Z3foov ' WHERE name='foo'`},
		{"invalid function location", `UPDATE coverage_detail_functions SET start_line=0,start_column=1 WHERE name='foo'`},
		{"gap ID", `UPDATE coverage_detail_gaps SET gap_id='aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa' WHERE kind='line' AND line=9`},
		{"gap position", `UPDATE coverage_detail_gaps SET line=7 WHERE kind='line' AND line=9`},
		{"function missing", `DELETE FROM coverage_detail_functions WHERE name='foo'`},
		{"status reason", `UPDATE coverage_detail_files SET status='incomplete' WHERE relative_path='src/a.cpp'`},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := openTestStore(t)
			index := attestedDetailFixture(t, s, 7210+i)
			if _, err := s.db.Exec(tc.statement); err != nil {
				t.Fatal(err)
			}
			q := coveragedetail.CurrentIndexQuery{ProjectID: index.ProjectID, ReportID: index.ReportID, WorkspaceGeneration: index.WorkspaceGeneration}
			if _, err := s.ReadValidatedCoverageIndex(context.Background(), q); !errors.Is(err, task.ErrStorageUnavailable) {
				t.Fatalf("tampered index err = %v", err)
			}
		})
	}
}

func TestValidatedCoverageIndexRequiresAnAuthoritativeGap(t *testing.T) {
	s := openTestStore(t)
	index := attestedDetailFixture(t, s, 7220)
	q := coveragedetail.CurrentIndexQuery{ProjectID: index.ProjectID, ReportID: index.ReportID, WorkspaceGeneration: index.WorkspaceGeneration}
	got, err := s.ReadValidatedCoverageIndex(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := got.GapByID(index.Gaps[0].ID); !ok {
		t.Fatal("known uncovered gap unavailable")
	}
	covered, _ := coveragedetail.StableGapID(index.ReportID, index.Gaps[0].FunctionID, "line", coveragedomain.SourceLocation{Line: 1}, 0)
	if _, ok := got.GapByID(covered); ok {
		t.Fatal("caller-derived covered coordinate accepted as gap")
	}
}

func TestValidatedCoverageIndexDoesNotChangeLegacyCoverageAndPaging(t *testing.T) {
	s := openTestStore(t)
	index := detailStoreFixture(t, s, 7230)
	ctx := context.Background()
	if _, err := s.GetCoverageReport(ctx, index.ReportID); err != nil {
		t.Fatalf("legacy report read: %v", err)
	}
	q := coveragedetail.CurrentIndexQuery{ProjectID: index.ProjectID, ReportID: index.ReportID, WorkspaceGeneration: index.WorkspaceGeneration}
	if _, err := s.ReadValidatedCoverageIndex(ctx, q); !errors.Is(err, task.ErrNotFound) {
		t.Fatalf("missing optional index = %v", err)
	}
	if err := s.PutCoverageDetail(ctx, index); err != nil {
		t.Fatal(err)
	}
	pageQuery := coveragedetail.FileQuery{ReportID: index.ReportID, WorkspaceGeneration: index.WorkspaceGeneration, Limit: 1}
	first, err := s.ListCoverageFiles(ctx, pageQuery)
	if err != nil || len(first.Items) != 1 || first.NextCursor == "" {
		t.Fatalf("legacy first page = %#v, %v", first, err)
	}
	pageQuery.Cursor = first.NextCursor
	second, err := s.ListCoverageFiles(ctx, pageQuery)
	if err != nil || len(second.Items) != 1 || second.NextCursor != "" {
		t.Fatalf("legacy second page = %#v, %v", second, err)
	}
}
