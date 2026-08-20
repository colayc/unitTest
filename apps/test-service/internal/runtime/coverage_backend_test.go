package runtime

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"unit-test-ide.local/test-service/internal/coveragecoord"
	"unit-test-ide.local/test-service/internal/coveragedomain"
	"unit-test-ide.local/test-service/internal/coveragellvm"
	"unit-test-ide.local/test-service/internal/session"
	"unit-test-ide.local/test-service/internal/task"
	"unit-test-ide.local/test-service/internal/testdomain"
	"unit-test-ide.local/test-service/internal/toolchain"
)

type fakeCoverageQueue struct {
	called int
	input  coveragecoord.QueuedStartInput
	result coveragecoord.QueuedStartResult
	err    error
	steps  *[]string
}

func (queue *fakeCoverageQueue) Start(_ context.Context, input coveragecoord.QueuedStartInput) (coveragecoord.QueuedStartResult, error) {
	if queue.steps != nil {
		*queue.steps = append(*queue.steps, "persist")
	}
	queue.called++
	queue.input = input
	return queue.result, queue.err
}

type fakeCoverageRepository struct {
	task    task.Task
	run     coveragedomain.Run
	testRun testdomain.TestRun
	page    coveragedomain.RunPage
	report  coveragedomain.Report
	steps   *[]string
}

func (repository *fakeCoverageRepository) Get(context.Context, string) (task.Task, error) {
	if repository.steps != nil {
		*repository.steps = append(*repository.steps, "reload-task")
	}
	return repository.task, nil
}

func (repository *fakeCoverageRepository) GetRunForTask(context.Context, string) (testdomain.TestRun, error) {
	if repository.steps != nil {
		*repository.steps = append(*repository.steps, "reload-test-run")
	}
	return repository.testRun, nil
}

func (repository *fakeCoverageRepository) GetCoverageRun(context.Context, string) (coveragedomain.Run, error) {
	if repository.steps != nil {
		*repository.steps = append(*repository.steps, "reload-coverage-run")
	}
	return repository.run, nil
}

func (repository *fakeCoverageRepository) ListCoverageRuns(context.Context, coveragedomain.RunPageRequest) (coveragedomain.RunPage, error) {
	return repository.page, nil
}

type fakeCoverageExecutor struct {
	resumed      []string
	unsupported  []string
	resumeResult task.Task
	resumeErr    error
	closeCalls   int
	steps        *[]string
	onClose      func()
}

func (executor *fakeCoverageExecutor) Resume(_ context.Context, persisted task.Task) (task.Task, error) {
	if executor.steps != nil {
		*executor.steps = append(*executor.steps, "resume")
	}
	executor.resumed = append(executor.resumed, persisted.ID)
	return executor.resumeResult, executor.resumeErr
}

func (executor *fakeCoverageExecutor) FinishUnsupported(_ context.Context, persisted task.Task) (task.Task, error) {
	executor.unsupported = append(executor.unsupported, persisted.ID)
	return executor.resumeResult, executor.resumeErr
}

func (executor *fakeCoverageExecutor) Close() error {
	executor.closeCalls++
	if executor.onClose != nil {
		executor.onClose()
	}
	return nil
}

func (repository *fakeCoverageRepository) GetCoverageReport(context.Context, string) (coveragedomain.Report, error) {
	return repository.report, nil
}

