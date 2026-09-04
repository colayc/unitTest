package coverageexec

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"unit-test-ide.local/test-service/internal/artifactstore"
	"unit-test-ide.local/test-service/internal/build"
	"unit-test-ide.local/test-service/internal/cmake"
	"unit-test-ide.local/test-service/internal/coveragecoord"
	"unit-test-ide.local/test-service/internal/coveragedomain"
	"unit-test-ide.local/test-service/internal/coveragellvm"
	"unit-test-ide.local/test-service/internal/coveragerun"
	"unit-test-ide.local/test-service/internal/task"
	"unit-test-ide.local/test-service/internal/taskstore"
	"unit-test-ide.local/test-service/internal/testdomain"
	"unit-test-ide.local/test-service/internal/toolchain"
	"unit-test-ide.local/test-service/internal/workspace"
)

func TestNewCoordinatorRejectsMissingExecutionDependencies(t *testing.T) {
	if _, err := NewCoordinator(Config{}); !errors.Is(err, task.ErrInvalidArgument) {
		t.Fatalf("NewCoordinator() error = %v", err)
	}
}

func TestCoordinatorResumeRejectsNonCoverageTask(t *testing.T) {
	coordinator := &Coordinator{}
	if _, err := coordinator.Resume(context.Background(), task.Task{
		ID:     "11111111111111111111111111111111",
		Kind:   task.KindTestRun,
		Status: task.StatusQueued,
	}); !errors.Is(err, task.ErrInvalidArgument) {
		t.Fatalf("Resume() error = %v", err)
	}
}

func TestCoordinatorAcceptsOnlyTheRetainedInstrumentationContract(t *testing.T) {
	fingerprint := coveragellvm.InstrumentationFingerprint()
	snapshot := coveragedomain.ToolchainSnapshot{InstrumentationFingerprint: fingerprint}
	instrumentation := coveragellvm.Instrumentation{Fingerprint: fingerprint}
	if err := validateInstrumentationContract(snapshot, instrumentation); err != nil {
		t.Fatalf("matching producer/adapter contract = %v", err)
	}
	instrumentation.Fingerprint = strings.Repeat("f", 64)
	if instrumentation.Fingerprint == fingerprint {
		instrumentation.Fingerprint = strings.Repeat("e", 64)
	}
	if err := validateInstrumentationContract(snapshot, instrumentation); !errors.Is(err, task.ErrInvalidArgument) {
		t.Fatalf("mismatched adapter contract error = %v", err)
	}
}

func TestCoverageBuildInterpretationContinuesOnlyAfterSuccess(t *testing.T) {
	step := task.ExecutionStep{Kind: task.StepCoverageBuild}
	current := task.Task{ID: "11111111111111111111111111111111"}

	succeeded := &execution{taskID: current.ID}
	verdict, err := succeeded.Interpret(context.Background(), current, step, task.ProcessResult{})
	if err != nil || verdict != task.StepVerdictSucceeded {
		t.Fatalf("successful build interpretation = %v, %v", verdict, err)
	}

	failed := &execution{taskID: current.ID}
	verdict, err = failed.Interpret(context.Background(), current, step, task.ProcessResult{ExitCode: 1})
	if err != nil || verdict != task.StepVerdictDefault || failed.failedPhase != coveragerun.PhaseBuild {
		t.Fatalf("failed build interpretation = %v, %v, phase=%s", verdict, err, failed.failedPhase)
	}
}

func TestSameCoverageRunIdentityAllowsLifecycleAdvanceButRejectsMutation(t *testing.T) {
	created := time.Unix(123, 0).UTC()
	queued := coveragedomain.Run{
		ID: "11111111111111111111111111111111", TaskID: "22222222222222222222222222222222",
		TestRunID: "33333333333333333333333333333333", Status: coveragedomain.StatusQueued,
		CreatedAt: created,
	}
	running := queued
	running.Status = coveragedomain.StatusRunning
	started := created.Add(time.Second)
	running.StartedAt = &started
	running.LastSequence = 1
	if !sameCoverageRunIdentity(running, queued) {
		t.Fatal("queued-to-running lifecycle advance must preserve coverage identity")
	}
	running.Request.ProjectID = "changed"
	if sameCoverageRunIdentity(running, queued) {
		t.Fatal("coverage request mutation must invalidate coverage identity")
	}
}

