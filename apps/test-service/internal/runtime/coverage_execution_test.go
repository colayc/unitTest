package runtime

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"unit-test-ide.local/test-service/internal/build"
	"unit-test-ide.local/test-service/internal/coveragecoord"
	"unit-test-ide.local/test-service/internal/coveragedomain"
	"unit-test-ide.local/test-service/internal/session"
	"unit-test-ide.local/test-service/internal/task"
	"unit-test-ide.local/test-service/internal/testdomain"
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
