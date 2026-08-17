package coveragemodel_test

import (
	"testing"

	coveragemodelv1 "unit-test-ide.local/test-service/internal/coveragemodel/v1"
)

func TestGeneratedCoverageV1UsesStableJSONFields(t *testing.T) {
	value := coveragemodelv1.CoverageMetricV1{Covered: 1, Total: 2}
	if value.Covered != 1 || value.Total != 2 {
		t.Fatalf("generated metric = %#v", value)
	}
}
