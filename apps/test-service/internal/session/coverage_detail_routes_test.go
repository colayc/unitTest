package session_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"unit-test-ide.local/test-service/internal/coveragedetail"
	"unit-test-ide.local/test-service/internal/coveragedomain"
	"unit-test-ide.local/test-service/internal/protocol"
	"unit-test-ide.local/test-service/internal/session"
	"unit-test-ide.local/test-service/internal/task"
)

type detailRouteBackend struct {
	ready      bool
	err        error
	calls      int
	lines      []coveragedetail.Line
	files      []coveragedetail.File
	functions  []coveragedetail.Function
	reasons    []string
	status     coveragedetail.Status
	noBaseline bool
	summary    coveragedomain.Summary
}
type detailCoverageBackend struct{ *coverageBackend }

func (*detailCoverageBackend) GetCoverageReport(_ context.Context, id string) (coveragedomain.Report, error) {
	return coveragedomain.Report{ID: id, RunID: strings.Repeat("d", 32)}, nil
}
func (*detailCoverageBackend) GetCoverageRun(_ context.Context, id string) (coveragedomain.Run, error) {
	return coveragedomain.Run{ID: id, ReportID: strings.Repeat("a", 32), Request: coveragedomain.Request{ProjectID: "core", WorkspaceGeneration: strings.Repeat("b", 64)}}, nil
}
func (b *detailRouteBackend) CoverageDetailsReady() bool { return b.ready }
func (b *detailRouteBackend) GetCoverageProject(context.Context, string) (coveragedetail.Project, error) {
	b.calls++
	reasons := b.reasons
	status := b.status
	if status == "" {
		status = coveragedetail.StatusCurrent
	}
	baseline := strings.Repeat("e", 32)
	if b.noBaseline {
		baseline = ""
	}
	return coveragedetail.Project{Status: status, Reasons: reasons, BaselineReportID: baseline, Summary: b.summary}, b.err
}
func (b *detailRouteBackend) ListCoverageFiles(context.Context, coveragedetail.FileQuery) (coveragedetail.FilePage, error) {
	b.calls++
	return coveragedetail.FilePage{Items: b.files}, b.err
}

func TestCoverageDetailRouteRejectsUnsafeStoredProjection(t *testing.T) {
	b := &detailRouteBackend{ready: true}
	s := readyDetailSession(t, b)
	report := strings.Repeat("a", 32)
	generation := strings.Repeat("b", 64)
	request := requestVersion(t, protocol.Version16, "coverage/details/files/list", map[string]any{"coverageReportId": report, "projectId": "core", "workspaceGeneration": generation})
	b.files = []coveragedetail.File{{ID: strings.Repeat("c", 32), RelativePath: "/home/user/private.c", SourceSHA256: strings.Repeat("d", 64), Status: coveragedetail.StatusCurrent}}
	got := s.Handle(context.Background(), request)
	if got.Response.Error == nil || got.Response.Error.Code != "SERVICE_UNHEALTHY" || strings.Contains(got.Response.Error.Message, "private") {
		t.Fatalf("absolute stored path leaked: %#v", got.Response)
	}
	b.files = nil
	b.reasons = []string{"unexpected_internal_reason"}
	got = s.Handle(context.Background(), requestVersion(t, protocol.Version16, "coverage/details/project/get", map[string]any{"coverageReportId": report, "projectId": "core", "workspaceGeneration": generation}))
	if got.Response.Error == nil || got.Response.Error.Code != "SERVICE_UNHEALTHY" {
		t.Fatalf("unknown stored reason was synthesized: %#v", got.Response)
	}
	b.reasons = nil
	b.functions = []coveragedetail.Function{{ID: strings.Repeat("e", 32), Name: strings.Repeat("x", 513), Start: coveragedomain.SourceLocation{Line: 1}, End: coveragedomain.SourceLocation{Line: 2}, Status: coveragedetail.StatusCurrent}}
	got = s.Handle(context.Background(), requestVersion(t, protocol.Version16, "coverage/details/functions/list", map[string]any{"coverageReportId": report, "fileId": strings.Repeat("c", 32), "workspaceGeneration": generation}))
	if got.Response.Error == nil || got.Response.Error.Code != "SERVICE_UNHEALTHY" {
		t.Fatalf("out-of-schema name emitted: %#v", got.Response)
	}
}

