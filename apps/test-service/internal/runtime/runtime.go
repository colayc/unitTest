package runtime

import (
	"context"
	"errors"
	"io"
	goruntime "runtime"
	"sync"
	"time"

	"unit-test-ide.local/test-service/internal/artifactstore"
	"unit-test-ide.local/test-service/internal/eventbroker"
	"unit-test-ide.local/test-service/internal/instance"
	"unit-test-ide.local/test-service/internal/processcontrol"
	"unit-test-ide.local/test-service/internal/session"
	"unit-test-ide.local/test-service/internal/task"
	"unit-test-ide.local/test-service/internal/taskstore"
)

const (
	brokerQueueSize = 256
	brokerPageSize  = 200
	closeTimeout    = 10 * time.Second
	adapterTimeout  = time.Second
)

type Config struct {
	DataDir           string
	ServiceExecutable string
	Platform          string
	Clock             task.Clock
	NewID             task.IDGenerator
	TerminationGrace  time.Duration
	dependencies      *dependencies
}

type Runtime struct {
	store               runtimeStore
	artifacts           runtimeArtifacts
	broker              *eventbroker.Broker
	manager             runtimeManager
	runner              processcontrol.Runner
	lock                io.Closer
	guard               io.Closer
	grace               time.Duration
	serviceExecutable   string
	simulationDirectory string

	shutdownMu          sync.Mutex
	shutdownRunning     bool
	shutdownAttemptDone chan struct{}
	shutdownComplete    bool
	shutdownErr         error
}

type runtimeStore interface {
	task.Store
}

type runtimeArtifacts interface {
	task.ArtifactWriter
	ReadChunk(context.Context, task.Artifact, int64, int) ([]byte, int64, bool, error)
	Cleanup(context.Context, map[string]struct{}) error
	Close() error
}

type runtimeManager interface {
	Start(context.Context, task.StartRequest) (task.Task, error)
	Get(context.Context, string) (task.Task, error)
	List(context.Context, string, int) (task.Page[task.Task], error)
	Cancel(context.Context, string) (task.Task, error)
	Shutdown(context.Context) error
}

type dependencies struct {
	prepareDataDir func(string) (Layout, io.Closer, error)
	lockInstance   func(string) (io.Closer, error)
	openStore      func(string) (runtimeStore, error)
	openArtifacts  func(string) (runtimeArtifacts, error)
	newRunner      func(string) processcontrol.Runner
	newBroker      func(eventbroker.Source, int, int) (*eventbroker.Broker, error)
	newManager     func(task.ManagerConfig) (runtimeManager, error)
}

func defaultDependencies() dependencies {
	return dependencies{
		prepareDataDir: prepareDataDirGuard,
		lockInstance:   instance.Lock,
		openStore: func(path string) (runtimeStore, error) {
			return taskstore.Open(path)
		},
		openArtifacts: func(path string) (runtimeArtifacts, error) {
			return artifactstore.New(path)
		},
		newRunner: processcontrol.NewRunner,
		newBroker: eventbroker.New,
		newManager: func(config task.ManagerConfig) (runtimeManager, error) {
			return task.NewManager(config)
		},
	}
}

func (d dependencies) complete() dependencies {
	defaults := defaultDependencies()
	if d.prepareDataDir == nil {
		d.prepareDataDir = defaults.prepareDataDir
	}
	if d.lockInstance == nil {
		d.lockInstance = defaults.lockInstance
	}
	if d.openStore == nil {
		d.openStore = defaults.openStore
	}
	if d.openArtifacts == nil {
		d.openArtifacts = defaults.openArtifacts
	}
	if d.newRunner == nil {
		d.newRunner = defaults.newRunner
	}
	if d.newBroker == nil {
		d.newBroker = defaults.newBroker
	}
	if d.newManager == nil {
		d.newManager = defaults.newManager
	}
	return d
}

