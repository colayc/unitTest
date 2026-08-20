package coveragereport

import (
	"bytes"
	"strings"
	"testing"

	coveragemodelv1 "unit-test-ide.local/test-service/internal/coveragemodel/v1"
	"unit-test-ide.local/test-service/internal/testdomain"
)

func TestJUnitRedactsSensitiveDiagnosticsAndKeepsSafeStructure(t *testing.T) {
	input := reportFixture(t)
	results := append([]testdomain.TestItemResult(nil), input.TestRun.Results...)
	results[1].SourceLocation = &testdomain.SourceLocation{URI: "file:///C:/Users/Alice/private/test.cpp", Line: 41, Column: 7, Navigable: true, Provenance: "test-declaration"}
	results[1].FailureDetails = []testdomain.FailureDetail{{
		Category: "assertion_failure", Subtype: testdomain.FailureSubtypeMockParameterMismatch,
		Message:  "safe assertion\ntoken=very-secret-token argv=--secret C:\\Users\\Alice\\private\\test.cpp /home/alice/private.cpp https://example.invalid/leak",
		Expected: "expected token=very-secret-token", Actual: "actual file:///C:/Users/Alice/private/test.cpp",
		Locations: []testdomain.SourceLocation{{URI: "https://example.invalid/leak", Line: 11, Column: 3, Navigable: true, Provenance: "framework-output"}}, EvidenceRefs: []string{},
	}}
	input.TestRun = completedRunWithResults(t, input.TestRun, results)
	set, err := Render(input)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"very-secret-token", "--secret", `C:\Users\Alice`, "/home/alice", "https://example.invalid", "file:///C:"} {
		if bytes.Contains(set.JUnitXML, []byte(forbidden)) {
			t.Fatalf("JUnit leaked %q:\n%s", forbidden, set.JUnitXML)
		}
	}
	for _, expected := range []string{"safe assertion", "mock_parameter_mismatch", "expected: [redacted-sensitive-diagnostic]", "actual: [redacted-sensitive-diagnostic]", "primary-location: line 41, column 7", "location: line 11, column 3"} {
		if !bytes.Contains(set.JUnitXML, []byte(expected)) {
			t.Fatalf("JUnit missing %q:\n%s", expected, set.JUnitXML)
		}
	}
}

func TestRenderRequiresCompleteConsistentAttachedResults(t *testing.T) {
	for name, mutate := range map[string]func(*testdomain.TestRun){
		"nil results": func(run *testdomain.TestRun) { run.Results = nil },
		"missing result": func(run *testdomain.TestRun) {
			run.Results = run.Results[:len(run.Results)-1]
			run.ResultRevision = mustRevision(t, run.Results)
		},
		"duplicate result": func(run *testdomain.TestRun) {
			run.Results = append(run.Results, run.Results[0])
			run.ResultRevision = mustRevision(t, run.Results)
		},
		"inconsistent summary": func(run *testdomain.TestRun) {
			run.Summary = testdomain.RunSummary{Total: 5, Completed: 4, Passed: 2, Skipped: 1, Errored: 1, NotRun: 1, Iterations: 2}
		},
		"unverified revision": func(run *testdomain.TestRun) { run.Results = nil; run.ResultRevision = strings.Repeat("f", 64) },
	} {
		t.Run(name, func(t *testing.T) {
			input := reportFixture(t)
			mutate(&input.TestRun)
			if _, err := Render(input); err == nil {
				t.Fatal("Render accepted incomplete or inconsistent completed result set")
			}
		})
	}
}

func TestValidateRejectsStructuralAndCrossDocumentArtifacts(t *testing.T) {
	set, err := Render(reportFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	otherInput := reportFixture(t)
	otherInput.Document.Provenance.Compiler.Version = "19.0.0"
	otherInput.CoverageJSON = mustCoverage(t, otherInput.Document)
	other, err := Render(otherInput)
	if err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*Set){
		"xml stylesheet processing instruction": func(value *Set) {
			value.JUnitXML = append([]byte("<?xml-stylesheet href=\"https://example.invalid/report.css\"?>"), value.JUnitXML...)
		},
		"xml unknown child": func(value *Set) {
			value.JUnitXML = []byte(`<?xml version="1.0"?><testsuite name="coverage-test-run" tests="0" failures="0" errors="0" skipped="0"><unknown/></testsuite>`)
		},
		"xml incorrect counts": func(value *Set) {
			value.JUnitXML = bytes.Replace(value.JUnitXML, []byte(`tests="5"`), []byte(`tests="99"`), 1)
		},
		"xml multiple outcomes": func(value *Set) {
			value.JUnitXML = bytes.Replace(value.JUnitXML, []byte(`</failure></testcase>`), []byte(`</failure><skipped message="wrong"></skipped></testcase>`), 1)
			value.JUnitXML = bytes.Replace(value.JUnitXML, []byte(`skipped="2"`), []byte(`skipped="3"`), 1)
		},
		"html meta refresh": func(value *Set) {
			value.CoverageHTML = bytes.Replace(value.CoverageHTML, []byte("</head>"), []byte(`<meta http-equiv="refresh" content="0; url=https://example.invalid/"> </head>`), 1)
		},
		"html external navigation": func(value *Set) {
			value.CoverageHTML = bytes.Replace(value.CoverageHTML, []byte("</body>"), []byte(`<div href="https://example.invalid/">x</div></body>`), 1)
		},
		"html external source": func(value *Set) {
			value.CoverageHTML = bytes.Replace(value.CoverageHTML, []byte("</body>"), []byte(`<video src="https://example.invalid/a"></video></body>`), 1)
		},
		"html style URL": func(value *Set) {
			value.CoverageHTML = bytes.Replace(value.CoverageHTML, []byte("</body>"), []byte(`<div style="background:url(https://example.invalid/a)"></div></body>`), 1)
		},
		"html unknown structure": func(value *Set) {
			value.CoverageHTML = bytes.Replace(value.CoverageHTML, []byte("</body>"), []byte(`<aside>unknown</aside></body>`), 1)
		},
		"html cross document swap": func(value *Set) { value.CoverageHTML = other.CoverageHTML },
	} {
		t.Run(name, func(t *testing.T) {
			copy := cloneSet(set)
			mutate(&copy)
			if err := Validate(copy); err == nil {
				t.Fatal("Validate accepted artifact structural bypass")
			}
		})
	}
}

func completedRunWithResults(t *testing.T, run testdomain.TestRun, results []testdomain.TestItemResult) testdomain.TestRun {
	t.Helper()
	run.Results = results
	run.ResultRevision = mustRevision(t, results)
	validated, err := testdomain.NewTestRun(run)
	if err != nil {
		t.Fatal(err)
	}
	return validated
}

func mustRevision(t *testing.T, results []testdomain.TestItemResult) string {
	t.Helper()
	revision, err := testdomain.ResultRevision(results)
	if err != nil {
		t.Fatal(err)
	}
	return revision
}

func mustCoverage(t *testing.T, document coveragemodelv1.CoverageDocumentV1) []byte {
	t.Helper()
	coverage, err := encodeFixtureDocument(document)
	if err != nil {
		t.Fatal(err)
	}
	return coverage
}
