package coverageexec

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"time"

	"unit-test-ide.local/test-service/internal/build"
	"unit-test-ide.local/test-service/internal/cmake"
	"unit-test-ide.local/test-service/internal/coveragedomain"
	"unit-test-ide.local/test-service/internal/coveragellvm"
	coveragemodelv1 "unit-test-ide.local/test-service/internal/coveragemodel/v1"
	"unit-test-ide.local/test-service/internal/coveragenormalize"
	"unit-test-ide.local/test-service/internal/coverageparser/llvm"
	"unit-test-ide.local/test-service/internal/coveragereport"
	"unit-test-ide.local/test-service/internal/coveragerun"
	"unit-test-ide.local/test-service/internal/task"
	"unit-test-ide.local/test-service/internal/testdomain"
	"unit-test-ide.local/test-service/internal/testrun"
	"unit-test-ide.local/test-service/internal/toolchain"
	"unit-test-ide.local/test-service/internal/workspace"
)

type catalogReader interface {
	GetCatalog(context.Context, string, string) (testdomain.Catalog, error)
}

const defaultCoordinatorCloseTimeout = 5 * time.Second

type execution struct {
	config          Config
	coordinator     *Coordinator
	taskID          string
	initialTask     task.Task
	run             coveragedomain.Run
	testRun         testdomain.TestRun
	profile         workspace.CoverageProfile
	buildInput      build.StartRequest
	prepared        PreparedBuild
	adapter         PreparedAdapter
	root            *executionRootOwner
	boundary        *executionBoundary
	taskRoot        string
	profileRoot     string
	buildRoot       string
	toolchain       toolchain.Instance
	preparedTargets []cmake.Target
	instrument      coveragellvm.Instrumentation
	unsupported     bool
	terminalErr     error
	terminalOutcome task.Outcome

	mu                sync.Mutex
	embedded          testrun.EmbeddedRun
	testOriginals     map[string]task.ExecutionStep
	testOrder         []string
	outcomes          map[string]testrun.InvocationOutcome
	manifest          *coveragellvm.Manifest
	binaries          []*retainedFile
	targets           []processTarget
	state             coveragerun.State
	failedPhase       coveragerun.Phase
	exportOutput      bytes.Buffer
	document          coveragemodelv1.CoverageDocumentV1
	coverageJSON      []byte
	normalized        bool
	bindings          []coveragenormalize.SourceBinding
	reportSet         *coveragereport.Set
	finishedTestRun   *testdomain.TestRun
	events            []task.DomainEvent
	completionEvents  []task.DomainEvent
	coverageStarted   bool
	buildFinished     bool
	collectionStarted bool

	closeOnce sync.Once
	closeErr  error

	completionMu sync.Mutex
	completion   *completionReplay
}

func NewCoordinator(config Config) (*Coordinator, error) {
	if nilPort(config.Tasks) || nilPort(config.Store) ||
		nilPort(config.Build) || nilPort(config.Tests) ||
		nilPort(config.Adapter) || config.WorkspaceRoot.NativePath == "" ||
		config.WorkspaceRoot.ID == "" || config.ExecutionRoot == "" {
		return nil, task.ErrInvalidArgument
	}
	absolute, err := filepath.Abs(config.ExecutionRoot)
	if err != nil || filepath.Clean(absolute) != config.ExecutionRoot ||
		config.WorkspaceRoot.Contains(config.ExecutionRoot) {
		return nil, task.ErrInvalidArgument
	}
	info, err := os.Lstat(config.ExecutionRoot)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, task.ErrInvalidArgument
	}
	if config.Clock == nil {
		config.Clock = task.RealClock{}
	}
	if config.NewID == nil {
		config.NewID = task.NewID
	}
	if config.RenderReport == nil {
		config.RenderReport = coveragereport.Render
	}
	if config.CloseTimeout < 0 {
		return nil, task.ErrInvalidArgument
	}
	if config.CloseTimeout == 0 {
		config.CloseTimeout = defaultCoordinatorCloseTimeout
	}
	return &Coordinator{
		config:     config,
		executions: make(map[string]liveExecution),
		preparing:  make(map[string]chan struct{}),
	}, nil
}

func (coordinator *Coordinator) Resume(
	ctx context.Context,
	persisted task.Task,
) (task.Task, error) {
	if coordinator == nil || ctx == nil ||
		persisted.Kind != task.KindCoverageRun ||
		persisted.Status != task.StatusQueued ||
		!lowerHex(persisted.ID, 32) {
		return task.Task{}, task.ErrInvalidArgument
	}
	return coordinator.resume(ctx, persisted, false)
}

func (coordinator *Coordinator) FinishUnsupported(
	ctx context.Context,
	persisted task.Task,
) (task.Task, error) {
	if coordinator == nil || ctx == nil ||
		persisted.Kind != task.KindCoverageRun ||
		persisted.Status != task.StatusQueued ||
		!lowerHex(persisted.ID, 32) {
		return task.Task{}, task.ErrInvalidArgument
	}
	return coordinator.resume(ctx, persisted, true)
}

func (coordinator *Coordinator) resume(
	ctx context.Context,
	persisted task.Task,
	unsupported bool,
) (task.Task, error) {
	for {
		coordinator.mu.Lock()
		if coordinator.closed {
			coordinator.mu.Unlock()
			return task.Task{}, task.ErrStorageUnavailable
		}
		if _, exists := coordinator.executions[persisted.ID]; exists {
			coordinator.mu.Unlock()
			return coordinator.config.Store.Get(ctx, persisted.ID)
		}
		if wait := coordinator.preparing[persisted.ID]; wait != nil {
			coordinator.mu.Unlock()
			select {
			case <-wait:
				continue
			case <-ctx.Done():
				return task.Task{}, ctx.Err()
			}
		}
		wait := make(chan struct{})
		coordinator.preparing[persisted.ID] = wait
		coordinator.mu.Unlock()
		defer coordinator.finishPreparing(persisted.ID, wait)
		break
	}

	stored, err := coordinator.config.Store.Get(ctx, persisted.ID)
	if err != nil {
		return task.Task{}, err
	}
	if stored.Status == task.StatusFinished && sameTaskIdentity(stored, persisted) {
		return stored, nil
	}
	execution, plan, err := coordinator.prepare(ctx, persisted, unsupported)
	if err != nil {
		failure := preparationFailure{phase: coveragerun.PhaseConfigure, cause: err}
		if !errors.As(err, &failure) {
			failure.cause = err
		}
		return coordinator.resumePreparationFailure(ctx, persisted, failure)
	}
	if unsupported {
		plan = task.ExecutionPlan{Version: 1, Steps: []task.ExecutionStep{reportActionStep()}}
		plan.Fingerprint = task.FingerprintPlan(plan)
	}
	if err := coordinator.install(execution); err != nil {
		_ = execution.Close()
		return task.Task{}, err
	}
	resumed, err := coordinator.config.Tasks.ResumeQueued(ctx, task.ResumeRequest{
		Task: persisted, Plan: plan, Boundary: execution.boundary,
		Continuation: execution, ResultInterpreter: execution,
		ActionExecutor: execution,
	})
	if err != nil {
		coordinator.forget(persisted.ID, execution)
		_ = execution.Close()
		return resumed, err
	}
	return resumed, nil
}

