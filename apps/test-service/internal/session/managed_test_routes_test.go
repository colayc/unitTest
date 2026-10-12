package session_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
	"time"

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

type managedRunReadBackend struct {
	*liveManagedBackend
	owner  string
	getRun generationv16.TestGenerationRunV16
	page   generationv16.TestGenerationCandidatePageV16
}

func (b *managedRunReadBackend) ListManagedTargets(_ context.Context, owner string, _ generationv16.TestGenerationTargetListRequestV16) (generationv16.TestGenerationTargetListV16, error) {
	b.owner = owner
	return generationv16.TestGenerationTargetListV16{Items: []generationv16.TestGenerationTargetV16{}}, nil
}
func (b *managedRunReadBackend) GetManagedRun(_ context.Context, owner, _ string) (generationv16.TestGenerationRunV16, error) {
	b.owner = owner
	return b.getRun, nil
}
func (b *managedRunReadBackend) CancelManagedRun(_ context.Context, owner, _ string) (generationv16.TestGenerationRunV16, error) {
	b.owner = owner
	return b.getRun, nil
}
func (b *managedRunReadBackend) ReplayManagedEvents(_ context.Context, owner string, _ generationv16.TestGenerationEventReplayRequestV16) (generationv16.TestGenerationEventPageV16, error) {
	b.owner = owner
	return generationv16.TestGenerationEventPageV16{Items: []generationv16.TestGenerationProgressEventV16{}, NextAfterSequence: 0}, nil
}
func (b *managedRunReadBackend) ListManagedCandidates(_ context.Context, owner string, _ generationv16.TestGenerationCandidateListRequestV16) (generationv16.TestGenerationCandidatePageV16, error) {
	b.owner = owner
	return b.page, nil
}

func TestReadyManagedRunRoutesReadImmediatelyAfterStart(t *testing.T) {
	backend := &managedRunReadBackend{liveManagedBackend: &liveManagedBackend{managedStartResolverBackend: &managedStartResolverBackend{managedGenerationBackend: &managedGenerationBackend{generationBackend: &generationBackend{ready: true}, ready: true}, gapID: strings.Repeat("3", 32)}},
		getRun: generationv16.TestGenerationRunV16{RunID: strings.Repeat("a", 32), TaskID: strings.Repeat("b", 32), ProjectID: "core", WorkspaceGeneration: strings.Repeat("2", 64), State: generationv16.Queued, CreatedAt: time.Date(2026, 9, 28, 0, 0, 0, 0, time.UTC)},
		page:   generationv16.TestGenerationCandidatePageV16{Items: []generationv16.TestGenerationCandidateV16{}}}
	s := session.NewWithManagedDetails("0123456789abcdef", "linux", "unix-socket", &fakeBackend{}, &detailCoverageBackend{&coverageBackend{fakeBackend: &fakeBackend{}}}, backend, &detailRouteBackend{ready: true}, &managedProvider{ready: true})
	s.Handle(context.Background(), requestVersion(t, protocol.Version16, "handshake", map[string]any{"token": "0123456789abcdef", "clientName": "test", "clientVersion": "1.0.0", "supportedProtocolVersions": []string{protocol.Version16}}))
	inputs := []struct {
		method  string
		payload map[string]any
	}{
		{"testGeneration/targets/list", map[string]any{"projectId": "core", "workspaceGeneration": strings.Repeat("2", 64)}},
		{"testGeneration/runs/get", map[string]any{"runId": strings.Repeat("a", 32)}},
		{"testGeneration/runs/cancel", map[string]any{"runId": strings.Repeat("a", 32)}},
		{"testGeneration/events/replay", map[string]any{"runId": strings.Repeat("a", 32), "afterSequence": 0}},
		{"testGeneration/candidates/list", map[string]any{"runId": strings.Repeat("a", 32)}},
	}
	for _, input := range inputs {
		result := s.Handle(context.Background(), requestVersion(t, protocol.Version16, input.method, input.payload))
		if result.Response.Error != nil {
			t.Errorf("%s: %#v", input.method, result.Response.Error)
		}
	}
	if backend.owner == "" {
		t.Fatal("authenticated owner was not forwarded")
	}
}

