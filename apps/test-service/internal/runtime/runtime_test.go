package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	goruntime "runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"unit-test-ide.local/test-service/internal/artifactstore"
	"unit-test-ide.local/test-service/internal/build"
	"unit-test-ide.local/test-service/internal/cmake"
	"unit-test-ide.local/test-service/internal/coveragedomain"
	"unit-test-ide.local/test-service/internal/diagnostic"
	"unit-test-ide.local/test-service/internal/discovery"
	"unit-test-ide.local/test-service/internal/eventbroker"
	"unit-test-ide.local/test-service/internal/instance"
	"unit-test-ide.local/test-service/internal/probe"
	"unit-test-ide.local/test-service/internal/processcontrol"
	"unit-test-ide.local/test-service/internal/session"
	"unit-test-ide.local/test-service/internal/task"
	"unit-test-ide.local/test-service/internal/taskstore"
	"unit-test-ide.local/test-service/internal/testdomain"
	"unit-test-ide.local/test-service/internal/testrun"
	"unit-test-ide.local/test-service/internal/toolchain"
	"unit-test-ide.local/test-service/internal/workspace"
)

func TestUntrustedRuntimeAllowsNoWorkspaceMethods(t *testing.T) {
	root := t.TempDir()
	coverage := &runtimeCoverageBackend{}
	active, err := Open(Config{
		DataDir: filepath.Join(root, "data"), ServiceExecutable: os.Args[0],
		WorkspaceRoot: root, TrustedWorkspace: false, Platform: platformForTest(),
		CoverageBackend: coverage,
		dependencies:    testDependencies(&recordingRunner{}, nil),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = active.Close() })
	if _, err := active.InspectWorkspace(context.Background()); !errors.Is(err, build.ErrWorkspaceTrustRequired) {
		t.Fatalf("InspectWorkspace() error = %v, want ErrWorkspaceTrustRequired", err)
	}
	if _, err := active.StartTestDiscovery(
		context.Background(),
		session.TestDiscoveryStart{},
	); !errors.Is(err, build.ErrWorkspaceTrustRequired) {
		t.Fatalf(
			"StartTestDiscovery() error = %v, want trust error",
			err,
		)
	}
	if _, _, err := active.StartTestRun(
		context.Background(),
		session.TestRunStart{},
	); !errors.Is(err, build.ErrWorkspaceTrustRequired) {
		t.Fatalf(
			"StartTestRun() error = %v, want trust error",
			err,
		)
	}
	if _, err := active.GetCoverageRun(context.Background(), strings.Repeat("a", 32)); !errors.Is(err, build.ErrWorkspaceTrustRequired) {
		t.Fatalf("GetCoverageRun() error = %v, want trust error", err)
	}
	if _, err := active.ListCoverageRuns(context.Background(), coveragedomain.RunPageRequest{WorkspaceGeneration: strings.Repeat("b", 64)}); !errors.Is(err, build.ErrWorkspaceTrustRequired) {
		t.Fatalf("ListCoverageRuns() error = %v, want trust error", err)
	}
	if _, err := active.GetCoverageReport(context.Background(), strings.Repeat("c", 32)); !errors.Is(err, build.ErrWorkspaceTrustRequired) {
		t.Fatalf("GetCoverageReport() error = %v, want trust error", err)
	}
	if active.CoverageBackend() != nil {
		t.Fatal("untrusted runtime exposed a coverage provider")
	}
}

func TestUntrustedRuntimeConstructsNoCoverageExecutorOrNativeSideEffect(t *testing.T) {
	base := t.TempDir()
	runner := &recordingRunner{}
	deps := testDependencies(runner, nil)
	var buildCalls, testCalls, executorCalls int
	deps.newCoordinator = func(build.CoordinatorConfig) (runtimeCoordinator, error) {
		buildCalls++
		return &fakeRuntimeCoordinator{}, nil
	}
	deps.newTestCoordinator = func(testCoordinatorConfig) (runtimeTestCoordinator, io.Closer, error) {
		testCalls++
		return &fakeRuntimeTestCoordinator{}, nil, nil
	}
	deps.newCoverageExecutor = func(coverageExecutionConfig) (coverageExecutor, error) {
		executorCalls++
		return &fakeCoverageExecutor{}, nil
	}
	active, err := Open(Config{
		DataDir: filepath.Join(base, "data"), ServiceExecutable: os.Args[0],
		WorkspaceRoot: base, TrustedWorkspace: false, Platform: platformForTest(),
		dependencies: deps,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = active.Close() })
	if buildCalls != 0 || testCalls != 0 || executorCalls != 0 || active.coverageExecutor != nil || active.CoverageBackend() != nil {
		t.Fatalf("untrusted construction calls build/test/coverage=%d/%d/%d executor=%#v backend=%#v", buildCalls, testCalls, executorCalls, active.coverageExecutor, active.CoverageBackend())
	}
	if runner.prepares.Load() != 0 {
		t.Fatalf("untrusted process prepares = %d, want 0", runner.prepares.Load())
	}
	layout, err := PrepareDataDir(filepath.Join(base, "data"))
	if err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(layout.Coverage)
	if err != nil || len(entries) != 0 {
		t.Fatalf("untrusted coverage execution directory = %#v, %v", entries, err)
	}
}