type preparationFailure struct {
	phase coveragerun.Phase
	cause error
}

func (failure preparationFailure) Error() string { return failure.cause.Error() }
func (failure preparationFailure) Unwrap() error { return failure.cause }

func failPreparation(phase coveragerun.Phase, err error) error {
	if err == nil {
		err = task.ErrInvalidArgument
	}
	return preparationFailure{phase: phase, cause: err}
}

func (coordinator *Coordinator) resumePreparationFailure(
	ctx context.Context,
	persisted task.Task,
	failure preparationFailure,
) (task.Task, error) {
	stored, err := coordinator.config.Store.Get(ctx, persisted.ID)
	if err != nil || !sameQueuedTask(stored, persisted) {
		return task.Task{}, errOrInvalid(err)
	}
	runID := coverageRunID(stored.Request)
	run, err := coordinator.config.Store.GetCoverageRun(ctx, runID)
	if err != nil {
		return task.Task{}, err
	}
	testRun, err := coordinator.config.Store.GetRunForTask(ctx, stored.ID)
	if err != nil {
		return task.Task{}, err
	}
	execution := &execution{
		config: coordinator.config, coordinator: coordinator,
		taskID: stored.ID, initialTask: stored, run: run, testRun: testRun,
		state: coveragerun.NewState(), failedPhase: failure.phase,
		outcomes: make(map[string]testrun.InvocationOutcome), terminalErr: failure.cause,
		terminalOutcome: preparationTaskOutcome(failure.phase),
	}
	execution.boundary = &executionBoundary{execution: execution}
	if err := coordinator.install(execution); err != nil {
		_ = execution.Close()
		return task.Task{}, err
	}
	plan := task.ExecutionPlan{Version: 1, Steps: []task.ExecutionStep{reportActionStep()}}
	plan.Fingerprint = task.FingerprintPlan(plan)
	resumed, err := coordinator.config.Tasks.ResumeQueued(ctx, task.ResumeRequest{
		Task: persisted, Plan: plan, Boundary: execution.boundary,
		Continuation: execution, ResultInterpreter: execution, ActionExecutor: execution,
	})
	if err != nil {
		coordinator.forget(persisted.ID, execution)
		_ = execution.Close()
	}
	return resumed, err
}

func preparationTaskOutcome(phase coveragerun.Phase) task.Outcome {
	if phase == coveragerun.PhaseBuild {
		return task.OutcomeCommandFailed
	}
	return task.OutcomeInfrastructureFailed
}

func (coordinator *Coordinator) Close() error {
	if coordinator == nil {
		return nil
	}
	coordinator.mu.Lock()
	if coordinator.closeDone != nil {
		done := coordinator.closeDone
		coordinator.mu.Unlock()
		<-done
		coordinator.mu.Lock()
		err := coordinator.closeErr
		coordinator.mu.Unlock()
		return err
	}
	coordinator.closed = true
	coordinator.closeDone = make(chan struct{})
	done := coordinator.closeDone
	coordinator.mu.Unlock()
	result := coordinator.closeActiveExecutions()
	coordinator.mu.Lock()
	coordinator.closeErr = result
	close(done)
	coordinator.mu.Unlock()
	return result
}

