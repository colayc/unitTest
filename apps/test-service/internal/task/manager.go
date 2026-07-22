package task

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
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

type StartRequest struct {
	IdempotencyKey string
	Scenario       Scenario
	Timeout        time.Duration
}

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
	outputFlushInterval time.Duration
	commands            chan any
	stopped             chan struct{}
	healthy             atomic.Bool
	closing             atomic.Bool
	storageFault        atomic.Bool
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

type shutdownCommand struct{ reply chan error }
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
type cleanupCommand struct {
	taskID  string
	current *activeTask
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
	task             Task
	process          ManagedProcess
	cause            Outcome
	terminating      bool
	segments         []outputSegment
	bufferedBytes    int
	persistedBytes   int
	truncated        bool
	flushPending     bool
	flushToken       uint64
	timerStop        chan struct{}
	timeoutStop      chan struct{}
	watcherStop      chan struct{}
	terminationDone  chan struct{}
	terminationErr   error
	processCompleted bool
	cleanupScheduled bool
	stoppedWatchers  bool
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
	if config.CommandQueue <= 0 {
		config.CommandQueue = defaultCommandQueue
	}
	manager := &Manager{
		store: config.Store, publisher: config.Publisher, processes: config.Processes, artifacts: config.Artifacts,
		clock: config.Clock, newID: config.NewID, serviceExecutable: config.ServiceExecutable,
		serviceInstanceID: config.ServiceInstanceID, terminationGrace: config.TerminationGrace,
		outputFlushInterval: config.OutputFlushInterval, commands: make(chan any, config.CommandQueue), stopped: make(chan struct{}),
	}
	manager.healthy.Store(true)
	go manager.loop()
	return manager, nil
}

func (m *Manager) Healthy() bool { return m != nil && m.healthy.Load() && !m.closing.Load() }

func (m *Manager) Start(ctx context.Context, request StartRequest) (Task, error) {
	if m == nil || request.IdempotencyKey == "" || !ValidScenario(request.Scenario) ||
		request.Timeout < time.Millisecond || request.Timeout > 24*time.Hour || request.Timeout%time.Millisecond != 0 {
		return Task{}, ErrInvalidArgument
	}
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
	reply := make(chan error, 1)
	select {
	case m.commands <- shutdownCommand{reply: reply}:
	case <-m.stopped:
		return nil
	}
	select {
	case err := <-reply:
		return err
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
	var shutdownWaiters []chan error
	for command := range m.commands {
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
				m.terminate(current)
			}
		case processDoneCommand:
			if current := active[value.taskID]; current != nil && !current.processCompleted {
				current.processCompleted = true
				if m.storageFault.Load() {
					m.abandon(current)
				} else {
					m.finish(current, value.result, active)
				}
			}
		case cleanupCommand:
			if active[value.taskID] == value.current {
				delete(active, value.taskID)
			}
		case shutdownCommand:
			m.closing.Store(true)
			m.healthy.Store(false)
			shutdownWaiters = append(shutdownWaiters, value.reply)
			for _, current := range active {
				if current.task.Status != StatusFinished && current.cause == "" {
					current.cause = OutcomeInterrupted
					m.terminate(current)
				}
			}
		}
		if m.closing.Load() && len(active) == 0 {
			for _, waiter := range shutdownWaiters {
				waiter <- nil
			}
			close(m.stopped)
			return
		}
	}
}

