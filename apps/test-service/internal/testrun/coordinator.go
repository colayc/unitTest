package testrun

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"sync"
	"time"

	"unit-test-ide.local/test-service/internal/build"
	"unit-test-ide.local/test-service/internal/cmake"
	"unit-test-ide.local/test-service/internal/ctest"
	"unit-test-ide.local/test-service/internal/task"
	"unit-test-ide.local/test-service/internal/testdomain"
	"unit-test-ide.local/test-service/internal/toolchain"
	"unit-test-ide.local/test-service/internal/workspace"
)

const (
	maxCoordinatorRuntimeSteps = 10_000
	maxCoordinatorBatchSteps   = 256
)

type PreparedBuild interface {
	Plan() task.ExecutionPlan
	Boundary() task.ExecutionBoundary
	WorkspaceGeneration() string
	Project() workspace.ProjectConfig
	Profile() cmake.BuildProfile
	Toolchain() toolchain.Instance
	Targets() []cmake.Target
	AllowTestExecutable(cmake.FingerprintFile) error
	ReleaseIfUnadopted()
}

type PrepareBuild func(
	context.Context,
	build.StartRequest,
) (PreparedBuild, error)

type CatalogReader interface {
	GetCatalog(
		context.Context,
		string,
		string,
	) (testdomain.Catalog, error)
}

type CatalogRefresher interface {
	PrepareAfterBuild(
		context.Context,
		RefreshRequest,
	) (CatalogRefresh, RefreshProgress, error)
}

type CatalogRefresh interface {
	ObserveOutput(
		context.Context,
		task.ExecutionStep,
		task.ProcessOutput,
	) error
	Interpret(
		context.Context,
		task.ExecutionStep,
		task.ProcessResult,
	) (task.StepVerdict, error)
	AfterStep(
		context.Context,
		task.ExecutionStep,
	) (RefreshProgress, error)
}

type RefreshProgress struct {
	Steps    []task.ExecutionStep
	Pins     []cmake.FingerprintFile
	Snapshot *RefreshedCatalog
}

type TaskStarter interface {
	Start(context.Context, task.StartRequest) (task.Task, error)
}

type QueuedRunRebinder interface {
	RebindQueuedRun(
		context.Context,
		string,
		string,
		testdomain.Catalog,
		testdomain.SelectionSnapshot,
	) error
}

type RefreshRequest struct {
	TaskID              string
	WorkspaceGeneration string
	Project             workspace.ProjectConfig
	Profile             cmake.BuildProfile
	Toolchain           toolchain.Instance
	Targets             []cmake.Target
}

type RefreshedCatalog struct {
	Catalog  testdomain.Catalog
	Bindings []ContainerBinding
}

func (value RefreshedCatalog) Clone() RefreshedCatalog {
	result := RefreshedCatalog{
		Catalog:  value.Catalog.Clone(),
		Bindings: make([]ContainerBinding, len(value.Bindings)),
	}
	for index, binding := range value.Bindings {
		result.Bindings[index] = binding
		result.Bindings[index].Descriptor =
			cloneExecutionDescriptor(binding.Descriptor)
	}
	return result
}

type CoordinatorConfig struct {
	PrepareBuild PrepareBuild
	Catalogs     CatalogReader
	Refresher    CatalogRefresher
	Tasks        TaskStarter
	Runs         task.TestRunRepository
	Runner       *ctest.Runner
	Limits       testdomain.Limits
}

type Coordinator struct {
	config CoordinatorConfig
}

func NewCoordinator(config CoordinatorConfig) (*Coordinator, error) {
	if config.PrepareBuild == nil ||
		nilCoordinatorPort(config.Catalogs) ||
		nilCoordinatorPort(config.Refresher) ||
		nilCoordinatorPort(config.Tasks) ||
		nilCoordinatorPort(config.Runs) ||
		config.Runner == nil ||
		config.Limits.MaxSelectionSize < 1 ||
		config.Limits.MaxSelectionSize > 100_000 {
		return nil, task.ErrInvalidArgument
	}
	if _, ok := config.Runs.(QueuedRunRebinder); !ok {
		return nil, task.ErrInvalidArgument
	}
	return &Coordinator{config: config}, nil
}

