package session_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"unit-test-ide.local/test-service/internal/build"
	"unit-test-ide.local/test-service/internal/cmake"
	"unit-test-ide.local/test-service/internal/diagnostic"
	"unit-test-ide.local/test-service/internal/discovery"
	"unit-test-ide.local/test-service/internal/eventbroker"
	"unit-test-ide.local/test-service/internal/protocol"
	"unit-test-ide.local/test-service/internal/session"
	"unit-test-ide.local/test-service/internal/task"
	"unit-test-ide.local/test-service/internal/toolchain"
	"unit-test-ide.local/test-service/internal/workspace"
)

type v12Backend struct {
	simulationInput task.SimulationStart
	buildInput      build.StartRequest
	targetsInput    build.TargetsRequest
	listKinds       []task.Kind
	cancelCalls     int

	snapshot     discovery.Snapshot
	targets      []cmake.Target
	startResult  task.Task
	getResult    task.Task
	listResult   task.Page[task.Task]
	cancelResult task.Task
	artifacts    task.Page[task.Artifact]
	chunk        session.ArtifactChunk

	inspectErr, targetsErr, startErr, getErr, listErr, cancelErr, artifactErr error
}

func (b *v12Backend) StartSimulation(_ context.Context, input task.SimulationStart) (task.Task, error) {
	b.simulationInput = input
	return b.startResult, b.startErr
}

func (b *v12Backend) InspectWorkspace(context.Context) (discovery.Snapshot, error) {
	return b.snapshot, b.inspectErr
}

func (b *v12Backend) ListTargets(_ context.Context, input build.TargetsRequest) ([]cmake.Target, error) {
	b.targetsInput = input
	return b.targets, b.targetsErr
}

func (b *v12Backend) StartBuild(_ context.Context, input build.StartRequest) (task.Task, error) {
	b.buildInput = input
	return b.startResult, b.startErr
}

func (b *v12Backend) Get(_ context.Context, taskID string) (task.Task, error) {
	return b.getResult, b.getErr
}

func (b *v12Backend) List(_ context.Context, cursor string, limit int, kinds []task.Kind) (task.Page[task.Task], error) {
	b.listKinds = append([]task.Kind(nil), kinds...)
	return b.listResult, b.listErr
}

func (b *v12Backend) Cancel(_ context.Context, taskID string) (task.Task, error) {
	b.cancelCalls++
	return b.cancelResult, b.cancelErr
}

func (b *v12Backend) Subscribe(context.Context, int64) (*eventbroker.Subscription, error) {
	return nil, b.listErr
}

func (b *v12Backend) ListArtifacts(context.Context, string, string, int) (task.Page[task.Artifact], error) {
	return b.artifacts, b.artifactErr
}

func (b *v12Backend) ReadArtifact(context.Context, string, int64, int) (session.ArtifactChunk, error) {
	return b.chunk, b.artifactErr
}

func authenticatedV12(t *testing.T, backend session.Backend) *session.Session {
	t.Helper()
	s := session.New("0123456789abcdef", "linux", "unix-socket", backend)
	result := s.Handle(context.Background(), requestVersion(t, protocol.Version12, "handshake", map[string]any{
		"token": "0123456789abcdef", "clientName": "test", "clientVersion": "0.3.0",
		"supportedProtocolVersions": []string{protocol.Version10, protocol.Version12, protocol.Version11},
	}))
	if result.Response.Kind != "response" || s.NegotiatedVersion() != protocol.Version12 {
		t.Fatalf("v1.2 handshake failed: %#v", result)
	}
	return s
}

func TestSessionVersion12NegotiatesHighestCommonVersion(t *testing.T) {
	for _, version := range []string{protocol.Version12, protocol.Version11, protocol.Version10} {
		t.Run(version, func(t *testing.T) {
			s := session.New("0123456789abcdef", "linux", "unix-socket", nil)
			payload := map[string]any{
				"token": "0123456789abcdef", "clientName": "test", "clientVersion": "0.3.0",
			}
			if version != protocol.Version10 {
				payload["supportedProtocolVersions"] = []string{protocol.Version10, version}
			}
			result := s.Handle(context.Background(), requestVersion(t, version, "handshake", payload))
			if result.Response.Kind != "response" || s.NegotiatedVersion() != version {
				t.Fatalf("version %s negotiation = %#v", version, result)
			}
		})
	}
}

