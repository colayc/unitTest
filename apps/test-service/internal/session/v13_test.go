package session_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"unit-test-ide.local/test-service/internal/protocol"
	capabilitiesv13 "unit-test-ide.local/test-service/internal/protocolmodel/v1_3/capabilities"
	taskv13 "unit-test-ide.local/test-service/internal/protocolmodel/v1_3/task"
	testv13 "unit-test-ide.local/test-service/internal/protocolmodel/v1_3/test"
	"unit-test-ide.local/test-service/internal/session"
	"unit-test-ide.local/test-service/internal/task"
	"unit-test-ide.local/test-service/internal/testdomain"
)

func authenticatedV13(
	t *testing.T,
	backend session.Backend,
	offered []string,
) *session.Session {
	t.Helper()
	active := session.New(
		"0123456789abcdef",
		"linux",
		"unix-socket",
		backend,
	)
	result := active.Handle(
		context.Background(),
		requestVersion(
			t,
			protocol.Version13,
			"handshake",
			map[string]any{
				"token":                     "0123456789abcdef",
				"clientName":                "test",
				"clientVersion":             "0.4.0",
				"supportedProtocolVersions": offered,
			},
		),
	)
	if result.Response.Kind != "response" {
		t.Fatalf("handshake failed: %#v", result.Response)
	}
	return active
}

func TestV12CompatibilityHidesTestTasksAndArtifacts(t *testing.T) {
	testTask := task.Task{
		ID:        id('6'),
		Kind:      task.KindTestRun,
		Status:    task.StatusRunning,
		CreatedAt: fixedTime,
	}
	backend := &fakeBackend{
		getResult: testTask,
		chunk: session.ArtifactChunk{
			Metadata: task.Artifact{
				ID:        id('7'),
				TaskID:    testTask.ID,
				Kind:      "test-results",
				MIMEType:  "application/x-ndjson",
				SHA256:    string(bytesOf('8', 64)),
				CreatedAt: fixedTime,
			},
		},
	}
	active := authenticatedV12(t, backend)
	for _, input := range []struct {
		method  string
		payload map[string]any
	}{
		{
			method:  "tasks/get",
			payload: map[string]any{"taskId": testTask.ID},
		},
		{
			method:  "tasks/cancel",
			payload: map[string]any{"taskId": testTask.ID},
		},
		{
			method: "artifacts/list",
			payload: map[string]any{
				"taskId": testTask.ID,
				"limit":  1,
			},
		},
		{
			method: "artifacts/read",
			payload: map[string]any{
				"artifactId": id('7'),
				"offset":     0,
				"length":     1,
			},
		},
	} {
		result := active.Handle(
			context.Background(),
			requestVersion(
				t,
				protocol.Version12,
				input.method,
				input.payload,
			),
		)
		if result.Response.Error == nil ||
			result.Response.Error.Code != "TASK_NOT_FOUND" {
			t.Fatalf(
				"%s response = %#v",
				input.method,
				result.Response,
			)
		}
	}

	backend.listResult = task.Page[task.Task]{}
	result := active.Handle(
		context.Background(),
		requestVersion(
			t,
			protocol.Version12,
			"tasks/list",
			map[string]any{"limit": 10},
		),
	)
	if result.Response.Kind != "response" ||
		len(backend.listKinds) != 2 ||
		backend.listKinds[0] != task.KindSimulation ||
		backend.listKinds[1] != task.KindCMakeBuild {
		t.Fatalf(
			"tasks/list response = %#v, kinds = %#v",
			result.Response,
			backend.listKinds,
		)
	}
}

func TestV13NegotiationSelectsHighestMutualVersion(t *testing.T) {
	active := authenticatedV13(
		t,
		&fakeBackend{},
		[]string{
			protocol.Version10,
			protocol.Version12,
			protocol.Version13,
			protocol.Version11,
		},
	)
	if got := active.NegotiatedVersion(); got != protocol.Version13 {
		t.Fatalf("NegotiatedVersion() = %q, want 1.3", got)
	}

	downgraded := authenticatedV13(
		t,
		&fakeBackend{},
		[]string{protocol.Version10, protocol.Version12},
	)
	if got := downgraded.NegotiatedVersion(); got != protocol.Version12 {
		t.Fatalf("downgraded NegotiatedVersion() = %q, want 1.2", got)
	}
}

