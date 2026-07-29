package diagnostic

import (
	"bytes"
	"math/rand"
	"os"
	"reflect"
	"strings"
	"testing"

	"unit-test-ide.local/test-service/internal/workspace"
)

func TestCMakeParserNormalizesMultilineDiagnosticAndRange(t *testing.T) {
	rootPath := t.TempDir()
	root, err := workspace.OpenRoot(rootPath)
	if err != nil {
		t.Fatal(err)
	}
	parser, err := NewParser(FamilyCMake, Options{
		Root:             root,
		WorkingDirectory: root.NativePath,
		TaskID:           "task-1",
		StepID:           "configure",
	})
	if err != nil {
		t.Fatal(err)
	}

	input := "CMake Error at CMakeLists.txt:7 (add_executable):\n" +
		"  Cannot find source file:\n" +
		"    missing.cpp\n\n"
	got := append(parser.Feed("stderr", []byte(input)), parser.Close()...)

	if len(got) != 1 {
		t.Fatalf("diagnostics = %#v, want one", got)
	}
	wantRange := &Range{
		Start: Position{Line: 6, Character: 0},
		End:   Position{Line: 6, Character: 0},
	}
	if got[0].Source != "cmake" || got[0].Severity != "error" ||
		got[0].Code != "CMAKE_ERROR" ||
		got[0].Message != "Cannot find source file:\nmissing.cpp" ||
		got[0].TaskID != "task-1" || got[0].StepID != "configure" ||
		got[0].External || got[0].FileURI == "" ||
		!reflect.DeepEqual(got[0].Range, wantRange) || len(got[0].ID) != 64 {
		t.Fatalf("diagnostic = %#v", got[0])
	}
}

func TestMSVCGoldenNormalizesCompilerAndLinkerDiagnostics(t *testing.T) {
	parser := newTestParser(t, FamilyMSVC)
	got := feedFixture(t, parser, "msvc.txt")
	if len(got) != 3 {
		t.Fatalf("diagnostics = %#v, want three", got)
	}
	if got[0].Source != "compiler" || got[0].Severity != "error" ||
		got[0].Code != "C2143" ||
		got[0].Message != "syntax error: missing ';' before '}'" ||
		got[0].Range == nil || got[0].Range.Start != (Position{Line: 11, Character: 4}) {
		t.Fatalf("compiler error = %#v", got[0])
	}
	if got[1].Code != "C4100" || got[1].Range == nil ||
		got[1].Range.Start != (Position{Line: 19, Character: 0}) ||
		got[1].Range.End != (Position{Line: 19, Character: 0}) {
		t.Fatalf("compiler warning = %#v", got[1])
	}
	if got[2].Source != "linker" || got[2].Code != "LNK1120" ||
		got[2].Message != "1 unresolved externals" || got[2].FileURI != "" {
		t.Fatalf("linker error = %#v", got[2])
	}
}

func TestGNUGoldenKeepsWarningOptionAsStableCode(t *testing.T) {
	parser := newTestParser(t, FamilyGNU)
	got := feedFixture(t, parser, "gcc.txt")
	if len(got) != 2 {
		t.Fatalf("diagnostics = %#v, want two", got)
	}
	if got[0].Code != "-Wconversion" ||
		got[0].Message != "conversion changes value" ||
		got[1].Code != "COMPILER_ERROR" {
		t.Fatalf("diagnostics = %#v", got)
	}
}

func TestClangNoteBecomesRelatedRecordAndWindowsDriveIsNotSplit(t *testing.T) {
	parser := newTestParser(t, FamilyGNU)
	got := feedFixture(t, parser, "clang.txt")
	if len(got) != 1 {
		t.Fatalf("diagnostics = %#v, want one", got)
	}
	if got[0].Message != "use of undeclared identifier 'value'" ||
		len(got[0].Related) != 1 ||
		got[0].Related[0].Message != "declared here" ||
		got[0].Related[0].FileURI != "file:///C:/%E5%B7%A5%E4%BD%9C/header.hpp" {
		t.Fatalf("diagnostic = %#v", got[0])
	}
}

func TestLinkerGoldenRecognizesErrorsAndIgnoresOrdinaryOutput(t *testing.T) {
	parser := newTestParser(t, FamilyLinker)
	got := feedFixture(t, parser, "linkers.txt")
	if len(got) != 3 {
		t.Fatalf("diagnostics = %#v, want three", got)
	}
	wantCodes := []string{"LD_UNDEFINED_REFERENCE", "LLD_UNDEFINED_SYMBOL", "LD_ERROR"}
	for index, code := range wantCodes {
		if got[index].Source != "linker" || got[index].Severity != "error" ||
			got[index].Code != code {
			t.Fatalf("diagnostics[%d] = %#v", index, got[index])
		}
	}
}