func Open(config Config) (*Runtime, error) {
	if config.DataDir == "" || config.ServiceExecutable == "" || config.Platform != goruntime.GOOS {
		return nil, task.ErrInvalidArgument
	}
	deps := defaultDependencies()
	if config.dependencies != nil {
		deps = config.dependencies.complete()
	}
	newID := config.NewID
	if newID == nil {
		newID = task.NewID
	}
	grace := config.TerminationGrace
	if grace <= 0 {
		grace = 2 * time.Second
	}

	layout, guard, err := deps.prepareDataDir(config.DataDir)
	if err != nil {
		return nil, err
	}
	locked, err := deps.lockInstance(layout.Lock)
	if err != nil {
		return nil, errors.Join(err, guard.Close())
	}
	failLocked := func(cause error) (*Runtime, error) {
		return nil, errors.Join(cause, locked.Close(), guard.Close())
	}

	store, err := deps.openStore(layout.Database)
	if err != nil {
		return failLocked(err)
	}
	failStore := func(cause error) (*Runtime, error) {
		return nil, errors.Join(cause, store.Close(), locked.Close(), guard.Close())
	}
	artifacts, err := deps.openArtifacts(layout.Artifacts)
	if err != nil {
		return failStore(err)
	}
	failArtifacts := func(cause error) (*Runtime, error) {
		return nil, errors.Join(cause, artifacts.Close(), store.Close(), locked.Close(), guard.Close())
	}

	runner := deps.newRunner(config.ServiceExecutable)
	if runner == nil {
		return failArtifacts(task.ErrInvalidArgument)
	}
	ctx := context.Background()
	leases, err := store.ActiveLeases(ctx)
	if err != nil {
		return failArtifacts(err)
	}
	for _, lease := range leases {
		if err := runner.Cleanup(ctx, lease, grace); err != nil && !errors.Is(err, processcontrol.ErrLeaseIdentityMismatch) {
			return failArtifacts(err)
		}
	}

	if _, err := store.RecoverInterrupted(ctx, clockNow(config.Clock)); err != nil {
		return failArtifacts(err)
	}
	references, err := store.ReferencedArtifactPaths(ctx)
	if err != nil {
		return failArtifacts(err)
	}
	if err := artifacts.Cleanup(ctx, references); err != nil {
		return failArtifacts(err)
	}
	broker, err := deps.newBroker(store, brokerQueueSize, brokerPageSize)
	if err != nil {
		return failArtifacts(err)
	}
	manager, err := deps.newManager(task.ManagerConfig{
		Store: store, Publisher: broker, Processes: processFactory{runner: runner}, Artifacts: artifacts,
		Clock: config.Clock, NewID: newID, ServiceExecutable: config.ServiceExecutable,
		ServiceInstanceID: newID(), TerminationGrace: grace,
	})
	if err != nil {
		return failArtifacts(errors.Join(err, broker.Close()))
	}
	return &Runtime{
		store: store, artifacts: artifacts, broker: broker, manager: manager, runner: runner,
		lock: locked, guard: guard, grace: grace,
		serviceExecutable: config.ServiceExecutable, simulationDirectory: layout.Root,
	}, nil
}

func clockNow(clock task.Clock) time.Time {
	if clock == nil {
		return time.Now().UTC()
	}
	return clock.Now()
}

func (r *Runtime) StartSimulation(
	ctx context.Context,
	idempotencyKey string,
	scenario task.Scenario,
	timeout time.Duration,
) (task.Task, error) {
	request, err := task.NewSimulationStartRequest(
		idempotencyKey,
		scenario,
		timeout,
		r.serviceExecutable,
		r.simulationDirectory,
	)
	if err != nil {
		return task.Task{}, err
	}
	return r.manager.Start(ctx, request)
}

func (r *Runtime) Get(ctx context.Context, id string) (task.Task, error) {
	return r.manager.Get(ctx, id)
}

func (r *Runtime) List(ctx context.Context, cursor string, limit int) (task.Page[task.Task], error) {
	return r.manager.List(ctx, cursor, limit)
}