func TestV13CapabilitiesAdvertiseBoundedTestExecution(t *testing.T) {
	active := authenticatedV13(
		t,
		&fakeBackend{},
		[]string{protocol.Version13},
	)
	result := active.Handle(
		context.Background(),
		requestVersion(
			t,
			protocol.Version13,
			"capabilities/get",
			map[string]any{},
		),
	)
	capabilities, ok :=
		result.Response.Payload.(capabilitiesv13.CapabilitiesV13)
	if result.Response.Kind != "response" || !ok {
		t.Fatalf("capabilities response = %#v", result.Response)
	}
	if !capabilities.TestDiscovery || !capabilities.TestRun ||
		!capabilities.OpaqueCTestFallback ||
		capabilities.MaxRepeatCount != 100 ||
		capabilities.MaxSelectionSize != 100_000 ||
		capabilities.MaxCatalogPageSize != 1_000 ||
		len(capabilities.FrameworkAdapters) != 2 {
		t.Fatalf("capabilities = %#v", capabilities)
	}
}

type v13Backend struct {
	*fakeBackend

	discoveryInput session.TestDiscoveryStart
	runInput       session.TestRunStart
	catalogInput   testdomain.CatalogPageRequest
	runID          string
	runTaskID      string
	runPageInput   testdomain.RunPageRequest

	discoveryTask task.Task
	runTask       task.Task
	run           testdomain.TestRun
	catalog       testdomain.CatalogPage
	runPage       testdomain.RunPage
}

func (backend *v13Backend) StartTestDiscovery(
	_ context.Context,
	input session.TestDiscoveryStart,
) (task.Task, error) {
	backend.discoveryInput = input
	return backend.discoveryTask, backend.err
}

func (backend *v13Backend) StartTestRun(
	_ context.Context,
	input session.TestRunStart,
) (task.Task, testdomain.TestRun, error) {
	backend.runInput = input
	return backend.runTask, backend.run, backend.err
}

func (backend *v13Backend) GetTestCatalog(
	_ context.Context,
	input testdomain.CatalogPageRequest,
) (testdomain.CatalogPage, error) {
	backend.catalogInput = input
	return backend.catalog, backend.err
}

func (backend *v13Backend) GetTestRun(
	_ context.Context,
	runID string,
) (testdomain.TestRun, error) {
	backend.runID = runID
	return backend.run, backend.err
}

func (backend *v13Backend) GetTestRunForTask(
	_ context.Context,
	taskID string,
) (testdomain.TestRun, error) {
	backend.runTaskID = taskID
	return backend.run, backend.err
}

func (backend *v13Backend) ListTestRuns(
	_ context.Context,
	input testdomain.RunPageRequest,
) (testdomain.RunPage, error) {
	backend.runPageInput = input
	return backend.runPage, backend.err
}

