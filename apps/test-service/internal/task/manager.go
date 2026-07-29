package task

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"unit-test-ide.local/test-service/internal/diagnostic"
)

const (
	defaultTerminationGrace = 2 * time.Second
	defaultFlushInterval    = 25 * time.Millisecond
	defaultCommandQueue     = 256
	maxOutputBlock          = 16 * 1024
	maxPersistedOutput      = 4 * 1024 * 1024
)

type ManagerConfig struct {
	Store               Store
	Publisher           Publisher
	Processes           ProcessFactory
	Artifacts           ArtifactWriter
	StepObserver        StepObserver
	Clock               Clock
	NewID               IDGenerator
	ServiceExecutable   string
	ServiceInstanceID   string
	TerminationGrace    time.Duration
	ProcessCloseTimeout time.Duration
	OutputFlushInterval time.Duration
	CommandQueue        int
}

type Manager struct {
	store               Store
	publisher           Publisher
	processes           ProcessFactory
	artifacts           ArtifactWriter
	stepObserver        StepObserver
	clock               Clock
	newID               IDGenerator
	serviceExecutable   string
	serviceInstanceID   string
	terminationGrace    time.Duration
	processCloseTimeout time.Duration
	outputFlushInterval time.Duration
	commands            chan any
	shutdownSignal      chan struct{}
	stopped             chan struct{}
	healthy             atomic.Bool
	closing             atomic.Bool
	shutdownPending     atomic.Bool
	executionSignals    sync.Map
	storageFailed       bool // command-loop owned
	publisherFailed     bool // command-loop owned
}

type startCommand struct {
	request StartRequest
	reply   chan taskResponse
}

type resumeCommand struct {
	request ResumeRequest
	reply   chan taskResponse
}

type taskIDCommand struct {
	id     string
	cancel bool
	reply  chan taskResponse
}

type listCommand struct {
	cursor string
	limit  int
	kinds  []Kind
	reply  chan listResponse
}

type shutdownCommand struct{}
type outputCommand struct {
	taskID string
	value  ProcessOutput
}
type processDoneCommand struct {
	taskID string
	result ProcessResult
}
type flushCommand struct {
	taskID string
	token  uint64
}
type terminationResultCommand struct {
	taskID     string
	generation uint64
	err        error
}
type closeResultCommand struct {
	taskID     string
	generation uint64
	err        error
}

type taskResponse struct {
	task Task
	err  error
}

type listResponse struct {
	page Page[Task]
	err  error
}

type outputSegment struct {
	stream string
	data   []byte
}

type activeTask struct {
	task                  Task
	plan                  ExecutionPlan
	boundary              ExecutionBoundary
	artifactSink          ArtifactSink
	boundaryReleased      bool
	nextStep              int
	process               ManagedProcess
	pendingCompletion     *pendingProcessCompletion
	leasePersisted        bool
	terminating           bool
	segments              []outputSegment
	bufferedBytes         int
	persistedBytes        int
	truncated             bool
	flushPending          bool
	flushToken            uint64
	timerStop             chan struct{}
	timeoutStop           chan struct{}
	watcherStop           chan struct{}
	processCompleted      bool
	terminationGeneration uint64
	terminationComplete   bool
	terminationFailed     bool
	closeStarted          bool
	closeComplete         bool
	closeGeneration       uint64
	closeFailed           bool
	recoveryRequired      bool
	cleanupWithoutDone    bool
	failPendingStep       bool
	stoppedWatchers       bool
	execution             *executionSignal
}

type executionSignal struct {
	mu                 sync.Mutex
	ctx                context.Context
	cancel             context.CancelFunc
	requested          Outcome
	outcome            Outcome
	cancelDelivery     bool
	cancelCompleted    bool
	cancelResponse     taskResponse
	cancelReplyWaiters []chan taskResponse
}

func newExecutionSignal() *executionSignal {
	ctx, cancel := context.WithCancel(context.Background())
	return &executionSignal{ctx: ctx, cancel: cancel}
}

