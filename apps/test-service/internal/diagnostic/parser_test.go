package diagnostic

import (
	"bytes"
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

func TestParserReservesEightMiBTextBudgetForOneTruncationNotice(t *testing.T) {
	const (
		smallMessageBytes = 65506
		smallDiagnostics  = 127
		ordinaryBaseBytes = len("linker") + len("toolchain-1") + len("error") + len("LD_ERROR")
		noticeBytes       = len("parser") + len("toolchain-1") + len("info") +
			len("DIAGNOSTIC_TRUNCATED") + len("Diagnostic output was truncated")
	)
	var prefix strings.Builder
	for index := 0; index < smallDiagnostics; index++ {
		prefix.WriteString("collect2: error: ")
		prefix.WriteString(strings.Repeat("x", smallMessageBytes))
		prefix.WriteByte('\n')
	}
	finalMessageBytes := 8*1024*1024 -
		smallDiagnostics*(ordinaryBaseBytes+smallMessageBytes) - noticeBytes - ordinaryBaseBytes
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
	if got := retainedDiagnosticText(exactValues); got != 8*1024*1024-noticeBytes {
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
