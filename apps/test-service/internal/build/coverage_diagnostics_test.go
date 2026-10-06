package build

import (
	"errors"
	"strings"
	"testing"

	"unit-test-ide.local/test-service/internal/task"
)

func TestCoveragePreparationDebugErrorKeepsStableStageAndCause(t *testing.T) {
	t.Setenv("UT_DEBUG_PROCESS_HOST_FAILURES", "1")
	debugErr := coveragePreparationDebugError("coverage toolset capability", task.ErrInvalidArgument)
	if !errors.Is(debugErr, task.ErrInvalidArgument) ||
		!strings.Contains(debugErr.Error(), "coverage preparation build plan rejected: coverage toolset capability") {
		t.Fatalf("debug coverage preparation error = %v", debugErr)
	}

	t.Setenv("UT_DEBUG_PROCESS_HOST_FAILURES", "")
	plainErr := coveragePreparationDebugError("coverage toolset capability", task.ErrInvalidArgument)
	if !errors.Is(plainErr, task.ErrInvalidArgument) || strings.Contains(plainErr.Error(), "coverage preparation build plan rejected") {
		t.Fatalf("plain coverage preparation error = %v", plainErr)
	}
}
