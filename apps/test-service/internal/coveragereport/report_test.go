package coveragereport

import (
	"bytes"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"unit-test-ide.local/test-service/internal/coveragedomain"
	coveragemodelv1 "unit-test-ide.local/test-service/internal/coveragemodel/v1"
	"unit-test-ide.local/test-service/internal/coveragenormalize"
	"unit-test-ide.local/test-service/internal/testdomain"
)

func TestRenderMatchesGoldenAndReturnsImmutableSet(t *testing.T) {
	input := reportFixture(t)
	first, err := Render(input)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Render(input)
	if err != nil {
		t.Fatal(err)
	}
	for _, artifact := range []struct {
		name string
		got  []byte
		path string
	}{
		{"JUnit", first.JUnitXML, "testdata/junit.golden.xml"},
		{"HTML", first.CoverageHTML, "testdata/report.golden.html"},
	} {
		golden, readErr := os.ReadFile(artifact.path)
		if readErr != nil {
			t.Fatal(readErr)
		}
		if !bytes.Equal(artifact.got, golden) {
			t.Fatalf("%s output differs from golden (got=%d, golden=%d):\n%s", artifact.name, len(artifact.got), len(golden), artifact.got)
		}
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("Render is not deterministic\nfirst=%#v\nsecond=%#v", first, second)
	}
	if err := Validate(first); err != nil {
		t.Fatalf("Validate(Render()) = %v", err)
	}
	first.CoverageJSON[0] = 'X'
	if bytes.Equal(first.CoverageJSON, second.CoverageJSON) {
		t.Fatal("rendered set shares mutable artifact bytes")
	}
	if first.Sources[0].URI == "" || first.Sources[0].SHA256 == "" || first.Sources[0].URI != input.Sources[0].URI {
		t.Fatalf("sources = %#v", first.Sources)
	}
}

func TestRenderRejectsMismatchedCoverageDocumentAndIncompleteRun(t *testing.T) {
	input := reportFixture(t)
	input.Document.Summary.Lines.Covered = 0
	if _, err := Render(input); err == nil {
		t.Fatal("Render accepted JSON that does not equal Document")
	}
	input = reportFixture(t)
	input.TestRun.Status = testdomain.RunRunning
	if _, err := Render(input); err == nil {
		t.Fatal("Render accepted incomplete test run")
	}
}

func TestValidateRejectsMalformedOrNonImmutableSet(t *testing.T) {
	set, err := Render(reportFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*Set){
		"missing junit": func(value *Set) { value.JUnitXML = nil },
		"xml doctype":   func(value *Set) { value.JUnitXML = []byte("<!DOCTYPE x [<!ENTITY x 'y'>]><testsuites/>") },
		"html network": func(value *Set) {
			value.CoverageHTML = []byte("<!doctype html><html><head><meta http-equiv=\"Content-Security-Policy\" content=\"default-src 'none'\"></head><body><img src=\"https://example.invalid/x\"></body></html>")
		},
		"json mismatch": func(value *Set) { value.CoverageJSON = []byte("{}") },
	} {
		t.Run(name, func(t *testing.T) {
			copy := Set{CoverageJSON: append([]byte(nil), set.CoverageJSON...), JUnitXML: append([]byte(nil), set.JUnitXML...), CoverageHTML: append([]byte(nil), set.CoverageHTML...), Summary: set.Summary, Sources: append([]coveragedomain.SourceSnapshot(nil), set.Sources...)}
			mutate(&copy)
			if err := Validate(copy); err == nil {
				t.Fatal("Validate accepted malformed set")
			}
		})
	}
}

