package coveragedomain

import "testing"

func TestFunctionObservationExactRangeRequiresBothLocations(t *testing.T) {
	value := FunctionObservation{Start: SourceLocation{Line: 4, Column: 2}}
	if value.HasExactRange() {
		t.Fatal("a start line alone cannot imply an exact function range")
	}
	value.End = SourceLocation{Line: 5, Column: 1}
	if !value.HasExactRange() {
		t.Fatal("explicit source endpoints should make an exact range")
	}
	value.End.Line = MaxSafeInteger + 1
	if value.HasExactRange() {
		t.Fatal("unsafe endpoint must not be considered exact")
	}
}
