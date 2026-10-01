package testgencoord

import "unit-test-ide.local/test-service/internal/testgendomain"

// NextStage identifies work after the last durably completed checkpoint.
func NextStage(completed testgendomain.State) (testgendomain.State, error) {
	switch completed {
	case testgendomain.StateQueued:
		return testgendomain.StateBaseline, nil
	case testgendomain.StateBaseline:
		return testgendomain.StateAnalyzing, nil
	case testgendomain.StateAnalyzing:
		return testgendomain.StateSolving, nil
	case testgendomain.StateSolving:
		return testgendomain.StateRendering, nil
	case testgendomain.StateRendering:
		return testgendomain.StateValidating, nil
	case testgendomain.StateValidating:
		return testgendomain.StateMinimizing, nil
	case testgendomain.StateMinimizing:
		return testgendomain.StateAwaitingConfirmation, nil
	default:
		return "", testgendomain.ErrInvalid
	}
}
