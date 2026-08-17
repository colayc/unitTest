package testrun

import "unit-test-ide.local/test-service/internal/testdomain"

const MaxRepeatCount int64 = 100

func Summarize(
	results []testdomain.TestItemResult,
	repeatCount int64,
) (testdomain.RunSummary, bool, error) {
	if repeatCount < 1 || repeatCount > MaxRepeatCount {
		return testdomain.RunSummary{}, false, testdomain.ErrInvalidResult
	}
	summary := testdomain.RunSummary{
		Iterations: repeatCount,
	}
	type resultIdentity struct {
		itemID    testdomain.ID
		iteration int64
	}
	seen := make(map[resultIdentity]struct{}, len(results))
	incomplete := false
	for _, candidate := range results {
		result, err := testdomain.NewTestItemResult(candidate)
		if err != nil || result.Iteration > repeatCount {
			return testdomain.RunSummary{}, false,
				testdomain.ErrInvalidResult
		}
		identity := resultIdentity{
			itemID:    result.ItemID,
			iteration: result.Iteration,
		}
		if _, duplicate := seen[identity]; duplicate {
			return testdomain.RunSummary{}, false,
				testdomain.ErrInvalidResult
		}
		seen[identity] = struct{}{}
		summary.Total++
		incomplete = incomplete || result.Partial
		switch result.Outcome {
		case testdomain.ItemPassed:
			summary.Passed++
			summary.Completed++
		case testdomain.ItemFailed:
			summary.Failed++
			summary.Completed++
		case testdomain.ItemSkipped:
			summary.Skipped++
			summary.Completed++
		case testdomain.ItemErrored:
			summary.Errored++
			summary.Completed++
		case testdomain.ItemCancelled:
			summary.Cancelled++
			summary.Completed++
		case testdomain.ItemTimedOut:
			summary.TimedOut++
			summary.Completed++
		case testdomain.ItemNotRun:
			summary.NotRun++
			incomplete = true
		}
	}
	return summary, incomplete, nil
}
