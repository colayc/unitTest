package coveragerun

import (
	"context"
	"errors"
)

var ErrNilExecutor = errors.New("coverage step executor is nil")

type StepExecutor interface {
	Execute(context.Context, Phase) (StepResult, error)
}

// Run serializes the platform executor through the fixed coverage state
// machine. Executor errors become terminal infrastructure outcomes while the
// original error is returned for internal diagnostics; cancellation is a
// normal terminal outcome and does not leak an execution error.
func Run(ctx context.Context, executor StepExecutor) (State, error) {
	if executor == nil {
		return State{}, ErrNilExecutor
	}
	state := NewState()
	for !state.Terminal {
		if ctx.Err() != nil {
			cancelled, err := state.Apply(StepResult{Phase: state.Phase, Cancelled: true})
			if err != nil {
				return State{}, err
			}
			return cancelled, nil
		}
		phase := state.Phase
		result, executeErr := executor.Execute(ctx, phase)
		if executeErr != nil {
			if errors.Is(executeErr, context.Canceled) {
				cancelled, err := state.Apply(StepResult{Phase: phase, Cancelled: true})
				if err != nil {
					return State{}, err
				}
				return cancelled, nil
			}
			failed, applyErr := state.Apply(StepResult{Phase: phase, InfrastructureFailure: true})
			if applyErr != nil {
				return State{}, applyErr
			}
			return failed, executeErr
		}
		if result.Phase == "" {
			result.Phase = phase
		}
		next, err := state.Apply(result)
		if err != nil {
			return State{}, err
		}
		state = next
	}
	return state, nil
}