func TestTrustedRuntimeConstructsCoverageExecutionAndResumesAfterBuildAndTests(t *testing.T) {
	base := t.TempDir()
	workspaceRoot := filepath.Join(base, "workspace")
	if err := os.MkdirAll(workspaceRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	var stages []string
	deps := testDependencies(&recordingRunner{}, func(stage string) { stages = append(stages, stage) })
	deps.resolveCMake = func(context.Context, probe.Runner, cmake.ResolverConfig) (cmake.Installation, error) {
		return cmake.Installation{Executable: os.Args[0], Identity: strings.Repeat("a", 64), Version: "test", Source: cmake.SourceDev}, nil
	}
	buildCoordinator := &fakeRuntimeCoordinator{}
	deps.newCoordinator = func(build.CoordinatorConfig) (runtimeCoordinator, error) {
		return buildCoordinator, nil
	}
	testCoordinator := &fakeRuntimeTestCoordinator{}
	deps.newTestCoordinator = func(testCoordinatorConfig) (runtimeTestCoordinator, io.Closer, error) {
		return testCoordinator, nil, nil
	}
	executor := &fakeCoverageExecutor{}
	var captured coverageExecutionConfig
	deps.newCoverageExecutor = func(config coverageExecutionConfig) (coverageExecutor, error) {
		captured = config
		return executor, nil
	}
	dataDir := filepath.Join(base, "data")
	active, err := Open(Config{
		DataDir: dataDir, ServiceExecutable: os.Args[0], WorkspaceRoot: workspaceRoot,
		TrustedWorkspace: true, DevCMakeExecutable: os.Args[0], Platform: platformForTest(),
		dependencies: deps,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = active.Close() })
	layout, err := PrepareDataDir(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if active.coverageExecutor != executor {
		t.Fatal("trusted runtime did not retain its coverage executor")
	}
	if _, ok := active.CoverageBackend().(*queuedCoverageBackend); !ok {
		t.Fatalf("trusted coverage backend = %T", active.CoverageBackend())
	}
	if captured.Platform != platformForTest() || captured.Tasks == nil || captured.Store == nil ||
		captured.Build == nil || captured.Tests != testCoordinator ||
		captured.WorkspaceRoot.NativePath != workspaceRoot || captured.ExecutionRoot != layout.Coverage {
		t.Fatalf("coverage execution config = %#v", captured)
	}
	wantStages := []string{
		"validate-data-dir", "lock-instance", "open-sqlite", "cleanup-process",
		"recover-interrupted", "cleanup-artifacts", "start-manager",
		"resume-queued-builds", "resume-queued-tests", "resume-queued-coverage",
	}
	if !reflect.DeepEqual(stages, wantStages) {
		t.Fatalf("trusted startup stages = %v, want %v", stages, wantStages)
	}
}

func TestTrustedRuntimeDelegatesWorkspaceMethodsToCoordinator(t *testing.T) {
	base := t.TempDir()
	workspaceRoot := filepath.Join(base, "workspace")
	if err := os.MkdirAll(workspaceRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	deps := testDependencies(&recordingRunner{}, nil)
	deps.resolveCMake = func(context.Context, probe.Runner, cmake.ResolverConfig) (cmake.Installation, error) {
		return cmake.Installation{
			Executable: os.Args[0], Identity: strings.Repeat("a", 64),
			Version: "test", Source: cmake.SourceDev,
		}, nil
	}
	fake := &fakeRuntimeCoordinator{
		snapshot: discovery.Snapshot{
			WorkspaceID: strings.Repeat("b", 64),
			Generation:  strings.Repeat("c", 64),
		},
		targets: []cmake.Target{{ID: strings.Repeat("d", 64), Name: "tests"}},
		started: task.Task{ID: strings.Repeat("e", 32), Kind: task.KindCMakeBuild},
	}
	coverage := &runtimeCoverageBackend{}
	deps.newCoordinator = func(config build.CoordinatorConfig) (runtimeCoordinator, error) {
		fake.config = config
		return fake, nil
	}
	active, err := Open(Config{
		DataDir: filepath.Join(base, "data"), ServiceExecutable: os.Args[0],
		WorkspaceRoot: workspaceRoot, TrustedWorkspace: true,
		CoverageBackend:    coverage,
		DevCMakeExecutable: os.Args[0], Platform: platformForTest(),
		dependencies: deps,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = active.Close() })
	snapshot, err := active.InspectWorkspace(context.Background())
	if err != nil || snapshot.Generation != fake.snapshot.Generation {
		t.Fatalf("InspectWorkspace() = %#v, %v", snapshot, err)
	}
	targets, err := active.ListTargets(context.Background(), build.TargetsRequest{})
	if err != nil || !reflect.DeepEqual(targets, fake.targets) {
		t.Fatalf("ListTargets() = %#v, %v", targets, err)
	}
	started, err := active.StartBuild(context.Background(), build.StartRequest{})
	if err != nil || started.ID != fake.started.ID {
		t.Fatalf("StartBuild() = %#v, %v", started, err)
	}
	if fake.config.Inspector == nil || fake.config.Tasks == nil ||
		fake.config.Configurations == nil || fake.config.Locks == nil ||
		fake.config.ServiceDataRoot == "" {
		t.Fatalf("Coordinator config = %#v", fake.config)
	}
	if active.CoverageBackend() != coverage {
		t.Fatal("trusted runtime did not retain the explicitly injected coverage provider")
	}
}

func TestTrustedRuntimeOwnsTestExecutionDefaultsAndGeneration(
	t *testing.T,
) {
	base := t.TempDir()
	workspaceRoot := filepath.Join(base, "workspace")
	if err := os.MkdirAll(workspaceRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	deps := testDependencies(&recordingRunner{}, nil)
	deps.resolveCMake = func(
		context.Context,
		probe.Runner,
		cmake.ResolverConfig,
	) (cmake.Installation, error) {
		return cmake.Installation{
			Executable: os.Args[0],
			Identity:   strings.Repeat("a", 64),
			Version:    "test",
			Source:     cmake.SourceDev,
		}, nil
	}
	generation := strings.Repeat("c", 64)
	buildCoordinator := &fakeRuntimeCoordinator{
		snapshot: discovery.Snapshot{
			WorkspaceID: strings.Repeat("b", 64),
			Generation:  generation,
		},
	}
	deps.newCoordinator = func(
		build.CoordinatorConfig,
	) (runtimeCoordinator, error) {
		return buildCoordinator, nil
	}
	tests := &fakeRuntimeTestCoordinator{
		discoveryTask: task.Task{
			ID:   strings.Repeat("d", 32),
			Kind: task.KindTestDiscovery,
		},
		runTask: task.Task{
			ID:   strings.Repeat("e", 32),
			Kind: task.KindTestRun,
		},
		run: testdomain.TestRun{
			RunID: strings.Repeat("f", 32),
		},
	}
	var composition testCoordinatorConfig
	deps.newTestCoordinator = func(
		config testCoordinatorConfig,
	) (runtimeTestCoordinator, io.Closer, error) {
		composition = config
		return tests, nil, nil
	}
	active, err := Open(Config{
		DataDir:            filepath.Join(base, "data"),
		ServiceExecutable:  os.Args[0],
		WorkspaceRoot:      workspaceRoot,
		TrustedWorkspace:   true,
		DevCMakeExecutable: os.Args[0],
		Platform:           platformForTest(),
		dependencies:       deps,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = active.Close() })
	profileID := strings.Repeat("1", 64)
	catalogRevision := strings.Repeat("2", 64)
	discovered, err := active.StartTestDiscovery(
		context.Background(),
		session.TestDiscoveryStart{
			IdempotencyKey: strings.Repeat("3", 32),
			ProjectID:      "core",
			ProfileID:      profileID,
		},
	)
	if err != nil || discovered.ID != tests.discoveryTask.ID {
		t.Fatalf("StartTestDiscovery() = %#v, %v", discovered, err)
	}
	selection := testdomain.Selection{
		Mode: testdomain.SelectionAll,
	}
	started, run, err := active.StartTestRun(
		context.Background(),
		session.TestRunStart{
			IdempotencyKey:  strings.Repeat("4", 32),
			ProjectID:       "core",
			ProfileID:       profileID,
			CatalogRevision: catalogRevision,
			Selection:       selection,
			RepeatCount:     3,
		},
	)
	if err != nil || started.ID != tests.runTask.ID ||
		run.RunID != tests.run.RunID {
		t.Fatalf(
			"StartTestRun() = %#v / %#v / %v",
			started,
			run,
			err,
		)
	}
	if tests.discoveryInput.WorkspaceGeneration != generation ||
		tests.discoveryInput.ProjectID != "core" ||
		tests.discoveryInput.BuildProfileID != profileID ||
		tests.discoveryInput.Jobs != runtimeTestJobs() ||
		tests.discoveryInput.Timeout != testTaskTimeout ||
		len(tests.discoveryInput.TargetIDs) != 0 ||
		tests.runInput.WorkspaceGeneration != generation ||
		tests.runInput.CatalogRevision != catalogRevision ||
		tests.runInput.RepeatCount != 3 ||
		tests.runInput.MaxConcurrency != runtimeTestConcurrency() ||
		!reflect.DeepEqual(tests.runInput.Selection, selection) {
		t.Fatalf(
			"test runtime inputs = %#v / %#v",
			tests.discoveryInput,
			tests.runInput,
		)
	}
	if composition.ControlRoot == "" ||
		composition.BuildDataRoot == "" ||
		composition.Build != buildCoordinator ||
		composition.Tasks == nil ||
		composition.Store == nil ||
		composition.Artifacts == nil {
		t.Fatalf("test composition = %#v", composition)
	}
}

func TestRuntimeBuildDirectoryIdentityRestrictsOwnedRoots(
	t *testing.T,
) {
	base := t.TempDir()
	workspaceRoot := filepath.Join(base, "workspace")
	serviceRoot := filepath.Join(base, "service-builds")
	for _, root := range []string{workspaceRoot, serviceRoot} {
		if err := os.MkdirAll(root, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	tests := []struct {
		name string
		path string
		want string
		ok   bool
	}{
		{
			name: "workspace build",
			path: filepath.Join(workspaceRoot, "out", "debug"),
			want: "workspace/out/debug",
			ok:   true,
		},
		{
			name: "service build",
			path: filepath.Join(serviceRoot, "root", "profile"),
			want: "service/root/profile",
			ok:   true,
		},
		{
			name: "sibling is outside",
			path: filepath.Join(base, "workspace-sibling", "build"),
		},
		{
			name: "relative path",
			path: filepath.Join("relative", "build"),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := runtimeBuildDirectoryIdentity(
				workspaceRoot,
				serviceRoot,
				test.path,
			)
			if test.ok {
				if err != nil || got != test.want {
					t.Fatalf(
						"runtimeBuildDirectoryIdentity() = %q, %v; want %q",
						got,
						err,
						test.want,
					)
				}
				return
			}
			if !errors.Is(err, task.ErrInvalidArgument) {
				t.Fatalf(
					"runtimeBuildDirectoryIdentity() error = %v",
					err,
				)
			}
		})
	}
}

func TestRuntimeTestDiscoveryRefreshesTargetsAfterBuild(
	t *testing.T,
) {
	base := t.TempDir()
	workspaceRoot := filepath.Join(base, "workspace")
	serviceRoot := filepath.Join(base, "service-builds")
	buildDirectory := filepath.Join(workspaceRoot, "out", "debug")
	for _, root := range []string{
		workspaceRoot,
		serviceRoot,
		buildDirectory,
	} {
		if err := os.MkdirAll(root, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	store, err := taskstore.Open(
		filepath.Join(base, "history.sqlite"),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	workspaceID := strings.Repeat("1", 64)
	profileID := strings.Repeat("2", 64)
	cmakeIdentity := strings.Repeat("3", 64)
	fileAPIIdentity := strings.Repeat("4", 64)
	if err := store.PutBuildConfiguration(
		context.Background(),
		taskstore.BuildConfiguration{
			WorkspaceID:     workspaceID,
			ProjectID:       "core",
			ProfileID:       profileID,
			Fingerprint:     strings.Repeat("5", 64),
			BuildDirectory:  "workspace/out/debug",
			CMakeIdentity:   cmakeIdentity,
			FileAPIIdentity: fileAPIIdentity,
			ConfiguredAt:    time.Now().UTC(),
		},
	); err != nil {
		t.Fatal(err)
	}
	freshTarget := cmake.Target{
		ID:        strings.Repeat("6", 64),
		Name:      "fresh-tests",
		Artifacts: []string{filepath.Join(buildDirectory, "tests")},
	}
	buildCoordinator := &fakeRuntimeCoordinator{
		targets: []cmake.Target{freshTarget},
	}
	factory := newTaskDiscoveryInputFactory(
		testCoordinatorConfig{
			Build:        buildCoordinator,
			Store:        store,
			Installation: cmake.Installation{Identity: cmakeIdentity},
			WorkspaceRoot: workspace.Root{
				ID:         workspaceID,
				NativePath: workspaceRoot,
			},
			BuildDataRoot: serviceRoot,
			NewID:         task.NewID,
		},
	)
	input, err := factory(
		context.Background(),
		testrun.RefreshRequest{
			TaskID:              strings.Repeat("7", 32),
			WorkspaceGeneration: strings.Repeat("8", 64),
			Project: workspace.ProjectConfig{
				ID: "core",
			},
			Profile: cmake.BuildProfile{
				ID:        profileID,
				ProjectID: "core",
				BinaryDir: buildDirectory,
			},
			Toolchain: toolchain.Instance{ID: "preset-toolchain"},
			Targets: []cmake.Target{{
				ID:   strings.Repeat("9", 64),
				Name: "stale-before-build",
			}},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(
		input.Targets,
		[]cmake.Target{freshTarget},
	) {
		t.Fatalf(
			"refreshed discovery targets = %#v",
			input.Targets,
		)
	}
	buildCoordinator.targets[0].Artifacts[0] = "mutated"
	if input.Targets[0].Artifacts[0] == "mutated" {
		t.Fatal("refreshed discovery target aliases coordinator state")
	}
}

func TestRuntimeTestDiscoveryUsesCoverageConfigurationAndPreparedTargets(
	t *testing.T,
) {
	base := t.TempDir()
	workspaceRoot := filepath.Join(base, "workspace")
	serviceRoot := filepath.Join(base, "service-builds")
	coverageRoot := filepath.Join(base, "coverage-builds")
	buildDirectory := filepath.Join(coverageRoot, "run", "build")
	for _, root := range []string{workspaceRoot, serviceRoot, coverageRoot, buildDirectory} {
		if err := os.MkdirAll(root, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	store, err := taskstore.Open(filepath.Join(base, "history.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	workspaceID := strings.Repeat("1", 64)
	profileID := strings.Repeat("2", 64)
	coverageConfigurationID := strings.Repeat("a", 64)
	cmakeIdentity := strings.Repeat("3", 64)
	fileAPIIdentity := strings.Repeat("4", 64)
	if err := store.PutBuildConfiguration(context.Background(), taskstore.BuildConfiguration{
		WorkspaceID: workspaceID, ProjectID: "core", ProfileID: coverageConfigurationID,
		Fingerprint: strings.Repeat("5", 64), BuildDirectory: "coverage/run/build",
		CMakeIdentity: cmakeIdentity, FileAPIIdentity: fileAPIIdentity,
		ConfiguredAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	preparedTarget := cmake.Target{
		ID: strings.Repeat("6", 64), Name: "coverage-tests",
		Artifacts: []string{filepath.Join(buildDirectory, "tests")},
	}
	buildCoordinator := &fakeRuntimeCoordinator{targets: []cmake.Target{{
		ID: strings.Repeat("7", 64), Name: "stale-base-target",
	}}}
	factory := newTaskDiscoveryInputFactory(testCoordinatorConfig{
		Build: buildCoordinator, Store: store,
		Installation:  cmake.Installation{Identity: cmakeIdentity},
		WorkspaceRoot: workspace.Root{ID: workspaceID, NativePath: workspaceRoot},
		BuildDataRoot: serviceRoot, CoverageRoot: coverageRoot, NewID: task.NewID,
	})
	input, err := factory(context.Background(), testrun.RefreshRequest{
		TaskID: strings.Repeat("8", 32), WorkspaceGeneration: strings.Repeat("9", 64),
		Project:         workspace.ProjectConfig{ID: "core"},
		Profile:         cmake.BuildProfile{ID: profileID, ProjectID: "core", BinaryDir: buildDirectory},
		Toolchain:       toolchain.Instance{ID: "preset-toolchain"},
		ConfigurationID: coverageConfigurationID,
		Targets:         []cmake.Target{preparedTarget},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(input.Targets, []cmake.Target{preparedTarget}) {
		t.Fatalf("coverage discovery targets = %#v", input.Targets)
	}
	if len(buildCoordinator.targetCalls) != 0 {
		t.Fatalf("coverage refresh queried base targets: %#v", buildCoordinator.targetCalls)
	}
	if input.Fingerprint.FileAPIReplyIdentity != fileAPIIdentity {
		t.Fatalf("coverage file API identity = %q", input.Fingerprint.FileAPIReplyIdentity)
	}
}

func TestRuntimeTestDiscoveryRebindsOneStableGenerationAfterBuild(
	t *testing.T,
) {
	base := t.TempDir()
	workspaceRoot := filepath.Join(base, "workspace")
	serviceRoot := filepath.Join(base, "service-builds")
	buildDirectory := filepath.Join(workspaceRoot, "out", "debug")
	for _, root := range []string{workspaceRoot, serviceRoot, buildDirectory} {
		if err := os.MkdirAll(root, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	store, err := taskstore.Open(filepath.Join(base, "history.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	workspaceID := strings.Repeat("1", 64)
	profileID := strings.Repeat("2", 64)
	cmakeIdentity := strings.Repeat("3", 64)
	fileAPIIdentity := strings.Repeat("4", 64)
	if err := store.PutBuildConfiguration(context.Background(), taskstore.BuildConfiguration{
		WorkspaceID: workspaceID, ProjectID: "core", ProfileID: profileID,
		Fingerprint: strings.Repeat("5", 64), BuildDirectory: "workspace/out/debug",
		CMakeIdentity: cmakeIdentity, FileAPIIdentity: fileAPIIdentity,
		ConfiguredAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	project := workspace.ProjectConfig{ID: "core"}
	profile := cmake.BuildProfile{ID: profileID, ProjectID: "core", BinaryDir: buildDirectory}
	target := cmake.Target{
		ID: strings.Repeat("6", 64), Name: "fresh-tests",
		Artifacts: []string{filepath.Join(buildDirectory, "tests")},
	}
	currentGeneration := strings.Repeat("a", 64)
	coordinator := &fakeRuntimeCoordinator{
		targets:    []cmake.Target{target},
		targetErrs: []error{build.ErrWorkspaceChanged, nil},
		snapshot: discovery.Snapshot{
			Generation: currentGeneration,
			Projects:   []workspace.ProjectConfig{project},
			Profiles:   []cmake.BuildProfile{profile},
			Toolchains: []toolchain.Instance{{ID: "preset-toolchain"}},
		},
	}
	factory := newTaskDiscoveryInputFactory(testCoordinatorConfig{
		Build: coordinator, Store: store,
		Installation:  cmake.Installation{Identity: cmakeIdentity},
		WorkspaceRoot: workspace.Root{ID: workspaceID, NativePath: workspaceRoot},
		BuildDataRoot: serviceRoot, NewID: task.NewID,
	})
	input, err := factory(context.Background(), testrun.RefreshRequest{
		TaskID: strings.Repeat("7", 32), WorkspaceGeneration: strings.Repeat("8", 64),
		Project: project, Profile: profile,
		Toolchain: toolchain.Instance{ID: "preset-toolchain"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if input.Fingerprint.WorkspaceGeneration != currentGeneration {
		t.Fatalf("generation = %q, want %q", input.Fingerprint.WorkspaceGeneration, currentGeneration)
	}
	if len(coordinator.targetCalls) != 2 || coordinator.targetCalls[1].WorkspaceGeneration != currentGeneration {
		t.Fatalf("target calls = %#v", coordinator.targetCalls)
	}
	if !reflect.DeepEqual(input.Targets, []cmake.Target{target}) {
		t.Fatalf("targets = %#v", input.Targets)
	}
}

func TestRuntimeTestDiscoveryRebindsAcrossTransientGenerationChurn(
	t *testing.T,
) {
	base := t.TempDir()
	workspaceRoot := filepath.Join(base, "workspace")
	serviceRoot := filepath.Join(base, "service-builds")
	buildDirectory := filepath.Join(workspaceRoot, "out", "debug")
	for _, root := range []string{workspaceRoot, serviceRoot, buildDirectory} {
		if err := os.MkdirAll(root, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	store, err := taskstore.Open(filepath.Join(base, "history.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	workspaceID := strings.Repeat("1", 64)
	profileID := strings.Repeat("2", 64)
	cmakeIdentity := strings.Repeat("3", 64)
	fileAPIIdentity := strings.Repeat("4", 64)
	if err := store.PutBuildConfiguration(context.Background(), taskstore.BuildConfiguration{
		WorkspaceID: workspaceID, ProjectID: "core", ProfileID: profileID,
		Fingerprint: strings.Repeat("5", 64), BuildDirectory: "workspace/out/debug",
		CMakeIdentity: cmakeIdentity, FileAPIIdentity: fileAPIIdentity,
		ConfiguredAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	project := workspace.ProjectConfig{ID: "core"}
	profile := cmake.BuildProfile{ID: profileID, ProjectID: "core", BinaryDir: buildDirectory}
	target := cmake.Target{
		ID: strings.Repeat("6", 64), Name: "fresh-tests",
		Artifacts: []string{filepath.Join(buildDirectory, "tests")},
	}
	currentGeneration := strings.Repeat("a", 64)
	coordinator := &fakeRuntimeCoordinator{
		targets: targetSlice(target),
		targetErrs: []error{
			build.ErrWorkspaceChanged,
			build.ErrWorkspaceChanged,
			nil,
		},
		snapshot: discovery.Snapshot{
			Generation: currentGeneration,
			Projects:   []workspace.ProjectConfig{project},
			Profiles:   []cmake.BuildProfile{profile},
			Toolchains: []toolchain.Instance{{ID: "preset-toolchain"}},
		},
	}
	factory := newTaskDiscoveryInputFactory(testCoordinatorConfig{
		Build: coordinator, Store: store,
		Installation:  cmake.Installation{Identity: cmakeIdentity},
		WorkspaceRoot: workspace.Root{ID: workspaceID, NativePath: workspaceRoot},
		BuildDataRoot: serviceRoot, NewID: task.NewID,
	})
	input, err := factory(context.Background(), testrun.RefreshRequest{
		TaskID: strings.Repeat("7", 32), WorkspaceGeneration: strings.Repeat("8", 64),
		Project: project, Profile: profile,
		Toolchain: toolchain.Instance{ID: "preset-toolchain"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if input.Fingerprint.WorkspaceGeneration != currentGeneration {
		t.Fatalf("generation = %q, want %q", input.Fingerprint.WorkspaceGeneration, currentGeneration)
	}
	if len(coordinator.targetCalls) != 3 {
		t.Fatalf("target calls = %d, want 3", len(coordinator.targetCalls))
	}
}

func targetSlice(value cmake.Target) []cmake.Target {
	return []cmake.Target{value}
}

func TestTrustedRuntimeKeepsInvalidWorkspaceAvailableForInspectorDiagnostics(t *testing.T) {
	base := t.TempDir()
	workspaceRoot := filepath.Join(base, "workspace")
	if err := os.MkdirAll(workspaceRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	deps := testDependencies(&recordingRunner{}, nil)
	deps.loadWorkspace = func(workspace.Root) (workspace.LoadResult, error) {
		return workspace.LoadResult{}, workspace.ErrInvalidConfig
	}
	deps.resolveCMake = func(context.Context, probe.Runner, cmake.ResolverConfig) (cmake.Installation, error) {
		return cmake.Installation{
			Executable: os.Args[0], Root: filepath.Dir(os.Args[0]),
			Identity: strings.Repeat("a", 64), Version: "test", Source: cmake.SourceDev,
		}, nil
	}
	var manual []workspace.ToolchainConfig
	newRegistry := deps.newRegistry
	deps.newRegistry = func(
		platform string,
		runner probe.Runner,
		configured []workspace.ToolchainConfig,
	) (*toolchain.Registry, error) {
		manual = append([]workspace.ToolchainConfig(nil), configured...)
		return newRegistry(platform, runner, configured)
	}
	fake := &fakeRuntimeCoordinator{
		snapshot: discovery.Snapshot{Diagnostics: []diagnostic.Diagnostic{{
			Source: "workspace", Severity: "error", Code: "WORKSPACE_INVALID_CONFIG",
			Message: "Workspace configuration is invalid",
		}}},
	}
	deps.newCoordinator = func(build.CoordinatorConfig) (runtimeCoordinator, error) {
		return fake, nil
	}
	active, err := Open(Config{
		DataDir: filepath.Join(base, "data"), ServiceExecutable: os.Args[0],
		WorkspaceRoot: workspaceRoot, TrustedWorkspace: true,
		DevCMakeExecutable: os.Args[0], Platform: platformForTest(),
		dependencies: deps,
	})
	if err != nil {
		t.Fatalf("Open() rejected invalid workspace before Inspector could report it: %v", err)
	}
	t.Cleanup(func() { _ = active.Close() })
	if len(manual) != 0 {
		t.Fatalf("invalid workspace supplied manual toolchains: %#v", manual)
	}
	snapshot, err := active.InspectWorkspace(context.Background())
	if err != nil || len(snapshot.Diagnostics) != 1 ||
		snapshot.Diagnostics[0].Code != "WORKSPACE_INVALID_CONFIG" {
		t.Fatalf("InspectWorkspace() = %#v, %v", snapshot, err)
	}
}

func TestTrustedRuntimeFailsInvalidQueuedBuildWithStableRecoveryCode(t *testing.T) {
	base := t.TempDir()
	workspaceRoot := filepath.Join(base, "workspace")
	if err := os.MkdirAll(workspaceRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	dataRoot := filepath.Join(base, "data")
	layout, err := PrepareDataDir(dataRoot)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 29, 7, 0, 0, 0, time.UTC)
	input := seedQueuedRuntimeBuild(t, layout.Database, now)

	deps := testDependencies(&recordingRunner{}, nil)
	deps.resolveCMake = func(context.Context, probe.Runner, cmake.ResolverConfig) (cmake.Installation, error) {
		return cmake.Installation{
			Executable: os.Args[0], Identity: strings.Repeat("a", 64),
			Version: "test", Source: cmake.SourceDev,
		}, nil
	}
	fake := &fakeRuntimeCoordinator{resumeErr: build.ErrWorkspaceChanged}
	deps.newCoordinator = func(build.CoordinatorConfig) (runtimeCoordinator, error) {
		return fake, nil
	}
	active, err := Open(Config{
		DataDir: dataRoot, ServiceExecutable: os.Args[0],
		WorkspaceRoot: workspaceRoot, TrustedWorkspace: true,
		DevCMakeExecutable: os.Args[0], Platform: platformForTest(),
		Clock: fixedRuntimeClock{at: now.Add(time.Minute)}, dependencies: deps,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = active.Close() })
	recovered, err := active.Get(context.Background(), input.ID)
	if err != nil || recovered.Status != task.StatusFinished ||
		recovered.Outcome != task.OutcomeInterrupted ||
		recovered.ErrorCode != "WORKSPACE_CHANGED" ||
		len(recovered.Steps) != 2 ||
		recovered.Steps[0].Status != task.StepSkipped ||
		recovered.Steps[1].Status != task.StepSkipped ||
		fake.resumeCalls != 1 {
		t.Fatalf("recovered queued build = %#v, err = %v, resume calls = %d", recovered, err, fake.resumeCalls)
	}
}

func TestTrustedRuntimeRevalidatesAndFailsQueuedTestDiscovery(
	t *testing.T,
) {
	base := t.TempDir()
	workspaceRoot := filepath.Join(base, "workspace")
	if err := os.MkdirAll(workspaceRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	dataRoot := filepath.Join(base, "data")
	layout, err := PrepareDataDir(dataRoot)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 31, 9, 0, 0, 0, time.UTC)
	input := seedQueuedRuntimeTestDiscovery(
		t,
		layout.Database,
		now,
	)
	deps := testDependencies(&recordingRunner{}, nil)
	deps.resolveCMake = func(
		context.Context,
		probe.Runner,
		cmake.ResolverConfig,
	) (cmake.Installation, error) {
		return cmake.Installation{
			Executable: os.Args[0],
			Identity:   strings.Repeat("a", 64),
			Version:    "test",
			Source:     cmake.SourceDev,
		}, nil
	}
	deps.newCoordinator = func(
		build.CoordinatorConfig,
	) (runtimeCoordinator, error) {
		return &fakeRuntimeCoordinator{}, nil
	}
	tests := &fakeRuntimeTestCoordinator{
		resumeErr: build.ErrWorkspaceChanged,
	}
	deps.newTestCoordinator = func(
		testCoordinatorConfig,
	) (runtimeTestCoordinator, io.Closer, error) {
		return tests, nil, nil
	}
	active, err := Open(Config{
		DataDir:            dataRoot,
		ServiceExecutable:  os.Args[0],
		WorkspaceRoot:      workspaceRoot,
		TrustedWorkspace:   true,
		DevCMakeExecutable: os.Args[0],
		Platform:           platformForTest(),
		Clock: fixedRuntimeClock{
			at: now.Add(time.Minute),
		},
		dependencies: deps,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = active.Close() })
	recovered, err := active.Get(
		context.Background(),
		input.ID,
	)
	if err != nil ||
		recovered.Status != task.StatusFinished ||
		recovered.Outcome != task.OutcomeInterrupted ||
		recovered.ErrorCode != "WORKSPACE_CHANGED" ||
		len(recovered.Steps) != 1 ||
		recovered.Steps[0].Status != task.StepSkipped ||
		tests.resumeDiscoveryCalls != 1 {
		t.Fatalf(
			"recovered queued test discovery = %#v, %v; calls=%d",
			recovered,
			err,
			tests.resumeDiscoveryCalls,
		)
	}
}

func TestTrustedRuntimeResumeFailureClosesTestResourcesAndReleasesLock(
	t *testing.T,
) {
	base := t.TempDir()
	workspaceRoot := filepath.Join(base, "workspace")
	if err := os.MkdirAll(workspaceRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	dataRoot := filepath.Join(base, "data")
	layout, err := PrepareDataDir(dataRoot)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 31, 10, 0, 0, 0, time.UTC)
	seedQueuedRuntimeTestDiscovery(t, layout.Database, now)
	deps := testDependencies(&recordingRunner{}, nil)
	deps.resolveCMake = func(
		context.Context,
		probe.Runner,
		cmake.ResolverConfig,
	) (cmake.Installation, error) {
		return cmake.Installation{
			Executable: os.Args[0],
			Identity:   strings.Repeat("c", 64),
			Version:    "test",
			Source:     cmake.SourceDev,
		}, nil
	}
	deps.newCoordinator = func(
		build.CoordinatorConfig,
	) (runtimeCoordinator, error) {
		return &fakeRuntimeCoordinator{}, nil
	}
	resumeFailure := errors.New("test resume storage failure")
	resource := &recordingCloser{}
	deps.newTestCoordinator = func(
		testCoordinatorConfig,
	) (runtimeTestCoordinator, io.Closer, error) {
		return &fakeRuntimeTestCoordinator{
			resumeErr: resumeFailure,
		}, resource, nil
	}
	active, err := Open(Config{
		DataDir:            dataRoot,
		ServiceExecutable:  os.Args[0],
		WorkspaceRoot:      workspaceRoot,
		TrustedWorkspace:   true,
		DevCMakeExecutable: os.Args[0],
		Platform:           platformForTest(),
		Clock: fixedRuntimeClock{
			at: now.Add(time.Minute),
		},
		dependencies: deps,
	})
	if active != nil || !errors.Is(err, resumeFailure) {
		t.Fatalf("Open() = %#v, %v", active, err)
	}
	if resource.closeCalls.Load() != 1 {
		t.Fatalf(
			"test resource Close calls = %d, want 1",
			resource.closeCalls.Load(),
		)
	}
	locked, err := instance.Lock(layout.Lock)
	if err != nil {
		t.Fatalf("failed Open retained instance lock: %v", err)
	}
	if err := locked.Close(); err != nil {
		t.Fatal(err)
	}
	store, err := taskstore.Open(layout.Database)
	if err != nil {
		t.Fatalf("failed Open retained SQLite store: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestUntrustedRuntimePreservesQueuedBuildForTrustedRestart(t *testing.T) {
	base := t.TempDir()
	workspaceRoot := filepath.Join(base, "workspace")
	if err := os.MkdirAll(workspaceRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	dataRoot := filepath.Join(base, "data")
	layout, err := PrepareDataDir(dataRoot)
	if err != nil {
		t.Fatal(err)
	}
	input := seedQueuedRuntimeBuild(
		t,
		layout.Database,
		time.Date(2026, 7, 29, 8, 0, 0, 0, time.UTC),
	)
	active, err := Open(Config{
		DataDir: dataRoot, ServiceExecutable: os.Args[0],
		WorkspaceRoot: workspaceRoot, TrustedWorkspace: false,
		Platform: platformForTest(), dependencies: testDependencies(&recordingRunner{}, nil),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = active.Close() })
	preserved, err := active.Get(context.Background(), input.ID)
	if err != nil || preserved.Status != task.StatusQueued ||
		preserved.Outcome != "" || preserved.ErrorCode != "" {
		t.Fatalf("preserved queued build = %#v, err = %v", preserved, err)
	}
}

func seedQueuedRuntimeBuild(t *testing.T, database string, now time.Time) task.Task {
	t.Helper()
	store, err := taskstore.Open(database)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	input := task.Task{
		ID: strings.Repeat("1", 32), IdempotencyKey: strings.Repeat("2", 32),
		RequestHash: strings.Repeat("3", 64), Kind: task.KindCMakeBuild,
		Request:             json.RawMessage(`{"projectId":"core","buildProfileId":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","targetIds":[],"jobs":1,"timeoutMs":60000}`),
		WorkspaceGeneration: strings.Repeat("4", 64),
		PlanFingerprint:     strings.Repeat("5", 64), Timeout: time.Minute,
		Status: task.StatusQueued, CreatedAt: now,
	}
	if _, _, err := store.Create(context.Background(), input, []task.StepSnapshot{
		{ID: "configure", Kind: task.StepConfigure, Status: task.StepPending},
		{ID: "build", Kind: task.StepBuild, Status: task.StepPending},
	}, task.EventDraft{
		TaskID: input.ID, Type: task.EventTaskCreated, At: now,
		Payload: json.RawMessage(`{"status":"queued"}`),
	}); err != nil {
		t.Fatal(err)
	}
	return input
}

func seedQueuedRuntimeTestDiscovery(
	t *testing.T,
	database string,
	now time.Time,
) task.Task {
	t.Helper()
	store, err := taskstore.Open(database)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	input := task.Task{
		ID:             strings.Repeat("6", 32),
		IdempotencyKey: strings.Repeat("7", 32),
		RequestHash:    strings.Repeat("8", 64),
		Kind:           task.KindTestDiscovery,
		Request: json.RawMessage(
			`{"projectId":"core","buildProfileId":"` +
				strings.Repeat("9", 64) +
				`","targetIds":[],"jobs":1,"timeoutMs":60000}`,
		),
		WorkspaceGeneration: strings.Repeat("a", 64),
		PlanFingerprint:     strings.Repeat("b", 64),
		Timeout:             time.Minute,
		Status:              task.StatusQueued,
		CreatedAt:           now,
	}
	if _, _, err := store.Create(
		context.Background(),
		input,
		[]task.StepSnapshot{{
			ID:     "build",
			Kind:   task.StepBuild,
			Status: task.StepPending,
		}},
		task.EventDraft{
			TaskID: input.ID,
			Type:   task.EventTaskCreated,
			At:     now,
			Payload: json.RawMessage(
				`{"status":"queued"}`,
			),
		},
	); err != nil {
		t.Fatal(err)
	}
	return input
}

type fakeRuntimeCoordinator struct {
	config      build.CoordinatorConfig
	snapshot    discovery.Snapshot
	targets     []cmake.Target
	targetErrs  []error
	targetCalls []build.TargetsRequest
	started     task.Task
	resumeErr   error
	resumeCalls int
}

func (f *fakeRuntimeCoordinator) Inspect(context.Context) (discovery.Snapshot, error) {
	return f.snapshot, nil
}

func (f *fakeRuntimeCoordinator) Targets(_ context.Context, request build.TargetsRequest) ([]cmake.Target, error) {
	f.targetCalls = append(f.targetCalls, request)
	if len(f.targetErrs) > 0 {
		err := f.targetErrs[0]
		f.targetErrs = f.targetErrs[1:]
		if err != nil {
			return nil, err
		}
	}
	return append([]cmake.Target(nil), f.targets...), nil
}

func (f *fakeRuntimeCoordinator) Start(context.Context, build.StartRequest) (task.Task, error) {
	return f.started, nil
}

func (f *fakeRuntimeCoordinator) PreparePlan(context.Context, build.StartRequest) (*build.PreparedPlan, error) {
	return nil, task.ErrStorageUnavailable
}

func (f *fakeRuntimeCoordinator) Resume(context.Context, task.Task) (task.Task, error) {
	f.resumeCalls++
	return task.Task{}, f.resumeErr
}

func (*fakeRuntimeCoordinator) Succeeded(context.Context, task.Task, task.ExecutionStep) error {
	return nil
}

type fakeRuntimeTestCoordinator struct {
	discoveryTask        task.Task
	runTask              task.Task
	run                  testdomain.TestRun
	discoveryInput       testrun.DiscoveryRequest
	runInput             testrun.RunRequest
	discoveryErr         error
	runErr               error
	resumeErr            error
	resumeDiscoveryCalls int
	resumeRunCalls       int
}

func (fake *fakeRuntimeTestCoordinator) StartDiscovery(
	_ context.Context,
	input testrun.DiscoveryRequest,
) (task.Task, error) {
	fake.discoveryInput = input
	return fake.discoveryTask, fake.discoveryErr
}

func (fake *fakeRuntimeTestCoordinator) StartRun(
	_ context.Context,
	input testrun.RunRequest,
) (task.Task, testdomain.TestRun, error) {
	fake.runInput = input
	return fake.runTask, fake.run, fake.runErr
}

func (fake *fakeRuntimeTestCoordinator) ResumeDiscovery(
	context.Context,
	task.Task,
) (task.Task, error) {
	fake.resumeDiscoveryCalls++
	return task.Task{}, fake.resumeErr
}

func (fake *fakeRuntimeTestCoordinator) ResumeRun(
	context.Context,
	task.Task,
) (task.Task, error) {
	fake.resumeRunCalls++
	return task.Task{}, fake.resumeErr
}

func (fake *fakeRuntimeTestCoordinator) PrepareEmbedded(
	context.Context,
	testrun.EmbeddedRequest,
) (testrun.EmbeddedRun, error) {
	return nil, task.ErrStorageUnavailable
}

type fixedRuntimeClock struct{ at time.Time }

func (c fixedRuntimeClock) Now() time.Time { return c.at }

func (fixedRuntimeClock) After(time.Duration) <-chan time.Time {
	return make(chan time.Time)
}

func TestMain(m *testing.M) {
	root, err := os.MkdirTemp(".", ".runtime-test-tmp-")
	if err != nil {
		panic(err)
	}
	_ = os.Setenv("TEMP", root)
	_ = os.Setenv("TMP", root)
	code := m.Run()
	_ = os.RemoveAll(root)
	os.Exit(code)
}

func TestOpenUsesRequiredOrderAndRecoversPersistentState(t *testing.T) {
	root := filepath.Join(t.TempDir(), "data")
	layout, err := PrepareDataDir(root)
	if err != nil {
		t.Fatal(err)
	}
	seedInterruptedTask(t, layout.Database, testLease())
	if err := os.MkdirAll(layout.Artifacts, 0o700); err != nil {
		t.Fatal(err)
	}
	temporary := filepath.Join(layout.Artifacts, ".artifact-stale.tmp")
	orphan := filepath.Join(layout.Artifacts, "orphan.json")
	for _, path := range []string{temporary, orphan} {
		if err := os.WriteFile(path, []byte("stale"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	runner := &recordingRunner{}
	var stages []string
	active, err := Open(Config{
		DataDir: root, ServiceExecutable: os.Args[0], WorkspaceRoot: filepath.Dir(root), Platform: platformForTest(),
		Clock: task.RealClock{}, NewID: task.NewID, TerminationGrace: time.Millisecond,
		dependencies: testDependencies(runner, func(stage string) { stages = append(stages, stage) }),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = active.Close() })

	wantStages := []string{"validate-data-dir", "lock-instance", "open-sqlite", "cleanup-process", "recover-interrupted", "cleanup-artifacts", "start-manager"}
	if !reflect.DeepEqual(stages, wantStages) {
		t.Fatalf("Open stages = %#v, want %#v", stages, wantStages)
	}
	if got := runner.cleanupLeases(); len(got) != 1 || got[0].TaskID != interruptedTaskID {
		t.Fatalf("Cleanup leases = %#v", got)
	}
	got, err := active.Get(context.Background(), interruptedTaskID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != task.StatusFinished || got.Outcome != task.OutcomeInterrupted {
		t.Fatalf("recovered task = %#v", got)
	}
	if len(activeLeases(t, layout.Database)) != 0 {
		t.Fatal("recovery retained an active lease")
	}
	for _, path := range []string{temporary, orphan} {
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("stale artifact %q still exists: %v", filepath.Base(path), err)
		}
	}

	subscription, err := active.Subscribe(context.Background(), 0)
	if err != nil {
		t.Fatal(err)
	}
	defer subscription.Close()
	subscription.Activate()
	foundRecovery := false
	deadline := time.After(5 * time.Second)
	for !foundRecovery {
		select {
		case event := <-subscription.Events:
			foundRecovery = event.TaskID == interruptedTaskID && event.Type == task.EventTaskFinished && strings.Contains(string(event.Payload), "interrupted")
		case err := <-subscription.Errors:
			t.Fatalf("subscription error = %v", err)
		case <-deadline:
			t.Fatal("recovery event was not replayed")
		}
	}
}

func TestOpenCleansQueuedPreparedLeaseBeforeInterruptedRecovery(t *testing.T) {
	root := filepath.Join(t.TempDir(), "data")
	layout, err := PrepareDataDir(root)
	if err != nil {
		t.Fatal(err)
	}
	seedQueuedPreparedTask(t, layout.Database, testLease())
	runner := &recordingRunner{}
	active, err := Open(Config{
		DataDir:           root,
		ServiceExecutable: os.Args[0],
		WorkspaceRoot:     filepath.Dir(root),
		Platform:          platformForTest(),
		Clock:             task.RealClock{},
		NewID:             task.NewID,
		TerminationGrace:  time.Millisecond,
		dependencies:      testDependencies(runner, nil),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer active.Close()
	cleaned := runner.cleanupLeases()
	if len(cleaned) != 1 || cleaned[0].TaskID != interruptedTaskID {
		t.Fatalf("queued prepared cleanup leases = %#v", cleaned)
	}
	got, err := active.Get(context.Background(), interruptedTaskID)
	if err != nil ||
		got.Status != task.StatusFinished ||
		got.Outcome != task.OutcomeInterrupted {
		t.Fatalf("recovered queued prepared task = %#v, %v", got, err)
	}
	if leases := activeLeases(t, layout.Database); len(leases) != 0 {
		t.Fatalf("recovery retained leases %#v", leases)
	}
}

func TestOpenIgnoresLeaseIdentityMismatchButStillRecovers(t *testing.T) {
	root := filepath.Join(t.TempDir(), "data")
	layout, err := PrepareDataDir(root)
	if err != nil {
		t.Fatal(err)
	}
	seedInterruptedTask(t, layout.Database, testLease())
	runner := &recordingRunner{cleanupErr: processcontrol.ErrLeaseIdentityMismatch}
	active, err := Open(Config{
		DataDir: root, ServiceExecutable: os.Args[0], WorkspaceRoot: filepath.Dir(root), Platform: platformForTest(),
		TerminationGrace: time.Millisecond, dependencies: testDependencies(runner, nil),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer active.Close()
	got, err := active.Get(context.Background(), interruptedTaskID)
	if err != nil || got.Outcome != task.OutcomeInterrupted {
		t.Fatalf("recovered task = %#v, %v", got, err)
	}
	if len(runner.cleanupLeases()) != 1 {
		t.Fatal("mismatched lease was not checked exactly once")
	}
}

func TestOpenRejectsSecondRuntimeAndCloseReleasesInstanceLock(t *testing.T) {
	root := filepath.Join(t.TempDir(), "data")
	config := Config{DataDir: root, ServiceExecutable: os.Args[0], WorkspaceRoot: filepath.Dir(root), Platform: platformForTest(), dependencies: testDependencies(&recordingRunner{}, nil)}
	first, err := Open(config)
	if err != nil {
		t.Fatal(err)
	}
	if second, err := Open(config); !errors.Is(err, instance.ErrAlreadyRunning) || second != nil {
		t.Fatalf("second Open() = %#v, %v", second, err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(config)
	if err != nil {
		t.Fatalf("Open after Close error = %v", err)
	}
	if err := reopened.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestOpenFailureReleasesLockAndClosesOpenedStores(t *testing.T) {
	root := filepath.Join(t.TempDir(), "data")
	deps := testDependencies(&recordingRunner{}, nil)
	managerFailure := errors.New("manager construction failed")
	var openedBroker *eventbroker.Broker
	deps.newBroker = func(source eventbroker.Source, queueSize, pageSize int) (*eventbroker.Broker, error) {
		var err error
		openedBroker, err = eventbroker.New(source, queueSize, pageSize)
		return openedBroker, err
	}
	deps.newManager = func(task.ManagerConfig) (runtimeManager, error) { return nil, managerFailure }
	if active, err := Open(Config{DataDir: root, ServiceExecutable: os.Args[0], WorkspaceRoot: filepath.Dir(root), Platform: platformForTest(), dependencies: deps}); !errors.Is(err, managerFailure) || active != nil {
		t.Fatalf("Open() = %#v, %v", active, err)
	}
	layout, err := PrepareDataDir(root)
	if err != nil {
		t.Fatal(err)
	}
	locked, err := instance.Lock(layout.Lock)
	if err != nil {
		t.Fatalf("lock leaked after failed Open: %v", err)
	}
	if err := locked.Close(); err != nil {
		t.Fatal(err)
	}
	store, err := taskstore.Open(layout.Database)
	if err != nil {
		t.Fatalf("store unusable after failed Open: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if subscription, err := openedBroker.Subscribe(context.Background(), 0); !errors.Is(err, eventbroker.ErrBrokerClosed) || subscription != nil {
		t.Fatalf("broker leaked after failed Open: %#v, %v", subscription, err)
	}
}

func TestTrustedOpenCoordinatorFailureClosesPartialInitializationInReverseOrder(t *testing.T) {
	base := t.TempDir()
	workspaceRoot := filepath.Join(base, "workspace")
	if err := os.MkdirAll(workspaceRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	deps := testDependencies(&recordingRunner{}, nil)
	var closed []string
	track := func(name string) { closed = append(closed, name) }

	prepareDataDir := deps.prepareDataDir
	deps.prepareDataDir = func(path string) (Layout, io.Closer, error) {
		layout, guard, err := prepareDataDir(path)
		if err != nil {
			return Layout{}, nil, err
		}
		return layout, &trackedCloser{Closer: guard, name: "guard", track: track}, nil
	}
	lockInstance := deps.lockInstance
	deps.lockInstance = func(path string) (io.Closer, error) {
		lock, err := lockInstance(path)
		if err != nil {
			return nil, err
		}
		return &trackedCloser{Closer: lock, name: "lock", track: track}, nil
	}
	openStore := deps.openStore
	deps.openStore = func(path string) (runtimeStore, error) {
		store, err := openStore(path)
		if err != nil {
			return nil, err
		}
		return &trackedRuntimeStore{runtimeStore: store, track: track}, nil
	}
	openArtifacts := deps.openArtifacts
	deps.openArtifacts = func(path string) (runtimeArtifacts, error) {
		artifacts, err := openArtifacts(path)
		if err != nil {
			return nil, err
		}
		return &trackedRuntimeArtifacts{runtimeArtifacts: artifacts, track: track}, nil
	}
	newManager := deps.newManager
	deps.newManager = func(config task.ManagerConfig) (runtimeManager, error) {
		manager, err := newManager(config)
		if err != nil {
			return nil, err
		}
		return &trackedRuntimeManager{runtimeManager: manager, track: track}, nil
	}
	deps.resolveCMake = func(context.Context, probe.Runner, cmake.ResolverConfig) (cmake.Installation, error) {
		return cmake.Installation{
			Executable: os.Args[0], Identity: strings.Repeat("a", 64),
			Version: "test", Source: cmake.SourceDev,
		}, nil
	}
	coordinatorFailure := errors.New("coordinator construction failed")
	deps.newCoordinator = func(build.CoordinatorConfig) (runtimeCoordinator, error) {
		return nil, coordinatorFailure
	}

	active, err := Open(Config{
		DataDir: filepath.Join(base, "data"), ServiceExecutable: os.Args[0],
		WorkspaceRoot: workspaceRoot, TrustedWorkspace: true,
		DevCMakeExecutable: os.Args[0], Platform: platformForTest(),
		dependencies: deps,
	})
	if !errors.Is(err, coordinatorFailure) || active != nil {
		t.Fatalf("Open() = %#v, %v", active, err)
	}
	want := []string{"manager", "artifacts", "store", "lock", "guard"}
	if !reflect.DeepEqual(closed, want) {
		t.Fatalf("close order = %v, want %v", closed, want)
	}
}

func TestRuntimeArtifactBackendHidesPathsAndVerifiesContent(t *testing.T) {
	root := filepath.Join(t.TempDir(), "data")
	layout, err := PrepareDataDir(root)
	if err != nil {
		t.Fatal(err)
	}
	artifact := seedFinishedArtifact(t, layout)
	active, err := Open(Config{DataDir: root, ServiceExecutable: os.Args[0], WorkspaceRoot: filepath.Dir(root), Platform: platformForTest(), dependencies: testDependencies(&recordingRunner{}, nil)})
	if err != nil {
		t.Fatal(err)
	}
	defer active.Close()
	page, err := active.ListArtifacts(context.Background(), artifact.TaskID, "", 10)
	if err != nil || len(page.Items) != 1 {
		t.Fatalf("ListArtifacts() = %#v, %v", page, err)
	}
	if page.Items[0].RelativePath != "" {
		t.Fatalf("ListArtifacts leaked relative path %q", page.Items[0].RelativePath)
	}
	chunk, err := active.ReadArtifact(context.Background(), artifact.ID, 0, artifactstore.MaxReadChunk)
	if err != nil {
		t.Fatal(err)
	}
	if chunk.Metadata.RelativePath != "" || !chunk.EOF || !strings.Contains(string(chunk.Data), `"outcome":"succeeded"`) {
		t.Fatalf("ReadArtifact() = %#v", chunk)
	}
	if _, err := active.ReadArtifact(context.Background(), strings.Repeat("f", 32), 0, 1); !errors.Is(err, task.ErrNotFound) {
		t.Fatalf("unknown artifact error = %v", err)
	}
}

func TestShutdownReleasesOwnershipWhenManagerCannotQuiesce(t *testing.T) {
	root := filepath.Join(t.TempDir(), "data")
	process := newBlockingProcess()
	cleanupStarted := make(chan struct{})
	cleanupReturned := make(chan struct{})
	allowCleanup := make(chan struct{})
	var startedOnce, returnedOnce, releaseOnce sync.Once
	runner := &recordingRunner{prepared: process, cleanup: func(ctx context.Context, _ task.ProcessLease) error {
		startedOnce.Do(func() { close(cleanupStarted) })
		defer returnedOnce.Do(func() { close(cleanupReturned) })
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-allowCleanup:
			releaseOnce.Do(func() { close(process.releaseTerminate) })
			process.complete(processcontrol.Result{Err: context.Canceled})
			return nil
		}
	}}
	active, err := Open(Config{DataDir: root, ServiceExecutable: os.Args[0], WorkspaceRoot: filepath.Dir(root), Platform: platformForTest(), TerminationGrace: 250 * time.Millisecond, dependencies: testDependencies(runner, nil)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := active.StartSimulation(context.Background(), task.SimulationStart{
		IdempotencyKey: "shutdown-active",
		Scenario:       task.ScenarioHang,
		Timeout:        time.Minute,
	}); err != nil {
		t.Fatal(err)
	}
	subscription, err := active.Subscribe(context.Background(), 0)
	if err != nil {
		t.Fatal(err)
	}
	subscription.Activate()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	shutdownReturned := make(chan error, 1)
	go func() { shutdownReturned <- active.Shutdown(ctx) }()
	select {
	case <-cleanupStarted:
	case <-time.After(5 * time.Second):
		close(allowCleanup)
		t.Fatal("Shutdown did not begin forced lease cleanup")
	}
	select {
	case <-cleanupReturned:
	case <-time.After(5 * time.Second):
		close(allowCleanup)
		t.Fatal("Shutdown left forced cleanup running after caller cancellation")
	}
	select {
	case err := <-shutdownReturned:
		if !errors.Is(err, context.Canceled) || !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("Shutdown() error = %v, want caller cancellation and bounded cleanup deadline", err)
		}
		if repeated := active.Close(); repeated != err {
			t.Fatalf("repeated Close() error = %v, want canonical %v", repeated, err)
		}
	case <-time.After(5 * time.Second):
		close(allowCleanup)
		t.Fatal("Shutdown did not return after caller cancellation")
	}
	layout, err := PrepareDataDir(root)
	if err != nil {
		t.Fatal(err)
	}
	locked, err := instance.Lock(layout.Lock)
	if err != nil {
		t.Fatalf("Shutdown retained ownership after manager failure: %v", err)
	}
	_ = locked.Close()
	close(allowCleanup)
	assertClosed(t, subscription.Events)
	assertClosed(t, subscription.Errors)
	cleaned := runner.cleanupLeases()
	if len(cleaned) != 1 {
		t.Fatalf("forced cleanup leases = %#v", cleaned)
	}
	for _, lease := range cleaned {
		if lease.HostStartIdentity != "blocking-process" {
			t.Fatalf("forced cleanup lease = %#v", lease)
		}
	}
}

func TestShutdownProcessCloseFailureReleasesInstanceLockAndFreezesError(t *testing.T) {
	root := filepath.Join(t.TempDir(), "data")
	process := newRetryCloseProcess()
	runner := &recordingRunner{prepared: process}
	active, err := Open(Config{
		DataDir: root, ServiceExecutable: os.Args[0], WorkspaceRoot: filepath.Dir(root), Platform: platformForTest(),
		TerminationGrace: time.Millisecond, dependencies: testDependencies(runner, nil),
	})
	if err != nil {
		t.Fatal(err)
	}
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(process.releaseClose) }) }
	t.Cleanup(func() { release(); _ = active.Close() })
	started, err := active.StartSimulation(context.Background(), task.SimulationStart{
		IdempotencyKey: "retry-close-runtime",
		Scenario:       task.ScenarioSuccess,
		Timeout:        time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	process.complete(processcontrol.Result{ExitCode: 0})
	deadline := time.Now().Add(time.Second)
	for process.closeCalls.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if process.closeCalls.Load() != 1 {
		t.Fatalf("initial process Close calls = %d, want 1", process.closeCalls.Load())
	}
	beforeRetry, err := active.Get(context.Background(), started.ID)
	if err != nil ||
		beforeRetry.Status != task.StatusRunning ||
		beforeRetry.Outcome != "" ||
		beforeRetry.Steps[0].Status != task.StepRunning {
		t.Fatalf("Task before Close retry = %#v, %v", beforeRetry, err)
	}
	layout, err := PrepareDataDir(root)
	if err != nil {
		t.Fatal(err)
	}
	leases := activeLeases(t, layout.Database)
	if len(leases) != 1 || leases[0].TaskID != started.ID {
		t.Fatalf("leases before Close retry = %#v", leases)
	}

	short, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	shutdownErr := active.Shutdown(short)
	if !errors.Is(shutdownErr, context.DeadlineExceeded) {
		t.Fatalf("Shutdown after transient Close failure = %v, want deadline", shutdownErr)
	}
	locked, err := instance.Lock(layout.Lock)
	if err != nil {
		t.Fatalf("failed Shutdown retained instance lock: %v", err)
	}
	_ = locked.Close()

	callsAfterShutdown := process.closeCalls.Load()
	release()
	long, cancelLong := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancelLong()
	if err := active.Shutdown(long); err != shutdownErr {
		t.Fatalf("repeated Shutdown error = %v, want canonical %v", err, shutdownErr)
	}
	if calls := process.closeCalls.Load(); calls != callsAfterShutdown {
		t.Fatalf("repeated Shutdown retried process Close: before=%d after=%d", callsAfterShutdown, calls)
	}
	locked, err = instance.Lock(layout.Lock)
	if err != nil {
		t.Fatalf("repeated Shutdown retained instance lock: %v", err)
	}
	_ = locked.Close()
}

func TestManagedProcessCloseStopsForwardersWhenUnderlyingChannelsRemainOpen(t *testing.T) {
	process := newBlockingProcess()
	managed := newManagedProcess(process)
	received := make(chan struct{})
	go func() {
		process.output <- processcontrol.Output{Stream: processcontrol.StreamStdout, Data: []byte("pending")}
		close(received)
	}()
	select {
	case <-received:
	case <-time.After(time.Second):
		t.Fatal("output forwarder did not receive the underlying value")
	}
	if err := managed.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	assertChannelClosedSoon(t, managed.Output())
	assertChannelClosedSoon(t, managed.Done())
}

func TestManagedProcessConcurrentCloseBoundsUnderlyingCloseAndStopsOutput(t *testing.T) {
	process := newContextBlockingProcess()
	managed := newManagedProcess(process)
	producerDone := make(chan struct{})
	go func() {
		defer close(producerDone)
		for {
			select {
			case <-process.closed:
				return
			case process.output <- processcontrol.Output{Stream: processcontrol.StreamStdout, Data: []byte("continuous")}:
			}
		}
	}()

	const callers = 8
	errs := make(chan error, callers)
	for range callers {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
			defer cancel()
			errs <- managed.Close(ctx)
		}()
	}
	for range callers {
		if err := <-errs; !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("Close error = %v, want deadline", err)
		}
	}
	select {
	case <-producerDone:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("continuous output producer remained blocked after Close")
	}
	assertChannelClosedSoon(t, managed.Output())
	assertChannelClosedSoon(t, managed.Done())
	if calls := process.closeCalls.Load(); calls != 1 {
		t.Fatalf("underlying Close calls = %d, want 1", calls)
	}
	close(process.releaseClose)
	retry, cancelRetry := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancelRetry()
	if err := managed.Close(retry); err != nil {
		t.Fatalf("retry Close = %v", err)
	}
	if calls := process.closeCalls.Load(); calls != 2 {
		t.Fatalf("underlying Close calls after retry = %d, want 2", calls)
	}
}

const interruptedTaskID = "11111111111111111111111111111111"

func seedQueuedPreparedTask(t *testing.T, database string, lease task.ProcessLease) {
	t.Helper()
	store, err := taskstore.Open(database)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Date(2026, 7, 27, 15, 0, 0, 0, time.UTC)
	created := task.Task{
		ID:             interruptedTaskID,
		IdempotencyKey: "queued-prepared-recovery-key",
		RequestHash:    strings.Repeat("c", 64),
		Kind:           task.KindSimulation,
		Request:        json.RawMessage(`{"scenario":"hang"}`),
		Scenario:       task.ScenarioHang,
		Timeout:        time.Minute,
		Status:         task.StatusQueued,
		CreatedAt:      now,
	}
	created, _, err = store.Create(
		context.Background(),
		created,
		nil,
		task.EventDraft{
			TaskID:  created.ID,
			Type:    task.EventTaskCreated,
			At:      now,
			Payload: []byte(`{"status":"queued"}`),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	lease.TaskID = created.ID
	if _, _, err := store.Apply(context.Background(), task.Mutation{
		Task:     created,
		Expected: task.StatusQueued,
		PutLease: &lease,
	}); err != nil {
		t.Fatal(err)
	}
}

func seedInterruptedTask(t *testing.T, database string, lease task.ProcessLease) {
	t.Helper()
	store, err := taskstore.Open(database)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Date(2026, 7, 23, 1, 2, 3, 0, time.UTC)
	created := task.Task{
		ID: interruptedTaskID, IdempotencyKey: "recovery-key", RequestHash: strings.Repeat("a", 64),
		Kind: task.KindSimulation, Request: json.RawMessage(`{"scenario":"hang"}`),
		Scenario: task.ScenarioHang, Timeout: time.Minute, Status: task.StatusQueued, CreatedAt: now,
	}
	created, _, err = store.Create(context.Background(), created, nil, task.EventDraft{TaskID: created.ID, Type: task.EventTaskCreated, At: now, Payload: []byte(`{"status":"queued"}`)})
	if err != nil {
		t.Fatal(err)
	}
	running, err := task.ApplyTransition(created, task.Transition{From: task.StatusQueued, To: task.StatusRunning, At: now.Add(time.Second)})
	if err != nil {
		t.Fatal(err)
	}
	lease.TaskID = created.ID
	_, _, err = store.Apply(context.Background(), task.Mutation{Task: running, Expected: task.StatusQueued, PutLease: &lease, Events: []task.EventDraft{{TaskID: created.ID, Type: task.EventTaskStarted, At: now.Add(time.Second), Payload: []byte(`{"status":"running"}`)}}})
	if err != nil {
		t.Fatal(err)
	}
}

func activeLeases(t *testing.T, database string) []task.ProcessLease {
	t.Helper()
	store, err := taskstore.Open(database)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	leases, err := store.ActiveLeases(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return leases
}

func testLease() task.ProcessLease {
	return task.ProcessLease{HostPID: os.Getpid(), HostStartIdentity: "mismatch", ServiceInstanceID: "old-service"}
}

type recordingRunner struct {
	mu         sync.Mutex
	cleaned    []task.ProcessLease
	cleanupErr error
	cleanup    func(context.Context, task.ProcessLease) error
	prepared   processcontrol.Process
	prepares   atomic.Int32
}

func (r *recordingRunner) Prepare(context.Context, processcontrol.Spec, string, string) (processcontrol.Process, error) {
	r.prepares.Add(1)
	if r.prepared != nil {
		return r.prepared, nil
	}
	return nil, errors.New("unexpected process start")
}

func (r *recordingRunner) Cleanup(ctx context.Context, lease task.ProcessLease, _ time.Duration) error {
	r.mu.Lock()
	r.cleaned = append(r.cleaned, lease)
	cleanup, cleanupErr := r.cleanup, r.cleanupErr
	r.mu.Unlock()
	if cleanup != nil {
		cleanupErr = errors.Join(cleanupErr, cleanup(ctx, lease))
	}
	return cleanupErr
}

func (r *recordingRunner) cleanupLeases() []task.ProcessLease {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]task.ProcessLease(nil), r.cleaned...)
}

func platformForTest() string { return goruntime.GOOS }

func testDependencies(runner processcontrol.Runner, stage func(string)) *dependencies {
	value := defaultDependencies()
	value.newRunner = func(string) processcontrol.Runner { return runner }
	value.newTestCoordinator = func(
		testCoordinatorConfig,
	) (runtimeTestCoordinator, io.Closer, error) {
		return &fakeRuntimeTestCoordinator{}, nil, nil
	}
	if stage != nil {
		prepare := value.prepareDataDir
		value.prepareDataDir = func(path string) (Layout, io.Closer, error) {
			stage("validate-data-dir")
			return prepare(path)
		}
		lockInstance := value.lockInstance
		value.lockInstance = func(path string) (io.Closer, error) {
			stage("lock-instance")
			return lockInstance(path)
		}
		openStore := value.openStore
		value.openStore = func(path string) (runtimeStore, error) {
			stage("open-sqlite")
			opened, err := openStore(path)
			if err != nil {
				return nil, err
			}
			return &stageStore{runtimeStore: opened, stage: stage}, nil
		}
		openArtifacts := value.openArtifacts
		value.openArtifacts = func(path string) (runtimeArtifacts, error) {
			opened, err := openArtifacts(path)
			if err != nil {
				return nil, err
			}
			return &stageArtifacts{runtimeArtifacts: opened, stage: stage}, nil
		}
		newManager := value.newManager
		value.newManager = func(config task.ManagerConfig) (runtimeManager, error) {
			stage("start-manager")
			return newManager(config)
		}
	}
	return &value
}

type stageStore struct {
	runtimeStore
	stage func(string)
}

func (s *stageStore) List(ctx context.Context, cursor string, limit int, kinds ...task.Kind) (task.Page[task.Task], error) {
	switch {
	case reflect.DeepEqual(kinds, []task.Kind{task.KindCMakeBuild}):
		s.stage("resume-queued-builds")
	case reflect.DeepEqual(kinds, []task.Kind{task.KindTestDiscovery, task.KindTestRun}):
		s.stage("resume-queued-tests")
	case reflect.DeepEqual(kinds, []task.Kind{task.KindCoverageRun}):
		s.stage("resume-queued-coverage")
	}
	return s.runtimeStore.List(ctx, cursor, limit, kinds...)
}

func (s *stageStore) ActiveLeases(ctx context.Context) ([]task.ProcessLease, error) {
	s.stage("cleanup-process")
	return s.runtimeStore.ActiveLeases(ctx)
}

func (s *stageStore) RecoverInterrupted(ctx context.Context, at time.Time) ([]task.Event, error) {
	s.stage("recover-interrupted")
	return s.runtimeStore.RecoverInterrupted(ctx, at)
}

type stageArtifacts struct {
	runtimeArtifacts
	stage func(string)
}

func (s *stageArtifacts) Cleanup(ctx context.Context, referenced map[string]struct{}) error {
	s.stage("cleanup-artifacts")
	return s.runtimeArtifacts.Cleanup(ctx, referenced)
}

type trackedCloser struct {
	io.Closer
	name  string
	track func(string)
}

func (c *trackedCloser) Close() error {
	c.track(c.name)
	return c.Closer.Close()
}

type recordingCloser struct {
	closeCalls atomic.Int32
}

func (closer *recordingCloser) Close() error {
	closer.closeCalls.Add(1)
	return nil
}

type runtimeCoverageBackend struct{}

func (*runtimeCoverageBackend) StartCoverageRun(context.Context, session.CoverageRunStart) (task.Task, coveragedomain.Run, testdomain.TestRun, error) {
	return task.Task{}, coveragedomain.Run{}, testdomain.TestRun{}, task.ErrStorageUnavailable
}

func (*runtimeCoverageBackend) GetCoverageRun(context.Context, string) (coveragedomain.Run, error) {
	return coveragedomain.Run{}, task.ErrStorageUnavailable
}

func (*runtimeCoverageBackend) ListCoverageRuns(context.Context, coveragedomain.RunPageRequest) (coveragedomain.RunPage, error) {
	return coveragedomain.RunPage{}, task.ErrStorageUnavailable
}

func (*runtimeCoverageBackend) GetCoverageReport(context.Context, string) (coveragedomain.Report, error) {
	return coveragedomain.Report{}, task.ErrStorageUnavailable
}

type trackedRuntimeStore struct {
	runtimeStore
	track func(string)
}

func (s *trackedRuntimeStore) Close() error {
	s.track("store")
	return s.runtimeStore.Close()
}

type trackedRuntimeArtifacts struct {
	runtimeArtifacts
	track func(string)
}

func (a *trackedRuntimeArtifacts) Close() error {
	a.track("artifacts")
	return a.runtimeArtifacts.Close()
}

type trackedRuntimeManager struct {
	runtimeManager
	track func(string)
}

func (m *trackedRuntimeManager) Shutdown(ctx context.Context) error {
	m.track("manager")
	return m.runtimeManager.Shutdown(ctx)
}

func seedFinishedArtifact(t *testing.T, layout Layout) task.Artifact {
	t.Helper()
	store, err := taskstore.Open(layout.Database)
	if err != nil {
		t.Fatal(err)
	}
	artifacts, err := artifactstore.New(layout.Artifacts)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 23, 2, 3, 4, 0, time.UTC)
	input := task.Task{
		ID: strings.Repeat("2", 32), IdempotencyKey: "finished-artifact", RequestHash: strings.Repeat("b", 64),
		Kind: task.KindSimulation, Request: json.RawMessage(`{"scenario":"success"}`),
		Scenario: task.ScenarioSuccess, Timeout: time.Minute, Status: task.StatusQueued, CreatedAt: now,
	}
	input, _, err = store.Create(context.Background(), input, nil, task.EventDraft{TaskID: input.ID, Type: task.EventTaskCreated, At: now, Payload: []byte(`{"status":"queued"}`)})
	if err != nil {
		t.Fatal(err)
	}
	finished, err := task.ApplyTransition(input, task.Transition{From: task.StatusQueued, To: task.StatusFinished, Outcome: task.OutcomeSucceeded, At: now.Add(time.Second)})
	if err != nil {
		t.Fatal(err)
	}
	artifact, err := artifacts.CommitJSON(context.Background(), input.ID, strings.Repeat("3", 32), now.Add(time.Second), map[string]string{"outcome": "succeeded"})
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = store.Apply(context.Background(), task.Mutation{Task: finished, Expected: task.StatusQueued, Artifacts: []task.Artifact{artifact}, Events: []task.EventDraft{{TaskID: input.ID, Type: task.EventTaskFinished, At: now.Add(time.Second), Payload: []byte(`{"outcome":"succeeded"}`)}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := artifacts.Close(); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	return artifact
}

type blockingProcess struct {
	output           chan processcontrol.Output
	done             chan processcontrol.Result
	releaseTerminate chan struct{}
	once             sync.Once
}

func newBlockingProcess() *blockingProcess {
	return &blockingProcess{output: make(chan processcontrol.Output), done: make(chan processcontrol.Result, 1), releaseTerminate: make(chan struct{})}
}

func (p *blockingProcess) Lease() task.ProcessLease {
	return task.ProcessLease{HostPID: 4242, HostStartIdentity: "blocking-process", TargetProcessGroup: 4243, ServiceInstanceID: "runtime"}
}
func (p *blockingProcess) Start(context.Context) error          { return nil }
func (p *blockingProcess) Output() <-chan processcontrol.Output { return p.output }
func (p *blockingProcess) Done() <-chan processcontrol.Result   { return p.done }
func (p *blockingProcess) Terminate(context.Context, time.Duration) error {
	<-p.releaseTerminate
	return nil
}
func (p *blockingProcess) Close(context.Context) error { return nil }
func (p *blockingProcess) complete(result processcontrol.Result) {
	p.once.Do(func() { close(p.output); p.done <- result; close(p.done) })
}

type contextBlockingProcess struct {
	output       chan processcontrol.Output
	done         chan processcontrol.Result
	closed       chan struct{}
	releaseClose chan struct{}
	closeOnce    sync.Once
	closeCalls   atomic.Int32
}

type retryCloseProcess struct {
	output       chan processcontrol.Output
	done         chan processcontrol.Result
	releaseClose chan struct{}
	completeOnce sync.Once
	closeCalls   atomic.Int32
}

func newRetryCloseProcess() *retryCloseProcess {
	return &retryCloseProcess{
		output: make(chan processcontrol.Output), done: make(chan processcontrol.Result, 1), releaseClose: make(chan struct{}),
	}
}

func (*retryCloseProcess) Lease() task.ProcessLease {
	return task.ProcessLease{HostPID: 5252, HostStartIdentity: "retry-close-process", TargetProcessGroup: 5253, ServiceInstanceID: "runtime"}
}
func (*retryCloseProcess) Start(context.Context) error                    { return nil }
func (p *retryCloseProcess) Output() <-chan processcontrol.Output         { return p.output }
func (p *retryCloseProcess) Done() <-chan processcontrol.Result           { return p.done }
func (*retryCloseProcess) Terminate(context.Context, time.Duration) error { return nil }
func (p *retryCloseProcess) Close(ctx context.Context) error {
	if p.closeCalls.Add(1) == 1 {
		return errors.New("injected transient process close failure")
	}
	select {
	case <-p.releaseClose:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (p *retryCloseProcess) complete(result processcontrol.Result) {
	p.completeOnce.Do(func() { close(p.output); p.done <- result; close(p.done) })
}

func newContextBlockingProcess() *contextBlockingProcess {
	return &contextBlockingProcess{
		output: make(chan processcontrol.Output), done: make(chan processcontrol.Result), closed: make(chan struct{}), releaseClose: make(chan struct{}),
	}
}

func (*contextBlockingProcess) Lease() task.ProcessLease                       { return task.ProcessLease{} }
func (*contextBlockingProcess) Start(context.Context) error                    { return nil }
func (p *contextBlockingProcess) Output() <-chan processcontrol.Output         { return p.output }
func (p *contextBlockingProcess) Done() <-chan processcontrol.Result           { return p.done }
func (*contextBlockingProcess) Terminate(context.Context, time.Duration) error { return nil }
func (p *contextBlockingProcess) Close(ctx context.Context) error {
	p.closeCalls.Add(1)
	p.closeOnce.Do(func() { close(p.closed) })
	select {
	case <-p.releaseClose:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func assertClosed[T any](t *testing.T, values <-chan T) {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case _, ok := <-values:
			if !ok {
				return
			}
		case <-deadline:
			t.Fatal("channel did not close")
		}
	}
}

func assertChannelClosedSoon[T any](t *testing.T, values <-chan T) {
	t.Helper()
	deadline := time.After(200 * time.Millisecond)
	for {
		select {
		case _, ok := <-values:
			if !ok {
				return
			}
		case <-deadline:
			t.Fatal("channel did not close after adapter Close")
		}
	}
}
