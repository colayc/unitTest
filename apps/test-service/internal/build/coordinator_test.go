package build

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"unit-test-ide.local/test-service/internal/cmake"
	"unit-test-ide.local/test-service/internal/coveragellvm"
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
	unselectedCompilerRoot := filepath.Join(t.TempDir(), "unselected", "bin")
	fixture.snapshot.Toolchains = append(fixture.snapshot.Toolchains, toolchain.Instance{
		ID:          "unselected-clang",
		Family:      toolchain.FamilyClang,
		CCompiler:   filepath.Join(unselectedCompilerRoot, "clang"),
		CXXCompiler: filepath.Join(unselectedCompilerRoot, "clang++"),
	})
	fixture.request.TargetIDs = nil
	fixture.reader.reply = cmake.FileAPIReply{}
	started, err := fixture.coordinator.Start(context.Background(), fixture.request)
	if err != nil {
		t.Fatal(err)
	}
	if started.ID == "" || len(fixture.starter.request.Plan.Steps) != 2 {
		t.Fatalf("first task = %#v, plan = %#v", started, fixture.starter.request.Plan)
	}
	for _, want := range []string{
		fixture.root.NativePath,
		fixture.installation.Root,
		filepath.Dir(fixture.toolchain.CXXCompiler),
	} {
		if !containsCanonicalBoundaryPath(fixture.reader.allowedRoots, want) {
			t.Fatalf("File API allowed roots = %#v, missing trusted root %q", fixture.reader.allowedRoots, want)
		}
	}
	if containsCanonicalBoundaryPath(fixture.reader.allowedRoots, unselectedCompilerRoot) {
		t.Fatalf(
			"generated profile File API roots included unselected toolchain root: %#v",
			fixture.reader.allowedRoots,
		)
	}
	var persistedRequest struct {
		TargetIDs []string `json:"targetIds"`
		TimeoutMS int64    `json:"timeoutMs"`
	}
	if err := json.Unmarshal(fixture.starter.request.Request, &persistedRequest); err != nil {
		t.Fatal(err)
	}
	if persistedRequest.TargetIDs == nil || len(persistedRequest.TargetIDs) != 0 ||
		persistedRequest.TimeoutMS != fixture.request.Timeout.Milliseconds() {
		t.Fatalf("persisted build request = %#v, want [] targetIds and matching timeout", persistedRequest)
	}

	reply := fixture.validReply()
	fixture.reader.reply = reply
	fingerprint := configureFingerprint(
		fixture.snapshot.Generation,
		fixture.profile,
		fixture.installation.Identity,
		fixture.toolchain.ID,
		reply,
		"", nil,
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

func TestCoordinatorPreparePlanSharesBoundaryWithoutCreatingNestedTask(
	t *testing.T,
) {
	fixture := newCoordinatorFixture(t)
	fixture.request.TargetIDs = nil
	fixture.project.Tests.Containers = []workspace.TestContainerMapping{{
		CTestName: "framework-tests",
		Framework: workspace.FrameworkCppUTest,
	}}
	fixture.snapshot.Projects[0] = fixture.project
	executable := filepath.Join(
		fixture.profile.BinaryDir,
		"framework-tests.exe",
	)
	if err := os.MkdirAll(fixture.profile.BinaryDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		executable,
		[]byte("trusted test executable"),
		0o700,
	); err != nil {
		t.Fatal(err)
	}
	target := cmake.Target{
		ID: fixture.target.ID, Name: fixture.target.Name,
		Type: "EXECUTABLE", ProjectID: fixture.project.ID,
		ProfileID: fixture.profile.ID,
		ProjectSourceDir: filepath.Join(
			fixture.root.NativePath,
			fixture.project.SourceDir,
		),
		ProjectBuildDir: fixture.profile.BinaryDir,
		Artifacts:       []string{executable},
	}
	fixture.target = target
	fixture.reader.reply = fixture.validReply()
	state, err := cmake.SnapshotTargetArtifact(
		fixture.profile,
		target,
		executable,
	)
	if err != nil {
		t.Fatal(err)
	}

	prepared, err := fixture.coordinator.PreparePlan(
		context.Background(),
		fixture.request,
	)
	if err != nil {
		t.Fatal(err)
	}
	defer prepared.ReleaseIfUnadopted()
	if fixture.starter.calls != 0 ||
		prepared.WorkspaceGeneration() != fixture.snapshot.Generation ||
		prepared.Project().ID != fixture.project.ID ||
		prepared.Profile().ID != fixture.profile.ID ||
		prepared.Toolchain().ID != fixture.toolchain.ID ||
		len(prepared.Plan().Steps) != 2 ||
		prepared.Boundary() == nil {
		t.Fatalf("prepared plan = %#v", prepared)
	}
	first := prepared.Plan()
	first.Steps[0].Process.Args[0] = "mutated"
	if prepared.Plan().Steps[0].Process.Args[0] == "mutated" {
		t.Fatal("PreparedPlan.Plan returned aliased runtime state")
	}
	project := prepared.Project()
	project.Tests.Containers[0].CTestName = "mutated"
	if prepared.Project().Tests.Containers[0].CTestName == "mutated" {
		t.Fatal("PreparedPlan.Project returned aliased runtime state")
	}
	toolchain := prepared.Toolchain()
	toolchain.Environment[0] = "MUTATED=1"
	if prepared.Toolchain().Environment[0] == "MUTATED=1" {
		t.Fatal("PreparedPlan.Toolchain returned aliased runtime state")
	}
	if err := prepared.AllowTestExecutable(state); err != nil {
		t.Fatal(err)
	}
	if err := prepared.Boundary().ValidateExecutable(executable); err != nil {
		t.Fatalf("ValidateExecutable() = %v", err)
	}
	writeErr := os.WriteFile(
		executable,
		[]byte("mutated executable"),
		0o700,
	)
	if writeErr == nil &&
		prepared.Boundary().ValidateExecutable(executable) == nil {
		t.Fatal("mutated test executable remained inside boundary")
	}
}

func TestCoordinatorPreparePlanOwnsCoverageIsolationAndRuntimeOnlyFields(t *testing.T) {
	fixture := newCoordinatorFixture(t)
	fixture.request.TargetIDs = nil
	include := filepath.Join(fixture.dataRoot(), "coverage-task", "coverage-instrumentation.cmake")
	if err := os.MkdirAll(filepath.Dir(include), 0o700); err != nil {
		t.Fatal(err)
	}
	contents := []byte("trusted instrumentation\n")
	if err := os.WriteFile(include, contents, 0o400); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(contents)
	digest := hex.EncodeToString(sum[:])
	options := &CoverageOptions{
		BinaryDir: filepath.Join(fixture.dataRoot(), "coverage-build", "identity"),
		TopLevelInclude: cmake.FingerprintFile{
			Path: include, Identity: strings.Repeat("4", 64), SHA256: digest,
		},
		InstrumentationFingerprint: strings.Repeat("4", 64),
	}
	fixture.request.Coverage = options
	encoded, err := json.Marshal(fixture.request)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "coverage") || strings.Contains(string(encoded), options.BinaryDir) {
		t.Fatalf("StartRequest JSON exposed runtime-only coverage fields: %s", encoded)
	}
	if _, err := fixture.coordinator.Start(context.Background(), fixture.request); !errors.Is(err, task.ErrInvalidArgument) {
		t.Fatalf("Start() coverage error = %v, want task.ErrInvalidArgument", err)
	}
	if fixture.starter.calls != 0 {
		t.Fatalf("normal Start created %d tasks for runtime-only coverage request", fixture.starter.calls)
	}
	if runtime.GOOS != "windows" {
		if _, err := fixture.coordinator.PreparePlan(context.Background(), fixture.request); !errors.Is(err, task.ErrInvalidArgument) {
			t.Fatalf("non-Windows PreparePlan() = %v, want unsupported invalid argument", err)
		}
		return
	}
	instance := fixture.toolchain
	instance.Family = toolchain.FamilyClangCL
	instance.Version = "20.1.8"
	toolRoot := filepath.Join(fixture.dataRoot(), "llvm", "bin")
	if err := os.MkdirAll(toolRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	instance.CCompiler = filepath.Join(toolRoot, "clang-cl.exe")
	instance.CXXCompiler = instance.CCompiler
	instance.Coverage.LLVMProfdata = filepath.Join(toolRoot, "llvm-profdata.exe")
	instance.Coverage.LLVMCov = filepath.Join(toolRoot, "llvm-cov.exe")
	for _, path := range []string{instance.CXXCompiler, instance.Coverage.LLVMProfdata, instance.Coverage.LLVMCov} {
		if err := os.WriteFile(path, []byte(path), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	instance = testCoverageToolchainEvidence(t, instance)
	fixture.toolchain = instance
	fixture.snapshot.Toolchains = []toolchain.Instance{instance}
	fixture.profile.ToolchainID = instance.ID
	fixture.snapshot.Profiles = []cmake.BuildProfile{fixture.profile}
	prepared, err := fixture.coordinator.PreparePlan(context.Background(), fixture.request)
	if err != nil {
		t.Fatal(err)
	}
	defer prepared.ReleaseIfUnadopted()
	if fixture.configurations.lastGetProfileID == fixture.profile.ID || fixture.configurations.lastGetProfileID == "" {
		t.Fatalf("coverage configuration used base storage key %q", fixture.configurations.lastGetProfileID)
	}
	if prepared.CoverageBinaryDir() != options.BinaryDir || prepared.Profile().BinaryDir != options.BinaryDir {
		t.Fatalf("coverage binary dir = %q, profile = %#v", prepared.CoverageBinaryDir(), prepared.Profile())
	}
	if prepared.CoverageBinaryDir() == fixture.profile.BinaryDir {
		t.Fatal("coverage plan reused the base profile binary directory")
	}
	if err := prepared.Boundary().ValidateExecutable(fixture.installation.Executable); err == nil {
		t.Fatal("coverage boundary became executable before retained LLVM toolset transfer")
	}

	toolset, err := coveragellvm.PinToolset(instance)
	if err != nil {
		t.Fatal(err)
	}
	if err := prepared.AttachCoverageToolset(toolset); err != nil {
		_ = toolset.Close()
		t.Fatalf("AttachCoverageToolset() = %v", err)
	}
	if err := prepared.Boundary().ValidateExecutable(fixture.installation.Executable); err != nil {
		t.Fatalf("coverage boundary rejected CMake after retained toolset transfer: %v", err)
	}
	prepared.ReleaseIfUnadopted()
	if err := toolset.Verify(); err == nil {
		t.Fatal("boundary release did not close transferred toolset")
	}
}

func TestCoverageConfigureFingerprintTracksInstrumentationAndToolIdentityOnly(t *testing.T) {
	fixture := newCoordinatorFixture(t)
	reply := fixture.validReply()
	options := &CoverageOptions{
		BinaryDir:         filepath.Join(fixture.dataRoot(), "coverage-build"),
		BinaryDirIdentity: strings.Repeat("8", 64),
		ToolsetIdentity:   strings.Repeat("9", 64),
		TopLevelInclude: cmake.FingerprintFile{
			Path:     filepath.Join(fixture.dataRoot(), "task", "coverage.cmake"),
			Identity: strings.Repeat("4", 64), SHA256: strings.Repeat("5", 64),
		},
		InstrumentationFingerprint: strings.Repeat("4", 64),
	}
	base := configureFingerprint(
		fixture.snapshot.Generation, fixture.profile, fixture.installation.Identity,
		fixture.toolchain.ID, reply, "", options,
	)
	if base == "" {
		t.Fatal("coverage configure fingerprint is empty")
	}
	mutations := []struct {
		name string
		edit func(*string, *CoverageOptions)
	}{
		{"instrumentation digest", func(_ *string, value *CoverageOptions) { value.TopLevelInclude.SHA256 = strings.Repeat("6", 64) }},
		{"template fingerprint", func(_ *string, value *CoverageOptions) {
			value.InstrumentationFingerprint = strings.Repeat("7", 64)
			value.TopLevelInclude.Identity = value.InstrumentationFingerprint
		}},
		{"tool identity", func(identity *string, _ *CoverageOptions) { *identity = "other-toolchain" }},
		{"retained compiler/profdata/cov identity", func(_ *string, value *CoverageOptions) { value.ToolsetIdentity = strings.Repeat("a", 64) }},
		{"verified coverage directory identity", func(_ *string, value *CoverageOptions) { value.BinaryDirIdentity = strings.Repeat("b", 64) }},
	}
	for _, mutation := range mutations {
		t.Run(mutation.name, func(t *testing.T) {
			identity := fixture.toolchain.ID
			changed := *options
			mutation.edit(&identity, &changed)
			got := configureFingerprint(
				fixture.snapshot.Generation, fixture.profile, fixture.installation.Identity,
				identity, reply, "", &changed,
			)
			if got == base {
				t.Fatalf("configure fingerprint did not track %s", mutation.name)
			}
		})
	}
	source := filepath.Join(fixture.root.NativePath, "project", "ordinary.cpp")
	if err := os.WriteFile(source, []byte("int first;"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, []byte("int second;"), 0o600); err != nil {
		t.Fatal(err)
	}
	stable := configureFingerprint(
		fixture.snapshot.Generation, fixture.profile, fixture.installation.Identity,
		fixture.toolchain.ID, reply, "", options,
	)
	if stable != base {
		t.Fatal("ordinary source content changed coverage configure fingerprint")
	}
}

func TestCoverageConfigurationIdentitySeparatesBaseAndCoverageStorage(t *testing.T) {
	base := strings.Repeat("1", 64)
	first := &CoverageOptions{
		BinaryDir:                  filepath.Join(t.TempDir(), "coverage-one"),
		BinaryDirIdentity:          strings.Repeat("2", 64),
		ToolsetIdentity:            strings.Repeat("3", 64),
		TopLevelInclude:            cmake.FingerprintFile{Path: filepath.Join(t.TempDir(), "coverage.cmake"), Identity: strings.Repeat("4", 64), SHA256: strings.Repeat("5", 64)},
		InstrumentationFingerprint: strings.Repeat("4", 64),
	}
	second := *first
	second.BinaryDir = filepath.Join(t.TempDir(), "coverage-two")
	second.BinaryDirIdentity = strings.Repeat("6", 64)
	firstID := configurationStorageID(base, first)
	secondID := configurationStorageID(base, &second)
	if firstID == "" || firstID == base || secondID == "" || secondID == base || firstID == secondID {
		t.Fatalf("storage identities base=%q first=%q second=%q", base, firstID, secondID)
	}
	if configurationStorageID(base, nil) != base {
		t.Fatal("ordinary build storage identity changed")
	}
}

func TestCoverageConfigurationRecordsCoexistInRealStore(t *testing.T) {
	store, err := taskstore.Open(filepath.Join(t.TempDir(), "coverage-configurations.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	baseID := strings.Repeat("1", 64)
	firstOptions := &CoverageOptions{BinaryDir: filepath.Join(t.TempDir(), "one"), BinaryDirIdentity: strings.Repeat("2", 64), ToolsetIdentity: strings.Repeat("3", 64), TopLevelInclude: cmake.FingerprintFile{Path: filepath.Join(t.TempDir(), "one.cmake"), Identity: strings.Repeat("4", 64), SHA256: strings.Repeat("5", 64)}, InstrumentationFingerprint: strings.Repeat("4", 64)}
	secondOptions := *firstOptions
	secondOptions.BinaryDir = filepath.Join(t.TempDir(), "two")
	secondOptions.BinaryDirIdentity = strings.Repeat("6", 64)
	ids := []string{baseID, configurationStorageID(baseID, firstOptions), configurationStorageID(baseID, &secondOptions)}
	for index, id := range ids {
		value := taskstore.BuildConfiguration{WorkspaceID: strings.Repeat("7", 64), ProjectID: "core", ProfileID: id, Fingerprint: strings.Repeat(string(rune('a'+index)), 64), BuildDirectory: "service/build", CMakeIdentity: strings.Repeat("d", 64), FileAPIIdentity: strings.Repeat("e", 64), ConfiguredAt: time.Date(2026, 8, 20, index, 0, 0, 0, time.UTC)}
		if err := store.PutBuildConfiguration(context.Background(), value); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range ids {
		if _, err := store.GetBuildConfiguration(context.Background(), strings.Repeat("7", 64), "core", id); err != nil {
			t.Fatalf("configuration %q did not coexist: %v", id, err)
		}
	}
}

func (f *coordinatorFixture) dataRoot() string {
	return filepath.Dir(filepath.Dir(f.profile.BinaryDir))
}

func TestPresetFileAPIRootsIncludeOnlyServiceDiscoveredToolchains(t *testing.T) {
	fixture := newCoordinatorFixture(t)
	preset := fixture.profile
	preset.ID = strings.Repeat("8", 64)
	preset.Origin = "preset"
	preset.ConfigurePreset = "unit-test-ide-debug"
	preset.BuildPreset = "unit-test-ide-debug"
	preset.ToolchainID = ""
	preset.Generator = "Ninja"
	preset.Configuration = "Debug"
	preset.BinaryDir = filepath.Join(fixture.root.NativePath, "preset-build")
	fixture.profile = preset
	fixture.snapshot.Profiles = []cmake.BuildProfile{preset}
	fixture.request.BuildProfileID = preset.ID
	fixture.request.TargetIDs = nil
	fixture.reader.reply = cmake.FileAPIReply{}

	secondRoot := filepath.Join(t.TempDir(), "verified-clang")
	secondSysroot := filepath.Join(secondRoot, "sysroot")
	fixture.snapshot.Toolchains = append(fixture.snapshot.Toolchains, toolchain.Instance{
		ID:          "verified-clang",
		Family:      toolchain.FamilyClang,
		CCompiler:   filepath.Join(secondRoot, "bin", "clang"),
		CXXCompiler: filepath.Join(secondRoot, "bin", "clang++"),
		Sysroot:     secondSysroot,
	})

	prepared, err := fixture.coordinator.PreparePlan(
		context.Background(),
		fixture.request,
	)
	if err != nil {
		t.Fatal(err)
	}
	defer prepared.ReleaseIfUnadopted()
	wantToolchainID := effectiveToolchainIdentity(
		preset,
		toolchain.Instance{},
		cmake.FileAPIReply{},
	)
	if prepared.Toolchain().ID != wantToolchainID {
		t.Fatalf(
			"preset prepared Toolchain ID = %q, want %q",
			prepared.Toolchain().ID,
			wantToolchainID,
		)
	}
	for _, want := range []string{
		filepath.Dir(fixture.toolchain.CXXCompiler),
		filepath.Join(secondRoot, "bin"),
		secondSysroot,
	} {
		if !containsCanonicalBoundaryPath(fixture.reader.allowedRoots, want) {
			t.Fatalf(
				"preset File API roots = %#v, missing verified toolchain root %q",
				fixture.reader.allowedRoots,
				want,
			)
		}
	}
}

func TestConfigureObserverWritesStateAndRejectsDisappearedOrRemappedTarget(t *testing.T) {
	fixture := newCoordinatorFixture(t)
	state, err := fixture.coordinator.configureState(
		fixture.snapshot, fixture.project, fixture.profile, fixture.toolchain,
		fixture.validReply(), []string{fixture.target.ID}, nil,
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
	value            taskstore.BuildConfiguration
	getErr           error
	putErr           error
	putCalls         int
	lastGetProfileID string
}

func (f *fakeConfigurationStore) GetBuildConfiguration(
	_ context.Context, _, _, profileID string,
) (taskstore.BuildConfiguration, error) {
	f.lastGetProfileID = profileID
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
	reply        cmake.FileAPIReply
	err          error
	allowedRoots []string
}

func (f *fakeFileAPIReader) Read(
	_ string,
	allowedRoots []string,
	_ ...cmake.BuildProfile,
) (cmake.FileAPIReply, error) {
	f.allowedRoots = append([]string(nil), allowedRoots...)
	return f.reply, f.err
}

func containsCanonicalBoundaryPath(values []string, want string) bool {
	want = canonicalPortablePathForBoundary(filepath.Clean(want))
	for _, value := range values {
		if canonicalPortablePathForBoundary(filepath.Clean(value)) == want {
			return true
		}
	}
	return false
}
