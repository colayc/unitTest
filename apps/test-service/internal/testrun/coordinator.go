package testrun

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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
	RefreshAfterBuild(
		context.Context,
		RefreshRequest,
	) (RefreshedCatalog, error)
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
	execution := &runExecution{
		runID:          runID,
		prepared:       prepared,
		refresher:      coordinator.config.Refresher,
		runs:           coordinator.config.Runs,
		runner:         coordinator.config.Runner,
		selection:      selection.Clone(),
		repeatCount:    request.RepeatCount,
		taskTimeout:    request.Timeout,
		maxConcurrency: request.MaxConcurrency,
		initialSteps:   len(plan.Steps),
	}
	execution.lastBuildStep = plan.Steps[len(plan.Steps)-1].ID
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
	initialSteps   int

	taskID      string
	initialized bool
	interpreter *Interpreter
	steps       []task.ExecutionStep
	next        int
	lastIssued  string
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
		if step.ID != execution.lastBuildStep {
			return task.Continuation{}, nil
		}
		if err := execution.initialize(ctx, current.ID); err != nil {
			return task.Continuation{}, err
		}
		return execution.nextContinuation(), nil
	}
	if step.Kind != task.StepTestRun ||
		step.ID != execution.lastIssued {
		return task.Continuation{}, nil
	}
	return execution.nextContinuation(), nil
}

func (execution *runExecution) initialize(
	ctx context.Context,
	taskID string,
) error {
	refreshed, err := execution.refresher.RefreshAfterBuild(
		ctx,
		RefreshRequest{
			TaskID:              taskID,
			WorkspaceGeneration: execution.prepared.WorkspaceGeneration(),
			Project:             execution.prepared.Project(),
			Profile:             execution.prepared.Profile(),
			Toolchain:           execution.prepared.Toolchain(),
			Targets:             execution.prepared.Targets(),
		},
	)
	if err != nil {
		return err
	}
	refreshed = refreshed.Clone()
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
		maxCoordinatorRuntimeSteps-execution.initialSteps {
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
	execution.steps = make(
		[]task.ExecutionStep,
		len(planned.Invocations),
	)
	for index, invocation := range planned.Invocations {
		execution.steps[index] = invocation.Step
	}
	execution.interpreter = interpreter
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
	seen := make(map[string]struct{})
	for _, invocation := range planned.Invocations {
		state := invocation.ParseInput.Descriptor.Executable
		if state.Path == "" {
			continue
		}
		key := state.Path + "\x00" + state.SHA256
		if _, exists := seen[key]; exists {
			continue
		}
		if err := execution.prepared.AllowTestExecutable(
			state,
		); err != nil {
			return err
		}
		seen[key] = struct{}{}
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
		return task.StepVerdictDefault, nil
	}
	execution.mu.Lock()
	interpreter := execution.interpreter
	execution.mu.Unlock()
	if interpreter == nil {
		return task.StepVerdictDefault,
			task.ErrInvalidArgument
	}
	return interpreter.Interpret(
		ctx,
		current,
		step,
		result,
	)
}

func (execution *runExecution) ObserveOutput(
	ctx context.Context,
	current task.Task,
	step task.ExecutionStep,
	output task.ProcessOutput,
) error {
	if step.Kind != task.StepTestRun {
		return nil
	}
	execution.mu.Lock()
	interpreter := execution.interpreter
	execution.mu.Unlock()
	if interpreter == nil {
		return task.ErrInvalidArgument
	}
	return interpreter.ObserveOutput(
		ctx,
		current,
		step,
		output,
	)
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