func TestSessionVersion12NegotiatesDownToHighestOfferedVersion(t *testing.T) {
	tests := []struct {
		envelope string
		offered  []string
		want     string
	}{
		{protocol.Version12, []string{protocol.Version10, protocol.Version11}, protocol.Version11},
		{protocol.Version12, []string{protocol.Version10}, protocol.Version10},
		{protocol.Version11, []string{protocol.Version12, protocol.Version10}, protocol.Version10},
	}
	for _, test := range tests {
		t.Run(test.envelope+"-"+test.want, func(t *testing.T) {
			s := session.New("0123456789abcdef", "linux", "unix-socket", nil)
			result := s.Handle(context.Background(), requestVersion(t, test.envelope, "handshake", map[string]any{
				"token": "0123456789abcdef", "clientName": "test", "clientVersion": "0.3.0",
				"supportedProtocolVersions": test.offered,
			}))
			if result.Response.Kind != "response" || s.NegotiatedVersion() != test.want ||
				result.Response.ProtocolVersion != test.want {
				t.Fatalf("negotiation = %#v, want %s", result, test.want)
			}
		})
	}
}

func TestSessionVersion12GatesWorkspaceMethodsBeforeBackendLookup(t *testing.T) {
	s := authenticatedV11(t, nil)
	for _, method := range []string{"workspace/inspect", "cmake/targets/list"} {
		result := s.Handle(context.Background(), requestVersion(t, protocol.Version11, method, map[string]any{}))
		if result.Error == nil || result.Error.Code != "PROTOCOL_FEATURE_UNAVAILABLE" {
			t.Fatalf("%s = %#v", method, result)
		}
	}
}

func TestSessionVersion12RoutesWorkspaceInspectTargetsAndBuild(t *testing.T) {
	generation := strings.Repeat("a", 64)
	profileID := strings.Repeat("b", 64)
	targetID := strings.Repeat("c", 64)
	backend := &v12Backend{
		snapshot: discovery.Snapshot{
			WorkspaceURI: "file:///workspace",
			Generation:   generation,
			Projects:     []workspace.ProjectConfig{{ID: "core", SourceDir: "src/core"}},
			Profiles: []cmake.BuildProfile{{
				ID: profileID, ProjectID: "core", Origin: "generated",
				ToolchainID: "gcc-test", Generator: "Ninja", Configuration: "Debug",
			}},
			Toolchains: []toolchain.Instance{{
				ID: "gcc-test", Family: toolchain.FamilyGCC, Version: "15.1.0",
				TargetTriple: "x86_64-linux-gnu", HostArchitecture: "x64", TargetArchitecture: "x64",
				Generators: []string{"Ninja", "Unix Makefiles"},
				Coverage:   toolchain.CoverageCapability{GCov: "/usr/bin/gcov"},
			}},
			Diagnostics: []diagnostic.Diagnostic{{
				Severity: "warning", Code: "TOOLCHAIN_NOTICE",
				Message: "toolchain notice", FileURI: "file:///workspace/CMakeLists.txt",
			}},
		},
		targets: []cmake.Target{{ID: targetID, Name: "unit-tests"}},
		startResult: task.Task{
			ID: id('1'), Kind: task.KindCMakeBuild,
			Request:             json.RawMessage(`{"projectId":"core","buildProfileId":"` + profileID + `","targetIds":["` + targetID + `"],"jobs":8,"timeoutMs":600000}`),
			WorkspaceGeneration: generation, Timeout: 10 * time.Minute,
			Status: task.StatusQueued, CreatedAt: fixedTime, LastSequence: 1,
		},
	}
	s := authenticatedV12(t, backend)

	inspect := s.Handle(context.Background(), requestVersion(t, protocol.Version12, "workspace/inspect", map[string]any{}))
	inspectJSON, _ := json.Marshal(inspect.Payload)
	if inspect.Kind != "response" || !strings.Contains(string(inspectJSON), `"sourceUri":"file:///workspace/src/core"`) ||
		!strings.Contains(string(inspectJSON), `"buildProfileId":"`+profileID+`"`) ||
		!strings.Contains(string(inspectJSON), `"family":"gcc"`) ||
		!strings.Contains(string(inspectJSON), `"toolchainId":"gcc-test"`) ||
		!strings.Contains(string(inspectJSON), `"code":"TOOLCHAIN_NOTICE"`) ||
		strings.Contains(string(inspectJSON), "/usr/bin/gcov") {
		t.Fatalf("inspect = %#v (%s)", inspect, inspectJSON)
	}

	targets := s.Handle(context.Background(), requestVersion(t, protocol.Version12, "cmake/targets/list", map[string]any{
		"workspaceGeneration": generation, "projectId": "core", "buildProfileId": profileID,
	}))
	if targets.Kind != "response" || backend.targetsInput.WorkspaceGeneration != generation ||
		backend.targetsInput.ProjectID != "core" || backend.targetsInput.BuildProfileID != profileID {
		t.Fatalf("targets = %#v input=%#v", targets, backend.targetsInput)
	}

	start := s.Handle(context.Background(), requestVersion(t, protocol.Version12, "tasks/start", map[string]any{
		"idempotencyKey": id('2'), "kind": "cmakeBuild", "workspaceGeneration": generation,
		"projectId": "core", "buildProfileId": profileID, "targetIds": []string{targetID},
		"jobs": 8, "timeoutMs": 600000,
	}))
	startJSON, _ := json.Marshal(start.Payload)
	if start.Kind != "response" || backend.buildInput.IdempotencyKey != id('2') ||
		backend.buildInput.BuildProfileID != profileID || backend.buildInput.Jobs != 8 ||
		backend.buildInput.Timeout != 10*time.Minute || !strings.Contains(string(startJSON), `"kind":"cmakeBuild"`) {
		t.Fatalf("start = %#v input=%#v (%s)", start, backend.buildInput, startJSON)
	}
}

