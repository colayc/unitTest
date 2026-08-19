package coveragerun

import (
	"errors"
	"sort"

	"unit-test-ide.local/test-service/internal/coveragedomain"
)

var ErrCompletenessUnavailable = errors.New("coverage completeness is unavailable")

// FinalizeCompleteness converts a terminal runner state into the report
// completeness value. Unavailable/cancelled runs intentionally produce no
// completeness object because they have no trustworthy coverage report.
func FinalizeCompleteness(state State) (coveragedomain.Completeness, error) {
	if !state.Terminal {
		return coveragedomain.Completeness{}, ErrCompletenessUnavailable
	}
	reasons := append([]coveragedomain.CompletenessReason(nil), state.PartialReasons...)
	switch state.Outcome {
	case coveragedomain.OutcomeAvailable:
		if len(reasons) != 0 {
			return coveragedomain.Completeness{}, ErrCompletenessUnavailable
		}
		return coveragedomain.Completeness{Outcome: coveragedomain.OutcomeAvailable}, nil
	case coveragedomain.OutcomePartial:
		if len(reasons) == 0 || len(reasons) > 64 {
			return coveragedomain.Completeness{}, ErrCompletenessUnavailable
		}
		seen := make(map[coveragedomain.CompletenessReason]struct{}, len(reasons))
		for _, reason := range reasons {
			if !validPartialReason(reason) {
				return coveragedomain.Completeness{}, ErrCompletenessUnavailable
			}
			if _, exists := seen[reason]; exists {
				return coveragedomain.Completeness{}, ErrCompletenessUnavailable
			}
			seen[reason] = struct{}{}
		}
		sort.Slice(reasons, func(left, right int) bool { return reasons[left] < reasons[right] })
		return coveragedomain.Completeness{Outcome: coveragedomain.OutcomePartial, Reasons: reasons}, nil
	default:
		return coveragedomain.Completeness{}, ErrCompletenessUnavailable
	}
}

func validPartialReason(reason coveragedomain.CompletenessReason) bool {
	switch reason {
	case coveragedomain.CompletenessReasonTestCrashed,
		coveragedomain.CompletenessReasonTestTimedOut,
		coveragedomain.CompletenessReasonProfileMissingForFailedInvocation:
		return true
	default:
		return false
	}
}
