//go:build linux

package runtime

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"unit-test-ide.local/test-service/internal/coveragedomain"
	"unit-test-ide.local/test-service/internal/coverageexec"
	"unit-test-ide.local/test-service/internal/coveragenormalize"
	"unit-test-ide.local/test-service/internal/toolchain"
)

func TestLinuxCoverageBundleRootIsExactCanonicalSeam(t *testing.T) {
	root := filepath.Join(t.TempDir(), "bundles", "coverage")
	adapter := gccCoverageAdapter{bundleRoot: root}
	if adapter.bundleRoot != root || !filepath.IsAbs(adapter.bundleRoot) || filepath.Clean(adapter.bundleRoot) != adapter.bundleRoot {
		t.Fatalf("bundle root seam = %q", adapter.bundleRoot)
	}
}

func TestLinuxLLVMAdapterNormalizesPathFreeFunctionLineBranchTotals(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "src", "branch.cpp")
	if err := os.MkdirAll(filepath.Dir(source), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, []byte("int branch(int x) { return x ? 1 : 0; }\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	fixture, err := os.ReadFile("../coverageparser/llvm/testdata/branches.json")
	if err != nil {
		t.Fatal(err)
	}
	encodedPath, err := json.Marshal(source)
	if err != nil {
		t.Fatal(err)
	}
	fixture = []byte(strings.ReplaceAll(string(fixture), `"C:\\workspace\\src\\branch.cpp"`, string(encodedPath)))
	matcher, err := coveragenormalize.NewGlobMatcher([]string{"**/*.cpp"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	adapter := &llvmPreparedCoverageAdapter{}
	doc, _, err := adapter.Normalize(context.Background(), coverageexec.NormalizeInput{
		ProcessOutput: fixture, WorkspaceRoot: root, Matcher: matcher,
		Toolchain: coveragedomain.ToolchainSnapshot{Platform: coveragedomain.PlatformLinux,
			Architecture:      coveragedomain.ArchitectureX64,
			Compiler:          coveragedomain.CompilerSnapshot{Family: coveragedomain.CompilerFamilyClang, Version: "18.1.8"},
			Driver:            coveragedomain.DriverSnapshot{Name: coveragedomain.DriverLLVMCov, Version: "18.1.8"},
			Collector:         coveragedomain.CollectorSnapshot{Name: coveragedomain.CollectorLLVMCov, Version: "18.1.8"},
			NormalizerVersion: "coverage-normalize-v1", InstrumentationFingerprint: strings.Repeat("a", 64)},
		Completeness: coveragedomain.Completeness{Outcome: coveragedomain.OutcomeAvailable},
		Limits:       coveragenormalize.DefaultLimits(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if doc.Summary.Functions.Covered != 1 || doc.Summary.Functions.Total != 1 ||
		doc.Summary.Lines.Covered != 1 || doc.Summary.Lines.Total != 1 ||
		doc.Summary.Branches.Covered != 2 || doc.Summary.Branches.Total != 4 ||
		len(doc.Files) != 1 || doc.Files[0].URI != "src/branch.cpp" {
		t.Fatalf("normalized Linux LLVM document = %#v", doc)
	}
	encodedDoc, err := coveragenormalize.EncodeCanonical(doc)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encodedDoc), root) {
		t.Fatal("canonical Linux LLVM output leaked a host path")
	}
}

func TestLinuxCoverageAdapterSelectsLLVMOnlyForClang(t *testing.T) {
	clang := selectLinuxCoverageAdapter(toolchain.FamilyClang, "/trusted/bundle")
	if _, ok := clang.(llvmCoverageAdapter); !ok {
		t.Fatalf("Linux Clang selected %T, want pinned LLVM adapter", clang)
	}
	gcc := selectLinuxCoverageAdapter(toolchain.FamilyGCC, "/trusted/bundle")
	if _, ok := gcc.(gccCoverageAdapter); !ok {
		t.Fatalf("Linux GCC selected %T, want retained GCC adapter", gcc)
	}
	if _, ok := selectLinuxCoverageAdapter(toolchain.FamilyMSVC, "/trusted/bundle").(unsupportedCoverageAdapter); !ok {
		t.Fatal("unsupported Linux compiler selected a native adapter")
	}
}