func TestGNUParserRecognizesUbuntuLinkerFailureFromBuildStep(t *testing.T) {
	parser := newTestParser(t, FamilyGNU)
	got := append(parser.Feed("stderr", []byte(
		"/usr/bin/ld: CMakeFiles/linker_failure.dir/src/main.cpp.o: in function `main':\n"+
			"/home/runner/work/unitTest/unitTest/src/main.cpp:4:(.text+0x5): undefined reference to `native_missing_symbol()'\n"+
			"collect2: error: ld returned 1 exit status\n",
	)), parser.Close()...)
	if len(got) != 2 ||
		got[0].Source != "linker" ||
		got[0].Severity != "error" ||
		got[0].Code != "LD_UNDEFINED_REFERENCE" ||
		!strings.Contains(got[0].Message, "native_missing_symbol") ||
		got[1].Code != "LD_ERROR" {
		t.Fatalf("GNU build-step linker diagnostics = %#v", got)
	}
}

func TestLinkerParsersRecognizeRestrictedMSVCAndLLDLinkShapes(t *testing.T) {
	tests := []struct {
		name    string
		family  Family
		input   string
		code    string
		message string
	}{
		{
			name: "object error", family: FamilyLinker,
			input: "foo.obj : error LNK2019: unresolved external symbol run [app.vcxproj]\n",
			code:  "LNK2019", message: "unresolved external symbol run",
		},
		{
			name: "executable fatal", family: FamilyMSVC,
			input: "foo.exe : fatal error LNK1120: 1 unresolved externals\n",
			code:  "LNK1120", message: "1 unresolved externals",
		},
		{
			name: "lld-link error", family: FamilyLinker,
			input: "lld-link: error: undefined symbol: run\n",
			code:  "LLD_LINK_ERROR", message: "undefined symbol: run",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			parser := newTestParser(t, test.family)
			got := append(parser.Feed("stderr", []byte(test.input)), parser.Close()...)
			if len(got) != 1 || got[0].Source != "linker" ||
				got[0].Severity != "error" || got[0].Code != test.code ||
				got[0].Message != test.message {
				t.Fatalf("diagnostics=%#v", got)
			}
		})
	}
}

func TestCMakeFixedConfigureFailureReprocessesPendingLine(t *testing.T) {
	parser := newTestParser(t, FamilyCMake)
	got := append(parser.Feed("stderr", []byte(
		"CMake Error at CMakeLists.txt:4 (message):\n"+
			"  primary failure\n"+
			"-- Configuring incomplete, errors occurred!\n",
	)), parser.Close()...)
	if len(got) != 2 ||
		got[0].Code != "CMAKE_ERROR" || got[0].Message != "primary failure" ||
		got[1].Code != "CMAKE_CONFIGURE_FAILED" ||
		got[1].Message != "CMake configure failed" {
		t.Fatalf("diagnostics=%#v", got)
	}
}

func TestLinkerAndCMakeParsersIgnoreSimilarOrdinaryOutput(t *testing.T) {
	linker := newTestParser(t, FamilyLinker)
	linkerValues := append(linker.Feed("stderr", []byte(
		"foo.lib : warning LNK4099: debug symbols unavailable\n"+
			"foo.obj: error LNK2019: missing diagnostic separator\n"+
			"lld-link: warning: unused argument\n",
	)), linker.Close()...)
	if len(linkerValues) != 0 {
		t.Fatalf("ordinary linker output diagnostics=%#v", linkerValues)
	}

	cmake := newTestParser(t, FamilyCMake)
	cmakeValues := append(cmake.Feed("stdout", []byte(
		"-- Configuring done\n"+
			"-- Generating incomplete metadata\n",
	)), cmake.Close()...)
	if len(cmakeValues) != 0 {
		t.Fatalf("ordinary CMake output diagnostics=%#v", cmakeValues)
	}
}

func TestCMakeGoldenRecognizesFixedGenerateFailure(t *testing.T) {
	parser := newTestParser(t, FamilyCMake)
	got := feedFixture(t, parser, "cmake.txt")
	if len(got) != 2 {
		t.Fatalf("diagnostics = %#v, want two", got)
	}
	if got[1].Code != "CMAKE_GENERATE_FAILED" ||
		got[1].Message != "CMake generate step failed" {
		t.Fatalf("generate diagnostic = %#v", got[1])
	}
}

