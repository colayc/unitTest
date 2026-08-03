package coveragemodelv1

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
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

func TestDecodeRejectsMissingNullWrongCaseAndDuplicateStructure(t *testing.T) {
	tests := map[string][]byte{
		"missing nested summary metric": mutatedFixture(t, func(document map[string]any) {
			delete(document["summary"].(map[string]any), "functions")
		}),
		"missing line branches": mutatedFixture(t, func(document map[string]any) {
			file := document["files"].([]any)[0].(map[string]any)
			delete(file["lines"].([]any)[0].(map[string]any), "branches")
		}),
		"missing file summary": mutatedFixture(t, func(document map[string]any) {
			delete(document["files"].([]any)[0].(map[string]any), "summary")
		}),
		"null object": mutatedFixture(t, func(document map[string]any) { document["summary"] = nil }),
		"null files":  mutatedFixture(t, func(document map[string]any) { document["files"] = nil }),
		"null reasons": mutatedFixture(t, func(document map[string]any) {
			document["completeness"].(map[string]any)["reasons"] = nil
		}),
		"wrong case key": []byte(strings.Replace(string(validFixture(t)), `"schemaVersion"`, `"SchemaVersion"`, 1)),
		"duplicate key": []byte(strings.Replace(string(validFixture(t)),
			`"schemaVersion": "1.0",`, `"schemaVersion": "1.0", "schemaVersion": "1.0",`, 1)),
	}
	for name, data := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := Decode(data); !errors.Is(err, ErrInvalidDocument) {
				t.Fatalf("Decode() error = %v, want ErrInvalidDocument", err)
			}
		})
	}
}

func mutatedFixture(t *testing.T, mutate func(map[string]any)) []byte {
	t.Helper()
	var document map[string]any
	if err := json.Unmarshal(validFixture(t), &document); err != nil {
		t.Fatal(err)
	}
	mutate(document)
	data, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	return data
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
