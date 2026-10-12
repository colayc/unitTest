package protocol_test

import (
	"encoding/json"
	"testing"

	"unit-test-ide.local/test-service/internal/protocol"
	coveragev16 "unit-test-ide.local/test-service/internal/protocolmodel/v1_6/coverage"
)

func TestCoverageDetailNullDeltaIsRequiredJSON(t *testing.T) {
	for _, metric := range []coveragev16.CoverageMetricV16{{}, {Covered: 2, Total: 4}} {
		data, err := json.Marshal(metric)
		if err != nil {
			t.Fatal(err)
		}
		var wire map[string]json.RawMessage
		if err := json.Unmarshal(data, &wire); err != nil {
			t.Fatal(err)
		}
		if string(wire["coveredDelta"]) != "null" {
			t.Fatalf("wire=%s", data)
		}
		if !protocol.ValidCoverageMetricV16(metric) {
			t.Fatal("null rejected")
		}
	}
}

func detailDelta(value int64) *int64 { return &value }

func TestCoverageDetailSummaryComparisonIsConsistent(t *testing.T) {
	nullMetric := coveragev16.CoverageMetricV16{Covered: 2, Total: 4}
	numberMetric := coveragev16.CoverageMetricV16{Covered: 2, Total: 4, CoveredDelta: detailDelta(-1)}
	for _, tc := range []struct {
		name    string
		summary coveragev16.CoverageSummaryV16
		valid   bool
	}{
		{"null", coveragev16.CoverageSummaryV16{Functions: nullMetric, Lines: nullMetric, Branches: nullMetric}, true},
		{"number", coveragev16.CoverageSummaryV16{Functions: numberMetric, Lines: numberMetric, Branches: numberMetric}, true},
		{"mixed functions", coveragev16.CoverageSummaryV16{Functions: numberMetric, Lines: nullMetric, Branches: nullMetric}, false},
		{"mixed lines", coveragev16.CoverageSummaryV16{Functions: nullMetric, Lines: numberMetric, Branches: nullMetric}, false},
		{"mixed branches", coveragev16.CoverageSummaryV16{Functions: nullMetric, Lines: nullMetric, Branches: numberMetric}, false},
		{"reverse mixed", coveragev16.CoverageSummaryV16{Functions: numberMetric, Lines: nullMetric, Branches: numberMetric}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := protocol.ValidCoverageSummaryV16(tc.summary); got != tc.valid {
				t.Fatalf("valid=%t want=%t", got, tc.valid)
			}
		})
	}
	if protocol.ValidCoverageMetricV16(coveragev16.CoverageMetricV16{Covered: 5, Total: 4}) {
		t.Fatal("null delta bypassed current count invariant")
	}
}