type RunRequest struct {
	IdempotencyKey      string
	WorkspaceGeneration string
	ProjectID           string
	BuildProfileID      string
	CatalogRevision     string
	TargetIDs           []string
	Jobs                int
	Timeout             time.Duration
	RepeatCount         int64
	MaxConcurrency      int
	Selection           testdomain.Selection
}

func (coordinator *Coordinator) StartRun(
	ctx context.Context,
	request RunRequest,
) (task.Task, testdomain.TestRun, error) {
	if coordinator == nil || ctx == nil ||
		!lowerHexID(request.IdempotencyKey, 32) ||
		!lowerHexID(request.WorkspaceGeneration, 64) ||
		request.ProjectID == "" ||
		!lowerHexID(request.BuildProfileID, 64) ||
		!lowerHexID(request.CatalogRevision, 64) ||
		request.Jobs < 1 || request.Jobs > 256 ||
		request.Timeout < time.Millisecond ||
		request.Timeout > 24*time.Hour ||
		request.Timeout%time.Millisecond != 0 ||
		request.RepeatCount < 1 ||
		request.RepeatCount > MaxRepeatCount ||
		request.MaxConcurrency < 1 ||
		request.MaxConcurrency > maxScheduleConcurrency {
		return task.Task{}, testdomain.TestRun{},
			task.ErrInvalidArgument
	}
	targetIDs, err := canonicalCoordinatorTargetIDs(
		request.TargetIDs,
	)
	if err != nil {
		return task.Task{}, testdomain.TestRun{}, err
	}
	request.TargetIDs = targetIDs
	prepared, err := coordinator.config.PrepareBuild(
		ctx,
		build.StartRequest{
			IdempotencyKey:      request.IdempotencyKey,
			WorkspaceGeneration: request.WorkspaceGeneration,
			ProjectID:           request.ProjectID,
			BuildProfileID:      request.BuildProfileID,
			TargetIDs: append(
				[]string(nil),
				request.TargetIDs...,
			),
			Jobs:    request.Jobs,
			Timeout: request.Timeout,
		},
	)
	if err != nil {
		return task.Task{}, testdomain.TestRun{}, err
	}
	if nilCoordinatorPort(prepared) {
		return task.Task{}, testdomain.TestRun{},
			task.ErrStorageUnavailable
	}
	defer prepared.ReleaseIfUnadopted()
	project := prepared.Project()
	profile := prepared.Profile()
	instance := prepared.Toolchain()
	if prepared.WorkspaceGeneration() !=
		request.WorkspaceGeneration ||
		project.ID != request.ProjectID ||
		profile.ID != request.BuildProfileID ||
		profile.ProjectID != project.ID ||
		instance.ID == "" {
		return task.Task{}, testdomain.TestRun{},
			task.ErrInvalidArgument
	}
	catalog, err := coordinator.config.Catalogs.GetCatalog(
		ctx,
		project.ID,
		profile.ID,
	)
	if err != nil {
		return task.Task{}, testdomain.TestRun{}, err
	}
	catalog, err = testdomain.NewCatalog(catalog)
	if err != nil {
		return task.Task{}, testdomain.TestRun{},
			task.ErrInvalidArgument
	}
	if catalog.Revision != request.CatalogRevision {
		return task.Task{}, testdomain.TestRun{},
			testdomain.ErrCatalogStale
	}
	selection, err := Resolve(
		ctx,
		catalog,
		request.Selection,
		coordinator.config.Runs,
		coordinator.config.Limits,
	)
	if err != nil {
		return task.Task{}, testdomain.TestRun{}, err
	}
	requestJSON, err := encodeRunRequest(request)
	if err != nil {
		return task.Task{}, testdomain.TestRun{},
			task.ErrInvalidArgument
	}
	runID := coordinatorRunID(
		request.IdempotencyKey,
		requestJSON,
	)
	run := testdomain.TestRun{
		RunID:             runID,
		IdempotencyKey:    request.IdempotencyKey,
		ProjectID:         project.ID,
		ProfileID:         profile.ID,
		ToolchainID:       instance.ID,
		CatalogRevision:   catalog.Revision,
		SelectionSnapshot: selection.Clone(),
		Status:            testdomain.RunQueued,
		Summary: testdomain.RunSummary{
			Iterations: request.RepeatCount,
		},
		ResultRevision: testdomain.EmptyResultRevision(),
		Incomplete:     true,
	}
	plan := prepared.Plan()
	if len(plan.Steps) == 0 {
		return task.Task{}, testdomain.TestRun{},
			task.ErrInvalidArgument
	}
	execution, err := coordinator.newRunExecution(
		prepared,
		runID,
		selection,
		request,
	)
	if err != nil {
		return task.Task{}, testdomain.TestRun{}, err
	}
	started, err := coordinator.config.Tasks.Start(
		ctx,
		task.StartRequest{
			IdempotencyKey:      request.IdempotencyKey,
			Kind:                task.KindTestRun,
			Request:             requestJSON,
			WorkspaceGeneration: request.WorkspaceGeneration,
			Timeout:             request.Timeout,
			Plan:                plan,
			Boundary:            prepared.Boundary(),
			Continuation:        execution,
			ResultInterpreter:   execution,
			TestRun:             &run,
		},
	)
	if err != nil {
		return task.Task{}, testdomain.TestRun{}, err
	}
	persisted, err := coordinator.config.Runs.GetRun(
		ctx,
		runID,
	)
	if err != nil {
		return started, testdomain.TestRun{}, err
	}
	if persisted.TaskID != started.ID {
		return started, testdomain.TestRun{},
			task.ErrConflict
	}
	return started, persisted, nil
}