func (m *Manager) start(request StartRequest, active map[string]*activeTask) taskResponse {
	if !m.Healthy() {
		return taskResponse{err: ErrStorageUnavailable}
	}
	requestHash := hashStartRequest(request)
	now := m.clock.Now()
	created := Task{
		ID: m.newID(), IdempotencyKey: request.IdempotencyKey, RequestHash: requestHash,
		Scenario: request.Scenario, Timeout: request.Timeout, Status: StatusQueued, CreatedAt: now,
	}
	draft := eventDraft(created.ID, EventTaskCreated, now, map[string]any{"status": StatusQueued})
	stored, events, err := m.store.Create(context.Background(), created, draft)
	if err != nil {
		if errors.Is(err, ErrStorageUnavailable) {
			m.tripStorage(active)
		}
		return taskResponse{err: err}
	}
	if len(events) == 0 {
		if stored.RequestHash != requestHash {
			return taskResponse{err: ErrIdempotencyConflict}
		}
		return taskResponse{task: stored}
	}
	if !m.publishAll(events, active) {
		return taskResponse{err: ErrStorageUnavailable}
	}

	spec := ProcessSpec{Executable: m.serviceExecutable, Args: []string{"--task-fixture", string(request.Scenario)}}
	process, err := m.processes.Prepare(context.Background(), spec, stored.ID, m.serviceInstanceID)
	if err != nil {
		finished, finishErr := m.finishWithoutProcess(stored, OutcomeInfrastructureFailed, active)
		return taskResponse{task: finished, err: finishErr}
	}
	current := &activeTask{
		task: stored, process: process, timerStop: make(chan struct{}), timeoutStop: make(chan struct{}),
		watcherStop: make(chan struct{}), terminationDone: make(chan struct{}),
	}
	active[stored.ID] = current
	startedAt := m.clock.Now()
	running, err := ApplyTransition(stored, Transition{From: StatusQueued, To: StatusRunning, At: startedAt})
	if err != nil {
		delete(active, stored.ID)
		_ = process.Close()
		return taskResponse{err: err}
	}
	lease := process.Lease()
	lease.TaskID = stored.ID
	lease.ServiceInstanceID = m.serviceInstanceID
	running, startedEvents, err := m.store.Apply(context.Background(), Mutation{
		Task: running, Expected: StatusQueued, PutLease: &lease,
		Events: []EventDraft{eventDraft(stored.ID, EventTaskStarted, startedAt, map[string]any{"status": StatusRunning})},
	})
	if err != nil {
		m.terminate(current)
		if errors.Is(err, ErrStorageUnavailable) {
			m.tripStorage(active)
		}
		m.abandon(current)
		return taskResponse{err: err}
	}
	current.task = running
	if !m.publishAll(startedEvents, active) {
		m.abandon(current)
		return taskResponse{err: ErrStorageUnavailable}
	}
	if err := process.Start(context.Background()); err != nil {
		m.terminate(current)
		finished, finishErr := m.finishNow(current, ProcessResult{Err: err}, active)
		if finishErr != nil {
			m.abandon(current)
		}
		return taskResponse{task: finished, err: finishErr}
	}
	updatedLease := process.Lease()
	updatedLease.TaskID = stored.ID
	updatedLease.ServiceInstanceID = m.serviceInstanceID
	if err := m.store.UpdateLease(context.Background(), updatedLease); err != nil {
		m.tripStorage(active)
		m.abandon(current)
		return taskResponse{err: err}
	}
	m.watch(current)
	m.armTimeout(current)
	return taskResponse{task: current.task}
}

func hashStartRequest(request StartRequest) string {
	canonical, _ := json.Marshal(struct {
		Scenario  Scenario `json:"scenario"`
		TimeoutMS int64    `json:"timeoutMs"`
	}{request.Scenario, request.Timeout.Milliseconds()})
	sum := sha256.Sum256(canonical)
	return hex.EncodeToString(sum[:])
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
	current.cause = OutcomeCancelled
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
	if !m.publishAll(events, active) {
		return taskResponse{task: current.task, err: ErrStorageUnavailable}
	}
	m.terminate(current)
	return taskResponse{task: current.task}
}

