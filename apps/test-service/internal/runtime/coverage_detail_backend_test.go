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