func (coordinator *Coordinator) newRunExecution(
	prepared PreparedBuild,
	runID string,
	selection testdomain.SelectionSnapshot,
	request RunRequest,
) (*runExecution, error) {
	if coordinator == nil || nilCoordinatorPort(prepared) ||
		!lowerHexID(runID, 32) ||
		request.RepeatCount < 1 ||
		request.RepeatCount > MaxRepeatCount ||
		request.Timeout < time.Millisecond ||
		request.Timeout > 24*time.Hour ||
		request.MaxConcurrency < 1 ||
		request.MaxConcurrency > maxScheduleConcurrency {
		return nil, task.ErrInvalidArgument
	}
	plan := prepared.Plan()
	if len(plan.Steps) == 0 {
		return nil, task.ErrInvalidArgument
	}
	return &runExecution{
		runID:          runID,
		lastBuildStep:  plan.Steps[len(plan.Steps)-1].ID,
		prepared:       prepared,
		refresher:      coordinator.config.Refresher,
		runs:           coordinator.config.Runs,
		runner:         coordinator.config.Runner,
		selection:      selection.Clone(),
		repeatCount:    request.RepeatCount,
		taskTimeout:    request.Timeout,
		maxConcurrency: request.MaxConcurrency,
		runtimeSteps:   len(plan.Steps),
	}, nil
}

type runExecution struct {
	mu sync.Mutex

	runID          string
	lastBuildStep  string
	prepared       PreparedBuild
	refresher      CatalogRefresher
	runs           task.TestRunRepository
	runner         *ctest.Runner
	selection      testdomain.SelectionSnapshot
	repeatCount    int64
	taskTimeout    time.Duration
	maxConcurrency int
	runtimeSteps   int

	taskID            string
	initialized       bool
	interpreter       *Interpreter
	refresh           CatalogRefresh
	steps             []task.ExecutionStep
	invocationSteps   map[string]task.ExecutionStep
	waveInvocations   map[string]map[string]struct{}
	next              int
	lastIssued        string
	pinned            map[string]struct{}
	refreshLastIssued string
	expectedResults   int64
	events            []task.DomainEvent
	discoveryStarted  bool
	catalogPublished  bool
	runStarted        bool
}

