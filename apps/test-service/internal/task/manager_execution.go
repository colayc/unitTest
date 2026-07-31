package task

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"
)

// NewSimulationStartRequest projects the protocol-owned simulation inputs into
// a service-owned execution plan and runtime-only execution boundary.
func NewSimulationStartRequest(
	idempotencyKey string,
	scenario Scenario,
	timeout time.Duration,
	serviceExecutable string,
	simulationDirectory string,
) (StartRequest, error) {
	if idempotencyKey == "" || !ValidScenario(scenario) || !validTimeout(timeout) {
		return StartRequest{}, ErrInvalidArgument
	}
	boundary, executable, directory, err := newSimulationBoundary(serviceExecutable, simulationDirectory)
	if err != nil {
		return StartRequest{}, ErrInvalidArgument
	}
	request, err := json.Marshal(struct {
		Scenario  Scenario `json:"scenario"`
		TimeoutMS int64    `json:"timeoutMs"`
	}{Scenario: scenario, TimeoutMS: timeout.Milliseconds()})
	if err != nil {
		return StartRequest{}, ErrInvalidArgument
	}
	plan := ExecutionPlan{
		Version: 1,
		Steps: []ExecutionStep{{
			ID:   "simulate",
			Kind: StepSimulation,
			Process: ProcessSpec{
				Executable: executable,
				Args:       []string{"--task-fixture", string(scenario)},
				Dir:        directory,
			},
			Public: CommandSummary{
				Executable: filepath.Base(executable),
				Args:       []string{"--task-fixture", string(scenario)},
			},
		}},
	}
	plan.Fingerprint = FingerprintPlan(plan)
	result := StartRequest{
		IdempotencyKey: idempotencyKey,
		Kind:           KindSimulation,
		Request:        request,
		Timeout:        timeout,
		Plan:           plan,
		Boundary:       boundary,
		Scenario:       scenario,
	}
	if err := validateStartRequest(result); err != nil {
		return StartRequest{}, err
	}
	return result, nil
}

type simulationBoundary struct {
	executablePath string
	executableInfo os.FileInfo
	directoryPath  string
	directoryInfo  os.FileInfo
}

func newSimulationBoundary(executable, directory string) (*simulationBoundary, string, string, error) {
	executable, err := absoluteCleanPath(executable)
	if err != nil {
		return nil, "", "", err
	}
	directory, err = absoluteCleanPath(directory)
	if err != nil {
		return nil, "", "", err
	}
	executableInfo, err := os.Stat(executable)
	if err != nil || executableInfo.IsDir() {
		return nil, "", "", ErrInvalidArgument
	}
	directoryInfo, err := os.Stat(directory)
	if err != nil || !directoryInfo.IsDir() {
		return nil, "", "", ErrInvalidArgument
	}
	return &simulationBoundary{
		executablePath: executable,
		executableInfo: executableInfo,
		directoryPath:  directory,
		directoryInfo:  directoryInfo,
	}, executable, directory, nil
}

func (b *simulationBoundary) ValidateExecutable(path string) error {
	return validatePathIdentity(path, b.executablePath, b.executableInfo, false)
}

func (b *simulationBoundary) ValidateWorkingDirectory(path string) error {
	return validatePathIdentity(path, b.directoryPath, b.directoryInfo, true)
}

func absoluteCleanPath(path string) (string, error) {
	if path == "" || containsNUL(path) {
		return "", ErrInvalidArgument
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", ErrInvalidArgument
	}
	return filepath.Clean(absolute), nil
}

func validatePathIdentity(path, allowed string, identity os.FileInfo, directory bool) error {
	absolute, err := absoluteCleanPath(path)
	if err != nil || absolute != allowed {
		return ErrInvalidArgument
	}
	current, err := os.Stat(absolute)
	if err != nil || current.IsDir() != directory || !os.SameFile(identity, current) {
		return ErrInvalidArgument
	}
	return nil
}

func validTimeout(timeout time.Duration) bool {
	return timeout >= time.Millisecond && timeout <= 24*time.Hour && timeout%time.Millisecond == 0
}

