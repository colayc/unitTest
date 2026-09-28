package session_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"

	"unit-test-ide.local/test-service/internal/managedtest"
	"unit-test-ide.local/test-service/internal/protocol"
	generationv16 "unit-test-ide.local/test-service/internal/protocolmodel/v1_6/testgeneration"
	"unit-test-ide.local/test-service/internal/session"
	"unit-test-ide.local/test-service/internal/testgendomain"
)

type managedReviewPageBackend struct {
	*managedGenerationBackend
	owner, cursor string
	limit         int
	page          generationv16.ManagedReviewV16
}

type managedStartResolverBackend struct {
	*managedGenerationBackend
	gapID           string
	resolutionCalls int
}

type liveManagedBackend struct {
	*managedStartResolverBackend
	starts, applies int
}

type managedRecordsBackend struct {
	*managedGenerationBackend
	called bool
	page   generationv16.ManagedTestRecordPageV16
}

func (b *managedRecordsBackend) ListManagedTestRecords(_ context.Context, _ string, _ generationv16.ManagedRecordsRequestV16) (generationv16.ManagedTestRecordPageV16, error) {
	b.called = true
	return b.page, nil
}

func TestExplicitManagedRecordsRouteRequiresBoundedProvider(t *testing.T) {
	report, workspace := strings.Repeat("4", 32), strings.Repeat("2", 64)
	backend := &managedRecordsBackend{managedGenerationBackend: &managedGenerationBackend{generationBackend: &generationBackend{ready: true}, ready: true},
		page: generationv16.ManagedTestRecordPageV16{CoverageReportID: report, WorkspaceGeneration: workspace, Items: []generationv16.ManagedTestRecordV16{}}}
	s := session.NewWithManagedDetails("0123456789abcdef", "linux", "unix-socket", &fakeBackend{}, &detailCoverageBackend{&coverageBackend{fakeBackend: &fakeBackend{}}}, backend, &detailRouteBackend{ready: true}, &managedProvider{ready: true})
	s.Handle(context.Background(), requestVersion(t, protocol.Version16, "handshake", map[string]any{"token": "0123456789abcdef", "clientName": "test", "clientVersion": "1.0.0", "supportedProtocolVersions": []string{protocol.Version16}}))
	input := map[string]any{"projectId": "core", "workspaceGeneration": workspace, "coverageReportId": report, "limit": 1}
	got := s.Handle(context.Background(), requestVersion(t, protocol.Version16, "managedTests/records/list", input))
	if got.Response.Error != nil || !backend.called {
		t.Fatalf("records=%#v called=%v", got.Response, backend.called)
	}
	backend.page.WorkspaceGeneration = strings.Repeat("3", 64)
	bad := s.Handle(context.Background(), requestVersion(t, protocol.Version16, "managedTests/records/list", input))
	if bad.Response.Error == nil || bad.Response.Error.Code != "SERVICE_UNHEALTHY" {
		t.Fatalf("unbound page=%#v", bad.Response)
	}
}

func (b *liveManagedBackend) StartManaged(_ context.Context, _ string, _ generationv16.TestGenerationStartRequestV16) (generationv16.TestGenerationRunV16, error) {
	b.starts++
	return generationv16.TestGenerationRunV16{RunID: strings.Repeat("a", 32), TaskID: strings.Repeat("b", 32), ProjectID: "core", WorkspaceGeneration: strings.Repeat("2", 64), State: generationv16.Queued}, nil
}
func (b *liveManagedBackend) ManagedApplyReady() bool { return true }
func (b *liveManagedBackend) ApplyManagedReview(_ context.Context, request managedtest.ApplyRequest) (testgendomain.Run, error) {
	b.applies++
	return testgendomain.Run{State: testgendomain.StateAccepted, Request: testgendomain.Request{SessionOwnerDigest: request.Owner}}, nil
}