func TestParserFailsClosedOnInvalidUTF8NULUnknownStreamAndAfterClose(t *testing.T) {
	parser := newTestParser(t, FamilyGNU)
	var got []Diagnostic
	got = append(got, parser.Feed("stderr", []byte{0xff, '\n'})...)
	got = append(got, parser.Feed("stdout", []byte("x\x00y\n"))...)
	if unknown := parser.Feed("trace", []byte("secret\n")); unknown != nil {
		t.Fatalf("unknown stream diagnostics = %#v, want nil", unknown)
	}
	got = append(got, parser.Close()...)
	if second := parser.Close(); second != nil {
		t.Fatalf("second Close = %#v, want nil", second)
	}
	if after := parser.Feed("stderr", []byte("file.cpp:1:1: error: late\n")); after != nil {
		t.Fatalf("Feed after Close = %#v, want nil", after)
	}
	if len(got) != 1 || got[0].Code != "DIAGNOSTIC_INPUT_INVALID" ||
		got[0].Severity != "info" ||
		strings.Contains(got[0].Message, "secret") ||
		strings.Contains(got[0].Message, string([]byte{0xff})) {
		t.Fatalf("diagnostics = %#v", got)
	}
}

func TestParserAcceptsSplitUTF8AndExactLineLimitButTruncatesLimitPlusOne(t *testing.T) {
	const prefix = "src/空.cpp:1:1: error: "
	exact := []byte(prefix + strings.Repeat("x", 64*1024-len([]byte(prefix))) + "\n")
	parser := newTestParser(t, FamilyGNU)
	var got []Diagnostic
	for _, value := range exact {
		got = append(got, parser.Feed("stderr", []byte{value})...)
	}
	got = append(got, parser.Close()...)
	if len(got) != 1 || got[0].Code != "COMPILER_ERROR" {
		t.Fatalf("exact line diagnostics = %#v", got)
	}

	over := newTestParser(t, FamilyGNU)
	tooLong := append(bytes.Clone(exact[:len(exact)-1]), 'x', '\n')
	got = append(over.Feed("stderr", tooLong), over.Close()...)
	if len(got) != 1 || got[0].Code != "DIAGNOSTIC_TRUNCATED" ||
		strings.Contains(got[0].Message, strings.Repeat("x", 32)) {
		t.Fatalf("overlong diagnostics = %#v", got)
	}
}

func TestParserLogicalLineLimitIsDelimiterAndChunkInvariant(t *testing.T) {
	root, err := workspace.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	options := Options{Root: root, WorkingDirectory: root.NativePath}
	newParser := func() Parser {
		value, err := NewParser(FamilyGNU, options)
		if err != nil {
			t.Fatal(err)
		}
		return value
	}

	const prefix = "src/main.cpp:1:1: error: "
	exactLine := prefix + strings.Repeat("x", maxLineBytes-len(prefix))
	overLine := exactLine + "x"
	var exactID string
	for _, delimiter := range []string{"\n", "\r\n"} {
		for _, chunks := range []string{"whole", "byte", "random"} {
			got := feedParserChunks(t, newParser(), "stderr", []byte(exactLine+delimiter), chunks)
			if len(got) != 1 || got[0].Code != "COMPILER_ERROR" {
				t.Fatalf("exact delimiter=%q chunks=%s diagnostics=%#v", delimiter, chunks, got)
			}
			if exactID == "" {
				exactID = got[0].ID
			} else if got[0].ID != exactID {
				t.Fatalf("exact delimiter=%q chunks=%s ID=%q, want %q", delimiter, chunks, got[0].ID, exactID)
			}

			got = feedParserChunks(t, newParser(), "stderr", []byte(overLine+delimiter), chunks)
			if len(got) != 1 || got[0].Code != "DIAGNOSTIC_TRUNCATED" {
				t.Fatalf("limit+1 delimiter=%q chunks=%s diagnostics=%#v", delimiter, chunks, got)
			}
		}
	}

	concrete := newParser().(*parser)
	if got := concrete.Feed("stderr", []byte(exactLine)); len(got) != 0 {
		t.Fatalf("exact partial diagnostics=%#v", got)
	}
	if got := concrete.Feed("stderr", []byte{'\r'}); len(got) != 0 {
		t.Fatalf("pending delimiter CR diagnostics=%#v", got)
	}
	if got := len(concrete.streams["stderr"].buffer); got > maxLineBytes+1 {
		t.Fatalf("pending line retained %d bytes", got)
	}
	got := append(concrete.Feed("stderr", []byte{'x'}), concrete.Close()...)
	if len(got) != 1 || got[0].Code != "DIAGNOSTIC_TRUNCATED" {
		t.Fatalf("CR followed by non-LF diagnostics=%#v", got)
	}

	bounded := newParser().(*parser)
	bounded.Feed("stderr", bytes.Repeat([]byte{'x'}, 1024*1024))
	if got := len(bounded.streams["stderr"].buffer); got > maxLineBytes+1 {
		t.Fatalf("overlong partial retained %d bytes", got)
	}
	if !bounded.streams["stderr"].discardingLine {
		t.Fatal("overlong partial did not enter bounded discard mode")
	}
}

