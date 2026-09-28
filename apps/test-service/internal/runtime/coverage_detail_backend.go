package runtime

import (
	"context"
	"net/url"
	"path/filepath"
	"strings"

	"unit-test-ide.local/test-service/internal/coveragedetail"
	"unit-test-ide.local/test-service/internal/coveragenormalize"
	"unit-test-ide.local/test-service/internal/session"
	"unit-test-ide.local/test-service/internal/task"
)

type coverageDetailReader interface {
	CoverageDetailReady() bool
	GetCoverageProject(context.Context, string) (coveragedetail.Project, error)
	ListCoverageFiles(context.Context, coveragedetail.FileQuery) (coveragedetail.FilePage, error)
	ListCoverageFunctions(context.Context, coveragedetail.FunctionQuery) (coveragedetail.FunctionPage, error)
	ListCoverageLines(context.Context, coveragedetail.LineQuery) (coveragedetail.LinePage, error)
}

func (r *Runtime) detailReader() (coverageDetailReader, error) {
	if r == nil || !r.trustedWorkspace || r.store == nil || r.detailFailed.Load() {
		return nil, task.ErrStorageUnavailable
	}
	reader, ok := r.store.(coverageDetailReader)
	if !ok || !reader.CoverageDetailReady() {
		return nil, task.ErrStorageUnavailable
	}
	return reader, nil
}

func (r *Runtime) disableCoverageDetails(error) {
	if r != nil {
		r.detailFailed.Store(true)
	}
}

func (r *Runtime) CoverageDetailsReady() bool { _, err := r.detailReader(); return err == nil }

// CoverageDetailsProvider remains separate from the legacy coverage backend;
// production server wiring must still require a real managed-test provider.
func (r *Runtime) CoverageDetailsProvider() session.CoverageDetailsProvider {
	if !r.CoverageDetailsReady() {
		return nil
	}
	return r
}

func (r *Runtime) detailSourceStatus(ctx context.Context, reportID string, reader coverageDetailReader) (string, error) {
	report, err := r.store.GetCoverageReport(ctx, reportID)
	if err != nil {
		return "", err
	}
	run, err := r.store.GetCoverageRun(ctx, report.RunID)
	if err != nil {
		return "", err
	}
	snapshot, err := r.InspectWorkspace(ctx)
	if err != nil {
		return "", err
	}
	if snapshot.Generation != run.Request.WorkspaceGeneration {
		return "source_changed", nil
	}
	cursor := ""
	count := 0
	for {
		page, e := reader.ListCoverageFiles(ctx, coveragedetail.FileQuery{ReportID: reportID, WorkspaceGeneration: run.Request.WorkspaceGeneration, Limit: 200, Cursor: cursor})
		if e != nil {
			return "", e
		}
		if reason := detailSourceReason(r.workspaceRoot.NativePath, page.Items); reason != "" {
			return reason, nil
		}
		count += len(page.Items)
		if page.NextCursor == "" {
			break
		}
		if page.NextCursor == cursor || count > 500000 {
			return "", task.ErrInvalidArgument
		}
		cursor = page.NextCursor
	}
	// A persisted v1 report has no live source list. With no indexed files the
	// source set cannot be positively re-bound, so fail closed.
	if count == 0 {
		return "source_missing", nil
	}
	return "", nil
}

func detailSourceReason(root string, files []coveragedetail.File) string {
	for _, f := range files {
		if f.RelativePath == "" || len(f.RelativePath) > 8192 || f.SourceSHA256 == "" {
			return "source_missing"
		}
		parts := strings.Split(f.RelativePath, "/")
		decoded := make([]string, 0, len(parts))
		for _, part := range parts {
			value, err := url.PathUnescape(part)
			if err != nil || value == "" || value == "." || value == ".." || strings.ContainsAny(value, "/\\\x00") || url.PathEscape(value) != part {
				return "source_missing"
			}
			decoded = append(decoded, value)
		}
		path := filepath.Join(append([]string{root}, decoded...)...)
		binding, err := coveragenormalize.DigestSource(root, path, coveragenormalize.DefaultLimits())
		if err != nil {
			return "source_missing"
		}
		if binding.URI != f.RelativePath || binding.SHA256 != f.SourceSHA256 {
			return "source_changed"
		}
	}
	return ""
}

func (r *Runtime) GetCoverageProject(ctx context.Context, reportID string) (coveragedetail.Project, error) {
	reader, err := r.detailReader()
	if err != nil {
		return coveragedetail.Project{}, err
	}
	value, err := reader.GetCoverageProject(ctx, reportID)
	if err != nil {
		return coveragedetail.Project{}, err
	}
	reason, err := r.detailSourceStatus(ctx, reportID, reader)
	if err != nil {
		return coveragedetail.Project{}, err
	}
	if reason != "" {
		value.Status = coveragedetail.StatusStale
		value.Reasons = []string{reason}
		value.Delta = coveragedetail.DeltaSummary{}
	}
	return value, nil
}
func (r *Runtime) ListCoverageFiles(ctx context.Context, q coveragedetail.FileQuery) (coveragedetail.FilePage, error) {
	reader, err := r.detailReader()
	if err != nil {
		return coveragedetail.FilePage{}, err
	}
	reason, err := r.detailSourceStatus(ctx, q.ReportID, reader)
	if err != nil {
		return coveragedetail.FilePage{}, err
	}
	if reason != "" {
		return coveragedetail.FilePage{}, coveragedetail.ErrStale
	}
	return reader.ListCoverageFiles(ctx, q)
}
func (r *Runtime) ListCoverageFunctions(ctx context.Context, q coveragedetail.FunctionQuery) (coveragedetail.FunctionPage, error) {
	reader, err := r.detailReader()
	if err != nil {
		return coveragedetail.FunctionPage{}, err
	}
	reason, err := r.detailSourceStatus(ctx, q.ReportID, reader)
	if err != nil {
		return coveragedetail.FunctionPage{}, err
	}
	if reason != "" {
		return coveragedetail.FunctionPage{}, coveragedetail.ErrStale
	}
	return reader.ListCoverageFunctions(ctx, q)
}
func (r *Runtime) ListCoverageLines(ctx context.Context, q coveragedetail.LineQuery) (coveragedetail.LinePage, error) {
	reader, err := r.detailReader()
	if err != nil {
		return coveragedetail.LinePage{}, err
	}
	reason, err := r.detailSourceStatus(ctx, q.ReportID, reader)
	if err != nil {
		return coveragedetail.LinePage{}, err
	}
	if reason != "" {
		return coveragedetail.LinePage{}, coveragedetail.ErrStale
	}
	return reader.ListCoverageLines(ctx, q)
}
