package session_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"unit-test-ide.local/test-service/internal/coveragedomain"
	"unit-test-ide.local/test-service/internal/discovery"
	"unit-test-ide.local/test-service/internal/protocol"
	capabilitiesv14 "unit-test-ide.local/test-service/internal/protocolmodel/v1_4/capabilities"
	taskv14 "unit-test-ide.local/test-service/internal/protocolmodel/v1_4/task"
	"unit-test-ide.local/test-service/internal/session"
	"unit-test-ide.local/test-service/internal/task"
	"unit-test-ide.local/test-service/internal/testdomain"
)

func TestV14CoverageNegotiationAndCapabilitiesRequireCoverageBackend(t *testing.T) {
	backend := &coverageBackend{fakeBackend: &fakeBackend{}}
	active := session.NewWithCoverage("0123456789abcdef", "linux", "unix-socket", backend, backend)
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

func TestV14CoverageSessionKeepsWorkspaceMethodsAvailable(t *testing.T) {
	backend := &coverageBackend{fakeBackend: &fakeBackend{}}
	active := session.NewWithCoverage("0123456789abcdef", "linux", "unix-socket", backend, backend)
	if result := active.Handle(context.Background(), requestVersion(t, protocol.Version14, "handshake", map[string]any{
		"token": "0123456789abcdef", "clientName": "test", "clientVersion": "0.5.0",
		"supportedProtocolVersions": []string{protocol.Version14},
	})); result.Response.Kind != "response" {
		t.Fatalf("handshake = %#v", result.Response)
	}
	result := active.Handle(context.Background(), requestVersion(t, protocol.Version14, "workspace/inspect", map[string]any{}))
	if result.Response.Kind != "response" || result.Response.Error != nil {
		t.Fatalf("workspace/inspect = %#v", result.Response)
	}
}

func TestV14CoverageNegotiationRequiresExplicitCoverageProvider(t *testing.T) {
	backend := &coverageBackend{fakeBackend: &fakeBackend{}}
	active := session.New("0123456789abcdef", "linux", "unix-socket", backend)
	handshake := active.Handle(context.Background(), requestVersion(t, protocol.Version14, "handshake", map[string]any{
		"token": "0123456789abcdef", "clientName": "test", "clientVersion": "0.5.0",
		"supportedProtocolVersions": []string{protocol.Version13, protocol.Version14},
	}))
	if handshake.Response.Kind != "response" || active.NegotiatedVersion() != protocol.Version13 {
		t.Fatalf("handshake = %#v, negotiated=%q; coverage must remain gated", handshake.Response, active.NegotiatedVersion())
	}
	capabilities := active.Handle(context.Background(), requestVersion(t, protocol.Version13, "capabilities/get", map[string]any{}))
	if capabilities.Response.Kind != "response" || capabilities.Response.ProtocolVersion != protocol.Version13 {
		t.Fatalf("capabilities = %#v", capabilities.Response)
	}
}

func TestV14CoverageRoutesRejectUnsafePayloadBeforeBackend(t *testing.T) {
	backend := &coverageBackend{fakeBackend: &fakeBackend{}}
	active := session.NewWithCoverage("0123456789abcdef", "linux", "unix-socket", backend, backend)
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

func TestV14CoverageStartAcceptsMillisecondTimeout(t *testing.T) {
	backend := &coverageBackend{fakeBackend: &fakeBackend{}}
	active := session.NewWithCoverage("0123456789abcdef", "linux", "unix-socket", backend, backend)
	if result := active.Handle(context.Background(), requestVersion(t, protocol.Version14, "handshake", map[string]any{
		"token": "0123456789abcdef", "clientName": "test", "clientVersion": "0.5.0",
		"supportedProtocolVersions": []string{protocol.Version14},
	})); result.Response.Kind != "response" {
		t.Fatalf("handshake = %#v", result.Response)
	}
	result := active.Handle(context.Background(), requestVersion(t, protocol.Version14, "coverage/runs/start", map[string]any{
		"idempotencyKey":      "0123456789abcdef0123456789abcdef",
		"workspaceGeneration": strings.Repeat("a", 64),
		"projectId":           "core", "coverageProfileId": "coverage-debug",
		"catalogRevision": strings.Repeat("b", 64),
		"selection":       map[string]any{"mode": "all"}, "repeatCount": 1,
		"timeoutMs": 180_000,
	}))
	if result.Response.Error == nil || result.Response.Error.Code == "INVALID_MESSAGE" {
		t.Fatalf("coverage/runs/start = %#v, want backend error after payload validation", result.Response)
	}
	if backend.calls != 1 {
		t.Fatalf("backend calls = %d, want 1", backend.calls)
	}
}

func TestV14CoverageTaskCancelLoadsRelationsBeforeCancellation(t *testing.T) {
	request, err := coveragedomain.NewRequest(coveragedomain.Request{
		IdempotencyKey: strings.Repeat("1", 32), WorkspaceGeneration: strings.Repeat("2", 64),
		ProjectID: "core", CoverageProfileID: "coverage-debug", CatalogRevision: strings.Repeat("3", 64),
		Selection: testdomain.Selection{Mode: testdomain.SelectionAll}, RepeatCount: 1, Timeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := request.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	runID, err := coveragedomain.CoverageRunID(request)
	if err != nil {
		t.Fatal(err)
	}
	started := fixedTime.Add(time.Second)
	taskID, testRunID := strings.Repeat("4", 32), strings.Repeat("5", 32)
	running := task.Task{
		ID: taskID, Kind: task.KindCoverageRun, Request: canonical,
		WorkspaceGeneration: request.WorkspaceGeneration, Timeout: request.Timeout,
		Status: task.StatusRunning, CreatedAt: fixedTime, StartedAt: &started, LastSequence: 7,
	}
	cancelling := running
	cancelling.Status, cancelling.LastSequence = task.StatusCancelling, 8
	backend := &coverageBackend{
		fakeBackend:              &fakeBackend{getResult: running, cancelResult: cancelling},
		testRun:                  testdomain.TestRun{RunID: testRunID, TaskID: taskID},
		failRelationsAfterCancel: true,
	}
	active := session.NewWithCoverage("0123456789abcdef", "linux", "unix-socket", backend, backend)
	if result := active.Handle(context.Background(), requestVersion(t, protocol.Version14, "handshake", map[string]any{
		"token": "0123456789abcdef", "clientName": "test", "clientVersion": "0.5.0",
		"supportedProtocolVersions": []string{protocol.Version14},
	})); result.Response.Kind != "response" {
		t.Fatalf("handshake = %#v", result.Response)
	}

	result := active.Handle(context.Background(), requestVersion(t, protocol.Version14, "tasks/cancel", map[string]any{"taskId": taskID}))
	projected, ok := result.Response.Payload.(taskv14.CoverageRunTaskSnapshotV14)
	if result.Response.Error != nil || !ok {
		t.Fatalf("tasks/cancel = %#v", result.Response)
	}
	if !backend.testRunLoadedBeforeCancel || projected.CoverageRunID != runID || projected.TestRunID != testRunID || projected.TimeoutMS != 5_000 || projected.Status != taskv14.TaskCancellingV14 {
		t.Fatalf("projection = %#v, loaded-before-cancel=%t", projected, backend.testRunLoadedBeforeCancel)
	}
}

func TestV14CoverageTaskListProjectsCoverageItems(t *testing.T) {
	request, err := coveragedomain.NewRequest(coveragedomain.Request{
		IdempotencyKey: strings.Repeat("6", 32), WorkspaceGeneration: strings.Repeat("7", 64),
		ProjectID: "core", CoverageProfileID: "coverage-debug", CatalogRevision: strings.Repeat("8", 64),
		Selection: testdomain.Selection{Mode: testdomain.SelectionAll}, RepeatCount: 2, Timeout: 9 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := request.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	runID, err := coveragedomain.CoverageRunID(request)
	if err != nil {
		t.Fatal(err)
	}
	taskID, testRunID := strings.Repeat("9", 32), strings.Repeat("a", 32)
	coverageTask := task.Task{
		ID: taskID, Kind: task.KindCoverageRun, Request: canonical,
		WorkspaceGeneration: request.WorkspaceGeneration, Timeout: request.Timeout,
		Status: task.StatusQueued, CreatedAt: fixedTime, LastSequence: 3,
	}
	backend := &coverageBackend{
		fakeBackend: &fakeBackend{listResult: task.Page[task.Task]{Items: []task.Task{coverageTask}, NextCursor: "next"}},
		testRun:     testdomain.TestRun{RunID: testRunID, TaskID: taskID},
	}
	active := session.NewWithCoverage("0123456789abcdef", "linux", "unix-socket", backend, backend)
	if result := active.Handle(context.Background(), requestVersion(t, protocol.Version14, "handshake", map[string]any{
		"token": "0123456789abcdef", "clientName": "test", "clientVersion": "0.5.0",
		"supportedProtocolVersions": []string{protocol.Version14},
	})); result.Response.Kind != "response" {
		t.Fatalf("handshake = %#v", result.Response)
	}

	result := active.Handle(context.Background(), requestVersion(t, protocol.Version14, "tasks/list", map[string]any{"limit": 10}))
	if result.Response.Error != nil {
		t.Fatalf("tasks/list = %#v", result.Response)
	}
	raw, err := json.Marshal(result.Response.Payload)
	if err != nil {
		t.Fatal(err)
	}
	var page struct {
		Items      []taskv14.CoverageRunTaskSnapshotV14 `json:"items"`
		NextCursor string                               `json:"nextCursor"`
	}
	if err := json.Unmarshal(raw, &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || page.Items[0].CoverageRunID != runID || page.Items[0].TestRunID != testRunID || page.Items[0].TimeoutMS != 9_000 || page.NextCursor != "next" {
		t.Fatalf("tasks/list payload = %s", raw)
	}
}

func TestV13CompatibilityHidesCoverageTasks(t *testing.T) {
	coverageTask := task.Task{ID: strings.Repeat("b", 32), Kind: task.KindCoverageRun}
	backend := &coverageBackend{fakeBackend: &fakeBackend{
		getResult:  coverageTask,
		listResult: task.Page[task.Task]{},
	}}
	active := session.NewWithCoverage("0123456789abcdef", "linux", "unix-socket", backend, backend)
	if result := active.Handle(context.Background(), requestVersion(t, protocol.Version14, "handshake", map[string]any{
		"token": "0123456789abcdef", "clientName": "test", "clientVersion": "0.5.0",
		"supportedProtocolVersions": []string{protocol.Version13},
	})); result.Response.Kind != "response" || active.NegotiatedVersion() != protocol.Version13 {
		t.Fatalf("handshake = %#v, negotiated=%q", result.Response, active.NegotiatedVersion())
	}

	getResult := active.Handle(context.Background(), requestVersion(t, protocol.Version13, "tasks/get", map[string]any{"taskId": coverageTask.ID}))
	if getResult.Response.Error == nil || getResult.Response.Error.Code != "TASK_NOT_FOUND" {
		t.Fatalf("tasks/get = %#v", getResult.Response)
	}
	listResult := active.Handle(context.Background(), requestVersion(t, protocol.Version13, "tasks/list", map[string]any{"limit": 10}))
	if listResult.Response.Error != nil {
		t.Fatalf("tasks/list = %#v", listResult.Response)
	}
	wantKinds := []task.Kind{task.KindSimulation, task.KindCMakeBuild, task.KindTestDiscovery, task.KindTestRun}
	if !reflect.DeepEqual(backend.listKinds, wantKinds) {
		t.Fatalf("tasks/list kinds = %#v, want %#v", backend.listKinds, wantKinds)
	}
}

type coverageBackend struct {
	*fakeBackend
	calls                     int
	testRun                   testdomain.TestRun
	cancelCalled              bool
	failRelationsAfterCancel  bool
	testRunLoadedBeforeCancel bool
}

func (*coverageBackend) InspectWorkspace(context.Context) (discovery.Snapshot, error) {
	return discovery.Snapshot{WorkspaceURI: "file:///workspace", Generation: strings.Repeat("a", 64)}, nil
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
func (backend *coverageBackend) GetTestRunForTask(context.Context, string) (testdomain.TestRun, error) {
	if backend.failRelationsAfterCancel && backend.cancelCalled {
		return testdomain.TestRun{}, task.ErrStorageUnavailable
	}
	backend.testRunLoadedBeforeCancel = true
	return backend.testRun, nil
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

func (backend *coverageBackend) Cancel(ctx context.Context, taskID string) (task.Task, error) {
	backend.cancelCalled = true
	return backend.fakeBackend.Cancel(ctx, taskID)
}