func TestRuntimeCoverageBackendPersistsBeforeResumeAndReturnsCanonicalGraph(t *testing.T) {
	var steps []string
	queue := &fakeCoverageQueue{steps: &steps, result: coveragecoord.QueuedStartResult{
		Task:    task.Task{ID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Status: task.StatusQueued},
		Run:     coveragedomain.Run{ID: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", TaskID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", TestRunID: "cccccccccccccccccccccccccccccccc", Status: coveragedomain.StatusQueued},
		TestRun: testdomain.TestRun{RunID: "cccccccccccccccccccccccccccccccc", TaskID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Status: testdomain.RunQueued},
	}}
	resolver := func(context.Context, session.CoverageRunStart) (coveragecoord.QueuedStartInput, error) {
		return coveragecoord.QueuedStartInput{BuildProfileID: "profile", ToolchainID: "toolchain"}, nil
	}
	executor := &fakeCoverageExecutor{
		resumeResult: task.Task{ID: queue.result.Task.ID, Status: task.StatusRunning},
		steps:        &steps,
	}
	repository := &fakeCoverageRepository{
		task: executor.resumeResult,
		run: coveragedomain.Run{
			ID: queue.result.Run.ID, TaskID: queue.result.Task.ID,
			TestRunID: queue.result.TestRun.RunID, Status: coveragedomain.StatusRunning,
		},
		testRun: testdomain.TestRun{
			RunID: queue.result.TestRun.RunID, TaskID: queue.result.Task.ID,
			Status: testdomain.RunRunning,
		},
	}
	backend, err := newRuntimeCoverageBackend(queue, repository, resolver, executor)
	if err != nil {
		t.Fatalf("newRuntimeCoverageBackend() error = %v", err)
	}
	taskValue, run, testRun, err := backend.StartCoverageRun(context.Background(), session.CoverageRunStart{})
	if err != nil {
		t.Fatalf("StartCoverageRun() error = %v", err)
	}
	if queue.called != 1 || !reflect.DeepEqual(executor.resumed, []string{queue.result.Task.ID}) ||
		taskValue.Status != task.StatusRunning || run.Status != coveragedomain.StatusRunning || testRun.Status != testdomain.RunRunning {
		t.Fatalf("resumed result = task=%#v run=%#v testRun=%#v calls=%d resumed=%v", taskValue, run, testRun, queue.called, executor.resumed)
	}
	if want := []string{"persist", "resume"}; !reflect.DeepEqual(steps, want) {
		t.Fatalf("start steps = %v, want %v", steps, want)
	}
}

func TestRuntimeCoverageBackendReloadsCanonicalPersistedGraphWhenResumeFails(t *testing.T) {
	var steps []string
	queuedTask := task.Task{ID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Status: task.StatusQueued}
	queuedRun := coveragedomain.Run{
		ID: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", TaskID: queuedTask.ID,
		TestRunID: "cccccccccccccccccccccccccccccccc", Status: coveragedomain.StatusQueued,
	}
	queuedTestRun := testdomain.TestRun{RunID: queuedRun.TestRunID, TaskID: queuedTask.ID, Status: testdomain.RunQueued}
	queue := &fakeCoverageQueue{steps: &steps, result: coveragecoord.QueuedStartResult{Task: queuedTask, Run: queuedRun, TestRun: queuedTestRun}}
	wantErr := errors.New("resume failed after persistence")
	executor := &fakeCoverageExecutor{resumeErr: wantErr, steps: &steps}
	repository := &fakeCoverageRepository{task: queuedTask, run: queuedRun, testRun: queuedTestRun, steps: &steps}
	backend, err := newRuntimeCoverageBackend(queue, repository, func(context.Context, session.CoverageRunStart) (coveragecoord.QueuedStartInput, error) {
		return coveragecoord.QueuedStartInput{}, nil
	}, executor)
	if err != nil {
		t.Fatal(err)
	}
	gotTask, gotRun, gotTestRun, err := backend.StartCoverageRun(context.Background(), session.CoverageRunStart{})
	if !errors.Is(err, wantErr) {
		t.Fatalf("StartCoverageRun() error = %v, want %v", err, wantErr)
	}
	if gotTask.ID != queuedTask.ID || gotRun.ID != queuedRun.ID || gotTestRun.RunID != queuedTestRun.RunID {
		t.Fatalf("canonical graph = %#v / %#v / %#v", gotTask, gotRun, gotTestRun)
	}
	wantSteps := []string{"persist", "resume", "reload-task", "reload-coverage-run", "reload-test-run"}
	if !reflect.DeepEqual(steps, wantSteps) {
		t.Fatalf("resume failure steps = %v, want %v", steps, wantSteps)
	}
}

func TestRuntimeCoverageBackendRejectsResolverFailureBeforeQueue(t *testing.T) {
	queue := &fakeCoverageQueue{}
	want := errors.New("coverage identity is stale")
	backend, err := newRuntimeCoverageBackend(queue, &fakeCoverageRepository{}, func(context.Context, session.CoverageRunStart) (coveragecoord.QueuedStartInput, error) {
		return coveragecoord.QueuedStartInput{}, want
	}, &fakeCoverageExecutor{})
	if err != nil {
		t.Fatalf("newRuntimeCoverageBackend() error = %v", err)
	}
	if _, _, _, err := backend.StartCoverageRun(context.Background(), session.CoverageRunStart{}); !errors.Is(err, want) {
		t.Fatalf("StartCoverageRun() error = %v, want %v", err, want)
	}
	if queue.called != 0 {
		t.Fatalf("queue calls = %d, want 0", queue.called)
	}
}

