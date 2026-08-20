//go:build windows

package coverageexec

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"unit-test-ide.local/test-service/internal/artifactstore"
	"unit-test-ide.local/test-service/internal/coveragedomain"
	"unit-test-ide.local/test-service/internal/coveragereport"
	"unit-test-ide.local/test-service/internal/task"
	"unit-test-ide.local/test-service/internal/testdomain"
)

func TestCoordinatorRealArtifactFinalizationFailureRollsBackAndTerminalizesUnavailable(t *testing.T) {
	factory := &orchestrationProcessFactory{
		export: orchestrationLLVMExport(t), raw: `RAW_FINALIZE_FAILURE C:\private\reports\secret.profraw`,
		results: make(map[string]task.ProcessResult), outputs: make(map[string][]task.ProcessOutput),
	}
	var writer *partwayFailingCoverageWriter
	fixture := newSQLiteCoverageFixtureWithArtifacts(t, factory, func(store *artifactstore.Store) task.ArtifactWriter {
		writer = &partwayFailingCoverageWriter{delegate: store}
		return writer
	})
	writer.root = fixture.artifactRoot
	coordinator, err := NewCoordinator(Config{
		Tasks: fixture.manager, Store: fixture.store,
		Build: &orchestrationBuildPreparer{instance: orchestrationToolchain(t)},
		Tests: orchestrationEmbeddedPreparer{store: fixture.store}, Adapter: &orchestrationAdapter{},
		WorkspaceRoot: fixture.workspaceRoot, ExecutionRoot: fixture.executionRoot,
		Clock: task.RealClock{}, NewID: task.NewID,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = coordinator.Close() })
	if _, err := coordinator.Resume(context.Background(), fixture.persisted); err != nil {
		t.Fatal(err)
	}
	assertRealUnavailableFault(t, fixture, factory, task.OutcomeInfrastructureFailed,
		coveragedomain.ReasonPersistenceFailed,
		[]string{"configure", "build", "test", "merge", "normalize"})
	if writer.failures() != 1 {
		t.Fatalf("filesystem finalization failures = %d, want 1", writer.failures())
	}
	assertNoRegularArtifactFiles(t, fixture.artifactRoot, fixture.persisted.ID)
}