func TestParserCapsRelatedRecordsAndDefensivelyCopiesResults(t *testing.T) {
	parser := newTestParser(t, FamilyGNU)
	var input strings.Builder
	input.WriteString("src/main.cpp:1:1: error: primary\n")
	for index := 0; index < 33; index++ {
		input.WriteString("src/header.hpp:2:1: note: related\n")
	}
	got := append(parser.Feed("stderr", []byte(input.String())), parser.Close()...)
	if len(got) != 2 {
		t.Fatalf("diagnostics = %#v, want primary and truncation", got)
	}
	var primary *Diagnostic
	for index := range got {
		if got[index].Code == "COMPILER_ERROR" {
			primary = &got[index]
		}
	}
	if primary == nil || len(primary.Related) != 32 {
		t.Fatalf("diagnostics = %#v", got)
	}
	primary.Range.Start.Line = 99
	primary.Related[0].Range.Start.Line = 99

	second := newTestParser(t, FamilyGNU)
	again := append(second.Feed("stderr", []byte(
		"src/main.cpp:1:1: error: primary\n"+
			"src/header.hpp:2:1: note: related\n",
	)), second.Close()...)
	if again[0].Range.Start.Line != 0 || again[0].Related[0].Range.Start.Line != 1 {
		t.Fatalf("caller mutation leaked: %#v", again[0])
	}
}

func TestDiagnosticIDUsesWorkspaceRelativeIdentityAndOccurrenceOrdinal(t *testing.T) {
	first := newTestParser(t, FamilyGNU)
	second := newTestParser(t, FamilyGNU)
	const input = "src/main.cpp:1:1: error: broken\n"
	firstValues := append(first.Feed("stderr", []byte(input+input)), first.Close()...)
	secondValues := append(second.Feed("stderr", []byte(input)), second.Close()...)
	if len(firstValues) != 2 || len(secondValues) != 1 {
		t.Fatalf("first = %#v, second = %#v", firstValues, secondValues)
	}
	if firstValues[0].ID != secondValues[0].ID {
		t.Fatalf("workspace-relative IDs differ: %q != %q", firstValues[0].ID, secondValues[0].ID)
	}
	if firstValues[0].ID == firstValues[1].ID {
		t.Fatalf("duplicate occurrences share ID %q", firstValues[0].ID)
	}
}

func TestParserMapsUNCAndPOSIXPathsToCanonicalFileURIs(t *testing.T) {
	parser := newTestParser(t, FamilyGNU)
	got := append(parser.Feed("stderr", []byte(
		"\\\\server\\share\\sdk.hpp:3:2: error: unc\n"+
			"/usr/include/sdk.hpp:4:1: warning: posix\n",
	)), parser.Close()...)
	if len(got) != 2 ||
		got[0].FileURI != "file://server/share/sdk.hpp" || !got[0].External ||
		got[1].FileURI != "file:///usr/include/sdk.hpp" || !got[1].External {
		t.Fatalf("diagnostics = %#v", got)
	}
}

func TestParserKeepsStdoutAndStderrStateIndependent(t *testing.T) {
	parser := newTestParser(t, FamilyCMake)
	var got []Diagnostic
	got = append(got, parser.Feed("stdout", []byte(
		"CMake Warning at first.cmake:1 (message):\n  stdout",
	))...)
	got = append(got, parser.Feed("stderr", []byte(
		"CMake Error at second.cmake:2 (message):\n  stderr",
	))...)
	got = append(got, parser.Close()...)
	if len(got) != 2 ||
		got[0].Message != "stdout" || got[1].Message != "stderr" {
		t.Fatalf("diagnostics = %#v", got)
	}
}

func TestMSVCTrailingNoteBecomesRelatedAndLinkerFamilyAcceptsLNK(t *testing.T) {
	compiler := newTestParser(t, FamilyMSVC)
	got := append(compiler.Feed("stderr", []byte(
		"src/main.cpp(8,2): error C2664: cannot convert argument\n"+
			"src/header.hpp(4): note: see declaration of 'value'\n",
	)), compiler.Close()...)
	if len(got) != 1 || len(got[0].Related) != 1 ||
		got[0].Related[0].Message != "see declaration of 'value'" {
		t.Fatalf("compiler diagnostics = %#v", got)
	}

	linker := newTestParser(t, FamilyLinker)
	got = append(linker.Feed("stderr", []byte(
		"LINK : fatal error LNK2019: unresolved external symbol run [app.vcxproj]\n",
	)), linker.Close()...)
	if len(got) != 1 || got[0].Code != "LNK2019" ||
		got[0].Message != "unresolved external symbol run" {
		t.Fatalf("linker diagnostics = %#v", got)
	}
}

