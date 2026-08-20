package testrun

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"time"

	"unit-test-ide.local/test-service/internal/task"
	"unit-test-ide.local/test-service/internal/testdomain"
)

const embeddedTaskTimeout = 24 * time.Hour

const MaxProfileCount = 250

type ProfileExpectation struct {
	InvocationID string
	Iteration    int64
	FileName     string
}

type InvocationOutcome struct {
	InvocationID string
	Iteration    int64
	ExitCode     int
	Crashed      bool
	TimedOut     bool
}

type ProfileAllocator interface {
	Decorate(ProfileExpectation, task.ProcessSpec) (task.ProcessSpec, error)
}

type EmbeddedRequest struct {
	TaskID         string
	Run            testdomain.TestRun
	PreparedBuild  PreparedBuild
	Catalog        testdomain.Catalog
	Allocator      ProfileAllocator
	MaxConcurrency int
}

type EmbeddedRun interface {
	Steps() []task.ExecutionStep
	Interpret(
		context.Context,
		task.Task,
		task.ExecutionStep,
		task.ProcessResult,
	) (task.StepVerdict, error)
	ObserveOutput(
		context.Context,
		task.Task,
		task.ExecutionStep,
		task.ProcessOutput,
	) error
	DrainDomainEvents() []task.DomainEvent
	Finish(
		context.Context,
		time.Time,
		task.Outcome,
	) (testdomain.TestRun, error)
	Expectations() []ProfileExpectation
}

type embeddedCatalogRefresher interface {
	PrepareEmbedded(
		context.Context,
		RefreshRequest,
	) (RefreshedCatalog, error)
}

