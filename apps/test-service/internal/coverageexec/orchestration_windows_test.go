//go:build windows

package coverageexec

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/sys/windows"

	"unit-test-ide.local/test-service/internal/build"
	"unit-test-ide.local/test-service/internal/cmake"
	"unit-test-ide.local/test-service/internal/coveragedomain"
	"unit-test-ide.local/test-service/internal/coveragellvm"
	"unit-test-ide.local/test-service/internal/coveragerun"
	"unit-test-ide.local/test-service/internal/task"
	"unit-test-ide.local/test-service/internal/testdomain"
	"unit-test-ide.local/test-service/internal/testrun"
	"unit-test-ide.local/test-service/internal/toolchain"
	"unit-test-ide.local/test-service/internal/workspace"
)

func TestCoordinatorRealManagerRunsExactPrivateCoveragePhaseSequence(t *testing.T) {
	instance := orchestrationToolchain(t)
	processes := &orchestrationProcessFactory{
		export: orchestrationLLVMExport(t),
		raw:    `RAW_SENTINEL C:\private\workspace --token secret LLVM_PROFILE_FILE=C:\private\profiles\x.profraw`,
	}
	fixture := newSQLiteCoverageFixture(t, processes)
	adapter := &orchestrationAdapter{}
	coordinator, err := NewCoordinator(Config{
		Tasks: fixture.manager, Store: fixture.store,
		Build: &orchestrationBuildPreparer{instance: instance},
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
	finished := fixture.awaitFinished(t)
	if finished.Outcome != task.OutcomeSucceeded {
		run, runErr := fixture.store.GetCoverageRun(context.Background(), fixture.aggregate.Run.ID)
		t.Fatalf("Task outcome = %q, want succeeded; CoverageRun=%#v, %v; phases=%#v events=%#v", finished.Outcome, run, runErr, processes.phases(), fixture.publisher.snapshot())
	}
	wantSteps := []task.StepKind{
		task.StepCoverageConfigure, task.StepCoverageBuild, task.StepCoverageTest,
		task.StepCoverageMerge, task.StepCoverageNormalize,
		task.StepCoverageReport, task.StepCoveragePublish,
	}
	if len(finished.Steps) != len(wantSteps) {
		t.Fatalf("steps = %#v", finished.Steps)
	}
	for index, want := range wantSteps {
		if finished.Steps[index].Kind != want || finished.Steps[index].Status != task.StepSucceeded {
			t.Fatalf("step %d = %#v, want %q succeeded", index, finished.Steps[index], want)
		}
	}
	if got := processes.phases(); fmt.Sprint(got) != fmt.Sprint([]string{"configure", "build", "test", "merge", "normalize"}) {
		t.Fatalf("process phases = %#v", got)
	}
	run, err := fixture.store.GetCoverageRun(context.Background(), fixture.aggregate.Run.ID)
	if err != nil || run.Outcome != coveragedomain.OutcomeAvailable || run.ReportID == "" {
		t.Fatalf("CoverageRun = %#v, %v", run, err)
	}
	testRun, err := fixture.store.GetRun(context.Background(), fixture.aggregate.TestRun.RunID)
	if err != nil || testRun.Outcome != testdomain.RunPassed {
		t.Fatalf("TestRun = %#v, %v", testRun, err)
	}
	page, err := fixture.store.ListArtifacts(context.Background(), fixture.persisted.ID, "", 100)
	if err != nil || len(page.Items) != 3 {
		t.Fatalf("artifacts = %#v, %v", page.Items, err)
	}
	replayed, err := coordinator.Resume(context.Background(), fixture.persisted)
	if err != nil || replayed.Status != task.StatusFinished || replayed.Outcome != task.OutcomeSucceeded {
		t.Fatalf("coordinator terminal replay = %#v, %v", replayed, err)
	}
	replayedPage, replayErr := fixture.store.ListArtifacts(context.Background(), fixture.persisted.ID, "", 100)
	awaitCoverageTerminalPublication(t, fixture.publisher)
	if replayErr != nil || len(replayedPage.Items) != 3 ||
		fixture.publisher.count(task.EventCoverageRunFinished) != 1 ||
		fixture.publisher.count(task.EventCoverageReportAvailable) != 1 ||
		fixture.publisher.count(task.EventTaskFinished) != 1 {
		t.Fatalf("coordinator replay duplicated terminal graph: artifacts=%#v/%v events=%#v", replayedPage.Items, replayErr, fixture.publisher.snapshot())
	}
	assertNoRawCoverageSentinel(t, fixture, processes.raw)
	if adapter.closeCount() != 1 {
		t.Fatalf("adapter closes = %d, want 1", adapter.closeCount())
	}
}

func awaitCoverageTerminalPublication(t *testing.T, publisher *coordinatorPublisher) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if publisher.count(task.EventCoverageRunFinished) == 1 &&
			publisher.count(task.EventTaskFinished) == 1 {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("terminal events not published after commit: %#v", publisher.snapshot())
}

func TestCoordinatorAssertionFailureContinuesToAvailableRealAggregate(t *testing.T) {
	instance := orchestrationToolchain(t)
	containerID, err := testdomain.ContainerID("core", "assertions")
	if err != nil {
		t.Fatal(err)
	}
	itemID, err := testdomain.CaseID(testdomain.CaseIdentity{
		ProjectID: "core", CTestName: "assertions", Framework: testdomain.FrameworkCppUTest,
		Group: "group", Suite: "suite", Name: "fails",
		ProfileID: strings.Repeat("7", 64), ToolchainID: "clang-cl",
	})
	if err != nil {
		t.Fatal(err)
	}
	processes := &orchestrationProcessFactory{export: orchestrationLLVMExport(t), raw: `RAW_ASSERT C:\private\assert\profile.profraw`}
	fixture := newSQLiteCoverageFixtureWithSelection(
		t, processes,
		testdomain.Selection{Mode: testdomain.SelectionItems, ItemIDs: []testdomain.ID{itemID}},
		testdomain.SelectionSnapshot{Mode: testdomain.SelectionItems, ItemIDs: []testdomain.ID{itemID}, ContainerIDs: []testdomain.ID{}},
	)
	failed := testdomain.TestItemResult{
		ItemID: itemID, ContainerID: containerID, Iteration: 1,
		Outcome: testdomain.ItemFailed, FailureDetails: []testdomain.FailureDetail{}, OutputRefs: []string{},
	}
	coordinator, err := NewCoordinator(Config{
		Tasks: fixture.manager, Store: fixture.store,
		Build:   &orchestrationBuildPreparer{instance: instance},
		Tests:   orchestrationEmbeddedPreparer{store: fixture.store, result: &failed},
		Adapter: &orchestrationAdapter{}, WorkspaceRoot: fixture.workspaceRoot,
		ExecutionRoot: fixture.executionRoot, Clock: task.RealClock{}, NewID: task.NewID,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = coordinator.Close() })
	if _, err := coordinator.Resume(context.Background(), fixture.persisted); err != nil {
		t.Fatal(err)
	}
	finished := fixture.awaitFinished(t)
	run, runErr := fixture.store.GetCoverageRun(context.Background(), fixture.aggregate.Run.ID)
	testRun, testErr := fixture.store.GetRun(context.Background(), fixture.aggregate.TestRun.RunID)
	if finished.Outcome != task.OutcomeSucceeded || runErr != nil || run.Outcome != coveragedomain.OutcomeAvailable ||
		testErr != nil || testRun.Outcome != testdomain.RunFailed || testRun.Summary.Failed != 1 {
		t.Fatalf("assertion aggregate: task=%#v coverage=%#v/%v test=%#v/%v", finished, run, runErr, testRun, testErr)
	}
	if got := processes.phases(); fmt.Sprint(got) != fmt.Sprint([]string{"configure", "build", "test", "merge", "normalize"}) {
		t.Fatalf("assertion stopped phases: %#v", got)
	}
	assertNoRawCoverageSentinel(t, fixture, processes.raw)
}

func TestCoordinatorMissingProfileOutcomesUseExactRealInvocationEvidence(t *testing.T) {
	tests := []struct {
		name        string
		exitCode    int
		timedOut    bool
		wantOutcome coveragedomain.Outcome
		wantReason  coveragedomain.Reason
		wantPartial coveragedomain.CompletenessReason
	}{
		{name: "windows access violation", exitCode: int(uint32(0xC0000005)), wantOutcome: coveragedomain.OutcomePartial, wantPartial: coveragedomain.CompletenessReasonTestCrashed},
		{name: "child timeout", exitCode: 1, timedOut: true, wantOutcome: coveragedomain.OutcomePartial, wantPartial: coveragedomain.CompletenessReasonTestTimedOut},
		{name: "ordinary failure", exitCode: 1, wantOutcome: coveragedomain.OutcomePartial, wantPartial: coveragedomain.CompletenessReasonProfileMissingForFailedInvocation},
		{name: "normal success", exitCode: 0, wantOutcome: coveragedomain.OutcomeUnavailable, wantReason: coveragedomain.ReasonProfileCollectionFailed},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			instance := orchestrationToolchain(t)
			processes := &orchestrationProcessFactory{
				export: orchestrationLLVMExport(t), raw: `RAW_MISSING C:\private\missing\profile.profraw`,
				skipProfile: true, childExitCode: test.exitCode, childTimedOut: test.timedOut,
			}
			fixture := newSQLiteCoverageFixture(t, processes)
			coordinator, err := NewCoordinator(Config{
				Tasks: fixture.manager, Store: fixture.store,
				Build: &orchestrationBuildPreparer{instance: instance},
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
			finished := fixture.awaitFinished(t)
			run, runErr := fixture.store.GetCoverageRun(context.Background(), fixture.aggregate.Run.ID)
			if runErr != nil || run.Outcome != test.wantOutcome || run.Reason != test.wantReason {
				t.Fatalf("missing profile aggregate: task=%#v run=%#v/%v", finished, run, runErr)
			}
			if test.wantOutcome == coveragedomain.OutcomePartial {
				if finished.Outcome != task.OutcomeSucceeded || run.ReportID == "" {
					t.Fatalf("partial terminal = task %#v run %#v", finished, run)
				}
				report, err := fixture.store.GetCoverageReport(context.Background(), run.ReportID)
				if err != nil || len(report.Completeness.Reasons) != 1 || report.Completeness.Reasons[0] != test.wantPartial {
					t.Fatalf("partial report = %#v, %v", report, err)
				}
			} else {
				if finished.Outcome != task.OutcomeInfrastructureFailed || run.ReportID != "" {
					t.Fatalf("ordinary missing profile terminal = task %#v run %#v", finished, run)
				}
			}
			assertNoRawCoverageSentinel(t, fixture, processes.raw)
		})
	}
}

func TestCoordinatorReportAndPublishRevalidationFailureNeverPublishesRealAggregate(t *testing.T) {
	for _, test := range []struct {
		name       string
		mismatchAt int
		wantReason coveragedomain.Reason
	}{
		{name: "report", mismatchAt: 8, wantReason: coveragedomain.ReasonReportGenerationFailed},
		{name: "publish", mismatchAt: 10, wantReason: coveragedomain.ReasonPersistenceFailed},
	} {
		t.Run(test.name, func(t *testing.T) {
			instance := orchestrationToolchain(t)
			processes := &orchestrationProcessFactory{export: orchestrationLLVMExport(t), raw: `RAW_REVALIDATE C:\private\revalidate\profile.profraw`}
			fixture := newSQLiteCoverageFixture(t, processes)
			coordinator, err := NewCoordinator(Config{
				Tasks: fixture.manager, Store: fixture.store,
				Build: &orchestrationBuildPreparer{instance: instance, mismatchAt: test.mismatchAt},
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
			assertRealUnavailableFault(
				t, fixture, processes, task.OutcomeInfrastructureFailed,
				test.wantReason, []string{"configure", "build", "test", "merge", "normalize"},
			)
		})
	}
}

func TestCoordinatorEveryPreparationBoundaryGetsOneUnavailableRealAggregate(t *testing.T) {
	tests := []struct {
		name       string
		configure  func(*sqliteCoverageFixture, *orchestrationBuildPreparer, *orchestrationAdapter)
		wantReason coveragedomain.Reason
		wantTask   task.Outcome
	}{
		{name: "build probe error", configure: func(_ *sqliteCoverageFixture, build *orchestrationBuildPreparer, _ *orchestrationAdapter) {
			build.failAt = 1
		}, wantReason: coveragedomain.ReasonBuildFailed, wantTask: task.OutcomeCommandFailed},
		{name: "probe identity", configure: func(_ *sqliteCoverageFixture, build *orchestrationBuildPreparer, _ *orchestrationAdapter) {
			build.mismatchAt = 1
		}, wantReason: coveragedomain.ReasonInstrumentationFailed, wantTask: task.OutcomeInfrastructureFailed},
		{name: "execution root allocation", configure: func(fixture *sqliteCoverageFixture, _ *orchestrationBuildPreparer, _ *orchestrationAdapter) {
			if err := os.Mkdir(filepath.Join(fixture.executionRoot, fixture.persisted.ID), 0o700); err != nil {
				t.Fatal(err)
			}
		}, wantReason: coveragedomain.ReasonInstrumentationFailed, wantTask: task.OutcomeInfrastructureFailed},
		{name: "instrumentation identity", configure: func(_ *sqliteCoverageFixture, _ *orchestrationBuildPreparer, adapter *orchestrationAdapter) {
			adapter.mismatchInstrumentation = true
		}, wantReason: coveragedomain.ReasonInstrumentationFailed, wantTask: task.OutcomeInfrastructureFailed},
		{name: "coverage plan error", configure: func(_ *sqliteCoverageFixture, build *orchestrationBuildPreparer, _ *orchestrationAdapter) {
			build.failAt = 2
		}, wantReason: coveragedomain.ReasonBuildFailed, wantTask: task.OutcomeCommandFailed},
		{name: "coverage plan identity", configure: func(_ *sqliteCoverageFixture, build *orchestrationBuildPreparer, _ *orchestrationAdapter) {
			build.mismatchAt = 2
		}, wantReason: coveragedomain.ReasonBuildFailed, wantTask: task.OutcomeCommandFailed},
		{name: "coverage binary root", configure: func(_ *sqliteCoverageFixture, build *orchestrationBuildPreparer, _ *orchestrationAdapter) {
			build.wrongBinaryAt = 2
		}, wantReason: coveragedomain.ReasonBuildFailed, wantTask: task.OutcomeCommandFailed},
		{name: "toolset attachment", configure: func(_ *sqliteCoverageFixture, build *orchestrationBuildPreparer, _ *orchestrationAdapter) {
			build.attachFailureAt = 2
		}, wantReason: coveragedomain.ReasonBuildFailed, wantTask: task.OutcomeCommandFailed},
		{name: "build plan rewrite", configure: func(_ *sqliteCoverageFixture, build *orchestrationBuildPreparer, _ *orchestrationAdapter) {
			build.invalidPlanAt = 2
		}, wantReason: coveragedomain.ReasonBuildFailed, wantTask: task.OutcomeCommandFailed},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			instance := orchestrationToolchain(t)
			processes := &orchestrationProcessFactory{export: orchestrationLLVMExport(t), raw: `RAW_PREP C:\private\prep\profile.profraw`}
			fixture := newSQLiteCoverageFixture(t, processes)
			buildPreparer := &orchestrationBuildPreparer{instance: instance}
			adapter := &orchestrationAdapter{}
			test.configure(fixture, buildPreparer, adapter)
			coordinator, err := NewCoordinator(Config{
				Tasks: fixture.manager, Store: fixture.store, Build: buildPreparer,
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
			finished := fixture.awaitFinished(t)
			run, runErr := fixture.store.GetCoverageRun(context.Background(), fixture.aggregate.Run.ID)
			page, artifactErr := fixture.store.ListArtifacts(context.Background(), fixture.persisted.ID, "", 100)
			if finished.Outcome != test.wantTask || runErr != nil || run.Outcome != coveragedomain.OutcomeUnavailable ||
				run.Reason != test.wantReason || run.ReportID != "" || artifactErr != nil || len(page.Items) != 0 ||
				fixture.publisher.count(task.EventCoverageRunFinished) != 1 || fixture.publisher.count(task.EventTaskFinished) != 1 ||
				fixture.publisher.count(task.EventCoverageReportAvailable) != 0 || len(processes.phases()) != 0 {
				t.Fatalf("preparation %s: task=%#v run=%#v/%v artifacts=%#v/%v phases=%#v events=%#v",
					test.name, finished, run, runErr, page.Items, artifactErr, processes.phases(), fixture.publisher.snapshot())
			}
		})
	}
}

func TestCoordinatorCloseCancelsRealActiveManagerProcessBeforeReleasing(t *testing.T) {
	instance := orchestrationToolchain(t)
	processes := &orchestrationProcessFactory{
		export: orchestrationLLVMExport(t), raw: `RAW_CANCEL C:\private\cancel\profile.profraw`,
		blockPhase: "configure", started: make(chan struct{}),
	}
	fixture := newSQLiteCoverageFixture(t, processes)
	adapter := &orchestrationAdapter{}
	coordinator, err := NewCoordinator(Config{
		Tasks: fixture.manager, Store: fixture.store,
		Build: &orchestrationBuildPreparer{instance: instance},
		Tests: orchestrationEmbeddedPreparer{store: fixture.store}, Adapter: adapter,
		WorkspaceRoot: fixture.workspaceRoot, ExecutionRoot: fixture.executionRoot,
		Clock: task.RealClock{}, NewID: task.NewID, CloseTimeout: 3 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := coordinator.Resume(context.Background(), fixture.persisted); err != nil {
		t.Fatal(err)
	}
	select {
	case <-processes.started:
	case <-time.After(time.Second):
		current, _ := fixture.store.Get(context.Background(), fixture.persisted.ID)
		run, _ := fixture.store.GetCoverageRun(context.Background(), fixture.aggregate.Run.ID)
		t.Fatalf("configure process did not start; task=%#v run=%#v phases=%#v events=%#v", current, run, processes.phases(), fixture.publisher.snapshot())
	}
	startRace := make(chan struct{})
	resumeErrors := make(chan error, 4)
	for range 4 {
		go func() {
			<-startRace
			_, err := coordinator.Resume(context.Background(), fixture.persisted)
			resumeErrors <- err
		}()
	}
	errorsSeen := make(chan error, 2)
	go func() { <-startRace; errorsSeen <- coordinator.Close() }()
	go func() { <-startRace; errorsSeen <- coordinator.Close() }()
	close(startRace)
	for range 2 {
		if err := <-errorsSeen; err != nil {
			t.Fatalf("Close() error = %v", err)
		}
	}
	for range 4 {
		if err := <-resumeErrors; err != nil && !errors.Is(err, task.ErrStorageUnavailable) {
			t.Fatalf("concurrent Resume/Close error = %v", err)
		}
	}
	finished := fixture.awaitFinished(t)
	if finished.Outcome != task.OutcomeCancelled {
		t.Fatalf("cancelled Task = %#v", finished)
	}
	if got := processes.phases(); fmt.Sprint(got) != fmt.Sprint([]string{"configure"}) {
		t.Fatalf("later phase started after Close: %#v", got)
	}
	if processes.terminateCount() != 1 || adapter.closeCount() != 1 || adapter.allocatorCloseCount() != 1 {
		t.Fatalf("close ownership: terminates=%d adapter closes=%d allocator closes=%d", processes.terminateCount(), adapter.closeCount(), adapter.allocatorCloseCount())
	}
	if _, err := os.Lstat(filepath.Join(fixture.executionRoot, fixture.persisted.ID)); !os.IsNotExist(err) {
		t.Fatalf("cancelled execution root was not released: %v", err)
	}
	page, err := fixture.store.ListArtifacts(context.Background(), fixture.persisted.ID, "", 100)
	if err != nil || len(page.Items) != 0 {
		t.Fatalf("cancel artifacts = %#v, %v", page.Items, err)
	}
	assertNoRawCoverageSentinel(t, fixture, processes.raw)
}

func assertNoRawCoverageSentinel(t *testing.T, fixture *sqliteCoverageFixture, sentinel string) {
	t.Helper()
	page, err := fixture.store.ListArtifacts(context.Background(), fixture.persisted.ID, "", 100)
	if err != nil {
		t.Fatal(err)
	}
	for _, artifact := range page.Items {
		data, err := os.ReadFile(filepath.Join(filepath.Dir(fixture.executionRoot), "artifacts", filepath.FromSlash(artifact.RelativePath)))
		if err != nil {
			t.Fatal(err)
		}
		for _, forbidden := range []string{sentinel, `C:\private`, fixture.workspaceRoot.NativePath, "LLVM_PROFILE_FILE", "--token"} {
			if forbidden != "" && strings.Contains(string(data), forbidden) {
				t.Fatalf("artifact %q contains private value %q", artifact.Kind, forbidden)
			}
		}
	}
	events, err := fixture.store.EventsAfter(context.Background(), 0, 0, 1000)
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		for _, forbidden := range []string{sentinel, `C:\private`, fixture.workspaceRoot.NativePath, "LLVM_PROFILE_FILE", "--token"} {
			if forbidden != "" && strings.Contains(string(event.Payload), forbidden) {
				t.Fatalf("event %q contains private value %q", event.Type, forbidden)
			}
		}
	}
}

type orchestrationBuildPreparer struct {
	mu              sync.Mutex
	instance        toolchain.Instance
	calls           int
	mismatchAt      int
	failAt          int
	invalidPlanAt   int
	wrongBinaryAt   int
	attachFailureAt int
	trustLost       bool
}

func (preparer *orchestrationBuildPreparer) PreparePlan(_ context.Context, request build.StartRequest) (PreparedBuild, error) {
	preparer.mu.Lock()
	preparer.calls++
	call := preparer.calls
	instance := preparer.instance
	mismatchAt := preparer.mismatchAt
	failAt := preparer.failAt
	invalidPlanAt := preparer.invalidPlanAt
	wrongBinaryAt := preparer.wrongBinaryAt
	attachFailureAt := preparer.attachFailureAt
	trustLost := preparer.trustLost
	preparer.mu.Unlock()
	if trustLost {
		return nil, build.ErrWorkspaceTrustRequired
	}
	if call == failAt {
		return nil, errors.New("injected build preparation failure")
	}
	if call == mismatchAt {
		instance.ID = "changed-toolchain"
	}
	plan := task.ExecutionPlan{Version: 1, Steps: []task.ExecutionStep{
		{ID: "configure", Kind: task.StepConfigure, Process: task.ProcessSpec{Executable: instance.CXXCompiler, Args: []string{"--configure"}, Dir: filepath.Dir(instance.CXXCompiler)}},
		{ID: "build", Kind: task.StepBuild, Process: task.ProcessSpec{Executable: instance.CXXCompiler, Args: []string{"--build"}, Dir: filepath.Dir(instance.CXXCompiler)}},
	}}
	plan.Fingerprint = task.FingerprintPlan(plan)
	if call == invalidPlanAt {
		plan.Steps = plan.Steps[:1]
		plan.Fingerprint = task.FingerprintPlan(plan)
	}
	binaryDir := ""
	if request.Coverage != nil {
		binaryDir = request.Coverage.BinaryDir
	}
	if call == wrongBinaryAt {
		binaryDir = filepath.Join(filepath.Dir(binaryDir), "wrong-build-root")
	}
	var attachErr error
	if call == attachFailureAt {
		attachErr = errors.New("injected toolset attachment failure")
	}
	return &fakePreparedBuild{
		workspaceGeneration: request.WorkspaceGeneration,
		project:             workspace.ProjectConfig{ID: request.ProjectID},
		profile:             cmake.BuildProfile{ID: request.BuildProfileID, ProjectID: request.ProjectID},
		toolchain:           instance, plan: plan, coverageBinaryDir: binaryDir,
		attachErr: attachErr,
	}, nil
}

type orchestrationAdapter struct {
	mu                      sync.Mutex
	prepared                *orchestrationPreparedAdapter
	mismatchInstrumentation bool
}

func (adapter *orchestrationAdapter) Prepare(_ context.Context, input AdapterInput) (PreparedAdapter, error) {
	toolset, err := coveragellvm.PinToolset(input.Toolchain)
	if err != nil {
		return nil, err
	}
	instrumentation, err := coveragellvm.WriteInstrumentation(input.TaskRoot)
	if err != nil {
		_ = toolset.Close()
		return nil, err
	}
	if adapter.mismatchInstrumentation {
		instrumentation.Fingerprint = strings.Repeat("f", 64)
	}
	allocator, err := coveragellvm.NewProfileAllocator(input.ProfileRoot)
	if err != nil {
		_ = toolset.Close()
		return nil, err
	}
	countedAllocator := &countingProfileAllocator{delegate: allocator}
	if closer, ok := allocator.(io.Closer); ok {
		countedAllocator.closer = closer
	}
	prepared := &orchestrationPreparedAdapter{
		toolset: toolset, instrumentation: instrumentation,
		allocator: countedAllocator, profileRoot: input.ProfileRoot,
	}
	adapter.mu.Lock()
	adapter.prepared = prepared
	adapter.mu.Unlock()
	return prepared, nil
}

func (adapter *orchestrationAdapter) closeCount() int {
	adapter.mu.Lock()
	prepared := adapter.prepared
	adapter.mu.Unlock()
	if prepared == nil {
		return 0
	}
	prepared.mu.Lock()
	defer prepared.mu.Unlock()
	return prepared.closes
}

func (adapter *orchestrationAdapter) allocatorCloseCount() int {
	adapter.mu.Lock()
	prepared := adapter.prepared
	adapter.mu.Unlock()
	if prepared == nil {
		return 0
	}
	allocator, ok := prepared.allocator.(*countingProfileAllocator)
	if !ok {
		return 0
	}
	allocator.mu.Lock()
	defer allocator.mu.Unlock()
	return allocator.closes
}

func (adapter *orchestrationAdapter) sealedManifestState() (int, bool) {
	adapter.mu.Lock()
	prepared := adapter.prepared
	adapter.mu.Unlock()
	if prepared == nil {
		return 0, false
	}
	return prepared.sealedManifestState()
}

type countingProfileAllocator struct {
	delegate  testrun.ProfileAllocator
	closer    io.Closer
	closeOnce sync.Once
	mu        sync.Mutex
	closes    int
}

func (allocator *countingProfileAllocator) Decorate(expectation testrun.ProfileExpectation, spec task.ProcessSpec) (task.ProcessSpec, error) {
	return allocator.delegate.Decorate(expectation, spec)
}

func (allocator *countingProfileAllocator) Close() error {
	var result error
	allocator.closeOnce.Do(func() {
		allocator.mu.Lock()
		allocator.closes++
		allocator.mu.Unlock()
		if allocator.closer != nil {
			result = allocator.closer.Close()
		}
	})
	return result
}

type orchestrationPreparedAdapter struct {
	toolset         *coveragellvm.Toolset
	instrumentation coveragellvm.Instrumentation
	allocator       testrun.ProfileAllocator
	profileRoot     string
	closeOnce       sync.Once
	mu              sync.Mutex
	closes          int
	seals           int
	manifest        *coveragellvm.Manifest
}

func (adapter *orchestrationPreparedAdapter) Toolset() *coveragellvm.Toolset { return adapter.toolset }
func (adapter *orchestrationPreparedAdapter) Instrumentation() coveragellvm.Instrumentation {
	return adapter.instrumentation
}
func (adapter *orchestrationPreparedAdapter) Allocator() testrun.ProfileAllocator {
	return adapter.allocator
}
func (adapter *orchestrationPreparedAdapter) SealProfiles(expectations []testrun.ProfileExpectation, outcomes []testrun.InvocationOutcome) (coveragellvm.Manifest, error) {
	manifest, err := coveragellvm.SealProfiles(adapter.profileRoot, expectations, outcomes)
	if err == nil {
		copy := manifest
		adapter.mu.Lock()
		adapter.seals++
		adapter.manifest = &copy
		adapter.mu.Unlock()
	}
	return manifest, err
}
func (adapter *orchestrationPreparedAdapter) Collector(_ coveragellvm.Manifest, _ []coveragerun.TrustedPath) (task.ProcessSpec, task.ProcessSpec, error) {
	dir := filepath.Dir(adapter.profileRoot)
	return task.ProcessSpec{Executable: adapter.toolset.Profdata().Path(), Args: []string{"--merge"}, Dir: dir},
		task.ProcessSpec{Executable: adapter.toolset.Cov().Path(), Args: []string{"--export"}, Dir: dir}, nil
}
func (adapter *orchestrationPreparedAdapter) Close() error {
	var result error
	adapter.closeOnce.Do(func() {
		adapter.mu.Lock()
		adapter.closes++
		adapter.mu.Unlock()
		if closer, ok := adapter.allocator.(io.Closer); ok {
			result = errors.Join(result, closer.Close())
		}
		result = errors.Join(result, adapter.toolset.Close())
	})
	return result
}

func (adapter *orchestrationPreparedAdapter) releaseProfileRootRenameBlockerForTest() error {
	allocator, ok := adapter.allocator.(*countingProfileAllocator)
	if !ok || allocator.closer == nil {
		return errors.New("real profile-root handle is unavailable")
	}
	return allocator.closer.Close()
}

func (adapter *orchestrationPreparedAdapter) sealedManifestState() (int, bool) {
	adapter.mu.Lock()
	defer adapter.mu.Unlock()
	if adapter.manifest == nil {
		return adapter.seals, false
	}
	return adapter.seals, adapter.manifest.Verify() != nil
}

type orchestrationRunStore interface {
	AppendResult(context.Context, string, testdomain.TestItemResult) error
	GetRun(context.Context, string) (testdomain.TestRun, error)
}

type orchestrationEmbeddedPreparer struct {
	store  orchestrationRunStore
	result *testdomain.TestItemResult
}

func (preparer orchestrationEmbeddedPreparer) PrepareEmbedded(_ context.Context, request testrun.EmbeddedRequest) (testrun.EmbeddedRun, error) {
	expectation := testrun.ProfileExpectation{InvocationID: "invocation-1", Iteration: 1, FileName: "p-000001-i-000001-%p-%m.profraw"}
	decorated, err := request.Allocator.Decorate(expectation, task.ProcessSpec{
		Executable: request.PreparedBuild.Toolchain().CXXCompiler,
		Args:       []string{"--test"}, Dir: filepath.Dir(request.PreparedBuild.Toolchain().CXXCompiler),
	})
	if err != nil {
		return nil, err
	}
	return &orchestrationEmbeddedRun{run: request.Run.Clone(), store: preparer.store, result: preparer.result, expectation: expectation, step: task.ExecutionStep{
		ID: "test-wave-1", Kind: task.StepTestRun,
		Process: task.ProcessSpec{Batch: []task.ProcessBatchItem{{
			ID: expectation.InvocationID, Executable: decorated.Executable,
			Args: decorated.Args, Env: decorated.Env, EnvUnset: decorated.EnvUnset,
			Dir: decorated.Dir, Timeout: time.Second,
		}}},
	}}, nil
}

type orchestrationEmbeddedRun struct {
	run         testdomain.TestRun
	store       orchestrationRunStore
	result      *testdomain.TestItemResult
	expectation testrun.ProfileExpectation
	step        task.ExecutionStep
	mu          sync.Mutex
	finished    bool
}

func (run *orchestrationEmbeddedRun) Steps() []task.ExecutionStep {
	return []task.ExecutionStep{cloneStep(run.step)}
}
func (run *orchestrationEmbeddedRun) Expectations() []testrun.ProfileExpectation {
	return []testrun.ProfileExpectation{run.expectation}
}
func (run *orchestrationEmbeddedRun) Interpret(ctx context.Context, _ task.Task, _ task.ExecutionStep, result task.ProcessResult) (task.StepVerdict, error) {
	if len(result.Children) != 1 || result.Children[0].ID != "invocation-1" || result.Children[0].Err != nil {
		return task.StepVerdictDefault, task.ErrInvalidArgument
	}
	if run.result != nil {
		if err := run.store.AppendResult(ctx, run.run.RunID, run.result.Clone()); err != nil {
			return task.StepVerdictDefault, err
		}
	}
	return task.StepVerdictSucceeded, nil
}
func (*orchestrationEmbeddedRun) ObserveOutput(context.Context, task.Task, task.ExecutionStep, task.ProcessOutput) error {
	return nil
}
func (*orchestrationEmbeddedRun) DrainDomainEvents() []task.DomainEvent { return nil }
func (run *orchestrationEmbeddedRun) Finish(_ context.Context, at time.Time, outcome task.Outcome) (testdomain.TestRun, error) {
	run.mu.Lock()
	defer run.mu.Unlock()
	if run.finished {
		return testdomain.TestRun{}, task.ErrConflict
	}
	result, err := run.store.GetRun(context.Background(), run.run.RunID)
	if err != nil {
		return testdomain.TestRun{}, err
	}
	startedAt := at.Add(-time.Millisecond)
	result.Status = testdomain.RunCompleted
	result.StartedAt = &startedAt
	result.FinishedAt = &at
	result.Summary, result.Incomplete, err = testrun.Summarize(result.Results, result.Summary.Iterations)
	if err != nil {
		return testdomain.TestRun{}, err
	}
	result.Outcome = coverageTestRunOutcome(outcome, result.Summary, result.Incomplete)
	result.ResultRevision, err = testdomain.ResultRevision(result.Results)
	if err != nil {
		return testdomain.TestRun{}, err
	}
	validated, err := testdomain.NewTestRun(result)
	if err == nil {
		run.finished = true
	}
	return validated, err
}

type orchestrationProcessFactory struct {
	mu             sync.Mutex
	export         []byte
	raw            string
	results        map[string]task.ProcessResult
	outputs        map[string][]task.ProcessOutput
	beforeComplete func(string)
	blockPhase     string
	started        chan struct{}
	phaseOrder     []string
	terminates     int
	skipProfile    bool
	childExitCode  int
	childTimedOut  bool
}

func (factory *orchestrationProcessFactory) Prepare(_ context.Context, spec task.ProcessSpec, taskID, serviceID string) (task.ManagedProcess, error) {
	phase := orchestrationPhase(spec)
	if phase == "" {
		return nil, fmt.Errorf("unknown orchestration process: %#v", spec)
	}
	factory.mu.Lock()
	factory.phaseOrder = append(factory.phaseOrder, phase)
	factory.mu.Unlock()
	process := &orchestrationManagedProcess{
		factory: factory, spec: spec, phase: phase,
		output: make(chan task.ProcessOutput, 16), done: make(chan task.ProcessResult, 1),
		lease: task.ProcessLease{TaskID: taskID, ServiceInstanceID: serviceID, HostPID: 41, HostStartIdentity: "host-start"},
	}
	return process, nil
}

func orchestrationPhase(spec task.ProcessSpec) string {
	if len(spec.Batch) != 0 {
		return "test"
	}
	if len(spec.Args) == 0 {
		return ""
	}
	phase := strings.TrimPrefix(spec.Args[0], "--")
	if phase == "export" {
		return "normalize"
	}
	return phase
}

func (factory *orchestrationProcessFactory) phases() []string {
	factory.mu.Lock()
	defer factory.mu.Unlock()
	return append([]string(nil), factory.phaseOrder...)
}
func (factory *orchestrationProcessFactory) terminateCount() int {
	factory.mu.Lock()
	defer factory.mu.Unlock()
	return factory.terminates
}

type orchestrationManagedProcess struct {
	factory  *orchestrationProcessFactory
	spec     task.ProcessSpec
	phase    string
	lease    task.ProcessLease
	output   chan task.ProcessOutput
	done     chan task.ProcessResult
	complete sync.Once
}

func (process *orchestrationManagedProcess) Lease() task.ProcessLease { return process.lease }
func (process *orchestrationManagedProcess) Start(context.Context) error {
	process.lease.TargetProcessGroup = 42
	process.factory.mu.Lock()
	blockPhase := process.factory.blockPhase
	started := process.factory.started
	raw := process.factory.raw
	export := append([]byte(nil), process.factory.export...)
	outputs := append([]task.ProcessOutput(nil), process.factory.outputs[process.phase]...)
	result, hasResult := process.factory.results[process.phase]
	beforeComplete := process.factory.beforeComplete
	skipProfile := process.factory.skipProfile
	process.factory.mu.Unlock()
	if process.phase == blockPhase {
		if started != nil {
			close(started)
		}
		process.output <- task.ProcessOutput{Stream: "stderr", Data: []byte(raw)}
		return nil
	}
	process.output <- task.ProcessOutput{Stream: "stderr", Data: []byte(raw)}
	for _, output := range outputs {
		process.output <- task.ProcessOutput{
			Source: output.Source, Stream: output.Stream, Data: append([]byte(nil), output.Data...),
		}
	}
	if process.phase == "test" && !skipProfile {
		for _, item := range process.spec.Batch {
			for _, entry := range item.Env {
				if name, value, ok := strings.Cut(entry, "="); ok && strings.EqualFold(name, "LLVM_PROFILE_FILE") {
					value = strings.ReplaceAll(strings.ReplaceAll(value, "%p", "123"), "%m", "module")
					if err := os.WriteFile(value, []byte("raw profile"), 0o600); err != nil {
						return err
					}
				}
			}
		}
	}
	if process.phase == "normalize" && !hasOutputStream(outputs, "stdout") {
		process.output <- task.ProcessOutput{Stream: "stdout", Data: export}
	}
	if beforeComplete != nil {
		beforeComplete(process.phase)
	}
	if !hasResult {
		result = task.ProcessResult{ExitCode: 0}
	}
	if process.phase == "test" && result.Children == nil {
		result.Children = orchestrationChildren(process.spec, process.factory)
	}
	process.finish(result)
	return nil
}

func hasOutputStream(outputs []task.ProcessOutput, stream string) bool {
	for _, output := range outputs {
		if output.Stream == stream {
			return true
		}
	}
	return false
}
func (process *orchestrationManagedProcess) Output() <-chan task.ProcessOutput { return process.output }
func (process *orchestrationManagedProcess) Done() <-chan task.ProcessResult   { return process.done }
func (process *orchestrationManagedProcess) Terminate(context.Context, time.Duration) error {
	process.factory.mu.Lock()
	process.factory.terminates++
	process.factory.mu.Unlock()
	process.finish(task.ProcessResult{ExitCode: 1})
	return nil
}
func (*orchestrationManagedProcess) Close(context.Context) error { return nil }
func (process *orchestrationManagedProcess) finish(result task.ProcessResult) {
	process.complete.Do(func() {
		close(process.output)
		process.done <- result
		close(process.done)
	})
}

func orchestrationChildren(spec task.ProcessSpec, factory *orchestrationProcessFactory) []task.ProcessChildResult {
	children := make([]task.ProcessChildResult, len(spec.Batch))
	for index, item := range spec.Batch {
		children[index] = task.ProcessChildResult{
			ID: item.ID, ExitCode: factory.childExitCode, TimedOut: factory.childTimedOut,
		}
	}
	return children
}

func orchestrationLLVMExport(t *testing.T) []byte {
	t.Helper()
	path := filepath.Join(t.TempDir(), "workspace", "source.cpp")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("int main(){}"), 0o600); err != nil {
		t.Fatal(err)
	}
	return []byte(fmt.Sprintf(`{"version":"2.0.1","type":"llvm.coverage.json.export","data":[{"files":[],"functions":[],"totals":{"lines":{"count":0,"covered":0,"percent":100},"functions":{"count":0,"covered":0,"percent":100},"instantiations":{"count":0,"covered":0,"percent":100},"regions":{"count":0,"covered":0,"notcovered":0,"percent":100},"branches":{"count":0,"covered":0,"notcovered":0,"percent":100},"mcdc":{"count":0,"covered":0,"notcovered":0,"percent":100}}}]}`))
}

func orchestrationToolchain(t *testing.T) toolchain.Instance {
	t.Helper()
	root := filepath.Join(t.TempDir(), "LLVM", "bin")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	paths := []string{filepath.Join(root, "clang-cl.exe"), filepath.Join(root, "llvm-profdata.exe"), filepath.Join(root, "llvm-cov.exe")}
	for index, path := range paths {
		if err := os.WriteFile(path, []byte(strings.Repeat(string(rune('a'+index)), 128)), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	evidence := make([]toolchain.ExecutableEvidence, len(paths))
	for index, path := range paths {
		contents, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(contents)
		pointer, err := windows.UTF16PtrFromString(path)
		if err != nil {
			t.Fatal(err)
		}
		handle, err := windows.CreateFile(pointer, windows.FILE_READ_ATTRIBUTES, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
		if err != nil {
			t.Fatal(err)
		}
		var info windows.ByHandleFileInformation
		err = windows.GetFileInformationByHandle(handle, &info)
		_ = windows.CloseHandle(handle)
		if err != nil {
			t.Fatal(err)
		}
		evidence[index] = toolchain.ExecutableEvidence{
			FileIdentity: fmt.Sprintf("windows:%08x:%08x%08x", info.VolumeSerialNumber, info.FileIndexHigh, info.FileIndexLow),
			SHA256:       hex.EncodeToString(sum[:]),
		}
	}
	instance := toolchain.Instance{
		ID: "clang-cl", Family: toolchain.FamilyClangCL,
		CCompiler: paths[0], CXXCompiler: paths[0], Version: "18.1.8",
		Coverage: toolchain.CoverageCapability{
			LLVMProfdata: paths[1], LLVMCov: paths[2], CompilerEvidence: evidence[0],
			ProfdataEvidence: evidence[1], CovEvidence: evidence[2],
		},
	}
	instance.Coverage.ToolsetIdentity = toolchain.LLVMToolsetIdentity(instance.Version, paths, evidence)
	return instance
}