func TestCoordinatorTerminalSQLiteFailureKeepsBrokerInvisibleAndRecoversExactly(t *testing.T) {
	containerID, err := testdomain.ContainerID("core", "recovery.tests")
	if err != nil {
		t.Fatal(err)
	}
	itemID, err := testdomain.CaseID(testdomain.CaseIdentity{
		ProjectID: "core", CTestName: "recovery.tests", Framework: testdomain.FrameworkCppUTest,
		Group: "recovery", Suite: "sqlite", Name: "interrupted",
		ProfileID: strings.Repeat("7", 64), ToolchainID: "clang-cl",
	})
	if err != nil {
		t.Fatal(err)
	}
	factory := &orchestrationProcessFactory{
		export: orchestrationLLVMExport(t), raw: `RAW_SQLITE_FAILURE C:\private\sqlite\secret.profraw`,
		results: make(map[string]task.ProcessResult), outputs: make(map[string][]task.ProcessOutput),
	}
	fixture := newSQLiteCoverageFixtureWithSelection(
		t, factory,
		testdomain.Selection{Mode: testdomain.SelectionItems, ItemIDs: []testdomain.ID{itemID}},
		testdomain.SelectionSnapshot{Mode: testdomain.SelectionItems, ItemIDs: []testdomain.ID{itemID}, ContainerIDs: []testdomain.ID{}},
	)
	persistRecoveryCatalog(t, fixture, containerID, itemID)
	adapter := &orchestrationAdapter{}
	fixture.store.mu.Lock()
	fixture.store.terminalApplyFailures = 1
	fixture.store.mu.Unlock()
	coordinator, err := NewCoordinator(Config{
		Tasks: fixture.manager, Store: fixture.store,
		Build: &orchestrationBuildPreparer{instance: orchestrationToolchain(t)},
		Tests: orchestrationEmbeddedPreparer{store: fixture.store}, Adapter: adapter,
		WorkspaceRoot: fixture.workspaceRoot, ExecutionRoot: fixture.executionRoot,
		Clock: task.RealClock{}, NewID: task.NewID,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = coordinator.Close() })
	if _, err := coordinator.Resume(context.Background(), fixture.persisted); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && (fixture.manager.Healthy() || adapter.closeCount() != 1) {
		time.Sleep(10 * time.Millisecond)
	}
	current, currentErr := fixture.store.Store.Get(context.Background(), fixture.persisted.ID)
	run, runErr := fixture.store.Store.GetCoverageRun(context.Background(), fixture.aggregate.Run.ID)
	page, artifactErr := fixture.store.Store.ListArtifacts(context.Background(), fixture.persisted.ID, "", 100)
	if fixture.manager.Healthy() || adapter.closeCount() != 1 || currentErr != nil || current.Status == task.StatusFinished ||
		runErr != nil || run.Status == coveragedomain.StatusFinished || artifactErr != nil || len(page.Items) != 0 ||
		fixture.publisher.count(task.EventTestRunFinished) != 0 ||
		fixture.publisher.count(task.EventCoverageRunFinished) != 0 ||
		fixture.publisher.count(task.EventCoverageReportAvailable) != 0 ||
		fixture.publisher.count(task.EventTaskFinished) != 0 {
		t.Fatalf("precommit SQLite failure leaked terminal state: healthy=%v adapter=%d task=%#v/%v run=%#v/%v artifacts=%#v/%v events=%#v",
			fixture.manager.Healthy(), adapter.closeCount(), current, currentErr, run, runErr, page.Items, artifactErr, fixture.publisher.snapshot())
	}
	assertNoRegularArtifactFiles(t, fixture.artifactRoot, fixture.persisted.ID)
	recoveredAt := time.Now().UTC().Add(time.Second)
	events, err := fixture.store.Store.RecoverInterrupted(context.Background(), recoveredAt)
	if err != nil {
		t.Fatal(err)
	}
	finished, taskErr := fixture.store.Store.Get(context.Background(), fixture.persisted.ID)
	recovered, coverageErr := fixture.store.Store.GetCoverageRun(context.Background(), fixture.aggregate.Run.ID)
	testRun, testErr := fixture.store.Store.GetRun(context.Background(), fixture.aggregate.TestRun.RunID)
	if len(events) != 3 || events[0].Type != task.EventTestRunFinished ||
		events[1].Type != task.EventCoverageRunFinished || events[2].Type != task.EventTaskFinished ||
		taskErr != nil || finished.Status != task.StatusFinished || finished.Outcome != task.OutcomeInterrupted ||
		coverageErr != nil || recovered.Outcome != coveragedomain.OutcomeUnavailable ||
		recovered.Reason != coveragedomain.ReasonServiceRestarted || recovered.ReportID != "" ||
		testErr != nil || testRun.Outcome != testdomain.RunInterrupted || !testRun.Incomplete {
		t.Fatalf("SQLite recovery terminal graph: events=%#v task=%#v/%v run=%#v/%v test=%#v/%v",
			events, finished, taskErr, recovered, coverageErr, testRun, testErr)
	}
	assertNoPublicFaultArtifacts(t, fixture, factory.raw)
	assertNoRegularArtifactFiles(t, fixture.artifactRoot, fixture.persisted.ID)
}

