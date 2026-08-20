package runtime

import (
	"context"
	"encoding/json"
	"net"
	"reflect"
	"testing"
	"time"

	"unit-test-ide.local/test-service/internal/coveragecoord"
	"unit-test-ide.local/test-service/internal/coveragedomain"
	"unit-test-ide.local/test-service/internal/protocol"
	"unit-test-ide.local/test-service/internal/server"
	"unit-test-ide.local/test-service/internal/session"
	"unit-test-ide.local/test-service/internal/task"
	"unit-test-ide.local/test-service/internal/testdomain"
	"unit-test-ide.local/test-service/internal/toolchain"
)

func TestCoverageStartThroughRealServerPersistsBeforeResumeAndUntrustedHasNoEffect(t *testing.T) {
	aggregate := coverageServerAggregate(t)
	startedAt := aggregate.Task.CreatedAt.Add(time.Second)
	canonicalTask, err := task.ApplyTransition(aggregate.Task, task.Transition{
		From: task.StatusQueued, To: task.StatusRunning, At: startedAt,
	})
	if err != nil {
		t.Fatal(err)
	}
	canonicalRun := aggregate.Run
	canonicalRun.Status = coveragedomain.StatusRunning
	canonicalRun.StartedAt = &startedAt
	canonicalRun, err = coveragedomain.NewRun(canonicalRun)
	if err != nil {
		t.Fatal(err)
	}
	canonicalTest := aggregate.TestRun
	canonicalTest.Status = testdomain.RunRunning
	canonicalTest.StartedAt = &startedAt
	canonicalTest, err = testdomain.NewTestRun(canonicalTest)
	if err != nil {
		t.Fatal(err)
	}
	var steps []string
	queue := &fakeCoverageQueue{steps: &steps, result: coveragecoord.QueuedStartResult{
		Task: aggregate.Task, Run: aggregate.Run, TestRun: aggregate.TestRun,
	}}
	executor := &fakeCoverageExecutor{steps: &steps, resumeResult: canonicalTask}
	repository := &fakeCoverageRepository{
		task: canonicalTask, run: canonicalRun, testRun: canonicalTest, steps: &steps,
	}
	backend, err := newRuntimeCoverageBackend(queue, repository, func(context.Context, session.CoverageRunStart) (coveragecoord.QueuedStartInput, error) {
		return coveragecoord.QueuedStartInput{}, nil
	}, executor)
	if err != nil {
		t.Fatal(err)
	}

	trusted := &Runtime{trustedWorkspace: true, coverageBackend: backend}
	response := exchangeCoverageProtocol(t, trusted, trusted.CoverageBackend())
	if response.Error != nil {
		t.Fatalf("trusted coverage/runs/start response = %#v", response)
	}
	payload, ok := response.Payload.(map[string]any)
	if !ok || payload["coverageRunId"] != canonicalRun.ID || payload["taskId"] != canonicalTask.ID || payload["status"] != string(coveragedomain.StatusRunning) {
		t.Fatalf("trusted canonical protocol payload = %#v", response.Payload)
	}
	wantSteps := []string{"persist", "resume", "reload-task", "reload-coverage-run", "reload-test-run"}
	if !reflect.DeepEqual(steps, wantSteps) {
		t.Fatalf("trusted protocol effects = %v, want %v", steps, wantSteps)
	}

	untrusted := &Runtime{trustedWorkspace: false, coverageBackend: backend}
	response = exchangeCoverageProtocol(t, untrusted, untrusted.CoverageBackend())
	if response.Error == nil {
		t.Fatalf("untrusted coverage/runs/start unexpectedly succeeded: %#v", response)
	}
	if !reflect.DeepEqual(steps, wantSteps) || queue.called != 1 || len(executor.resumed) != 1 {
		t.Fatalf("untrusted protocol caused coverage effects: steps=%v queue=%d resume=%v", steps, queue.called, executor.resumed)
	}
}