func (execution *runExecution) AfterStep(
	ctx context.Context,
	current task.Task,
	step task.ExecutionStep,
	result task.StepResult,
) (task.Continuation, error) {
	if execution == nil || ctx == nil ||
		current.ID == "" ||
		current.Kind != task.KindTestRun ||
		result.Verdict != task.StepVerdictSucceeded {
		return task.Continuation{}, task.ErrInvalidArgument
	}
	execution.mu.Lock()
	defer execution.mu.Unlock()
	if execution.taskID == "" {
		execution.taskID = current.ID
	}
	if execution.taskID != current.ID {
		return task.Continuation{}, task.ErrInvalidArgument
	}
	if !execution.initialized {
		if execution.refresh == nil {
			if step.ID != execution.lastBuildStep {
				return task.Continuation{}, nil
			}
			refresh, progress, err :=
				execution.refresher.PrepareAfterBuild(
					ctx,
					RefreshRequest{
						TaskID:              current.ID,
						WorkspaceGeneration: execution.prepared.WorkspaceGeneration(),
						Project:             execution.prepared.Project(),
						Profile:             execution.prepared.Profile(),
						Toolchain:           execution.prepared.Toolchain(),
						Targets:             execution.prepared.Targets(),
					},
				)
			if err != nil {
				return task.Continuation{}, err
			}
			if nilCoordinatorPort(refresh) {
				return task.Continuation{},
					task.ErrStorageUnavailable
			}
			execution.refresh = refresh
			if progress.Snapshot == nil &&
				len(progress.Steps) == 0 {
				return task.Continuation{},
					task.ErrInvalidArgument
			}
			return execution.applyRefreshProgress(
				ctx,
				progress,
			)
		}
		if step.Kind != task.StepTestDiscovery {
			return task.Continuation{}, nil
		}
		progress, err := execution.refresh.AfterStep(
			ctx,
			step,
		)
		if err != nil {
			return task.Continuation{}, err
		}
		if progress.Snapshot == nil &&
			len(progress.Steps) == 0 &&
			step.ID == execution.refreshLastIssued {
			return task.Continuation{},
				task.ErrInvalidArgument
		}
		return execution.applyRefreshProgress(ctx, progress)
	}
	if step.Kind != task.StepTestRun ||
		step.ID != execution.lastIssued {
		return task.Continuation{}, nil
	}
	return execution.nextContinuation(), nil
}

func (execution *runExecution) applyRefreshProgress(
	ctx context.Context,
	progress RefreshProgress,
) (task.Continuation, error) {
	if ctx == nil {
		return task.Continuation{}, task.ErrInvalidArgument
	}
	if progress.Snapshot != nil &&
		len(progress.Steps) != 0 {
		return task.Continuation{}, task.ErrInvalidArgument
	}
	if len(progress.Steps) >
		maxCoordinatorRuntimeSteps-execution.runtimeSteps {
		return task.Continuation{}, task.ErrInvalidArgument
	}
	for _, step := range progress.Steps {
		if step.Kind != task.StepTestDiscovery {
			return task.Continuation{},
				task.ErrInvalidArgument
		}
	}
	if err := execution.pinFiles(progress.Pins); err != nil {
		return task.Continuation{}, err
	}
	if progress.Snapshot == nil {
		if len(progress.Steps) != 0 {
			execution.runtimeSteps += len(progress.Steps)
			execution.refreshLastIssued =
				progress.Steps[len(progress.Steps)-1].ID
		}
		return task.Continuation{
			Steps: cloneCoordinatorSteps(progress.Steps),
		}, nil
	}
	if err := execution.initialize(
		ctx,
		progress.Snapshot.Clone(),
	); err != nil {
		return task.Continuation{}, err
	}
	if err := execution.ensureDiscoveryStarted(); err != nil {
		return task.Continuation{}, err
	}
	if err := execution.recordCatalogPublished(
		progress.Snapshot.Catalog,
	); err != nil {
		return task.Continuation{}, err
	}
	return execution.nextContinuation(), nil
}