func TestMSVCParserAcceptsClangCLDiagnosticWithoutNumericCode(t *testing.T) {
	parser := newTestParser(t, FamilyMSVC)
	got := append(parser.Feed("stderr", []byte(
		"src/main.cpp(3,21): error: use of undeclared identifier 'UNIT_TEST_IDE_UNKNOWN_IDENTIFIER'\n",
	)), parser.Close()...)
	if len(got) != 1 || got[0].Source != "compiler" ||
		got[0].Severity != "error" || got[0].Code != "COMPILER_ERROR" ||
		got[0].Message !=
			"use of undeclared identifier 'UNIT_TEST_IDE_UNKNOWN_IDENTIFIER'" ||
		got[0].Range == nil ||
		got[0].Range.Start != (Position{Line: 2, Character: 20}) {
		t.Fatalf("clang-cl diagnostic = %#v", got)
	}
}

func TestCMakeParserRecognizesFixedConfigureFatalWithoutEchoingPath(t *testing.T) {
	parser := newTestParser(t, FamilyCMake)
	got := append(parser.Feed("stderr", []byte(
		"CMake Error: The source directory \"/tmp/token-secret\" does not appear to contain CMakeLists.txt.\n",
	)), parser.Close()...)
	if len(got) != 1 || got[0].Code != "CMAKE_CONFIGURE_FAILED" ||
		strings.Contains(got[0].Message, "/tmp") ||
		strings.Contains(got[0].Message, "token-secret") {
		t.Fatalf("diagnostics = %#v", got)
	}
}

func TestParserCapsDiagnosticCountWithOneStableTruncationNotice(t *testing.T) {
	parser := newTestParser(t, FamilyGNU)
	var input strings.Builder
	for index := 0; index < 4097; index++ {
		input.WriteString("src/main.cpp:1:1: error: broken\n")
	}
	got := append(parser.Feed("stderr", []byte(input.String())), parser.Close()...)
	if len(got) != 4096 {
		t.Fatalf("diagnostic count = %d, want 4096", len(got))
	}
	notices := 0
	for _, value := range got {
		if value.Code == "DIAGNOSTIC_TRUNCATED" {
			notices++
		}
	}
	if notices != 1 {
		t.Fatalf("truncation notices = %d, want one", notices)
	}
}

func TestParserUsesEarlyTruncationNoticeAsOneOf4096Slots(t *testing.T) {
	triggers := []struct {
		name string
		feed func(Parser) []Diagnostic
	}{
		{
			name: "line limit",
			feed: func(parser Parser) []Diagnostic {
				return parser.Feed("stderr", []byte(
					strings.Repeat("x", maxLineBytes+1)+"\n",
				))
			},
		},
		{
			name: "related limit",
			feed: func(parser Parser) []Diagnostic {
				var input strings.Builder
				input.WriteString("src/main.cpp:1:1: error: primary\n")
				for index := 0; index < maxRelatedRecords+1; index++ {
					input.WriteString("src/header.hpp:2:1: note: related\n")
				}
				input.WriteString("ordinary separator\n")
				return parser.Feed("stderr", []byte(input.String()))
			},
		},
		{
			name: "single diagnostic limit",
			feed: func(parser Parser) []Diagnostic {
				const notePrefix = "src/header.hpp:2:1: note: "
				longNote := notePrefix +
					strings.Repeat("n", maxLineBytes-len(notePrefix)) + "\n"
				var input strings.Builder
				input.WriteString("src/main.cpp:1:1: error: primary\n")
				for index := 0; index < 17; index++ {
					input.WriteString(longNote)
				}
				input.WriteString("ordinary separator\n")
				return parser.Feed("stderr", []byte(input.String()))
			},
		},
	}

	for _, trigger := range triggers {
		t.Run(trigger.name, func(t *testing.T) {
			for _, extra := range []int{0, 1} {
				parser := newTestParser(t, FamilyGNU)
				got := trigger.feed(parser)
				if !hasDiagnosticCode(got, "DIAGNOSTIC_TRUNCATED") {
					t.Fatalf("extra=%d trigger did not emit early notice: %#v", extra, got)
				}
				var input strings.Builder
				for index := 0; index < maxDiagnostics-len(got)+extra; index++ {
					input.WriteString("src/main.cpp:1:1: error: broken\n")
				}
				got = append(got, parser.Feed("stderr", []byte(input.String()))...)
				got = append(got, parser.Close()...)
				if len(got) != maxDiagnostics {
					t.Fatalf("extra=%d diagnostic count=%d, want %d", extra, len(got), maxDiagnostics)
				}
				notices := 0
				for _, value := range got {
					if value.Code == "DIAGNOSTIC_TRUNCATED" {
						notices++
					}
				}
				if notices != 1 {
					t.Fatalf("extra=%d truncation notices=%d, want one", extra, notices)
				}
			}
		})
	}
}