func (s *executionSignal) claim(cause Outcome) Outcome {
	if s == nil {
		return cause
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.outcome != "" {
		return s.outcome
	}
	if s.requested == "" {
		s.requested = cause
		s.cancel()
	}
	return s.requested
}

func (s *executionSignal) currentCause() Outcome {
	if s == nil {
		return ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.outcome != "" {
		return s.outcome
	}
	return s.requested
}

func (s *executionSignal) state() (requested, outcome Outcome) {
	if s == nil {
		return "", ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.requested, s.outcome
}

func (s *executionSignal) resolve(fallback Outcome) Outcome {
	if s == nil {
		return fallback
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.outcome == "" {
		if s.requested != "" {
			s.outcome = s.requested
		} else {
			s.outcome = fallback
		}
		s.cancel()
	}
	return s.outcome
}

func (s *executionSignal) replaceOutcome(outcome Outcome) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.outcome = outcome
	s.cancel()
}

func (s *executionSignal) stop() {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cancel()
}

func (s *executionSignal) acceptCancellation(reply chan taskResponse) (deliver bool, completed *taskResponse) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.outcome == "" && s.requested == "" {
		s.requested = OutcomeCancelled
		s.cancel()
	}
	if s.cancelCompleted {
		response := s.cancelResponse
		return false, &response
	}
	s.cancelReplyWaiters = append(s.cancelReplyWaiters, reply)
	if !s.cancelDelivery {
		s.cancelDelivery = true
		return true, nil
	}
	return false, nil
}

func (s *executionSignal) completeCancellation(response taskResponse) {
	s.mu.Lock()
	if s.cancelCompleted {
		s.mu.Unlock()
		return
	}
	s.cancelCompleted = true
	s.cancelResponse = response
	waiters := s.cancelReplyWaiters
	s.cancelReplyWaiters = nil
	s.mu.Unlock()
	for _, waiter := range waiters {
		waiter <- response
	}
}

func NewManager(config ManagerConfig) (*Manager, error) {
	if config.Store == nil || config.Publisher == nil || config.Processes == nil || config.Artifacts == nil ||
		config.ServiceExecutable == "" || config.ServiceInstanceID == "" {
		return nil, ErrInvalidArgument
	}
	if config.Clock == nil {
		config.Clock = RealClock{}
	}
	if config.NewID == nil {
		config.NewID = NewID
	}
	if config.TerminationGrace <= 0 {
		config.TerminationGrace = defaultTerminationGrace
	}
	if config.OutputFlushInterval < defaultFlushInterval {
		config.OutputFlushInterval = defaultFlushInterval
	}
	if config.ProcessCloseTimeout <= 0 {
		config.ProcessCloseTimeout = config.TerminationGrace + 2*time.Second
	}
	if config.CommandQueue <= 0 {
		config.CommandQueue = defaultCommandQueue
	}
	manager := &Manager{
		store: config.Store, publisher: config.Publisher, processes: config.Processes, artifacts: config.Artifacts,
		stepObserver: config.StepObserver,
		clock:        config.Clock, newID: config.NewID, serviceExecutable: config.ServiceExecutable,
		serviceInstanceID: config.ServiceInstanceID, terminationGrace: config.TerminationGrace, processCloseTimeout: config.ProcessCloseTimeout,
		outputFlushInterval: config.OutputFlushInterval, commands: make(chan any, config.CommandQueue),
		shutdownSignal: make(chan struct{}, 1), stopped: make(chan struct{}),
	}
	manager.healthy.Store(true)
	go manager.loop()
	return manager, nil
}

func (m *Manager) Healthy() bool { return m != nil && m.healthy.Load() && !m.closing.Load() }

func (m *Manager) Start(ctx context.Context, request StartRequest) (Task, error) {
	if m == nil {
		return Task{}, ErrInvalidArgument
	}
	if err := validateStartRequest(request); err != nil {
		return Task{}, ErrInvalidArgument
	}
	request = cloneStartRequest(request)
	if !m.Healthy() {
		return Task{}, ErrStorageUnavailable
	}
	reply := make(chan taskResponse, 1)
	if err := m.send(ctx, startCommand{request: request, reply: reply}); err != nil {
		return Task{}, err
	}
	select {
	case response := <-reply:
		return response.task, publicError(response.err)
	case <-ctx.Done():
		return Task{}, ctx.Err()
	case <-m.stopped:
		return Task{}, ErrStorageUnavailable
	}
}

func (m *Manager) ResumeQueued(ctx context.Context, request ResumeRequest) (Task, error) {
	if m == nil || request.Task.ID == "" || request.Task.Kind != KindCMakeBuild ||
		request.Task.Status != StatusQueued ||
		request.Plan.Fingerprint == "" ||
		request.Plan.Fingerprint != FingerprintPlan(request.Plan) ||
		ValidatePlan(request.Plan, request.Boundary) != nil {
		return Task{}, ErrInvalidArgument
	}
	request.Task.Request = append(json.RawMessage(nil), request.Task.Request...)
	request.Task.Steps = append([]StepSnapshot(nil), request.Task.Steps...)
	request.Plan = cloneExecutionPlan(request.Plan)
	if !m.Healthy() {
		return Task{}, ErrStorageUnavailable
	}
	reply := make(chan taskResponse, 1)
	if err := m.send(ctx, resumeCommand{request: request, reply: reply}); err != nil {
		return Task{}, err
	}
	select {
	case response := <-reply:
		return response.task, publicError(response.err)
	case <-ctx.Done():
		return Task{}, ctx.Err()
	case <-m.stopped:
		return Task{}, ErrStorageUnavailable
	}
}

func (m *Manager) Get(ctx context.Context, id string) (Task, error) {
	if m == nil || id == "" {
		return Task{}, ErrInvalidArgument
	}
	reply := make(chan taskResponse, 1)
	if err := m.send(ctx, taskIDCommand{id: id, reply: reply}); err != nil {
		return Task{}, err
	}
	select {
	case response := <-reply:
		return response.task, publicError(response.err)
	case <-ctx.Done():
		return Task{}, ctx.Err()
	case <-m.stopped:
		return Task{}, ErrStorageUnavailable
	}
}

func (m *Manager) List(ctx context.Context, cursor string, limit int, kinds ...Kind) (Page[Task], error) {
	if m == nil || limit < 1 || len(kinds) > 2 {
		return Page[Task]{}, ErrInvalidArgument
	}
	reply := make(chan listResponse, 1)
	if err := m.send(ctx, listCommand{
		cursor: cursor,
		limit:  limit,
		kinds:  append([]Kind(nil), kinds...),
		reply:  reply,
	}); err != nil {
		return Page[Task]{}, err
	}
	select {
	case response := <-reply:
		return response.page, publicError(response.err)
	case <-ctx.Done():
		return Page[Task]{}, ctx.Err()
	case <-m.stopped:
		return Page[Task]{}, ErrStorageUnavailable
	}
}

func (m *Manager) Cancel(ctx context.Context, id string) (Task, error) {
	if m == nil || id == "" {
		return Task{}, ErrInvalidArgument
	}
	if err := ctx.Err(); err != nil {
		return Task{}, err
	}
	reply := make(chan taskResponse, 1)
	if value, ok := m.executionSignals.Load(id); ok {
		signal := value.(*executionSignal)
		deliver, completed := signal.acceptCancellation(reply)
		if completed != nil {
			reply <- *completed
		} else if deliver {
			// Once a live request is accepted, caller cancellation only bounds
			// waiting; one signal-owned command continues for all callers.
			go m.deliverCancellation(id, signal)
		}
	} else if err := m.send(ctx, taskIDCommand{id: id, cancel: true, reply: reply}); err != nil {
		return Task{}, err
	}
	select {
	case response := <-reply:
		return response.task, publicError(response.err)
	case <-ctx.Done():
		return Task{}, ctx.Err()
	case <-m.stopped:
		return Task{}, ErrStorageUnavailable
	}
}

func (m *Manager) deliverCancellation(id string, signal *executionSignal) {
	reply := make(chan taskResponse, 1)
	if !m.sendInternal(taskIDCommand{id: id, cancel: true, reply: reply}) {
		signal.completeCancellation(taskResponse{err: ErrStorageUnavailable})
		return
	}
	select {
	case response := <-reply:
		signal.completeCancellation(response)
	case <-m.stopped:
		signal.completeCancellation(taskResponse{err: ErrStorageUnavailable})
	}
}

func (m *Manager) Shutdown(ctx context.Context) error {
	if m == nil {
		return nil
	}
	select {
	case <-m.stopped:
		return nil
	default:
	}
	m.closing.Store(true)
	m.healthy.Store(false)
	m.executionSignals.Range(func(_, value any) bool {
		value.(*executionSignal).claim(OutcomeInterrupted)
		return true
	})
	if m.shutdownPending.CompareAndSwap(false, true) {
		select {
		case m.shutdownSignal <- struct{}{}:
		default:
		}
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-m.stopped:
		if err := ctx.Err(); err != nil {
			return err
		}
		return nil
	}
}

func (m *Manager) send(ctx context.Context, command any) error {
	select {
	case m.commands <- command:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-m.stopped:
		return ErrStorageUnavailable
	}
}

func (m *Manager) sendInternal(command any) bool {
	select {
	case m.commands <- command:
		return true
	case <-m.stopped:
		return false
	}
}

func (m *Manager) loop() {
	active := make(map[string]*activeTask)
	shutdownInitiated := false
	for {
		var command any
		select {
		case <-m.shutdownSignal:
			command = shutdownCommand{}
		default:
			select {
			case <-m.shutdownSignal:
				command = shutdownCommand{}
			case command = <-m.commands:
			}
		}
		switch value := command.(type) {
		case startCommand:
			value.reply <- m.start(value.request, active)
		case resumeCommand:
			value.reply <- m.resumeQueued(value.request, active)
		case taskIDCommand:
			if value.cancel {
				value.reply <- m.cancel(value.id, active)
			} else {
				got, err := m.store.Get(context.Background(), value.id)
				value.reply <- taskResponse{task: got, err: err}
			}
		case listCommand:
			page, err := m.store.List(context.Background(), value.cursor, value.limit, value.kinds...)
			value.reply <- listResponse{page: page, err: err}
		case outputCommand:
			if current := active[value.taskID]; !m.circuitFailed() && current != nil && !current.processCompleted {
				m.acceptOutput(current, value.value, active)
			}
		case flushCommand:
			if current := active[value.taskID]; !m.circuitFailed() && current != nil &&
				current.flushPending && current.flushToken == value.token {
				current.flushPending = false
				m.flushOutput(current, active)
			}
		case timeoutCommand:
			if current := active[string(value)]; current != nil && current.task.Status != StatusFinished {
				cause := current.execution.resolve(OutcomeTimedOut)
				if cause != OutcomeTimedOut {
					break
				}
				if current.process == nil {
					if _, err := m.finishExecution(current, ProcessResult{}, cause, false, active); err != nil {
						m.abandon(current)
					}
					if active[string(value)] == current && current.task.Status == StatusFinished {
						removeActiveTask(active, string(value))
					}
					break
				}
				if current.cleanupWithoutDone && !current.terminating {
					m.terminate(current)
					m.stageProcessCompletion(
						current,
						ProcessResult{Err: context.DeadlineExceeded},
						current.failPendingStep,
					)
				} else if current.processCompleted {
					m.maybeStartClose(current)
				} else {
					m.terminate(current)
				}
			}
		case processDoneCommand:
			if current := active[value.taskID]; current != nil && !current.processCompleted {
				m.finish(current, value.result, active)
				if active[value.taskID] == current && m.canRemove(current) {
					removeActiveTask(active, value.taskID)
				}
			}
		case terminationResultCommand:
			if current := active[value.taskID]; current != nil && current.terminationGeneration == value.generation {
				current.terminationComplete = true
				if value.err != nil {
					current.terminationFailed = true
					m.healthy.Store(false)
					m.stageProcessCompletion(
						current,
						ProcessResult{Err: value.err},
						current.failPendingStep,
					)
				}
				m.maybeStartClose(current)
			}
		case closeResultCommand:
			if current := active[value.taskID]; current != nil && current.closeGeneration == value.generation {
				if value.err != nil {
					m.recordCloseFailure(current)
					recoveryHandoffSafe := current.task.Status != StatusFinished &&
						current.leasePersisted
					if (m.circuitFailed() || current.recoveryRequired) && recoveryHandoffSafe {
						current.recoveryRequired = true
						m.stopActive(current)
						removeActiveTask(active, value.taskID)
					} else {
						current.closeStarted = false
						current.closeComplete = false
						current.closeFailed = true
					}
				} else {
					current.closeComplete = true
					current.closeFailed = false
					if !m.circuitFailed() && !current.recoveryRequired {
						if err := m.commitClosedCompletion(current, active); err != nil {
							m.abandon(current)
						}
					}
				}
				if value.err == nil && m.canRemove(current) {
					removeActiveTask(active, value.taskID)
				}
			}
		case shutdownCommand:
			m.shutdownPending.Store(false)
			initiate := !shutdownInitiated
			shutdownInitiated = true
			for _, current := range active {
				if initiate && current.task.Status != StatusFinished &&
					current.execution.claim(OutcomeInterrupted) == OutcomeInterrupted {
					if current.process == nil {
						if _, err := m.finishExecution(
							current,
							ProcessResult{},
							OutcomeInterrupted,
							false,
							active,
						); err != nil {
							m.abandon(current)
						}
						if active[current.task.ID] == current && current.task.Status == StatusFinished {
							removeActiveTask(active, current.task.ID)
						}
					} else if current.cleanupWithoutDone && !current.terminationComplete {
						if !current.terminating {
							m.terminate(current)
						}
						m.stageProcessCompletion(
							current,
							ProcessResult{Err: context.Canceled},
							current.failPendingStep,
						)
					} else if current.processCompleted {
						m.maybeStartClose(current)
					} else {
						m.terminate(current)
					}
				}
				if current.closeFailed {
					m.retryClose(current)
				}
			}
		}
		if m.closing.Load() && len(active) == 0 {
			close(m.stopped)
			return
		}
	}
}

func (m *Manager) cancel(id string, active map[string]*activeTask) taskResponse {
	if m.circuitFailed() {
		return taskResponse{err: ErrStorageUnavailable}
	}
	stored, err := m.store.Get(context.Background(), id)
	if err != nil {
		return taskResponse{err: err}
	}
	if stored.Status == StatusFinished {
		return taskResponse{task: stored}
	}
	current := active[id]
	if current == nil {
		return taskResponse{task: stored, err: ErrConflict}
	}
	requested, outcome := current.execution.state()
	if outcome != "" || (requested != "" && requested != OutcomeCancelled) {
		return taskResponse{task: current.task}
	}
	if cause := current.execution.claim(OutcomeCancelled); cause != OutcomeCancelled {
		return taskResponse{task: current.task}
	}
	if current.task.Status == StatusQueued {
		current.execution.resolve(OutcomeCancelled)
		if current.process == nil {
			finished, finishErr := m.finishExecution(current, ProcessResult{}, OutcomeCancelled, false, active)
			if finishErr != nil {
				m.abandon(current)
				return taskResponse{task: current.task, err: finishErr}
			}
			if active[id] == current && current.task.Status == StatusFinished {
				removeActiveTask(active, id)
			}
			return taskResponse{task: finished}
		}
		current.cleanupWithoutDone = true
		m.terminate(current)
		m.stageProcessCompletion(
			current,
			ProcessResult{Err: context.Canceled},
			current.failPendingStep,
		)
		return taskResponse{task: current.task}
	}
	now := m.clock.Now()
	cancelling, err := ApplyTransition(current.task, Transition{From: StatusRunning, To: StatusCancelling, At: now})
	if err != nil {
		return taskResponse{task: current.task, err: err}
	}
	cancelling, events, err := m.store.Apply(context.Background(), Mutation{
		Task: cancelling, Expected: StatusRunning,
		Events: []EventDraft{eventDraft(id, EventTaskCancellationRequested, now, map[string]any{"status": StatusCancelling})},
	})
	if err != nil {
		if errors.Is(err, ErrStorageUnavailable) {
			m.tripStorage(active)
		} else if errors.Is(err, ErrConflict) {
			current.execution.replaceOutcome(OutcomeInfrastructureFailed)
			current.failPendingStep = false
			m.finishCancellationConflict(current, active)
		}
		return taskResponse{task: current.task, err: err}
	}
	current.task = cancelling
	current.execution.resolve(OutcomeCancelled)
	if !m.publishAll(events) {
		m.tripPublisher(active)
		return taskResponse{task: current.task, err: ErrStorageUnavailable}
	}
	if current.process == nil {
		finished, finishErr := m.finishExecution(current, ProcessResult{}, OutcomeCancelled, false, active)
		if finishErr != nil {
			m.abandon(current)
			return taskResponse{task: current.task, err: finishErr}
		}
		if active[id] == current && current.task.Status == StatusFinished {
			removeActiveTask(active, id)
		}
		return taskResponse{task: finished}
	}
	if current.cleanupWithoutDone && !current.terminating {
		m.terminate(current)
		m.stageProcessCompletion(
			current,
			ProcessResult{Err: context.Canceled},
			current.failPendingStep,
		)
	} else if current.processCompleted {
		m.maybeStartClose(current)
	} else {
		m.terminate(current)
	}
	return taskResponse{task: current.task}
}

func (m *Manager) finishCancellationConflict(current *activeTask, active map[string]*activeTask) {
	if current.process == nil {
		if _, err := m.finishExecution(
			current,
			ProcessResult{Err: ErrConflict},
			OutcomeInfrastructureFailed,
			false,
			active,
		); err != nil {
			m.abandon(current)
		}
		if active[current.task.ID] == current && current.task.Status == StatusFinished {
			removeActiveTask(active, current.task.ID)
		}
		return
	}
	if current.cleanupWithoutDone && !current.terminating {
		m.terminate(current)
		m.stageProcessCompletion(
			current,
			ProcessResult{Err: ErrConflict},
			current.failPendingStep,
		)
	} else if current.processCompleted {
		m.maybeStartClose(current)
	} else {
		m.terminate(current)
	}
}

func (m *Manager) watch(current *activeTask) {
	taskID := current.task.ID
	process := current.process
	stop := current.watcherStop
	go func() {
		for {
			select {
			case value, ok := <-process.Output():
				if !ok {
					result, ok := <-process.Done()
					if !ok {
						result = ProcessResult{Err: errors.New("process result unavailable")}
					}
					m.sendInternal(processDoneCommand{taskID: taskID, result: result})
					return
				}
				value.Data = append([]byte(nil), value.Data...)
				if !m.sendInternal(outputCommand{taskID: taskID, value: value}) {
					return
				}
			case <-stop:
				return
			}
		}
	}()
}

func (m *Manager) armTimeout(current *activeTask) {
	taskID := current.task.ID
	wait := m.clock.After(current.task.Timeout)
	stop := current.timeoutStop
	go func() {
		select {
		case <-wait:
			if current.execution.claim(OutcomeTimedOut) == OutcomeTimedOut {
				m.sendInternal(timeoutCommand(taskID))
			}
		case <-stop:
		case <-m.stopped:
		}
	}()
}

type timeoutCommand string

func (m *Manager) acceptOutput(current *activeTask, output ProcessOutput, active map[string]*activeTask) {
	if current.artifactSink == nil ||
		current.artifactSink.AppendOutput(
			context.Background(), current.task.ActiveStep, output.Stream, output.Data,
		) != nil {
		m.tripStorage(active)
		return
	}
	if len(output.Data) != 0 && !current.truncated {
		m.bufferOutput(current, output, active)
		if m.circuitFailed() {
			return
		}
	}
	values, failed := feedDiagnosticParser(current, output)
	if failed {
		current.plan.Steps[current.nextStep].DiagnosticParser = nil
		return
	}
	if len(values) == 0 {
		return
	}
	m.flushOutput(current, active)
	if m.circuitFailed() {
		return
	}
	m.persistDiagnostics(current, values, active)
}

func (m *Manager) bufferOutput(current *activeTask, output ProcessOutput, active map[string]*activeTask) {
	remaining := maxPersistedOutput - current.persistedBytes - current.bufferedBytes
	accepted := output.Data
	overflow := false
	if len(accepted) > remaining {
		accepted = accepted[:max(remaining, 0)]
		overflow = true
	}
	for len(accepted) > 0 {
		count := min(len(accepted), maxOutputBlock)
		part := append([]byte(nil), accepted[:count]...)
		accepted = accepted[count:]
		if len(current.segments) > 0 && current.segments[len(current.segments)-1].stream == output.Stream &&
			len(current.segments[len(current.segments)-1].data)+len(part) <= maxOutputBlock {
			last := &current.segments[len(current.segments)-1]
			last.data = append(last.data, part...)
		} else {
			current.segments = append(current.segments, outputSegment{stream: output.Stream, data: part})
		}
		current.bufferedBytes += len(part)
		if current.bufferedBytes >= maxOutputBlock {
			m.flushOutput(current, active)
			if m.circuitFailed() {
				return
			}
		}
	}
	if overflow {
		m.flushOutput(current, active)
		if m.circuitFailed() {
			return
		}
		m.persistTruncation(current, active)
	}
	if current.bufferedBytes > 0 && !current.flushPending {
		m.armFlush(current)
	}
}

func feedDiagnosticParser(
	current *activeTask,
	output ProcessOutput,
) (values []diagnostic.Diagnostic, failed bool) {
	if current.nextStep >= len(current.plan.Steps) {
		return nil, false
	}
	parser := current.plan.Steps[current.nextStep].DiagnosticParser
	if parser == nil {
		return nil, false
	}
	defer func() {
		if recover() != nil {
			values = nil
			failed = true
		}
	}()
	return parser.Feed(output.Stream, output.Data), false
}

func (m *Manager) closeDiagnosticParser(current *activeTask, active map[string]*activeTask) {
	if current.nextStep >= len(current.plan.Steps) {
		return
	}
	parser := current.plan.Steps[current.nextStep].DiagnosticParser
	if parser == nil {
		return
	}
	values, failed := closeParserSafely(parser)
	if failed {
		current.plan.Steps[current.nextStep].DiagnosticParser = nil
		return
	}
	m.persistDiagnostics(current, values, active)
}

func closeParserSafely(
	parser diagnostic.Parser,
) (values []diagnostic.Diagnostic, failed bool) {
	defer func() {
		if recover() != nil {
			values = nil
			failed = true
		}
	}()
	return parser.Close(), false
}

func (m *Manager) persistDiagnostics(
	current *activeTask,
	values []diagnostic.Diagnostic,
	active map[string]*activeTask,
) {
	for _, value := range values {
		value.TaskID = current.task.ID
		if value.StepID == "" {
			value.StepID = current.task.ActiveStep
		}
		if current.artifactSink == nil ||
			current.artifactSink.AppendDiagnostic(context.Background(), value) != nil {
			m.tripStorage(active)
			return
		}
		payload := map[string]any{
			"severity": value.Severity,
			"code":     value.Code,
			"message":  value.Message,
		}
		if value.FileURI != "" {
			payload["sourceUri"] = value.FileURI
		}
		if value.Range != nil {
			payload["line"] = value.Range.Start.Line + 1
			payload["column"] = value.Range.Start.Character + 1
		}
		event, err := m.store.AppendEvent(
			context.Background(),
			current.task.ID,
			eventDraft(current.task.ID, EventTaskDiagnostic, m.clock.Now(), map[string]any{
				"diagnostic": payload,
			}),
		)
		if err != nil {
			m.tripStorage(active)
			return
		}
		current.task.LastSequence = event.Sequence
		if !m.publish(event) {
			m.tripPublisher(active)
			return
		}
	}
}

func (m *Manager) armFlush(current *activeTask) {
	current.flushPending = true
	current.flushToken++
	token := current.flushToken
	taskID := current.task.ID
	wait := m.clock.After(m.outputFlushInterval)
	go func(stop <-chan struct{}) {
		select {
		case <-wait:
			m.sendInternal(flushCommand{taskID: taskID, token: token})
		case <-stop:
		case <-m.stopped:
		}
	}(current.timerStop)
}

func (m *Manager) flushOutput(current *activeTask, active map[string]*activeTask) {
	if len(current.segments) == 0 {
		current.bufferedBytes = 0
		return
	}
	segments := current.segments
	current.segments = nil
	current.bufferedBytes = 0
	for _, segment := range segments {
		for _, text := range validTextBlocks(segment.data) {
			payload := eventDraft(current.task.ID, EventTaskOutput, m.clock.Now(), map[string]any{
				"stepId":    current.task.ActiveStep,
				"stream":    segment.stream,
				"text":      text,
				"truncated": false,
			})
			event, err := m.store.AppendEvent(context.Background(), current.task.ID, payload)
			if err != nil {
				m.tripStorage(active)
				return
			}
			current.task.LastSequence = event.Sequence
			if !m.publish(event) {
				m.tripPublisher(active)
				return
			}
		}
		current.persistedBytes += len(segment.data)
	}
}

func validTextBlocks(data []byte) []string {
	valid := strings.ToValidUTF8(string(data), "\uFFFD")
	if len(valid) <= maxOutputBlock {
		return []string{valid}
	}
	var result []string
	for len(valid) > 0 {
		end := min(len(valid), maxOutputBlock)
		for end < len(valid) && end > 0 && !utf8.RuneStart(valid[end]) {
			end--
		}
		if end == 0 {
			end = len(valid)
		}
		result = append(result, valid[:end])
		valid = valid[end:]
	}
	return result
}

func (m *Manager) persistTruncation(current *activeTask, active map[string]*activeTask) {
	if current.truncated {
		return
	}
	current.truncated = true
	draft := eventDraft(current.task.ID, EventTaskOutput, m.clock.Now(), map[string]any{
		"stepId":    current.task.ActiveStep,
		"stream":    "combined",
		"text":      "",
		"truncated": true,
	})
	event, err := m.store.AppendEvent(context.Background(), current.task.ID, draft)
	if err != nil {
		m.tripStorage(active)
		return
	}
	current.task.LastSequence = event.Sequence
	if !m.publish(event) {
		m.tripPublisher(active)
	}
}

func (m *Manager) terminate(current *activeTask) {
	if current.terminating {
		return
	}
	current.terminating = true
	current.terminationGeneration++
	taskID := current.task.ID
	generation := current.terminationGeneration
	process := current.process
	grace := m.terminationGrace
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), grace+2*time.Second)
		defer cancel()
		err := process.Terminate(ctx, grace)
		m.sendInternal(terminationResultCommand{taskID: taskID, generation: generation, err: err})
	}()
}