func (r *Runtime) Cancel(ctx context.Context, id string) (task.Task, error) {
	return r.manager.Cancel(ctx, id)
}

func (r *Runtime) Subscribe(ctx context.Context, afterSequence int64) (*eventbroker.Subscription, error) {
	return r.broker.Subscribe(ctx, afterSequence)
}

func (r *Runtime) ListArtifacts(ctx context.Context, taskID, cursor string, limit int) (task.Page[task.Artifact], error) {
	page, err := r.store.ListArtifacts(ctx, taskID, cursor, limit)
	if err != nil {
		return task.Page[task.Artifact]{}, err
	}
	for index := range page.Items {
		page.Items[index].RelativePath = ""
	}
	return page, nil
}

func (r *Runtime) ReadArtifact(ctx context.Context, artifactID string, offset int64, length int) (session.ArtifactChunk, error) {
	metadata, err := r.store.GetArtifact(ctx, artifactID)
	if err != nil {
		return session.ArtifactChunk{}, err
	}
	data, next, eof, err := r.artifacts.ReadChunk(ctx, metadata, offset, length)
	if err != nil {
		return session.ArtifactChunk{}, err
	}
	metadata.RelativePath = ""
	return session.ArtifactChunk{Data: data, NextOffset: next, EOF: eof, Metadata: metadata}, nil
}

func (r *Runtime) Shutdown(ctx context.Context) error {
	if r == nil {
		return nil
	}
	for {
		r.shutdownMu.Lock()
		if r.shutdownComplete {
			err := r.shutdownErr
			r.shutdownMu.Unlock()
			return err
		}
		if r.shutdownRunning {
			done := r.shutdownAttemptDone
			r.shutdownMu.Unlock()
			select {
			case <-done:
				continue
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		r.shutdownRunning = true
		r.shutdownAttemptDone = make(chan struct{})
		done := r.shutdownAttemptDone
		r.shutdownMu.Unlock()

		err := r.shutdownAttempt(ctx)
		r.shutdownMu.Lock()
		if err == nil {
			r.shutdownErr = r.closeResources()
			r.shutdownComplete = true
			err = r.shutdownErr
		}
		r.shutdownRunning = false
		close(done)
		r.shutdownMu.Unlock()
		return err
	}
}

func (r *Runtime) shutdownAttempt(ctx context.Context) error {
	if r.manager == nil {
		return nil
	}
	attempt, cancel := context.WithTimeout(ctx, r.grace)
	err := r.manager.Shutdown(attempt)
	cancel()
	if err == nil {
		return nil
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err := r.forceCleanup(ctx); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return err
	}
	return r.manager.Shutdown(ctx)
}

func (r *Runtime) closeResources() error {
	var brokerErr, artifactErr, storeErr, lockErr, guardErr error
	if r.broker != nil {
		brokerErr = r.broker.Close()
	}
	if r.artifacts != nil {
		artifactErr = r.artifacts.Close()
	}
	if r.store != nil {
		storeErr = r.store.Close()
	}
	if r.lock != nil {
		lockErr = r.lock.Close()
	}
	if r.guard != nil {
		guardErr = r.guard.Close()
	}
	return errors.Join(brokerErr, artifactErr, storeErr, lockErr, guardErr)
}

func (r *Runtime) forceCleanup(ctx context.Context) error {
	if r.store == nil || r.runner == nil {
		return nil
	}
	leases, err := r.store.ActiveLeases(ctx)
	if err != nil {
		return err
	}
	var cleanupErr error
	for _, lease := range leases {
		err := r.runner.Cleanup(ctx, lease, r.grace)
		if err != nil && !errors.Is(err, processcontrol.ErrLeaseIdentityMismatch) {
			cleanupErr = errors.Join(cleanupErr, err)
		}
	}
	return cleanupErr
}

func (r *Runtime) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), closeTimeout)
	defer cancel()
	return r.Shutdown(ctx)
}

