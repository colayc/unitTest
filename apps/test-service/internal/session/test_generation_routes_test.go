package session_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"unit-test-ide.local/test-service/internal/build"
	"unit-test-ide.local/test-service/internal/protocol"
	capabilitiesv15 "unit-test-ide.local/test-service/internal/protocolmodel/v1_5/capabilities"
	generationv15 "unit-test-ide.local/test-service/internal/protocolmodel/v1_5/testgeneration"
	"unit-test-ide.local/test-service/internal/session"
	"unit-test-ide.local/test-service/internal/task"
	"unit-test-ide.local/test-service/internal/testgenpublish"
)

type generationBackend struct {
	ready bool
	calls []string
	err   error
	owner string
}

func (b *generationBackend) TestGenerationReady() bool { return b.ready }
func (b *generationBackend) ListTestGenerationTargets(_ context.Context, owner string, _ generationv15.TestGenerationTargetListRequestV15) (generationv15.TestGenerationTargetListV15, error) {
	b.calls = append(b.calls, "targets")
	b.owner = owner
	return generationv15.TestGenerationTargetListV15{Items: []generationv15.TestGenerationTargetV15{}}, b.err
}
func (b *generationBackend) StartTestGeneration(_ context.Context, owner string, _ generationv15.TestGenerationStartRequestV15) (generationv15.TestGenerationRunV15, error) {
	b.calls = append(b.calls, "start")
	b.owner = owner
	return generationRun(), b.err
}
func (b *generationBackend) GetTestGenerationRun(_ context.Context, owner, _ string) (generationv15.TestGenerationRunV15, error) {
	b.calls = append(b.calls, "get")
	b.owner = owner
	return generationRun(), b.err
}
func (b *generationBackend) ListTestGenerationCandidates(_ context.Context, owner string, _ generationv15.TestGenerationCandidateListRequestV15) (generationv15.TestGenerationCandidatePageV15, error) {
	b.calls = append(b.calls, "candidates")
	b.owner = owner
	return generationv15.TestGenerationCandidatePageV15{Items: []generationv15.TestGenerationCandidateV15{}}, b.err
}
func (b *generationBackend) AcceptTestGeneration(_ context.Context, owner string, _ generationv15.TestGenerationAcceptRequestV15) (generationv15.TestGenerationRunV15, error) {
	b.calls = append(b.calls, "accept")
	b.owner = owner
	return generationRun(), b.err
}

func generationRun() generationv15.TestGenerationRunV15 {
	return generationv15.TestGenerationRunV15{
		RunID: strings.Repeat("a", 32), TaskID: strings.Repeat("b", 32), ProjectID: "core",
		WorkspaceGeneration: strings.Repeat("c", 64), State: generationv15.Queued,
		CreatedAt: time.Date(2026, 9, 27, 0, 0, 0, 0, time.UTC), LastSequence: 1,
	}
}