func TestCoordinatorRealManagerTerminalizesExactPhaseFaultMatrix(t *testing.T) {
	tests := []struct {
		name       string
		wantReason coveragedomain.Reason
		wantTask   task.Outcome
		wantPhases []string
		configure  func(*testing.T, *sqliteCoverageFixture, *orchestrationProcessFactory, *Config)
	}{
		{
			name: "configure process", wantReason: coveragedomain.ReasonInstrumentationFailed,
			wantTask: task.OutcomeInfrastructureFailed, wantPhases: []string{"configure"},
			configure: func(_ *testing.T, _ *sqliteCoverageFixture, factory *orchestrationProcessFactory, _ *Config) {
				factory.results["configure"] = task.ProcessResult{ExitCode: 1}
			},
		},
		{
			name: "build process", wantReason: coveragedomain.ReasonBuildFailed,
			wantTask: task.OutcomeCommandFailed, wantPhases: []string{"configure", "build"},
			configure: func(_ *testing.T, _ *sqliteCoverageFixture, factory *orchestrationProcessFactory, _ *Config) {
				factory.results["build"] = task.ProcessResult{ExitCode: 1}
			},
		},
		{
			name: "merge process", wantReason: coveragedomain.ReasonMergeFailed,
			wantTask: task.OutcomeInfrastructureFailed, wantPhases: []string{"configure", "build", "test", "merge"},
			configure: func(_ *testing.T, _ *sqliteCoverageFixture, factory *orchestrationProcessFactory, _ *Config) {
				factory.results["merge"] = task.ProcessResult{ExitCode: 1}
			},
		},
		{
			name: "malformed export parser", wantReason: coveragedomain.ReasonNormalizationFailed,
			wantTask: task.OutcomeInfrastructureFailed, wantPhases: []string{"configure", "build", "test", "merge", "normalize"},
			configure: func(_ *testing.T, _ *sqliteCoverageFixture, factory *orchestrationProcessFactory, _ *Config) {
				factory.outputs["normalize"] = []task.ProcessOutput{{Stream: "stdout", Data: []byte(`{"type":`)}}
			},
		},
		{
			name: "normalizer duplicate source", wantReason: coveragedomain.ReasonNormalizationFailed,
			wantTask: task.OutcomeInfrastructureFailed, wantPhases: []string{"configure", "build", "test", "merge", "normalize"},
			configure: func(t *testing.T, fixture *sqliteCoverageFixture, factory *orchestrationProcessFactory, _ *Config) {
				factory.export = orchestrationDuplicateSourceExport(t, fixture.workspaceRoot.NativePath)
			},
		},
		{
			name: "renderer action", wantReason: coveragedomain.ReasonReportGenerationFailed,
			wantTask: task.OutcomeInfrastructureFailed, wantPhases: []string{"configure", "build", "test", "merge", "normalize"},
			configure: func(_ *testing.T, _ *sqliteCoverageFixture, _ *orchestrationProcessFactory, config *Config) {
				config.RenderReport = func(coveragereport.Input) (coveragereport.Set, error) {
					return coveragereport.Set{}, errors.New("injected renderer failure")
				}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			factory := &orchestrationProcessFactory{
				export:  orchestrationLLVMExport(t),
				raw:     `RAW_PHASE_FAULT C:\private\phase\secret.profraw LLVM_PROFILE_FILE --token`,
				results: make(map[string]task.ProcessResult), outputs: make(map[string][]task.ProcessOutput),
			}
			fixture := newSQLiteCoverageFixture(t, factory)
			adapter := &orchestrationAdapter{}
			config := Config{
				Tasks: fixture.manager, Store: fixture.store,
				Build: &orchestrationBuildPreparer{instance: orchestrationToolchain(t)},
				Tests: orchestrationEmbeddedPreparer{store: fixture.store}, Adapter: adapter,
				WorkspaceRoot: fixture.workspaceRoot, ExecutionRoot: fixture.executionRoot,
				Clock: task.RealClock{}, NewID: task.NewID,
			}
			test.configure(t, fixture, factory, &config)
			coordinator, err := NewCoordinator(config)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = coordinator.Close() })
			if _, err := coordinator.Resume(context.Background(), fixture.persisted); err != nil {
				t.Fatal(err)
			}
			assertRealUnavailableFault(t, fixture, factory, test.wantTask, test.wantReason, test.wantPhases)
		})
	}
}

