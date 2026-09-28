package session

import (
	"context"
	"encoding/base64"
	"errors"
	"strings"

	"unit-test-ide.local/test-service/internal/coveragedetail"
	"unit-test-ide.local/test-service/internal/coveragedomain"
	"unit-test-ide.local/test-service/internal/protocol"
	coveragev16 "unit-test-ide.local/test-service/internal/protocolmodel/v1_6/coverage"
	"unit-test-ide.local/test-service/internal/task"
)

type CoverageDetailBackend interface {
	CoverageDetailsReady() bool
	GetCoverageProject(context.Context, string) (coveragedetail.Project, error)
	ListCoverageFiles(context.Context, coveragedetail.FileQuery) (coveragedetail.FilePage, error)
	ListCoverageFunctions(context.Context, coveragedetail.FunctionQuery) (coveragedetail.FunctionPage, error)
	ListCoverageLines(context.Context, coveragedetail.LineQuery) (coveragedetail.LinePage, error)
}

func (s *Session) handleCoverageDetail(ctx context.Context, version string, request protocol.Request, backend CoverageDetailBackend) HandleResult {
	var reportID, generation, projectID string
	var fileID, functionID, cursor string
	limit := 100
	switch request.Method {
	case "coverage/details/project/get":
		p, e := decodeStrict[coveragev16.CoverageDetailProjectRequestV16](request.Payload)
		if e != nil {
			return invalidPayload(version, request)
		}
		reportID, generation, projectID = p.CoverageReportID, p.WorkspaceGeneration, p.ProjectID
	case "coverage/details/files/list":
		p, e := decodeStrict[coveragev16.CoverageDetailFilesRequestV16](request.Payload)
		if e != nil {
			return invalidPayload(version, request)
		}
		reportID, generation, projectID = p.CoverageReportID, p.WorkspaceGeneration, p.ProjectID
		cursor, limit, e = detailPage(p.Cursor, p.Limit, 200)
		if e != nil {
			return invalidPayload(version, request)
		}
	case "coverage/details/functions/list":
		p, e := decodeStrict[coveragev16.CoverageDetailFunctionsRequestV16](request.Payload)
		if e != nil {
			return invalidPayload(version, request)
		}
		reportID, generation, fileID = p.CoverageReportID, p.WorkspaceGeneration, p.FileID
		cursor, limit, e = detailPage(p.Cursor, p.Limit, 200)
		if e != nil {
			return invalidPayload(version, request)
		}
	case "coverage/details/lines/list":
		p, e := decodeStrict[coveragev16.CoverageDetailLinesRequestV16](request.Payload)
		if e != nil {
			return invalidPayload(version, request)
		}
		reportID, generation = p.CoverageReportID, p.WorkspaceGeneration
		if p.FileID != nil {
			fileID = *p.FileID
		}
		if p.FunctionID != nil {
			functionID = *p.FunctionID
		}
		cursor, limit, e = detailPage(p.Cursor, p.Limit, 1000)
		if e != nil {
			return invalidPayload(version, request)
		}
	default:
		return handled(protocol.Failure(version, request, "METHOD_NOT_FOUND", "method is not supported", false))
	}
	if !validID(reportID) || !validHash(generation) || (request.Method == "coverage/details/project/get" || request.Method == "coverage/details/files/list") && !validProjectID(projectID) || fileID != "" && !validID(fileID) || functionID != "" && !validID(functionID) || request.Method == "coverage/details/lines/list" && (fileID == "") == (functionID == "") || request.Method == "coverage/details/functions/list" && fileID == "" {
		return invalidPayload(version, request)
	}
	// The durable detail API keys by report alone for project reads. Bind every
	// caller-provided generation/project to the canonical v1 run first.
	report, err := s.coverageBackend.GetCoverageReport(ctx, reportID)
	if err != nil {
		return detailFailure(version, request, err)
	}
	run, err := s.coverageBackend.GetCoverageRun(ctx, report.RunID)
	if err != nil {
		return detailFailure(version, request, err)
	}
	if report.ID != reportID || run.ReportID != reportID || run.Request.WorkspaceGeneration != generation || projectID != "" && run.Request.ProjectID != projectID {
		return invalidPayload(version, request)
	}
	projectID = run.Request.ProjectID
	switch request.Method {
	case "coverage/details/project/get":
		value, err := backend.GetCoverageProject(ctx, reportID)
		if err != nil {
			return detailFailure(version, request, err)
		}
		if !validDetailProjection(value.Status, value.Summary, value.Delta) || !validDetailReasons(value.Reasons) {
			return detailFailure(version, request, coveragedetail.ErrInvalidDetail)
		}
		var count int64
		if value.Status != coveragedetail.StatusStale {
			count, err = countDetailFiles(ctx, backend, reportID, generation)
			if err != nil {
				return detailFailure(version, request, err)
			}
		}
		return handled(protocol.Success(version, request, coveragev16.CoverageProjectV16{CoverageReportID: reportID, WorkspaceGeneration: generation, ProjectID: projectID, FileCount: count, Status: coveragev16.CoverageDetailStatusV16(value.Status), Reasons: detailReasons(value.Reasons), Summary: detailSummary(value.Summary, value.Delta)}))
	case "coverage/details/files/list":
		page, err := backend.ListCoverageFiles(ctx, coveragedetail.FileQuery{ReportID: reportID, WorkspaceGeneration: generation, Cursor: cursor, Limit: limit})
		if err != nil {
			return detailFailure(version, request, err)
		}
		out := coveragev16.CoverageFilePageV16{CoverageReportID: reportID, WorkspaceGeneration: generation, Items: make([]coveragev16.CoverageFileV16, 0, len(page.Items)), NextCursor: detailNext(page.NextCursor)}
		for _, f := range page.Items {
			stableID, pathErr := coveragedetail.StableFileID(projectID, f.RelativePath)
			if !validID(f.ID) || pathErr != nil || stableID != f.ID || !validHash(f.SourceSHA256) || !validDetailProjection(f.Status, f.Summary, f.Delta) || !validDetailReasons(f.Reasons) {
				return detailFailure(version, request, coveragedetail.ErrInvalidDetail)
			}
			count, e := countDetailFunctions(ctx, backend, reportID, generation, f.ID)
			if e != nil {
				return detailFailure(version, request, e)
			}
			out.Items = append(out.Items, coveragev16.CoverageFileV16{FileID: f.ID, RelativePath: f.RelativePath, SourceSha256: f.SourceSHA256, FunctionCount: count, Status: coveragev16.CoverageDetailStatusV16(f.Status), Reasons: detailReasons(f.Reasons), Summary: detailSummary(f.Summary, f.Delta)})
		}
		return handled(protocol.Success(version, request, out))
	case "coverage/details/functions/list":
		page, err := backend.ListCoverageFunctions(ctx, coveragedetail.FunctionQuery{ReportID: reportID, WorkspaceGeneration: generation, FileID: fileID, Cursor: cursor, Limit: limit})
		if err != nil {
			return detailFailure(version, request, err)
		}
		out := coveragev16.CoverageFunctionPageV16{CoverageReportID: reportID, WorkspaceGeneration: generation, Items: make([]coveragev16.CoverageFunctionV16, 0, len(page.Items)), NextCursor: detailNext(page.NextCursor)}
		for _, f := range page.Items {
			if !validID(f.ID) || f.Start.Line < 1 || f.Start.Line > coveragedomain.MaxSafeInteger || f.End.Line < f.Start.Line || f.End.Line > coveragedomain.MaxSafeInteger || f.Name == "" || len(f.Name) > 512 || strings.ContainsAny(f.Name, "\x00\r\n") || !validDetailProjection(f.Status, f.Summary, f.Delta) || !validDetailReasons(f.Reasons) {
				return detailFailure(version, request, coveragedetail.ErrInvalidDetail)
			}
			out.Items = append(out.Items, coveragev16.CoverageFunctionV16{FileID: fileID, FunctionID: f.ID, QualifiedName: f.Name, StartLine: f.Start.Line, EndLine: f.End.Line, Status: coveragev16.CoverageDetailStatusV16(f.Status), Reasons: detailReasons(f.Reasons), Summary: detailSummary(f.Summary, f.Delta)})
		}
		return handled(protocol.Success(version, request, out))
	case "coverage/details/lines/list":
		page, err := backend.ListCoverageLines(ctx, coveragedetail.LineQuery{ReportID: reportID, WorkspaceGeneration: generation, FileID: fileID, FunctionID: functionID, Cursor: cursor, Limit: limit})
		if err != nil {
			return detailFailure(version, request, err)
		}
		// The durable v1.6 index currently retains line counts but not a
		// per-line baseline/branch projection. Those fields are required by
		// the wire schema; never fabricate zeroes for nonempty pages.
		if len(page.Items) != 0 {
			return handled(protocol.Failure(version, request, "SERVICE_UNHEALTHY", "line and branch evidence is incomplete", false))
		}
		out := coveragev16.CoverageLinePageV16{CoverageReportID: reportID, WorkspaceGeneration: generation, Items: make([]coveragev16.CoverageLineDetailV16, 0, len(page.Items)), NextCursor: detailNext(page.NextCursor)}
		return handled(protocol.Success(version, request, out))
	}
	return invalidPayload(version, request)
}

