//go:build linux

package coveragellvm

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"unit-test-ide.local/test-service/internal/testrun"
)

func TestLinuxInstrumentationUsesDistinctCompilersAndFingerprint(t *testing.T) {
	root := filepath.Join(t.TempDir(), "task")
	makeOwnerOnlyInstrumentationRoot(t, root)
	got, err := WriteInstrumentation(root)
	if err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(got.IncludePath)
	if err != nil {
		t.Fatal(err)
	}
	windowsSHA := sha256.Sum256([]byte(goldenInstrumentation))
	windowsFingerprint := sha256.Sum256([]byte(instrumentationVersion + "\x00" + hex.EncodeToString(windowsSHA[:])))
	if string(content) != goldenLinuxInstrumentation ||
		got.Fingerprint == hex.EncodeToString(windowsFingerprint[:]) ||
		strings.Contains(string(content), "clang-cl") {
		t.Fatalf("Linux instrumentation = %q fingerprint %q", content, got.Fingerprint)
	}
}

func TestLinuxLLVMNonzeroTestExitRetainsProducedProfile(t *testing.T) {
	root := newProfileRoot(t)
	expectation := profileExpectation(1, 1)
	writeExpandedProfile(t, root, expectation, "42", "module", []byte("bounded raw profile"))
	manifest, err := SealProfiles(root, []testrun.ProfileExpectation{expectation},
		[]testrun.InvocationOutcome{profileOutcome(1, 1, 7)})
	if err != nil {
		t.Fatal(err)
	}
	defer closeManifest(t, &manifest)
	if len(manifest.Entries) != 1 || len(manifest.PartialReasons) != 0 {
		t.Fatalf("nonzero exit lost produced profile: %#v", manifest)
	}
}

func TestPlanLinuxInstrumentationKeepsPinnedCAndCXXSeparate(t *testing.T) {
	toolset, err := PinToolset(linuxLLVMFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	defer toolset.Close()
	root := filepath.Join(t.TempDir(), "task")
	makeOwnerOnlyInstrumentationRoot(t, root)
	plan, err := PlanInstrumentation(toolset, BuildRequest{TaskRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	if plan.CCompiler.Path() != toolset.CCompiler().Path() ||
		plan.CXXCompiler.Path() != toolset.CXXCompiler().Path() ||
		plan.CCompiler.Path() == plan.CXXCompiler.Path() ||
		plan.Instrumentation.Fingerprint != InstrumentationFingerprint() {
		t.Fatalf("instrumentation plan did not retain separate compiler pins: %#v", plan)
	}
	if len(plan.CompileFlags) != 2 || plan.CompileFlags[0] != "-fprofile-instr-generate" ||
		plan.CompileFlags[1] != "-fcoverage-mapping" || len(plan.LinkFlags) != 1 ||
		plan.LinkFlags[0] != "-fprofile-instr-generate" {
		t.Fatalf("Linux LLVM flags = %#v / %#v", plan.CompileFlags, plan.LinkFlags)
	}
}