func validateStartRequest(request StartRequest) error {
	if _, requestOK := canonicalRequestJSON(request.Request); request.IdempotencyKey == "" ||
		!validTimeout(request.Timeout) || !requestOK ||
		request.Plan.Fingerprint == "" || request.Plan.Fingerprint != FingerprintPlan(request.Plan) {
		return ErrInvalidArgument
	}
	switch request.Kind {
	case KindSimulation:
		if !ValidScenario(request.Scenario) || request.WorkspaceGeneration != "" {
			return ErrInvalidArgument
		}
	case KindCMakeBuild:
		if request.Scenario != "" || request.WorkspaceGeneration == "" {
			return ErrInvalidArgument
		}
	default:
		return ErrInvalidArgument
	}
	return ValidatePlan(request.Plan, request.Boundary)
}

func cloneStartRequest(request StartRequest) StartRequest {
	result := request
	result.Request = append(json.RawMessage(nil), request.Request...)
	result.Plan = cloneExecutionPlan(request.Plan)
	return result
}

func cloneExecutionPlan(plan ExecutionPlan) ExecutionPlan {
	result := plan
	result.Steps = make([]ExecutionStep, len(plan.Steps))
	for index, step := range plan.Steps {
		result.Steps[index] = step
		result.Steps[index].Process.Args = append([]string(nil), step.Process.Args...)
		result.Steps[index].Process.Env = append([]string(nil), step.Process.Env...)
		result.Steps[index].Public.Args = append([]string(nil), step.Public.Args...)
		result.Steps[index].State = append(json.RawMessage(nil), step.State...)
	}
	return result
}

func hashStartRequest(request StartRequest) string {
	normalizedRequest, ok := canonicalRequestJSON(request.Request)
	if !ok {
		return ""
	}
	canonical, _ := json.Marshal(struct {
		Kind                Kind   `json:"kind"`
		Request             string `json:"request"`
		WorkspaceGeneration string `json:"workspaceGeneration"`
		TimeoutMS           int64  `json:"timeoutMs"`
	}{
		Kind:                request.Kind,
		Request:             string(normalizedRequest),
		WorkspaceGeneration: request.WorkspaceGeneration,
		TimeoutMS:           request.Timeout.Milliseconds(),
	})
	sum := sha256.Sum256(canonical)
	return hex.EncodeToString(sum[:])
}

func initialStepSnapshots(plan ExecutionPlan) []StepSnapshot {
	result := make([]StepSnapshot, len(plan.Steps))
	for index, step := range plan.Steps {
		result[index] = StepSnapshot{ID: step.ID, Kind: step.Kind, Status: StepPending}
	}
	return result
}

func (m *Manager) start(request StartRequest, active map[string]*activeTask) taskResponse {
	if !m.Healthy() {
		return taskResponse{err: ErrStorageUnavailable}
	}
	if err := validateStartRequest(request); err != nil {
		return taskResponse{err: err}
	}
	request = cloneStartRequest(request)
	requestHash := hashStartRequest(request)
	now := m.clock.Now()
	created := Task{
		ID: m.newID(), IdempotencyKey: request.IdempotencyKey, RequestHash: requestHash,
		Kind: request.Kind, Request: append(json.RawMessage(nil), request.Request...), WorkspaceGeneration: request.WorkspaceGeneration,
		PlanFingerprint: request.Plan.Fingerprint, Scenario: request.Scenario, Timeout: request.Timeout,
		Status: StatusQueued, CreatedAt: now,
	}
	execution := newExecutionSignal()
	m.executionSignals.Store(created.ID, execution)
	if m.closing.Load() {
		execution.claim(OutcomeInterrupted)
	}
	executionRetained := false
	defer func() {
		if executionRetained {
			return
		}
		execution.stop()
		m.executionSignals.CompareAndDelete(created.ID, execution)
	}()
	steps := initialStepSnapshots(request.Plan)
	draft := eventDraft(created.ID, EventTaskCreated, now, map[string]any{"status": StatusQueued})
	stored, events, err := m.store.Create(context.Background(), created, steps, draft)
	if err != nil {
		if errors.Is(err, ErrStorageUnavailable) {
			m.tripStorage(active)
		}
		return taskResponse{err: err}
	}
	if len(events) == 0 {
		if !EquivalentIdempotencyRequest(stored, created) {
			return taskResponse{err: ErrIdempotencyConflict}
		}
		return taskResponse{task: stored}
	}
	if boundary, ok := request.Boundary.(ManagedExecutionBoundary); ok {
		boundary.Adopt(stored.ID)
	}
	current := &activeTask{
		task: stored, plan: request.Plan, boundary: request.Boundary,
		continuation: request.Continuation, resultInterpreter: request.ResultInterpreter,
		timerStop: make(chan struct{}), timeoutStop: make(chan struct{}),
		watcherStop: make(chan struct{}), execution: execution,
	}
	active[stored.ID] = current
	executionRetained = true
	if err := m.openTaskArtifacts(current, active); err != nil {
		return taskResponse{task: current.task, err: err}
	}
	m.armTimeout(current)

	if !m.publishAll(events) {
		terminal, terminalErr := m.persistCommittedCreateFailure(current, active)
		m.tripPublisher(active)
		if terminalErr != nil {
			return taskResponse{task: current.task, err: ErrStorageUnavailable}
		}
		return taskResponse{task: terminal, err: ErrStorageUnavailable}
	}
	if err := m.startNextStep(current, active); err != nil {
		return taskResponse{task: current.task, err: err}
	}
	if current.task.Status == StatusFinished && current.process == nil {
		removeActiveTask(active, stored.ID)
	}
	return taskResponse{task: current.task}
}

