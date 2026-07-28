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
	"unit-test-ide.local/test-service/internal/eventbroker"
	"unit-test-ide.local/test-service/internal/instance"
	"unit-test-ide.local/test-service/internal/processcontrol"
	"unit-test-ide.local/test-service/internal/task"
	"unit-test-ide.local/test-service/internal/taskstore"
)

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
		DataDir: root, ServiceExecutable: os.Args[0], Platform: platformForTest(),
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
		DataDir: root, ServiceExecutable: os.Args[0], Platform: platformForTest(),
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
	config := Config{DataDir: root, ServiceExecutable: os.Args[0], Platform: platformForTest(), dependencies: testDependencies(&recordingRunner{}, nil)}
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
	if active, err := Open(Config{DataDir: root, ServiceExecutable: os.Args[0], Platform: platformForTest(), dependencies: deps}); !errors.Is(err, managerFailure) || active != nil {
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

func TestRuntimeArtifactBackendHidesPathsAndVerifiesContent(t *testing.T) {
	root := filepath.Join(t.TempDir(), "data")
	layout, err := PrepareDataDir(root)
	if err != nil {
		t.Fatal(err)
	}
	artifact := seedFinishedArtifact(t, layout)
	active, err := Open(Config{DataDir: root, ServiceExecutable: os.Args[0], Platform: platformForTest(), dependencies: testDependencies(&recordingRunner{}, nil)})
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

func TestShutdownRetainsOwnershipUntilManagerQuiescesAndCanBeRetried(t *testing.T) {
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
	active, err := Open(Config{DataDir: root, ServiceExecutable: os.Args[0], Platform: platformForTest(), TerminationGrace: time.Millisecond, dependencies: testDependencies(runner, nil)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := active.StartSimulation(context.Background(), "shutdown-active", task.ScenarioHang, time.Minute); err != nil {
		t.Fatal(err)
	}
	subscription, err := active.Subscribe(context.Background(), 0)
	if err != nil {
		t.Fatal(err)
	}
	subscription.Activate()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := active.Shutdown(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Shutdown() error = %v, want deadline", err)
	}
	select {
	case <-cleanupStarted:
	case <-time.After(time.Second):
		t.Fatal("Shutdown did not begin forced lease cleanup")
	}
	select {
	case <-cleanupReturned:
	case <-time.After(100 * time.Millisecond):
		close(allowCleanup)
		t.Fatal("Shutdown left forced cleanup running beyond the caller deadline")
	}
	layout, err := PrepareDataDir(root)
	if err != nil {
		t.Fatal(err)
	}
	locked, err := instance.Lock(layout.Lock)
	if !errors.Is(err, instance.ErrAlreadyRunning) || locked != nil {
		if locked != nil {
			_ = locked.Close()
		}
		t.Fatalf("Shutdown released ownership before manager quiesced: lock=%#v error=%v", locked, err)
	}
	close(allowCleanup)
	if err := active.Close(); err != nil {
		t.Fatalf("retrying Close() after quiescence: %v", err)
	}
	assertClosed(t, subscription.Events)
	assertClosed(t, subscription.Errors)
	locked, err = instance.Lock(layout.Lock)
	if err != nil {
		t.Fatalf("Close retained instance lock after quiescence: %v", err)
	}
	_ = locked.Close()
	cleaned := runner.cleanupLeases()
	if len(cleaned) != 2 {
		t.Fatalf("forced cleanup leases = %#v", cleaned)
	}
	for _, lease := range cleaned {
		if lease.HostStartIdentity != "blocking-process" {
			t.Fatalf("forced cleanup lease = %#v", lease)
		}
	}
}

func TestShutdownRetryAfterProcessCloseFailureReleasesInstanceLock(t *testing.T) {
	root := filepath.Join(t.TempDir(), "data")
	process := newRetryCloseProcess()
	runner := &recordingRunner{prepared: process}
	active, err := Open(Config{
		DataDir: root, ServiceExecutable: os.Args[0], Platform: platformForTest(),
		TerminationGrace: time.Millisecond, dependencies: testDependencies(runner, nil),
	})
	if err != nil {
		t.Fatal(err)
	}
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(process.releaseClose) }) }
	t.Cleanup(func() { release(); _ = active.Close() })
	started, err := active.StartSimulation(context.Background(), "retry-close-runtime", task.ScenarioSuccess, time.Minute)
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
	if err := active.Shutdown(short); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Shutdown after transient Close failure = %v, want deadline", err)
	}
	locked, err := instance.Lock(layout.Lock)
	if !errors.Is(err, instance.ErrAlreadyRunning) || locked != nil {
		if locked != nil {
			_ = locked.Close()
		}
		t.Fatalf("failed Close released instance lock: lock=%#v error=%v", locked, err)
	}

	release()
	long, cancelLong := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancelLong()
	if err := active.Shutdown(long); err != nil {
		t.Fatalf("retry Shutdown = %v", err)
	}
	if calls := process.closeCalls.Load(); calls < 2 {
		t.Fatalf("process Close calls = %d, want retry", calls)
	}
	store, err := taskstore.Open(layout.Database)
	if err != nil {
		t.Fatal(err)
	}
	finished, getErr := store.Get(context.Background(), started.ID)
	closeErr := store.Close()
	if getErr != nil ||
		closeErr != nil ||
		finished.Status != task.StatusFinished ||
		finished.Outcome != task.OutcomeInfrastructureFailed {
		t.Fatalf("Task after Close retry = %#v, get=%v close=%v", finished, getErr, closeErr)
	}
	if leases := activeLeases(t, layout.Database); len(leases) != 0 {
		t.Fatalf("leases after Close retry = %#v", leases)
	}
	locked, err = instance.Lock(layout.Lock)
	if err != nil {
		t.Fatalf("retry Shutdown retained instance lock: %v", err)
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
}

func (r *recordingRunner) Prepare(context.Context, processcontrol.Spec, string, string) (processcontrol.Process, error) {
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