func TestExplicitManagedStartAndApplyRoutes(t *testing.T) {
	gapID, reportID, reviewID, reviewDigest := strings.Repeat("3", 32), strings.Repeat("4", 32), strings.Repeat("8", 32), strings.Repeat("9", 64)
	backend := &liveManagedBackend{managedStartResolverBackend: &managedStartResolverBackend{managedGenerationBackend: &managedGenerationBackend{generationBackend: &generationBackend{ready: true}, ready: true}, gapID: gapID}}
	s := session.NewWithManagedDetails("0123456789abcdef", "linux", "unix-socket", &fakeBackend{}, &detailCoverageBackend{&coverageBackend{fakeBackend: &fakeBackend{}}}, backend, &detailRouteBackend{ready: true}, &managedProvider{ready: true})
	s.Handle(context.Background(), requestVersion(t, protocol.Version16, "handshake", map[string]any{"token": "0123456789abcdef", "clientName": "test", "clientVersion": "1.0.0", "supportedProtocolVersions": []string{protocol.Version16}}))
	start := s.Handle(context.Background(), requestVersion(t, protocol.Version16, "testGeneration/start", map[string]any{"idempotencyKey": strings.Repeat("1", 32), "workspaceGeneration": strings.Repeat("2", 64), "projectId": "core", "scope": "coverage-gap", "coverageGapId": gapID, "coverageReportId": reportID, "framework": "auto", "goals": map[string]any{"functionPercent": 70, "linePercent": 80, "branchPercent": 60}, "budgets": map[string]any{"wallTimeMs": 60000, "candidateCount": 2, "memoryMiB": 64, "concurrency": 1}}))
	if start.Response.Error != nil || backend.starts != 1 || backend.resolutionCalls != 1 {
		t.Fatalf("start=%#v calls=%d/%d", start.Response, backend.starts, backend.resolutionCalls)
	}
	apply := s.Handle(context.Background(), requestVersion(t, protocol.Version16, "managedTests/reviews/apply", map[string]any{"reviewId": reviewID, "reviewDigest": reviewDigest, "resolutions": []any{}}))
	if apply.Response.Error != nil || backend.applies != 1 {
		t.Fatalf("apply=%#v calls=%d", apply.Response, backend.applies)
	}
}

func (b *managedStartResolverBackend) ResolveManagedStart(_ context.Context, _ string, input generationv16.TestGenerationStartRequestV16) (testgendomain.ManagedTarget, error) {
	b.resolutionCalls++
	if input.CoverageGapID == nil || *input.CoverageGapID != b.gapID {
		return testgendomain.ManagedTarget{}, testgendomain.ErrStaleSnapshot
	}
	return testgendomain.ManagedTarget{GapID: b.gapID}, nil
}

func TestManagedStartResolvesExactIDBeforeAnyGenerationWork(t *testing.T) {
	gapID := strings.Repeat("3", 32)
	reportID := strings.Repeat("4", 32)
	base := map[string]any{"idempotencyKey": strings.Repeat("1", 32), "workspaceGeneration": strings.Repeat("2", 64), "projectId": "core", "scope": "coverage-gap", "coverageGapId": gapID, "coverageReportId": reportID, "framework": "auto", "goals": map[string]any{"functionPercent": 70, "linePercent": 80, "branchPercent": 60}, "budgets": map[string]any{"wallTimeMs": 60000, "candidateCount": 2, "memoryMiB": 64, "concurrency": 1}}
	backend := &managedStartResolverBackend{managedGenerationBackend: &managedGenerationBackend{generationBackend: &generationBackend{ready: true}, ready: true}, gapID: gapID}
	s := session.NewWithManagedDetails("0123456789abcdef", "linux", "unix-socket", &fakeBackend{}, &detailCoverageBackend{&coverageBackend{fakeBackend: &fakeBackend{}}}, backend, &detailRouteBackend{ready: true}, &managedProvider{ready: true})
	s.Handle(context.Background(), requestVersion(t, protocol.Version16, "handshake", map[string]any{"token": "0123456789abcdef", "clientName": "test", "clientVersion": "1.0.0", "supportedProtocolVersions": []string{protocol.Version16}}))
	bad := make(map[string]any, len(base))
	for k, v := range base {
		bad[k] = v
	}
	bad["coverageGapId"] = strings.Repeat("5", 32)
	got := s.Handle(context.Background(), requestVersion(t, protocol.Version16, "testGeneration/start", bad))
	if got.Response.Error == nil || got.Response.Error.Code != "WORKSPACE_CHANGED" || backend.resolutionCalls != 1 || len(backend.calls) != 0 {
		t.Fatalf("unknown ID reached generation: %#v calls=%d legacy=%v", got.Response, backend.resolutionCalls, backend.calls)
	}
	got = s.Handle(context.Background(), requestVersion(t, protocol.Version16, "testGeneration/start", base))
	if got.Response.Error == nil || got.Response.Error.Code != "PROTOCOL_FEATURE_UNAVAILABLE" || backend.resolutionCalls != 2 || len(backend.calls) != 0 {
		t.Fatalf("resolved ID reached unimplemented generation: %#v calls=%d legacy=%v", got.Response, backend.resolutionCalls, backend.calls)
	}
}