func (m *Manager) resumeQueued(
	request ResumeRequest,
	active map[string]*activeTask,
) taskResponse {
	if !m.Healthy() {
		return taskResponse{err: ErrStorageUnavailable}
	}
	if active[request.Task.ID] != nil {
		return taskResponse{err: ErrConflict}
	}
	stored, err := m.store.Get(context.Background(), request.Task.ID)
	if err != nil {
		if errors.Is(err, ErrStorageUnavailable) {
			m.tripStorage(active)
		}
		return taskResponse{err: err}
	}
	if !sameQueuedTaskIdentity(stored, request.Task) {
		return taskResponse{task: stored, err: ErrConflict}
	}
	planStore, ok := m.store.(QueuedPlanStore)
	if !ok {
		m.tripStorage(active)
		return taskResponse{task: stored, err: ErrStorageUnavailable}
	}
	stored, err = planStore.ReplaceQueuedPlan(
		context.Background(), stored.ID, stored.RequestHash,
		request.Plan.Fingerprint, initialStepSnapshots(request.Plan),
	)
	if err != nil {
		if errors.Is(err, ErrStorageUnavailable) {
			m.tripStorage(active)
		}
		return taskResponse{task: stored, err: err}
	}
	execution := newExecutionSignal()
	m.executionSignals.Store(stored.ID, execution)
	if m.closing.Load() {
		execution.claim(OutcomeInterrupted)
	}
	if boundary, ok := request.Boundary.(ManagedExecutionBoundary); ok {
		boundary.Adopt(stored.ID)
	}
	current := &activeTask{
		task: stored, plan: request.Plan, boundary: request.Boundary,
		continuation: request.Continuation, resultInterpreter: request.ResultInterpreter,
		timerStop: make(chan struct{}), timeoutStop: make(chan struct{}),
		watcherStop: make(chan struct{}), execution: execution,
	}
	active[stored.ID] = current
	if err := m.openTaskArtifacts(current, active); err != nil {
		return taskResponse{task: current.task, err: err}
	}
	m.armTimeout(current)
	if err := m.startNextStep(current, active); err != nil {
		return taskResponse{task: current.task, err: err}
	}
	if current.task.Status == StatusFinished && current.process == nil {
		removeActiveTask(active, stored.ID)
	}
	return taskResponse{task: current.task}
}

func sameQueuedTaskIdentity(stored, supplied Task) bool {
	return stored.ID == supplied.ID &&
		stored.IdempotencyKey == supplied.IdempotencyKey &&
		stored.RequestHash == supplied.RequestHash &&
		stored.Kind == KindCMakeBuild &&
		stored.Status == StatusQueued &&
		supplied.Kind == KindCMakeBuild &&
		supplied.Status == StatusQueued &&
		stored.WorkspaceGeneration == supplied.WorkspaceGeneration &&
		stored.Timeout == supplied.Timeout &&
		string(stored.Request) == string(supplied.Request)
}

