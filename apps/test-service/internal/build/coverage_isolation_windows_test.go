//go:build windows

package build

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"unit-test-ide.local/test-service/internal/cmake"
	"unit-test-ide.local/test-service/internal/coveragellvm"
	"unit-test-ide.local/test-service/internal/toolchain"
)

func TestCoverageIsolationRejectsBinaryDirectoryJunctionToBase(t *testing.T) {
	fixture := newCoordinatorFixture(t)
	if err := os.MkdirAll(fixture.profile.BinaryDir, 0o700); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(fixture.dataRoot(), "coverage-base-alias")
	makeJunction(t, alias, fixture.profile.BinaryDir)
	options := coverageIsolationOptions(t, fixture, alias, filepath.Join(fixture.dataRoot(), "coverage-task", "coverage.cmake"))
	if _, err := fixture.coordinator.prepareCoverageOptions(options, fixture.profile); err == nil {
		t.Fatal("coverage isolation accepted a junction resolving to the base binary directory")
	}
}

func TestCoverageIsolationRejectsIncludeThroughJunctionAncestor(t *testing.T) {
	fixture := newCoordinatorFixture(t)
	realParent := filepath.Join(fixture.dataRoot(), "real-task")
	if err := os.MkdirAll(realParent, 0o700); err != nil {
		t.Fatal(err)
	}
	aliasParent := filepath.Join(fixture.dataRoot(), "task-alias")
	makeJunction(t, aliasParent, realParent)
	include := filepath.Join(aliasParent, "coverage.cmake")
	options := coverageIsolationOptions(t, fixture, filepath.Join(fixture.dataRoot(), "coverage-build", "secure"), include)
	if _, err := fixture.coordinator.prepareCoverageOptions(options, fixture.profile); err == nil {
		t.Fatal("coverage isolation accepted an include through a junction ancestor")
	}
}

func TestCoverageDirectoryPinRejectsOrBlocksReplacementAndClosesOnce(t *testing.T) {
	directoryPath := filepath.Join(t.TempDir(), "coverage-build")
	if err := os.MkdirAll(directoryPath, 0o700); err != nil {
		t.Fatal(err)
	}
	directory, err := pinVerifiedDirectory(directoryPath)
	if err != nil {
		t.Fatal(err)
	}
	removeErr := os.Remove(directoryPath)
	if removeErr == nil {
		if err := os.Mkdir(directoryPath, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := directory.Verify(); err == nil {
			t.Fatal("retained directory accepted a replacement identity")
		}
	} else if err := directory.Verify(); err != nil {
		t.Fatalf("blocked replacement damaged retained directory: %v", err)
	}
	if err := directory.Close(); err != nil {
		t.Fatal(err)
	}
	if err := directory.Close(); err != nil {
		t.Fatalf("second Close() = %v", err)
	}
}

func TestCoverageBoundaryToolsetAttachIsSingleOwnerAndFailedAttachRetries(t *testing.T) {
	fixture := newCoordinatorFixture(t)
	instance := coverageToolsetFixture(t, fixture)
	toolset, err := coveragellvm.PinToolset(instance)
	if err != nil {
		t.Fatal(err)
	}
	invalid, err := newExecutionBoundary(fixture.installation, fixture.root, fixture.dataRoot(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := invalid.attachCoverageToolset(toolset); err == nil {
		t.Fatal("toolset attached without a coverage plan")
	}
	if err := invalid.Release(); err != nil {
		t.Fatal(err)
	}
	first := preparedCoverageBoundary(t, fixture, instance.Coverage.ToolsetIdentity, "first")
	second := preparedCoverageBoundary(t, fixture, instance.Coverage.ToolsetIdentity, "second")
	defer second.Release()
	if err := first.attachCoverageToolset(toolset); err != nil {
		t.Fatalf("attach after failed-attachment rollback = %v", err)
	}
	if err := second.attachCoverageToolset(toolset); err == nil {
		t.Fatal("same retained toolset attached to two boundaries")
	}
	if err := first.Release(); err != nil {
		t.Fatal(err)
	}
	if err := first.Release(); err != nil {
		t.Fatalf("second boundary release = %v", err)
	}
	if err := toolset.Verify(); err == nil {
		t.Fatal("owning boundary release did not close the toolset")
	}
}

func coverageToolsetFixture(t *testing.T, fixture *coordinatorFixture) toolchain.Instance {
	t.Helper()
	root := filepath.Join(fixture.dataRoot(), "llvm-attach", "bin")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	paths := []string{filepath.Join(root, "clang-cl.exe"), filepath.Join(root, "llvm-profdata.exe"), filepath.Join(root, "llvm-cov.exe")}
	for index, path := range paths {
		if err := os.WriteFile(path, []byte(strings.Repeat(string(rune('a'+index)), 128)), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	instance := toolchain.Instance{ID: "coverage-attach", Family: toolchain.FamilyClangCL, CCompiler: paths[0], CXXCompiler: paths[0], Version: "20.1.8", Coverage: toolchain.CoverageCapability{LLVMProfdata: paths[1], LLVMCov: paths[2]}}
	return testCoverageToolchainEvidence(t, instance)
}

func preparedCoverageBoundary(t *testing.T, fixture *coordinatorFixture, toolsetIdentity, suffix string) *executionBoundary {
	t.Helper()
	binaryDir := filepath.Join(fixture.dataRoot(), "coverage-attach-build", suffix)
	if err := os.MkdirAll(binaryDir, 0o700); err != nil {
		t.Fatal(err)
	}
	directory, err := pinVerifiedDirectory(binaryDir)
	if err != nil {
		t.Fatal(err)
	}
	boundary, err := newExecutionBoundary(fixture.installation, fixture.root, fixture.dataRoot(), nil)
	if err != nil {
		_ = directory.Close()
		t.Fatal(err)
	}
	if err := boundary.attachCoverageDirectory(directory); err != nil {
		_ = directory.Close()
		_ = boundary.Release()
		t.Fatal(err)
	}
	include := filepath.Join(fixture.dataRoot(), "coverage-attach-task", suffix, "coverage.cmake")
	options := coverageIsolationOptions(t, fixture, binaryDir, include)
	options.BinaryDirIdentity = directory.identity
	options.ToolsetIdentity = toolsetIdentity
	if err := boundary.attachCoveragePlan(options); err != nil {
		_ = boundary.Release()
		t.Fatal(err)
	}
	return boundary
}

func coverageIsolationOptions(t *testing.T, fixture *coordinatorFixture, binaryDir, include string) *CoverageOptions {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(include), 0o700); err != nil {
		t.Fatal(err)
	}
	contents := []byte("trusted instrumentation\n")
	if err := os.WriteFile(include, contents, 0o400); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(contents)
	return &CoverageOptions{
		BinaryDir:                  binaryDir,
		TopLevelInclude:            cmake.FingerprintFile{Path: include, Identity: strings.Repeat("4", 64), SHA256: hex.EncodeToString(sum[:])},
		InstrumentationFingerprint: strings.Repeat("4", 64),
	}
}

func makeJunction(t *testing.T, alias, target string) {
	t.Helper()
	command := exec.Command(os.Getenv("ComSpec"), "/d", "/s", "/c", "mklink", "/J", alias, target)
	if output, err := command.CombinedOutput(); err != nil {
		t.Skipf("directory junctions are unavailable: %v: %s", err, output)
	}
}