func exchangeCoverageProtocol(t *testing.T, backend session.Backend, coverage session.CoverageBackend) protocol.Response {
	t.Helper()
	client, service := net.Pipe()
	active := session.NewWithCoverage("0123456789abcdef", platformForTest(), "test-pipe", backend, coverage)
	go server.ServeConnection(service, active)
	t.Cleanup(func() { _ = client.Close() })
	handshakePayload, err := json.Marshal(map[string]any{
		"token": "0123456789abcdef", "clientName": "runtime-test", "clientVersion": "1.0.0",
		"supportedProtocolVersions": []string{protocol.Version14, protocol.Version13},
	})
	if err != nil {
		t.Fatal(err)
	}
	handshake := coverageProtocolExchange(t, client, protocol.Request{
		ProtocolVersion: protocol.Version14, Kind: "request", MessageID: stringsOf('b', 32),
		Method: "handshake", SentAt: "2026-08-20T00:00:00Z", Payload: handshakePayload,
	})
	if handshake.Error != nil {
		t.Fatalf("coverage handshake = %#v", handshake)
	}
	startPayload, err := json.Marshal(map[string]any{
		"idempotencyKey": stringsOf('3', 32), "workspaceGeneration": stringsOf('5', 64),
		"projectId": "core", "coverageProfileId": "coverage-default", "catalogRevision": stringsOf('6', 64),
		"selection": map[string]any{"mode": "all"}, "repeatCount": 1, "timeoutMs": 1_000_000,
	})
	if err != nil {
		t.Fatal(err)
	}
	return coverageProtocolExchange(t, client, protocol.Request{
		ProtocolVersion: protocol.Version14, Kind: "request", MessageID: stringsOf('c', 32),
		Method: "coverage/runs/start", SentAt: "2026-08-20T00:00:00Z", Payload: startPayload,
	})
}

func coverageProtocolExchange(t *testing.T, connection net.Conn, request protocol.Request) protocol.Response {
	t.Helper()
	if err := json.NewEncoder(connection).Encode(request); err != nil {
		t.Fatal(err)
	}
	var response protocol.Response
	if err := json.NewDecoder(connection).Decode(&response); err != nil {
		t.Fatal(err)
	}
	return response
}

func coverageServerAggregate(t *testing.T) coveragecoord.QueuedAggregate {
	t.Helper()
	toolchainSnapshot, err := coverageToolchainSnapshot(toolchain.Instance{
		ID: "retained-toolchain", Family: func() toolchain.Family {
			if platformForTest() == "windows" {
				return toolchain.FamilyClangCL
			}
			return toolchain.FamilyGCC
		}(), Version: "18.1.8", TargetArchitecture: "amd64",
	}, platformForTest())
	if err != nil {
		t.Fatal(err)
	}
	ids := []string{stringsOf('1', 32), stringsOf('2', 32)}
	aggregate, err := coveragecoord.NewQueuedAggregate(coveragecoord.QueuedInput{
		Request: coveragedomain.Request{
			IdempotencyKey: stringsOf('3', 32), WorkspaceGeneration: stringsOf('5', 64),
			ProjectID: "core", CoverageProfileID: "coverage-default", CatalogRevision: stringsOf('6', 64),
			Selection: testdomain.Selection{Mode: testdomain.SelectionAll}, RepeatCount: 1, Timeout: 1000 * time.Second,
		},
		Selection:      testdomain.SelectionSnapshot{Mode: testdomain.SelectionAll, ContainerIDs: []testdomain.ID{}, ItemIDs: []testdomain.ID{}},
		BuildProfileID: stringsOf('7', 64), ToolchainID: "retained-toolchain", Toolchain: toolchainSnapshot,
		CreatedAt: time.Date(2026, 8, 20, 1, 2, 3, 0, time.UTC),
		NewID:     func() string { id := ids[0]; ids = ids[1:]; return id },
	})
	if err != nil {
		t.Fatal(err)
	}
	return aggregate
}