func TestCoordinatorTaskTimeoutStopsCurrentTreeBeforeLaterPhase(t *testing.T) {
	factory := &orchestrationProcessFactory{
		export: orchestrationLLVMExport(t), raw: `RAW_TASK_TIMEOUT C:\private\timeout\secret.profraw`,
		results: make(map[string]task.ProcessResult), outputs: make(map[string][]task.ProcessOutput),
		blockPhase: "configure", started: make(chan struct{}),
	}
	fixture := newSQLiteCoverageFixtureWithTimeout(t, factory, 2*time.Second)
	coordinator, err := NewCoordinator(Config{
		Tasks: fixture.manager, Store: fixture.store,
		Build: &orchestrationBuildPreparer{instance: orchestrationToolchain(t)},
		Tests: orchestrationEmbeddedPreparer{store: fixture.store}, Adapter: &orchestrationAdapter{},
		WorkspaceRoot: fixture.workspaceRoot, ExecutionRoot: fixture.executionRoot,
		Clock: task.RealClock{}, NewID: task.NewID,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = coordinator.Close() })
	if _, err := coordinator.Resume(context.Background(), fixture.persisted); err != nil {
		t.Fatal(err)
	}
	select {
	case <-factory.started:
	case <-time.After(3 * time.Second):
		t.Fatal("configure did not start before Task timeout")
	}
	finished := fixture.awaitFinished(t)
	run, err := fixture.store.GetCoverageRun(context.Background(), fixture.aggregate.Run.ID)
	if err != nil || finished.Outcome != task.OutcomeTimedOut ||
		run.Outcome != coveragedomain.OutcomeCancelled || run.Reason != coveragedomain.ReasonTaskTimedOut ||
		run.ReportID != "" || !reflect.DeepEqual(factory.phases(), []string{"configure"}) ||
		factory.terminateCount() != 1 {
		t.Fatalf("Task timeout terminal: task=%#v run=%#v/%v phases=%v terminates=%d",
			finished, run, err, factory.phases(), factory.terminateCount())
	}
	assertNoPublicFaultArtifacts(t, fixture, factory.raw)
}