func detailPage(c *string, l *int64, max int) (string, int, error) {
	cursor := ""
	limit := 100
	if c != nil {
		decoded, err := base64.RawURLEncoding.DecodeString(*c)
		if *c == "" || len(*c) > 2048 || err != nil || base64.RawURLEncoding.EncodeToString(decoded) != *c {
			return "", 0, task.ErrInvalidArgument
		}
		cursor = *c
	}
	if l != nil {
		if *l < 1 || *l > int64(max) {
			return "", 0, task.ErrInvalidArgument
		}
		limit = int(*l)
	}
	return cursor, limit, nil
}
func detailNext(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
func detailSummary(s coveragedomain.Summary, d coveragedetail.DeltaSummary) coveragev16.CoverageSummaryV16 {
	return coveragev16.CoverageSummaryV16{Functions: coveragev16.CoverageMetricV16{Covered: s.Functions.Covered, Total: s.Functions.Total, CoveredDelta: d.Functions.Covered}, Lines: coveragev16.CoverageMetricV16{Covered: s.Lines.Covered, Total: s.Lines.Total, CoveredDelta: d.Lines.Covered}, Branches: coveragev16.CoverageMetricV16{Covered: s.Branches.Covered, Total: s.Branches.Total, CoveredDelta: d.Branches.Covered}}
}
func detailReasons(values []string) []coveragev16.CoverageDetailReasonV16 {
	out := make([]coveragev16.CoverageDetailReasonV16, 0, len(values))
	seen := map[coveragev16.CoverageDetailReasonV16]bool{}
	for _, r := range values {
		var mapped coveragev16.CoverageDetailReasonV16
		switch r {
		case "attribution_ambiguous":
			mapped = coveragev16.AttributionAmbiguous
		case "baseline_unavailable":
			mapped = coveragev16.BaselineUnavailable
		case "source_changed":
			mapped = coveragev16.SourceChanged
		case "source_missing":
			mapped = coveragev16.SourceMissing
		case "tool_identity_changed":
			mapped = coveragev16.ToolIdentityChanged
		default:
			mapped = coveragev16.ReportPartial
		}
		if !seen[mapped] {
			out = append(out, mapped)
			seen[mapped] = true
		}
	}
	return out
}
func validDetailReasons(values []string) bool {
	for _, reason := range values {
		switch reason {
		case "attribution_ambiguous", "baseline_unavailable", "source_changed", "source_missing", "tool_identity_changed",
			"detail_aggregate_mismatch", "observation_limit", "test_crashed", "test_timed_out", "profile_missing_for_failed_invocation":
		default:
			return false
		}
	}
	return true
}
func validDetailProjection(status coveragedetail.Status, s coveragedomain.Summary, d coveragedetail.DeltaSummary) bool {
	if status != coveragedetail.StatusCurrent && status != coveragedetail.StatusStale && status != coveragedetail.StatusIncomplete {
		return false
	}
	return protocol.ValidCoverageSummaryV16(detailSummary(s, d))
}
func detailFailure(version string, request protocol.Request, err error) HandleResult {
	code, message, retry := "SERVICE_UNHEALTHY", "coverage detail service is unavailable", true
	switch {
	case errors.Is(err, coveragedetail.ErrStale):
		code, message, retry = "WORKSPACE_CHANGED", "coverage detail source binding is stale", false
	case errors.Is(err, coveragedetail.ErrInvalidDetail):
		code, message, retry = "SERVICE_UNHEALTHY", "coverage detail is unavailable", true
	case errors.Is(err, task.ErrInvalidArgument):
		code, message, retry = "INVALID_MESSAGE", "invalid coverage detail page or cursor", false
	case errors.Is(err, task.ErrNotFound):
		code, message, retry = "COVERAGE_REPORT_NOT_FOUND", "coverage detail is unavailable", false
	}
	return handled(protocol.Failure(version, request, code, message, retry))
}
func countDetailFiles(ctx context.Context, b CoverageDetailBackend, report, generation string) (int64, error) {
	var n int64
	cursor := ""
	for {
		p, e := b.ListCoverageFiles(ctx, coveragedetail.FileQuery{ReportID: report, WorkspaceGeneration: generation, Limit: 200, Cursor: cursor})
		if e != nil {
			return 0, e
		}
		n += int64(len(p.Items))
		if p.NextCursor == "" {
			return n, nil
		}
		if p.NextCursor == cursor || n > 500000 {
			return 0, task.ErrInvalidArgument
		}
		cursor = p.NextCursor
	}
}
func countDetailFunctions(ctx context.Context, b CoverageDetailBackend, report, generation, file string) (int64, error) {
	var n int64
	cursor := ""
	for {
		p, e := b.ListCoverageFunctions(ctx, coveragedetail.FunctionQuery{ReportID: report, WorkspaceGeneration: generation, FileID: file, Limit: 200, Cursor: cursor})
		if e != nil {
			return 0, e
		}
		n += int64(len(p.Items))
		if p.NextCursor == "" {
			return n, nil
		}
		if p.NextCursor == cursor || n > 500000 {
			return 0, task.ErrInvalidArgument
		}
		cursor = p.NextCursor
	}
}
