package coverageexec

import (
	"context"
	"errors"
	"strings"
	"testing"

	"unit-test-ide.local/test-service/internal/coveragerun"
	"unit-test-ide.local/test-service/internal/task"
)

func TestExecuteServiceActionExposesPreparationCommandFailureInDebugMode(t *testing.T) {
	t.Setenv("UT_DEBUG_PROCESS_HOST_FAILURES", "1")
	current := task.Task{ID: "11111111111111111111111111111111"}
	terminalErr := errors.New("coverage instrumentation preparation rejected")
	execution := &execution{
		taskID:          current.ID,
		terminalErr:     terminalErr,
		terminalOutcome: task.OutcomeCommandFailed,
	}

	result, err := execution.ExecuteServiceAction(
		context.Background(),
		current,
		task.ExecutionStep{Action: task.ServiceActionCoverageNormalize},
	)
	if err != nil {
		t.Fatalf("debug preparation error = %v, want nil", err)
	}
	if result.Verdict != task.StepVerdictFailed {
		t.Fatalf("debug preparation verdict = %v, want failed", result.Verdict)
	}
	if !errors.Is(result.Process.Err, terminalErr) {
		t.Fatalf("debug preparation process error = %v, want %v", result.Process.Err, terminalErr)
	}
}

func TestFailPreparationStageAddsOnlyStableDebugCategory(t *testing.T) {
	t.Setenv("UT_DEBUG_PROCESS_HOST_FAILURES", "1")
	debugErr := failPreparationStage(coveragerun.PhaseBuild, "instrumented build plan", task.ErrInvalidArgument)
	if !errors.Is(debugErr, task.ErrInvalidArgument) || !strings.Contains(debugErr.Error(), "coverage preparation rejected: instrumented build plan") {
		t.Fatalf("debug preparation stage error = %v", debugErr)
	}

	t.Setenv("UT_DEBUG_PROCESS_HOST_FAILURES", "")
	plainErr := failPreparationStage(coveragerun.PhaseBuild, "instrumented build plan", task.ErrInvalidArgument)
	if !errors.Is(plainErr, task.ErrInvalidArgument) || strings.Contains(plainErr.Error(), "coverage preparation rejected") {
		t.Fatalf("plain preparation stage error = %v", plainErr)
	}
}

func TestExecuteServiceActionKeepsPreparationCommandFailureTerminalOutsideDebugMode(t *testing.T) {
	t.Setenv("UT_DEBUG_PROCESS_HOST_FAILURES", "")
	current := task.Task{ID: "22222222222222222222222222222222"}
	execution := &execution{
		taskID:          current.ID,
		terminalErr:     errors.New("coverage instrumentation preparation rejected"),
		terminalOutcome: task.OutcomeCommandFailed,
	}

	result, err := execution.ExecuteServiceAction(
		context.Background(),
		current,
		task.ExecutionStep{Action: task.ServiceActionCoverageNormalize},
	)
	if err != nil {
		t.Fatalf("non-debug preparation error = %v, want nil", err)
	}
	if result.Verdict != task.StepVerdictFailed {
		t.Fatalf("non-debug preparation verdict = %v, want failed", result.Verdict)
	}
}