func TestCoordinatorCancellationAfterProfileSealingReleasesRealManifestTreeOnce(t *testing.T) {
	factory := &orchestrationProcessFactory{
		export: orchestrationLLVMExport(t), raw: `RAW_LATE_CANCEL C:\private\cancel\sealed.profraw`,
		results: make(map[string]task.ProcessResult), outputs: make(map[string][]task.ProcessOutput),
		blockPhase: "merge", started: make(chan struct{}),
	}
	fixture := newSQLiteCoverageFixture(t, factory)
	adapter := &orchestrationAdapter{}
	coordinator, err := NewCoordinator(Config{
		Tasks: fixture.manager, Store: fixture.store,
		Build: &orchestrationBuildPreparer{instance: orchestrationToolchain(t)},
		Tests: orchestrationEmbeddedPreparer{store: fixture.store}, Adapter: adapter,
		WorkspaceRoot: fixture.workspaceRoot, ExecutionRoot: fixture.executionRoot,
		Clock: task.RealClock{}, NewID: task.NewID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := coordinator.Resume(context.Background(), fixture.persisted); err != nil {
		t.Fatal(err)
	}
	select {
	case <-factory.started:
	case <-time.After(3 * time.Second):
		t.Fatal("merge did not start after profile sealing")
	}
	if seals, closed := adapter.sealedManifestState(); seals != 1 || closed {
		t.Fatalf("manifest before cancellation: seals=%d closed=%v", seals, closed)
	}
	if _, err := fixture.manager.Cancel(context.Background(), fixture.persisted.ID); err != nil {
		t.Fatal(err)
	}
	finished := fixture.awaitFinished(t)
	run, err := fixture.store.GetCoverageRun(context.Background(), fixture.aggregate.Run.ID)
	if err != nil || finished.Outcome != task.OutcomeCancelled ||
		run.Outcome != coveragedomain.OutcomeCancelled || run.Reason != coveragedomain.ReasonUserCancelled ||
		run.ReportID != "" || !reflect.DeepEqual(factory.phases(), []string{"configure", "build", "test", "merge"}) ||
		factory.terminateCount() != 1 {
		t.Fatalf("late cancellation terminal: task=%#v run=%#v/%v phases=%v terminates=%d",
			finished, run, err, factory.phases(), factory.terminateCount())
	}
	executionRoot := filepath.Join(fixture.executionRoot, fixture.persisted.ID)
	closeDeadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(closeDeadline) {
		_, rootErr := os.Lstat(executionRoot)
		if errors.Is(rootErr, os.ErrNotExist) && adapter.closeCount() == 1 &&
			adapter.allocatorCloseCount() == 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if seals, closed := adapter.sealedManifestState(); seals != 1 || !closed ||
		adapter.closeCount() != 1 || adapter.allocatorCloseCount() != 1 {
		t.Fatalf("late cancellation ownership: seals=%d manifestClosed=%v adapter=%d allocator=%d",
			seals, closed, adapter.closeCount(), adapter.allocatorCloseCount())
	}
	if _, err := os.Lstat(executionRoot); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("late cancellation execution root remains: %v", err)
	}
	if err := coordinator.Close(); err != nil {
		t.Fatal(err)
	}
	if err := coordinator.Close(); err != nil {
		t.Fatal(err)
	}
	if adapter.closeCount() != 1 || adapter.allocatorCloseCount() != 1 {
		t.Fatalf("duplicate Close changed ownership: adapter=%d allocator=%d",
			adapter.closeCount(), adapter.allocatorCloseCount())
	}
	assertNoPublicFaultArtifacts(t, fixture, factory.raw)
}

func TestCoordinatorRevalidatesEveryRetainedBoundaryBeforeContinuation(t *testing.T) {
	tests := []struct {
		name        string
		mutatePhase string
		wantReason  coveragedomain.Reason
		wantPhases  []string
		mutate      func(*testing.T, *sqliteCoverageFixture, *Coordinator, *orchestrationBuildPreparer)
	}{
		{
			name: "workspace generation", wantReason: coveragedomain.ReasonInstrumentationFailed,
			mutate: func(_ *testing.T, fixture *sqliteCoverageFixture, _ *Coordinator, _ *orchestrationBuildPreparer) {
				fixture.store.mu.Lock()
				fixture.store.taskMutation = func(value *task.Task) { value.WorkspaceGeneration = strings.Repeat("a", 64) }
				fixture.store.mu.Unlock()
			},
		},
		{
			name: "catalog revision", wantReason: coveragedomain.ReasonInstrumentationFailed,
			mutate: func(_ *testing.T, fixture *sqliteCoverageFixture, _ *Coordinator, _ *orchestrationBuildPreparer) {
				fixture.store.mu.Lock()
				fixture.store.catalogMutation = func(value *testdomain.Catalog) { value.Revision = strings.Repeat("a", 64) }
				fixture.store.mu.Unlock()
			},
		},
		{
			name: "coverage profile", wantReason: coveragedomain.ReasonInstrumentationFailed,
			mutate: func(t *testing.T, fixture *sqliteCoverageFixture, _ *Coordinator, _ *orchestrationBuildPreparer) {
				profile := fmt.Sprintf(`{"version":3,"coverageProfiles":[{"id":"coverage-default","baseBuildProfileId":"%s","include":["src/**"],"exclude":[]}]}`, fixture.aggregate.TestRun.ProfileID)
				if err := os.WriteFile(filepath.Join(fixture.workspaceRoot.NativePath, ".unit-test-ide", "workspace.json"), []byte(profile), 0o600); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "instrumentation fingerprint", wantReason: coveragedomain.ReasonInstrumentationFailed,
			mutate: func(_ *testing.T, fixture *sqliteCoverageFixture, _ *Coordinator, _ *orchestrationBuildPreparer) {
				fixture.store.mu.Lock()
				fixture.store.runMutation = func(value *coveragedomain.Run) { value.Toolchain.InstrumentationFingerprint = strings.Repeat("a", 64) }
				fixture.store.mu.Unlock()
			},
		},
		{
			name: "retained boundary identity", mutatePhase: "test",
			wantReason: coveragedomain.ReasonProfileCollectionFailed,
			wantPhases: []string{"configure", "build", "test"},
			mutate: func(t *testing.T, fixture *sqliteCoverageFixture, coordinator *Coordinator, _ *orchestrationBuildPreparer) {
				coordinator.mu.Lock()
				live := coordinator.executions[fixture.persisted.ID]
				execution, ok := live.(*execution)
				if !ok {
					coordinator.mu.Unlock()
					t.Fatal("live execution missing")
				}
				coordinator.mu.Unlock()
				execution.mu.Lock()
				if len(execution.binaries) == 0 {
					execution.mu.Unlock()
					t.Fatal("retained test binary missing")
				}
				execution.binaries[0].digest = strings.Repeat("0", 64)
				execution.mu.Unlock()
			},
		},
		{
			name: "workspace trust loss", wantReason: coveragedomain.ReasonInstrumentationFailed,
			mutate: func(_ *testing.T, _ *sqliteCoverageFixture, _ *Coordinator, preparer *orchestrationBuildPreparer) {
				preparer.mu.Lock()
				preparer.trustLost = true
				preparer.mu.Unlock()
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			factory := &orchestrationProcessFactory{
				export: orchestrationLLVMExport(t), raw: `RAW_REVALIDATION C:\private\revalidate\secret.profraw`,
				results: make(map[string]task.ProcessResult), outputs: make(map[string][]task.ProcessOutput),
			}
			fixture := newSQLiteCoverageFixture(t, factory)
			preparer := &orchestrationBuildPreparer{instance: orchestrationToolchain(t)}
			coordinator, err := NewCoordinator(Config{
				Tasks: fixture.manager, Store: fixture.store, Build: preparer,
				Tests: orchestrationEmbeddedPreparer{store: fixture.store}, Adapter: &orchestrationAdapter{},
				WorkspaceRoot: fixture.workspaceRoot, ExecutionRoot: fixture.executionRoot,
				Clock: task.RealClock{}, NewID: task.NewID,
			})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = coordinator.Close() })
			mutatePhase := test.mutatePhase
			if mutatePhase == "" {
				mutatePhase = "configure"
			}
			var once sync.Once
			factory.beforeComplete = func(phase string) {
				if phase == mutatePhase {
					once.Do(func() { test.mutate(t, fixture, coordinator, preparer) })
				}
			}
			if _, err := coordinator.Resume(context.Background(), fixture.persisted); err != nil {
				t.Fatal(err)
			}
			wantPhases := test.wantPhases
			if wantPhases == nil {
				wantPhases = []string{"configure"}
			}
			assertRealUnavailableFault(t, fixture, factory, task.OutcomeInfrastructureFailed, test.wantReason, wantPhases)
		})
	}
}

func assertRealUnavailableFault(
	t *testing.T,
	fixture *sqliteCoverageFixture,
	factory *orchestrationProcessFactory,
	wantTask task.Outcome,
	wantReason coveragedomain.Reason,
	wantPhases []string,
) {
	t.Helper()
	finished := fixture.awaitFinished(t)
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) && (fixture.publisher.count(task.EventTestRunFinished) != 1 ||
		fixture.publisher.count(task.EventCoverageRunFinished) != 1 ||
		fixture.publisher.count(task.EventTaskFinished) != 1) {
		time.Sleep(time.Millisecond)
	}
	run, runErr := fixture.store.GetCoverageRun(context.Background(), fixture.aggregate.Run.ID)
	if finished.Outcome != wantTask || runErr != nil ||
		run.Outcome != coveragedomain.OutcomeUnavailable || run.Reason != wantReason || run.ReportID != "" ||
		!reflect.DeepEqual(factory.phases(), wantPhases) ||
		fixture.publisher.count(task.EventTestRunFinished) != 1 ||
		fixture.publisher.count(task.EventCoverageRunFinished) != 1 ||
		fixture.publisher.count(task.EventTaskFinished) != 1 ||
		fixture.publisher.count(task.EventCoverageReportAvailable) != 0 {
		t.Fatalf("real fault terminal: task=%#v run=%#v/%v phases=%v events=%#v",
			finished, run, runErr, factory.phases(), fixture.publisher.snapshot())
	}
	assertNoPublicFaultArtifacts(t, fixture, factory.raw)
}