var _ session.Backend = (*Runtime)(nil)

type processFactory struct{ runner processcontrol.Runner }

func (f processFactory) Prepare(ctx context.Context, spec task.ProcessSpec, taskID, serviceID string) (task.ManagedProcess, error) {
	process, err := f.runner.Prepare(ctx, processcontrol.Spec{
		Executable: spec.Executable,
		Args:       append([]string(nil), spec.Args...),
		Env:        append([]string(nil), spec.Env...),
		Dir:        spec.Dir,
	}, taskID, serviceID)
	if err != nil {
		return nil, err
	}
	return newManagedProcess(process), nil
}

type managedProcess struct {
	process       processcontrol.Process
	output        chan task.ProcessOutput
	done          chan task.ProcessResult
	stop          chan struct{}
	outputStopped chan struct{}
	doneStopped   chan struct{}

	closeMu       sync.Mutex
	closeRunning  bool
	closeComplete bool
	closeAttempt  *managedCloseAttempt
}

type managedCloseAttempt struct {
	done chan struct{}
	err  error
}

func newManagedProcess(process processcontrol.Process) *managedProcess {
	result := &managedProcess{
		process: process, output: make(chan task.ProcessOutput), done: make(chan task.ProcessResult, 1), stop: make(chan struct{}),
		outputStopped: make(chan struct{}), doneStopped: make(chan struct{}),
	}
	go func() {
		defer close(result.outputStopped)
		defer close(result.output)
		for {
			select {
			case <-result.stop:
				return
			case value, ok := <-process.Output():
				if !ok {
					return
				}
				select {
				case <-result.stop:
					return
				case result.output <- task.ProcessOutput{Stream: string(value.Stream), Data: append([]byte(nil), value.Data...)}:
				}
			}
		}
	}()
	go func() {
		defer close(result.doneStopped)
		defer close(result.done)
		for {
			select {
			case <-result.stop:
				return
			case value, ok := <-process.Done():
				if !ok {
					return
				}
				select {
				case <-result.stop:
					return
				case result.done <- task.ProcessResult{ExitCode: value.ExitCode, Err: value.Err}:
				}
			}
		}
	}()
	return result
}

func (p *managedProcess) Lease() task.ProcessLease          { return p.process.Lease() }
func (p *managedProcess) Start(ctx context.Context) error   { return p.process.Start(ctx) }
func (p *managedProcess) Output() <-chan task.ProcessOutput { return p.output }
func (p *managedProcess) Done() <-chan task.ProcessResult   { return p.done }
func (p *managedProcess) Terminate(ctx context.Context, grace time.Duration) error {
	return p.process.Terminate(ctx, grace)
}
func (p *managedProcess) Close(ctx context.Context) error {
	p.closeMu.Lock()
	if p.closeComplete {
		err := p.closeAttempt.err
		p.closeMu.Unlock()
		return err
	}
	if p.closeRunning {
		attempt := p.closeAttempt
		p.closeMu.Unlock()
		select {
		case <-attempt.done:
			return attempt.err
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	p.closeRunning = true
	attempt := &managedCloseAttempt{done: make(chan struct{})}
	p.closeAttempt = attempt
	select {
	case <-p.stop:
	default:
		close(p.stop)
	}
	p.closeMu.Unlock()

	closeCtx, cancel := context.WithTimeout(ctx, adapterTimeout)
	defer cancel()
	processErr := p.process.Close(closeCtx)
	waitErr := waitAdapterStopped(closeCtx, p.outputStopped, p.doneStopped)
	p.closeMu.Lock()
	attempt.err = errors.Join(processErr, waitErr)
	p.closeRunning = false
	p.closeComplete = attempt.err == nil
	close(attempt.done)
	err := attempt.err
	p.closeMu.Unlock()
	return err
}

func waitAdapterStopped(ctx context.Context, stopped ...<-chan struct{}) error {
	for _, done := range stopped {
		select {
		case <-done:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}
