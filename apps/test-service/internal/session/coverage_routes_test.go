package session_test

import (
	"context"
	"errors"
	"testing"

	"unit-test-ide.local/test-service/internal/coveragedomain"
	"unit-test-ide.local/test-service/internal/protocol"
	capabilitiesv14 "unit-test-ide.local/test-service/internal/protocolmodel/v1_4/capabilities"
	"unit-test-ide.local/test-service/internal/session"
	"unit-test-ide.local/test-service/internal/task"
	"unit-test-ide.local/test-service/internal/testdomain"
)

func TestV14CoverageNegotiationAndCapabilitiesRequireCoverageBackend(t *testing.T) {
	backend := &coverageBackend{fakeBackend: &fakeBackend{}}
	active := session.New("0123456789abcdef", "linux", "unix-socket", backend)
	handshake := active.Handle(context.Background(), requestVersion(t, protocol.Version14, "handshake", map[string]any{
		"token": "0123456789abcdef", "clientName": "test", "clientVersion": "0.5.0",
		"supportedProtocolVersions": []string{protocol.Version13, protocol.Version14},
	}))
	if handshake.Response.Kind != "response" || active.NegotiatedVersion() != protocol.Version14 {
		t.Fatalf("handshake = %#v, negotiated=%q", handshake.Response, active.NegotiatedVersion())
	}
	capabilities := active.Handle(context.Background(), requestVersion(t, protocol.Version14, "capabilities/get", map[string]any{}))
	value, ok := capabilities.Response.Payload.(capabilitiesv14.CapabilitiesV14)
	if capabilities.Response.Kind != "response" || !ok || !value.CoverageRun || !value.CoverageReport {
		t.Fatalf("capabilities = %#v", capabilities.Response)
	}
}

func TestV14CoverageRoutesRejectUnsafePayloadBeforeBackend(t *testing.T) {
	backend := &coverageBackend{fakeBackend: &fakeBackend{}}
	active := session.New("0123456789abcdef", "linux", "unix-socket", backend)
	if result := active.Handle(context.Background(), requestVersion(t, protocol.Version14, "handshake", map[string]any{
		"token": "0123456789abcdef", "clientName": "test", "clientVersion": "0.5.0",
		"supportedProtocolVersions": []string{protocol.Version14},
	})); result.Response.Kind != "response" {
		t.Fatalf("handshake = %#v", result.Response)
	}
	for _, input := range []struct {
		method  string
		payload map[string]any
	}{
		{method: "coverage/runs/start", payload: map[string]any{
			"idempotencyKey": "bad", "workspaceGeneration": "bad", "projectId": "core",
			"coverageProfileId": "coverage-debug", "catalogRevision": "bad", "selection": map[string]any{"mode": "all"}, "repeatCount": 1, "timeoutMs": 1,
		}},
		{method: "coverage/runs/get", payload: map[string]any{"coverageRunId": "bad"}},
		{method: "coverage/runs/list", payload: map[string]any{"limit": 201}},
		{method: "coverage/reports/get", payload: map[string]any{"reportId": "bad"}},
	} {
		result := active.Handle(context.Background(), requestVersion(t, protocol.Version14, input.method, input.payload))
		if result.Response.Error == nil || result.Response.Error.Code != "INVALID_MESSAGE" {
			t.Fatalf("%s response = %#v", input.method, result.Response)
		}
	}
	if backend.calls != 0 {
		t.Fatalf("backend calls = %d, want 0", backend.calls)
	}
}

type coverageBackend struct {
	*fakeBackend
	calls int
}

func (*coverageBackend) StartTestDiscovery(context.Context, session.TestDiscoveryStart) (task.Task, error) {
	return task.Task{}, nil
}
func (*coverageBackend) StartTestRun(context.Context, session.TestRunStart) (task.Task, testdomain.TestRun, error) {
	return task.Task{}, testdomain.TestRun{}, nil
}
func (*coverageBackend) GetTestCatalog(context.Context, testdomain.CatalogPageRequest) (testdomain.CatalogPage, error) {
	return testdomain.CatalogPage{}, nil
}
func (*coverageBackend) GetTestRun(context.Context, string) (testdomain.TestRun, error) {
	return testdomain.TestRun{}, nil
}
func (*coverageBackend) GetTestRunForTask(context.Context, string) (testdomain.TestRun, error) {
	return testdomain.TestRun{}, nil
}
func (*coverageBackend) ListTestRuns(context.Context, testdomain.RunPageRequest) (testdomain.RunPage, error) {
	return testdomain.RunPage{}, nil
}
func (backend *coverageBackend) StartCoverageRun(context.Context, session.CoverageRunStart) (task.Task, coveragedomain.Run, testdomain.TestRun, error) {
	backend.calls++
	return task.Task{}, coveragedomain.Run{}, testdomain.TestRun{}, errors.New("unexpected coverage call")
}
func (backend *coverageBackend) GetCoverageRun(context.Context, string) (coveragedomain.Run, error) {
	backend.calls++
	return coveragedomain.Run{}, errors.New("unexpected coverage call")
}
func (backend *coverageBackend) ListCoverageRuns(context.Context, coveragedomain.RunPageRequest) (coveragedomain.RunPage, error) {
	backend.calls++
	return coveragedomain.RunPage{}, errors.New("unexpected coverage call")
}
func (backend *coverageBackend) GetCoverageReport(context.Context, string) (coveragedomain.Report, error) {
	backend.calls++
	return coveragedomain.Report{}, errors.New("unexpected coverage call")
}