func outcomeErrorCode(outcome Outcome) string {
	switch outcome {
	case OutcomeCommandFailed:
		return "command_failed"
	case OutcomeInfrastructureFailed:
		return "infrastructure_failed"
	default:
		return ""
	}
}

func outcomeErrorMessage(outcome Outcome) string {
	switch outcome {
	case OutcomeCommandFailed:
		return "task command exited unsuccessfully"
	case OutcomeInfrastructureFailed:
		return "task infrastructure failed"
	default:
		return ""
	}
}

func (m *Manager) stopActive(current *activeTask) {
	if current.stoppedWatchers {
		return
	}
	current.stoppedWatchers = true
	current.execution.stop()
	m.executionSignals.Delete(current.task.ID)
	close(current.timerStop)
	close(current.timeoutStop)
	close(current.watcherStop)
}

func (m *Manager) abandon(current *activeTask) {
	m.stopActive(current)
	if current.process != nil && !current.processCompleted && !current.terminating {
		m.terminate(current)
	}
	m.maybeStartClose(current)
}

func (m *Manager) maybeStartClose(current *activeTask) {
	m.startClose(current, false)
}

func (m *Manager) retryClose(current *activeTask) {
	m.startClose(current, true)
}

func (m *Manager) startClose(current *activeTask, retryFailed bool) {
	if current.closeStarted || current.process == nil {
		return
	}
	if current.closeFailed && !retryFailed {
		return
	}
	if current.terminating && !current.terminationComplete {
		return
	}
	ready := current.recoveryRequired || current.cleanupWithoutDone || current.processCompleted || current.terminationFailed
	if !ready {
		return
	}
	current.closeStarted = true
	current.closeFailed = false
	current.closeGeneration++
	taskID := current.task.ID
	generation := current.closeGeneration
	process := current.process
	closeTimeout := m.processCloseTimeout
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), closeTimeout)
		defer cancel()
		err := process.Close(ctx)
		m.sendInternal(closeResultCommand{taskID: taskID, generation: generation, err: err})
	}()
}

