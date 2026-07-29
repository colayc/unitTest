package build

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"unit-test-ide.local/test-service/internal/cmake"
	"unit-test-ide.local/test-service/internal/discovery"
	"unit-test-ide.local/test-service/internal/task"
	"unit-test-ide.local/test-service/internal/taskstore"
	"unit-test-ide.local/test-service/internal/toolchain"
	"unit-test-ide.local/test-service/internal/workspace"
)

func TestCoordinatorRejectsStaleAndUnknownWorkspaceReferencesBeforeTaskCreation(t *testing.T) {
	fixture := newCoordinatorFixture(t)
	tests := []struct {
		name   string
		change func(*StartRequest)
		want   error
	}{
		{"generation", func(value *StartRequest) { value.WorkspaceGeneration = strings.Repeat("9", 64) }, ErrWorkspaceChanged},
		{"project", func(value *StartRequest) { value.ProjectID = "missing" }, ErrProjectNotFound},
		{"profile", func(value *StartRequest) { value.BuildProfileID = strings.Repeat("9", 64) }, ErrBuildProfileNotFound},
		{"target", func(value *StartRequest) { value.TargetIDs = []string{strings.Repeat("9", 64)} }, ErrTargetNotFound},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := fixture.request
			test.change(&request)
			_, err := fixture.coordinator.Start(context.Background(), request)
			if !errors.Is(err, test.want) {
				t.Fatalf("Start() error = %v, want %v", err, test.want)
			}
		})
	}
	if fixture.starter.calls != 0 {
		t.Fatalf("task starter calls = %d, want 0", fixture.starter.calls)
	}
}

func TestCoordinatorPlansConfigureThenSkipsItForUnchangedSuccessfulState(t *testing.T) {
	fixture := newCoordinatorFixture(t)
	fixture.request.TargetIDs = nil
	fixture.reader.reply = cmake.FileAPIReply{}
	started, err := fixture.coordinator.Start(context.Background(), fixture.request)
	if err != nil {
		t.Fatal(err)
	}
	if started.ID == "" || len(fixture.starter.request.Plan.Steps) != 2 {
		t.Fatalf("first task = %#v, plan = %#v", started, fixture.starter.request.Plan)
	}

	reply := fixture.validReply()
	fixture.reader.reply = reply
	fingerprint := configureFingerprint(
		fixture.snapshot.Generation,
		fixture.profile,
		fixture.installation.Identity,
		fixture.toolchain.ID,
		reply,
	)
	fixture.configurations.value = taskstore.BuildConfiguration{
		WorkspaceID: fixture.root.ID, ProjectID: fixture.project.ID,
		ProfileID: fixture.profile.ID, Fingerprint: fingerprint,
		BuildDirectory: "build/profile", CMakeIdentity: fixture.installation.Identity,
		FileAPIIdentity: fileAPIIdentity(reply), ConfiguredAt: time.Now().UTC(),
	}
	fixture.starter.request = task.StartRequest{}
	if _, err := fixture.coordinator.Start(context.Background(), fixture.request); err != nil {
		t.Fatal(err)
	}
	if len(fixture.starter.request.Plan.Steps) != 1 ||
		fixture.starter.request.Plan.Steps[0].Kind != task.StepBuild {
		t.Fatalf("unchanged plan = %#v, want build only", fixture.starter.request.Plan)
	}

	fixture.snapshot.Generation = strings.Repeat("8", 64)
	fixture.request.WorkspaceGeneration = fixture.snapshot.Generation
	if _, err := fixture.coordinator.Start(context.Background(), fixture.request); err != nil {
		t.Fatal(err)
	}
	if len(fixture.starter.request.Plan.Steps) != 2 {
		t.Fatalf("changed generation plan = %#v, want configure + build", fixture.starter.request.Plan)
	}
}

