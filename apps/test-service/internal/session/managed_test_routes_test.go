package session_test

import (
	"context"
	"strings"
	"testing"

	"unit-test-ide.local/test-service/internal/protocol"
	"unit-test-ide.local/test-service/internal/session"
)

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
