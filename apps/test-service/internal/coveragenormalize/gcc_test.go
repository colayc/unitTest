package coveragenormalize

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"unit-test-ide.local/test-service/internal/coveragedomain"
	coveragemodelv1 "unit-test-ide.local/test-service/internal/coveragemodel/v1"
	coverageparsergcovr "unit-test-ide.local/test-service/internal/coverageparser/gcovr"
)

func TestNormalizeGCCFiltersSourcesBuildsMetricsAndStableCanonicalBytes(t *testing.T) {
	input := gccNormalizationFixture(t)
	first, bindings, err := NormalizeGCC(input)
	if err != nil {
		t.Fatal(err)
	}
	input.Export.Files[0], input.Export.Files[1] = input.Export.Files[1], input.Export.Files[0]
	second, _, err := NormalizeGCC(input)
	if err != nil {
		t.Fatal(err)
	}
	firstJSON, err := EncodeCanonical(first)
	if err != nil {
		t.Fatal(err)
	}
	secondJSON, err := EncodeCanonical(second)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(firstJSON, secondJSON) {
		t.Fatalf("reordered input changed canonical bytes\n%s\n%s", firstJSON, secondJSON)
	}
	if len(bindings) != 2 || bindings[0].URI != "src/a.c" || bindings[1].URI != "src/z.c" || bindings[0].NativePath == "" {
		t.Fatalf("bindings = %#v", bindings)
	}
	if first.Summary != (coveragemodelv1.CoverageSummaryV1{
		Lines:     coveragemodelv1.CoverageMetricV1{Covered: 2, Total: 3},
		Branches:  coveragemodelv1.CoverageMetricV1{Covered: 1, Total: 2},
		Functions: coveragemodelv1.CoverageMetricV1{Covered: 1, Total: 2},
	}) {
		t.Fatalf("summary = %#v", first.Summary)
	}
	if first.Provenance.Compiler.Family != coveragemodelv1.GCC || first.Provenance.Driver.Name != coveragemodelv1.Gcov || first.Provenance.Collector.Name != coveragemodelv1.Gcovr {
		t.Fatalf("GCC provenance = %#v", first.Provenance)
	}
}

func TestNormalizeGCCPartialAndInvalidEvidenceFailClosed(t *testing.T) {
	input := gccNormalizationFixture(t)
	input.Export.Files = input.Export.Files[:1]
	input.Completeness = coveragedomain.Completeness{Outcome: coveragedomain.OutcomePartial, Reasons: []coveragedomain.CompletenessReason{coveragedomain.CompletenessReasonTestTimedOut}}
	got, _, err := NormalizeGCC(input)
	if err != nil || got.Completeness.Outcome != coveragemodelv1.Partial || len(got.Files) != 1 {
		t.Fatalf("partial = %#v, %v", got, err)
	}

	input = gccNormalizationFixture(t)
	input.Export.Files[0].RelativePath = "../escape.c"
	got, bindings, err := NormalizeGCC(input)
	if err == nil || !reflect.DeepEqual(got, coveragemodelv1.CoverageDocumentV1{}) || bindings != nil {
		t.Fatalf("escape = %#v / %#v / %v", got, bindings, err)
	}
}

func TestNormalizeGCCRejectsSymlinkHardLinkAndOverflow(t *testing.T) {
	input := gccNormalizationFixture(t)
	escaping := filepath.Join(input.WorkspaceRoot, "src", "escape.c")
	if err := os.Symlink(filepath.Join(input.WorkspaceRoot, "outside.c"), escaping); err == nil {
		input.Export.Files[0].RelativePath = "src/escape.c"
		if _, _, err := NormalizeGCC(input); err == nil {
			t.Fatal("NormalizeGCC accepted symlink source")
		}
	}

	input = gccNormalizationFixture(t)
	if err := os.Link(filepath.Join(input.WorkspaceRoot, "src", "a.c"), filepath.Join(input.WorkspaceRoot, "src", "alias.c")); err == nil {
		input.Export.Files = append(input.Export.Files, coverageparsergcovr.File{RelativePath: "src/alias.c", Lines: []coverageparsergcovr.Line{{Number: 1, Count: 1}}})
		if _, _, err := NormalizeGCC(input); !errors.Is(err, ErrDuplicateSource) {
			t.Fatalf("hard link error = %v", err)
		}
	}

	input = gccNormalizationFixture(t)
	input.Export.Files[0].Functions = coverageparsergcovr.Metric{Covered: 1, Total: 1}
	input.Export.Files[1].Functions = coverageparsergcovr.Metric{Covered: 1, Total: coveragedomain.MaxSafeInteger}
	if _, _, err := NormalizeGCC(input); err == nil {
		t.Fatalf("summary overflow was accepted")
	}
}