func TestV15GenerationRoutesRequireReadyBackendAndClosedPayloads(t *testing.T) {
	legacy := session.NewWithCoverage("0123456789abcdef", "linux", "unix-socket", &fakeBackend{}, &coverageBackend{fakeBackend: &fakeBackend{}})
	result := legacy.Handle(context.Background(), requestVersion(t, protocol.Version15, "handshake", map[string]any{
		"token": "0123456789abcdef", "clientName": "test", "clientVersion": "0.6.0",
		"supportedProtocolVersions": []string{protocol.Version14, protocol.Version15},
	}))
	if result.Response.Error != nil || legacy.NegotiatedVersion() != protocol.Version14 {
		t.Fatalf("unready negotiation = %#v, %q", result.Response, legacy.NegotiatedVersion())
	}
	result = legacy.Handle(context.Background(), requestVersion(t, protocol.Version14, "testGeneration/accept", map[string]any{}))
	if result.Response.Error == nil || result.Response.Error.Code != "PROTOCOL_FEATURE_UNAVAILABLE" {
		t.Fatalf("legacy accept = %#v", result.Response)
	}

	backend := &generationBackend{ready: true}
	active := session.NewWithGeneration("0123456789abcdef", "linux", "unix-socket", &fakeBackend{}, &coverageBackend{fakeBackend: &fakeBackend{}}, backend)
	result = active.Handle(context.Background(), requestVersion(t, protocol.Version15, "handshake", map[string]any{
		"token": "0123456789abcdef", "clientName": "test", "clientVersion": "0.6.0",
		"supportedProtocolVersions": []string{protocol.Version15},
	}))
	if result.Response.Error != nil || active.NegotiatedVersion() != protocol.Version15 {
		t.Fatalf("v1.5 negotiation = %#v, %q", result.Response, active.NegotiatedVersion())
	}
	capability := active.Handle(context.Background(), requestVersion(t, protocol.Version15, "capabilities/get", map[string]any{}))
	value, ok := capability.Response.Payload.(capabilitiesv15.CapabilitiesV15)
	if !ok || !value.TestGeneration {
		t.Fatalf("v1.5 capability = %#v", capability.Response)
	}
	workspace := active.Handle(context.Background(), requestVersion(t, protocol.Version15, "workspace/inspect", map[string]any{}))
	if workspace.Response.Error != nil && workspace.Response.Error.Code == "PROTOCOL_FEATURE_UNAVAILABLE" {
		t.Fatalf("v1.5 must retain workspace inspection routing: %#v", workspace.Response)
	}
	for _, input := range []struct {
		method  string
		payload map[string]any
	}{
		{"testGeneration/targets/list", map[string]any{"workspaceGeneration": strings.Repeat("c", 64), "projectId": "core"}},
		{"testGeneration/start", map[string]any{"idempotencyKey": strings.Repeat("d", 32), "workspaceGeneration": strings.Repeat("c", 64), "projectId": "core", "scope": "workspace", "framework": "auto", "goals": map[string]any{"functionPercent": 70, "linePercent": 80, "branchPercent": 60}, "budgets": map[string]any{"wallTimeMs": 60000, "candidateCount": 2, "memoryMiB": 64, "concurrency": 1}}},
		{"testGeneration/runs/get", map[string]any{"runId": strings.Repeat("a", 32)}},
		{"testGeneration/candidates/list", map[string]any{"runId": strings.Repeat("a", 32)}},
		{"testGeneration/accept", map[string]any{"runId": strings.Repeat("a", 32), "candidateId": strings.Repeat("e", 32), "confirmationDigest": strings.Repeat("f", 64), "confirmCharacterization": false}},
	} {
		result := active.Handle(context.Background(), requestVersion(t, protocol.Version15, input.method, input.payload))
		if result.Response.Error != nil {
			t.Fatalf("%s = %#v", input.method, result.Response)
		}
	}
	if strings.Join(backend.calls, ",") != "targets,start,get,candidates,accept" || len(backend.owner) != 64 {
		t.Fatalf("backend calls = %#v, owner=%q", backend.calls, backend.owner)
	}
	bad := active.Handle(context.Background(), requestVersion(t, protocol.Version15, "testGeneration/accept", map[string]any{"runId": strings.Repeat("a", 32), "candidateId": strings.Repeat("e", 32), "confirmCharacterization": false}))
	if bad.Response.Error == nil || bad.Response.Error.Code != "INVALID_MESSAGE" || len(backend.calls) != 5 {
		t.Fatalf("missing confirmation digest = %#v, calls=%#v", bad.Response, backend.calls)
	}
	backend.err = errors.New("C:\\private\\secret.cpp token=private")
	failed := active.Handle(context.Background(), requestVersion(t, protocol.Version15, "testGeneration/runs/get", map[string]any{"runId": strings.Repeat("a", 32)}))
	if failed.Response.Error == nil || strings.Contains(failed.Response.Error.Message, "secret") || strings.Contains(failed.Response.Error.Message, "private") {
		t.Fatalf("unredacted error = %#v", failed.Response)
	}
	for _, tc := range []struct {
		err  error
		code string
	}{
		{build.ErrWorkspaceTrustRequired, "WORKSPACE_TRUST_REQUIRED"},
		{task.ErrNotFound, "TASK_NOT_FOUND"},
		{testgenpublish.ErrConflict, "WORKSPACE_CHANGED"},
		{session.ErrCharacterizationConfirmationRequired, "INVALID_TASK_SPEC"},
	} {
		backend.err = tc.err
		response := active.Handle(context.Background(), requestVersion(t, protocol.Version15, "testGeneration/runs/get", map[string]any{"runId": strings.Repeat("a", 32)}))
		if response.Response.Error == nil || response.Response.Error.Code != tc.code {
			t.Fatalf("error %v projected as %#v", tc.err, response.Response)
		}
	}
}