func (execution *runExecution) initialize(
	ctx context.Context,
	refreshed RefreshedCatalog,
) error {
	catalog, err := testdomain.NewCatalog(refreshed.Catalog)
	if err != nil ||
		catalog.ProjectID != execution.prepared.Project().ID ||
		catalog.ProfileID != execution.prepared.Profile().ID {
		return task.ErrInvalidArgument
	}
	planned, err := PlanRun(
		ctx,
		PlannerInput{
			Catalog:        catalog,
			Selection:      execution.selection,
			Bindings:       refreshed.Bindings,
			Runner:         execution.runner,
			RepeatCount:    execution.repeatCount,
			TaskTimeout:    execution.taskTimeout,
			MaxConcurrency: execution.maxConcurrency,
		},
	)
	if err != nil {
		return err
	}
	if len(planned.Invocations) >
		maxCoordinatorRuntimeSteps-execution.runtimeSteps {
		return task.ErrInvalidArgument
	}
	if catalog.Revision != refreshed.Catalog.Revision {
		return task.ErrInvalidArgument
	}
	currentRevision, err := execution.currentCatalogRevision(ctx)
	if err != nil {
		return err
	}
	if catalog.Revision != currentRevision {
		rebinder := execution.runs.(QueuedRunRebinder)
		if err := rebinder.RebindQueuedRun(
			ctx,
			execution.runID,
			currentRevision,
			catalog,
			execution.selection,
		); err != nil {
			return err
		}
	}
	if err := execution.pinInvocations(planned); err != nil {
		return err
	}
	interpreter, err := NewInterpreter(
		execution.runID,
		execution.runs,
		planned,
	)
	if err != nil {
		return err
	}
	steps, invocationSteps, waveInvocations, err :=
		buildRunWaveSteps(planned)
	if err != nil {
		return err
	}
	execution.steps = steps
	execution.invocationSteps = invocationSteps
	execution.waveInvocations = waveInvocations
	execution.interpreter = interpreter
	for _, invocation := range planned.Invocations {
		execution.expectedResults += int64(
			len(invocation.ExpectedCases),
		)
	}
	execution.initialized = true
	return nil
}

func (execution *runExecution) currentCatalogRevision(
	ctx context.Context,
) (string, error) {
	run, err := execution.runs.GetRun(ctx, execution.runID)
	if err != nil {
		return "", err
	}
	return run.CatalogRevision, nil
}

func (execution *runExecution) pinInvocations(
	planned PlannedRun,
) error {
	files := make([]cmake.FingerprintFile, 0)
	for _, invocation := range planned.Invocations {
		state := invocation.ParseInput.Descriptor.Executable
		if state.Path == "" {
			continue
		}
		files = append(files, state)
	}
	return execution.pinFiles(files)
}

func (execution *runExecution) pinFiles(
	files []cmake.FingerprintFile,
) error {
	if execution.pinned == nil {
		execution.pinned = make(map[string]struct{})
	}
	for _, state := range files {
		if state.Path == "" || state.Identity == "" ||
			state.SHA256 == "" {
			return task.ErrInvalidArgument
		}
		key := state.Path + "\x00" + state.Identity +
			"\x00" + state.SHA256
		if _, exists := execution.pinned[key]; exists {
			continue
		}
		if err := execution.prepared.AllowTestExecutable(
			state,
		); err != nil {
			return err
		}
		execution.pinned[key] = struct{}{}
	}
	return nil
}