func (m *Manager) recordCloseFailure(current *activeTask) {
	if !m.circuitFailed() &&
		!current.recoveryRequired {
		outcome := current.execution.resolve(OutcomeInfrastructureFailed)
		current.failPendingStep = outcome == OutcomeInfrastructureFailed
	}
	m.healthy.Store(false)
}

func (m *Manager) canRemove(current *activeTask) bool {
	if current.recoveryRequired {
		return current.closeComplete
	}
	if current.process == nil {
		return current.task.Status == StatusFinished &&
			current.pendingCompletion == nil
	}
	return current.closeComplete &&
		current.task.Status == StatusFinished &&
		current.pendingCompletion == nil
}

func (m *Manager) tripStorage(active map[string]*activeTask) {
	if m.storageFailed {
		return
	}
	m.storageFailed = true
	m.healthy.Store(false)
	m.quiesceActive(active)
}

func (m *Manager) circuitFailed() bool {
	return m.storageFailed || m.publisherFailed
}

func (m *Manager) quiesceActive(active map[string]*activeTask) {
	for taskID, current := range active {
		current.recoveryRequired = true
		m.stopActive(current)
		if current.process == nil {
			removeActiveTask(active, taskID)
			continue
		}
		if current.cleanupWithoutDone && !current.terminationComplete {
			if !current.terminating {
				m.terminate(current)
			}
			continue
		}
		if current.processCompleted {
			m.maybeStartClose(current)
		} else if !current.terminating {
			m.terminate(current)
		}
	}
}

