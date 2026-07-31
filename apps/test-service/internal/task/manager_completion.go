package task

import (
	"context"
	"errors"
)

type pendingProcessCompletion struct {
	Result      ProcessResult
	FailPending bool
}

func (m *Manager) stageProcessCompletion(
	current *activeTask,
	result ProcessResult,
	failPending bool,
) {
	if current.pendingCompletion != nil {
		return
	}
	current.pendingCompletion = &pendingProcessCompletion{
		Result:      result,
		FailPending: failPending,
	}
	current.processCompleted = true
	m.maybeStartClose(current)
}

func processCompletionOutcome(
	current *activeTask,
	pending pendingProcessCompletion,
) (Outcome, StepVerdict) {
	if cause := current.execution.currentCause(); cause != "" {
		return cause, StepVerdictDefault
	}
	if current.terminationFailed || pending.Result.Err != nil {
		return OutcomeInfrastructureFailed, StepVerdictDefault
	}
	if pending.Result.ExitCode == 0 {
		return OutcomeSucceeded, StepVerdictSucceeded
	}
	return OutcomeCommandFailed, StepVerdictFailed
}

func (m *Manager) commitClosedCompletion(
	current *activeTask,
	active map[string]*activeTask,
) error {
	if current.pendingCompletion == nil || !current.closeComplete {
		return ErrConflict
	}
	pending := *current.pendingCompletion
	outcome, verdict := processCompletionOutcome(current, pending)
	if outcome == OutcomeSucceeded || outcome == OutcomeCommandFailed {
		var interpretationErr error
		outcome, verdict, interpretationErr = m.interpretProcessResult(
			current,
			pending,
		)
		if interpretationErr != nil {
			pending.Result = ProcessResult{Err: interpretationErr}
			pending.FailPending = false
		}
	}
	nextPlan := current.plan
	appendedSnapshots := []StepSnapshot(nil)
	if outcome == OutcomeSucceeded {
		var callbackErr error
		if m.stepObserver != nil &&
			current.nextStep+1 < len(current.plan.Steps) {
			callbackErr = m.stepObserver.Succeeded(
				current.execution.ctx,
				cloneRuntimeTask(current.task),
				cloneRuntimeStep(current.plan.Steps[current.nextStep]),
			)
		}
		if callbackErr == nil && current.continuation != nil {
			var continuation Continuation
			continuation, callbackErr = callContinuation(
				current.execution.ctx,
				current.continuation,
				cloneRuntimeTask(current.task),
				cloneRuntimeStep(current.plan.Steps[current.nextStep]),
				StepResult{Process: pending.Result, Verdict: verdict},
			)
			if callbackErr == nil {
				nextPlan, appendedSnapshots, callbackErr =
					extendExecutionPlan(
						current.plan,
						continuation,
						current.boundary,
					)
			}
		}
		if cause := current.execution.currentCause(); cause != "" {
			outcome = cause
			callbackErr = nil
		}
		if callbackErr != nil {
			pending.Result = ProcessResult{Err: callbackErr}
			pending.FailPending = false
			outcome = OutcomeInfrastructureFailed
		} else if current.nextStep+1 < len(nextPlan.Steps) {
			if err := m.persistSuccessfulStep(
				current,
				pending.Result,
				nextPlan,
				appendedSnapshots,
				active,
			); err != nil {
				if !errors.Is(err, ErrConflict) {
					return err
				}
				pending.Result = ProcessResult{Err: ErrConflict}
				pending.FailPending = false
				outcome = OutcomeInfrastructureFailed
			} else {
				current.nextStep++
				resetClosedProcess(current)
				return m.startNextStep(current, active)
			}
		}
	}
	finished, err := m.persistTerminal(
		current,
		pending.Result,
		outcome,
		pending.FailPending,
		true,
		active,
	)
	if err != nil {
		return err
	}
	current.task = finished
	current.leasePersisted = false
	current.pendingCompletion = nil
	m.stopActive(current)
	return nil
}

func (m *Manager) interpretProcessResult(
	current *activeTask,
	pending pendingProcessCompletion,
) (Outcome, StepVerdict, error) {
	outcome, verdict := processCompletionOutcome(current, pending)
	if outcome != OutcomeSucceeded && outcome != OutcomeCommandFailed ||
		current.resultInterpreter == nil {
		return outcome, verdict, nil
	}
	interpreted, err := callResultInterpreter(
		current.execution.ctx,
		current.resultInterpreter,
		cloneRuntimeTask(current.task),
		cloneRuntimeStep(current.plan.Steps[current.nextStep]),
		pending.Result,
	)
	if cause := current.execution.currentCause(); cause != "" {
		return cause, StepVerdictDefault, nil
	}
	if err != nil {
		return OutcomeInfrastructureFailed, StepVerdictDefault, err
	}
	switch interpreted {
	case StepVerdictDefault:
		return outcome, verdict, nil
	case StepVerdictSucceeded:
		return OutcomeSucceeded, StepVerdictSucceeded, nil
	case StepVerdictFailed:
		return OutcomeCommandFailed, StepVerdictFailed, nil
	default:
		return OutcomeInfrastructureFailed, StepVerdictDefault,
			ErrInvalidArgument
	}
}

func callResultInterpreter(
	ctx context.Context,
	interpreter ResultInterpreter,
	current Task,
	step ExecutionStep,
	result ProcessResult,
) (verdict StepVerdict, resultErr error) {
	defer func() {
		if recover() != nil {
			verdict = StepVerdictDefault
			resultErr = errors.New("result interpreter panicked")
		}
	}()
	return interpreter.Interpret(ctx, current, step, result)
}

func callResultOutputObserver(
	ctx context.Context,
	observer ResultOutputObserver,
	current Task,
	step ExecutionStep,
	output ProcessOutput,
) (resultErr error) {
	defer func() {
		if recover() != nil {
			resultErr = errors.New("result output observer panicked")
		}
	}()
	return observer.ObserveOutput(ctx, current, step, output)
}

func callContinuation(
	ctx context.Context,
	provider PlanContinuation,
	current Task,
	step ExecutionStep,
	result StepResult,
) (continuation Continuation, resultErr error) {
	defer func() {
		if recover() != nil {
			continuation = Continuation{}
			resultErr = errors.New("plan continuation panicked")
		}
	}()
	return provider.AfterStep(ctx, current, step, result)
}

func cloneRuntimeTask(value Task) Task {
	result := value
	result.Request = append([]byte(nil), value.Request...)
	result.Steps = cloneStepSnapshots(value.Steps)
	return result
}

func cloneRuntimeStep(value ExecutionStep) ExecutionStep {
	return cloneExecutionPlan(ExecutionPlan{
		Version: 1,
		Steps:   []ExecutionStep{value},
	}).Steps[0]
}

func resetClosedProcess(current *activeTask) {
	current.process = nil
	current.leasePersisted = false
	current.pendingCompletion = nil
	current.processCompleted = false
	current.terminating = false
	current.terminationComplete = false
	current.terminationFailed = false
	current.closeStarted = false
	current.closeComplete = false
	current.closeFailed = false
	current.cleanupWithoutDone = false
	current.failPendingStep = false
}