func (m *Manager) startNextStep(current *activeTask, active map[string]*activeTask) error {
	if current.nextStep >= len(current.plan.Steps) {
		return nil
	}
	if err := validateExecutionPlan(current.plan, current.boundary); err != nil {
		return m.finishPendingStep(current, active)
	}
	step := current.plan.Steps[current.nextStep]
	if current.execution.currentCause() != "" {
		return nil
	}
	process, prepareErr := m.processes.Prepare(
		current.execution.ctx,
		step.Process,
		current.task.ID,
		m.serviceInstanceID,
	)
	if process == nil {
		if prepareErr == nil {
			prepareErr = errors.New("process preparation returned no process")
		}
		return m.finishPendingStep(current, active)
	}
	m.setCurrentProcess(current, process)
	if leaseErr := m.persistPreparedLease(current, active); leaseErr != nil {
		cause := current.execution.resolve(OutcomeInfrastructureFailed)
		current.cleanupWithoutDone = true
		if !current.terminating {
			m.terminate(current)
		}
		m.stageProcessCompletion(
			current,
			ProcessResult{Err: leaseErr},
			cause == OutcomeInfrastructureFailed,
		)
		return leaseErr
	}
	if current.execution.currentCause() != "" {
		m.cleanupPreparedProcess(current)
		return nil
	}
	if prepareErr != nil {
		m.cleanupPreparedProcess(current)
		return nil
	}

	startedAt := m.clock.Now()
	runningTask := current.task
	expectedStatus := current.task.Status
	events := []EventDraft(nil)
	if current.task.Status == StatusQueued {
		var err error
		runningTask, err = ApplyTransition(current.task, Transition{From: StatusQueued, To: StatusRunning, At: startedAt})
		if err != nil {
			m.cleanupPreparedProcess(current)
			return err
		}
		events = []EventDraft{eventDraft(current.task.ID, EventTaskStarted, startedAt, map[string]any{"status": StatusRunning})}
	}
	runningTask.ActiveStep = step.ID
	runningStep := runningTask.Steps[current.nextStep]
	runningStep.Status = StepRunning
	runningStep.StartedAt = timePointer(startedAt)
	if current.task.Kind == KindCMakeBuild {
		events = append(events, eventDraft(
			current.task.ID,
			EventTaskStepStarted,
			startedAt,
			stepEventPayload(runningStep),
		))
	}
	stored, committed, err := m.store.Apply(context.Background(), Mutation{
		Task: runningTask, Expected: expectedStatus,
		Steps:  []StepMutation{{Step: runningStep, Expected: StepPending}},
		Events: events,
	})
	if err != nil {
		cause := current.execution.resolve(OutcomeInfrastructureFailed)
		current.cleanupWithoutDone = true
		m.terminate(current)
		m.stageProcessCompletion(
			current,
			ProcessResult{Err: err},
			cause == OutcomeInfrastructureFailed,
		)
		if errors.Is(err, ErrStorageUnavailable) {
			m.tripStorage(active)
		}
		m.abandon(current)
		return err
	}
	current.task = stored
	if !m.publishAll(committed) {
		m.tripPublisher(active)
		return ErrStorageUnavailable
	}
	if current.execution.currentCause() != "" {
		m.cleanupPreparedProcess(current)
		return nil
	}
	startErr := process.Start(current.execution.ctx)
	if startErr != nil {
		cause := current.execution.resolve(OutcomeInfrastructureFailed)
		current.cleanupWithoutDone = true
		m.terminate(current)
		m.stageProcessCompletion(
			current,
			ProcessResult{Err: startErr},
			cause == OutcomeInfrastructureFailed,
		)
		return nil
	}
	if current.execution.currentCause() != "" {
		updatedLease := process.Lease()
		updatedLease.TaskID = current.task.ID
		updatedLease.ServiceInstanceID = m.serviceInstanceID
		if err := m.store.UpdateLease(context.Background(), updatedLease); err != nil {
			m.tripStorage(active)
			return err
		}
		m.watch(current)
		return nil
	}
	updatedLease := process.Lease()
	updatedLease.TaskID = current.task.ID
	updatedLease.ServiceInstanceID = m.serviceInstanceID
	if err := m.store.UpdateLease(context.Background(), updatedLease); err != nil {
		m.tripStorage(active)
		return err
	}
	m.watch(current)
	return nil
}

