package runtime

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"unit-test-ide.local/test-service/internal/coveragedetail"
	"unit-test-ide.local/test-service/internal/coveragedomain"
	"unit-test-ide.local/test-service/internal/coveragenormalize"
	"unit-test-ide.local/test-service/internal/discovery"
	"unit-test-ide.local/test-service/internal/workspace"
)

type sourceBoundDetailStore struct {
	runtimeStore
	report coveragedomain.Report
	run    coveragedomain.Run
	file   coveragedetail.File
	index  coveragedetail.Index
}

func (s *sourceBoundDetailStore) CoverageDetailReady() bool { return true }
func (s *sourceBoundDetailStore) GetCoverageReport(context.Context, string) (coveragedomain.Report, error) {
	return s.report, nil
}
func (s *sourceBoundDetailStore) GetCoverageRun(context.Context, string) (coveragedomain.Run, error) {
	return s.run, nil
}
func (s *sourceBoundDetailStore) GetCoverageProject(context.Context, string) (coveragedetail.Project, error) {
	return coveragedetail.Project{Status: coveragedetail.StatusCurrent}, nil
}
func (s *sourceBoundDetailStore) ListCoverageFiles(context.Context, coveragedetail.FileQuery) (coveragedetail.FilePage, error) {
	return coveragedetail.FilePage{Items: []coveragedetail.File{s.file}}, nil
}
func (s *sourceBoundDetailStore) ListCoverageFunctions(context.Context, coveragedetail.FunctionQuery) (coveragedetail.FunctionPage, error) {
	return coveragedetail.FunctionPage{}, nil
}
func (s *sourceBoundDetailStore) ListCoverageLines(context.Context, coveragedetail.LineQuery) (coveragedetail.LinePage, error) {
	return coveragedetail.LinePage{}, nil
}
func (s *sourceBoundDetailStore) ReadValidatedCoverageIndex(context.Context, coveragedetail.CurrentIndexQuery) (coveragedetail.Index, error) {
	return s.index, nil
}

type sourceBoundCoordinator struct {
	runtimeCoordinator
	generation string
}

func (c sourceBoundCoordinator) Inspect(context.Context) (discovery.Snapshot, error) {
	return discovery.Snapshot{Generation: c.generation}, nil
}

func TestRuntimeCoverageDetailMarksChangedSourceStale(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "source.c")
	if err := os.WriteFile(path, []byte("before\n"), 0600); err != nil {
		t.Fatal(err)
	}
	binding, err := coveragenormalize.DigestSource(dir, path, coveragenormalize.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	root, err := workspace.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	reportID := strings.Repeat("a", 32)
	generation := strings.Repeat("b", 64)
	store := &sourceBoundDetailStore{report: coveragedomain.Report{ID: reportID, RunID: strings.Repeat("c", 32)}, run: coveragedomain.Run{Request: coveragedomain.Request{WorkspaceGeneration: generation}}, file: coveragedetail.File{RelativePath: binding.URI, SourceSHA256: binding.SHA256}}
	r := &Runtime{store: store, coordinator: sourceBoundCoordinator{generation: generation}, workspaceRoot: root, trustedWorkspace: true}
	project, err := r.GetCoverageProject(context.Background(), reportID)
	if err != nil || project.Status != coveragedetail.StatusCurrent {
		t.Fatalf("unchanged project=%#v, %v", project, err)
	}
	if err := os.WriteFile(path, []byte("after\n"), 0600); err != nil {
		t.Fatal(err)
	}
	project, err = r.GetCoverageProject(context.Background(), reportID)
	if err != nil || project.Status != coveragedetail.StatusStale || len(project.Reasons) != 1 || project.Reasons[0] != "source_changed" {
		t.Fatalf("changed project=%#v, %v", project, err)
	}
	if _, err := r.ListCoverageFiles(context.Background(), coveragedetail.FileQuery{ReportID: reportID, WorkspaceGeneration: generation}); err != coveragedetail.ErrStale {
		t.Fatalf("changed file listing err=%v", err)
	}
}