func TestParserReservesCountCapacityForInvalidAndTruncationNotices(t *testing.T) {
	var ordinaryInput strings.Builder
	for index := 0; index < maxDiagnostics-1; index++ {
		ordinaryInput.WriteString("src/main.cpp:1:1: error: broken\n")
	}
	invalidInput := []byte{0xff, '\n'}
	overlongInput := []byte(strings.Repeat("x", maxLineBytes+1) + "\n")

	for _, invalidFirst := range []bool{false, true} {
		parser := newTestParser(t, FamilyGNU)
		var got []Diagnostic
		if invalidFirst {
			got = append(got, parser.Feed("stderr", invalidInput)...)
		}
		got = append(got, parser.Feed("stderr", []byte(ordinaryInput.String()))...)
		if !invalidFirst {
			got = append(got, parser.Feed("stderr", invalidInput)...)
		}
		got = append(got, parser.Feed("stderr", overlongInput)...)
		got = append(got, parser.Close()...)

		if len(got) != maxDiagnostics {
			t.Fatalf("invalidFirst=%t diagnostic count=%d, want %d", invalidFirst, len(got), maxDiagnostics)
		}
		ordinary := 0
		invalidNotices := 0
		truncationNotices := 0
		for _, value := range got {
			switch value.Code {
			case "COMPILER_ERROR":
				ordinary++
			case "DIAGNOSTIC_INPUT_INVALID":
				invalidNotices++
			case "DIAGNOSTIC_TRUNCATED":
				truncationNotices++
			}
		}
		if ordinary != maxDiagnostics-2 ||
			invalidNotices != 1 || truncationNotices != 1 {
			t.Fatalf(
				"invalidFirst=%t ordinary=%d invalid notices=%d truncation notices=%d",
				invalidFirst, ordinary, invalidNotices, truncationNotices,
			)
		}
	}
}

func TestParserCapsSingleAggregatedDiagnostic(t *testing.T) {
	parser := newTestParser(t, FamilyCMake)
	var got []Diagnostic
	got = append(got, parser.Feed("stderr", []byte(
		"CMake Error at CMakeLists.txt:1 (message):\n",
	))...)
	line := "  " + strings.Repeat("x", 64*1024-2) + "\n"
	for index := 0; index < 17; index++ {
		got = append(got, parser.Feed("stderr", []byte(line))...)
	}
	got = append(got, parser.Close()...)
	if len(got) != 2 {
		t.Fatalf("diagnostics count = %d, want primary and truncation", len(got))
	}
	for _, value := range got {
		if len(value.Message) > 1024*1024 {
			t.Fatalf("message retained %d bytes", len(value.Message))
		}
	}
}

func TestParserReservesEightMiBTextBudgetForStableNotices(t *testing.T) {
	const (
		smallMessageBytes     = 65506
		smallDiagnostics      = 127
		ordinaryBaseBytes     = len("linker") + len("toolchain-1") + len("error") + len("LD_ERROR")
		truncationNoticeBytes = len("parser") + len("toolchain-1") + len("info") +
			len("DIAGNOSTIC_TRUNCATED") + len("Diagnostic output was truncated")
		invalidNoticeBytes = len("parser") + len("toolchain-1") + len("info") +
			len("DIAGNOSTIC_INPUT_INVALID") + len("Diagnostic input was invalid")
		reservedNoticeBytes = truncationNoticeBytes + invalidNoticeBytes
	)
	var prefix strings.Builder
	for index := 0; index < smallDiagnostics; index++ {
		prefix.WriteString("collect2: error: ")
		prefix.WriteString(strings.Repeat("x", smallMessageBytes))
		prefix.WriteByte('\n')
	}
	finalMessageBytes := 8*1024*1024 -
		smallDiagnostics*(ordinaryBaseBytes+smallMessageBytes) -
		reservedNoticeBytes - ordinaryBaseBytes
	if finalMessageBytes <= 0 {
		t.Fatal("invalid hand-derived fixture size")
	}
	exactInput := prefix.String() +
		"collect2: error: " + strings.Repeat("y", finalMessageBytes) + "\n"

	exact := newTestParser(t, FamilyLinker)
	exactValues := append(exact.Feed("stderr", []byte(exactInput)), exact.Close()...)
	if hasDiagnosticCode(exactValues, "DIAGNOSTIC_TRUNCATED") {
		t.Fatalf("exact retained-text budget was truncated")
	}
	if got := retainedDiagnosticText(exactValues); got != 8*1024*1024-reservedNoticeBytes {
		t.Fatalf("exact retained text = %d", got)
	}

	over := newTestParser(t, FamilyLinker)
	overInput := prefix.String() +
		"collect2: error: " + strings.Repeat("y", finalMessageBytes+1) + "\n" +
		"collect2: error: later\n"
	overValues := append(over.Feed("stderr", []byte(overInput)), over.Close()...)
	if !hasDiagnosticCode(overValues, "DIAGNOSTIC_TRUNCATED") {
		t.Fatal("limit+1 did not emit truncation notice")
	}
	if got := retainedDiagnosticText(overValues); got > 8*1024*1024 {
		t.Fatalf("retained text including notice = %d, exceeds 8 MiB", got)
	}
	for _, value := range overValues {
		if value.Message == "later" {
			t.Fatal("parser retained content after total text limit")
		}
	}
}