func TestSameCoverageCatalogAllowsOnlyRevisionChange(t *testing.T) {
	created := time.Unix(123, 0).UTC()
	base := testdomain.Catalog{
		ProjectID: "project", ProfileID: "profile", Revision: "base",
		GeneratedAt: created,
	}
	alternate := base.Clone()
	alternate.Revision = "coverage"
	alternate.GeneratedAt = created.Add(time.Second)
	if !sameCoverageCatalog(base, alternate) {
		t.Fatal("coverage catalog revision/timestamp change must preserve semantic identity")
	}
	mutated := alternate.Clone()
	mutated.ProjectID = "other"
	if sameCoverageCatalog(base, mutated) {
		t.Fatal("coverage catalog project mutation must be rejected")
	}
}

func TestCoordinatorDuplicateResumeReturnsTheSingleLiveTask(t *testing.T) {
	persisted := task.Task{
		ID:   "11111111111111111111111111111111",
		Kind: task.KindCoverageRun, Status: task.StatusQueued,
	}
	store := &getOnlyCoordinatorStore{current: persisted}
	live := &execution{}
	coordinator := &Coordinator{
		config:     Config{Store: store},
		executions: map[string]liveExecution{persisted.ID: live},
		preparing:  make(map[string]chan struct{}),
	}
	for _, resume := range []func(context.Context, task.Task) (task.Task, error){
		coordinator.Resume,
		coordinator.FinishUnsupported,
	} {
		got, err := resume(context.Background(), persisted)
		if err != nil || got.ID != persisted.ID || got.Status != task.StatusQueued {
			t.Fatalf("duplicate resume = %#v, %v", got, err)
		}
	}
	if store.getCalls != 2 || len(coordinator.executions) != 1 {
		t.Fatalf("duplicate ownership = gets %d, executions %d", store.getCalls, len(coordinator.executions))
	}
}

func TestCoordinatorCloseCancelsWaitsThenReleasesActiveExecutionOnce(t *testing.T) {
	taskID := strings.Repeat("1", 32)
	controller := &blockingCloseController{
		cancelled: make(chan struct{}), allowFinish: make(chan struct{}),
	}
	live := &closeTrackedExecution{}
	coordinator := &Coordinator{
		config:     Config{Tasks: controller},
		executions: map[string]liveExecution{taskID: live},
		preparing:  make(map[string]chan struct{}),
	}
	results := make(chan error, 2)
	go func() { results <- coordinator.Close() }()
	go func() { results <- coordinator.Close() }()
	select {
	case <-controller.cancelled:
	case <-time.After(time.Second):
		t.Fatal("Close did not cancel the active Task")
	}
	if got := live.closeCount(); got != 0 {
		t.Fatalf("execution released before cancellation completed: %d", got)
	}
	close(controller.allowFinish)
	for range 2 {
		select {
		case err := <-results:
			if err != nil {
				t.Fatalf("Close() error = %v", err)
			}
		case <-time.After(time.Second):
			t.Fatal("Close did not wait boundedly for Task completion")
		}
	}
	if controller.cancelCount() != 1 || live.closeCount() != 1 {
		t.Fatalf("close ownership: cancels=%d releases=%d", controller.cancelCount(), live.closeCount())
	}
	if _, err := coordinator.Resume(context.Background(), task.Task{
		ID: taskID, Kind: task.KindCoverageRun, Status: task.StatusQueued,
	}); !errors.Is(err, task.ErrStorageUnavailable) {
		t.Fatalf("Resume after Close error = %v", err)
	}
}

type blockingCloseController struct {
	mu          sync.Mutex
	cancels     int
	cancelled   chan struct{}
	allowFinish chan struct{}
}

func (*blockingCloseController) ResumeQueued(context.Context, task.ResumeRequest) (task.Task, error) {
	return task.Task{}, task.ErrStorageUnavailable
}

func (controller *blockingCloseController) Cancel(ctx context.Context, id string) (task.Task, error) {
	controller.mu.Lock()
	controller.cancels++
	if controller.cancels == 1 {
		close(controller.cancelled)
	}
	controller.mu.Unlock()
	select {
	case <-controller.allowFinish:
		return task.Task{ID: id, Kind: task.KindCoverageRun, Status: task.StatusFinished}, nil
	case <-ctx.Done():
		return task.Task{}, ctx.Err()
	}
}

func (controller *blockingCloseController) cancelCount() int {
	controller.mu.Lock()
	defer controller.mu.Unlock()
	return controller.cancels
}

type closeTrackedExecution struct {
	mu     sync.Mutex
	closes int
}

