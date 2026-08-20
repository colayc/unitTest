package coveragereport

import (
	"bytes"
	"strings"
	"testing"
)

func TestJUnitSafelyRepresentsCompletedTestOutcomes(t *testing.T) {
	set, err := Render(reportFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{
		`tests="5"`, `failures="1"`, `errors="1"`, `skipped="2"`,
		`name="utid-v1-` + strings.Repeat("b", 64) + `#2"`,
		`<failure`, `&lt;safe&gt;`, `&amp; wrong`, `primary-location: line 5, column 2`, `mock_parameter_mismatch`, `expected: expected call(1)`, `actual: actual call(2)`, `<error`, `<skipped`,
	} {
		if !bytes.Contains(set.JUnitXML, []byte(expected)) {
			t.Fatalf("JUnit missing %q:\n%s", expected, set.JUnitXML)
		}
	}
	for _, forbidden := range []string{"duration", "2026-", `C:\secret`, "LLVM_PROFILE_FILE", "runId", "token", "environment", "command"} {
		if bytes.Contains(bytes.ToLower(set.JUnitXML), []byte(strings.ToLower(forbidden))) {
			t.Fatalf("JUnit leaked %q:\n%s", forbidden, set.JUnitXML)
		}
	}
}

func TestJUnitIsIndependentOfCoveragePercentageAndBoundsText(t *testing.T) {
	input := reportFixture(t)
	first, err := Render(input)
	if err != nil {
		t.Fatal(err)
	}
	input.Document.Summary.Lines.Covered = 0
	input.Document.Files[0].Summary.Lines.Covered = 0
	input.Document.Files[0].Lines[0].Count = 0
	input.Document.Files[0].Summary.Lines.Total = 2
	input.Document.Summary.Branches.Covered = 0
	input.Document.Files[0].Summary.Branches.Covered = 0
	input.Document.Files[0].Lines[0].Branches.Covered = 0
	coverage, err := encodeFixtureDocument(input.Document)
	if err != nil {
		t.Fatal(err)
	}
	input.CoverageJSON = coverage
	second, err := Render(input)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first.JUnitXML, second.JUnitXML) {
		t.Fatal("coverage changed JUnit outcome")
	}
}
