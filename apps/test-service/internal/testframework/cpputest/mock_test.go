package cpputest

import (
	"strings"
	"testing"

	"unit-test-ide.local/test-service/internal/testdomain"
	"unit-test-ide.local/test-service/internal/testframework"
)

func TestParserNormalizesCppUMockFailureDetails(t *testing.T) {
	tests := map[string]struct {
		fixture       string
		name          string
		wantSubtype   testdomain.FailureSubtype
		wantExpected  string
		wantActual    string
		wantLocations int
	}{
		"unexpected call": {
			fixture:       "mock-unexpected-call.txt",
			name:          "unexpectedCall",
			wantSubtype:   testdomain.FailureSubtypeMockUnexpectedCall,
			wantActual:    "dependency_read",
			wantLocations: 1,
		},
		"missing call": {
			fixture:       "mock-missing-call.txt",
			name:          "missingCall",
			wantSubtype:   testdomain.FailureSubtypeMockMissingCall,
			wantExpected:  "dependency_write -> int value: <7 (0x7)>",
			wantLocations: 1,
		},
		"parameter mismatch": {
			fixture:       "mock-parameter-mismatch.txt",
			name:          "parameterMismatch",
			wantSubtype:   testdomain.FailureSubtypeMockParameterMismatch,
			wantExpected:  "dependency_write -> int value: <7 (0x7)>",
			wantActual:    "int value: <20 (0x14)>",
			wantLocations: 2,
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			item := parserItem(t, "MockGroup", test.name)
			parser := newResultParser(
				t,
				[]testframework.RunItem{item},
				DefaultResultLimits(),
			)
			feedFixture(t, parser, test.fixture)
			result, err := parser.Finish(testframework.ProcessResult{
				ExitCode:    1,
				Termination: testframework.ProcessExited,
			})
			if err != nil {
				t.Fatal(err)
			}
			if !result.Complete || len(result.Cases) != 1 {
				t.Fatalf("result = %#v", result)
			}
			failure := result.Cases[0]
			if failure.Status != testframework.CaseFailed ||
				failure.Category != "assertion_failure" ||
				len(failure.FailureDetails) != 1 {
				t.Fatalf("failure = %#v", failure)
			}
			detail := failure.FailureDetails[0]
			if detail.Category != "assertion_failure" ||
				detail.Subtype != test.wantSubtype ||
				!strings.Contains(detail.Message, "Mock Failure") ||
				!strings.Contains(detail.Expected, test.wantExpected) ||
				!strings.Contains(detail.Actual, test.wantActual) ||
				len(detail.Locations) != test.wantLocations {
				t.Fatalf("detail = %#v", detail)
			}
			if test.wantLocations == 2 {
				if detail.Locations[0].Path != `C:\workspace\tests\mock_tests.cpp` ||
					detail.Locations[0].Provenance != "test-declaration" ||
					detail.Locations[1].Path != `C:\workspace\src\controller.cpp` ||
					detail.Locations[1].Provenance != "mock-actual-call" {
					t.Fatalf("locations = %#v", detail.Locations)
				}
			}
		})
	}
}

func TestParserKeepsUnknownCppUMockVariantAsAssertion(t *testing.T) {
	item := parserItem(t, "MockGroup", "futureVariant")
	parser := newResultParser(
		t,
		[]testframework.RunItem{item},
		DefaultResultLimits(),
	)
	output := strings.Join([]string{
		"TEST(MockGroup, futureVariant)",
		"/workspace/tests/mock_tests.cpp:80: error: Failure in TEST(MockGroup, futureVariant)",
		"\tMock Failure: New upstream detail shape",
		"\topaque detail that must be preserved",
		"",
		" - 1 ms",
		"",
		"Errors (1 failures, 1 tests, 1 ran, 0 checks, 0 ignored, 0 filtered out, 1 ms)",
		"",
	}, "\n")
	if _, err := parser.Feed(testframework.StreamStdout, []byte(output)); err != nil {
		t.Fatal(err)
	}
	result, err := parser.Finish(testframework.ProcessResult{
		ExitCode:    1,
		Termination: testframework.ProcessExited,
	})
	if err != nil {
		t.Fatal(err)
	}
	failure := result.Cases[0]
	if failure.Status != testframework.CaseFailed ||
		!strings.Contains(failure.Message, "opaque detail") ||
		len(failure.FailureDetails) != 1 ||
		failure.FailureDetails[0].Subtype != testdomain.FailureSubtypeMockFailure ||
		!strings.Contains(failure.FailureDetails[0].Message, "opaque detail") {
		t.Fatalf("failure = %#v", failure)
	}
}

func TestCppUMockAddressesAndANSIDoNotAffectItemIdentity(t *testing.T) {
	item := parserItem(t, "MockGroup", "unexpectedObject")
	var ids []testdomain.ID
	for _, address := range []string{"0x00000001", "0xDEADBEEF"} {
		parser := newResultParser(
			t,
			[]testframework.RunItem{item},
			DefaultResultLimits(),
		)
		output := strings.Join([]string{
			"\x1b[31mTEST(MockGroup, unexpectedObject)\x1b[0m",
			"/workspace/tests/mock_tests.cpp:90: error: Failure in TEST(MockGroup, unexpectedObject)",
			"\tMockFailure: Function called on an unexpected object: dependency_read",
			"\tActual object for call has address: <" + address + ">",
			"",
			" - 1 ms",
			"",
			"Errors (1 failures, 1 tests, 1 ran, 0 checks, 0 ignored, 0 filtered out, 1 ms)",
			"",
		}, "\n")
		if _, err := parser.Feed(testframework.StreamStdout, []byte(output)); err != nil {
			t.Fatal(err)
		}
		result, err := parser.Finish(testframework.ProcessResult{
			ExitCode:    1,
			Termination: testframework.ProcessExited,
		})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, result.Cases[0].ItemID)
	}
	if ids[0] != item.ItemID || ids[1] != item.ItemID {
		t.Fatalf("item IDs = %#v, want stable %q", ids, item.ItemID)
	}
}

func TestCppUMockResultEventsAreDefensiveCopies(t *testing.T) {
	item := parserItem(t, "MockGroup", "unexpectedCall")
	parser := newResultParser(
		t,
		[]testframework.RunItem{item},
		DefaultResultLimits(),
	)
	events := feedFixture(t, parser, "mock-unexpected-call.txt")
	if len(events) != 1 ||
		len(events[0].Case.FailureDetails) != 1 ||
		len(events[0].Case.FailureDetails[0].Locations) != 1 {
		t.Fatalf("events = %#v", events)
	}
	events[0].Case.FailureDetails[0].Locations[0].Path = "mutated"
	result, err := parser.Finish(testframework.ProcessResult{
		ExitCode:    1,
		Termination: testframework.ProcessExited,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Cases[0].FailureDetails[0].Locations[0].Path !=
		"/workspace/tests/mock_tests.cpp" {
		t.Fatalf("result was mutated through event: %#v", result.Cases[0])
	}
}
