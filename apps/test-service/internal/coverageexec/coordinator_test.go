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
	"unit-test-ide.local/test-service/internal/coveragecoord"
	"unit-test-ide.local/test-service/internal/coveragedomain"
	"unit-test-ide.local/test-service/internal/task"
	"unit-test-ide.local/test-service/internal/taskstore"
	"unit-test-ide.local/test-service/internal/testdomain"
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
	if err != nil || len(page.Items) != 3 {
		t.Fatalf("coverage artifacts = %#v, %v", page.Items, err)
	}
	for _, artifact := range page.Items {
		if artifact.Kind != "stdout" && artifact.Kind != "stderr" && artifact.Kind != "diagnostics" {
			t.Fatalf("unsupported completion published %q", artifact.Kind)
		}
	}
	if publisher.count(task.EventTestRunFinished) != 1 ||
		publisher.count(task.EventCoverageRunFinished) != 1 ||
		publisher.count(task.EventTaskFinished) != 1 ||
		publisher.count(task.EventCoverageReportAvailable) != 0 {
		t.Fatalf("published terminal events = %#v", publisher.snapshot())
	}
}

type coordinatorSQLiteStore struct {
	*taskstore.Store
	catalog testdomain.Catalog
}

func (store *coordinatorSQLiteStore) GetCatalog(context.Context, string, string) (testdomain.Catalog, error) {
	return store.catalog.Clone(), nil
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
