package runtime

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"unit-test-ide.local/test-service/internal/artifactstore"
	"unit-test-ide.local/test-service/internal/build"
	"unit-test-ide.local/test-service/internal/cmake"
	"unit-test-ide.local/test-service/internal/coveragecoord"
	"unit-test-ide.local/test-service/internal/coveragedomain"
	"unit-test-ide.local/test-service/internal/coverageexec"
	"unit-test-ide.local/test-service/internal/probe"
	"unit-test-ide.local/test-service/internal/session"
	"unit-test-ide.local/test-service/internal/task"
	"unit-test-ide.local/test-service/internal/taskstore"
	"unit-test-ide.local/test-service/internal/testdomain"
	"unit-test-ide.local/test-service/internal/toolchain"
	"unit-test-ide.local/test-service/internal/workspace"
)

type recordingCoveragePlanPreparer struct {
	prepared *build.PreparedPlan
	request  build.StartRequest
}

func (preparer *recordingCoveragePlanPreparer) PreparePlan(_ context.Context, request build.StartRequest) (*build.PreparedPlan, error) {
	preparer.request = request
	return preparer.prepared, nil
}

func TestCoverageBuildPreparerPreservesCurrentPreparedPlanCapability(t *testing.T) {
	want := &build.PreparedPlan{}
	delegate := &recordingCoveragePlanPreparer{prepared: want}
	request := build.StartRequest{WorkspaceGeneration: "generation", ProjectID: "core", BuildProfileID: "debug"}
	got, err := (coverageBuildPreparer{delegate: delegate}).PreparePlan(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if got != want || !reflect.DeepEqual(delegate.request, request) {
		t.Fatalf("typed build adapter returned %#v with request %#v", got, delegate.request)
	}
}

type recordingExecutionCoordinator struct {
	resumeCalls      []string
	unsupportedCalls []string
	closeCalls       int
	result           task.Task
}

func (coordinator *recordingExecutionCoordinator) Resume(_ context.Context, persisted task.Task) (task.Task, error) {
	coordinator.resumeCalls = append(coordinator.resumeCalls, persisted.ID)
	return coordinator.result, nil
}

func (coordinator *recordingExecutionCoordinator) FinishUnsupported(_ context.Context, persisted task.Task) (task.Task, error) {
	coordinator.unsupportedCalls = append(coordinator.unsupportedCalls, persisted.ID)
	return coordinator.result, nil
}

func (coordinator *recordingExecutionCoordinator) Close() error {
	coordinator.closeCalls++
	return nil
}

func TestPlatformCoverageExecutorUsesNativeResumeOnlyOnWindows(t *testing.T) {
	persisted := task.Task{ID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Kind: task.KindCoverageRun, Status: task.StatusQueued}
	for _, test := range []struct {
		name            string
		native          bool
		wantResume      int
		wantUnsupported int
	}{
		{name: "windows", native: true, wantResume: 1},
		{name: "linux explicit unsupported", native: false, wantUnsupported: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			coordinator := &recordingExecutionCoordinator{result: task.Task{ID: persisted.ID, Status: task.StatusFinished}}
			executor := &platformCoverageExecutor{coordinator: coordinator, native: test.native}
			got, err := executor.Resume(context.Background(), persisted)
			if err != nil || got.ID != persisted.ID {
				t.Fatalf("Resume() = %#v, %v", got, err)
			}
			if len(coordinator.resumeCalls) != test.wantResume || len(coordinator.unsupportedCalls) != test.wantUnsupported {
				t.Fatalf("native/unsupported calls = %v/%v", coordinator.resumeCalls, coordinator.unsupportedCalls)
			}
		})
	}
}

type orderedCoverageStore struct {
	runtimeStore
	items []task.Task
}

func (store *orderedCoverageStore) List(_ context.Context, cursor string, _ int, kinds ...task.Kind) (task.Page[task.Task], error) {
	if cursor != "" || !reflect.DeepEqual(kinds, []task.Kind{task.KindCoverageRun}) {
		return task.Page[task.Task]{}, task.ErrInvalidArgument
	}
	return task.Page[task.Task]{Items: append([]task.Task(nil), store.items...)}, nil
}