func (m *Manager) persistPreparedLease(
	current *activeTask,
	active map[string]*activeTask,
) error {
	lease := current.process.Lease()
	lease.TaskID = current.task.ID
	lease.ServiceInstanceID = m.serviceInstanceID
	stored, _, err := m.store.Apply(context.Background(), Mutation{
		Task:     current.task,
		Expected: current.task.Status,
		PutLease: &lease,
	})
	if err != nil {
		if errors.Is(err, ErrStorageUnavailable) {
			m.tripStorage(active)
		}
		return err
	}
	current.task = stored
	current.leasePersisted = true
	return nil
}

func validateExecutionPlan(plan ExecutionPlan, boundary ExecutionBoundary) error {
	if plan.Version != 1 ||
		plan.Fingerprint == "" ||
		plan.Fingerprint != FingerprintPlan(plan) {
		return ErrInvalidArgument
	}
	return validateExecutionSteps(plan.Steps, boundary)
}

func (m *Manager) setCurrentProcess(current *activeTask, process ManagedProcess) {
	current.process = process
	current.leasePersisted = false
	current.pendingCompletion = nil
	current.processCompleted = false
	current.terminating = false
	current.terminationComplete = false
	current.terminationFailed = false
	current.closeStarted = false
	current.closeComplete = false
	current.closeFailed = false
	current.cleanupWithoutDone = false
	current.failPendingStep = false
	current.watcherStop = make(chan struct{})
}

func (m *Manager) cleanupPreparedProcess(current *activeTask) {
	current.cleanupWithoutDone = true
	outcome := current.execution.resolve(OutcomeInfrastructureFailed)
	m.terminate(current)
	m.stageProcessCompletion(
		current,
		ProcessResult{Err: errors.New("step preparation failed")},
		outcome == OutcomeInfrastructureFailed,
	)
}

func (m *Manager) finishPendingStep(current *activeTask, active map[string]*activeTask) error {
	_, err := m.finishExecution(current, ProcessResult{Err: errors.New("step preparation failed")}, OutcomeInfrastructureFailed, true, active)
	if err == nil && m.canRemove(current) {
		removeActiveTask(active, current.task.ID)
	}
	return err
}

func (m *Manager) finish(current *activeTask, result ProcessResult, active map[string]*activeTask) {
	m.flushOutput(current, active)
	if active[current.task.ID] == nil {
		return
	}
	m.closeDiagnosticParser(current, active)
	if active[current.task.ID] == nil {
		return
	}
	m.stageProcessCompletion(current, result, false)
}

func (m *Manager) persistSuccessfulStep(
	current *activeTask,
	result ProcessResult,
	nextPlan ExecutionPlan,
	appended []StepSnapshot,
	active map[string]*activeTask,
) error {
	if current.process != nil && !current.closeComplete {
		return ErrConflict
	}
	finishedAt := m.clock.Now()
	step := current.task.Steps[current.nextStep]
	step.Status = StepSucceeded
	step.FinishedAt = timePointer(finishedAt)
	step.ExitCode = intPointer(result.ExitCode)
	updatedTask := current.task
	updatedTask.ActiveStep = ""
	updatedTask.PlanFingerprint = nextPlan.Fingerprint
	eventDrafts := []EventDraft(nil)
	if current.task.Kind == KindCMakeBuild {
		eventDrafts = append(eventDrafts, eventDraft(
			current.task.ID,
			EventTaskStepFinished,
			finishedAt,
			stepEventPayload(step),
		))
	}
	stored, committed, err := m.store.Apply(context.Background(), Mutation{
		Task: updatedTask, Expected: current.task.Status,
		Steps:       []StepMutation{{Step: step, Expected: StepRunning}},
		AppendSteps: appended,
		Events:      eventDrafts,
		DeleteLease: true,
	})
	if err != nil {
		current.execution.resolve(OutcomeInfrastructureFailed)
		current.failPendingStep = false
		if errors.Is(err, ErrStorageUnavailable) {
			m.tripStorage(active)
		}
		return err
	}
	current.task = stored
	current.plan = nextPlan
	current.leasePersisted = false
	return publishCommitted(m, committed, active)
}