func TestManagedRunRoutesRejectInvalidProviderPayloadsAndUnsafeInputs(t *testing.T) {
	backend := &managedRunReadBackend{liveManagedBackend: &liveManagedBackend{managedStartResolverBackend: &managedStartResolverBackend{managedGenerationBackend: &managedGenerationBackend{generationBackend: &generationBackend{ready: true}, ready: true}}},
		getRun: generationv16.TestGenerationRunV16{RunID: strings.Repeat("f", 32), TaskID: strings.Repeat("b", 32), ProjectID: "core", WorkspaceGeneration: strings.Repeat("2", 64), State: generationv16.Queued, CreatedAt: time.Date(2026, 9, 28, 0, 0, 0, 0, time.UTC)},
		page:   generationv16.TestGenerationCandidatePageV16{Items: []generationv16.TestGenerationCandidateV16{}}}
	s := session.NewWithManagedDetails("0123456789abcdef", "linux", "unix-socket", &fakeBackend{}, &detailCoverageBackend{&coverageBackend{fakeBackend: &fakeBackend{}}}, backend, &detailRouteBackend{ready: true}, &managedProvider{ready: true})
	s.Handle(context.Background(), requestVersion(t, protocol.Version16, "handshake", map[string]any{"token": "0123456789abcdef", "clientName": "test", "clientVersion": "1.0.0", "supportedProtocolVersions": []string{protocol.Version16}}))
	runID := strings.Repeat("a", 32)
	got := s.Handle(context.Background(), requestVersion(t, protocol.Version16, "testGeneration/runs/get", map[string]any{"runId": runID}))
	if got.Response.Error == nil || got.Response.Error.Code != "SERVICE_UNHEALTHY" {
		t.Fatalf("wrong run identity=%#v", got.Response)
	}
	backend.getRun.RunID = runID
	backend.getRun.State = "unknown"
	got = s.Handle(context.Background(), requestVersion(t, protocol.Version16, "testGeneration/runs/get", map[string]any{"runId": runID}))
	if got.Response.Error == nil || got.Response.Error.Code != "SERVICE_UNHEALTHY" {
		t.Fatalf("unknown run state=%#v", got.Response)
	}
	previewText := "wrong text"
	backend.getRun.State = generationv16.AwaitingConfirmation
	backend.getRun.Preview = &generationv16.TestGenerationPreviewV16{CandidateSetDigest: strings.Repeat("1", 64), DiffDigest: strings.Repeat("2", 64), ConfirmationDigest: strings.Repeat("3", 64), Diff: &previewText}
	got = s.Handle(context.Background(), requestVersion(t, protocol.Version16, "testGeneration/runs/get", map[string]any{"runId": runID}))
	if got.Response.Error == nil || got.Response.Error.Code != "SERVICE_UNHEALTHY" {
		t.Fatalf("unverified preview=%#v", got.Response)
	}
	for _, input := range []struct {
		method  string
		payload map[string]any
	}{
		{"testGeneration/events/replay", map[string]any{"runId": runID, "afterSequence": 9007199254740992}},
		{"testGeneration/candidates/list", map[string]any{"runId": runID, "limit": 201}},
		{"testGeneration/targets/list", map[string]any{"projectId": "core", "workspaceGeneration": strings.Repeat("2", 64), "cursor": ""}},
	} {
		got = s.Handle(context.Background(), requestVersion(t, protocol.Version16, input.method, input.payload))
		if got.Response.Error == nil || got.Response.Error.Code != "INVALID_MESSAGE" {
			t.Errorf("%s unsafe request=%#v", input.method, got.Response)
		}
	}
	backend.page.Items = []generationv16.TestGenerationCandidateV16{{CandidateID: strings.Repeat("c", 32), Kind: "unknown"}}
	got = s.Handle(context.Background(), requestVersion(t, protocol.Version16, "testGeneration/candidates/list", map[string]any{"runId": runID}))
	if got.Response.Error == nil || got.Response.Error.Code != "SERVICE_UNHEALTHY" {
		t.Fatalf("unknown candidate kind=%#v", got.Response)
	}
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
	return generationv16.TestGenerationRunV16{RunID: strings.Repeat("a", 32), TaskID: strings.Repeat("b", 32), ProjectID: "core", WorkspaceGeneration: strings.Repeat("2", 64), State: generationv16.Queued, CreatedAt: time.Date(2026, 9, 28, 0, 0, 0, 0, time.UTC)}, nil
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
	if got.Response.Error == nil || backend.resolutionCalls != 2 || len(backend.calls) != 0 {
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
		"projectId": "core", "scope": "symbol", "functionId": strings.Repeat("3", 32), "coverageReportId": strings.Repeat("4", 32),
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
		{"missing report", func(p map[string]any) { delete(p, "coverageReportId") }},
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
	if got.Response.Error == nil {
		t.Fatalf("unwired current index reached generation: %#v", got.Response)
	}
}