func TestRuntimeCoverageBackendDelegatesCanonicalReads(t *testing.T) {
	wantRun := coveragedomain.Run{ID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}
	wantPage := coveragedomain.RunPage{NextCursor: "next"}
	wantReport := coveragedomain.Report{ID: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}
	backend, err := newRuntimeCoverageBackend(&fakeCoverageQueue{}, &fakeCoverageRepository{run: wantRun, page: wantPage, report: wantReport}, func(context.Context, session.CoverageRunStart) (coveragecoord.QueuedStartInput, error) {
		return coveragecoord.QueuedStartInput{}, nil
	}, &fakeCoverageExecutor{})
	if err != nil {
		t.Fatalf("newRuntimeCoverageBackend() error = %v", err)
	}
	if got, _ := backend.GetCoverageRun(context.Background(), wantRun.ID); got.ID != wantRun.ID {
		t.Fatalf("GetCoverageRun() = %#v", got)
	}
	if got, _ := backend.ListCoverageRuns(context.Background(), coveragedomain.RunPageRequest{}); got.NextCursor != wantPage.NextCursor {
		t.Fatalf("ListCoverageRuns() = %#v", got)
	}
	if got, _ := backend.GetCoverageReport(context.Background(), wantReport.ID); got.ID != wantReport.ID {
		t.Fatalf("GetCoverageReport() = %#v", got)
	}
}

func TestCoverageToolchainSnapshotAcceptsOnlySupportedPlatformFamilies(t *testing.T) {
	valid := []struct {
		name     string
		platform string
		family   toolchain.Family
		compiler coveragedomain.CompilerFamily
		driver   coveragedomain.DriverName
		collect  coveragedomain.CollectorName
	}{
		{"windows clang-cl", "windows", toolchain.FamilyClangCL, coveragedomain.CompilerFamilyClangCL, coveragedomain.DriverLLVMCov, coveragedomain.CollectorLLVMCov},
		{"linux gcc", "linux", toolchain.FamilyGCC, coveragedomain.CompilerFamilyGCC, coveragedomain.DriverGCov, coveragedomain.CollectorGCovr},
		{"linux clang", "linux", toolchain.FamilyClang, coveragedomain.CompilerFamilyClang, coveragedomain.DriverLLVMCov, coveragedomain.CollectorLLVMCov},
	}
	for _, test := range valid {
		t.Run(test.name, func(t *testing.T) {
			got, err := coverageToolchainSnapshot(toolchain.Instance{ID: "toolchain", Family: test.family, Version: "18.1.8", TargetArchitecture: "amd64"}, test.platform)
			if err != nil {
				t.Fatalf("coverageToolchainSnapshot() error = %v", err)
			}
			if got.Compiler.Family != test.compiler || got.Driver.Name != test.driver || got.Collector.Name != test.collect || len(got.InstrumentationFingerprint) != 64 {
				t.Fatalf("snapshot = %#v", got)
			}
		})
	}
	if _, err := coverageToolchainSnapshot(toolchain.Instance{ID: "toolchain", Family: toolchain.FamilyMSVC, Version: "19.0", TargetArchitecture: "amd64"}, "windows"); !errors.Is(err, coveragedomain.ErrInvalidToolchain) {
		t.Fatalf("unsupported family error = %v", err)
	}
}

func TestWindowsCoverageProducerUsesRetainedInstrumentationContract(t *testing.T) {
	first, err := coverageToolchainSnapshot(toolchain.Instance{
		ID: "first-toolchain", Family: toolchain.FamilyClangCL,
		Version: "18.1.8", TargetArchitecture: "amd64",
	}, "windows")
	if err != nil {
		t.Fatal(err)
	}
	second, err := coverageToolchainSnapshot(toolchain.Instance{
		ID: "replacement-toolchain", Family: toolchain.FamilyClangCL,
		Version: "19.0.1", TargetArchitecture: "amd64",
	}, "windows")
	if err != nil {
		t.Fatal(err)
	}
	want := coveragellvm.InstrumentationFingerprint()
	if first.InstrumentationFingerprint != want || second.InstrumentationFingerprint != want {
		t.Fatalf("producer fingerprints = %q, %q; retained contract = %q",
			first.InstrumentationFingerprint, second.InstrumentationFingerprint, want)
	}
	if first.Compiler.Version == second.Compiler.Version {
		t.Fatal("toolchain version identity was not preserved separately")
	}
}