func (m *Manager) finishExecution(
	current *activeTask,
	result ProcessResult,
	outcome Outcome,
	failPending bool,
	active map[string]*activeTask,
) (Task, error) {
	finished, err := m.persistTerminal(
		current,
		result,
		outcome,
		failPending,
		current.process != nil,
		active,
	)
	if err != nil {
		return current.task, err
	}
	current.task = finished
	if current.process != nil {
		current.leasePersisted = false
	}
	m.stopActive(current)
	m.maybeStartClose(current)
	return finished, nil
}

func (m *Manager) persistCommittedCreateFailure(
	current *activeTask,
	active map[string]*activeTask,
) (Task, error) {
	outcome := current.execution.resolve(OutcomeInfrastructureFailed)
	for attempt := 0; attempt < 2; attempt++ {
		finishedAt := m.clock.Now()
		finished, err := ApplyTransition(current.task, Transition{
			From: current.task.Status, To: StatusFinished, Outcome: outcome, At: finishedAt,
			ErrorCode: outcomeErrorCode(outcome), ErrorMessage: outcomeErrorMessage(outcome),
		})
		if err != nil {
			return current.task, err
		}
		steps := terminalStepMutations(current, ProcessResult{}, outcome, false, finishedAt)
		stored, _, err := m.store.Apply(context.Background(), Mutation{
			Task: finished, Expected: StatusQueued, Steps: steps,
			Events: []EventDraft{
				eventDraft(current.task.ID, EventTaskFinished, finishedAt, map[string]any{"outcome": outcome}),
			},
		})
		if err == nil {
			current.task = stored
			releaseExecutionBoundary(current)
			m.stopActive(current)
			removeActiveTask(active, current.task.ID)
			return stored, nil
		}
		if errors.Is(err, ErrConflict) && attempt == 0 {
			continue
		}
		current.recoveryRequired = true
		m.stopActive(current)
		removeActiveTask(active, current.task.ID)
		if !errors.Is(err, ErrConflict) {
			m.tripStorage(active)
		}
		return current.task, err
	}
	panic("unreachable")
}

func (m *Manager) persistTerminal(
	current *activeTask,
	result ProcessResult,
	outcome Outcome,
	failPending bool,
	deleteLease bool,
	active map[string]*activeTask,
) (Task, error) {
	if deleteLease && current.process != nil && !current.closeComplete {
		return current.task, ErrConflict
	}
	outcome = current.execution.resolve(outcome)
	if outcome != OutcomeInfrastructureFailed {
		failPending = false
	}
	for attempt := 0; attempt < 2; attempt++ {
		finishedAt := m.clock.Now()
		updatedTask := current.task
		updatedTask.ActiveStep = ""
		steps := terminalStepMutations(current, result, outcome, failPending, finishedAt)
		finished, err := m.persistFinished(current, updatedTask, outcome, deleteLease, steps, active)
		if !errors.Is(err, ErrConflict) {
			return finished, err
		}
		if attempt == 0 {
			current.execution.replaceOutcome(OutcomeInfrastructureFailed)
			current.failPendingStep = failPending
			result = ProcessResult{Err: ErrConflict}
			outcome = OutcomeInfrastructureFailed
			continue
		}
		current.recoveryRequired = true
		m.stopActive(current)
		if current.process == nil {
			removeActiveTask(active, current.task.ID)
		} else {
			m.maybeStartClose(current)
		}
		return current.task, ErrConflict
	}
	panic("unreachable")
}

func releaseExecutionBoundary(current *activeTask) {
	if current == nil || current.boundaryReleased {
		return
	}
	current.boundaryReleased = true
	if boundary, ok := current.boundary.(ManagedExecutionBoundary); ok {
		_ = boundary.Release()
	}
}

func removeActiveTask(active map[string]*activeTask, taskID string) {
	current := active[taskID]
	if current != nil {
		abortArtifactSink(current)
		releaseExecutionBoundary(current)
	}
	delete(active, taskID)
}

func abortArtifactSink(current *activeTask) {
	if current == nil || current.artifactSink == nil {
		return
	}
	_ = current.artifactSink.Abort(context.Background())
	current.artifactSink = nil
}