func (*closeTrackedExecution) AfterStep(context.Context, task.Task, task.ExecutionStep, task.StepResult) (task.Continuation, error) {
	return task.Continuation{}, nil
}
func (*closeTrackedExecution) Interpret(context.Context, task.Task, task.ExecutionStep, task.ProcessResult) (task.StepVerdict, error) {
	return task.StepVerdictSucceeded, nil
}
func (*closeTrackedExecution) ObserveOutput(context.Context, task.Task, task.ExecutionStep, task.ProcessOutput) error {
	return nil
}
func (*closeTrackedExecution) DrainDomainEvents() []task.DomainEvent { return nil }
func (*closeTrackedExecution) ExecuteServiceAction(context.Context, task.Task, task.ExecutionStep) (task.StepResult, error) {
	return task.StepResult{}, nil
}
func (*closeTrackedExecution) PrepareCompletion(context.Context, task.Task, time.Time, task.Outcome, task.ArtifactSink, task.IDGenerator) (task.DomainCompletion, error) {
	return task.DomainCompletion{}, nil
}
func (execution *closeTrackedExecution) Close() error {
	execution.mu.Lock()
	execution.closes++
	execution.mu.Unlock()
	return nil
}
func (execution *closeTrackedExecution) closeCount() int {
	execution.mu.Lock()
	defer execution.mu.Unlock()
	return execution.closes
}

type getOnlyCoordinatorStore struct {
	Store
	current  task.Task
	getCalls int
}

func (store *getOnlyCoordinatorStore) Get(context.Context, string) (task.Task, error) {
	store.getCalls++
	return store.current, nil
}

