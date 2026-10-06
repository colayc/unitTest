package coverageexec

import (
	"context"
	"errors"
	"testing"

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