func TestResumeQueuedCoverageUsesCreatedTimeThenIDAndSkipsRecoveredRunning(t *testing.T) {
	created := time.Date(2026, 8, 20, 3, 4, 5, 0, time.UTC)
	firstID := "11111111111111111111111111111111"
	secondID := "22222222222222222222222222222222"
	thirdID := "33333333333333333333333333333333"
	store := &orderedCoverageStore{items: []task.Task{
		{ID: thirdID, Kind: task.KindCoverageRun, Status: task.StatusQueued, CreatedAt: created.Add(time.Second)},
		{ID: secondID, Kind: task.KindCoverageRun, Status: task.StatusQueued, CreatedAt: created},
		{ID: "55555555555555555555555555555555", Kind: task.KindCoverageRun, Status: task.StatusRunning, CreatedAt: created.Add(-2 * time.Second)},
		{ID: "44444444444444444444444444444444", Kind: task.KindCoverageRun, Status: task.StatusFinished, CreatedAt: created.Add(-time.Second)},
		{ID: firstID, Kind: task.KindCoverageRun, Status: task.StatusQueued, CreatedAt: created},
	}}
	executor := &fakeCoverageExecutor{}
	if err := resumeQueuedCoverage(context.Background(), store, executor); err != nil {
		t.Fatal(err)
	}
	if want := []string{firstID, secondID, thirdID}; !reflect.DeepEqual(executor.resumed, want) {
		t.Fatalf("coverage resume order = %v, want %v", executor.resumed, want)
	}
}

type delegatingCoverageExecutor struct {
	delegate coverageExecutor
	mu       sync.Mutex
	resumed  []string
}

func (executor *delegatingCoverageExecutor) Resume(ctx context.Context, persisted task.Task) (task.Task, error) {
	executor.mu.Lock()
	executor.resumed = append(executor.resumed, persisted.ID)
	executor.mu.Unlock()
	return executor.delegate.Resume(ctx, persisted)
}

func (executor *delegatingCoverageExecutor) FinishUnsupported(ctx context.Context, persisted task.Task) (task.Task, error) {
	return executor.delegate.FinishUnsupported(ctx, persisted)
}

func (executor *delegatingCoverageExecutor) Close() error { return executor.delegate.Close() }

func (executor *delegatingCoverageExecutor) resumeIDs() []string {
	executor.mu.Lock()
	defer executor.mu.Unlock()
	return append([]string(nil), executor.resumed...)
}