func (coordinator *Coordinator) closeActiveExecutions() error {
	timeout := coordinator.config.CloseTimeout
	if timeout <= 0 {
		timeout = defaultCoordinatorCloseTimeout
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	for {
		coordinator.mu.Lock()
		waits := make([]chan struct{}, 0, len(coordinator.preparing))
		for _, wait := range coordinator.preparing {
			waits = append(waits, wait)
		}
		coordinator.mu.Unlock()
		if len(waits) == 0 {
			break
		}
		for _, wait := range waits {
			select {
			case <-wait:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}

	coordinator.mu.Lock()
	executions := make(map[string]liveExecution, len(coordinator.executions))
	for id, execution := range coordinator.executions {
		executions[id] = execution
	}
	coordinator.mu.Unlock()
	var result error
	for id, execution := range executions {
		finished, err := coordinator.config.Tasks.Cancel(ctx, id)
		if err != nil {
			result = errors.Join(result, err)
		}
		if finished.Status != task.StatusFinished {
			finished, err = coordinator.waitTaskFinished(ctx, id)
			if err != nil {
				result = errors.Join(result, err)
				continue
			}
		}
		result = errors.Join(result, execution.Close())
	}
	return result
}

func (coordinator *Coordinator) waitTaskFinished(ctx context.Context, id string) (task.Task, error) {
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		stored, err := coordinator.config.Store.Get(ctx, id)
		if err != nil {
			return task.Task{}, err
		}
		if stored.Status == task.StatusFinished {
			return stored, nil
		}
		select {
		case <-ticker.C:
		case <-ctx.Done():
			return task.Task{}, ctx.Err()
		}
	}
}

func (coordinator *Coordinator) install(execution *execution) error {
	coordinator.mu.Lock()
	defer coordinator.mu.Unlock()
	if coordinator.closed || coordinator.executions[execution.taskID] != nil {
		return task.ErrConflict
	}
	coordinator.executions[execution.taskID] = execution
	return nil
}

func (coordinator *Coordinator) finishPreparing(taskID string, wait chan struct{}) {
	coordinator.mu.Lock()
	if coordinator.preparing[taskID] == wait {
		delete(coordinator.preparing, taskID)
		close(wait)
	}
	coordinator.mu.Unlock()
}

func (coordinator *Coordinator) forget(taskID string, execution liveExecution) {
	coordinator.mu.Lock()
	if coordinator.executions[taskID] == execution {
		delete(coordinator.executions, taskID)
	}
	coordinator.mu.Unlock()
}

func (coordinator *Coordinator) prepare(
	ctx context.Context,
	persisted task.Task,
	unsupported bool,
) (*execution, task.ExecutionPlan, error) {
	stored, run, testRun, profile, err := coordinator.loadGraph(ctx, persisted)
	if err != nil {
		return nil, task.ExecutionPlan{}, failPreparation(coveragerun.PhaseConfigure, err)
	}
	execution := &execution{
		config: coordinator.config, coordinator: coordinator,
		taskID: stored.ID, initialTask: stored, run: run,
		testRun: testRun, profile: profile,
		state: coveragerun.NewState(), failedPhase: coveragerun.PhaseConfigure,
		outcomes:    make(map[string]testrun.InvocationOutcome),
		unsupported: unsupported,
	}
	execution.buildInput = build.StartRequest{
		IdempotencyKey:      stored.IdempotencyKey,
		WorkspaceGeneration: stored.WorkspaceGeneration,
		ProjectID:           run.Request.ProjectID,
		BuildProfileID:      testRun.ProfileID,
		Jobs:                1, Timeout: stored.Timeout,
	}
	if unsupported {
		execution.boundary = &executionBoundary{execution: execution}
		return execution, task.ExecutionPlan{}, nil
	}
	probe, err := coordinator.config.Build.PreparePlan(ctx, execution.buildInput)
	if err != nil {
		return nil, task.ExecutionPlan{}, failPreparation(coveragerun.PhaseBuild, err)
	}
	if probe == nil {
		return nil, task.ExecutionPlan{}, failPreparation(coveragerun.PhaseBuild, task.ErrStorageUnavailable)
	}
	currentToolchain := probe.Toolchain()
	identityErr := validatePreparedIdentity(probe, run, testRun, profile, currentToolchain)
	probe.ReleaseIfUnadopted()
	if identityErr != nil {
		return nil, task.ExecutionPlan{}, failPreparation(coveragerun.PhaseConfigure, identityErr)
	}
	root, taskRoot, profileRoot, buildRoot, err := allocateExecutionRoots(
		coordinator.config.ExecutionRoot,
		stored.ID,
	)
	if err != nil {
		return nil, task.ExecutionPlan{}, failPreparation(coveragerun.PhaseConfigure, err)
	}
	execution.root, execution.taskRoot = root, taskRoot
	execution.profileRoot, execution.buildRoot = profileRoot, buildRoot
	cleanup := true
	defer func() {
		if cleanup {
			_ = execution.closeRuntime()
		}
	}()
	preparedAdapter, err := coordinator.config.Adapter.Prepare(ctx, AdapterInput{
		Toolchain:   currentToolchain,
		TaskRoot:    taskRoot,
		ProfileRoot: profileRoot,
	})
	if err != nil || nilPort(preparedAdapter) {
		return nil, task.ExecutionPlan{}, failPreparation(coveragerun.PhaseConfigure, errOrInvalid(err))
	}
	execution.adapter = preparedAdapter
	execution.toolchain = cloneToolchain(currentToolchain)
	execution.instrument = preparedAdapter.Instrumentation()
	if err := execution.validateAdapterIdentity(); err != nil {
		return nil, task.ExecutionPlan{}, failPreparation(coveragerun.PhaseConfigure, err)
	}
	coverageInput := execution.buildInput
	coverageInput.Coverage = &build.CoverageOptions{
		BinaryDir: buildRoot,
		TopLevelInclude: cmake.FingerprintFile{
			Path:     execution.instrument.IncludePath,
			Identity: execution.instrument.Fingerprint,
			SHA256:   execution.instrument.SHA256,
		},
		InstrumentationFingerprint: execution.instrument.Fingerprint,
		ToolsetIdentity:            preparedAdapter.Toolset().Identity(),
	}
	prepared, err := coordinator.config.Build.PreparePlan(ctx, coverageInput)
	if err != nil || prepared == nil {
		return nil, task.ExecutionPlan{}, failPreparation(coveragerun.PhaseBuild, errOrInvalid(err))
	}
	execution.prepared = prepared
	if err := validatePreparedIdentity(prepared, run, testRun, profile, currentToolchain); err != nil {
		return nil, task.ExecutionPlan{}, failPreparation(coveragerun.PhaseBuild, err)
	}
	if !samePath(prepared.CoverageBinaryDir(), buildRoot) ||
		prepared.AttachCoverageToolset(preparedAdapter.Toolset()) != nil {
		return nil, task.ExecutionPlan{}, failPreparation(coveragerun.PhaseBuild, task.ErrInvalidArgument)
	}
	plan, err := rewriteBuildPlan(prepared.Plan())
	if err != nil {
		return nil, task.ExecutionPlan{}, failPreparation(coveragerun.PhaseBuild, err)
	}
	execution.boundary = &executionBoundary{
		delegate: prepared.Boundary(), execution: execution, root: root,
	}
	execution.addApprovedSteps(plan.Steps)
	cleanup = false
	return execution, plan, nil
}

func (coordinator *Coordinator) loadGraph(
	ctx context.Context,
	supplied task.Task,
) (task.Task, coveragedomain.Run, testdomain.TestRun, workspace.CoverageProfile, error) {
	stored, err := coordinator.config.Store.Get(ctx, supplied.ID)
	if err != nil {
		return task.Task{}, coveragedomain.Run{}, testdomain.TestRun{}, workspace.CoverageProfile{}, err
	}
	if !sameQueuedTask(stored, supplied) {
		return task.Task{}, coveragedomain.Run{}, testdomain.TestRun{}, workspace.CoverageProfile{}, task.ErrConflict
	}
	runID := coverageRunID(stored.Request)
	if runID == "" {
		return task.Task{}, coveragedomain.Run{}, testdomain.TestRun{}, workspace.CoverageProfile{}, task.ErrInvalidArgument
	}
	run, err := coordinator.config.Store.GetCoverageRun(ctx, runID)
	if err != nil {
		return task.Task{}, coveragedomain.Run{}, testdomain.TestRun{}, workspace.CoverageProfile{}, err
	}
	testRun, err := coordinator.config.Store.GetRunForTask(ctx, stored.ID)
	if err != nil {
		return task.Task{}, coveragedomain.Run{}, testdomain.TestRun{}, workspace.CoverageProfile{}, err
	}
	profile, err := loadCoverageProfile(coordinator.config.WorkspaceRoot, run.Request.CoverageProfileID)
	if err != nil || validateQueuedGraph(stored, run, testRun, profile) != nil {
		return task.Task{}, coveragedomain.Run{}, testdomain.TestRun{}, workspace.CoverageProfile{}, task.ErrConflict
	}
	if _, err := coordinator.catalog(ctx, testRun); err != nil {
		return task.Task{}, coveragedomain.Run{}, testdomain.TestRun{}, workspace.CoverageProfile{}, err
	}
	return stored, run, testRun, profile, nil
}

func (coordinator *Coordinator) catalog(ctx context.Context, run testdomain.TestRun) (testdomain.Catalog, error) {
	reader, ok := coordinator.config.Store.(catalogReader)
	if !ok || nilPort(reader) {
		return testdomain.Catalog{}, task.ErrStorageUnavailable
	}
	catalog, err := reader.GetCatalog(ctx, run.ProjectID, run.ProfileID)
	if err != nil {
		return testdomain.Catalog{}, err
	}
	validated, err := testdomain.NewCatalog(catalog)
	if err != nil || validated.Revision != run.CatalogRevision {
		return testdomain.Catalog{}, testdomain.ErrCatalogStale
	}
	return validated, nil
}

func (execution *execution) revalidate(ctx context.Context) error {
	if execution == nil || ctx == nil {
		return task.ErrInvalidArgument
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	stored, err := execution.config.Store.Get(ctx, execution.taskID)
	if err != nil || stored.Kind != task.KindCoverageRun ||
		stored.IdempotencyKey != execution.initialTask.IdempotencyKey ||
		stored.RequestHash != execution.initialTask.RequestHash ||
		stored.WorkspaceGeneration != execution.initialTask.WorkspaceGeneration ||
		!bytes.Equal(stored.Request, execution.initialTask.Request) ||
		(stored.Status != task.StatusQueued && stored.Status != task.StatusRunning) {
		return task.ErrConflict
	}
	run, err := execution.config.Store.GetCoverageRun(ctx, execution.run.ID)
	if err != nil || !reflect.DeepEqual(run, execution.run) {
		return task.ErrConflict
	}
	testRun, err := execution.config.Store.GetRunForTask(ctx, execution.taskID)
	if err != nil || !sameTestRunIdentity(testRun, execution.testRun) ||
		(testRun.Status != testdomain.RunQueued && testRun.Status != testdomain.RunRunning) {
		return task.ErrConflict
	}
	profile, err := loadCoverageProfile(execution.config.WorkspaceRoot, execution.run.Request.CoverageProfileID)
	if err != nil || !reflect.DeepEqual(profile, execution.profile) {
		return task.ErrConflict
	}
	if _, err := execution.coordinator.catalog(ctx, execution.testRun); err != nil {
		return err
	}
	probe, err := execution.config.Build.PreparePlan(ctx, execution.buildInput)
	if err != nil || probe == nil {
		return errOrInvalid(err)
	}
	defer probe.ReleaseIfUnadopted()
	if err := validatePreparedIdentity(
		probe,
		execution.run,
		execution.testRun,
		execution.profile,
		execution.toolchain,
	); err != nil {
		return err
	}
	execution.mu.Lock()
	execution.preparedTargets = cloneCoverageTargets(probe.Targets())
	execution.mu.Unlock()
	if err := execution.verifyRetained(); err != nil {
		return err
	}
	return nil
}

func (execution *execution) verifyRetained() error {
	if execution == nil {
		return task.ErrInvalidArgument
	}
	execution.mu.Lock()
	defer execution.mu.Unlock()
	if execution.unsupported {
		return nil
	}
	if execution.root == nil || execution.root.Verify() != nil ||
		execution.adapter == nil || execution.validateAdapterIdentityLocked() != nil {
		return task.ErrInvalidArgument
	}
	if execution.manifest != nil && execution.manifest.Verify() != nil {
		return task.ErrInvalidArgument
	}
	for _, binary := range execution.binaries {
		if binary.Verify() != nil {
			return task.ErrInvalidArgument
		}
	}
	return nil
}

func (execution *execution) validateAdapterIdentity() error {
	execution.mu.Lock()
	defer execution.mu.Unlock()
	return execution.validateAdapterIdentityLocked()
}

func (execution *execution) validateAdapterIdentityLocked() error {
	if execution.adapter == nil || execution.adapter.Toolset() == nil ||
		execution.adapter.Allocator() == nil ||
		execution.adapter.Toolset().Verify() != nil ||
		execution.adapter.Toolset().Identity() != execution.toolchain.Coverage.ToolsetIdentity ||
		execution.adapter.Toolset().Version() != execution.toolchain.Version ||
		execution.instrument != execution.adapter.Instrumentation() ||
		validateInstrumentationContract(execution.run.Toolchain, execution.instrument) != nil ||
		!pathWithin(execution.taskRoot, execution.instrument.IncludePath) {
		return task.ErrInvalidArgument
	}
	return nil
}

func validateInstrumentationContract(
	snapshot coveragedomain.ToolchainSnapshot,
	instrumentation coveragellvm.Instrumentation,
) error {
	if snapshot.InstrumentationFingerprint == "" ||
		instrumentation.Fingerprint == "" ||
		snapshot.InstrumentationFingerprint != instrumentation.Fingerprint {
		return task.ErrInvalidArgument
	}
	return nil
}

func (execution *execution) AfterStep(
	ctx context.Context,
	current task.Task,
	step task.ExecutionStep,
	result task.StepResult,
) (task.Continuation, error) {
	if execution == nil || ctx == nil || current.ID != execution.taskID ||
		result.Verdict != task.StepVerdictSucceeded {
		return task.Continuation{}, task.ErrInvalidArgument
	}
	if err := execution.revalidate(ctx); err != nil {
		return task.Continuation{}, err
	}
	switch step.Kind {
	case task.StepCoverageConfigure:
		if err := execution.applyPhase(coveragerun.StepResult{Phase: coveragerun.PhaseConfigure, Succeeded: true}); err != nil {
			return task.Continuation{}, err
		}
		return task.Continuation{}, nil
	case task.StepCoverageBuild:
		if err := execution.applyPhase(coveragerun.StepResult{Phase: coveragerun.PhaseBuild, Succeeded: true}); err != nil {
			return task.Continuation{}, err
		}
		steps, err := execution.prepareTests(ctx, current)
		if err != nil {
			return task.Continuation{}, err
		}
		return task.Continuation{Steps: steps}, nil
	case task.StepCoverageTest:
		if !execution.lastTestStep(step.ID) {
			return task.Continuation{}, nil
		}
		steps, err := execution.prepareCollector(ctx)
		if err != nil {
			return task.Continuation{}, err
		}
		return task.Continuation{Steps: steps}, nil
	case task.StepCoverageMerge:
		if err := execution.applyPhase(coveragerun.StepResult{Phase: coveragerun.PhaseMerge, Succeeded: true}); err != nil {
			return task.Continuation{}, err
		}
		return task.Continuation{}, nil
	case task.StepCoverageNormalize:
		if err := execution.applyPhase(coveragerun.StepResult{Phase: coveragerun.PhaseNormalize, Succeeded: true}); err != nil {
			return task.Continuation{}, err
		}
		return task.Continuation{Steps: []task.ExecutionStep{reportActionStep()}}, nil
	case task.StepCoverageReport:
		if err := execution.applyPhase(coveragerun.StepResult{Phase: coveragerun.PhaseReport, Succeeded: true}); err != nil {
			return task.Continuation{}, err
		}
		return task.Continuation{Steps: []task.ExecutionStep{publishActionStep()}}, nil
	case task.StepCoveragePublish:
		if err := execution.applyPhase(coveragerun.StepResult{Phase: coveragerun.PhasePublish, Succeeded: true}); err != nil {
			return task.Continuation{}, err
		}
		return task.Continuation{}, nil
	default:
		return task.Continuation{}, task.ErrInvalidArgument
	}
}

func (execution *execution) Interpret(
	ctx context.Context,
	current task.Task,
	step task.ExecutionStep,
	result task.ProcessResult,
) (task.StepVerdict, error) {
	if execution == nil || ctx == nil || current.ID != execution.taskID ||
		result.Err != nil {
		return task.StepVerdictDefault, task.ErrInvalidArgument
	}
	if err := ctx.Err(); err != nil {
		return task.StepVerdictDefault, err
	}
	execution.ensureCoverageStarted()
	switch step.Kind {
	case task.StepCoverageConfigure:
		if result.ExitCode != 0 || result.TimedOut {
			execution.setFailedPhase(coveragerun.PhaseConfigure)
			return task.StepVerdictDefault, errors.New("coverage instrumentation configure failed")
		}
		return task.StepVerdictSucceeded, nil
	case task.StepCoverageBuild:
		if result.TimedOut {
			execution.setFailedPhase(coveragerun.PhaseBuild)
			return task.StepVerdictDefault, errors.New("coverage build timed out")
		}
		if result.ExitCode != 0 {
			execution.setFailedPhase(coveragerun.PhaseBuild)
			return task.StepVerdictDefault, nil
		}
		return task.StepVerdictSucceeded, nil
	case task.StepCoverageTest:
		execution.mu.Lock()
		embedded := execution.embedded
		original := execution.testOriginals[step.ID]
		execution.mu.Unlock()
		if embedded == nil || original.ID == "" {
			return task.StepVerdictDefault, task.ErrInvalidArgument
		}
		verdict, err := embedded.Interpret(ctx, current, original, result)
		if err != nil || verdict != task.StepVerdictSucceeded {
			execution.setFailedPhase(coveragerun.PhaseTest)
			return verdict, err
		}
		execution.recordOutcomes(result)
		return verdict, nil
	case task.StepCoverageMerge:
		if result.ExitCode != 0 || result.TimedOut {
			execution.setFailedPhase(coveragerun.PhaseMerge)
			return task.StepVerdictDefault, errors.New("coverage merge failed")
		}
		return task.StepVerdictSucceeded, nil
	case task.StepCoverageNormalize:
		if result.ExitCode != 0 || result.TimedOut {
			execution.setFailedPhase(coveragerun.PhaseNormalize)
			return task.StepVerdictDefault, errors.New("coverage export failed")
		}
		if err := execution.normalize(); err != nil {
			execution.setFailedPhase(coveragerun.PhaseNormalize)
			return task.StepVerdictDefault, err
		}
		return task.StepVerdictSucceeded, nil
	default:
		return task.StepVerdictDefault, task.ErrInvalidArgument
	}
}

func (execution *execution) ObserveOutput(
	ctx context.Context,
	current task.Task,
	step task.ExecutionStep,
	output task.ProcessOutput,
) error {
	if execution == nil || ctx == nil || current.ID != execution.taskID {
		return task.ErrInvalidArgument
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	execution.ensureCoverageStarted()
	if step.Kind == task.StepCoverageTest {
		execution.mu.Lock()
		embedded := execution.embedded
		original := execution.testOriginals[step.ID]
		execution.mu.Unlock()
		if embedded == nil || original.ID == "" {
			return task.ErrInvalidArgument
		}
		return embedded.ObserveOutput(ctx, current, original, output)
	}
	if step.Kind != task.StepCoverageNormalize || output.Stream != "stdout" {
		return nil
	}
	limits := coveragenormalize.DefaultLimits()
	execution.mu.Lock()
	defer execution.mu.Unlock()
	if int64(execution.exportOutput.Len()) > limits.MaxInputBytes-int64(len(output.Data)) {
		return coveragenormalize.ErrLimitExceeded
	}
	_, _ = execution.exportOutput.Write(output.Data)
	return nil
}

func (execution *execution) DrainDomainEvents() []task.DomainEvent {
	if execution == nil {
		return nil
	}
	execution.mu.Lock()
	events := cloneEvents(execution.events)
	execution.events = nil
	embedded := execution.embedded
	execution.mu.Unlock()
	if embedded != nil {
		for _, event := range embedded.DrainDomainEvents() {
			if event.Type == task.EventTestRunFinished {
				execution.mu.Lock()
				execution.completionEvents = append(execution.completionEvents, event)
				execution.mu.Unlock()
				continue
			}
			events = append(events, event)
		}
	}
	return events
}

func (execution *execution) ExecuteServiceAction(
	ctx context.Context,
	current task.Task,
	step task.ExecutionStep,
) (task.StepResult, error) {
	if execution == nil || ctx == nil || current.ID != execution.taskID {
		return task.StepResult{}, task.ErrInvalidArgument
	}
	if execution.unsupported {
		execution.setFailedPhase(coveragerun.PhaseConfigure)
		return task.StepResult{}, errors.New("coverage adapter is unavailable")
	}
	if execution.terminalErr != nil {
		if execution.terminalOutcome == task.OutcomeCommandFailed {
			return task.StepResult{Verdict: task.StepVerdictFailed}, nil
		}
		return task.StepResult{}, execution.terminalErr
	}
	if err := execution.revalidate(ctx); err != nil {
		execution.setFailedPhase(phaseForStep(step.Kind))
		return task.StepResult{}, err
	}
	switch step.Action {
	case task.ServiceActionCoverageReport:
		execution.setFailedPhase(coveragerun.PhaseReport)
		run, err := execution.finishEmbedded(ctx, execution.config.Clock.Now(), task.OutcomeSucceeded)
		if err != nil {
			return task.StepResult{}, err
		}
		execution.mu.Lock()
		document := execution.document
		coverageJSON := append([]byte(nil), execution.coverageJSON...)
		bindings := append([]coveragenormalize.SourceBinding(nil), execution.bindings...)
		normalized := execution.normalized
		execution.mu.Unlock()
		if !normalized {
			return task.StepResult{}, task.ErrInvalidArgument
		}
		set, err := execution.config.RenderReport(coveragereport.Input{
			CoverageJSON: coverageJSON,
			Document:     document,
			TestRun:      run,
			Sources:      bindings,
		})
		if err != nil {
			return task.StepResult{}, err
		}
		execution.mu.Lock()
		execution.reportSet = &set
		execution.mu.Unlock()
		return task.StepResult{Verdict: task.StepVerdictSucceeded}, nil
	case task.ServiceActionCoveragePublish:
		execution.setFailedPhase(coveragerun.PhasePublish)
		execution.mu.Lock()
		if execution.reportSet == nil {
			execution.mu.Unlock()
			return task.StepResult{}, task.ErrInvalidArgument
		}
		set := *execution.reportSet
		execution.mu.Unlock()
		if err := coveragereport.Validate(set); err != nil {
			return task.StepResult{}, err
		}
		return task.StepResult{Verdict: task.StepVerdictSucceeded}, nil
	default:
		return task.StepResult{}, task.ErrInvalidArgument
	}
}

func (execution *execution) prepareTests(ctx context.Context, current task.Task) ([]task.ExecutionStep, error) {
	run, err := execution.config.Store.GetRunForTask(ctx, execution.taskID)
	if err != nil {
		return nil, err
	}
	catalog, err := execution.coordinator.catalog(ctx, run)
	if err != nil {
		return nil, err
	}
	execution.mu.Lock()
	prepared := execution.prepared
	targets := cloneCoverageTargets(execution.preparedTargets)
	execution.mu.Unlock()
	if prepared == nil {
		execution.setFailedPhase(coveragerun.PhaseTest)
		return nil, task.ErrInvalidArgument
	}
	if len(targets) != 0 {
		prepared = &preparedBuildWithTargets{PreparedBuild: prepared, targets: targets}
	}
	embedded, err := execution.config.Tests.PrepareEmbedded(ctx, testrun.EmbeddedRequest{
		TaskID:         execution.taskID,
		Run:            run,
		PreparedBuild:  prepared,
		Catalog:        catalog,
		Allocator:      execution.adapter.Allocator(),
		MaxConcurrency: 1,
	})
	if err != nil || nilPort(embedded) {
		execution.setFailedPhase(coveragerun.PhaseTest)
		return nil, errOrInvalid(err)
	}
	steps, originals, err := rewriteTestSteps(embedded.Steps())
	if err != nil {
		return nil, err
	}
	binaries, err := retainTestBinaries(originals)
	if err != nil {
		return nil, err
	}
	execution.mu.Lock()
	execution.embedded = embedded
	execution.testOriginals = originals
	execution.testOrder = make([]string, len(steps))
	for index := range steps {
		execution.testOrder[index] = steps[index].ID
	}
	execution.binaries = binaries
	execution.addApprovedStepsLocked(steps)
	if !execution.buildFinished {
		execution.buildFinished = true
		execution.appendEventLocked(task.EventCoverageBuildFinished, map[string]any{
			"coverageRunId": execution.run.ID,
		})
	}
	execution.mu.Unlock()
	_ = current
	return steps, nil
}

type preparedBuildWithTargets struct {
	PreparedBuild
	targets []cmake.Target
}

func (prepared *preparedBuildWithTargets) Targets() []cmake.Target {
	if prepared == nil {
		return nil
	}
	return cloneCoverageTargets(prepared.targets)
}

func cloneCoverageTargets(values []cmake.Target) []cmake.Target {
	if values == nil {
		return nil
	}
	result := make([]cmake.Target, len(values))
	for index, value := range values {
		result[index] = value
		result[index].Artifacts = append([]string(nil), value.Artifacts...)
	}
	return result
}

func (execution *execution) prepareCollector(ctx context.Context) ([]task.ExecutionStep, error) {
	execution.mu.Lock()
	embedded := execution.embedded
	outcomes := make([]testrun.InvocationOutcome, 0, len(execution.outcomes))
	for _, outcome := range execution.outcomes {
		outcomes = append(outcomes, outcome)
	}
	execution.mu.Unlock()
	if embedded == nil {
		return nil, task.ErrInvalidArgument
	}
	expectations := embedded.Expectations()
	sort.Slice(outcomes, func(left, right int) bool {
		if outcomes[left].InvocationID != outcomes[right].InvocationID {
			return outcomes[left].InvocationID < outcomes[right].InvocationID
		}
		return outcomes[left].Iteration < outcomes[right].Iteration
	})
	if len(outcomes) != len(expectations) {
		execution.setFailedPhase(coveragerun.PhaseTest)
		return nil, task.ErrInvalidArgument
	}
	manifest, err := execution.adapter.SealProfiles(expectations, outcomes)
	if err != nil {
		execution.setFailedPhase(coveragerun.PhaseTest)
		return nil, err
	}
	execution.mu.Lock()
	execution.manifest = &manifest
	binaries := make([]coveragerun.TrustedPath, len(execution.binaries))
	for index, binary := range execution.binaries {
		binaries[index] = binary
	}
	execution.mu.Unlock()
	merge, export, err := execution.adapter.Collector(manifest, binaries)
	if err != nil {
		execution.setFailedPhase(coveragerun.PhaseMerge)
		return nil, err
	}
	run, err := execution.config.Store.GetRunForTask(ctx, execution.taskID)
	if err != nil {
		return nil, err
	}
	assertionFailure := false
	for _, result := range run.Results {
		assertionFailure = assertionFailure || result.Outcome == testdomain.ItemFailed
	}
	reasons := make(map[coveragedomain.CompletenessReason]struct{}, len(manifest.PartialReasons))
	for _, reason := range manifest.PartialReasons {
		reasons[reason] = struct{}{}
	}
	if err := execution.applyPhase(coveragerun.StepResult{
		Phase:            coveragerun.PhaseTest,
		Succeeded:        !assertionFailure,
		AssertionFailure: assertionFailure,
		Crash:            hasReason(reasons, coveragedomain.CompletenessReasonTestCrashed),
		TimedOut:         hasReason(reasons, coveragedomain.CompletenessReasonTestTimedOut),
		ProfileMissing:   hasReason(reasons, coveragedomain.CompletenessReasonProfileMissingForFailedInvocation),
	}); err != nil {
		return nil, err
	}
	execution.mu.Lock()
	if !execution.collectionStarted {
		execution.collectionStarted = true
		execution.appendEventLocked(task.EventCoverageCollectionStarted, map[string]any{
			"coverageRunId": execution.run.ID,
			"testRunId":     execution.run.TestRunID,
		})
	}
	execution.mu.Unlock()
	steps, err := collectorSteps(merge, export)
	if err != nil {
		return nil, err
	}
	execution.addApprovedSteps(steps)
	return steps, nil
}

func (execution *execution) normalize() error {
	execution.mu.Lock()
	raw := append([]byte(nil), execution.exportOutput.Bytes()...)
	state := execution.state
	profile := execution.profile
	execution.mu.Unlock()
	limits := coveragenormalize.DefaultLimits()
	parsed, err := llvm.Parse(bytes.NewReader(raw), llvm.Limits{
		MaxInputBytes:  limits.MaxInputBytes,
		MaxDepth:       limits.MaxDepth,
		MaxFiles:       limits.MaxFiles,
		MaxFunctions:   limits.MaxFunctions,
		MaxLines:       limits.MaxLines,
		MaxBranches:    limits.MaxBranches,
		MaxStringBytes: limits.MaxStringBytes,
	})
	if err != nil {
		return err
	}
	matcher, err := coveragenormalize.NewGlobMatcher(profile.Include, profile.Exclude)
	if err != nil {
		return err
	}
	completeness := coveragedomain.Completeness{Outcome: coveragedomain.OutcomeAvailable}
	if len(state.PartialReasons) != 0 {
		completeness.Outcome = coveragedomain.OutcomePartial
		completeness.Reasons = append([]coveragedomain.CompletenessReason(nil), state.PartialReasons...)
	}
	document, bindings, err := coveragenormalize.NormalizeLLVM(coveragenormalize.LLVMInput{
		Export:        parsed,
		WorkspaceRoot: execution.config.WorkspaceRoot.NativePath,
		Matcher:       matcher,
		Toolchain:     execution.run.Toolchain,
		Completeness:  completeness,
		Limits:        limits,
	})
	if err != nil {
		return err
	}
	coverageJSON, err := coveragenormalize.EncodeCanonical(document)
	if err != nil {
		return err
	}
	execution.mu.Lock()
	execution.document = document
	execution.coverageJSON = append([]byte(nil), coverageJSON...)
	execution.normalized = true
	execution.bindings = append([]coveragenormalize.SourceBinding(nil), bindings...)
	execution.mu.Unlock()
	return nil
}

func (execution *execution) applyPhase(result coveragerun.StepResult) error {
	execution.mu.Lock()
	defer execution.mu.Unlock()
	if execution.state.Phase != result.Phase {
		return coveragerun.ErrInvalidTransition
	}
	next, err := execution.state.Apply(result)
	if err != nil {
		return err
	}
	execution.state = next
	execution.failedPhase = next.Phase
	return nil
}

func (execution *execution) setFailedPhase(phase coveragerun.Phase) {
	execution.mu.Lock()
	execution.failedPhase = phase
	execution.mu.Unlock()
}

func (execution *execution) ensureCoverageStarted() {
	execution.mu.Lock()
	defer execution.mu.Unlock()
	if execution.coverageStarted {
		return
	}
	execution.coverageStarted = true
	execution.appendEventLocked(task.EventCoverageRunStarted, map[string]any{
		"coverageRunId":   execution.run.ID,
		"testRunId":       execution.run.TestRunID,
		"catalogRevision": execution.run.Request.CatalogRevision,
		"repeatCount":     execution.run.Request.RepeatCount,
	})
}

func (execution *execution) appendEventLocked(kind task.EventType, value any) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return
	}
	execution.events = append(execution.events, task.DomainEvent{Type: kind, Payload: encoded})
}

func (execution *execution) lastTestStep(id string) bool {
	execution.mu.Lock()
	defer execution.mu.Unlock()
	return len(execution.testOrder) != 0 && execution.testOrder[len(execution.testOrder)-1] == id
}

func (execution *execution) recordOutcomes(result task.ProcessResult) {
	execution.mu.Lock()
	embedded := execution.embedded
	execution.mu.Unlock()
	if embedded == nil {
		return
	}
	expectations := embedded.Expectations()
	execution.mu.Lock()
	defer execution.mu.Unlock()
	for _, child := range result.Children {
		for _, expectation := range expectations {
			if expectation.InvocationID != child.ID {
				continue
			}
			outcome := invocationOutcomeFromChild(child)
			outcome.InvocationID = expectation.InvocationID
			outcome.Iteration = expectation.Iteration
			execution.outcomes[outcomeKey(expectation.InvocationID, expectation.Iteration)] = outcome
		}
	}
}

func invocationOutcomeFromChild(child task.ProcessChildResult) testrun.InvocationOutcome {
	return testrun.InvocationOutcome{
		InvocationID: child.ID,
		ExitCode:     child.ExitCode,
		Crashed:      !child.TimedOut && processExitWasCrash(child.ExitCode),
		TimedOut:     child.TimedOut,
	}
}

func (execution *execution) addApprovedSteps(steps []task.ExecutionStep) {
	execution.mu.Lock()
	defer execution.mu.Unlock()
	execution.addApprovedStepsLocked(steps)
}

func (execution *execution) addApprovedStepsLocked(steps []task.ExecutionStep) {
	for _, step := range steps {
		if step.Action != "" {
			continue
		}
		if len(step.Process.Batch) == 0 {
			execution.targets = append(execution.targets, processTarget{
				executable:  step.Process.Executable,
				arguments:   append([]string(nil), step.Process.Args...),
				environment: append([]string(nil), step.Process.Env...),
				unset:       append([]string(nil), step.Process.EnvUnset...),
				directory:   step.Process.Dir,
			})
			continue
		}
		for _, item := range step.Process.Batch {
			execution.targets = append(execution.targets, processTarget{
				executable:  item.Executable,
				arguments:   append([]string(nil), item.Args...),
				environment: append([]string(nil), item.Env...),
				unset:       append([]string(nil), item.EnvUnset...),
				directory:   item.Dir,
			})
		}
	}
}

func (execution *execution) Close() error {
	if execution == nil {
		return nil
	}
	if execution.boundary != nil {
		return execution.boundary.Release()
	}
	return execution.closeRuntime()
}

func (execution *execution) closeRuntime() error {
	if execution == nil {
		return nil
	}
	execution.closeOnce.Do(func() {
		execution.mu.Lock()
		prepared := execution.prepared
		manifest := execution.manifest
		binaries := append([]*retainedFile(nil), execution.binaries...)
		adapter := execution.adapter
		root := execution.root
		execution.manifest = nil
		execution.binaries = nil
		execution.adapter = nil
		execution.mu.Unlock()
		if prepared != nil {
			prepared.ReleaseIfUnadopted()
		}
		if manifest != nil {
			execution.closeErr = errors.Join(execution.closeErr, manifest.Close())
		}
		for _, binary := range binaries {
			execution.closeErr = errors.Join(execution.closeErr, binary.Close())
		}
		if adapter != nil {
			execution.closeErr = errors.Join(execution.closeErr, adapter.Close())
		}
		if root != nil {
			execution.closeErr = errors.Join(execution.closeErr, root.Close())
		}
		if execution.coordinator != nil {
			execution.coordinator.forget(execution.taskID, execution)
		}
	})
	return execution.closeErr
}

func allocateExecutionRoots(root, taskID string) (*executionRootOwner, string, string, string, error) {
	if !lowerHex(taskID, 32) {
		return nil, "", "", "", task.ErrInvalidArgument
	}
	executionRoot := filepath.Join(root, taskID)
	if err := createOwnerOnlyExecutionDirectory(executionRoot); err != nil {
		return nil, "", "", "", task.ErrConflict
	}
	owner, err := retainExecutionRoot(executionRoot)
	if err != nil {
		_ = os.Remove(executionRoot)
		return nil, "", "", "", err
	}
	fail := func() (*executionRootOwner, string, string, string, error) {
		_ = owner.Close()
		return nil, "", "", "", task.ErrInvalidArgument
	}
	paths := []string{
		filepath.Join(executionRoot, "instrumentation"),
		filepath.Join(executionRoot, "profiles"),
		filepath.Join(executionRoot, "build"),
	}
	for _, path := range paths {
		if err := createOwnerOnlyExecutionDirectory(path); err != nil || owner.VerifyDirectory(path) != nil {
			return fail()
		}
	}
	return owner, paths[0], paths[1], paths[2], nil
}

func retainTestBinaries(originals map[string]task.ExecutionStep) ([]*retainedFile, error) {
	paths := make(map[string]struct{})
	for _, step := range originals {
		if step.Process.Executable != "" {
			paths[step.Process.Executable] = struct{}{}
		}
		for _, item := range step.Process.Batch {
			paths[item.Executable] = struct{}{}
		}
	}
	ordered := make([]string, 0, len(paths))
	for path := range paths {
		ordered = append(ordered, path)
	}
	sort.Slice(ordered, func(left, right int) bool {
		return strings.ToLower(ordered[left]) < strings.ToLower(ordered[right])
	})
	result := make([]*retainedFile, 0, len(ordered))
	for _, path := range ordered {
		file, err := retainFile(path)
		if err != nil {
			for _, retained := range result {
				_ = retained.Close()
			}
			return nil, err
		}
		result = append(result, file)
	}
	if len(result) == 0 {
		return nil, task.ErrInvalidArgument
	}
	return result, nil
}

func validatePreparedIdentity(
	prepared PreparedBuild,
	run coveragedomain.Run,
	testRun testdomain.TestRun,
	profile workspace.CoverageProfile,
	wantToolchain toolchain.Instance,
) error {
	if prepared == nil || prepared.WorkspaceGeneration() != run.Request.WorkspaceGeneration ||
		prepared.Project().ID != run.Request.ProjectID ||
		prepared.Profile().ID != testRun.ProfileID ||
		profile.BaseBuildProfileID != testRun.ProfileID ||
		prepared.Toolchain().ID != testRun.ToolchainID {
		return task.ErrConflict
	}
	if wantToolchain.ID != "" && !reflect.DeepEqual(prepared.Toolchain(), wantToolchain) {
		return task.ErrConflict
	}
	return nil
}

func validateQueuedGraph(
	current task.Task,
	run coveragedomain.Run,
	testRun testdomain.TestRun,
	profile workspace.CoverageProfile,
) error {
	validatedRun, err := coveragedomain.NewRun(run)
	if err != nil || validatedRun.Status != coveragedomain.StatusQueued ||
		validatedRun.TaskID != current.ID ||
		validatedRun.Request.IdempotencyKey != current.IdempotencyKey ||
		validatedRun.Request.WorkspaceGeneration != current.WorkspaceGeneration ||
		validatedRun.Request.Timeout != current.Timeout ||
		validatedRun.TestRunID != testRun.RunID {
		return task.ErrConflict
	}
	canonical, err := validatedRun.Request.CanonicalJSON()
	if err != nil || !bytes.Equal(canonical, current.Request) {
		return task.ErrConflict
	}
	validatedTest, err := testdomain.NewTestRun(testRun)
	if err != nil || validatedTest.Status != testdomain.RunQueued ||
		validatedTest.TaskID != current.ID ||
		validatedTest.ProjectID != validatedRun.Request.ProjectID ||
		validatedTest.CatalogRevision != validatedRun.Request.CatalogRevision ||
		validatedTest.IdempotencyKey != current.IdempotencyKey ||
		profile.ID != validatedRun.Request.CoverageProfileID ||
		profile.BaseBuildProfileID != validatedTest.ProfileID {
		return task.ErrConflict
	}
	return nil
}

func loadCoverageProfile(root workspace.Root, id string) (workspace.CoverageProfile, error) {
	loaded, err := workspace.LoadConfig(root)
	if err != nil || len(loaded.Issues) != 0 {
		return workspace.CoverageProfile{}, task.ErrInvalidArgument
	}
	for _, profile := range loaded.Config.CoverageProfiles {
		if profile.ID == id {
			profile.Include = append([]string(nil), profile.Include...)
			profile.Exclude = append([]string(nil), profile.Exclude...)
			return profile, nil
		}
	}
	return workspace.CoverageProfile{}, task.ErrNotFound
}

func sameQueuedTask(stored, supplied task.Task) bool {
	return stored.ID == supplied.ID && stored.Kind == task.KindCoverageRun &&
		stored.Status == task.StatusQueued && supplied.Status == task.StatusQueued &&
		sameTaskIdentity(stored, supplied)
}

func sameTaskIdentity(left, right task.Task) bool {
	return left.ID == right.ID && left.IdempotencyKey == right.IdempotencyKey &&
		left.RequestHash == right.RequestHash && left.Kind == right.Kind &&
		left.WorkspaceGeneration == right.WorkspaceGeneration &&
		left.Timeout == right.Timeout && bytes.Equal(left.Request, right.Request)
}

func sameTestRunIdentity(left, right testdomain.TestRun) bool {
	return left.RunID == right.RunID && left.TaskID == right.TaskID &&
		left.ProjectID == right.ProjectID && left.ProfileID == right.ProfileID &&
		left.ToolchainID == right.ToolchainID &&
		left.CatalogRevision == right.CatalogRevision &&
		left.IdempotencyKey == right.IdempotencyKey &&
		left.CreatedAt.Equal(right.CreatedAt) &&
		reflect.DeepEqual(left.SelectionSnapshot, right.SelectionSnapshot)
}

func coverageRunID(request []byte) string {
	if len(request) == 0 || !json.Valid(request) {
		return ""
	}
	var canonical bytes.Buffer
	if err := json.Compact(&canonical, request); err != nil || !bytes.Equal(canonical.Bytes(), request) {
		return ""
	}
	hash := sha256.Sum256(append([]byte("coverage-run-v1\x00"), request...))
	return hex.EncodeToString(hash[:16])
}

func lowerHex(value string, size int) bool {
	if len(value) != size {
		return false
	}
	for _, character := range value {
		if !strings.ContainsRune("0123456789abcdef", character) {
			return false
		}
	}
	return true
}

func cloneToolchain(value toolchain.Instance) toolchain.Instance {
	value.Environment = append([]string(nil), value.Environment...)
	value.Generators = append([]string(nil), value.Generators...)
	return value
}

func errOrInvalid(err error) error {
	if err != nil {
		return err
	}
	return task.ErrInvalidArgument
}

func cloneEvents(values []task.DomainEvent) []task.DomainEvent {
	result := make([]task.DomainEvent, len(values))
	for index, value := range values {
		result[index] = task.DomainEvent{
			Type:    value.Type,
			Payload: append(json.RawMessage(nil), value.Payload...),
		}
	}
	return result
}

func outcomeKey(id string, iteration int64) string {
	return id + "\x00" + time.Unix(0, iteration).UTC().Format(time.RFC3339Nano)
}

func hasReason(values map[coveragedomain.CompletenessReason]struct{}, reason coveragedomain.CompletenessReason) bool {
	_, ok := values[reason]
	return ok
}

func phaseForStep(kind task.StepKind) coveragerun.Phase {
	switch kind {
	case task.StepCoverageConfigure:
		return coveragerun.PhaseConfigure
	case task.StepCoverageBuild:
		return coveragerun.PhaseBuild
	case task.StepCoverageTest:
		return coveragerun.PhaseTest
	case task.StepCoverageMerge:
		return coveragerun.PhaseMerge
	case task.StepCoverageNormalize:
		return coveragerun.PhaseNormalize
	case task.StepCoverageReport:
		return coveragerun.PhaseReport
	case task.StepCoveragePublish:
		return coveragerun.PhasePublish
	default:
		return ""
	}
}