func (b *managedReviewPageBackend) GetManagedReviewPage(_ context.Context, owner, _ string, cursor string, limit int) (generationv16.ManagedReviewV16, error) {
	b.owner, b.cursor, b.limit = owner, cursor, limit
	return b.page, nil
}

func TestManagedReviewRouteBindsOwnerAndBoundsPage(t *testing.T) {
	reviewID := strings.Repeat("8", 32)
	reportID := strings.Repeat("a", 32)
	workspace := strings.Repeat("b", 64)
	backend := &managedReviewPageBackend{managedGenerationBackend: &managedGenerationBackend{generationBackend: &generationBackend{ready: true}, ready: true},
		page: generationv16.ManagedReviewV16{ReviewID: reviewID, ReviewDigest: strings.Repeat("c", 64), CoverageReportID: reportID, WorkspaceGeneration: workspace, Cases: []generationv16.ManagedReviewCaseV16{}}}
	s := session.NewWithManagedDetails("0123456789abcdef", "linux", "unix-socket", &fakeBackend{}, &detailCoverageBackend{&coverageBackend{fakeBackend: &fakeBackend{}}}, backend, &detailRouteBackend{ready: true}, &managedProvider{ready: true})
	s.Handle(context.Background(), requestVersion(t, protocol.Version16, "handshake", map[string]any{"token": "0123456789abcdef", "clientName": "test", "clientVersion": "1.0.0", "supportedProtocolVersions": []string{protocol.Version16}}))
	got := s.Handle(context.Background(), requestVersion(t, protocol.Version16, "managedTests/reviews/get", map[string]any{"reviewId": reviewID, "limit": 2}))
	wantOwner := sha256.Sum256([]byte("0123456789abcdef"))
	if got.Response.Error != nil || backend.owner != hex.EncodeToString(wantOwner[:]) || backend.limit != 2 {
		t.Fatalf("read=%#v owner=%q limit=%d", got.Response, backend.owner, backend.limit)
	}
	for _, payload := range []map[string]any{{"reviewId": reviewID, "limit": 33}, {"reviewId": reviewID, "cursor": ""}, {"reviewId": reviewID, "ownerDigest": backend.owner}} {
		bad := s.Handle(context.Background(), requestVersion(t, protocol.Version16, "managedTests/reviews/get", payload))
		if bad.Response.Error == nil || bad.Response.Error.Code != "INVALID_MESSAGE" {
			t.Fatalf("invalid payload accepted: %#v", bad.Response)
		}
	}
	backend.page.Cases = make([]generationv16.ManagedReviewCaseV16, 33)
	bad := s.Handle(context.Background(), requestVersion(t, protocol.Version16, "managedTests/reviews/get", map[string]any{"reviewId": reviewID, "limit": 32}))
	if bad.Response.Error == nil || bad.Response.Error.Code != "SERVICE_UNHEALTHY" {
		t.Fatalf("oversized provider page accepted: %#v", bad.Response)
	}
	apply := s.Handle(context.Background(), requestVersion(t, protocol.Version16, "managedTests/reviews/apply", map[string]any{}))
	if apply.Response.Error == nil || apply.Response.Error.Code != "PROTOCOL_FEATURE_UNAVAILABLE" {
		t.Fatalf("apply enabled: %#v", apply.Response)
	}
}