func assertNoPublicFaultArtifacts(t *testing.T, fixture *sqliteCoverageFixture, sentinel string) {
	t.Helper()
	page, err := fixture.store.ListArtifacts(context.Background(), fixture.persisted.ID, "", 100)
	if err != nil || len(page.Items) != 0 {
		t.Fatalf("fault artifacts = %#v, %v", page.Items, err)
	}
	assertNoRawCoverageSentinel(t, fixture, sentinel)
}

func orchestrationDuplicateSourceExport(t *testing.T, workspaceRoot string) []byte {
	t.Helper()
	source := filepath.Join(workspaceRoot, "src", "duplicate.cpp")
	if err := os.MkdirAll(filepath.Dir(source), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, []byte("int duplicate;\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	quoted := fmt.Sprintf("%q", source)
	file := fmt.Sprintf(`{"filename":%s,"segments":[[1,1,1,true,true,false],[2,1,0,false,false,false]],"branches":[],"mcdc_records":[],"expansions":[],"summary":{"lines":{"count":1,"covered":1,"percent":100},"functions":{"count":0,"covered":0,"percent":100},"instantiations":{"count":0,"covered":0,"percent":100},"regions":{"count":0,"covered":0,"notcovered":0,"percent":100},"branches":{"count":0,"covered":0,"notcovered":0,"percent":100},"mcdc":{"count":0,"covered":0,"notcovered":0,"percent":100}}}`, quoted)
	return []byte(fmt.Sprintf(`{"version":"2.0.1","type":"llvm.coverage.json.export","data":[{"files":[%s,%s],"functions":[],"totals":{"lines":{"count":2,"covered":2,"percent":100},"functions":{"count":0,"covered":0,"percent":100},"instantiations":{"count":0,"covered":0,"percent":100},"regions":{"count":0,"covered":0,"notcovered":0,"percent":100},"branches":{"count":0,"covered":0,"notcovered":0,"percent":100},"mcdc":{"count":0,"covered":0,"notcovered":0,"percent":100}}}]}`, file, file))
}

type partwayFailingCoverageWriter struct {
	delegate task.ArtifactWriter
	root     string

	mu       sync.Mutex
	jsonID   string
	injected int
}

func (writer *partwayFailingCoverageWriter) OpenTask(
	ctx context.Context,
	taskID string,
	kind task.Kind,
) (task.ArtifactSink, error) {
	sink, err := writer.delegate.OpenTask(ctx, taskID, kind)
	if err != nil || kind != task.KindCoverageRun {
		return sink, err
	}
	coverage, ok := sink.(task.CoverageArtifactSink)
	if !ok {
		return nil, task.ErrStorageUnavailable
	}
	return &partwayFailingCoverageSink{
		CoverageArtifactSink: coverage,
		writer:               writer,
		taskID:               taskID,
	}, nil
}

func (writer *partwayFailingCoverageWriter) record(kind, id string) {
	if kind != "coverage-json" {
		return
	}
	writer.mu.Lock()
	writer.jsonID = id
	writer.mu.Unlock()
}

func (writer *partwayFailingCoverageWriter) collision(taskID string) string {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	if writer.injected != 0 || writer.jsonID == "" {
		return ""
	}
	writer.injected++
	return filepath.Join(writer.root, "tasks", taskID, writer.jsonID+".coverage.json")
}

func (writer *partwayFailingCoverageWriter) failures() int {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	return writer.injected
}

type partwayFailingCoverageSink struct {
	task.CoverageArtifactSink
	writer *partwayFailingCoverageWriter
	taskID string
}

func (sink *partwayFailingCoverageSink) CommitBlob(
	ctx context.Context,
	id string,
	kind string,
	data []byte,
) error {
	if err := sink.CoverageArtifactSink.CommitBlob(ctx, id, kind, data); err != nil {
		return err
	}
	sink.writer.record(kind, id)
	return nil
}

func (sink *partwayFailingCoverageSink) Finalize(ctx context.Context, at time.Time) ([]task.Artifact, error) {
	collision := sink.writer.collision(sink.taskID)
	if collision == "" {
		return sink.CoverageArtifactSink.Finalize(ctx, at)
	}
	if err := os.MkdirAll(filepath.Dir(collision), 0o700); err != nil {
		return nil, err
	}
	if err := os.WriteFile(collision, []byte("injected collision"), 0o600); err != nil {
		return nil, err
	}
	artifacts, err := sink.CoverageArtifactSink.Finalize(ctx, at)
	removeErr := os.Remove(collision)
	return artifacts, errors.Join(err, removeErr)
}

func assertNoRegularArtifactFiles(t *testing.T, root, taskID string) {
	t.Helper()
	root = filepath.Join(root, "tasks", taskID)
	if _, err := os.Lstat(root); errors.Is(err, os.ErrNotExist) {
		return
	} else if err != nil {
		t.Fatal(err)
	}
	var files []string
	if err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Type().IsRegular() {
			files = append(files, path)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(files) != 0 {
		t.Fatalf("artifact tree contains orphan files: %v", files)
	}
}

func persistRecoveryCatalog(
	t *testing.T,
	fixture *sqliteCoverageFixture,
	containerID, itemID testdomain.ID,
) {
	t.Helper()
	catalog, err := testdomain.NewCatalog(testdomain.Catalog{
		ProjectID: fixture.store.catalog.ProjectID, ProfileID: fixture.store.catalog.ProfileID,
		Revision: fixture.store.catalog.Revision, GeneratedAt: fixture.store.catalog.GeneratedAt,
		Containers: []testdomain.Container{{
			ID: containerID, ProjectID: "core", CTestLogicalName: "recovery.tests",
			DisplayName: "Recovery Tests", Framework: testdomain.FrameworkCppUTest, Labels: []string{},
		}},
		Items: []testdomain.Item{{
			ID: itemID, ContainerID: containerID, Kind: testdomain.ItemCase,
			Framework: testdomain.FrameworkCppUTest, LogicalName: "interrupted",
			DisplayName: "Interrupted", Labels: []string{},
		}},
		Diagnostics: []testdomain.Diagnostic{},
	})
	if err != nil {
		t.Fatal(err)
	}
	fixture.store.mu.Lock()
	fixture.store.catalog = catalog
	fixture.store.mu.Unlock()
	at := fixture.aggregate.Run.CreatedAt.Add(-time.Minute)
	owner := task.Task{
		ID: strings.Repeat("a", 32), IdempotencyKey: strings.Repeat("b", 32),
		RequestHash: strings.Repeat("c", 64), Kind: task.KindSimulation,
		Request: json.RawMessage(`{"scenario":"success"}`), Scenario: task.ScenarioSuccess,
		Timeout: time.Minute, Status: task.StatusQueued, CreatedAt: at,
	}
	if _, _, err := fixture.store.Store.Create(context.Background(), owner, nil, task.EventDraft{
		TaskID: owner.ID, Type: task.EventTaskCreated, At: at, Payload: json.RawMessage(`{}`),
	}); err != nil {
		t.Fatal(err)
	}
	artifact, err := fixture.artifacts.CommitTestCatalog(
		context.Background(), owner.ID, strings.Repeat("d", 32), at, catalog,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := fixture.store.Store.PublishCatalog(context.Background(), catalog, artifact); err != nil {
		t.Fatal(err)
	}
	created, err := fixture.store.Store.Get(context.Background(), owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	finishedAt := at.Add(time.Second)
	finished, err := task.ApplyTransition(created, task.Transition{
		From: task.StatusQueued, To: task.StatusFinished,
		Outcome: task.OutcomeSucceeded, At: finishedAt,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := fixture.store.Store.Apply(context.Background(), task.Mutation{
		Task: finished, Expected: task.StatusQueued,
		Events: []task.EventDraft{{
			TaskID: owner.ID, Type: task.EventTaskFinished, At: finishedAt,
			Payload: json.RawMessage(`{"outcome":"succeeded"}`),
		}},
	}); err != nil {
		t.Fatal(err)
	}
}
