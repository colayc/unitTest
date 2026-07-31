package cpputest

import (
	"errors"
	"os"
	"strings"
	"testing"

	"unit-test-ide.local/test-service/internal/testdomain"
	"unit-test-ide.local/test-service/internal/testframework"
)

func TestParserAcceptsPassFailAndIgnoredGoldenOutput(t *testing.T) {
	tests := map[string]struct {
		fixture     string
		items       []testframework.RunItem
		exitCode    int
		wantStatus  []testframework.CaseStatus
		wantMessage string
	}{
		"pass": {
			fixture: "pass.txt",
			items: []testframework.RunItem{
				parserItem(t, "Core", "passes"),
				parserItem(t, "Unicode_组", "案例_一"),
			},
			wantStatus: []testframework.CaseStatus{
				testframework.CasePassed,
				testframework.CasePassed,
			},
		},
		"assertion failure": {
			fixture:     "fail.txt",
			items:       []testframework.RunItem{parserItem(t, "Core", "fails")},
			exitCode:    1,
			wantStatus:  []testframework.CaseStatus{testframework.CaseFailed},
			wantMessage: "Expected <1>",
		},
		"ignored": {
			fixture:    "ignored.txt",
			items:      []testframework.RunItem{parserItem(t, "Core", "ignored")},
			wantStatus: []testframework.CaseStatus{testframework.CaseSkipped},
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			parser := newResultParser(t, test.items, DefaultResultLimits())
			events := feedFixture(t, parser, test.fixture)
			result, err := parser.Finish(testframework.ProcessResult{
				ExitCode:    test.exitCode,
				Termination: testframework.ProcessExited,
			})
			if err != nil {
				t.Fatal(err)
			}
			if !result.Complete || len(result.Diagnostics) != 0 {
				t.Fatalf("result = %#v, want complete result without diagnostics", result)
			}
			if len(events) != len(test.wantStatus) ||
				len(result.Cases) != len(test.wantStatus) {
				t.Fatalf("events/cases = %d/%d, want %d", len(events), len(result.Cases), len(test.wantStatus))
			}
			for index, want := range test.wantStatus {
				if events[index].Case.Status != want ||
					result.Cases[index].Status != want ||
					result.Cases[index].Partial {
					t.Fatalf("case %d = %#v, want status %q", index, result.Cases[index], want)
				}
			}
			if test.wantMessage != "" &&
				!strings.Contains(result.Cases[0].Message, test.wantMessage) {
				t.Fatalf("message = %q, want %q", result.Cases[0].Message, test.wantMessage)
			}
		})
	}
}