func TestManagedRoutesRemainUnavailableWithoutManagedBackend(t *testing.T) {
	s := session.NewWithManagedDetails("0123456789abcdef", "linux", "unix-socket", &fakeBackend{},
		&detailCoverageBackend{&coverageBackend{fakeBackend: &fakeBackend{}}},
		&managedGenerationBackend{generationBackend: &generationBackend{ready: true}, ready: true}, &detailRouteBackend{ready: true}, &managedProvider{ready: true})
	negotiated := s.Handle(context.Background(), requestVersion(t, protocol.Version16, "handshake", map[string]any{
		"token": "0123456789abcdef", "clientName": "test", "clientVersion": "1.0.0",
		"supportedProtocolVersions": []string{protocol.Version16},
	}))
	if negotiated.Response.Error != nil {
		t.Fatalf("handshake: %#v", negotiated.Response)
	}
	for _, method := range []string{"managedTests/records/list", "managedTests/reviews/get", "managedTests/reviews/apply"} {
		got := s.Handle(context.Background(), requestVersion(t, protocol.Version16, method, map[string]any{}))
		if got.Response.Error == nil || got.Response.Error.Code != "PROTOCOL_FEATURE_UNAVAILABLE" {
			t.Fatalf("%s must fail closed: %#v", method, got.Response)
		}
	}
}

func TestV16StartRejectsCallerSymbolPathAndGapCoordinates(t *testing.T) {
	s := session.NewWithManagedDetails("0123456789abcdef", "linux", "unix-socket", &fakeBackend{},
		&detailCoverageBackend{&coverageBackend{fakeBackend: &fakeBackend{}}},
		&managedGenerationBackend{generationBackend: &generationBackend{ready: true}, ready: true}, &detailRouteBackend{ready: true}, &managedProvider{ready: true})
	s.Handle(context.Background(), requestVersion(t, protocol.Version16, "handshake", map[string]any{
		"token": "0123456789abcdef", "clientName": "test", "clientVersion": "1.0.0", "supportedProtocolVersions": []string{protocol.Version16},
	}))
	base := map[string]any{
		"idempotencyKey": strings.Repeat("1", 32), "workspaceGeneration": strings.Repeat("2", 64),
		"projectId": "core", "scope": "symbol", "functionId": strings.Repeat("3", 32),
		"framework": "auto",
		"goals":     map[string]any{"functionPercent": 70, "linePercent": 80, "branchPercent": 60},
		"budgets":   map[string]any{"wallTimeMs": 60000, "candidateCount": 2, "memoryMiB": 64, "concurrency": 1},
	}
	for _, extra := range []string{"symbolId", "file", "line", "column", "gapKind", "sourcePath"} {
		payload := make(map[string]any, len(base)+1)
		for k, v := range base {
			payload[k] = v
		}
		payload[extra] = "fn:forged"
		got := s.Handle(context.Background(), requestVersion(t, protocol.Version16, "testGeneration/start", payload))
		if got.Response.Error == nil || got.Response.Error.Code != "INVALID_MESSAGE" {
			t.Fatalf("caller %s accepted: %#v", extra, got.Response)
		}
	}
	for _, mutation := range []struct {
		name   string
		change func(map[string]any)
	}{
		{"wrong function ID", func(p map[string]any) { p["functionId"] = "fn:forged" }},
		{"mixed selectors", func(p map[string]any) { p["fileId"] = strings.Repeat("5", 32) }},
		{"extra report", func(p map[string]any) { p["coverageReportId"] = strings.Repeat("4", 32) }},
	} {
		payload := make(map[string]any, len(base))
		for k, v := range base {
			payload[k] = v
		}
		mutation.change(payload)
		got := s.Handle(context.Background(), requestVersion(t, protocol.Version16, "testGeneration/start", payload))
		if got.Response.Error == nil || got.Response.Error.Code != "INVALID_MESSAGE" {
			t.Fatalf("%s accepted: %#v", mutation.name, got.Response)
		}
	}
	got := s.Handle(context.Background(), requestVersion(t, protocol.Version16, "testGeneration/start", base))
	if got.Response.Error == nil || got.Response.Error.Code != "PROTOCOL_FEATURE_UNAVAILABLE" {
		t.Fatalf("unwired current index reached generation: %#v", got.Response)
	}
}
