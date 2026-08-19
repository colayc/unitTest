package coveragerun

import (
	"context"
	"errors"

	"unit-test-ide.local/test-service/internal/processcontrol"
)

var ErrInvalidProcessPhase = errors.New("coverage process result has invalid phase")

// ProcessEvidence is the small adapter input shared by native collectors and
// the state machine. ProfileMissing is supplied by the collector after its
// bounded profile lookup; no profile path crosses this boundary.
type ProcessEvidence struct {
	Result         processcontrol.Result
	ProfileMissing bool
}

// MapProcessResult translates a controlled process result into coverage
// semantics. A non-zero test exit is assertion evidence, while a host/process
// error is infrastructure failure. Configure/build exits are infrastructure
// failures because no trustworthy test evidence exists yet.
func MapProcessResult(phase Phase, evidence ProcessEvidence) (StepResult, error) {
	if phase != PhaseConfigure && phase != PhaseBuild && phase != PhaseTest {
		return StepResult{}, ErrInvalidProcessPhase
	}
	result := StepResult{Phase: phase, ProfileMissing: evidence.ProfileMissing}
	process := evidence.Result
	if errors.Is(process.Err, context.Canceled) || childCancelled(process.Children) {
		result.Cancelled = true
		return result, nil
	}
	if errors.Is(process.Err, context.DeadlineExceeded) || childDeadline(process.Children) || childTimedOut(process.Children) {
		result.TimedOut = true
		return result, nil
	}
	if process.Err != nil || childError(process.Children) {
		result.InfrastructureFailure = true
		return result, nil
	}
	if process.ExitCode != 0 || childExitFailure(process.Children) {
		if phase == PhaseTest {
			result.AssertionFailure = true
		} else {
			result.InfrastructureFailure = true
		}
		return result, nil
	}
	result.Succeeded = true
	return result, nil
}

func childTimedOut(children []processcontrol.ChildResult) bool {
	for _, child := range children {
		if child.TimedOut {
			return true
		}
	}
	return false
}

func childCancelled(children []processcontrol.ChildResult) bool {
	for _, child := range children {
		if errors.Is(child.Err, context.Canceled) {
			return true
		}
	}
	return false
}

func childDeadline(children []processcontrol.ChildResult) bool {
	for _, child := range children {
		if errors.Is(child.Err, context.DeadlineExceeded) {
			return true
		}
	}
	return false
}

func childError(children []processcontrol.ChildResult) bool {
	for _, child := range children {
		if child.Err != nil && !errors.Is(child.Err, context.Canceled) && !errors.Is(child.Err, context.DeadlineExceeded) {
			return true
		}
	}
	return false
}

func childExitFailure(children []processcontrol.ChildResult) bool {
	for _, child := range children {
		if child.ExitCode != 0 {
			return true
		}
	}
	return false
}