func TestNormalizeGCCRejectsDigestMutationAndPreservesCaseSensitivePaths(t *testing.T) {
	input := gccNormalizationFixture(t)
	upper := filepath.Join(input.WorkspaceRoot, "src", "Case.c")
	lower := filepath.Join(input.WorkspaceRoot, "src", "case.c")
	writeSourceFile(t, upper, "UPPER\n")
	writeSourceFile(t, lower, "lower\n")
	upperInfo, upperErr := os.Stat(upper)
	lowerInfo, lowerErr := os.Stat(lower)
	if upperErr == nil && lowerErr == nil && !os.SameFile(upperInfo, lowerInfo) {
		input.Export.Files = []coverageparsergcovr.File{{RelativePath: "src/Case.c", Lines: []coverageparsergcovr.Line{{Number: 1, Count: 1}}}, {RelativePath: "src/case.c", Lines: []coverageparsergcovr.Line{{Number: 1, Count: 1}}}}
		got, _, err := NormalizeGCC(input)
		if err != nil || len(got.Files) != 2 {
			t.Fatalf("case paths = %#v, %v", got.Files, err)
		}
	}

	input = gccNormalizationFixture(t)
	input.Limits.MaxInputBytes = 1
	if _, _, err := NormalizeGCC(input); !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("digest limit error = %v", err)
	}
}

func gccNormalizationFixture(t *testing.T) GCCInput {
	t.Helper()
	root := t.TempDir()
	writeSourceFile(t, filepath.Join(root, "src", "a.c"), "alpha\n")
	writeSourceFile(t, filepath.Join(root, "src", "z.c"), "zeta\n")
	writeSourceFile(t, filepath.Join(root, "generated", "skip.c"), "skip\n")
	matcher, err := NewGlobMatcher([]string{"**/*.c"}, []string{"generated/**"})
	if err != nil {
		t.Fatal(err)
	}
	return GCCInput{
		Export: coverageparsergcovr.Export{FormatVersion: "0.14", Files: []coverageparsergcovr.File{
			{RelativePath: "src/z.c", Functions: coverageparsergcovr.Metric{Total: 1}, Lines: []coverageparsergcovr.Line{{Number: 2, Count: 2}}},
			{RelativePath: "src/a.c", Functions: coverageparsergcovr.Metric{Covered: 1, Total: 1}, Lines: []coverageparsergcovr.Line{{Number: 11, Count: 0}, {Number: 10, Count: 1, Branches: coverageparsergcovr.Metric{Covered: 1, Total: 2}}}},
			{RelativePath: "generated/skip.c", Lines: []coverageparsergcovr.Line{{Number: 1, Count: 99}}},
		}},
		WorkspaceRoot: root, Matcher: matcher,
		Toolchain: coveragedomain.ToolchainSnapshot{
			Platform: coveragedomain.PlatformLinux, Architecture: coveragedomain.ArchitectureX64,
			Compiler:          coveragedomain.CompilerSnapshot{Family: coveragedomain.CompilerFamilyGCC, Version: "15.1.0"},
			Driver:            coveragedomain.DriverSnapshot{Name: coveragedomain.DriverGCov, Version: "15.1.0"},
			Collector:         coveragedomain.CollectorSnapshot{Name: coveragedomain.CollectorGCovr, Version: "8.6"},
			NormalizerVersion: "1.0.0", InstrumentationFingerprint: strings.Repeat("b", 64),
		}, Completeness: coveragedomain.Completeness{Outcome: coveragedomain.OutcomeAvailable}, Limits: DefaultLimits(),
	}
}

func TestNormalizeGCCGolden(t *testing.T) {
	document, _, err := NormalizeGCC(gccNormalizationFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := EncodeCanonical(document)
	if err != nil {
		t.Fatal(err)
	}
	golden, err := os.ReadFile("testdata/gcc-coverage-v1.golden.json")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(encoded, golden) {
		t.Fatalf("GCC golden differs\n%s", encoded)
	}
	if bytes.Contains(encoded, []byte(filepath.VolumeName(gccNormalizationFixture(t).WorkspaceRoot))) {
		t.Fatal("native path leaked")
	}
}

var _ = reflect.DeepEqual