func (execution *runExecution) nextContinuation() task.Continuation {
	if execution.next >= len(execution.steps) {
		execution.lastIssued = ""
		return task.Continuation{}
	}
	end := min(
		execution.next+maxCoordinatorBatchSteps,
		len(execution.steps),
	)
	steps := cloneCoordinatorSteps(
		execution.steps[execution.next:end],
	)
	execution.next = end
	execution.lastIssued = steps[len(steps)-1].ID
	return task.Continuation{Steps: steps}
}

func (execution *runExecution) Interpret(
	ctx context.Context,
	current task.Task,
	step task.ExecutionStep,
	result task.ProcessResult,
) (task.StepVerdict, error) {
	if step.Kind != task.StepTestRun {
		execution.mu.Lock()
		refresh := execution.refresh
		initialized := execution.initialized
		if step.Kind == task.StepTestDiscovery &&
			!initialized && refresh != nil {
			if err := execution.ensureDiscoveryStarted(); err != nil {
				execution.mu.Unlock()
				return task.StepVerdictDefault, err
			}
		}
		execution.mu.Unlock()
		if step.Kind == task.StepTestDiscovery &&
			!initialized && refresh != nil {
			return refresh.Interpret(ctx, step, result)
		}
		return task.StepVerdictDefault, nil
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
		return task.StepVerdictDefault,
			task.ErrInvalidArgument
	}
	seen := make(map[string]struct{}, len(result.Children))
	for _, child := range result.Children {
		if _, expected := invocations[child.ID]; !expected ||
			child.Err != nil {
			return task.StepVerdictDefault,
				task.ErrInvalidArgument
		}
		if _, duplicate := seen[child.ID]; duplicate {
			return task.StepVerdictDefault,
				task.ErrInvalidArgument
		}
		seen[child.ID] = struct{}{}
		invocationStep := invocationSteps[child.ID]
		verdict, err := interpreter.Interpret(
			ctx,
			current,
			invocationStep,
			task.ProcessResult{
				ExitCode: child.ExitCode,
				TimedOut: child.TimedOut,
			},
		)
		if err != nil ||
			verdict != task.StepVerdictSucceeded {
			return task.StepVerdictDefault, err
		}
	}
	return task.StepVerdictSucceeded, nil
}