func (m *Manager) tripPublisher(active map[string]*activeTask) {
	if m.publisherFailed {
		return
	}
	m.publisherFailed = true
	m.healthy.Store(false)
	if !m.storageFailed {
		m.quiesceActive(active)
	}
}

func (m *Manager) publishAll(events []Event) bool {
	for _, event := range events {
		if !m.publish(event) {
			return false
		}
	}
	return true
}

func (m *Manager) publish(event Event) (ok bool) {
	ok = true
	defer func() {
		if recover() != nil {
			ok = false
		}
	}()
	m.publisher.Publish(event)
	return ok
}

func eventDraft(taskID string, kind EventType, at time.Time, payload any) EventDraft {
	encoded, err := json.Marshal(payload)
	if err != nil {
		panic(fmt.Sprintf("encode internal event: %v", err))
	}
	return EventDraft{TaskID: taskID, Type: kind, At: at, Payload: encoded}
}

func publicError(err error) error {
	if err == nil {
		return nil
	}
	for _, allowed := range []error{
		ErrNotFound, ErrConflict, ErrIdempotencyConflict, ErrInvalidArgument, ErrStorageUnavailable,
		context.Canceled, context.DeadlineExceeded,
	} {
		if errors.Is(err, allowed) {
			return allowed
		}
	}
	return ErrStorageUnavailable
}
