package coveragenormalize

import (
	"encoding/json"
	"fmt"
	"reflect"

	coveragemodelv1 "unit-test-ide.local/test-service/internal/coveragemodel/v1"
)

// EncodeCanonical validates, marshals in generated struct field order, adds
// exactly one LF, and proves the bytes decode to a deep-equal document.
func EncodeCanonical(value coveragemodelv1.CoverageDocumentV1) ([]byte, error) {
	if err := coveragemodelv1.Validate(value); err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("canonical Coverage JSON marshal: %w", err)
	}
	encoded = append(encoded, '\n')
	decoded, err := coveragemodelv1.Decode(encoded)
	if err != nil {
		return nil, fmt.Errorf("canonical Coverage JSON round trip: %w", err)
	}
	if !reflect.DeepEqual(decoded, value) {
		return nil, fmt.Errorf("canonical Coverage JSON round trip: value changed")
	}
	return encoded, nil
}
