package coveragereport

import (
	"bytes"
	"strings"
	"testing"

	"unit-test-ide.local/test-service/internal/testdomain"
)

func TestJUnitFailClosedSanitizesDiagnosticBypassForms(t *testing.T) {
	input := reportFixture(t)
	results := append([]testdomain.TestItemResult(nil), input.TestRun.Results...)
	results[1].FailureDetails = []testdomain.FailureDetail{{
		Category: "assertion_failure",
		Message: strings.Join([]string{
			"safe diagnostic retained",
			"Authorization: Bearer very-secret-token trailing-secret",
			"run --flag GITHUB_TOKEN=github-secret-value --other hidden-tail",
			`{"token":"json-secret-value","next":"hidden-tail"}`,
			"mixed AuThOrIzAtIoN: BeArEr mixed-secret-value",
			"punctuation (/home/alice/private.cpp) trailing-path-tail",
			`windows (C:\\Users\\Alice\\private.cpp) trailing-windows-tail`,
			"file:///C:/Users/Alice/private.cpp and https://example.invalid/secret?token=url-tail",
		}, "\n"),
		Expected: "ordinary expected text", Actual: "ordinary actual text",
		Locations: []testdomain.SourceLocation{}, EvidenceRefs: []string{},
	}}
	input.TestRun = completedRunWithResults(t, input.TestRun, results)
	set, err := Render(input)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"very-secret-token", "trailing-secret", "github-secret-value", "hidden-tail", "json-secret-value", "mixed-secret-value", "/home/alice/private.cpp", "trailing-path-tail", `C:\Users\Alice`, "trailing-windows-tail", "file:///C:", "https://example.invalid", "url-tail"} {
		if bytes.Contains(bytes.ToLower(set.JUnitXML), []byte(strings.ToLower(forbidden))) {
			t.Fatalf("JUnit leaked %q:\n%s", forbidden, set.JUnitXML)
		}
	}
	for _, expected := range []string{"safe diagnostic retained", "expected: ordinary expected text", "actual: ordinary actual text", "[redacted-sensitive-diagnostic]"} {
		if !bytes.Contains(set.JUnitXML, []byte(expected)) {
			t.Fatalf("JUnit missing %q:\n%s", expected, set.JUnitXML)
		}
	}
}

func TestValidateRejectsQualifiedJUnitGrammar(t *testing.T) {
	set, err := Render(reportFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*Set){
		"declared root prefix": func(value *Set) {
			value.JUnitXML = bytes.Replace(value.JUnitXML, []byte(`<testsuite `), []byte(`<x:testsuite xmlns:x="urn:test" `), 1)
			value.JUnitXML = bytes.Replace(value.JUnitXML, []byte(`</testsuite>`), []byte(`</x:testsuite>`), 1)
		},
		"undeclared root prefix": func(value *Set) {
			value.JUnitXML = bytes.Replace(value.JUnitXML, []byte(`<testsuite `), []byte(`<x:testsuite `), 1)
			value.JUnitXML = bytes.Replace(value.JUnitXML, []byte(`</testsuite>`), []byte(`</x:testsuite>`), 1)
		},
		"declared testcase prefix": func(value *Set) {
			value.JUnitXML = bytes.Replace(value.JUnitXML, []byte(`<testcase `), []byte(`<x:testcase xmlns:x="urn:test" `), 1)
			value.JUnitXML = bytes.Replace(value.JUnitXML, []byte(`</testcase>`), []byte(`</x:testcase>`), 1)
		},
		"undeclared testcase prefix": func(value *Set) {
			value.JUnitXML = bytes.Replace(value.JUnitXML, []byte(`<testcase `), []byte(`<x:testcase `), 1)
			value.JUnitXML = bytes.Replace(value.JUnitXML, []byte(`</testcase>`), []byte(`</x:testcase>`), 1)
		},
		"declared outcome prefix": func(value *Set) {
			value.JUnitXML = bytes.Replace(value.JUnitXML, []byte(`<failure `), []byte(`<x:failure xmlns:x="urn:test" `), 1)
			value.JUnitXML = bytes.Replace(value.JUnitXML, []byte(`</failure>`), []byte(`</x:failure>`), 1)
		},
		"undeclared outcome prefix": func(value *Set) {
			value.JUnitXML = bytes.Replace(value.JUnitXML, []byte(`<failure `), []byte(`<x:failure `), 1)
			value.JUnitXML = bytes.Replace(value.JUnitXML, []byte(`</failure>`), []byte(`</x:failure>`), 1)
		},
	} {
		t.Run(name, func(t *testing.T) {
			copy := cloneSet(set)
			mutate(&copy)
			if err := Validate(copy); err == nil {
				t.Fatal("Validate accepted qualified JUnit grammar")
			}
		})
	}
}