func TestProtocolTaskV12RejectsNonCanonicalPersistedBuildRequest(t *testing.T) {
	base := task.Task{
		ID: id('1'), Kind: task.KindCMakeBuild,
		WorkspaceGeneration: strings.Repeat("a", 64),
		Timeout:             time.Minute,
		Status:              task.StatusQueued,
		CreatedAt:           fixedTime,
	}
	for name, request := range map[string]string{
		"null target IDs":    `{"projectId":"core","buildProfileId":"` + strings.Repeat("b", 64) + `","targetIds":null,"jobs":1,"timeoutMs":60000}`,
		"mismatched timeout": `{"projectId":"core","buildProfileId":"` + strings.Repeat("b", 64) + `","targetIds":[],"jobs":1,"timeoutMs":1}`,
	} {
		t.Run(name, func(t *testing.T) {
			value := base
			value.Request = json.RawMessage(request)
			s := authenticatedV12(t, &v12Backend{getResult: value})
			result := s.Handle(context.Background(), requestVersion(t, protocol.Version12, "tasks/get", map[string]any{
				"taskId": value.ID,
			}))
			if result.Error == nil || result.Error.Code != "SERVICE_UNHEALTHY" {
				t.Fatalf("tasks/get accepted a non-canonical persisted build request: %#v", result)
			}
		})
	}
}

func TestSessionVersion12StrictlyRejectsUnsafeOrInvalidBuildFields(t *testing.T) {
	generation := strings.Repeat("a", 64)
	profileID := strings.Repeat("b", 64)
	targetID := strings.Repeat("c", 64)
	base := map[string]any{
		"idempotencyKey": id('2'), "kind": "cmakeBuild", "workspaceGeneration": generation,
		"projectId": "core", "buildProfileId": profileID, "targetIds": []string{targetID},
		"jobs": 8, "timeoutMs": 600000,
	}
	tests := map[string]any{
		"executable":           "cmake",
		"args":                 []string{"--build"},
		"env":                  map[string]string{"PATH": "unsafe"},
		"workingDirectory":     "C:/workspace",
		"presetPath":           "CMakePresets.json",
		"nativeToolOptions":    []string{"/m"},
		"unityRunnerGenerator": "C:/product/bin/unity-runner-generator.exe",
	}
	for name, value := range tests {
		t.Run(name, func(t *testing.T) {
			payload := make(map[string]any, len(base)+1)
			for key, item := range base {
				payload[key] = item
			}
			payload[name] = value
			backend := &v12Backend{}
			result := authenticatedV12(t, backend).Handle(
				context.Background(),
				requestVersion(t, protocol.Version12, "tasks/start", payload),
			)
			if result.Error == nil || result.Error.Code != "INVALID_MESSAGE" || backend.buildInput.IdempotencyKey != "" {
				t.Fatalf("%s = %#v input=%#v", name, result, backend.buildInput)
			}
		})
	}
}

