package coveragenormalize

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"unit-test-ide.local/test-service/internal/coveragedomain"
	coveragemodelv1 "unit-test-ide.local/test-service/internal/coveragemodel/v1"
	coverageparserllvm "unit-test-ide.local/test-service/internal/coverageparser/llvm"
)

func TestNormalizeLLVMFiltersSourcesAndBuildsCanonicalMetrics(t *testing.T) {
	input := llvmNormalizationFixture(t)
	got, bindings, err := NormalizeLLVM(input)
	if err != nil {
		t.Fatal(err)
	}
	want := expectedLLVMDocument(coveragemodelv1.Available, []coveragemodelv1.Reason{})
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("NormalizeLLVM() = %#v, want %#v", got, want)
	}
	if len(bindings) != 2 || bindings[0].URI != "src/a.cpp" || bindings[1].URI != "src/z.cpp" ||
		bindings[0].SHA256 != "b6a98d9ce9a2d9149288fa3df42d377c3e42737afdcdaf714e33c0a100b51060" ||
		bindings[0].NativePath == "" {
		t.Fatalf("bindings = %#v", bindings)
	}
}

func TestNormalizeLLVMPartialIncludesOnlyPresentEvidence(t *testing.T) {
	input := llvmNormalizationFixture(t)
	input.Export.Files = input.Export.Files[len(input.Export.Files)-1:]
	input.Completeness = coveragedomain.Completeness{
		Outcome: coveragedomain.OutcomePartial,
		Reasons: []coveragedomain.CompletenessReason{
			coveragedomain.CompletenessReasonTestTimedOut,
			coveragedomain.CompletenessReasonProfileMissingForFailedInvocation,
			coveragedomain.CompletenessReasonTestCrashed,
		},
	}
	got, bindings, err := NormalizeLLVM(input)
	if err != nil {
		t.Fatal(err)
	}
	if len(bindings) != 1 || len(got.Files) != 1 || got.Files[0].URI != "src/a.cpp" {
		t.Fatalf("partial files/bindings = %#v / %#v", got.Files, bindings)
	}
	if got.Summary != (coveragemodelv1.CoverageSummaryV1{
		Branches:  coveragemodelv1.CoverageMetricV1{Covered: 1, Total: 2},
		Functions: coveragemodelv1.CoverageMetricV1{Covered: 1, Total: 1},
		Lines:     coveragemodelv1.CoverageMetricV1{Covered: 1, Total: 2},
	}) {
		t.Fatalf("partial summary = %#v", got.Summary)
	}
	wantReasons := []coveragemodelv1.Reason{
		coveragemodelv1.ProfileMissingForFailedInvocation,
		coveragemodelv1.TestCrashed,
		coveragemodelv1.TestTimedOut,
	}
	if got.Completeness.Outcome != coveragemodelv1.Partial || !reflect.DeepEqual(got.Completeness.Reasons, wantReasons) {
		t.Fatalf("partial completeness = %#v", got.Completeness)
	}
}

func TestNormalizeLLVMRejectsDuplicatePhysicalSourceWithoutPartialOutput(t *testing.T) {
	for _, hardLink := range []bool{false, true} {
		name := "same path"
		if hardLink {
			name = "hard link alias"
		}
		t.Run(name, func(t *testing.T) {
			input := llvmNormalizationFixture(t)
			duplicate := input.Export.Files[len(input.Export.Files)-1]
			if hardLink {
				alias := filepath.Join(input.WorkspaceRoot, "src", "a-hardlink.cpp")
				if err := os.Link(duplicate.NativePath, alias); err != nil {
					t.Fatal(err)
				}
				duplicate.NativePath = alias
			}
			input.Export.Files = append(input.Export.Files, duplicate)
			got, bindings, err := NormalizeLLVM(input)
			if !errors.Is(err, ErrDuplicateSource) {
				t.Fatalf("error = %v, want ErrDuplicateSource", err)
			}
			if !reflect.DeepEqual(got, coveragemodelv1.CoverageDocumentV1{}) || bindings != nil {
				t.Fatalf("partial output = %#v / %#v", got, bindings)
			}
		})
	}
}

func TestNormalizeLLVMLimitsAndInvalidMetricsFailClosed(t *testing.T) {
	input := llvmNormalizationFixture(t)
	input.Limits.MaxFiles = 1
	if got, bindings, err := NormalizeLLVM(input); !errors.Is(err, ErrLimitExceeded) || !reflect.DeepEqual(got, coveragemodelv1.CoverageDocumentV1{}) || bindings != nil {
		t.Fatalf("file limit = %#v / %#v / %v", got, bindings, err)
	}

	input = llvmNormalizationFixture(t)
	input.Export.Files[len(input.Export.Files)-1].Lines[0].Count = -1
	if got, bindings, err := NormalizeLLVM(input); err == nil || !reflect.DeepEqual(got, coveragemodelv1.CoverageDocumentV1{}) || bindings != nil {
		t.Fatalf("invalid count = %#v / %#v / %v", got, bindings, err)
	}
}