func TestOpenRealSQLiteRecoversRunningCoverageAndDefaultCoordinatorResumesQueuedInOrder(t *testing.T) {
	base := t.TempDir()
	workspaceRoot := filepath.Join(base, "workspace")
	dataDir := filepath.Join(base, "data")
	if err := os.MkdirAll(workspaceRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	layout, err := PrepareDataDir(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	store, err := taskstore.Open(layout.Database)
	if err != nil {
		t.Fatal(err)
	}
	created := time.Date(2026, 8, 20, 4, 5, 6, 0, time.UTC)
	selection := publishRuntimeRecoveryCatalog(t, store, layout.Artifacts, created.Add(-time.Hour))
	first := persistCoverageForRuntimeRecovery(t, store, selection, created, '1', 'a', '5')
	second := persistCoverageForRuntimeRecovery(t, store, selection, created, '2', 'b', '6')
	third := persistCoverageForRuntimeRecovery(t, store, selection, created.Add(time.Second), '3', 'c', '7')
	running := persistCoverageForRuntimeRecovery(t, store, selection, created.Add(-time.Minute), '4', 'd', '8')
	startedAt := running.Task.CreatedAt.Add(time.Second)
	runningTask, err := task.ApplyTransition(running.Task, task.Transition{
		From: task.StatusQueued, To: task.StatusRunning, At: startedAt,
	})
	if err != nil {
		t.Fatal(err)
	}
	runningTask.ActiveStep = running.Steps[0].ID
	runningStep := running.Steps[0]
	runningStep.Status = task.StepRunning
	runningStep.StartedAt = &startedAt
	if _, _, err := store.Apply(context.Background(), task.Mutation{
		Task: runningTask, Expected: task.StatusQueued,
		Steps:  []task.StepMutation{{Step: runningStep, Expected: task.StepPending}},
		Events: []task.EventDraft{{TaskID: runningTask.ID, Type: task.EventTaskStarted, At: startedAt, Payload: []byte(`{"status":"running"}`)}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.StartRun(context.Background(), running.TestRun.RunID, startedAt); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	database, err := sql.Open("sqlite", layout.Database)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`UPDATE coverage_runs SET status='running', started_at=? WHERE coverage_run_id=?`, startedAt.UTC().Format("2006-01-02T15:04:05.000000000Z"), running.Run.ID); err != nil {
		_ = database.Close()
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}

	deps := testDependencies(&recordingRunner{}, nil)
	deps.resolveCMake = func(context.Context, probe.Runner, cmake.ResolverConfig) (cmake.Installation, error) {
		return cmake.Installation{Executable: os.Args[0], Identity: stringsOf('e', 64), Version: "test", Source: cmake.SourceDev}, nil
	}
	deps.newCoordinator = func(build.CoordinatorConfig) (runtimeCoordinator, error) {
		return &fakeRuntimeCoordinator{}, nil
	}
	deps.newTestCoordinator = func(testCoordinatorConfig) (runtimeTestCoordinator, io.Closer, error) {
		return &fakeRuntimeTestCoordinator{}, nil, nil
	}
	var constructedRealCoordinator bool
	var executor *delegatingCoverageExecutor
	deps.newCoverageExecutor = func(config coverageExecutionConfig) (coverageExecutor, error) {
		production, err := newRuntimeCoverageExecutor(config)
		if err != nil {
			return nil, err
		}
		platform, ok := production.(*platformCoverageExecutor)
		if ok {
			_, constructedRealCoordinator = platform.coordinator.(*coverageexec.Coordinator)
		}
		executor = &delegatingCoverageExecutor{delegate: production}
		return executor, nil
	}
	active, err := Open(Config{
		DataDir: dataDir, ServiceExecutable: os.Args[0], WorkspaceRoot: workspaceRoot,
		TrustedWorkspace: true, DevCMakeExecutable: os.Args[0], Platform: platformForTest(),
		dependencies: deps,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = active.Close() })
	if !constructedRealCoordinator {
		t.Fatal("default runtime coverage constructor did not retain a real coverageexec.Coordinator")
	}
	wantResumed := []string{first.Task.ID, second.Task.ID, third.Task.ID}
	if got := executor.resumeIDs(); !reflect.DeepEqual(got, wantResumed) {
		t.Fatalf("default coordinator resume order = %v, want %v", got, wantResumed)
	}
	recoveredTask, taskErr := active.store.Get(context.Background(), running.Task.ID)
	recoveredRun, runErr := active.store.GetCoverageRun(context.Background(), running.Run.ID)
	recoveredTest, testErr := active.store.GetRunForTask(context.Background(), running.Task.ID)
	artifacts, artifactErr := active.store.ListArtifacts(context.Background(), running.Task.ID, "", 10)
	if taskErr != nil || runErr != nil || testErr != nil || artifactErr != nil ||
		recoveredTask.Status != task.StatusFinished || recoveredTask.Outcome != task.OutcomeInterrupted ||
		recoveredRun.Status != coveragedomain.StatusFinished || recoveredRun.Outcome != coveragedomain.OutcomeUnavailable ||
		recoveredRun.Reason != coveragedomain.ReasonServiceRestarted || recoveredRun.ReportID != "" ||
		recoveredTest.Status != testdomain.RunCompleted || recoveredTest.Outcome != testdomain.RunInterrupted ||
		len(artifacts.Items) != 0 {
		t.Fatalf("recovered running aggregate: task=%#v/%v coverage=%#v/%v test=%#v/%v artifacts=%#v/%v",
			recoveredTask, taskErr, recoveredRun, runErr, recoveredTest, testErr, artifacts.Items, artifactErr)
	}
}

func publishRuntimeRecoveryCatalog(t *testing.T, store *taskstore.Store, artifactRoot string, generatedAt time.Time) testdomain.SelectionSnapshot {
	t.Helper()
	owner := task.Task{
		ID: stringsOf('0', 32), IdempotencyKey: stringsOf('0', 32), RequestHash: stringsOf('0', 64),
		Kind: task.KindSimulation, Request: []byte(`{"scenario":"success"}`), Scenario: task.ScenarioSuccess,
		Timeout: time.Minute, Status: task.StatusQueued, CreatedAt: generatedAt,
	}
	if _, _, err := store.Create(context.Background(), owner, nil, task.EventDraft{
		TaskID: owner.ID, Type: task.EventTaskCreated, At: generatedAt, Payload: []byte(`{"status":"queued"}`),
	}); err != nil {
		t.Fatal(err)
	}
	containerID, err := testdomain.ContainerID("core", "coverage.tests")
	if err != nil {
		t.Fatal(err)
	}
	itemID, err := testdomain.CaseID(testdomain.CaseIdentity{
		ProjectID: "core", CTestName: "coverage.tests", Framework: testdomain.FrameworkCppUTest, Name: "recovers",
	})
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := testdomain.NewCatalog(testdomain.Catalog{
		ProjectID: "core", ProfileID: stringsOf('e', 64), Revision: stringsOf('f', 64), GeneratedAt: generatedAt,
		Containers: []testdomain.Container{{
			ID: containerID, ProjectID: "core", CTestLogicalName: "coverage.tests", DisplayName: "Coverage Tests",
			Framework: testdomain.FrameworkCppUTest, Labels: []string{},
		}},
		Items: []testdomain.Item{{
			ID: itemID, ContainerID: containerID, Kind: testdomain.ItemCase, Framework: testdomain.FrameworkCppUTest,
			LogicalName: "recovers", DisplayName: "recovers", Labels: []string{},
		}},
		Diagnostics: []testdomain.Diagnostic{},
	})
	if err != nil {
		t.Fatal(err)
	}
	artifacts, err := artifactstore.New(artifactRoot)
	if err != nil {
		t.Fatal(err)
	}
	artifact, err := artifacts.CommitTestCatalog(context.Background(), owner.ID, stringsOf('0', 31)+"1", generatedAt, catalog)
	if err == nil {
		err = store.PublishCatalog(context.Background(), catalog, artifact)
	}
	closeErr := artifacts.Close()
	if err != nil || closeErr != nil {
		t.Fatalf("publish recovery Catalog: %v; close artifacts: %v", err, closeErr)
	}
	return testdomain.SelectionSnapshot{Mode: testdomain.SelectionAll, ContainerIDs: []testdomain.ID{}, ItemIDs: []testdomain.ID{itemID}}
}

func persistCoverageForRuntimeRecovery(t *testing.T, store *taskstore.Store, selection testdomain.SelectionSnapshot, created time.Time, taskDigit, testDigit, keyDigit byte) coveragecoord.QueuedAggregate {
	t.Helper()
	family := toolchain.FamilyGCC
	if platformForTest() == "windows" {
		family = toolchain.FamilyClangCL
	}
	toolchainSnapshot, err := coverageToolchainSnapshot(toolchain.Instance{
		ID: "retained-toolchain", Family: family, Version: "18.1.8", TargetArchitecture: "amd64",
	}, platformForTest())
	if err != nil {
		t.Fatal(err)
	}
	ids := []string{stringsOf(taskDigit, 32), stringsOf(testDigit, 32)}
	aggregate, err := coveragecoord.NewQueuedAggregate(coveragecoord.QueuedInput{
		Request: coveragedomain.Request{
			IdempotencyKey: stringsOf(keyDigit, 32), WorkspaceGeneration: stringsOf('9', 64),
			ProjectID: "core", CoverageProfileID: "coverage-default", CatalogRevision: stringsOf('f', 64),
			Selection: testdomain.Selection{Mode: testdomain.SelectionAll}, RepeatCount: 1, Timeout: time.Minute,
		},
		Selection:      selection,
		BuildProfileID: stringsOf('e', 64), ToolchainID: "retained-toolchain", Toolchain: toolchainSnapshot,
		CreatedAt: created, NewID: func() string { id := ids[0]; ids = ids[1:]; return id },
	})
	if err != nil {
		t.Fatal(err)
	}
	persisted, _, err := aggregate.Persist(context.Background(), store)
	if err != nil {
		t.Fatal(err)
	}
	aggregate.Task = persisted
	return aggregate
}

func stringsOf(value byte, count int) string {
	return string(makeRepeatedBytes(value, count))
}

type productionUnsupportedStore struct {
	*taskstore.Store
	catalog testdomain.Catalog
}

func (store *productionUnsupportedStore) GetCatalog(context.Context, string, string) (testdomain.Catalog, error) {
	return store.catalog.Clone(), nil
}

type productionUnsupportedBuild struct {
	calls int
}

func (preparer *productionUnsupportedBuild) PreparePlan(context.Context, build.StartRequest) (coverageexec.PreparedBuild, error) {
	preparer.calls++
	return nil, errors.New("Linux production wrapper reached native build preparation")
}

type productionUnsupportedTests struct {
	coverageexec.EmbeddedTestPreparer
}

type productionUnsupportedPublisher struct{}

func (productionUnsupportedPublisher) Publish(task.Event) {}

type productionUnsupportedProcesses struct {
	mu    sync.Mutex
	calls int
}

func (processes *productionUnsupportedProcesses) Prepare(context.Context, task.ProcessSpec, string, string) (task.ManagedProcess, error) {
	processes.mu.Lock()
	processes.calls++
	processes.mu.Unlock()
	return nil, errors.New("Linux production wrapper reached native process preparation")
}

func (processes *productionUnsupportedProcesses) count() int {
	processes.mu.Lock()
	defer processes.mu.Unlock()
	return processes.calls
}

func TestDefaultLinuxCoverageWrapperTerminalizesWithoutNativePreparationOrExecutionRoot(t *testing.T) {
	base := t.TempDir()
	workspacePath := filepath.Join(base, "workspace")
	if err := os.MkdirAll(filepath.Join(workspacePath, ".unit-test-ide"), 0o700); err != nil {
		t.Fatal(err)
	}
	buildProfileID := stringsOf('7', 64)
	configJSON := `{"version":3,"coverageProfiles":[{"id":"coverage-default","baseBuildProfileId":"` + buildProfileID + `","include":["**"],"exclude":[]}]}`
	if err := os.WriteFile(filepath.Join(workspacePath, ".unit-test-ide", "workspace.json"), []byte(configJSON), 0o600); err != nil {
		t.Fatal(err)
	}
	workspaceRoot, err := workspace.OpenRoot(workspacePath)
	if err != nil {
		t.Fatal(err)
	}
	executionRoot := filepath.Join(base, "coverage-executions")
	if err := os.Mkdir(executionRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	sqlite, err := taskstore.Open(filepath.Join(base, "tasks.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	createdAt := time.Date(2026, 8, 20, 1, 2, 3, 0, time.UTC)
	catalog, err := testdomain.NewCatalog(testdomain.Catalog{
		ProjectID: "core", ProfileID: buildProfileID, Revision: stringsOf('6', 64), GeneratedAt: createdAt,
		Containers: []testdomain.Container{}, Items: []testdomain.Item{}, Diagnostics: []testdomain.Diagnostic{},
	})
	if err != nil {
		t.Fatal(err)
	}
	store := &productionUnsupportedStore{Store: sqlite, catalog: catalog}
	artifacts, err := artifactstore.New(filepath.Join(base, "artifacts"))
	if err != nil {
		t.Fatal(err)
	}
	processes := &productionUnsupportedProcesses{}
	manager, err := task.NewManager(task.ManagerConfig{
		Store: store, Publisher: productionUnsupportedPublisher{}, Processes: processes, Artifacts: artifacts,
		Clock: task.RealClock{}, NewID: task.NewID, ServiceExecutable: "test-service",
		ServiceInstanceID: stringsOf('9', 32), TerminationGrace: time.Second, ProcessCloseTimeout: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = manager.Shutdown(ctx)
		_ = artifacts.Close()
		_ = sqlite.Close()
	})
	toolchainSnapshot, err := coverageToolchainSnapshot(toolchain.Instance{
		ID: "gcc-linux", Family: toolchain.FamilyGCC, Version: "14.2.0", TargetArchitecture: "amd64",
	}, "linux")
	if err != nil {
		t.Fatal(err)
	}
	ids := []string{stringsOf('1', 32), stringsOf('2', 32)}
	aggregate, err := coveragecoord.NewQueuedAggregate(coveragecoord.QueuedInput{
		Request: coveragedomain.Request{
			IdempotencyKey: stringsOf('3', 32), WorkspaceGeneration: stringsOf('5', 64),
			ProjectID: "core", CoverageProfileID: "coverage-default", CatalogRevision: catalog.Revision,
			Selection: testdomain.Selection{Mode: testdomain.SelectionAll}, RepeatCount: 1, Timeout: time.Minute,
		},
		Selection:      testdomain.SelectionSnapshot{Mode: testdomain.SelectionAll, ContainerIDs: []testdomain.ID{}, ItemIDs: []testdomain.ID{}},
		BuildProfileID: buildProfileID, ToolchainID: "gcc-linux", Toolchain: toolchainSnapshot,
		CreatedAt: createdAt, NewID: func() string { id := ids[0]; ids = ids[1:]; return id },
	})
	if err != nil {
		t.Fatal(err)
	}
	persisted, _, err := aggregate.Persist(context.Background(), store)
	if err != nil {
		t.Fatal(err)
	}
	buildPreparer := &productionUnsupportedBuild{}
	executor, err := newRuntimeCoverageExecutor(coverageExecutionConfig{
		Platform: "linux", Tasks: manager, Store: store, Build: buildPreparer,
		Tests: productionUnsupportedTests{}, WorkspaceRoot: workspaceRoot, ExecutionRoot: executionRoot,
		Clock: task.RealClock{}, NewID: task.NewID,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = executor.Close() })
	platform, ok := executor.(*platformCoverageExecutor)
	if !ok || platform.native {
		t.Fatalf("Linux default coverage executor = %#v", executor)
	}
	if _, ok := platform.coordinator.(*coverageexec.Coordinator); !ok {
		t.Fatalf("Linux default coordinator = %T, want real *coverageexec.Coordinator", platform.coordinator)
	}
	if _, err := executor.Resume(context.Background(), persisted); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	var finished task.Task
	for time.Now().Before(deadline) {
		finished, err = store.Get(context.Background(), persisted.ID)
		if err == nil && finished.Status == task.StatusFinished {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	run, runErr := store.GetCoverageRun(context.Background(), aggregate.Run.ID)
	page, artifactErr := store.ListArtifacts(context.Background(), persisted.ID, "", 10)
	entries, rootErr := os.ReadDir(executionRoot)
	if err != nil || runErr != nil || artifactErr != nil || rootErr != nil ||
		finished.Status != task.StatusFinished || finished.Outcome != task.OutcomeInfrastructureFailed ||
		run.Status != coveragedomain.StatusFinished || run.Outcome != coveragedomain.OutcomeUnavailable ||
		run.Reason != coveragedomain.ReasonInstrumentationFailed || run.ReportID != "" || len(page.Items) != 0 ||
		buildPreparer.calls != 0 || processes.count() != 0 || len(entries) != 0 {
		t.Fatalf("Linux production terminal: task=%#v/%v run=%#v/%v artifacts=%#v/%v build=%d process=%d root=%#v/%v",
			finished, err, run, runErr, page.Items, artifactErr, buildPreparer.calls, processes.count(), entries, rootErr)
	}
}

func makeRepeatedBytes(value byte, count int) []byte {
	result := make([]byte, count)
	for index := range result {
		result[index] = value
	}
	return result
}

type orderedShutdownManager struct {
	runtimeManager
	order *[]string
}

func (manager *orderedShutdownManager) Shutdown(context.Context) error {
	*manager.order = append(*manager.order, "manager")
	return nil
}

func TestRuntimeShutdownStopsCoverageAdmissionThenExecutorBeforeManager(t *testing.T) {
	var order []string
	queue := &fakeCoverageQueue{result: coverageQueuedResultForShutdown()}
	executor := &fakeCoverageExecutor{}
	backend, err := newRuntimeCoverageBackend(queue, &fakeCoverageRepository{}, func(context.Context, session.CoverageRunStart) (coveragecoord.QueuedStartInput, error) {
		return coveragecoord.QueuedStartInput{}, nil
	}, executor)
	if err != nil {
		t.Fatal(err)
	}
	executor.onClose = func() {
		order = append(order, "executor")
		if _, _, _, startErr := backend.StartCoverageRun(context.Background(), session.CoverageRunStart{}); !errors.Is(startErr, task.ErrStorageUnavailable) {
			t.Fatalf("coverage admission during executor Close = %v, want storage unavailable", startErr)
		}
	}
	runtimeValue := &Runtime{
		manager:          &orderedShutdownManager{order: &order},
		coverageBackend:  backend,
		coverageExecutor: executor,
		grace:            time.Second,
	}
	if err := runtimeValue.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := runtimeValue.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if want := []string{"executor", "manager"}; !reflect.DeepEqual(order, want) {
		t.Fatalf("shutdown order = %v, want %v", order, want)
	}
	if executor.closeCalls != 1 || queue.called != 0 {
		t.Fatalf("executor closes=%d queue starts=%d", executor.closeCalls, queue.called)
	}
}

type shutdownFaultTrace struct {
	mu    sync.Mutex
	order []string
}

func (trace *shutdownFaultTrace) add(value string) {
	trace.mu.Lock()
	trace.order = append(trace.order, value)
	trace.mu.Unlock()
}

func (trace *shutdownFaultTrace) snapshot() []string {
	trace.mu.Lock()
	defer trace.mu.Unlock()
	return append([]string(nil), trace.order...)
}

type shutdownFaultManager struct {
	runtimeManager
	trace     *shutdownFaultTrace
	firstErr  error
	retryErr  error
	mu        sync.Mutex
	shutdowns int
}

func (manager *shutdownFaultManager) Shutdown(ctx context.Context) error {
	manager.mu.Lock()
	manager.shutdowns++
	call := manager.shutdowns
	manager.mu.Unlock()
	manager.trace.add("manager-" + string(rune('0'+call)))
	if call == 1 {
		return errors.Join(manager.firstErr, ctx.Err())
	}
	return manager.retryErr
}

func (manager *shutdownFaultManager) calls() int {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	return manager.shutdowns
}

type shutdownFaultStore struct {
	runtimeStore
	trace        *shutdownFaultTrace
	cleanupErr   error
	closeErr     error
	mu           sync.Mutex
	cleanupCalls int
	closeCalls   int
}

func (store *shutdownFaultStore) ActiveLeases(context.Context) ([]task.ProcessLease, error) {
	store.trace.add("force-cleanup")
	store.mu.Lock()
	store.cleanupCalls++
	store.mu.Unlock()
	return nil, store.cleanupErr
}

func (store *shutdownFaultStore) Close() error {
	store.trace.add("store")
	store.mu.Lock()
	store.closeCalls++
	store.mu.Unlock()
	return store.closeErr
}

func (store *shutdownFaultStore) counts() (int, int) {
	store.mu.Lock()
	defer store.mu.Unlock()
	return store.cleanupCalls, store.closeCalls
}

type shutdownFaultArtifacts struct {
	runtimeArtifacts
	trace      *shutdownFaultTrace
	err        error
	mu         sync.Mutex
	closeCalls int
}

func (artifacts *shutdownFaultArtifacts) Close() error {
	artifacts.trace.add("artifacts")
	artifacts.mu.Lock()
	artifacts.closeCalls++
	artifacts.mu.Unlock()
	return artifacts.err
}

func (artifacts *shutdownFaultArtifacts) calls() int {
	artifacts.mu.Lock()
	defer artifacts.mu.Unlock()
	return artifacts.closeCalls
}

type shutdownFaultCloser struct {
	trace *shutdownFaultTrace
	name  string
	err   error
	mu    sync.Mutex
	calls int
}

func (closer *shutdownFaultCloser) Close() error {
	closer.trace.add(closer.name)
	closer.mu.Lock()
	closer.calls++
	closer.mu.Unlock()
	return closer.err
}

func (closer *shutdownFaultCloser) count() int {
	closer.mu.Lock()
	defer closer.mu.Unlock()
	return closer.calls
}

func TestRuntimeShutdownAggregatesEveryFaultAndReleasesEveryOwnerOnce(t *testing.T) {
	trace := &shutdownFaultTrace{}
	executorErr := errors.New("coverage executor close failed")
	managerErr := errors.New("manager shutdown failed")
	retryErr := errors.New("manager retry failed")
	cleanupErr := errors.New("forced lease cleanup failed")
	testErr := errors.New("test resources close failed")
	artifactErr := errors.New("artifact store close failed")
	storeErr := errors.New("sqlite close failed")
	lockErr := errors.New("instance lock close failed")
	guardErr := errors.New("data guard close failed")

	entered := make(chan struct{})
	release := make(chan struct{})
	executor := &fakeCoverageExecutor{closeErr: executorErr}
	queue := &fakeCoverageQueue{result: coverageQueuedResultForShutdown()}
	backend, err := newRuntimeCoverageBackend(queue, &fakeCoverageRepository{}, func(context.Context, session.CoverageRunStart) (coveragecoord.QueuedStartInput, error) {
		return coveragecoord.QueuedStartInput{}, nil
	}, executor)
	if err != nil {
		t.Fatal(err)
	}
	executor.onClose = func() {
		trace.add("executor")
		close(entered)
		<-release
		if _, _, _, startErr := backend.StartCoverageRun(context.Background(), session.CoverageRunStart{}); !errors.Is(startErr, task.ErrStorageUnavailable) {
			t.Errorf("coverage admission during executor Close = %v", startErr)
		}
	}
	manager := &shutdownFaultManager{trace: trace, firstErr: managerErr, retryErr: retryErr}
	store := &shutdownFaultStore{trace: trace, cleanupErr: cleanupErr, closeErr: storeErr}
	artifacts := &shutdownFaultArtifacts{trace: trace, err: artifactErr}
	testResources := &shutdownFaultCloser{trace: trace, name: "test-resources", err: testErr}
	lock := &shutdownFaultCloser{trace: trace, name: "instance-lock", err: lockErr}
	guard := &shutdownFaultCloser{trace: trace, name: "data-guard", err: guardErr}
	runtimeValue := &Runtime{
		manager: manager, store: store, artifacts: artifacts,
		testResources: testResources, lock: lock, guard: guard,
		coverageBackend: backend, coverageExecutor: executor, grace: time.Second,
		runner: &recordingRunner{},
	}

	leaderContext, cancel := context.WithCancel(context.Background())
	cancel()
	leader := make(chan error, 1)
	go func() { leader <- runtimeValue.Shutdown(leaderContext) }()
	<-entered
	const followers = 7
	results := make(chan error, followers)
	for range followers {
		go func() { results <- runtimeValue.Shutdown(context.Background()) }()
	}
	close(release)
	canonical := <-leader
	for range followers {
		if got := <-results; got != canonical {
			t.Fatalf("concurrent Shutdown error identity = %p, want canonical %p", got, canonical)
		}
	}
	if got := runtimeValue.Shutdown(context.Background()); got != canonical {
		t.Fatalf("repeated Shutdown error identity = %p, want canonical %p", got, canonical)
	}
	for _, want := range []error{executorErr, managerErr, retryErr, cleanupErr, context.Canceled, testErr, artifactErr, storeErr, lockErr, guardErr} {
		if !errors.Is(canonical, want) {
			t.Errorf("Shutdown error %v does not contain %v", canonical, want)
		}
	}
	wantOrder := []string{"executor", "manager-1", "force-cleanup", "manager-2", "test-resources", "artifacts", "store", "instance-lock", "data-guard"}
	if got := trace.snapshot(); !reflect.DeepEqual(got, wantOrder) {
		t.Fatalf("shutdown fault order = %v, want %v", got, wantOrder)
	}
	cleanupCalls, storeCloseCalls := store.counts()
	if executor.closeCalls != 1 || manager.calls() != 2 || cleanupCalls != 1 ||
		testResources.count() != 1 || artifacts.calls() != 1 || storeCloseCalls != 1 ||
		lock.count() != 1 || guard.count() != 1 || queue.called != 0 {
		t.Fatalf("shutdown counts executor=%d manager=%d cleanup=%d test=%d artifacts=%d store=%d lock=%d guard=%d queue=%d",
			executor.closeCalls, manager.calls(), cleanupCalls, testResources.count(), artifacts.calls(), storeCloseCalls, lock.count(), guard.count(), queue.called)
	}
}

func coverageQueuedResultForShutdown() coveragecoord.QueuedStartResult {
	return coveragecoord.QueuedStartResult{
		Task: task.Task{ID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Status: task.StatusQueued},
		Run: coveragedomain.Run{
			ID: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", TaskID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			TestRunID: "cccccccccccccccccccccccccccccccc", Status: coveragedomain.StatusQueued,
		},
		TestRun: testdomain.TestRun{
			RunID: "cccccccccccccccccccccccccccccccc", TaskID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			Status: testdomain.RunQueued,
		},
	}
}