func TestCoordinatorUnsupportedCompletesOneRealSQLiteAggregate(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	workspacePath := filepath.Join(root, "workspace")
	if err := os.MkdirAll(filepath.Join(workspacePath, ".unit-test-ide"), 0o700); err != nil {
		t.Fatal(err)
	}
	buildProfileID := strings.Repeat("7", 64)
	config := `{"version":3,"coverageProfiles":[{"id":"coverage-default","baseBuildProfileId":"` + buildProfileID + `","include":["**"],"exclude":[]}]}`
	if err := os.WriteFile(
		filepath.Join(workspacePath, ".unit-test-ide", "workspace.json"),
		[]byte(config), 0o600,
	); err != nil {
		t.Fatal(err)
	}
	workspaceRoot, err := workspace.OpenRoot(workspacePath)
	if err != nil {
		t.Fatal(err)
	}
	executionRoot := filepath.Join(root, "coverage-executions")
	if err := os.Mkdir(executionRoot, 0o700); err != nil {
		t.Fatal(err)
	}

	sqlite, err := taskstore.Open(filepath.Join(root, "tasks.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlite.Close() })
	artifacts, err := artifactstore.New(filepath.Join(root, "artifacts"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = artifacts.Close() })
	createdAt := time.Date(2026, 8, 20, 1, 2, 3, 0, time.UTC)
	catalog, err := testdomain.NewCatalog(testdomain.Catalog{
		ProjectID: "core", ProfileID: buildProfileID,
		Revision: strings.Repeat("6", 64), GeneratedAt: createdAt,
		Containers: []testdomain.Container{}, Items: []testdomain.Item{},
		Diagnostics: []testdomain.Diagnostic{},
	})
	if err != nil {
		t.Fatal(err)
	}
	store := &coordinatorSQLiteStore{Store: sqlite, catalog: catalog}
	publisher := &coordinatorPublisher{}
	manager, err := task.NewManager(task.ManagerConfig{
		Store: store, Publisher: publisher,
		Processes: unusedProcessFactory{}, Artifacts: artifacts,
		Clock: task.RealClock{}, NewID: task.NewID,
		ServiceExecutable:   "trusted-service",
		ServiceInstanceID:   strings.Repeat("9", 32),
		TerminationGrace:    time.Second,
		ProcessCloseTimeout: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		shutdown, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = manager.Shutdown(shutdown)
	})

	ids := []string{strings.Repeat("1", 32), strings.Repeat("2", 32)}
	aggregate, err := coveragecoord.NewQueuedAggregate(coveragecoord.QueuedInput{
		Request: coveragedomain.Request{
			IdempotencyKey:      strings.Repeat("3", 32),
			WorkspaceGeneration: strings.Repeat("5", 64),
			ProjectID:           "core", CoverageProfileID: "coverage-default",
			CatalogRevision: catalog.Revision,
			Selection:       testdomain.Selection{Mode: testdomain.SelectionAll},
			RepeatCount:     1, Timeout: time.Minute,
		},
		Selection: testdomain.SelectionSnapshot{
			Mode:         testdomain.SelectionAll,
			ContainerIDs: []testdomain.ID{}, ItemIDs: []testdomain.ID{},
		},
		BuildProfileID: buildProfileID, ToolchainID: "clang-cl",
		Toolchain: coveragedomain.ToolchainSnapshot{
			Platform:     coveragedomain.PlatformWindows,
			Architecture: coveragedomain.ArchitectureX64,
			Compiler: coveragedomain.CompilerSnapshot{
				Family: coveragedomain.CompilerFamilyClangCL, Version: "18.1.8",
			},
			Driver: coveragedomain.DriverSnapshot{
				Name: coveragedomain.DriverLLVMCov, Version: "18.1.8",
			},
			Collector: coveragedomain.CollectorSnapshot{
				Name: coveragedomain.CollectorLLVMCov, Version: "18.1.8",
			},
			NormalizerVersion:          "1.0.0",
			InstrumentationFingerprint: strings.Repeat("8", 64),
		},
		CreatedAt: createdAt,
		NewID: func() string {
			id := ids[0]
			ids = ids[1:]
			return id
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	persisted, _, err := aggregate.Persist(ctx, store)
	if err != nil {
		t.Fatal(err)
	}

	coordinator, err := NewCoordinator(Config{
		Tasks: manager, Store: store,
		Build: unusedBuildPreparer{}, Tests: unusedEmbeddedPreparer{},
		Adapter: unusedCoverageAdapter{}, WorkspaceRoot: workspaceRoot,
		ExecutionRoot: executionRoot, Clock: task.RealClock{}, NewID: task.NewID,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = coordinator.Close() })
	storedTask, storedErr := store.Get(ctx, persisted.ID)
	storedCoverage, coverageErr := store.GetCoverageRun(ctx, aggregate.Run.ID)
	storedTest, testErr := store.GetRunForTask(ctx, persisted.ID)
	storedProfile, profileErr := loadCoverageProfile(workspaceRoot, "coverage-default")
	if storedErr != nil || coverageErr != nil || testErr != nil || profileErr != nil ||
		!sameQueuedTask(storedTask, persisted) ||
		validateQueuedGraph(storedTask, storedCoverage, storedTest, storedProfile) != nil {
		t.Fatalf("queued graph inputs: task=%v coverage=%v test=%v profile=%v same=%v validate=%v",
			storedErr, coverageErr, testErr, profileErr,
			sameQueuedTask(storedTask, persisted),
			validateQueuedGraph(storedTask, storedCoverage, storedTest, storedProfile))
	}
	if _, _, _, _, err := coordinator.loadGraph(ctx, persisted); err != nil {
		t.Fatalf("load queued aggregate: %v", err)
	}
	if _, err := coordinator.FinishUnsupported(ctx, persisted); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	var finished task.Task
	for time.Now().Before(deadline) {
		finished, err = store.Get(ctx, persisted.ID)
		if err == nil && finished.Status == task.StatusFinished {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if finished.Status != task.StatusFinished ||
		finished.Outcome != task.OutcomeInfrastructureFailed {
		t.Fatalf("finished Task = %#v, %v", finished, err)
	}
	coverageRun, err := store.GetCoverageRun(ctx, aggregate.Run.ID)
	if err != nil || coverageRun.Outcome != coveragedomain.OutcomeUnavailable ||
		coverageRun.Reason != coveragedomain.ReasonInstrumentationFailed ||
		coverageRun.ReportID != "" {
		t.Fatalf("finished CoverageRun = %#v, %v", coverageRun, err)
	}
	testRun, err := store.GetRun(ctx, aggregate.TestRun.RunID)
	if err != nil || testRun.Status != testdomain.RunCompleted ||
		testRun.Outcome != testdomain.RunErrored {
		t.Fatalf("finished TestRun = %#v, %v", testRun, err)
	}
	page, err := store.ListArtifacts(ctx, persisted.ID, "", 100)
	if err != nil || len(page.Items) != 0 {
		t.Fatalf("coverage artifacts = %#v, %v", page.Items, err)
	}
	if publisher.count(task.EventTestRunFinished) != 1 ||
		publisher.count(task.EventCoverageRunFinished) != 1 ||
		publisher.count(task.EventTaskFinished) != 1 ||
		publisher.count(task.EventCoverageReportAvailable) != 0 {
		t.Fatalf("published terminal events = %#v", publisher.snapshot())
	}
}

func TestCoordinatorPreparationFailureCompletesRealAggregate(t *testing.T) {
	fixture := newSQLiteCoverageFixture(t, unusedProcessFactory{})
	preparer := &scriptedBuildPreparer{failureAt: 1}
	coordinator, err := NewCoordinator(Config{
		Tasks: fixture.manager, Store: fixture.store,
		Build: preparer, Tests: unusedEmbeddedPreparer{},
		Adapter: unusedCoverageAdapter{}, WorkspaceRoot: fixture.workspaceRoot,
		ExecutionRoot: fixture.executionRoot, Clock: task.RealClock{}, NewID: task.NewID,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = coordinator.Close() })
	if _, err := coordinator.Resume(context.Background(), fixture.persisted); err != nil {
		t.Fatalf("Resume preparation failure = %v, want terminalization", err)
	}
	finished := fixture.awaitFinished(t)
	if finished.Outcome != task.OutcomeCommandFailed {
		t.Fatalf("preparation Task = %#v", finished)
	}
	run, err := fixture.store.GetCoverageRun(context.Background(), fixture.aggregate.Run.ID)
	if err != nil || run.Outcome != coveragedomain.OutcomeUnavailable ||
		run.Reason != coveragedomain.ReasonBuildFailed || run.ReportID != "" {
		t.Fatalf("preparation CoverageRun = %#v, %v", run, err)
	}
	page, err := fixture.store.ListArtifacts(context.Background(), fixture.persisted.ID, "", 100)
	if err != nil || len(page.Items) != 0 {
		t.Fatalf("preparation artifacts = %#v, %v", page.Items, err)
	}
	if fixture.publisher.count(task.EventTestRunFinished) != 1 ||
		fixture.publisher.count(task.EventCoverageRunFinished) != 1 ||
		fixture.publisher.count(task.EventTaskFinished) != 1 {
		t.Fatalf("preparation terminal events = %#v", fixture.publisher.snapshot())
	}
}

func TestCoordinatorAdapterPreparationFailureCompletesRealAggregate(t *testing.T) {
	fixture := newSQLiteCoverageFixture(t, unusedProcessFactory{})
	preparer := &scriptedBuildPreparer{plan: preparedBuildForFixture(fixture)}
	coordinator, err := NewCoordinator(Config{
		Tasks: fixture.manager, Store: fixture.store,
		Build: preparer, Tests: unusedEmbeddedPreparer{},
		Adapter: failingCoverageAdapter{}, WorkspaceRoot: fixture.workspaceRoot,
		ExecutionRoot: fixture.executionRoot, Clock: task.RealClock{}, NewID: task.NewID,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = coordinator.Close() })
	if _, err := coordinator.Resume(context.Background(), fixture.persisted); err != nil {
		t.Fatalf("Resume adapter failure = %v, want terminalization", err)
	}
	finished := fixture.awaitFinished(t)
	if finished.Outcome != task.OutcomeInfrastructureFailed {
		t.Fatalf("adapter preparation Task = %#v", finished)
	}
	run, err := fixture.store.GetCoverageRun(context.Background(), fixture.aggregate.Run.ID)
	if err != nil || run.Outcome != coveragedomain.OutcomeUnavailable ||
		run.Reason != coveragedomain.ReasonInstrumentationFailed || run.ReportID != "" {
		t.Fatalf("adapter preparation CoverageRun = %#v, %v", run, err)
	}
}

type sqliteCoverageFixture struct {
	workspaceRoot workspace.Root
	executionRoot string
	artifactRoot  string
	sqlitePath    string
	artifacts     *artifactstore.Store
	store         *coordinatorSQLiteStore
	publisher     *coordinatorPublisher
	manager       *task.Manager
	aggregate     coveragecoord.QueuedAggregate
	persisted     task.Task
}

func newSQLiteCoverageFixture(t *testing.T, processes task.ProcessFactory) *sqliteCoverageFixture {
	return newSQLiteCoverageFixtureWithArtifacts(t, processes, func(store *artifactstore.Store) task.ArtifactWriter { return store })
}

func newSQLiteCoverageFixtureWithSelection(
	t *testing.T,
	processes task.ProcessFactory,
	selection testdomain.Selection,
	snapshot testdomain.SelectionSnapshot,
) *sqliteCoverageFixture {
	return newSQLiteCoverageFixtureInternal(
		t, processes,
		func(store *artifactstore.Store) task.ArtifactWriter { return store },
		selection, snapshot,
	)
}

func newSQLiteCoverageFixtureWithArtifacts(
	t *testing.T,
	processes task.ProcessFactory,
	wrap func(*artifactstore.Store) task.ArtifactWriter,
) *sqliteCoverageFixture {
	return newSQLiteCoverageFixtureInternal(
		t, processes, wrap,
		testdomain.Selection{Mode: testdomain.SelectionAll},
		testdomain.SelectionSnapshot{
			Mode: testdomain.SelectionAll, ContainerIDs: []testdomain.ID{}, ItemIDs: []testdomain.ID{},
		},
	)
}

func newSQLiteCoverageFixtureWithTimeout(
	t *testing.T,
	processes task.ProcessFactory,
	timeout time.Duration,
) *sqliteCoverageFixture {
	return newSQLiteCoverageFixtureInternal(
		t, processes,
		func(store *artifactstore.Store) task.ArtifactWriter { return store },
		testdomain.Selection{Mode: testdomain.SelectionAll},
		testdomain.SelectionSnapshot{
			Mode: testdomain.SelectionAll, ContainerIDs: []testdomain.ID{}, ItemIDs: []testdomain.ID{},
		},
		timeout,
	)
}

func newSQLiteCoverageFixtureInternal(
	t *testing.T,
	processes task.ProcessFactory,
	wrap func(*artifactstore.Store) task.ArtifactWriter,
	selection testdomain.Selection,
	snapshot testdomain.SelectionSnapshot,
	timeouts ...time.Duration,
) *sqliteCoverageFixture {
	t.Helper()
	ctx := context.Background()
	root := t.TempDir()
	workspacePath := filepath.Join(root, "workspace")
	if err := os.MkdirAll(filepath.Join(workspacePath, ".unit-test-ide"), 0o700); err != nil {
		t.Fatal(err)
	}
	buildProfileID := strings.Repeat("7", 64)
	config := `{"version":3,"coverageProfiles":[{"id":"coverage-default","baseBuildProfileId":"` + buildProfileID + `","include":["**"],"exclude":[]}]}`
	if err := os.WriteFile(filepath.Join(workspacePath, ".unit-test-ide", "workspace.json"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	workspaceRoot, err := workspace.OpenRoot(workspacePath)
	if err != nil {
		t.Fatal(err)
	}
	executionRoot := filepath.Join(root, "coverage-executions")
	if err := os.Mkdir(executionRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	sqlitePath := filepath.Join(root, "tasks.sqlite")
	sqlite, err := taskstore.Open(sqlitePath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlite.Close() })
	artifactRoot := filepath.Join(root, "artifacts")
	artifacts, err := artifactstore.New(artifactRoot)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = artifacts.Close() })
	createdAt := time.Date(2026, 8, 20, 1, 2, 3, 0, time.UTC)
	catalog, err := testdomain.NewCatalog(testdomain.Catalog{
		ProjectID: "core", ProfileID: buildProfileID,
		Revision: strings.Repeat("6", 64), GeneratedAt: createdAt,
		Containers: []testdomain.Container{}, Items: []testdomain.Item{},
		Diagnostics: []testdomain.Diagnostic{},
	})
	if err != nil {
		t.Fatal(err)
	}
	store := &coordinatorSQLiteStore{Store: sqlite, catalog: catalog}
	publisher := &coordinatorPublisher{}
	manager, err := task.NewManager(task.ManagerConfig{
		Store: store, Publisher: publisher, Processes: processes, Artifacts: wrap(artifacts),
		Clock: task.RealClock{}, NewID: task.NewID,
		ServiceExecutable: "trusted-service", ServiceInstanceID: strings.Repeat("9", 32),
		TerminationGrace: 100 * time.Millisecond, ProcessCloseTimeout: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = manager.Shutdown(ctx)
	})
	requestTimeout := time.Minute
	if len(timeouts) == 1 {
		requestTimeout = timeouts[0]
	} else if len(timeouts) != 0 {
		t.Fatal("invalid timeout fixture arguments")
	}
	ids := []string{strings.Repeat("1", 32), strings.Repeat("2", 32)}
	aggregate, err := coveragecoord.NewQueuedAggregate(coveragecoord.QueuedInput{
		Request: coveragedomain.Request{
			IdempotencyKey: strings.Repeat("3", 32), WorkspaceGeneration: strings.Repeat("5", 64),
			ProjectID: "core", CoverageProfileID: "coverage-default", CatalogRevision: catalog.Revision,
			Selection: selection, RepeatCount: 1, Timeout: requestTimeout,
		},
		Selection:      snapshot,
		BuildProfileID: buildProfileID, ToolchainID: "clang-cl",
		Toolchain: coveragedomain.ToolchainSnapshot{
			Platform: coveragedomain.PlatformWindows, Architecture: coveragedomain.ArchitectureX64,
			Compiler:          coveragedomain.CompilerSnapshot{Family: coveragedomain.CompilerFamilyClangCL, Version: "18.1.8"},
			Driver:            coveragedomain.DriverSnapshot{Name: coveragedomain.DriverLLVMCov, Version: "18.1.8"},
			Collector:         coveragedomain.CollectorSnapshot{Name: coveragedomain.CollectorLLVMCov, Version: "18.1.8"},
			NormalizerVersion: "1.0.0", InstrumentationFingerprint: coveragellvm.InstrumentationFingerprint(),
		},
		CreatedAt: createdAt,
		NewID:     func() string { id := ids[0]; ids = ids[1:]; return id },
	})
	if err != nil {
		t.Fatal(err)
	}
	persisted, _, err := aggregate.Persist(ctx, store)
	if err != nil {
		t.Fatal(err)
	}
	return &sqliteCoverageFixture{
		workspaceRoot: workspaceRoot, executionRoot: executionRoot,
		artifactRoot: artifactRoot, sqlitePath: sqlitePath, artifacts: artifacts,
		store: store, publisher: publisher, manager: manager,
		aggregate: aggregate, persisted: persisted,
	}
}

func (fixture *sqliteCoverageFixture) awaitFinished(t *testing.T) task.Task {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		value, err := fixture.store.Get(context.Background(), fixture.persisted.ID)
		if err == nil && value.Status == task.StatusFinished {
			return value
		}
		time.Sleep(10 * time.Millisecond)
	}
	value, err := fixture.store.Get(context.Background(), fixture.persisted.ID)
	events, eventsErr := fixture.store.EventsAfter(context.Background(), 0, 0, 100)
	mutation, applyErr := fixture.store.applySnapshot()
	coverageRun, coverageErr := fixture.store.GetCoverageRun(context.Background(), fixture.aggregate.Run.ID)
	var completionExpected coveragedomain.Status
	var completionRun coveragedomain.Run
	if mutation.FinishCoverage != nil {
		completionExpected = mutation.FinishCoverage.Expected
		completionRun = mutation.FinishCoverage.Run
	}
	t.Fatalf("Task did not finish: %#v, %v; healthy=%v events=%#v eventsErr=%v published=%#v applyErr=%v coverage=%#v coverageErr=%v completionExpected=%q completionRun=%#v",
		value, err, fixture.manager.Healthy(), events, eventsErr, fixture.publisher.snapshot(), applyErr, coverageRun, coverageErr, completionExpected, completionRun)
	return task.Task{}
}

type coordinatorSQLiteStore struct {
	*taskstore.Store
	catalog         testdomain.Catalog
	mu              sync.Mutex
	lastApplyErr    error
	lastMutation    task.Mutation
	taskMutation    func(*task.Task)
	runMutation     func(*coveragedomain.Run)
	catalogMutation func(*testdomain.Catalog)
}

func (store *coordinatorSQLiteStore) Apply(ctx context.Context, mutation task.Mutation) (task.Task, []task.Event, error) {
	value, events, err := store.Store.Apply(ctx, mutation)
	store.mu.Lock()
	store.lastApplyErr = err
	store.lastMutation = mutation
	store.mu.Unlock()
	return value, events, err
}

func (store *coordinatorSQLiteStore) Get(ctx context.Context, id string) (task.Task, error) {
	value, err := store.Store.Get(ctx, id)
	if err != nil {
		return task.Task{}, err
	}
	store.mu.Lock()
	mutate := store.taskMutation
	store.mu.Unlock()
	if mutate != nil && value.Status != task.StatusFinished {
		mutate(&value)
	}
	return value, nil
}

func (store *coordinatorSQLiteStore) GetCoverageRun(ctx context.Context, id string) (coveragedomain.Run, error) {
	value, err := store.Store.GetCoverageRun(ctx, id)
	if err != nil {
		return coveragedomain.Run{}, err
	}
	store.mu.Lock()
	mutate := store.runMutation
	store.mu.Unlock()
	if mutate != nil && value.Status != coveragedomain.StatusFinished {
		mutate(&value)
	}
	return value, nil
}

func (store *coordinatorSQLiteStore) applySnapshot() (task.Mutation, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	return store.lastMutation, store.lastApplyErr
}

func (store *coordinatorSQLiteStore) GetCatalog(context.Context, string, string) (testdomain.Catalog, error) {
	value := store.catalog.Clone()
	store.mu.Lock()
	mutate := store.catalogMutation
	store.mu.Unlock()
	if mutate != nil {
		mutate(&value)
	}
	return value, nil
}

type coordinatorPublisher struct {
	mu     sync.Mutex
	events []task.Event
}

func (publisher *coordinatorPublisher) Publish(event task.Event) {
	publisher.mu.Lock()
	defer publisher.mu.Unlock()
	publisher.events = append(publisher.events, event)
}

func (publisher *coordinatorPublisher) count(kind task.EventType) int {
	publisher.mu.Lock()
	defer publisher.mu.Unlock()
	count := 0
	for _, event := range publisher.events {
		if event.Type == kind {
			count++
		}
	}
	return count
}

func (publisher *coordinatorPublisher) snapshot() []task.Event {
	publisher.mu.Lock()
	defer publisher.mu.Unlock()
	return append([]task.Event(nil), publisher.events...)
}

type unusedProcessFactory struct{}

func (unusedProcessFactory) Prepare(context.Context, task.ProcessSpec, string, string) (task.ManagedProcess, error) {
	return nil, errors.New("process factory must not be called")
}

type unusedBuildPreparer struct{ BuildPreparer }
type unusedEmbeddedPreparer struct{ EmbeddedTestPreparer }
type unusedCoverageAdapter struct{ Adapter }

type scriptedBuildPreparer struct {
	plan      PreparedBuild
	calls     int
	failureAt int
}

func (preparer *scriptedBuildPreparer) PreparePlan(context.Context, build.StartRequest) (PreparedBuild, error) {
	preparer.calls++
	if preparer.calls == preparer.failureAt {
		return nil, errors.New("scripted preparation failure")
	}
	return preparer.plan, nil
}

type fakePreparedBuild struct {
	workspaceGeneration string
	project             workspace.ProjectConfig
	profile             cmake.BuildProfile
	toolchain           toolchain.Instance
	plan                task.ExecutionPlan
	coverageBinaryDir   string
	attachErr           error
	refresh             func() error
}

func (prepared *fakePreparedBuild) Plan() task.ExecutionPlan               { return prepared.plan }
func (*fakePreparedBuild) Boundary() task.ExecutionBoundary                { return permissiveCoverageBoundary{} }
func (prepared *fakePreparedBuild) WorkspaceGeneration() string            { return prepared.workspaceGeneration }
func (prepared *fakePreparedBuild) Project() workspace.ProjectConfig       { return prepared.project }
func (prepared *fakePreparedBuild) Profile() cmake.BuildProfile            { return prepared.profile }
func (prepared *fakePreparedBuild) Toolchain() toolchain.Instance          { return prepared.toolchain }
func (*fakePreparedBuild) Targets() []cmake.Target                         { return []cmake.Target{} }
func (*fakePreparedBuild) AllowTestExecutable(cmake.FingerprintFile) error { return nil }
func (*fakePreparedBuild) ReleaseIfUnadopted()                             {}
func (prepared *fakePreparedBuild) CoverageBinaryDir() string              { return prepared.coverageBinaryDir }
func (prepared *fakePreparedBuild) RefreshTargets(context.Context) ([]cmake.Target, error) {
	if prepared.refresh != nil {
		if err := prepared.refresh(); err != nil {
			return nil, err
		}
	}
	return nil, nil
}
func (prepared *fakePreparedBuild) AttachCoverageToolset(*coveragellvm.Toolset) error {
	return prepared.attachErr
}

func preparedBuildForFixture(fixture *sqliteCoverageFixture) *fakePreparedBuild {
	return &fakePreparedBuild{
		workspaceGeneration: fixture.persisted.WorkspaceGeneration,
		project:             workspace.ProjectConfig{ID: "core"},
		profile:             cmake.BuildProfile{ID: fixture.aggregate.TestRun.ProfileID, ProjectID: "core"},
		toolchain:           toolchain.Instance{ID: "clang-cl", Family: toolchain.FamilyClangCL, Version: "18.1.8"},
	}
}

type permissiveCoverageBoundary struct{}

func (permissiveCoverageBoundary) ValidateExecutable(string) error       { return nil }
func (permissiveCoverageBoundary) ValidateWorkingDirectory(string) error { return nil }

type failingCoverageAdapter struct{}

func (failingCoverageAdapter) Prepare(context.Context, AdapterInput) (PreparedAdapter, error) {
	return nil, errors.New("scripted adapter preparation failure")
}

var _ BuildPreparer = (*scriptedBuildPreparer)(nil)