func TestSessionVersion12MapsWorkspaceBuildErrors(t *testing.T) {
	tests := []struct {
		name, method, code string
		err                error
	}{
		{"workspace trust required", "cmake/targets/list", "WORKSPACE_TRUST_REQUIRED", build.ErrWorkspaceTrustRequired},
		{"workspace changed", "cmake/targets/list", "WORKSPACE_CHANGED", build.ErrWorkspaceChanged},
		{"project missing", "cmake/targets/list", "PROJECT_NOT_FOUND", build.ErrProjectNotFound},
		{"profile missing", "cmake/targets/list", "BUILD_PROFILE_NOT_FOUND", build.ErrBuildProfileNotFound},
		{"target missing", "tasks/start", "TARGET_NOT_FOUND", build.ErrTargetNotFound},
		{"configure required", "cmake/targets/list", "CONFIGURE_REQUIRED", build.ErrConfigureRequired},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			backend := &v12Backend{}
			payload := map[string]any{
				"workspaceGeneration": strings.Repeat("a", 64),
				"projectId":           "core", "buildProfileId": strings.Repeat("b", 64),
			}
			if test.method == "tasks/start" {
				payload = map[string]any{
					"idempotencyKey": id('2'), "kind": "cmakeBuild",
					"workspaceGeneration": strings.Repeat("a", 64), "projectId": "core",
					"buildProfileId": strings.Repeat("b", 64), "targetIds": []string{strings.Repeat("c", 64)},
					"jobs": 1, "timeoutMs": 1,
				}
				backend.startErr = test.err
			} else {
				backend.targetsErr = test.err
			}
			result := authenticatedV12(t, backend).Handle(
				context.Background(), requestVersion(t, protocol.Version12, test.method, payload),
			)
			if result.Error == nil || result.Error.Code != test.code {
				t.Fatalf("%s = %#v", test.name, result)
			}
		})
	}
}

func TestSessionVersion11FiltersAndHidesCMakeBuildTasks(t *testing.T) {
	buildTask := task.Task{ID: id('1'), Kind: task.KindCMakeBuild}
	backend := &v12Backend{
		getResult:    buildTask,
		cancelResult: buildTask,
		chunk: session.ArtifactChunk{
			Metadata: task.Artifact{TaskID: buildTask.ID},
		},
	}
	s := authenticatedV11(t, backend)

	list := s.Handle(context.Background(), requestVersion(t, protocol.Version11, "tasks/list", map[string]any{}))
	if list.Kind != "response" || !reflect.DeepEqual(backend.listKinds, []task.Kind{task.KindSimulation}) {
		t.Fatalf("list = %#v kinds=%v", list, backend.listKinds)
	}

	for _, test := range []struct {
		method  string
		payload map[string]any
	}{
		{"tasks/get", map[string]any{"taskId": buildTask.ID}},
		{"tasks/cancel", map[string]any{"taskId": buildTask.ID}},
		{"artifacts/list", map[string]any{"taskId": buildTask.ID}},
		{"artifacts/read", map[string]any{"artifactId": id('a'), "offset": 0, "length": 1}},
	} {
		result := s.Handle(context.Background(), requestVersion(t, protocol.Version11, test.method, test.payload))
		if result.Error == nil || result.Error.Code != "TASK_NOT_FOUND" {
			t.Fatalf("%s = %#v", test.method, result)
		}
	}
	if backend.cancelCalls != 0 {
		t.Fatalf("hidden build task was cancelled %d times", backend.cancelCalls)
	}
}

func TestSessionVersion12ErrorsRemainComparable(t *testing.T) {
	if !errors.Is(build.ErrWorkspaceChanged, build.ErrWorkspaceChanged) {
		t.Fatal("workspace error must remain a sentinel")
	}
}