func TestParserReservesTextBudgetForStableNoticesRegardlessOfOrder(t *testing.T) {
	const (
		smallMessageBytes = 65506
		smallDiagnostics  = 127
		ordinaryBaseBytes = len("linker") + len("toolchain-1") + len("error") + len("LD_ERROR")
		truncationBytes   = len("parser") + len("toolchain-1") + len("info") +
			len("DIAGNOSTIC_TRUNCATED") + len("Diagnostic output was truncated")
	)
	var prefix strings.Builder
	for index := 0; index < smallDiagnostics; index++ {
		prefix.WriteString("collect2: error: ")
		prefix.WriteString(strings.Repeat("x", smallMessageBytes))
		prefix.WriteByte('\n')
	}
	finalMessageBytes := maxRetainedTextBytes -
		smallDiagnostics*(ordinaryBaseBytes+smallMessageBytes) -
		truncationBytes - ordinaryBaseBytes
	if finalMessageBytes <= 0 || finalMessageBytes > maxLineBytes-len("collect2: error: ") {
		t.Fatal("invalid hand-derived fixture size")
	}
	normalInput := prefix.String() +
		"collect2: error: " + strings.Repeat("y", finalMessageBytes) + "\n"
	invalidInput := []byte{0xff, '\n'}
	overlongInput := []byte(strings.Repeat("z", maxLineBytes+1) + "\n")

	for _, invalidFirst := range []bool{false, true} {
		parser := newTestParser(t, FamilyLinker)
		var got []Diagnostic
		if invalidFirst {
			got = append(got, parser.Feed("stderr", invalidInput)...)
		}
		got = append(got, parser.Feed("stderr", []byte(normalInput))...)
		if !invalidFirst {
			got = append(got, parser.Feed("stderr", invalidInput)...)
		}
		got = append(got, parser.Feed("stderr", overlongInput)...)
		got = append(got, parser.Close()...)

		invalidNotices := 0
		truncationNotices := 0
		for _, value := range got {
			switch value.Code {
			case "DIAGNOSTIC_INPUT_INVALID":
				invalidNotices++
			case "DIAGNOSTIC_TRUNCATED":
				truncationNotices++
			}
		}
		if invalidNotices != 1 || truncationNotices != 1 {
			t.Fatalf(
				"invalidFirst=%t invalid notices=%d truncation notices=%d",
				invalidFirst, invalidNotices, truncationNotices,
			)
		}
		if retained := retainedDiagnosticText(got); retained > maxRetainedTextBytes {
			t.Fatalf("invalidFirst=%t retained text=%d, exceeds 8 MiB", invalidFirst, retained)
		}
	}
}

func TestParserRelatedFileURIIsNotMutatedWhileIDIsStableAcrossRoots(t *testing.T) {
	parse := func(root workspace.Root) Diagnostic {
		parser, err := NewParser(FamilyGNU, Options{
			Root: root, WorkingDirectory: root.NativePath,
		})
		if err != nil {
			t.Fatal(err)
		}
		values := append(parser.Feed("stderr", []byte(
			"src/main.cpp:1:1: error: broken\n"+
				"src/header.hpp:2:1: note: declared here\n",
		)), parser.Close()...)
		if len(values) != 1 {
			t.Fatalf("diagnostics = %#v", values)
		}
		return values[0]
	}
	firstRoot, err := workspace.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	secondRoot, err := workspace.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	first := parse(firstRoot)
	second := parse(secondRoot)
	if !strings.HasPrefix(first.Related[0].FileURI, firstRoot.URI+"/") ||
		strings.HasPrefix(first.Related[0].FileURI, "workspace:") {
		t.Fatalf("related URI was mutated: %q", first.Related[0].FileURI)
	}
	if first.ID != second.ID {
		t.Fatalf("IDs differ across roots: %q != %q", first.ID, second.ID)
	}
}