func (execution *runExecution) ObserveOutput(
	ctx context.Context,
	current task.Task,
	step task.ExecutionStep,
	output task.ProcessOutput,
) error {
	if step.Kind != task.StepTestRun {
		execution.mu.Lock()
		refresh := execution.refresh
		initialized := execution.initialized
		execution.mu.Unlock()
		if step.Kind == task.StepTestDiscovery &&
			!initialized && refresh != nil {
			return refresh.ObserveOutput(
				ctx,
				step,
				output,
			)
		}
		return nil
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

func buildRunWaveSteps(
	planned PlannedRun,
) (
	[]task.ExecutionStep,
	map[string]task.ExecutionStep,
	map[string]map[string]struct{},
	error,
) {
	if len(planned.Invocations) == 0 ||
		len(planned.Waves) == 0 {
		return nil, nil, nil, task.ErrInvalidArgument
	}
	invocations := make(
		map[string]PlannedInvocation,
		len(planned.Invocations),
	)
	invocationSteps := make(
		map[string]task.ExecutionStep,
		len(planned.Invocations),
	)
	for _, invocation := range planned.Invocations {
		if _, duplicate := invocations[invocation.Job.ID]; duplicate {
			return nil, nil, nil, task.ErrInvalidArgument
		}
		invocations[invocation.Job.ID] = invocation
		invocationSteps[invocation.Job.ID] =
			cloneCoordinatorSteps(
				[]task.ExecutionStep{invocation.Step},
			)[0]
	}
	steps := make([]task.ExecutionStep, len(planned.Waves))
	waveInvocations := make(
		map[string]map[string]struct{},
		len(planned.Waves),
	)
	seen := make(map[string]struct{}, len(planned.Invocations))
	for index, wave := range planned.Waves {
		if len(wave.Jobs) == 0 ||
			len(wave.Jobs) > maxScheduleConcurrency {
			return nil, nil, nil, task.ErrInvalidArgument
		}
		stepID := fmt.Sprintf("run-wave-%06d", index+1)
		step := task.ExecutionStep{
			ID:   stepID,
			Kind: task.StepTestRun,
			Public: task.CommandSummary{
				Executable: "test-wave",
				Args:       make([]string, 0, len(wave.Jobs)),
			},
			Process: task.ProcessSpec{
				Batch: make(
					[]task.ProcessBatchItem,
					0,
					len(wave.Jobs),
				),
			},
		}
		members := make(
			map[string]struct{},
			len(wave.Jobs),
		)
		for _, job := range wave.Jobs {
			invocation, exists := invocations[job.ID]
			if !exists || invocation.Timeout < time.Millisecond {
				return nil, nil, nil, task.ErrInvalidArgument
			}
			if _, duplicate := seen[job.ID]; duplicate {
				return nil, nil, nil, task.ErrInvalidArgument
			}
			seen[job.ID] = struct{}{}
			members[job.ID] = struct{}{}
			step.Public.Args = append(step.Public.Args, job.ID)
			step.Process.Batch = append(
				step.Process.Batch,
				task.ProcessBatchItem{
					ID:         job.ID,
					Executable: invocation.Step.Process.Executable,
					Args: append(
						[]string(nil),
						invocation.Step.Process.Args...,
					),
					Env: append(
						[]string(nil),
						invocation.Step.Process.Env...,
					),
					EnvUnset: append(
						[]string(nil),
						invocation.Step.Process.EnvUnset...,
					),
					Dir:     invocation.Step.Process.Dir,
					Timeout: invocation.Timeout,
				},
			)
		}
		steps[index] = step
		waveInvocations[stepID] = members
	}
	if len(seen) != len(planned.Invocations) {
		return nil, nil, nil, task.ErrInvalidArgument
	}
	return steps, invocationSteps, waveInvocations, nil
}

func encodeRunRequest(request RunRequest) ([]byte, error) {
	selection, err := testdomain.NewSelection(request.Selection)
	if err != nil {
		return nil, err
	}
	type filterJSON struct {
		Group          string          `json:"group,omitempty"`
		Suite          string          `json:"suite,omitempty"`
		Label          string          `json:"label,omitempty"`
		NameContains   string          `json:"nameContains,omitempty"`
		IncludeItemIDs []testdomain.ID `json:"includeItemIds,omitempty"`
		ExcludeItemIDs []testdomain.ID `json:"excludeItemIds,omitempty"`
	}
	type selectionJSON struct {
		Mode         testdomain.SelectionMode `json:"mode"`
		ContainerIDs []testdomain.ID          `json:"containerIds,omitempty"`
		ItemIDs      []testdomain.ID          `json:"itemIds,omitempty"`
		Filter       filterJSON               `json:"filter,omitempty"`
		RunID        string                   `json:"runId,omitempty"`
	}
	encoded, err := json.Marshal(struct {
		ProjectID       string        `json:"projectId"`
		BuildProfileID  string        `json:"buildProfileId"`
		CatalogRevision string        `json:"catalogRevision"`
		TargetIDs       []string      `json:"targetIds"`
		Jobs            int           `json:"jobs"`
		TimeoutMS       int64         `json:"timeoutMs"`
		RepeatCount     int64         `json:"repeatCount"`
		MaxConcurrency  int           `json:"maxConcurrency"`
		Selection       selectionJSON `json:"selection"`
	}{
		ProjectID:       request.ProjectID,
		BuildProfileID:  request.BuildProfileID,
		CatalogRevision: request.CatalogRevision,
		TargetIDs: append(
			[]string{},
			request.TargetIDs...,
		),
		Jobs:           request.Jobs,
		TimeoutMS:      request.Timeout.Milliseconds(),
		RepeatCount:    request.RepeatCount,
		MaxConcurrency: request.MaxConcurrency,
		Selection: selectionJSON{
			Mode: selection.Mode,
			ContainerIDs: append(
				[]testdomain.ID(nil),
				selection.ContainerIDs...,
			),
			ItemIDs: append(
				[]testdomain.ID(nil),
				selection.ItemIDs...,
			),
			Filter: filterJSON{
				Group:        selection.Filter.Group,
				Suite:        selection.Filter.Suite,
				Label:        selection.Filter.Label,
				NameContains: selection.Filter.NameContains,
				IncludeItemIDs: append(
					[]testdomain.ID(nil),
					selection.Filter.IncludeItemIDs...,
				),
				ExcludeItemIDs: append(
					[]testdomain.ID(nil),
					selection.Filter.ExcludeItemIDs...,
				),
			},
			RunID: selection.RunID,
		},
	})
	if err != nil {
		return nil, err
	}
	return encoded, nil
}

func canonicalCoordinatorTargetIDs(
	values []string,
) ([]string, error) {
	result := append([]string(nil), values...)
	sort.Strings(result)
	for index, value := range result {
		if value == "" ||
			index > 0 && value == result[index-1] {
			return nil, task.ErrInvalidArgument
		}
	}
	return result, nil
}

func coordinatorRunID(
	idempotencyKey string,
	requestJSON []byte,
) string {
	hash := sha256.New()
	_, _ = hash.Write([]byte("test-run-v1\x00"))
	_, _ = hash.Write([]byte(idempotencyKey))
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write(requestJSON)
	return hex.EncodeToString(hash.Sum(nil)[:16])
}

func cloneCoordinatorSteps(
	values []task.ExecutionStep,
) []task.ExecutionStep {
	result := make([]task.ExecutionStep, len(values))
	for index, value := range values {
		result[index] = value
		result[index].Process.Args = append(
			[]string(nil),
			value.Process.Args...,
		)
		result[index].Process.Env = append(
			[]string(nil),
			value.Process.Env...,
		)
		result[index].Process.EnvUnset = append(
			[]string(nil),
			value.Process.EnvUnset...,
		)
		result[index].Process.Batch = make(
			[]task.ProcessBatchItem,
			len(value.Process.Batch),
		)
		for batchIndex, item := range value.Process.Batch {
			result[index].Process.Batch[batchIndex] = item
			result[index].Process.Batch[batchIndex].Args =
				append([]string(nil), item.Args...)
			result[index].Process.Batch[batchIndex].Env =
				append([]string(nil), item.Env...)
			result[index].Process.Batch[batchIndex].EnvUnset =
				append([]string(nil), item.EnvUnset...)
		}
		result[index].Public.Args = append(
			[]string(nil),
			value.Public.Args...,
		)
		result[index].State = append(
			json.RawMessage(nil),
			value.State...,
		)
	}
	return result
}

func cloneExecutionDescriptor(
	value ctest.ExecutionDescriptor,
) ctest.ExecutionDescriptor {
	result := value
	result.Arguments = append([]string(nil), value.Arguments...)
	result.Environment = append(
		[]ctest.EnvironmentEntry(nil),
		value.Environment...,
	)
	result.EnvironmentChanges = append(
		[]ctest.EnvironmentModification(nil),
		value.EnvironmentChanges...,
	)
	result.Labels = append([]string(nil), value.Labels...)
	result.Compatibility.Reasons = append(
		[]ctest.Reason(nil),
		value.Compatibility.Reasons...,
	)
	return result
}

func nilCoordinatorPort(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface,
		reflect.Map, reflect.Pointer, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}
