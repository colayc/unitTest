package unity

import (
	"strings"
	"testing"

	"unit-test-ide.local/test-service/internal/testdomain"
	"unit-test-ide.local/test-service/internal/testframework"
)

func TestCMockFailureEnrichesControlFileAssertion(t *testing.T) {
	fixture := newAdapterFixture(t)
	parser := parserForCase(t, fixture, 0)
	if events, err := parser.Feed(
		testframework.StreamStdout,
		readUnityTestdata(t, "cmock-fail.txt"),
	); err != nil || len(events) != 0 {
		t.Fatalf("stdout Feed() = %#v, %v", events, err)
	}
	if _, err := parser.Feed(
		testframework.StreamControl,
		readUnityTestdata(t, "fail.jsonl"),
	); err != nil {
		t.Fatal(err)
	}
	result, err := parser.Finish(testframework.ProcessResult{
		ExitCode: 1, Termination: testframework.ProcessExited,
	})
	if err != nil || !result.Complete || len(result.Cases) != 1 {
		t.Fatalf("Finish() = %#v, %v", result, err)
	}
	testCase := result.Cases[0]
	if testCase.Status != testframework.CaseFailed ||
		testCase.Message == "Unity assertion failed; see stdout/stderr" ||
		len(testCase.FailureDetails) != 1 {
		t.Fatalf("case = %#v", testCase)
	}
	detail := testCase.FailureDetails[0]
	if detail.Subtype != testdomain.FailureSubtypeMockParameterMismatch ||
		detail.Expected != "7" ||
		detail.Actual != "20" ||
		len(detail.Locations) != 1 ||
		detail.Locations[0].Path != "testdata/basic.c" ||
		detail.Locations[0].Line != 18 ||
		detail.Locations[0].Provenance != "framework-output" {
		t.Fatalf("detail = %#v", detail)
	}
}

func TestParseCMockFailureClassifiesExpectationKinds(t *testing.T) {
	tests := []struct {
		name         string
		message      string
		wantSubtype  testdomain.FailureSubtype
		wantExpected string
		wantActual   string
	}{
		{
			name:        "unexpected call",
			message:     "Function dependency_read. Called more times than expected.",
			wantSubtype: testdomain.FailureSubtypeMockUnexpectedCall,
			wantActual:  "dependency_read",
		},
		{
			name:         "missing call",
			message:      "Function dependency_write. Called fewer times than expected.",
			wantSubtype:  testdomain.FailureSubtypeMockMissingCall,
			wantExpected: "dependency_write",
		},
		{
			name: "parameter mismatch",
			message: "Expected 7 Was 20. Function dependency_write Argument value. " +
				"Function called with unexpected value.",
			wantSubtype:  testdomain.FailureSubtypeMockParameterMismatch,
			wantExpected: "7",
			wantActual:   "20",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			subtype, expected, actual, ok := parseCMockFailure(test.message)
			if !ok || subtype != test.wantSubtype ||
				expected != test.wantExpected || actual != test.wantActual {
				t.Fatalf(
					"parseCMockFailure() = (%q, %q, %q, %t)",
					subtype,
					expected,
					actual,
					ok,
				)
			}
		})
	}
}

func TestControlConsistencyRejectsRecognizedContradictoryOutput(t *testing.T) {
	tests := []struct {
		name          string
		controlStatus testframework.CaseStatus
		exitCode      int
		output        string
	}{
		{
			name:          "control passed stdout failed",
			controlStatus: testframework.CasePassed,
			exitCode:      0,
			output: "testdata/basic.c:18:test_adds_numbers:FAIL:" +
				"Expected 1 Was 2\n",
		},
		{
			name:          "control failed stdout passed",
			controlStatus: testframework.CaseFailed,
			exitCode:      1,
			output:        "testdata/basic.c:16:test_adds_numbers:PASS\n",
		},
		{
			name:          "different case identity",
			controlStatus: testframework.CaseFailed,
			exitCode:      1,
			output: "testdata/basic.c:18:test_handles_zero:FAIL:" +
				"Expected 1 Was 2\n",
		},
		{
			name:          "external source path",
			controlStatus: testframework.CaseFailed,
			exitCode:      1,
			output: "outside.c:18:test_adds_numbers:FAIL:" +
				"Expected 1 Was 2\n",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newAdapterFixture(t)
			parser := parserForCase(t, fixture, 0)
			if _, err := parser.Feed(
				testframework.StreamStdout,
				[]byte(test.output),
			); err != nil {
				t.Fatal(err)
			}
			if _, err := parser.Feed(
				testframework.StreamControl,
				runRecord(
					t,
					fixture.manifest,
					fixture.manifest.Cases[0],
					test.controlStatus,
				),
			); err != nil {
				t.Fatal(err)
			}
			result, err := parser.Finish(testframework.ProcessResult{
				ExitCode:    test.exitCode,
				Termination: testframework.ProcessExited,
			})
			if err != nil || result.Complete ||
				result.Cases[0].Status != test.controlStatus ||
				!result.Cases[0].Partial ||
				!hasDiagnostic(
					result.Diagnostics,
					"framework_output_invalid",
				) {
				t.Fatalf("Finish() = %#v, %v", result, err)
			}
		})
	}
}

func TestMalformedStdoutNeverOverridesControlStatus(t *testing.T) {
	fixture := newAdapterFixture(t)
	parser := parserForCase(t, fixture, 0)
	malformed := []byte(strings.Join([]string{
		"arbitrary Project log",
		"{not-json}",
		"testdata/basic.c:not-a-line:test_adds_numbers:FAIL:bad",
		"testdata/basic.c:16:test_adds_numbers:UNKNOWN",
		"",
	}, "\n"))
	if _, err := parser.Feed(
		testframework.StreamStdout,
		malformed,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := parser.Feed(
		testframework.StreamControl,
		runRecord(
			t,
			fixture.manifest,
			fixture.manifest.Cases[0],
			testframework.CasePassed,
		),
	); err != nil {
		t.Fatal(err)
	}
	result, err := parser.Finish(testframework.ProcessResult{
		ExitCode: 0, Termination: testframework.ProcessExited,
	})
	if err != nil || !result.Complete ||
		result.Cases[0].Status != testframework.CasePassed {
		t.Fatalf("Finish() = %#v, %v", result, err)
	}
}

func hasDiagnostic(
	diagnostics []testdomain.Diagnostic,
	category string,
) bool {
	for _, diagnostic := range diagnostics {
		if diagnostic.Category == category {
			return true
		}
	}
	return false
}