func TestV13RoutesTestStartsAndReadModels(t *testing.T) {
	fixture := newV13BackendFixture(t)
	active := authenticatedV13(
		t,
		fixture,
		[]string{protocol.Version13},
	)
	discovery := active.Handle(
		context.Background(),
		requestVersion(
			t,
			protocol.Version13,
			"tasks/start",
			map[string]any{
				"idempotencyKey": id('2'),
				"kind":           "testDiscovery",
				"projectId":      "core",
				"profileId":      strings.Repeat("3", 64),
			},
		),
	)
	discoveryTask, ok := discovery.Response.Payload.(taskv13.TestDiscoveryTaskSnapshotV13)
	if discovery.Response.Kind != "response" || !ok ||
		discoveryTask.TaskID != fixture.discoveryTask.ID ||
		fixture.discoveryInput.ProjectID != "core" {
		t.Fatalf(
			"discovery response = %#v, input = %#v",
			discovery.Response,
			fixture.discoveryInput,
		)
	}

	run := active.Handle(
		context.Background(),
		requestVersion(
			t,
			protocol.Version13,
			"tasks/start",
			map[string]any{
				"idempotencyKey":  id('4'),
				"kind":            "testRun",
				"projectId":       "core",
				"profileId":       strings.Repeat("3", 64),
				"catalogRevision": strings.Repeat("5", 64),
				"selection": map[string]any{
					"mode": "items",
					"itemIds": []string{
						fixture.run.SelectionSnapshot.ItemIDs[0].String(),
					},
				},
				"repeatCount": 2,
			},
		),
	)
	runTask, ok :=
		run.Response.Payload.(taskv13.TestRunTaskSnapshotV13)
	if run.Response.Kind != "response" || !ok ||
		runTask.RunID != fixture.run.RunID ||
		fixture.runInput.RepeatCount != 2 ||
		fixture.runInput.Selection.Mode !=
			testdomain.SelectionItems {
		t.Fatalf(
			"run response = %#v, input = %#v",
			run.Response,
			fixture.runInput,
		)
	}

	catalog := active.Handle(
		context.Background(),
		requestVersion(
			t,
			protocol.Version13,
			"tests/catalog/get",
			map[string]any{
				"projectId": "core",
				"profileId": strings.Repeat("3", 64),
				"limit":     17,
			},
		),
	)
	projectedCatalog, ok :=
		catalog.Response.Payload.(testv13.TestCatalog)
	if catalog.Response.Kind != "response" || !ok ||
		len(projectedCatalog.Items) != 1 ||
		fixture.catalogInput.Limit != 17 {
		t.Fatalf(
			"catalog response = %#v, input = %#v",
			catalog.Response,
			fixture.catalogInput,
		)
	}

	getRun := active.Handle(
		context.Background(),
		requestVersion(
			t,
			protocol.Version13,
			"tests/runs/get",
			map[string]any{"runId": fixture.run.RunID},
		),
	)
	projectedRun, ok := getRun.Response.Payload.(testv13.TestRun)
	if !ok ||
		projectedRun.Outcome == nil ||
		*projectedRun.Outcome != testv13.TestRunOutcomeV13Passed ||
		fixture.runID != fixture.run.RunID {
		t.Fatalf(
			"run get response = %#v, runID = %q",
			getRun.Response,
			fixture.runID,
		)
	}

	listRuns := active.Handle(
		context.Background(),
		requestVersion(
			t,
			protocol.Version13,
			"tests/runs/list",
			map[string]any{
				"projectId": "core",
				"profileId": strings.Repeat("3", 64),
				"limit":     19,
			},
		),
	)
	projectedPage, ok :=
		listRuns.Response.Payload.(testv13.TestRunPage)
	if listRuns.Response.Kind != "response" || !ok ||
		len(projectedPage.Items) != 1 ||
		fixture.runPageInput.Limit != 19 {
		t.Fatalf(
			"run list response = %#v, input = %#v",
			listRuns.Response,
			fixture.runPageInput,
		)
	}
}

func TestV13QueuedTestRunOmitsTerminalOutcome(t *testing.T) {
	fixture := newV13BackendFixture(t)
	fixture.run.Status = testdomain.RunQueued
	fixture.run.Outcome = ""
	fixture.run.StartedAt = nil
	fixture.run.FinishedAt = nil
	fixture.runPage.Items = []testdomain.TestRun{
		fixture.run,
	}
	active := authenticatedV13(
		t,
		fixture,
		[]string{protocol.Version13},
	)
	result := active.Handle(
		context.Background(),
		requestVersion(
			t,
			protocol.Version13,
			"tests/runs/get",
			map[string]any{"runId": fixture.run.RunID},
		),
	)
	projected, ok := result.Response.Payload.(testv13.TestRun)
	if !ok || projected.Outcome != nil {
		t.Fatalf("queued TestRun projection = %#v", result.Response)
	}
	encoded, err := json.Marshal(projected)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &fields); err != nil {
		t.Fatal(err)
	}
	if _, exists := fields["outcome"]; exists {
		t.Fatalf("queued TestRun contains outcome: %s", encoded)
	}
}

