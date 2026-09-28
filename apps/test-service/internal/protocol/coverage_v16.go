package protocol

import coveragev16 "unit-test-ide.local/test-service/internal/protocolmodel/v1_6/coverage"

// ValidCoverageMetricV16 enforces the relational invariant that portable JSON
// Schema cannot express. Service providers must call this on decoded detail
// metrics before returning a v1.6 response.
func ValidCoverageMetricV16(metric coveragev16.CoverageMetricV16) bool {
	return validNonnegativeSafeInteger(metric.Covered) &&
		validNonnegativeSafeInteger(metric.Total) &&
		validSafeInteger(metric.CoveredDelta) &&
		metric.Covered <= metric.Total
}

func ValidCoverageSummaryV16(summary coveragev16.CoverageSummaryV16) bool {
	return ValidCoverageMetricV16(summary.Functions) &&
		ValidCoverageMetricV16(summary.Lines) &&
		ValidCoverageMetricV16(summary.Branches)
}

func validNonnegativeSafeInteger(value int64) bool {
	return value >= 0 && validSafeInteger(value)
}

func validSafeInteger(value int64) bool {
	return value >= -9007199254740991 && value <= 9007199254740991
}