func TestConfigureObserverWritesStateAndRejectsDisappearedOrRemappedTarget(t *testing.T) {
	fixture := newCoordinatorFixture(t)
	state, err := fixture.coordinator.configureState(
		fixture.snapshot, fixture.project, fixture.profile, fixture.toolchain,
		fixture.validReply(), []string{fixture.target.ID},
	)
	if err != nil {
		t.Fatal(err)
	}
	step := task.ExecutionStep{ID: "configure", Kind: task.StepConfigure, State: state}
	fixture.reader.reply = fixture.validReply()
	if err := fixture.coordinator.Succeeded(context.Background(), task.Task{}, step); err != nil {
		t.Fatal(err)
	}
	if fixture.configurations.putCalls != 1 {
		t.Fatalf("configuration writes = %d, want 1", fixture.configurations.putCalls)
	}

	fixture.reader.reply.Targets = nil
	if err := fixture.coordinator.Succeeded(context.Background(), task.Task{}, step); !errors.Is(err, ErrTargetNotFound) {
		t.Fatalf("Succeeded() missing target error = %v", err)
	}
	fixture.reader.reply = fixture.validReply()
	fixture.reader.reply.Targets[0].Name = "renamed-target"
	if err := fixture.coordinator.Succeeded(context.Background(), task.Task{}, step); !errors.Is(err, ErrTargetNotFound) {
		t.Fatalf("Succeeded() remapped target error = %v", err)
	}
	fixture.reader.reply = fixture.validReply()
	fixture.configurations.putErr = task.ErrStorageUnavailable
	if err := fixture.coordinator.Succeeded(context.Background(), task.Task{}, step); !errors.Is(err, task.ErrStorageUnavailable) {
		t.Fatalf("Succeeded() configuration write error = %v", err)
	}
}

func TestCoordinatorRevalidatesAndResumesPersistedQueuedBuild(t *testing.T) {
	fixture := newCoordinatorFixture(t)
	requestJSON, err := json.Marshal(map[string]any{
		"projectId": fixture.project.ID, "buildProfileId": fixture.profile.ID,
		"targetIds": []string{fixture.target.ID}, "jobs": 4, "timeoutMs": 60_000,
	})
	if err != nil {
		t.Fatal(err)
	}
	persisted := task.Task{
		ID: strings.Repeat("7", 32), IdempotencyKey: fixture.request.IdempotencyKey,
		RequestHash: strings.Repeat("6", 64), Kind: task.KindCMakeBuild,
		Request: requestJSON, WorkspaceGeneration: fixture.snapshot.Generation,
		PlanFingerprint: strings.Repeat("5", 64), Timeout: time.Minute,
		Status: task.StatusQueued, CreatedAt: time.Now().UTC(),
	}
	resumed, err := fixture.coordinator.Resume(context.Background(), persisted)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if boundary, ok := fixture.starter.resumeRequest.Boundary.(task.ManagedExecutionBoundary); ok {
			_ = boundary.Release()
		}
	})
	if resumed.ID != persisted.ID || fixture.starter.resumeCalls != 1 ||
		fixture.starter.calls != 0 ||
		fixture.starter.resumeRequest.Task.ID != persisted.ID ||
		len(fixture.starter.resumeRequest.Plan.Steps) != 2 {
		t.Fatalf("resumed = %#v, starter = %#v", resumed, fixture.starter)
	}

	persisted.WorkspaceGeneration = strings.Repeat("9", 64)
	if _, err := fixture.coordinator.Resume(context.Background(), persisted); !errors.Is(err, ErrWorkspaceChanged) {
		t.Fatalf("stale Resume() error = %v, want ErrWorkspaceChanged", err)
	}
	if fixture.starter.resumeCalls != 1 {
		t.Fatalf("stale queued build reached Manager: %d calls", fixture.starter.resumeCalls)
	}
}

type coordinatorFixture struct {
	coordinator    *Coordinator
	inspector      *fakeBuildInspector
	starter        *fakeTaskStarter
	configurations *fakeConfigurationStore
	reader         *fakeFileAPIReader
	root           workspace.Root
	project        workspace.ProjectConfig
	profile        cmake.BuildProfile
	toolchain      toolchain.Instance
	installation   cmake.Installation
	target         cmake.Target
	snapshot       discovery.Snapshot
	request        StartRequest
}

