package coverageexec

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"unit-test-ide.local/test-service/internal/coveragedomain"
	"unit-test-ide.local/test-service/internal/coverageplatform"
	"unit-test-ide.local/test-service/internal/coveragerun"
	"unit-test-ide.local/test-service/internal/task"
)

func TestConfigureCheckpointFailureDoesNotAdvanceCoveragePhase(t *testing.T) {
	ctx := context.Background()
	fixture := newSQLiteCoverageFixture(t, unusedProcessFactory{})
	base := preparedBuildForFixture(fixture)
	base.toolchain.Version = "1"
	base.toolchain.Coverage.ToolsetIdentity = "identity"
	checkpointErr := errors.New("configure checkpoint rejected")
	prepared := &configureCheckpointBuild{fakePreparedBuild: base, err: checkpointErr}
	owner, instrumentationRoot, _, _, err := allocateExecutionRoots(fixture.executionRoot, fixture.persisted.ID)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = owner.Close() })
	run, err := fixture.store.GetCoverageRun(ctx, fixture.aggregate.Run.ID)
	if err != nil {
		t.Fatal(err)
	}
	testRun, err := fixture.store.GetRunForTask(ctx, fixture.persisted.ID)
	if err != nil {
		t.Fatal(err)
	}
	profile, err := loadCoverageProfile(fixture.workspaceRoot, run.Request.CoverageProfileID)
	if err != nil {
		t.Fatal(err)
	}
	instrument := coverageplatform.Instrumentation{
		IncludePath: filepath.Join(instrumentationRoot, "coverage.cmake"),
		Fingerprint: run.Toolchain.InstrumentationFingerprint,
	}
	adapter := &configureCheckpointAdapter{
		handoffTestAdapter: handoffTestAdapter{toolset: &handoffTestToolset{path: filepath.Join(t.TempDir(), "clang")}},
		instrument:         instrument,
	}
	coordinator := &Coordinator{config: Config{Store: fixture.store, WorkspaceRoot: fixture.workspaceRoot}}
	live := &execution{
		config: Config{Store: fixture.store, WorkspaceRoot: fixture.workspaceRoot,
			Build: &scriptedBuildPreparer{plan: base}},
		coordinator: coordinator, taskID: fixture.persisted.ID, initialTask: fixture.persisted,
		run: run, testRun: testRun, profile: profile, catalog: fixture.store.catalog,
		prepared: prepared, toolchain: base.toolchain, adapter: adapter,
		root: owner, taskRoot: owner.path, instrument: instrument,
		state: coveragerun.NewState(), failedPhase: coveragerun.PhaseConfigure,
	}
	_, err = live.AfterStep(ctx, fixture.persisted,
		task.ExecutionStep{Kind: task.StepCoverageConfigure},
		task.StepResult{Verdict: task.StepVerdictSucceeded})
	if !errors.Is(err, checkpointErr) || prepared.calls != 1 {
		t.Fatalf("configure checkpoint = %v, calls=%d", err, prepared.calls)
	}
	if live.state.Phase != coveragerun.PhaseConfigure || live.failedPhase != coveragerun.PhaseConfigure {
		t.Fatalf("failed configure checkpoint advanced coverage: state=%#v, failedPhase=%q", live.state, live.failedPhase)
	}
	// An out-of-order callback must still fail before the checkpoint writes.
	live.state.Phase = coveragerun.PhaseBuild
	_, err = live.AfterStep(ctx, fixture.persisted,
		task.ExecutionStep{Kind: task.StepCoverageConfigure},
		task.StepResult{Verdict: task.StepVerdictSucceeded})
	if !errors.Is(err, coveragerun.ErrInvalidTransition) || prepared.calls != 1 {
		t.Fatalf("out-of-order configure callback = %v, checkpoint calls=%d", err, prepared.calls)
	}
	live.state = coveragerun.NewState()

	// Exercise the real Task -> TestRun -> CoverageRun terminal transaction,
	// rather than only checking the in-memory phase. The old phase advance
	// projected an infrastructure error as build_failed/command_failed and
	// left the durable coverage task unfinished.
	driver := &configureCheckpointFailureDriver{execution: live, err: checkpointErr}
	plan := task.ExecutionPlan{Version: 1, Steps: []task.ExecutionStep{reportActionStep()}}
	plan.Fingerprint = task.FingerprintPlan(plan)
	if _, err := fixture.manager.ResumeQueued(ctx, task.ResumeRequest{
		Task: fixture.persisted, Plan: plan, Boundary: permissiveCoverageBoundary{},
		ResultInterpreter: driver, ActionExecutor: driver,
	}); err != nil {
		t.Fatal(err)
	}
	finished := fixture.awaitFinished(t)
	run, err = fixture.store.GetCoverageRun(ctx, fixture.aggregate.Run.ID)
	if err != nil || finished.Outcome != task.OutcomeInfrastructureFailed ||
		run.Status != coveragedomain.StatusFinished || run.Outcome != coveragedomain.OutcomeUnavailable ||
		run.Reason != coveragedomain.ReasonInstrumentationFailed || !fixture.manager.Healthy() {
		t.Fatalf("configure failure terminal aggregate: task=%#v run=%#v healthy=%v err=%v", finished, run, fixture.manager.Healthy(), err)
	}
}

type configureCheckpointBuild struct {
	*fakePreparedBuild
	err   error
	calls int
}

func (prepared *configureCheckpointBuild) PersistConfiguration(context.Context) error {
	prepared.calls++
	return prepared.err
}

type configureCheckpointAdapter struct {
	handoffTestAdapter
	instrument coverageplatform.Instrumentation
}

func (adapter *configureCheckpointAdapter) Instrumentation() coverageplatform.Instrumentation {
	return adapter.instrument
}

type configureCheckpointFailureDriver struct {
	*execution
	err error
}

func (driver *configureCheckpointFailureDriver) ExecuteServiceAction(context.Context, task.Task, task.ExecutionStep) (task.StepResult, error) {
	return task.StepResult{}, driver.err
}