func TestDetailSourceBindingRejectsChangedMissingAndEscapingFiles(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "source.c")
	if err := os.WriteFile(path, []byte("int f() { return 1; }\n"), 0600); err != nil {
		t.Fatal(err)
	}
	binding, err := coveragenormalize.DigestSource(root, path, coveragenormalize.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	files := []coveragedetail.File{{RelativePath: binding.URI, SourceSHA256: binding.SHA256}}
	if reason := detailSourceReason(root, files); reason != "" {
		t.Fatalf("current source reason=%q", reason)
	}
	if err := os.WriteFile(path, []byte("int f() { return 2; }\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if reason := detailSourceReason(root, files); reason != "source_changed" {
		t.Fatalf("changed source reason=%q", reason)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if reason := detailSourceReason(root, files); reason != "source_missing" {
		t.Fatalf("missing source reason=%q", reason)
	}
	files[0].RelativePath = "../secret.c"
	if reason := detailSourceReason(root, files); reason != "source_missing" {
		t.Fatalf("escaping source reason=%q", reason)
	}
	files[0].RelativePath = strings.Repeat("a", 8193)
	if reason := detailSourceReason(root, files); reason != "source_missing" {
		t.Fatalf("oversized source reason=%q", reason)
	}
}

func TestRuntimeCurrentCoverageTargetAttestsEverySourceAndExactGap(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"a.c", "b.c"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("int f() { return 1; }\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	root, err := workspace.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	projectID, reportID, generation := "core", strings.Repeat("a", 32), strings.Repeat("b", 64)
	files := make([]coveragedetail.File, 0, 2)
	for _, name := range []string{"a.c", "b.c"} {
		binding, err := coveragenormalize.DigestSource(dir, filepath.Join(dir, name), coveragenormalize.DefaultLimits())
		if err != nil {
			t.Fatal(err)
		}
		id, err := coveragedetail.StableFileID(projectID, binding.URI)
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, coveragedetail.File{ID: id, RelativePath: binding.URI, SourceSHA256: binding.SHA256, Status: coveragedetail.StatusCurrent})
	}
	functionID, _ := coveragedetail.StableFunctionID(files[0].ID, "qualified:1:f:signature:")
	files[0].Functions = []coveragedetail.Function{{ID: functionID, Name: "f", Status: coveragedetail.StatusCurrent}}
	gapID, _ := coveragedetail.StableGapID(reportID, functionID, "line", coveragedomain.SourceLocation{Line: 2}, 0)
	index := coveragedetail.Index{ProjectID: projectID, ReportID: reportID, RunID: strings.Repeat("c", 32), WorkspaceGeneration: generation, Project: coveragedetail.Project{Status: coveragedetail.StatusCurrent}, Files: files, Gaps: []coveragedetail.Gap{{ID: gapID, FileID: files[0].ID, FunctionID: functionID, Kind: "line", Location: coveragedomain.SourceLocation{Line: 2}}}}
	store := &sourceBoundDetailStore{report: coveragedomain.Report{ID: reportID, RunID: strings.Repeat("c", 32)}, run: coveragedomain.Run{Request: coveragedomain.Request{WorkspaceGeneration: generation, ProjectID: projectID}}, index: index}
	r := &Runtime{store: store, coordinator: sourceBoundCoordinator{generation: generation}, workspaceRoot: root, trustedWorkspace: true}
	attested, err := r.ReadCurrentCoverageIndex(context.Background(), coveragedetail.CurrentIndexQuery{ProjectID: projectID, ReportID: reportID, WorkspaceGeneration: generation})
	if err != nil || len(attested.Files) != 2 {
		t.Fatalf("current index = %#v, %v", attested, err)
	}
	q := coveragedetail.CurrentTargetQuery{CurrentIndexQuery: coveragedetail.CurrentIndexQuery{ProjectID: projectID, ReportID: reportID, WorkspaceGeneration: generation}, FileID: files[0].ID, FunctionID: functionID, GapID: gapID}
	target, err := r.ResolveCurrentCoverageTarget(context.Background(), q)
	if err != nil || target.File.RelativePath != "a.c" || target.Function == nil || target.Function.ID != functionID || target.Gap == nil || target.Gap.Location.Line != 2 {
		t.Fatalf("resolved target = %#v, %v", target, err)
	}
	q.GapID = strings.Repeat("f", 32)
	if _, err := r.ResolveCurrentCoverageTarget(context.Background(), q); err == nil {
		t.Fatal("nonexistent gap accepted")
	}
	q.GapID = gapID
	if err := os.WriteFile(filepath.Join(dir, "b.c"), []byte("changed\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := r.ResolveCurrentCoverageTarget(context.Background(), q); err != coveragedetail.ErrStale {
		t.Fatalf("other indexed source drift = %v", err)
	}
	if _, err := r.ReadCurrentCoverageIndex(context.Background(), q.CurrentIndexQuery); err != coveragedetail.ErrStale {
		t.Fatalf("current index retained after source drift = %v", err)
	}
}
