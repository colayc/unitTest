package coveragemodelv1

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func validFixture(t *testing.T) []byte {
	t.Helper()
	path := filepath.Join("..", "..", "..", "..", "..",
		"packages", "coverage-schema", "fixtures", "v1", "report.valid.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func validDocument(t *testing.T) CoverageDocumentV1 {
	t.Helper()
	value, err := Decode(validFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func TestDecodeValidatesAndClonesCoverageDocument(t *testing.T) {
	value := validDocument(t)
	value.Completeness = CoverageCompletenessV1{Outcome: Partial, Reasons: []Reason{TestCrashed}}
	clone := Clone(value)
	clone.Files[0].URI = "mutated.cpp"
	clone.Files[0].Lines[0].Count = 99
	clone.Completeness.Reasons[0] = TestTimedOut
	if value.Files[0].URI != "src/calculator.cpp" {
		t.Fatalf("original URI mutated: %q", value.Files[0].URI)
	}
	if value.Files[0].Lines[0].Count != 1 {
		t.Fatalf("original line count mutated: %d", value.Files[0].Lines[0].Count)
	}
	if value.Completeness.Reasons[0] != TestCrashed {
		t.Fatalf("original reasons mutated: %v", value.Completeness.Reasons)
	}
}

func TestDecodeRejectsUnknownAndTrailingJSON(t *testing.T) {
	for _, data := range [][]byte{
		append(append([]byte{}, validFixture(t)...), []byte("\n{}")...),
		[]byte(`{"schemaVersion":"1.0","unknown":true}`),
	} {
		if _, err := Decode(data); !errors.Is(err, ErrInvalidDocument) {
			t.Fatalf("Decode() error = %v, want ErrInvalidDocument", err)
		}
	}
}

func TestValidateRejectsSemanticViolations(t *testing.T) {
	tests := map[string]func(*CoverageDocumentV1){
		"covered above total": func(value *CoverageDocumentV1) {
			value.Summary.Lines.Covered = value.Summary.Lines.Total + 1
		},
		"invalid provenance": func(value *CoverageDocumentV1) { value.Provenance.Platform = "darwin" },
		"inconsistent completeness": func(value *CoverageDocumentV1) {
			value.Completeness.Reasons = []Reason{TestCrashed}
		},
		"noncanonical uri": func(value *CoverageDocumentV1) { value.Files[0].URI = "../outside.cpp" },
		"uppercase sha":       func(value *CoverageDocumentV1) { value.Files[0].Sha256 = "A" + value.Files[0].Sha256[1:] },
		"noncanonical lines":  func(value *CoverageDocumentV1) { value.Files[0].Lines[1].Line = value.Files[0].Lines[0].Line },
		"invalid line branches": func(value *CoverageDocumentV1) {
			value.Files[0].Lines[0].Branches.Covered = value.Files[0].Lines[0].Branches.Total + 1
		},
		"file summary mismatch": func(value *CoverageDocumentV1) { value.Files[0].Summary.Lines.Total++ },
		"document summary mismatch": func(value *CoverageDocumentV1) { value.Summary.Functions.Total++ },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			value := validDocument(t)
			mutate(&value)
			if err := Validate(value); !errors.Is(err, ErrInvalidDocument) {
				t.Fatalf("Validate() error = %v, want ErrInvalidDocument", err)
			}
		})
	}
}
