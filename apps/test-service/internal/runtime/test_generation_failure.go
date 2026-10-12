package runtime

import (
	"context"
	"errors"
	"unit-test-ide.local/test-service/internal/task"
	"unit-test-ide.local/test-service/internal/testgencoord"
	"unit-test-ide.local/test-service/internal/testgendomain"
)

type generationOperationError struct {
	operation string
	cause     error
}

func (e *generationOperationError) Error() string { return "generation operation failed" }
func (e *generationOperationError) Unwrap() error { return e.cause }
func withGenerationOperation(operation string, cause error) error {
	if cause == nil {
		return nil
	}
	if !(testgendomain.FailureDiagnostic{"unknown", operation, "unknown"}).Valid() {
		return cause
	}
	return &generationOperationError{operation: operation, cause: cause}
}
func generationFailureDiagnostic(stage, operation string, cause error) testgendomain.FailureDiagnostic {
	if !(testgendomain.FailureDiagnostic{stage, "unknown", "unknown"}).Valid() {
		stage = "unknown"
	}
	if !(testgendomain.FailureDiagnostic{"unknown", operation, "unknown"}).Valid() {
		operation = "unknown"
	}
	if operation == "stage_run" {
		var boundary *generationOperationError
		if errors.As(cause, &boundary) && boundary != nil && (testgendomain.FailureDiagnostic{"unknown", boundary.operation, "unknown"}).Valid() {
			operation = boundary.operation
		}
	}
	reason := "unknown"
	switch {
	case errors.Is(cause, context.DeadlineExceeded), errors.Is(cause, testgencoord.ErrBudgetTimeout):
		reason = "timed_out"
	case errors.Is(cause, context.Canceled):
		reason = "cancelled"
	case errors.Is(cause, testgencoord.ErrBudgetExceeded):
		reason = "budget_exceeded"
	case errors.Is(cause, testgencoord.ErrBudgetInvalid):
		reason = "budget_invalid"
	case errors.Is(cause, testgendomain.ErrStaleSnapshot):
		reason = "stale_snapshot"
	case errors.Is(cause, task.ErrInvalidArgument), errors.Is(cause, testgendomain.ErrInvalid):
		reason = "invalid_argument"
	case errors.Is(cause, task.ErrStorageUnavailable):
		reason = "storage_unavailable"
	case errors.Is(cause, testgencoord.ErrUnavailable), errors.Is(cause, errProductionGenerationUnavailable):
		reason = "unavailable"
	case errors.Is(cause, task.ErrConflict):
		reason = "conflict"
	}
	return testgendomain.FailureDiagnostic{Stage: stage, Operation: operation, Reason: reason}
}
