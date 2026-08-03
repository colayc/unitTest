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
	return coverageFixture(t, "report.valid.json")
}

func coverageFixture(t *testing.T, name string) []byte {
	t.Helper()
	path := filepath.Join("..", "..", "..", "..", "..",
		"packages", "coverage-schema", "fixtures", "v1", name)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestDecodeRejectsSharedInvalidFixtures(t *testing.T) {
	for _, name := range []string{
		"report-native-path.invalid.json",
		"report-float.invalid.json",
		"report-unsafe-count.invalid.json",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Decode(coverageFixture(t, name)); !errors.Is(err, ErrInvalidDocument) {
				t.Fatalf("Decode() error = %v, want ErrInvalidDocument", err)
			}
		})
	}
}

func TestDecodeAcceptsIntegerValuedDecimalAndExponentNumbers(t *testing.T) {
	for _, lexeme := range []string{"1.0", "1e0"} {
		t.Run(lexeme, func(t *testing.T) {
			value, err := Decode(replaceFixtureNumber(t, "1", lexeme))
			if err != nil {
				t.Fatal(err)
			}
			if value.Summary.Lines.Covered != 1 {
				t.Fatalf("summary.lines.covered = %d, want 1", value.Summary.Lines.Covered)
			}
		})
	}
}

func TestDecodeRejectsNonIntegerAndUnsafeNumberLexemes(t *testing.T) {
	for _, lexeme := range []string{"1.5", "1e100", "-1", "NaN"} {
		t.Run(lexeme, func(t *testing.T) {
			if _, err := Decode(replaceFixtureNumber(t, "1", lexeme)); !errors.Is(err, ErrInvalidDocument) {
				t.Fatalf("Decode() error = %v, want ErrInvalidDocument", err)
			}
		})
	}
}

func replaceFixtureNumber(t *testing.T, original, replacement string) []byte {
	t.Helper()
	old := `"covered": ` + original + `, "total": 2`
	updated := strings.Replace(string(validFixture(t)), old,
		`"covered": `+replacement+`, "total": 2`, 1)
	if updated == string(validFixture(t)) {
		t.Fatalf("fixture did not contain %q", old)
	}
	return []byte(updated)
}

func TestDecodeEnforcesMultibyteUTF8ByteLimits(t *testing.T) {
	versionSetters := map[string]func(map[string]any, string){
		"compiler.version": func(document map[string]any, value string) {
			document["provenance"].(map[string]any)["compiler"].(map[string]any)["version"] = value
		},
		"driver.version": func(document map[string]any, value string) {
			document["provenance"].(map[string]any)["driver"].(map[string]any)["version"] = value
		},
		"collector.version": func(document map[string]any, value string) {
			document["provenance"].(map[string]any)["collector"].(map[string]any)["version"] = value
		},
		"normalizerVersion": func(document map[string]any, value string) {
			document["provenance"].(map[string]any)["normalizerVersion"] = value
		},
	}
	for name, setVersion := range versionSetters {
		t.Run(name, func(t *testing.T) {
			valid := mutatedFixture(t, func(document map[string]any) {
				setVersion(document, strings.Repeat("é", 64))
			})
			if _, err := Decode(valid); err != nil {
				t.Fatalf("128-byte version: %v", err)
			}
			invalid := mutatedFixture(t, func(document map[string]any) {
				setVersion(document, strings.Repeat("é", 65))
			})
			if _, err := Decode(invalid); !errors.Is(err, ErrInvalidDocument) {
				t.Fatalf("130-byte version error = %v, want ErrInvalidDocument", err)
			}
		})
	}

	validURI := mutatedFixture(t, func(document map[string]any) {
		document["files"].([]any)[0].(map[string]any)["uri"] = strings.Repeat("é", 2046) + ".cpp"
	})
	if _, err := Decode(validURI); err != nil {
		t.Fatalf("4096-byte URI: %v", err)
	}
	invalidURI := mutatedFixture(t, func(document map[string]any) {
		document["files"].([]any)[0].(map[string]any)["uri"] = strings.Repeat("é", 2047) + ".cpp"
	})
	if _, err := Decode(invalidURI); !errors.Is(err, ErrInvalidDocument) {
		t.Fatalf("4098-byte URI error = %v, want ErrInvalidDocument", err)
	}
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