func TestCoverageDetailRouteServesCanonicalFileIdentity(t *testing.T) {
	id, err := coveragedetail.StableFileID("core", "src/source.c")
	if err != nil {
		t.Fatal(err)
	}
	b := &detailRouteBackend{ready: true, files: []coveragedetail.File{{ID: id, RelativePath: "src/source.c", SourceSHA256: strings.Repeat("d", 64), Status: coveragedetail.StatusCurrent}}}
	s := readyDetailSession(t, b)
	got := s.Handle(context.Background(), requestVersion(t, protocol.Version16, "coverage/details/files/list", map[string]any{"coverageReportId": strings.Repeat("a", 32), "projectId": "core", "workspaceGeneration": strings.Repeat("b", 64)}))
	if got.Response.Error != nil {
		t.Fatalf("canonical file rejected: %#v", got.Response)
	}
}

func TestCoverageDetailRouteClosesAfterProviderLossAndOldVersionDowngrade(t *testing.T) {
	b := &detailRouteBackend{ready: true}
	s := readyDetailSession(t, b)
	payload := map[string]any{"coverageReportId": strings.Repeat("a", 32), "projectId": "core", "workspaceGeneration": strings.Repeat("b", 64)}
	b.ready = false
	got := s.Handle(context.Background(), requestVersion(t, protocol.Version16, "coverage/details/project/get", payload))
	if got.Response.Error == nil || got.Response.Error.Code != "SERVICE_UNHEALTHY" || b.calls != 0 {
		t.Fatalf("unavailable provider reached: %#v calls=%d", got.Response, b.calls)
	}
	old := session.NewWithGeneration("0123456789abcdef", "linux", "unix-socket", &fakeBackend{}, &detailCoverageBackend{&coverageBackend{fakeBackend: &fakeBackend{}}}, &generationBackend{ready: true})
	handshake := old.Handle(context.Background(), requestVersion(t, protocol.Version16, "handshake", map[string]any{"token": "0123456789abcdef", "clientName": "test", "clientVersion": "1.0.0", "supportedProtocolVersions": []string{protocol.Version16, protocol.Version15}}))
	if handshake.Response.Error != nil || old.NegotiatedVersion() != protocol.Version15 {
		t.Fatalf("old service negotiation: %#v, %q", handshake.Response, old.NegotiatedVersion())
	}
	got = old.Handle(context.Background(), requestVersion(t, protocol.Version15, "coverage/details/project/get", payload))
	if got.Response.Error == nil || got.Response.Error.Code != "PROTOCOL_FEATURE_UNAVAILABLE" {
		t.Fatalf("old service detail route: %#v", got.Response)
	}
}

func TestCoverageDetailRoutesDoNotInventBaselineDeltas(t *testing.T) {
	b := &detailRouteBackend{ready: true, noBaseline: true, summary: coveragedomain.Summary{Functions: coveragedomain.Metric{Covered: 1, Total: 1}}}
	fileID, err := coveragedetail.StableFileID("core", "src/source.c")
	if err != nil {
		t.Fatal(err)
	}
	b.files = []coveragedetail.File{{ID: fileID, RelativePath: "src/source.c", SourceSHA256: strings.Repeat("d", 64), Status: coveragedetail.StatusCurrent, Summary: coveragedomain.Summary{Functions: coveragedomain.Metric{Covered: 1, Total: 1}}}}
	b.functions = []coveragedetail.Function{{ID: strings.Repeat("f", 32), Name: "source", Start: coveragedomain.SourceLocation{Line: 1}, End: coveragedomain.SourceLocation{Line: 1}, Status: coveragedetail.StatusCurrent, Summary: coveragedomain.Summary{Functions: coveragedomain.Metric{Covered: 1, Total: 1}}}}
	s := readyDetailSession(t, b)
	report := strings.Repeat("a", 32)
	generation := strings.Repeat("b", 64)
	for _, tc := range []struct {
		method  string
		payload map[string]any
	}{
		{"coverage/details/project/get", map[string]any{"coverageReportId": report, "projectId": "core", "workspaceGeneration": generation}},
		{"coverage/details/files/list", map[string]any{"coverageReportId": report, "projectId": "core", "workspaceGeneration": generation}},
		{"coverage/details/functions/list", map[string]any{"coverageReportId": report, "fileId": fileID, "workspaceGeneration": generation}},
	} {
		got := s.Handle(context.Background(), requestVersion(t, protocol.Version16, tc.method, tc.payload))
		if got.Response.Error == nil || got.Response.Error.Code != "SERVICE_UNHEALTHY" || got.Response.Payload != nil {
			t.Fatalf("%s invented baseline delta: %#v", tc.method, got.Response)
		}
	}
}