func reportFixture(t *testing.T) Input {
	t.Helper()
	document := coveragemodelv1.CoverageDocumentV1{
		SchemaVersion: coveragemodelv1.The10,
		Provenance: coveragemodelv1.CoverageProvenanceV1{
			Platform: coveragemodelv1.Windows, Architecture: coveragemodelv1.X64,
			Compiler:          coveragemodelv1.CoverageCompilerV1{Family: coveragemodelv1.ClangCl, Version: "18.1.8"},
			Driver:            coveragemodelv1.CoverageDriverV1{Name: coveragemodelv1.FluffyLlvmCov, Version: "18.1.8"},
			Collector:         coveragemodelv1.CoverageCollectorV1{Name: coveragemodelv1.PurpleLlvmCov, Version: "18.1.8"},
			NormalizerVersion: "1.0.0", InstrumentationFingerprint: strings.Repeat("a", 64),
		},
		Completeness: coveragemodelv1.CoverageCompletenessV1{Outcome: coveragemodelv1.Available, Reasons: []coveragemodelv1.Reason{}},
		Summary:      coveragemodelv1.CoverageSummaryV1{Lines: coveragemodelv1.CoverageMetricV1{Covered: 1, Total: 2}, Branches: coveragemodelv1.CoverageMetricV1{Covered: 1, Total: 2}, Functions: coveragemodelv1.CoverageMetricV1{Covered: 1, Total: 1}},
		Files:        []coveragemodelv1.CoverageFileV1{{URI: "src/example.cpp", Sha256: strings.Repeat("b", 64), Summary: coveragemodelv1.CoverageSummaryV1{Lines: coveragemodelv1.CoverageMetricV1{Covered: 1, Total: 2}, Branches: coveragemodelv1.CoverageMetricV1{Covered: 1, Total: 2}, Functions: coveragemodelv1.CoverageMetricV1{Covered: 1, Total: 1}}, Lines: []coveragemodelv1.CoverageLineV1{{Line: 4, Count: 1, Branches: coveragemodelv1.CoverageMetricV1{Covered: 1, Total: 2}}, {Line: 5, Count: 0, Branches: coveragemodelv1.CoverageMetricV1{}}}}},
	}
	coverage, err := coveragenormalize.EncodeCanonical(document)
	if err != nil {
		t.Fatal(err)
	}
	items := []testdomain.TestItemResult{
		{ItemID: testID("a"), ContainerID: testID("c"), Iteration: 1, Outcome: testdomain.ItemPassed, FailureDetails: []testdomain.FailureDetail{}, OutputRefs: []string{}},
		{ItemID: testID("b"), ContainerID: testID("c"), Iteration: 1, Outcome: testdomain.ItemFailed, FailureDetails: []testdomain.FailureDetail{{Category: "assertion_failure", Message: "want <safe>, got & wrong", Locations: []testdomain.SourceLocation{{URI: "file:///workspace/src/example.cpp", Line: 5, Column: 2, Navigable: true, Provenance: "framework-output"}}, EvidenceRefs: []string{strings.Repeat("d", 32)}}}, OutputRefs: []string{}},
		{ItemID: testID("b"), ContainerID: testID("c"), Iteration: 2, Outcome: testdomain.ItemSkipped, FailureDetails: []testdomain.FailureDetail{}, OutputRefs: []string{}},
		{ItemID: testID("d"), ContainerID: testID("c"), Iteration: 1, Outcome: testdomain.ItemErrored, FailureDetails: []testdomain.FailureDetail{{Category: "test_process_crash", Message: "crash\u2028diagnostic", EvidenceRefs: []string{}}}, OutputRefs: []string{}},
		{ItemID: testID("e"), ContainerID: testID("c"), Iteration: 1, Outcome: testdomain.ItemNotRun, Reason: testdomain.ReasonBuildBlocked, FailureDetails: []testdomain.FailureDetail{}, OutputRefs: []string{}},
	}
	revision, err := testdomain.ResultRevision(items)
	if err != nil {
		t.Fatal(err)
	}
	finished := time.Date(2026, 8, 20, 1, 2, 3, 0, time.UTC)
	run, err := testdomain.NewTestRun(testdomain.TestRun{RunID: strings.Repeat("1", 32), TaskID: strings.Repeat("2", 32), IdempotencyKey: strings.Repeat("3", 32), ProjectID: "core", ProfileID: strings.Repeat("4", 64), ToolchainID: "clang-cl", CatalogRevision: strings.Repeat("5", 64), SelectionSnapshot: testdomain.SelectionSnapshot{Mode: testdomain.SelectionItems, ItemIDs: []testdomain.ID{testID("a"), testID("b"), testID("d"), testID("e")}}, Status: testdomain.RunCompleted, Outcome: testdomain.RunFailed, FinishedAt: &finished, Summary: testdomain.RunSummary{Total: 5, Completed: 4, Passed: 1, Failed: 1, Skipped: 1, Errored: 1, NotRun: 1, Iterations: 2}, ResultRevision: revision, Incomplete: true, CreatedAt: finished.Add(-time.Minute), Results: items})
	if err != nil {
		t.Fatal(err)
	}
	return Input{CoverageJSON: coverage, Document: document, TestRun: run, Sources: []coveragenormalize.SourceBinding{{URI: "src/example.cpp", SHA256: strings.Repeat("b", 64), NativePath: `C:\secret\example.cpp`}}}
}

func testID(char string) testdomain.ID { return testdomain.ID("utid-v1-" + strings.Repeat(char, 64)) }

func encodeFixtureDocument(document coveragemodelv1.CoverageDocumentV1) ([]byte, error) {
	return coveragenormalize.EncodeCanonical(document)
}
