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
) Outcome {
	if cause := current.execution.currentCause(); cause != "" {
		return cause
	}
	if current.terminationFailed || pending.Result.Err != nil {
		return OutcomeInfrastructureFailed
	}
	if pending.Result.ExitCode == 0 {
		return OutcomeSucceeded
	}
	return OutcomeCommandFailed
}

func (m *Manager) commitClosedCompletion(
	current *activeTask,
	active map[string]*activeTask,
) error {
	if current.pendingCompletion == nil || !current.closeComplete {
		return ErrConflict
	}
	pending := *current.pendingCompletion
	outcome := processCompletionOutcome(current, pending)
	if outcome == OutcomeSucceeded &&
		current.nextStep+1 < len(current.plan.Steps) {
		var observerErr error
		if m.stepObserver != nil {
			observerErr = m.stepObserver.Succeeded(
				context.Background(), current.task, current.plan.Steps[current.nextStep],
			)
		}
		if observerErr != nil {
			pending.Result = ProcessResult{Err: observerErr}
			pending.FailPending = false
			outcome = OutcomeInfrastructureFailed
		} else if err := m.persistSuccessfulStep(current, pending.Result, active); err != nil {
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
