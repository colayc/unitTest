// Package coveragerun contains the platform-neutral coverage task state
// machine. It decides whether a pipeline may continue and how evidence maps
// to the public coverage outcome; platform collectors supply the step facts.
package coveragerun

import (
	"errors"

	"unit-test-ide.local/test-service/internal/coveragedomain"
)

var (
	ErrInvalidTransition = errors.New("invalid coverage state transition")
	ErrTerminalState     = errors.New("coverage state is terminal")
)

type Phase string

const (
	PhaseConfigure Phase = "configure"
	PhaseBuild     Phase = "build"
	PhaseTest      Phase = "test"
	PhaseMerge     Phase = "merge"
	PhaseNormalize Phase = "normalize"
	PhaseReport    Phase = "report"
	PhasePublish   Phase = "publish"
)

type StepResult struct {
	Phase                 Phase
	Succeeded             bool
	Cancelled             bool
	AssertionFailure      bool
	Crash                 bool
	TimedOut              bool
	ProfileMissing        bool
	InfrastructureFailure bool
}

type State struct {
	Phase          Phase
	Terminal       bool
	Outcome        coveragedomain.Outcome
	Reason         coveragedomain.Reason
	PartialReasons []coveragedomain.CompletenessReason
	TestFailed     bool
}

func NewState() State { return State{Phase: PhaseConfigure} }

func (state State) Apply(result StepResult) (State, error) {
	if state.Terminal {
		return State{}, ErrTerminalState
	}
	if result.Phase != state.Phase {
		return State{}, ErrInvalidTransition
	}
	next := state
	next.PartialReasons = append([]coveragedomain.CompletenessReason(nil), state.PartialReasons...)
	if result.Cancelled {
		next.Terminal = true
		next.Outcome = coveragedomain.OutcomeCancelled
		next.Reason = coveragedomain.ReasonUserCancelled
		next.Phase = ""
		return next, nil
	}
	switch result.Phase {
	case PhaseConfigure:
		if !result.Succeeded || result.InfrastructureFailure {
			return unavailable(next, coveragedomain.ReasonInstrumentationFailed), nil
		}
		next.Phase = PhaseBuild
	case PhaseBuild:
		if !result.Succeeded || result.InfrastructureFailure {
			return unavailable(next, coveragedomain.ReasonBuildFailed), nil
		}
		next.Phase = PhaseTest
	case PhaseTest:
		if result.InfrastructureFailure {
			return unavailable(next, coveragedomain.ReasonProfileCollectionFailed), nil
		}
		if result.Crash {
			addPartialReason(&next, coveragedomain.CompletenessReasonTestCrashed)
		} else if result.TimedOut {
			addPartialReason(&next, coveragedomain.CompletenessReasonTestTimedOut)
		}
		if result.ProfileMissing {
			addPartialReason(&next, coveragedomain.CompletenessReasonProfileMissingForFailedInvocation)
		}
		if !result.Succeeded && !result.Crash && !result.TimedOut {
			next.TestFailed = true
		}
		next.Phase = PhaseMerge
	case PhaseMerge:
		if !result.Succeeded {
			return unavailable(next, coveragedomain.ReasonProfileCollectionFailed), nil
		}
		next.Phase = PhaseNormalize
	case PhaseNormalize:
		if !result.Succeeded {
			return unavailable(next, coveragedomain.ReasonNormalizationFailed), nil
		}
		next.Phase = PhaseReport
	case PhaseReport:
		if !result.Succeeded {
			return unavailable(next, coveragedomain.ReasonReportGenerationFailed), nil
		}
		next.Phase = PhasePublish
	case PhasePublish:
		if !result.Succeeded {
			return unavailable(next, coveragedomain.ReasonPersistenceFailed), nil
		}
		next.Terminal = true
		next.Phase = ""
		if len(next.PartialReasons) == 0 {
			next.Outcome = coveragedomain.OutcomeAvailable
		} else {
			next.Outcome = coveragedomain.OutcomePartial
		}
	default:
		return State{}, ErrInvalidTransition
	}
	return next, nil
}

func unavailable(state State, reason coveragedomain.Reason) State {
	state.Terminal = true
	state.Phase = ""
	state.Outcome = coveragedomain.OutcomeUnavailable
	state.Reason = reason
	return state
}

func addPartialReason(state *State, reason coveragedomain.CompletenessReason) {
	for _, existing := range state.PartialReasons {
		if existing == reason {
			return
		}
	}
	state.PartialReasons = append(state.PartialReasons, reason)
}