func TestV13StrictTestRequestsRejectExecutionInjection(t *testing.T) {
	fixture := newV13BackendFixture(t)
	active := authenticatedV13(
		t,
		fixture,
		[]string{protocol.Version13},
	)
	for _, raw := range []string{
		`{"idempotencyKey":"22222222222222222222222222222222","kind":"testDiscovery","projectId":"core","profileId":"3333333333333333333333333333333333333333333333333333333333333333","executable":"bad"}`,
		`{"idempotencyKey":"44444444444444444444444444444444","kind":"testRun","projectId":"core","profileId":"3333333333333333333333333333333333333333333333333333333333333333","catalogRevision":"5555555555555555555555555555555555555555555555555555555555555555","selection":{"mode":"all","environment":{"TOKEN":"bad"}},"repeatCount":1}`,
	} {
		result := active.Handle(
			context.Background(),
			protocol.Request{
				ProtocolVersion: protocol.Version13,
				Kind:            "request",
				MessageID:       id('9'),
				Method:          "tasks/start",
				Payload:         json.RawMessage(raw),
			},
		)
		if result.Response.Error == nil ||
			result.Response.Error.Code != "INVALID_MESSAGE" {
			t.Fatalf("injection response = %#v", result.Response)
		}
	}
}

func newV13BackendFixture(t *testing.T) *v13Backend {
	t.Helper()
	profileID := strings.Repeat("3", 64)
	revision := strings.Repeat("5", 64)
	containerID, err :=
		testdomain.ContainerID("core", "core.cpputest")
	if err != nil {
		t.Fatal(err)
	}
	itemID, err := testdomain.CaseID(
		testdomain.CaseIdentity{
			ProjectID: "core",
			CTestName: "core.cpputest",
			Framework: testdomain.FrameworkCppUTest,
			Group:     "math",
			Name:      "adds",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	started := fixedTime.Add(time.Second)
	finished := started.Add(time.Second)
	run := testdomain.TestRun{
		RunID:           id('6'),
		TaskID:          id('7'),
		IdempotencyKey:  id('4'),
		ProjectID:       "core",
		ProfileID:       profileID,
		ToolchainID:     "linux-clang",
		CatalogRevision: revision,
		SelectionSnapshot: testdomain.SelectionSnapshot{
			Mode:    testdomain.SelectionItems,
			ItemIDs: []testdomain.ID{itemID},
		},
		Status:     testdomain.RunCompleted,
		Outcome:    testdomain.RunPassed,
		CreatedAt:  fixedTime,
		StartedAt:  &started,
		FinishedAt: &finished,
		Summary: testdomain.RunSummary{
			Total:      1,
			Completed:  1,
			Passed:     1,
			Iterations: 2,
		},
		ResultRevision: testdomain.EmptyResultRevision(),
	}
	return &v13Backend{
		fakeBackend: &fakeBackend{},
		discoveryTask: task.Task{
			ID:   id('8'),
			Kind: task.KindTestDiscovery,
			Request: json.RawMessage(
				`{"projectId":"core","buildProfileId":"` +
					profileID +
					`","targetIds":[],"jobs":1,"timeoutMs":60000}`,
			),
			WorkspaceGeneration: strings.Repeat("a", 64),
			Timeout:             time.Minute,
			Status:              task.StatusQueued,
			CreatedAt:           fixedTime,
			LastSequence:        1,
		},
		runTask: task.Task{
			ID:           run.TaskID,
			Kind:         task.KindTestRun,
			Status:       task.StatusQueued,
			CreatedAt:    fixedTime,
			LastSequence: 1,
		},
		run: run,
		catalog: testdomain.CatalogPage{
			ProjectID:   "core",
			ProfileID:   profileID,
			Revision:    revision,
			GeneratedAt: fixedTime,
			Containers: []testdomain.Container{
				{
					ID:               containerID,
					ProjectID:        "core",
					CTestLogicalName: "core.cpputest",
					DisplayName:      "Core CppUTest",
					Framework:        testdomain.FrameworkCppUTest,
					Capabilities: testdomain.Capabilities{
						CanDiscoverCases: true,
						CanRunCase:       true,
					},
					Labels: []string{},
				},
			},
			Items: []testdomain.Item{
				{
					ID:          itemID,
					ContainerID: containerID,
					Kind:        testdomain.ItemCase,
					Framework:   testdomain.FrameworkCppUTest,
					LogicalName: "adds",
					DisplayName: "adds",
					Labels:      []string{},
				},
			},
			Diagnostics: []testdomain.Diagnostic{},
		},
		runPage: testdomain.RunPage{
			Items: []testdomain.TestRun{run},
		},
	}
}