func TestParserExtractsAssertionAndMemoryLeakEvidence(t *testing.T) {
	item := parserItem(t, "Core", "fails")
	parser := newResultParser(t, []testframework.RunItem{item}, DefaultResultLimits())
	feedFixture(t, parser, "fail.txt")
	result, err := parser.Finish(testframework.ProcessResult{
		ExitCode:    1,
		Termination: testframework.ProcessExited,
	})
	if err != nil {
		t.Fatal(err)
	}
	failure := result.Cases[0]
	if failure.Category != "assertion_failure" ||
		failure.SourceLocation == nil ||
		failure.SourceLocation.Path != `C:\work\source\core_test.cpp` ||
		failure.SourceLocation.Line != 42 {
		t.Fatalf("failure evidence = %#v", failure)
	}

	parser = newResultParser(t, []testframework.RunItem{item}, DefaultResultLimits())
	memoryLeak := strings.Join([]string{
		"TEST(Core, fails)",
		"/workspace/core_test.cpp:51: error: Failure in TEST(Core, fails)",
		"\tMemory leak(s) found.",
		"\tAlloc num (4) Leak size: 32",
		"",
		" - 1 ms",
		"",
		"Errors (1 failures, 1 tests, 1 ran, 0 checks, 0 ignored, 0 filtered out, 1 ms)",
		"",
	}, "\n")
	if _, err := parser.Feed(testframework.StreamStdout, []byte(memoryLeak)); err != nil {
		t.Fatal(err)
	}
	result, err = parser.Finish(testframework.ProcessResult{
		ExitCode:    1,
		Termination: testframework.ProcessExited,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Complete ||
		result.Cases[0].Category != "assertion_failure" ||
		!strings.Contains(result.Cases[0].Message, "Memory leak(s) found.") {
		t.Fatalf("memory leak result = %#v", result)
	}
}

func TestParserRequiresCompleteRecordBeforeTerminalEvent(t *testing.T) {
	parser := newResultParser(
		t,
		[]testframework.RunItem{parserItem(t, "Core", "passes")},
		DefaultResultLimits(),
	)
	events, err := parser.Feed(
		testframework.StreamStdout,
		[]byte("TEST(Core, passes) - 2"),
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 0 {
		t.Fatalf("events before complete record = %#v", events)
	}
	events, err = parser.Feed(testframework.StreamStdout, []byte(" ms\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Case.Status != testframework.CasePassed {
		t.Fatalf("events after complete record = %#v", events)
	}
}

func TestParserHandlesANSI_CRLFChunksUTF8AndInterleavedStderr(t *testing.T) {
	items := []testframework.RunItem{
		parserItem(t, "Core", "passes"),
		parserItem(t, "Unicode_组", "案例_一"),
	}
	data := readFixture(t, "pass.txt")
	decorated := strings.ReplaceAll(string(data), "\n", "\r\n")
	decorated = strings.Replace(
		decorated,
		"TEST(Core, passes)",
		"\x1b[32mTEST(Core, passes)\x1b[0m",
		1,
	)
	for width := 1; width <= len([]byte(decorated)); width++ {
		parser := newResultParser(t, items, DefaultResultLimits())
		var events []testframework.ResultEvent
		payload := []byte(decorated)
		for offset := 0; offset < len(payload); offset += width {
			end := offset + width
			if end > len(payload) {
				end = len(payload)
			}
			got, err := parser.Feed(testframework.StreamStdout, payload[offset:end])
			if err != nil {
				t.Fatalf("width %d: %v", width, err)
			}
			events = append(events, got...)
			if offset == 0 {
				if _, err := parser.Feed(
					testframework.StreamStderr,
					[]byte("sanitizer note that is not CppUTest grammar\n"),
				); err != nil {
					t.Fatalf("width %d stderr: %v", width, err)
				}
			}
		}
		result, err := parser.Finish(testframework.ProcessResult{
			Termination: testframework.ProcessExited,
		})
		if err != nil {
			t.Fatalf("width %d: %v", width, err)
		}
		if len(events) != 2 || !result.Complete {
			t.Fatalf("width %d: events/result = %#v / %#v", width, events, result)
		}
	}
}

func TestParserRejectsSummaryAndExitEvidenceMismatch(t *testing.T) {
	passItem := parserItem(t, "Core", "passes")
	failItem := parserItem(t, "Core", "fails")
	tests := map[string]struct {
		items        []testframework.RunItem
		output       string
		process      testframework.ProcessResult
		wantCategory string
	}{
		"malformed summary": {
			items:        []testframework.RunItem{passItem},
			output:       string(readFixture(t, "malformed-summary.txt")),
			process:      testframework.ProcessResult{Termination: testframework.ProcessExited},
			wantCategory: "framework_output_invalid",
		},
		"missing summary": {
			items:        []testframework.RunItem{passItem},
			output:       "TEST(Core, passes) - 2 ms\n",
			process:      testframework.ProcessResult{Termination: testframework.ProcessExited},
			wantCategory: "framework_output_invalid",
		},
		"nonzero without assertion": {
			items:        []testframework.RunItem{passItem},
			output:       "TEST(Core, passes)",
			process:      testframework.ProcessResult{ExitCode: 7, Termination: testframework.ProcessExited},
			wantCategory: "unexpected_exit",
		},
		"zero with assertion": {
			items:        []testframework.RunItem{failItem},
			output:       string(readFixture(t, "fail.txt")),
			process:      testframework.ProcessResult{Termination: testframework.ProcessExited},
			wantCategory: "inconsistent_exit_status",
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			parser := newResultParser(t, test.items, DefaultResultLimits())
			if _, err := parser.Feed(testframework.StreamStdout, []byte(test.output)); err != nil {
				t.Fatal(err)
			}
			result, err := parser.Finish(test.process)
			if err != nil {
				t.Fatal(err)
			}
			if result.Complete || !hasDiagnostic(result, test.wantCategory) {
				t.Fatalf("result = %#v, want incomplete with %q", result, test.wantCategory)
			}
			for _, item := range result.Cases {
				if !item.Partial {
					t.Fatalf("partial case = %#v", item)
				}
			}
		})
	}
}

func TestParserPreservesCompletedCasesAndMarksRemainingNotRun(t *testing.T) {
	items := []testframework.RunItem{
		parserItem(t, "Core", "passes"),
		parserItem(t, "Core", "crashes"),
	}
	tests := map[string]struct {
		termination  testframework.ProcessTermination
		wantCategory string
	}{
		"crash":   {testframework.ProcessCrashed, "test_process_crash"},
		"timeout": {testframework.ProcessTimedOut, "test_timeout"},
		"cancel":  {testframework.ProcessCancelled, "cancelled"},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			parser := newResultParser(t, items, DefaultResultLimits())
			feedFixture(t, parser, "crash-partial.txt")
			result, err := parser.Finish(testframework.ProcessResult{
				ExitCode:    137,
				Termination: test.termination,
			})
			if err != nil {
				t.Fatal(err)
			}
			if result.Complete ||
				!hasDiagnostic(result, test.wantCategory) ||
				len(result.Cases) != 2 ||
				result.Cases[0].Status != testframework.CasePassed ||
				result.Cases[1].Status != testframework.CaseNotRun ||
				!result.Cases[0].Partial ||
				!result.Cases[1].Partial {
				t.Fatalf("result = %#v", result)
			}
		})
	}
}

func TestParserRejectsInvalidUTF8UnknownCasesAndLimits(t *testing.T) {
	item := parserItem(t, "Core", "passes")
	tests := map[string]struct {
		limits ResultLimits
		data   []byte
	}{
		"invalid UTF-8": {
			limits: DefaultResultLimits(),
			data:   []byte{0xff, '\n'},
		},
		"output limit": {
			limits: ResultLimits{
				MaxOutputBytes:  4,
				MaxLineBytes:    4,
				MaxMessageBytes: 4,
				MaxCases:        1,
			},
			data: []byte("12345"),
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			parser := newResultParser(t, []testframework.RunItem{item}, test.limits)
			if _, err := parser.Feed(testframework.StreamStdout, test.data); !errors.Is(err, ErrInvalidResult) &&
				!errors.Is(err, ErrResultLimitExceeded) {
				t.Fatalf("Feed error = %v", err)
			}
		})
	}

	parser := newResultParser(t, []testframework.RunItem{item}, DefaultResultLimits())
	if _, err := parser.Feed(
		testframework.StreamStdout,
		[]byte("TEST(Other, injected) - 1 ms\n"),
	); err != nil {
		t.Fatal(err)
	}
	result, err := parser.Finish(testframework.ProcessResult{
		Termination: testframework.ProcessExited,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Complete ||
		len(result.Cases) != 1 ||
		result.Cases[0].ItemID != item.ItemID ||
		result.Cases[0].Status != testframework.CaseNotRun ||
		!hasDiagnostic(result, "framework_output_invalid") {
		t.Fatalf("unknown case result = %#v", result)
	}
}

func TestNewParserRejectsInvalidExpectedBoundary(t *testing.T) {
	item := parserItem(t, "Core", "passes")
	for name, input := range map[string]testframework.ParseInput{
		"duplicate": {Items: []testframework.RunItem{item, item}},
		"invalid ID": {Items: []testframework.RunItem{{
			ItemID:            "not-an-id",
			ParentLogicalName: "Core",
			LogicalName:       "passes",
		}}},
		"empty": {},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := NewParser(input, DefaultResultLimits()); !errors.Is(err, ErrInvalidResult) {
				t.Fatalf("NewParser error = %v", err)
			}
		})
	}
}