func llvmNormalizationFixture(t *testing.T) LLVMInput {
	t.Helper()
	root := t.TempDir()
	paths := map[string]string{
		"z":         filepath.Join(root, "src", "z.cpp"),
		"generated": filepath.Join(root, "generated", "skip.cpp"),
		"git":       filepath.Join(root, ".git", "hidden.cpp"),
		"data":      filepath.Join(root, "data", "fixture.cpp"),
		"build":     filepath.Join(root, "build", "generated.cpp"),
		"a":         filepath.Join(root, "src", "a.cpp"),
	}
	writeSourceFile(t, paths["z"], "zeta\n")
	writeSourceFile(t, paths["a"], "alpha\n")
	for _, name := range []string{"generated", "git", "data", "build"} {
		writeSourceFile(t, paths[name], name+"\n")
	}
	matcher, err := NewGlobMatcher([]string{"**/*.cpp"}, []string{"generated/**"})
	if err != nil {
		t.Fatal(err)
	}
	return LLVMInput{
		Export: coverageparserllvm.Export{Version: "2.0.1", Files: []coverageparserllvm.File{
			{NativePath: paths["z"], Functions: coverageparserllvm.Metric{Covered: 0, Total: 1}, Lines: []coverageparserllvm.Line{{Number: 2, Count: 2}}},
			{NativePath: paths["generated"], Lines: []coverageparserllvm.Line{{Number: 1, Count: 99}}},
			{NativePath: paths["git"], Lines: []coverageparserllvm.Line{{Number: 1, Count: 99}}},
			{NativePath: paths["data"], Lines: []coverageparserllvm.Line{{Number: 1, Count: 99}}},
			{NativePath: paths["build"], Lines: []coverageparserllvm.Line{{Number: 1, Count: 99}}},
			{NativePath: paths["a"], Functions: coverageparserllvm.Metric{Covered: 1, Total: 1}, Lines: []coverageparserllvm.Line{
				{Number: 11, Count: 0},
				{Number: 10, Count: 1, Branches: coverageparserllvm.Metric{Covered: 1, Total: 2}},
			}},
		}},
		WorkspaceRoot: root,
		Matcher:       matcher,
		Toolchain: coveragedomain.ToolchainSnapshot{
			Platform: coveragedomain.PlatformWindows, Architecture: coveragedomain.ArchitectureX64,
			Compiler:          coveragedomain.CompilerSnapshot{Family: coveragedomain.CompilerFamilyClangCL, Version: "18.1.8"},
			Driver:            coveragedomain.DriverSnapshot{Name: coveragedomain.DriverLLVMCov, Version: "18.1.8"},
			Collector:         coveragedomain.CollectorSnapshot{Name: coveragedomain.CollectorLLVMCov, Version: "18.1.8"},
			NormalizerVersion: "1.0.0", InstrumentationFingerprint: strings.Repeat("a", 64),
		},
		Completeness: coveragedomain.Completeness{Outcome: coveragedomain.OutcomeAvailable},
		Limits:       DefaultLimits(),
	}
}

func expectedLLVMDocument(outcome coveragemodelv1.Outcome, reasons []coveragemodelv1.Reason) coveragemodelv1.CoverageDocumentV1 {
	return coveragemodelv1.CoverageDocumentV1{
		Completeness: coveragemodelv1.CoverageCompletenessV1{Outcome: outcome, Reasons: reasons},
		Files: []coveragemodelv1.CoverageFileV1{
			{
				Lines: []coveragemodelv1.CoverageLineV1{
					{Branches: coveragemodelv1.CoverageMetricV1{Covered: 1, Total: 2}, Count: 1, Line: 10},
					{Branches: coveragemodelv1.CoverageMetricV1{}, Count: 0, Line: 11},
				},
				Sha256: "b6a98d9ce9a2d9149288fa3df42d377c3e42737afdcdaf714e33c0a100b51060",
				Summary: coveragemodelv1.CoverageSummaryV1{
					Branches:  coveragemodelv1.CoverageMetricV1{Covered: 1, Total: 2},
					Functions: coveragemodelv1.CoverageMetricV1{Covered: 1, Total: 1},
					Lines:     coveragemodelv1.CoverageMetricV1{Covered: 1, Total: 2},
				},
				URI: "src/a.cpp",
			},
			{
				Lines:  []coveragemodelv1.CoverageLineV1{{Branches: coveragemodelv1.CoverageMetricV1{}, Count: 2, Line: 2}},
				Sha256: "2088d0c4b41022d90f663fa8d8156cb525241b55d30ecdf922c38f94f7efda4c",
				Summary: coveragemodelv1.CoverageSummaryV1{
					Branches:  coveragemodelv1.CoverageMetricV1{},
					Functions: coveragemodelv1.CoverageMetricV1{Covered: 0, Total: 1},
					Lines:     coveragemodelv1.CoverageMetricV1{Covered: 1, Total: 1},
				},
				URI: "src/z.cpp",
			},
		},
		Provenance: coveragemodelv1.CoverageProvenanceV1{
			Architecture:               coveragemodelv1.X64,
			Collector:                  coveragemodelv1.CoverageCollectorV1{Name: coveragemodelv1.PurpleLlvmCov, Version: "18.1.8"},
			Compiler:                   coveragemodelv1.CoverageCompilerV1{Family: coveragemodelv1.ClangCl, Version: "18.1.8"},
			Driver:                     coveragemodelv1.CoverageDriverV1{Name: coveragemodelv1.FluffyLlvmCov, Version: "18.1.8"},
			InstrumentationFingerprint: strings.Repeat("a", 64), NormalizerVersion: "1.0.0", Platform: coveragemodelv1.Windows,
		},
		SchemaVersion: coveragemodelv1.The10,
		Summary: coveragemodelv1.CoverageSummaryV1{
			Branches:  coveragemodelv1.CoverageMetricV1{Covered: 1, Total: 2},
			Functions: coveragemodelv1.CoverageMetricV1{Covered: 1, Total: 2},
			Lines:     coveragemodelv1.CoverageMetricV1{Covered: 2, Total: 3},
		},
	}
}