func newCoordinatorFixture(t *testing.T) *coordinatorFixture {
	t.Helper()
	planner := newPlannerFixture(t)
	profile := planner.profile
	project := planner.project
	target := cmake.Target{ID: planner.targetID, Name: "unit-tests"}
	snapshot := discovery.Snapshot{
		WorkspaceID: planner.root.ID, WorkspaceURI: planner.root.URI,
		Generation: planner.generation, Projects: []workspace.ProjectConfig{project},
		Profiles: []cmake.BuildProfile{profile}, Toolchains: []toolchain.Instance{planner.toolchain},
	}
	inspector := &fakeBuildInspector{snapshot: &snapshot}
	starter := &fakeTaskStarter{}
	configurations := &fakeConfigurationStore{}
	reader := &fakeFileAPIReader{}
	coordinator, err := newCoordinator(CoordinatorConfig{
		Inspector: inspector, Tasks: starter, Configurations: configurations,
		Installation: planner.installation, WorkspaceRoot: planner.root,
		ServiceDataRoot: planner.dataRoot, Locks: NewDirectoryLocks(),
	}, coordinatorDependencies{
		readReply:  reader.Read,
		writeQuery: func(string) error { return nil },
		now:        func() time.Time { return time.Date(2026, 7, 29, 2, 0, 0, 0, time.UTC) },
	})
	if err != nil {
		t.Fatal(err)
	}
	fixture := &coordinatorFixture{
		coordinator: coordinator, inspector: inspector, starter: starter,
		configurations: configurations, reader: reader,
		root: planner.root, project: project, profile: profile,
		toolchain: planner.toolchain, installation: planner.installation,
		target: target, snapshot: snapshot,
	}
	inspector.fixture = fixture
	fixture.request = StartRequest{
		IdempotencyKey:      strings.Repeat("1", 32),
		WorkspaceGeneration: snapshot.Generation, ProjectID: project.ID,
		BuildProfileID: profile.ID, TargetIDs: []string{target.ID},
		Jobs: 4, Timeout: time.Minute,
	}
	reader.reply = fixture.validReply()
	configurations.value = taskstore.BuildConfiguration{
		WorkspaceID: planner.root.ID, ProjectID: project.ID, ProfileID: profile.ID,
		Fingerprint: strings.Repeat("0", 64), BuildDirectory: "build/profile",
		CMakeIdentity: planner.installation.Identity, FileAPIIdentity: strings.Repeat("0", 64),
		ConfiguredAt: time.Now().UTC(),
	}
	return fixture
}

func (f *coordinatorFixture) validReply() cmake.FileAPIReply {
	cache := testFingerprintFile(filepath.Join(f.profile.BinaryDir, "CMakeCache.txt"), "cache", "1")
	return cmake.FileAPIReply{
		Targets:      []cmake.Target{f.target},
		ToolchainIDs: []string{f.toolchain.ID},
		CMakeInputStates: []cmake.FingerprintFile{
			testFingerprintFile(filepath.Join(f.root.NativePath, "project", "CMakeLists.txt"), "input", "2"),
		},
		Cache: cache,
		StateFiles: []cmake.FingerprintFile{
			testFingerprintFile(filepath.Join(f.profile.BinaryDir, "index.json"), "reply", "3"),
		},
	}
}

func testFingerprintFile(path, identity, digit string) cmake.FingerprintFile {
	return cmake.FingerprintFile{Path: path, Identity: identity, SHA256: strings.Repeat(digit, 64)}
}

type fakeBuildInspector struct {
	snapshot *discovery.Snapshot
	fixture  *coordinatorFixture
}

func (f *fakeBuildInspector) Inspect(context.Context) (discovery.Snapshot, error) {
	if f.fixture != nil {
		*f.snapshot = f.fixture.snapshot
	}
	return *f.snapshot, nil
}

type fakeTaskStarter struct {
	calls         int
	request       task.StartRequest
	resumeCalls   int
	resumeRequest task.ResumeRequest
}

func (f *fakeTaskStarter) Start(_ context.Context, request task.StartRequest) (task.Task, error) {
	f.calls++
	f.request = request
	return task.Task{ID: strings.Repeat("2", 32), Kind: task.KindCMakeBuild}, nil
}

func (f *fakeTaskStarter) ResumeQueued(
	_ context.Context,
	request task.ResumeRequest,
) (task.Task, error) {
	f.resumeCalls++
	f.resumeRequest = request
	if boundary, ok := request.Boundary.(task.ManagedExecutionBoundary); ok {
		boundary.Adopt(request.Task.ID)
	}
	return request.Task, nil
}

type fakeConfigurationStore struct {
	value    taskstore.BuildConfiguration
	getErr   error
	putErr   error
	putCalls int
}

func (f *fakeConfigurationStore) GetBuildConfiguration(
	context.Context, string, string, string,
) (taskstore.BuildConfiguration, error) {
	if f.getErr != nil {
		return taskstore.BuildConfiguration{}, f.getErr
	}
	if f.value.ProfileID == "" {
		return taskstore.BuildConfiguration{}, task.ErrNotFound
	}
	return f.value, nil
}

func (f *fakeConfigurationStore) PutBuildConfiguration(
	_ context.Context, value taskstore.BuildConfiguration,
) error {
	f.putCalls++
	if f.putErr != nil {
		return f.putErr
	}
	f.value = value
	return nil
}

type fakeFileAPIReader struct {
	reply cmake.FileAPIReply
	err   error
}

func (f *fakeFileAPIReader) Read(string, []string, ...cmake.BuildProfile) (cmake.FileAPIReply, error) {
	return f.reply, f.err
}