func (m *Manager) watch(current *activeTask) {
	taskID := current.task.ID
	go func() {
		for {
			select {
			case value, ok := <-current.process.Output():
				if !ok {
					result, ok := <-current.process.Done()
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
			case <-current.watcherStop:
				return
			}
		}
	}()
}

func (m *Manager) armTimeout(current *activeTask) {
	taskID := current.task.ID
	wait := m.clock.After(current.task.Timeout)
	go func() {
		select {
		case <-wait:
			m.sendInternal(timeoutCommand(taskID))
		case <-current.timeoutStop:
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
			if m.storageFault.Load() {
				return
			}
		}
	}
	if overflow {
		m.flushOutput(current, active)
		if m.storageFault.Load() {
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
	go func() {
		defer close(current.terminationDone)
		ctx, cancel := context.WithTimeout(context.Background(), m.terminationGrace+2*time.Second)
		defer cancel()
		current.terminationErr = current.process.Terminate(ctx, m.terminationGrace)
	}()
}

func (m *Manager) finish(current *activeTask, result ProcessResult, active map[string]*activeTask) {
	m.flushOutput(current, active)
	if m.storageFault.Load() {
		m.abandon(current)
		return
	}
	if active[current.task.ID] == nil {
		return
	}
	if _, err := m.finishNow(current, result, active); err != nil {
		m.abandon(current)
	}
}

func (m *Manager) finishNow(current *activeTask, result ProcessResult, active map[string]*activeTask) (Task, error) {
	outcome := current.cause
	if outcome == "" {
		switch {
		case result.Err != nil:
			outcome = OutcomeInfrastructureFailed
		case result.ExitCode == 0:
			outcome = OutcomeSucceeded
		default:
			outcome = OutcomeCommandFailed
		}
	}
	finished, err := m.persistFinished(current.task, outcome, current.process != nil, active)
	if err != nil {
		return current.task, err
	}
	current.task = finished
	m.stopActive(current)
	m.scheduleCleanup(current)
	return finished, nil
}

func (m *Manager) finishWithoutProcess(created Task, outcome Outcome, active map[string]*activeTask) (Task, error) {
	return m.persistFinished(created, outcome, false, active)
}

func (m *Manager) persistFinished(current Task, outcome Outcome, deleteLease bool, active map[string]*activeTask) (Task, error) {
	finishedAt := m.clock.Now()
	finished, err := ApplyTransition(current, Transition{
		From: current.Status, To: StatusFinished, Outcome: outcome, At: finishedAt,
		ErrorCode: outcomeErrorCode(outcome), ErrorMessage: outcomeErrorMessage(outcome),
	})
	if err != nil {
		return current, err
	}
	summary := struct {
		TaskID     string   `json:"taskId"`
		Scenario   Scenario `json:"scenario"`
		Outcome    Outcome  `json:"outcome"`
		FinishedAt string   `json:"finishedAt"`
	}{current.ID, current.Scenario, outcome, finishedAt.Format(time.RFC3339Nano)}
	artifactID := m.newID()
	artifact, artifactErr := m.artifacts.CommitJSON(context.Background(), current.ID, artifactID, finishedAt, summary)
	artifacts := []Artifact(nil)
	events := []EventDraft(nil)
	if artifactErr == nil {
		artifacts = []Artifact{artifact}
		events = append(events, eventDraft(current.ID, EventArtifactCreated, finishedAt, map[string]any{
			"artifactId": artifact.ID, "kind": artifact.Kind,
		}))
	} else {
		outcome = OutcomeInfrastructureFailed
		finished.Outcome = outcome
		finished.ErrorCode = outcomeErrorCode(outcome)
		finished.ErrorMessage = outcomeErrorMessage(outcome)
		summary.Outcome = outcome
	}
	events = append(events, eventDraft(current.ID, EventTaskFinished, finishedAt, map[string]any{"outcome": outcome}))
	stored, committed, err := m.store.Apply(context.Background(), Mutation{
		Task: finished, Expected: current.Status, Events: events, DeleteLease: deleteLease, Artifacts: artifacts,
	})
	if err != nil {
		if errors.Is(err, ErrStorageUnavailable) {
			m.tripStorage(active)
		}
		return current, err
	}
	if !m.publishAll(committed, active) {
		return stored, ErrStorageUnavailable
	}
	return stored, nil
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
	if current.process != nil {
		if !current.terminating {
			m.terminate(current)
		}
	}
	m.scheduleCleanup(current)
}

func (m *Manager) scheduleCleanup(current *activeTask) {
	if current.cleanupScheduled {
		return
	}
	current.cleanupScheduled = true
	go func() {
		if current.process != nil {
			if current.terminating {
				<-current.terminationDone
				if current.terminationErr != nil {
					m.healthy.Store(false)
				}
			}
			if current.process.Close() != nil {
				m.healthy.Store(false)
			}
		}
		m.sendInternal(cleanupCommand{taskID: current.task.ID, current: current})
	}()
}

func (m *Manager) tripStorage(active map[string]*activeTask) {
	m.storageFault.Store(true)
	if !m.healthy.Swap(false) {
		return
	}
	for _, current := range active {
		m.terminate(current)
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