func terminalStepMutations(
	current *activeTask,
	result ProcessResult,
	outcome Outcome,
	failPending bool,
	finishedAt time.Time,
) []StepMutation {
	steps := current.task.Steps
	mutations := make([]StepMutation, 0, len(steps)-current.nextStep)
	if current.task.ActiveStep != "" && current.nextStep < len(steps) {
		step := steps[current.nextStep]
		expected := step.Status
		if outcome == OutcomeSucceeded {
			step.Status = StepSucceeded
		} else {
			step.Status = StepFailed
			step.ErrorCode = outcomeErrorCode(outcome)
		}
		step.FinishedAt = timePointer(finishedAt)
		if result.Err == nil {
			step.ExitCode = intPointer(result.ExitCode)
		}
		mutations = append(mutations, StepMutation{Step: step, Expected: expected})
	}
	start := current.nextStep
	if current.task.ActiveStep != "" {
		start++
	} else if failPending && start < len(steps) {
		step := steps[start]
		step.Status = StepFailed
		step.FinishedAt = timePointer(finishedAt)
		step.ErrorCode = outcomeErrorCode(outcome)
		mutations = append(mutations, StepMutation{Step: step, Expected: StepPending})
		start++
	}
	for index := start; index < len(steps); index++ {
		if steps[index].Status != StepPending {
			continue
		}
		step := steps[index]
		step.Status = StepSkipped
		step.FinishedAt = timePointer(finishedAt)
		mutations = append(mutations, StepMutation{Step: step, Expected: StepPending})
	}
	return mutations
}

func (m *Manager) persistFinished(
	owner *activeTask,
	current Task,
	outcome Outcome,
	deleteLease bool,
	steps []StepMutation,
	active map[string]*activeTask,
) (Task, error) {
	finishedAt := m.clock.Now()
	finished, err := ApplyTransition(current, Transition{
		From: current.Status, To: StatusFinished, Outcome: outcome, At: finishedAt,
		ErrorCode: outcomeErrorCode(outcome), ErrorMessage: outcomeErrorMessage(outcome),
	})
	if err != nil {
		return current, err
	}
	artifacts, artifactErr := m.finalizeTaskArtifacts(
		context.Background(),
		owner,
		current,
		finishedAt,
		outcome,
		steps,
	)
	if artifactErr != nil {
		m.tripStorage(active)
		return current, ErrStorageUnavailable
	}
	events := make([]EventDraft, 0, len(artifacts)+2)
	if step, ok := finishedRunningStep(steps); ok &&
		current.Kind == KindCMakeBuild {
		events = append(events, eventDraft(
			current.ID,
			EventTaskStepFinished,
			finishedAt,
			stepEventPayload(step),
		))
	}
	for _, artifact := range artifacts {
		events = append(events, eventDraft(
			current.ID,
			EventArtifactCreated,
			finishedAt,
			map[string]any{"artifactId": artifact.ID, "kind": artifact.Kind},
		))
	}
	events = append(events,
		eventDraft(current.ID, EventTaskFinished, finishedAt, map[string]any{"outcome": outcome}),
	)
	stored, committed, err := m.store.Apply(context.Background(), Mutation{
		Task: finished, Expected: current.Status, Steps: steps, Events: events,
		DeleteLease: deleteLease, Artifacts: artifacts,
	})
	if err != nil {
		if !errors.Is(err, ErrConflict) {
			m.tripStorage(active)
		}
		return current, err
	}
	owner.task = stored
	releaseExecutionBoundary(owner)
	if deleteLease && owner.process != nil {
		owner.leasePersisted = false
	}
	if !m.publishAll(committed) {
		m.tripPublisher(active)
		return stored, ErrStorageUnavailable
	}
	return stored, nil
}

func finishedRunningStep(mutations []StepMutation) (StepSnapshot, bool) {
	for _, mutation := range mutations {
		if mutation.Expected == StepRunning {
			return mutation.Step, true
		}
	}
	return StepSnapshot{}, false
}

func publishCommitted(m *Manager, events []Event, active map[string]*activeTask) error {
	if !m.publishAll(events) {
		m.tripPublisher(active)
		return ErrStorageUnavailable
	}
	return nil
}

func timePointer(value time.Time) *time.Time { return &value }

func intPointer(value int) *int { return &value }