func retainedDiagnosticText(values []Diagnostic) int {
	total := 0
	for _, value := range values {
		total += len(value.TaskID) + len(value.StepID) + len(value.Source) +
			len(value.ToolchainID) + len(value.Severity) + len(value.Code) +
			len(value.Message) + len(value.FileURI)
		for _, related := range value.Related {
			total += len(related.Message) + len(related.FileURI)
		}
	}
	return total
}

func hasDiagnosticCode(values []Diagnostic, code string) bool {
	for _, value := range values {
		if value.Code == code {
			return true
		}
	}
	return false
}

func TestOverlongLineFlushesPendingAndCannotAttachLaterNote(t *testing.T) {
	parser := newTestParser(t, FamilyGNU)
	var got []Diagnostic
	got = append(got, parser.Feed("stderr", []byte(
		"src/main.cpp:1:1: error: primary\n",
	))...)
	got = append(got, parser.Feed("stderr", []byte(
		strings.Repeat("x", 64*1024+1)+"\n",
	))...)
	got = append(got, parser.Feed("stderr", []byte(
		"src/header.hpp:2:1: note: must be standalone\n",
	))...)
	got = append(got, parser.Close()...)
	if len(got) != 3 {
		t.Fatalf("diagnostics = %#v, want primary, truncation, standalone note", got)
	}
	for _, value := range got {
		if value.Code == "COMPILER_ERROR" && len(value.Related) != 0 {
			t.Fatalf("note crossed overlong line: %#v", value)
		}
	}
}

func TestCMakeParserIsInvariantToByteChunksAndCRLF(t *testing.T) {
	rootPath := t.TempDir()
	root, err := workspace.OpenRoot(rootPath)
	if err != nil {
		t.Fatal(err)
	}
	options := Options{Root: root, WorkingDirectory: root.NativePath}
	const lf = "CMake Warning at cmake/Flags.cmake:3 (message):\n  unsafe flag\n\n"

	whole, err := NewParser(FamilyCMake, options)
	if err != nil {
		t.Fatal(err)
	}
	want := append(whole.Feed("stderr", []byte(lf)), whole.Close()...)

	chunked, err := NewParser(FamilyCMake, options)
	if err != nil {
		t.Fatal(err)
	}
	var got []Diagnostic
	for _, value := range []byte(strings.ReplaceAll(lf, "\n", "\r\n")) {
		got = append(got, chunked.Feed("stderr", []byte{value})...)
	}
	got = append(got, chunked.Close()...)

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("chunked = %#v\nwhole = %#v", got, want)
	}
}

func TestParserMapsExternalAbsolutePathWithoutHidingIt(t *testing.T) {
	rootPath := t.TempDir()
	root, err := workspace.OpenRoot(rootPath)
	if err != nil {
		t.Fatal(err)
	}
	parser, err := NewParser(FamilyGNU, Options{
		Root:             root,
		WorkingDirectory: root.NativePath,
		ToolchainID:      "gcc-local",
	})
	if err != nil {
		t.Fatal(err)
	}

	got := append(
		parser.Feed("stderr", []byte("/opt/sdk/include/header.h:2:9: error: broken\n")),
		parser.Close()...,
	)
	if len(got) != 1 {
		t.Fatalf("diagnostics = %#v, want one", got)
	}
	if !got[0].External || got[0].FileURI != "file:///opt/sdk/include/header.h" ||
		got[0].ToolchainID != "gcc-local" {
		t.Fatalf("diagnostic = %#v", got[0])
	}
}

func newTestParser(t *testing.T, family Family) Parser {
	t.Helper()
	root, err := workspace.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	parser, err := NewParser(family, Options{
		Root:             root,
		WorkingDirectory: root.NativePath,
		ToolchainID:      "toolchain-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	return parser
}

func feedFixture(t *testing.T, parser Parser, name string) []Diagnostic {
	t.Helper()
	data, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return append(parser.Feed("stderr", data), parser.Close()...)
}

func feedParserChunks(t *testing.T, parser Parser, stream string, data []byte, chunks string) []Diagnostic {
	t.Helper()
	var got []Diagnostic
	switch chunks {
	case "whole":
		got = append(got, parser.Feed(stream, data)...)
	case "byte":
		for _, value := range data {
			got = append(got, parser.Feed(stream, []byte{value})...)
		}
	case "random":
		random := rand.New(rand.NewSource(20260729))
		for len(data) != 0 {
			size := random.Intn(4096) + 1
			if size > len(data) {
				size = len(data)
			}
			got = append(got, parser.Feed(stream, data[:size])...)
			data = data[size:]
		}
	default:
		t.Fatalf("unknown chunk mode %q", chunks)
	}
	return append(got, parser.Close()...)
}
