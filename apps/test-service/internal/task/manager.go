package task

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"time"
	"unicode/utf8"
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
	storageFailed       bool // command-loop owned
}

type startCommand struct {
	request StartRequest
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
	cause      Outcome
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
	nextStep              int
	process               ManagedProcess
	cause                 Outcome
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
		clock: config.Clock, newID: config.NewID, serviceExecutable: config.ServiceExecutable,
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

func (m *Manager) List(ctx context.Context, cursor string, limit int) (Page[Task], error) {
	if m == nil || limit < 1 {
		return Page[Task]{}, ErrInvalidArgument
	}
	reply := make(chan listResponse, 1)
	if err := m.send(ctx, listCommand{cursor: cursor, limit: limit, reply: reply}); err != nil {
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
	reply := make(chan taskResponse, 1)
	if err := m.send(ctx, taskIDCommand{id: id, cancel: true, reply: reply}); err != nil {
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
		case taskIDCommand:
			if value.cancel {
				value.reply <- m.cancel(value.id, active)
			} else {
				got, err := m.store.Get(context.Background(), value.id)
				value.reply <- taskResponse{task: got, err: err}
			}
		case listCommand:
			page, err := m.store.List(context.Background(), value.cursor, value.limit)
			value.reply <- listResponse{page: page, err: err}
		case outputCommand:
			if current := active[value.taskID]; current != nil && !current.processCompleted {
				m.acceptOutput(current, value.value, active)
			}
		case flushCommand:
			if current := active[value.taskID]; current != nil && current.flushPending && current.flushToken == value.token {
				current.flushPending = false
				m.flushOutput(current, active)
			}
		case timeoutCommand:
			if current := active[string(value)]; current != nil && current.task.Status != StatusFinished && current.cause == "" {
				current.cause = OutcomeTimedOut
				if current.processCompleted {
					m.maybeStartClose(current)
				} else {
					m.terminate(current)
				}
			}
		case processDoneCommand:
			if current := active[value.taskID]; current != nil && !current.processCompleted {
				current.processCompleted = true
				if m.storageFailed || current.recoveryRequired {
					m.abandon(current)
				} else {
					m.finish(current, value.result, active)
				}
				if active[value.taskID] == current && m.canRemove(current) {
					delete(active, value.taskID)
				}
			}
		case terminationResultCommand:
			if current := active[value.taskID]; current != nil && current.terminationGeneration == value.generation {
				current.terminationComplete = true
				if value.err != nil {
					current.terminationFailed = true
					m.healthy.Store(false)
				}
				m.maybeStartClose(current)
			}
		case closeResultCommand:
			if current := active[value.taskID]; current != nil && current.closeGeneration == value.generation {
				if value.err != nil {
					m.healthy.Store(false)
					current.closeStarted = false
					current.closeComplete = false
					current.closeFailed = true
					if current.task.Status != StatusFinished {
						if _, err := m.finishAfterCloseFailure(current, active); err != nil {
							m.abandon(current)
						}
					}
				} else {
					current.closeComplete = true
					current.closeFailed = false
					if current.task.Status != StatusFinished && current.processCompleted &&
						!m.storageFailed && !current.recoveryRequired {
						if current.cause != "" {
							if _, err := m.finishExecution(
								current,
								ProcessResult{},
								current.cause,
								current.failPendingStep,
								active,
							); err != nil {
								m.abandon(current)
							}
						} else if current.nextStep < len(current.plan.Steps) {
							current.process = nil
							if err := m.startNextStep(current, active); err != nil && !errors.Is(err, ErrStorageUnavailable) {
								m.abandon(current)
							}
						}
					}
				}
				if value.err == nil && m.canRemove(current) {
					delete(active, value.taskID)
				}
			}
		case shutdownCommand:
			m.shutdownPending.Store(false)
			initiate := !shutdownInitiated
			shutdownInitiated = true
			for _, current := range active {
				if initiate && current.task.Status != StatusFinished && current.cause == "" {
					current.cause = OutcomeInterrupted
					if current.processCompleted {
						m.maybeStartClose(current)
					} else {
						m.terminate(current)
					}
				}
				if current.closeFailed {
					m.maybeStartClose(current)
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
	if current.cause != "" {
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
		}
		return taskResponse{task: current.task, err: err}
	}
	current.task = cancelling
	current.cause = OutcomeCancelled
	if !m.publishAll(events, active) {
		return taskResponse{task: current.task, err: ErrStorageUnavailable}
	}
	if current.processCompleted {
		m.maybeStartClose(current)
	} else {
		m.terminate(current)
	}
	return taskResponse{task: current.task}
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
			m.sendInternal(timeoutCommand(taskID))
		case <-stop:
		case <-m.stopped:
		}
	}()
}

type timeoutCommand string

func (m *Manager) acceptOutput(current *activeTask, output ProcessOutput, active map[string]*activeTask) {
	if len(output.Data) == 0 || current.truncated {
		return
	}
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
			if m.storageFailed {
				return
			}
		}
	}
	if overflow {
		m.flushOutput(current, active)
		if m.storageFailed {
			return
		}
		m.persistTruncation(current, active)
	}
	if current.bufferedBytes > 0 && !current.flushPending {
		m.armFlush(current)
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
				"stream": segment.stream, "text": text, "truncated": false,
			})
			event, err := m.store.AppendEvent(context.Background(), current.task.ID, payload)
			if err != nil {
				m.tripStorage(active)
				return
			}
			current.task.LastSequence = event.Sequence
			if !m.publish(event, active) {
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
		"stream": "combined", "text": "", "truncated": true,
	})
	event, err := m.store.AppendEvent(context.Background(), current.task.ID, draft)
	if err != nil {
		m.tripStorage(active)
		return
	}
	current.task.LastSequence = event.Sequence
	m.publish(event, active)
}

func (m *Manager) terminate(current *activeTask) {
	if current.terminating {
		return
	}
	current.terminating = true
	current.terminationGeneration++
	taskID := current.task.ID
	cause := current.cause
	generation := current.terminationGeneration
	process := current.process
	grace := m.terminationGrace
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), grace+2*time.Second)
		defer cancel()
		err := process.Terminate(ctx, grace)
		m.sendInternal(terminationResultCommand{taskID: taskID, cause: cause, generation: generation, err: err})
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
	if current.closeStarted || current.process == nil {
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

func (m *Manager) canRemove(current *activeTask) bool {
	if !current.closeComplete {
		return false
	}
	if current.recoveryRequired || current.cleanupWithoutDone {
		return true
	}
	return current.processCompleted && current.task.Status == StatusFinished
}

func (m *Manager) tripStorage(active map[string]*activeTask) {
	if m.storageFailed {
		return
	}
	m.storageFailed = true
	m.healthy.Store(false)
	for _, current := range active {
		current.recoveryRequired = true
		m.stopActive(current)
		if current.processCompleted {
			m.maybeStartClose(current)
		} else if !current.terminating {
			m.terminate(current)
		}
	}
}

func (m *Manager) publishAll(events []Event, active map[string]*activeTask) bool {
	for _, event := range events {
		if !m.publish(event, active) {
			return false
		}
	}
	return true
}

func (m *Manager) publish(event Event, active map[string]*activeTask) (ok bool) {
	ok = true
	defer func() {
		if recover() != nil {
			ok = false
			m.tripStorage(active)
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