func TestCoverageDetailRoutesMapKnownUnusableReportsToClosedErrors(t *testing.T) {
	b := &detailRouteBackend{ready: true}
	s := readyDetailSession(t, b)
	payload := map[string]any{"coverageReportId": strings.Repeat("a", 32), "projectId": "core", "workspaceGeneration": strings.Repeat("b", 64)}
	for _, tc := range []struct {
		status coveragedetail.Status
		reason string
		err    error
		code   string
	}{
		{coveragedetail.StatusStale, "source_changed", nil, "WORKSPACE_CHANGED"},
		{coveragedetail.StatusStale, "source_missing", nil, "WORKSPACE_CHANGED"},
		{coveragedetail.StatusIncomplete, "detail_aggregate_mismatch", nil, "SERVICE_UNHEALTHY"},
		{coveragedetail.StatusCurrent, "", task.ErrNotFound, "COVERAGE_REPORT_NOT_FOUND"},
	} {
		b.status, b.reasons, b.err = tc.status, []string{tc.reason}, tc.err
		if tc.reason == "" {
			b.reasons = nil
		}
		got := s.Handle(context.Background(), requestVersion(t, protocol.Version16, "coverage/details/project/get", payload))
		if got.Response.Error == nil || got.Response.Error.Code != tc.code || got.Response.Payload != nil {
			t.Fatalf("%s/%s mapped to %#v, want %s", tc.status, tc.reason, got.Response, tc.code)
		}
	}
}
func (b *detailRouteBackend) ListCoverageFunctions(context.Context, coveragedetail.FunctionQuery) (coveragedetail.FunctionPage, error) {
	b.calls++
	return coveragedetail.FunctionPage{Items: b.functions}, b.err
}
func (b *detailRouteBackend) ListCoverageLines(context.Context, coveragedetail.LineQuery) (coveragedetail.LinePage, error) {
	b.calls++
	return coveragedetail.LinePage{Items: b.lines}, b.err
}

func readyDetailSession(t *testing.T, b *detailRouteBackend) *session.Session {
	t.Helper()
	s := session.NewWithManagedDetails("0123456789abcdef", "linux", "unix-socket", &fakeBackend{}, &detailCoverageBackend{&coverageBackend{fakeBackend: &fakeBackend{}}}, &generationBackend{ready: true}, b, &managedProvider{true})
	got := s.Handle(context.Background(), requestVersion(t, protocol.Version16, "handshake", map[string]any{"token": "0123456789abcdef", "clientName": "test", "clientVersion": "1.0.0", "supportedProtocolVersions": []string{protocol.Version16}}))
	if got.Response.Error != nil || s.NegotiatedVersion() != protocol.Version16 {
		t.Fatalf("handshake: %#v", got.Response)
	}
	return s
}