func (coordinator *Coordinator) PrepareEmbedded(
	ctx context.Context,
	request EmbeddedRequest,
) (EmbeddedRun, error) {
	if coordinator == nil || ctx == nil ||
		!lowerHexID(request.TaskID, 32) ||
		nilCoordinatorPort(request.PreparedBuild) ||
		nilCoordinatorPort(request.Allocator) ||
		request.MaxConcurrency < 1 ||
		request.MaxConcurrency > maxScheduleConcurrency {
		return nil, task.ErrInvalidArgument
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	run, err := testdomain.NewTestRun(request.Run)
	if err != nil || run.TaskID != request.TaskID ||
		run.Status != testdomain.RunQueued ||
		len(run.Results) != 0 ||
		run.Summary.Iterations < 1 ||
		run.Summary.Iterations > MaxRepeatCount {
		return nil, task.ErrInvalidArgument
	}
	persisted, err := coordinator.config.Runs.GetRun(ctx, run.RunID)
	if err != nil {
		return nil, err
	}
	persisted, err = testdomain.NewTestRun(persisted)
	if err != nil || !reflect.DeepEqual(persisted, run) {
		return nil, task.ErrConflict
	}
	prepared := request.PreparedBuild
	project := prepared.Project()
	profile := prepared.Profile()
	instance := prepared.Toolchain()
	if !lowerHexID(prepared.WorkspaceGeneration(), 64) ||
		project.ID != run.ProjectID ||
		profile.ID != run.ProfileID ||
		profile.ProjectID != project.ID ||
		instance.ID == "" || instance.ID != run.ToolchainID {
		return nil, task.ErrConflict
	}
	catalog, err := testdomain.NewCatalog(request.Catalog)
	if err != nil || catalog.ProjectID != project.ID ||
		catalog.ProfileID != profile.ID ||
		catalog.Revision != run.CatalogRevision {
		return nil, task.ErrInvalidArgument
	}
	current, err := coordinator.config.Catalogs.GetCatalog(
		ctx,
		project.ID,
		profile.ID,
	)
	if err != nil {
		return nil, err
	}
	current, err = testdomain.NewCatalog(current)
	if err != nil || !reflect.DeepEqual(current, catalog) {
		return nil, testdomain.ErrCatalogStale
	}
	if _, err := resolvePlannerSelection(
		catalog,
		run.SelectionSnapshot,
	); err != nil {
		return nil, err
	}
	refresher, ok := coordinator.config.Refresher.(embeddedCatalogRefresher)
	if !ok || nilCoordinatorPort(refresher) {
		return nil, task.ErrStorageUnavailable
	}
	refreshed, err := refresher.PrepareEmbedded(
		ctx,
		RefreshRequest{
			TaskID:              request.TaskID,
			WorkspaceGeneration: prepared.WorkspaceGeneration(),
			Project:             project,
			Profile:             profile,
			Toolchain:           instance,
			Targets:             prepared.Targets(),
		},
	)
	if err != nil {
		return nil, err
	}
	refreshedCatalog, err := testdomain.NewCatalog(refreshed.Catalog)
	if err != nil || !sameEmbeddedCatalog(catalog, refreshedCatalog) {
		return nil, testdomain.ErrCatalogStale
	}
	planned, err := PlanRun(
		ctx,
		PlannerInput{
			Catalog:        catalog,
			Selection:      run.SelectionSnapshot,
			Bindings:       refreshed.Bindings,
			Runner:         coordinator.config.Runner,
			RepeatCount:    run.Summary.Iterations,
			TaskTimeout:    embeddedTaskTimeout,
			MaxConcurrency: request.MaxConcurrency,
		},
	)
	if err != nil {
		return nil, err
	}
	if len(planned.Invocations) > MaxProfileCount {
		return nil, task.ErrInvalidArgument
	}
	expectations := make(
		[]ProfileExpectation,
		len(planned.Invocations),
	)
	for index := range planned.Invocations {
		invocation := &planned.Invocations[index]
		expectation := ProfileExpectation{
			InvocationID: invocation.Job.ID,
			Iteration:    invocation.Job.Iteration,
			FileName: fmt.Sprintf(
				"p-%06d-i-%06d-%%p-%%m.profraw",
				index+1,
				invocation.Job.Iteration,
			),
		}
		decorated, err := request.Allocator.Decorate(
			expectation,
			invocation.Step.Process,
		)
		if err != nil {
			return nil, err
		}
		if !sameEmbeddedProcessTarget(
			invocation.Step.Process,
			decorated,
		) || countEmbeddedEnvironment(
			decorated.Env,
			"LLVM_PROFILE_FILE",
		) != 1 {
			return nil, task.ErrInvalidArgument
		}
		invocation.Step.Process = decorated
		expectations[index] = expectation
	}
	if err := pinPlannedInvocations(prepared, planned); err != nil {
		return nil, err
	}
	interpreter, err := NewInterpreter(
		run.RunID,
		coordinator.config.Runs,
		planned,
	)
	if err != nil {
		return nil, err
	}
	steps, invocationSteps, waveInvocations, err :=
		buildRunWaveSteps(planned)
	if err != nil {
		return nil, err
	}
	expectedResults := int64(0)
	for _, invocation := range planned.Invocations {
		expectedResults += int64(len(invocation.ExpectedCases))
	}
	return &embeddedExecution{
		taskID:          request.TaskID,
		runID:           run.RunID,
		runs:            coordinator.config.Runs,
		interpreter:     interpreter,
		steps:           steps,
		invocationSteps: invocationSteps,
		waveInvocations: waveInvocations,
		expectations:    expectations,
		expectedResults: expectedResults,
		catalogRevision: catalog.Revision,
	}, nil
}

func pinPlannedInvocations(
	prepared PreparedBuild,
	planned PlannedRun,
) error {
	seen := make(map[string]struct{})
	for _, invocation := range planned.Invocations {
		state := invocation.ParseInput.Descriptor.Executable
		if state.Path == "" {
			continue
		}
		if state.Identity == "" || state.SHA256 == "" {
			return task.ErrInvalidArgument
		}
		key := state.Path + "\x00" + state.Identity + "\x00" + state.SHA256
		if _, exists := seen[key]; exists {
			continue
		}
		if err := prepared.AllowTestExecutable(state); err != nil {
			return err
		}
		seen[key] = struct{}{}
	}
	return nil
}

func sameEmbeddedCatalog(
	left,
	right testdomain.Catalog,
) bool {
	left.GeneratedAt = right.GeneratedAt
	return reflect.DeepEqual(left, right)
}

func sameEmbeddedProcessTarget(left, right task.ProcessSpec) bool {
	return left.Executable == right.Executable &&
		reflect.DeepEqual(left.Args, right.Args) &&
		left.Dir == right.Dir &&
		len(left.Batch) == 0 && len(right.Batch) == 0
}

func countEmbeddedEnvironment(values []string, key string) int {
	count := 0
	for _, value := range values {
		name, _, found := strings.Cut(value, "=")
		if found && strings.EqualFold(name, key) {
			count++
		}
	}
	return count
}

type embeddedExecution struct {
	mu sync.Mutex

	taskID          string
	runID           string
	runs            task.TestRunRepository
	interpreter     *Interpreter
	steps           []task.ExecutionStep
	invocationSteps map[string]task.ExecutionStep
	waveInvocations map[string]map[string]struct{}
	expectations    []ProfileExpectation
	expectedResults int64
	catalogRevision string
	events          []task.DomainEvent
	runStarted      bool
	finished        bool
}

func (execution *embeddedExecution) Steps() []task.ExecutionStep {
	if execution == nil {
		return nil
	}
	execution.mu.Lock()
	defer execution.mu.Unlock()
	return cloneCoordinatorSteps(execution.steps)
}

func (execution *embeddedExecution) Expectations() []ProfileExpectation {
	if execution == nil {
		return nil
	}
	execution.mu.Lock()
	defer execution.mu.Unlock()
	return append([]ProfileExpectation(nil), execution.expectations...)
}

func (execution *embeddedExecution) Interpret(
	ctx context.Context,
	current task.Task,
	step task.ExecutionStep,
	result task.ProcessResult,
) (task.StepVerdict, error) {
	if execution == nil || ctx == nil || step.Kind != task.StepTestRun {
		return task.StepVerdictDefault, task.ErrInvalidArgument
	}
	execution.mu.Lock()
	if err := execution.ensureRunStarted(ctx, current); err != nil {
		execution.mu.Unlock()
		return task.StepVerdictDefault, err
	}
	interpreter := execution.interpreter
	invocations := execution.waveInvocations[step.ID]
	invocationSteps := execution.invocationSteps
	execution.mu.Unlock()
	if interpreter == nil || len(invocations) == 0 ||
		len(result.Children) != len(invocations) {
		return task.StepVerdictDefault, task.ErrInvalidArgument
	}
	seen := make(map[string]struct{}, len(result.Children))
	for _, child := range result.Children {
		if _, expected := invocations[child.ID]; !expected || child.Err != nil {
			return task.StepVerdictDefault, task.ErrInvalidArgument
		}
		if _, duplicate := seen[child.ID]; duplicate {
			return task.StepVerdictDefault, task.ErrInvalidArgument
		}
		seen[child.ID] = struct{}{}
		verdict, err := interpreter.Interpret(
			ctx,
			current,
			invocationSteps[child.ID],
			task.ProcessResult{
				ExitCode: child.ExitCode,
				TimedOut: child.TimedOut,
			},
		)
		if err != nil || verdict != task.StepVerdictSucceeded {
			return task.StepVerdictDefault, err
		}
	}
	return task.StepVerdictSucceeded, nil
}

func (execution *embeddedExecution) ObserveOutput(
	ctx context.Context,
	current task.Task,
	step task.ExecutionStep,
	output task.ProcessOutput,
) error {
	if execution == nil || ctx == nil || step.Kind != task.StepTestRun {
		return task.ErrInvalidArgument
	}
	execution.mu.Lock()
	if err := execution.ensureRunStarted(ctx, current); err != nil {
		execution.mu.Unlock()
		return err
	}
	interpreter := execution.interpreter
	invocations := execution.waveInvocations[step.ID]
	invocationStep := execution.invocationSteps[output.Source]
	execution.mu.Unlock()
	if interpreter == nil || len(invocations) == 0 {
		return task.ErrInvalidArgument
	}
	if _, expected := invocations[output.Source]; !expected {
		return task.ErrInvalidArgument
	}
	return interpreter.ObserveOutput(
		ctx,
		current,
		invocationStep,
		task.ProcessOutput{
			Stream: output.Stream,
			Data:   append([]byte(nil), output.Data...),
		},
	)
}

func (execution *embeddedExecution) DrainDomainEvents() []task.DomainEvent {
	if execution == nil {
		return nil
	}
	execution.mu.Lock()
	events := drainDomainEvents(&execution.events)
	interpreter := execution.interpreter
	execution.mu.Unlock()
	if interpreter != nil {
		events = append(events, interpreter.DrainDomainEvents()...)
	}
	return events
}

func (execution *embeddedExecution) Finish(
	ctx context.Context,
	finishedAt time.Time,
	outcome task.Outcome,
) (testdomain.TestRun, error) {
	if execution == nil || ctx == nil || finishedAt.IsZero() ||
		!validEmbeddedOutcome(outcome) {
		return testdomain.TestRun{}, task.ErrInvalidArgument
	}
	execution.mu.Lock()
	defer execution.mu.Unlock()
	if execution.finished {
		return testdomain.TestRun{}, task.ErrConflict
	}
	run, err := execution.runs.GetRun(ctx, execution.runID)
	if err != nil {
		return testdomain.TestRun{}, err
	}
	run, err = testdomain.NewTestRun(run)
	if err != nil {
		return testdomain.TestRun{}, err
	}
	if run.TaskID != execution.taskID ||
		run.CatalogRevision != execution.catalogRevision {
		return testdomain.TestRun{}, task.ErrConflict
	}
	switch run.Status {
	case testdomain.RunQueued:
		if execution.runStarted || run.StartedAt != nil ||
			len(run.Results) != 0 || finishedAt.Before(run.CreatedAt) {
			return testdomain.TestRun{}, task.ErrConflict
		}
	case testdomain.RunRunning:
		if run.StartedAt == nil || finishedAt.Before(*run.StartedAt) {
			return testdomain.TestRun{}, task.ErrConflict
		}
	default:
		return testdomain.TestRun{}, task.ErrConflict
	}
	summary, incomplete, err := Summarize(
		run.Results,
		run.Summary.Iterations,
	)
	if err != nil {
		return testdomain.TestRun{}, err
	}
	if summary.Total != execution.expectedResults {
		incomplete = true
	}
	run.Status = testdomain.RunCompleted
	run.Outcome = completedRunOutcome(outcome, summary, incomplete)
	run.FinishedAt = &finishedAt
	run.Summary = summary
	run.Incomplete = incomplete
	run.ResultRevision, err = testdomain.ResultRevision(run.Results)
	if err != nil {
		return testdomain.TestRun{}, err
	}
	validated, err := testdomain.NewTestRun(run)
	if err != nil {
		return testdomain.TestRun{}, err
	}
	event, err := newDomainEvent(
		task.EventTestRunFinished,
		map[string]any{
			"runId":          validated.RunID,
			"outcome":        validated.Outcome,
			"summary":        validated.Summary,
			"resultRevision": validated.ResultRevision,
			"incomplete":     validated.Incomplete,
		},
	)
	if err != nil {
		return testdomain.TestRun{}, err
	}
	execution.events = append(execution.events, event)
	execution.finished = true
	return validated, nil
}

func (execution *embeddedExecution) ensureRunStarted(
	ctx context.Context,
	current task.Task,
) error {
	if current.ID != execution.taskID ||
		current.Kind != task.KindCoverageRun ||
		current.StartedAt == nil {
		return task.ErrInvalidArgument
	}
	if execution.runStarted {
		return nil
	}
	if err := execution.runs.StartRun(
		ctx,
		execution.runID,
		*current.StartedAt,
	); err != nil {
		return err
	}
	if err := appendDomainEvent(
		&execution.events,
		task.EventTestRunStarted,
		map[string]any{
			"runId":           execution.runID,
			"catalogRevision": execution.catalogRevision,
			"total":           execution.expectedResults,
		},
	); err != nil {
		return err
	}
	execution.runStarted = true
	return nil
}

func validEmbeddedOutcome(outcome task.Outcome) bool {
	switch outcome {
	case task.OutcomeSucceeded,
		task.OutcomeCommandFailed,
		task.OutcomeCancelled,
		task.OutcomeTimedOut,
		task.OutcomeInterrupted,
		task.OutcomeInfrastructureFailed:
		return true
	default:
		return false
	}
}

var _ EmbeddedRun = (*embeddedExecution)(nil)