func newResultParser(
	t *testing.T,
	items []testframework.RunItem,
	limits ResultLimits,
) *Parser {
	t.Helper()
	parser, err := NewParser(testframework.ParseInput{Items: items}, limits)
	if err != nil {
		t.Fatal(err)
	}
	return parser
}

func feedFixture(
	t *testing.T,
	parser *Parser,
	name string,
) []testframework.ResultEvent {
	t.Helper()
	events, err := parser.Feed(testframework.StreamStdout, readFixture(t, name))
	if err != nil {
		t.Fatal(err)
	}
	return events
}

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func parserItem(t *testing.T, group, name string) testframework.RunItem {
	t.Helper()
	id, err := testdomain.CaseID(testdomain.CaseIdentity{
		ProjectID:   "project-1",
		CTestName:   "unit-tests",
		Framework:   testdomain.FrameworkCppUTest,
		Group:       group,
		Name:        name,
		ProfileID:   strings.Repeat("1", 64),
		ToolchainID: strings.Repeat("2", 64),
	})
	if err != nil {
		t.Fatal(err)
	}
	return testframework.RunItem{
		ItemID:            id,
		ParentLogicalName: group,
		LogicalName:       name,
	}
}

func hasDiagnostic(result testframework.ParseResult, category string) bool {
	for _, diagnostic := range result.Diagnostics {
		if diagnostic.Category == category {
			return true
		}
	}
	return false
}