func TestCoverageDetailV16RoutesDispatchAndRejectClosedPayloads(t *testing.T) {
	b := &detailRouteBackend{ready: true}
	s := readyDetailSession(t, b)
	report := strings.Repeat("a", 32)
	generation := strings.Repeat("b", 64)
	file := strings.Repeat("c", 32)
	cases := []struct {
		method  string
		payload map[string]any
	}{
		{"coverage/details/project/get", map[string]any{"coverageReportId": report, "projectId": "core", "workspaceGeneration": generation}},
		{"coverage/details/files/list", map[string]any{"coverageReportId": report, "projectId": "core", "workspaceGeneration": generation, "limit": 200}},
		{"coverage/details/functions/list", map[string]any{"coverageReportId": report, "fileId": file, "workspaceGeneration": generation, "limit": 200}},
		{"coverage/details/lines/list", map[string]any{"coverageReportId": report, "fileId": file, "workspaceGeneration": generation, "limit": 1000}},
		{"coverage/details/lines/list", map[string]any{"coverageReportId": report, "functionId": file, "workspaceGeneration": generation, "limit": 1000}},
	}
	for _, tc := range cases {
		got := s.Handle(context.Background(), requestVersion(t, protocol.Version16, tc.method, tc.payload))
		if got.Response.Error != nil {
			t.Fatalf("%s: %#v", tc.method, got.Response)
		}
	}
	if b.calls < 5 {
		t.Fatalf("backend calls=%d", b.calls)
	}
	beforeInvalid := b.calls
	for _, tc := range []struct {
		method  string
		payload map[string]any
	}{
		{cases[0].method, map[string]any{"coverageReportId": report, "projectId": "core", "workspaceGeneration": generation, "source": "secret"}},
		{cases[1].method, map[string]any{"coverageReportId": report, "projectId": "core", "workspaceGeneration": generation, "limit": 201}},
		{cases[1].method, map[string]any{"coverageReportId": report, "projectId": "core", "workspaceGeneration": generation, "limit": 0}},
		{cases[2].method, map[string]any{"coverageReportId": report, "fileId": file, "workspaceGeneration": generation, "limit": 0}},
		{cases[3].method, map[string]any{"coverageReportId": report, "fileId": file, "workspaceGeneration": generation, "limit": 0}},
		{cases[1].method, map[string]any{"coverageReportId": report, "projectId": "core", "workspaceGeneration": generation, "cursor": "not base64!"}},
		{cases[2].method, map[string]any{"coverageReportId": report, "fileId": "bad", "workspaceGeneration": generation}},
		{cases[3].method, map[string]any{"coverageReportId": report, "workspaceGeneration": generation}},
		{cases[3].method, map[string]any{"coverageReportId": report, "workspaceGeneration": generation, "fileId": file, "functionId": file}},
		{cases[0].method, map[string]any{"coverageReportId": report, "projectId": "core", "workspaceGeneration": strings.Repeat("e", 64)}},
	} {
		got := s.Handle(context.Background(), requestVersion(t, protocol.Version16, tc.method, tc.payload))
		if got.Response.Error == nil || got.Response.Error.Code != "INVALID_MESSAGE" {
			t.Fatalf("%s invalid=%#v", tc.method, got.Response)
		}
	}
	if b.calls != beforeInvalid {
		t.Fatalf("invalid payload reached backend: %d", b.calls)
	}
	b.err = errors.New(`native path C:\secret\file.cpp argv --token`)
	got := s.Handle(context.Background(), requestVersion(t, protocol.Version16, cases[0].method, cases[0].payload))
	if got.Response.Error == nil || strings.Contains(got.Response.Error.Message, "secret") {
		t.Fatalf("leaked error: %#v", got.Response)
	}
	b.err = task.ErrInvalidArgument
	got = s.Handle(context.Background(), requestVersion(t, protocol.Version16, cases[1].method, cases[1].payload))
	if got.Response.Error == nil || got.Response.Error.Code != "INVALID_MESSAGE" {
		t.Fatalf("cursor error: %#v", got.Response)
	}
	b.err = nil
	b.lines = []coveragedetail.Line{{Line: 5, Count: 1}}
	got = s.Handle(context.Background(), requestVersion(t, protocol.Version16, cases[3].method, cases[3].payload))
	if got.Response.Error == nil || got.Response.Error.Code != "SERVICE_UNHEALTHY" {
		t.Fatalf("line evidence silently zero-filled: %#v", got.Response)
	}
}
