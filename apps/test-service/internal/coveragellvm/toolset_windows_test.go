//go:build windows

package coveragellvm

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"golang.org/x/sys/windows"

	"unit-test-ide.local/test-service/internal/toolchain"
)

func TestLLVMToolsetRejectsReplacementBetweenDiscoveryAndPin(t *testing.T) {
	instance := llvmToolchainFixture(t)
	instance.Coverage = coverageEvidenceForTest(t, instance)
	if err := os.Remove(instance.Coverage.LLVMProfdata); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(instance.Coverage.LLVMProfdata, []byte(strings.Repeat("z", 128)), 0o700); err != nil {
		t.Fatal(err)
	}
	if toolset, err := PinToolset(instance); err == nil {
		_ = toolset.Close()
		t.Fatal("PinToolset accepted a replacement created after discovery evidence")
	}
}

func TestLLVMToolsetIdentityChangesWithEachRetainedExecutable(t *testing.T) {
	first := llvmToolchainFixture(t)
	first.Coverage = coverageEvidenceForTest(t, first)
	toolset, err := PinToolset(first)
	if err != nil {
		t.Fatal(err)
	}
	defer toolset.Close()
	if toolset.Identity() != first.Coverage.ToolsetIdentity {
		t.Fatalf("Identity() = %q, want discovery identity %q", toolset.Identity(), first.Coverage.ToolsetIdentity)
	}
	second := llvmToolchainFixture(t)
	if err := os.WriteFile(second.Coverage.LLVMCov, []byte(strings.Repeat("q", 128)), 0o700); err != nil {
		t.Fatal(err)
	}
	second.Coverage = coverageEvidenceForTest(t, second)
	if second.Coverage.ToolsetIdentity == first.Coverage.ToolsetIdentity {
		t.Fatal("toolset identity ignored llvm-cov identity")
	}
}

func TestLLVMToolsetSingleOwnerClaimRollsBackAndClosesExactlyOnce(t *testing.T) {
	instance := llvmToolchainFixture(t)
	toolset, err := PinToolset(instance)
	if err != nil {
		t.Fatal(err)
	}
	first, err := toolset.ClaimOwnership()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := toolset.ClaimOwnership(); err == nil {
		t.Fatal("second owner claimed one retained toolset")
	}
	first.Rollback()
	retry, err := toolset.ClaimOwnership()
	if err != nil {
		t.Fatalf("claim after failed attachment rollback = %v", err)
	}
	retry.Commit()
	if _, err := toolset.ClaimOwnership(); err == nil {
		t.Fatal("committed ownership was attachable again")
	}
	if err := toolset.Close(); err != nil {
		t.Fatal(err)
	}
	if err := toolset.Close(); err != nil {
		t.Fatalf("second Close() = %v", err)
	}
}

func TestLLVMToolsetRetainsSameInstallationAndClosesExactlyOnce(t *testing.T) {
	instance := llvmToolchainFixture(t)
	toolset, err := PinToolset(instance)
	if err != nil {
		t.Fatal(err)
	}
	if toolset.Version() != instance.Version ||
		!strings.EqualFold(toolset.Compiler().Path(), instance.CXXCompiler) ||
		!strings.EqualFold(toolset.Profdata().Path(), instance.Coverage.LLVMProfdata) ||
		!strings.EqualFold(toolset.Cov().Path(), instance.Coverage.LLVMCov) {
		t.Fatalf("pinned toolset paths/version do not preserve discovery snapshot: %#v", toolset)
	}
	if err := toolset.Verify(); err != nil {
		t.Fatalf("Verify() = %v", err)
	}
	if err := os.Remove(instance.CXXCompiler); err == nil {
		if err := os.WriteFile(instance.CXXCompiler, []byte("replacement"), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := toolset.Verify(); err == nil {
			t.Fatal("Verify accepted replacement while retained")
		}
	} else if err := toolset.Verify(); err != nil {
		t.Fatalf("blocked replacement damaged retained toolset: %v", err)
	}
	if err := toolset.Close(); err != nil {
		t.Fatal(err)
	}
	if err := toolset.Close(); err != nil {
		t.Fatalf("second Close() = %v", err)
	}
	if err := toolset.Verify(); err == nil || toolset.Compiler().Verify() == nil {
		t.Fatal("closed toolset remained verifiable")
	}
	for _, path := range []string{instance.CXXCompiler, instance.Coverage.LLVMProfdata, instance.Coverage.LLVMCov} {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			t.Fatalf("retained handle for %q was not closed: %v", path, err)
		}
	}
}

func TestLLVMToolsetSerializesConcurrentHandleVerification(t *testing.T) {
	toolset, err := PinToolset(llvmToolchainFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	defer toolset.Close()
	start := make(chan struct{})
	errorsSeen := make(chan error, 32)
	var workers sync.WaitGroup
	for worker := 0; worker < cap(errorsSeen); worker++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			for attempt := 0; attempt < 16; attempt++ {
				if err := toolset.Verify(); err != nil {
					errorsSeen <- err
					return
				}
			}
		}()
	}
	close(start)
	workers.Wait()
	close(errorsSeen)
	for err := range errorsSeen {
		t.Fatalf("concurrent Verify() = %v", err)
	}
}

func TestLLVMToolsetRejectsUnverifiedOrCrossInstallationDescriptors(t *testing.T) {
	base := llvmToolchainFixture(t)
	tests := []struct {
		name string
		edit func(*toolchain.Instance)
	}{
		{"family", func(value *toolchain.Instance) { value.Family = toolchain.FamilyMSVC }},
		{"version", func(value *toolchain.Instance) { value.Version = "" }},
		{"C compiler mismatch", func(value *toolchain.Instance) { value.CCompiler = value.Coverage.LLVMProfdata }},
		{"profdata missing", func(value *toolchain.Instance) { value.Coverage.LLVMProfdata = "" }},
		{"cov missing", func(value *toolchain.Instance) { value.Coverage.LLVMCov = "" }},
		{"compiler basename", func(value *toolchain.Instance) {
			value.CCompiler = renamedTool(t, value.CCompiler, "clang.exe")
			value.CXXCompiler = value.CCompiler
		}},
		{"profdata basename", func(value *toolchain.Instance) {
			value.Coverage.LLVMProfdata = renamedTool(t, value.Coverage.LLVMProfdata, "profdata.exe")
		}},
		{"cov basename", func(value *toolchain.Instance) {
			value.Coverage.LLVMCov = renamedTool(t, value.Coverage.LLVMCov, "cov.exe")
		}},
		{"different installation", func(value *toolchain.Instance) {
			value.Coverage.LLVMCov = copiedTool(t, value.Coverage.LLVMCov, filepath.Join(t.TempDir(), "llvm-cov.exe"))
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			value := base
			test.edit(&value)
			if toolset, err := PinToolset(value); err == nil {
				_ = toolset.Close()
				t.Fatal("PinToolset accepted an unverified descriptor")
			}
		})
	}
}

func TestLLVMToolsetRejectsHardlinkAndJunctionAliases(t *testing.T) {
	t.Run("hardlink", func(t *testing.T) {
		instance := llvmToolchainFixture(t)
		other := filepath.Join(filepath.Dir(instance.CXXCompiler), "clang-cl-original.exe")
		if err := os.Link(instance.CXXCompiler, other); err != nil {
			t.Skipf("hardlinks are unavailable: %v", err)
		}
		if toolset, err := PinToolset(instance); err == nil {
			_ = toolset.Close()
			t.Fatal("PinToolset accepted a multiply-linked executable")
		}
	})

	t.Run("junction", func(t *testing.T) {
		instance := llvmToolchainFixture(t)
		realRoot := filepath.Dir(instance.CXXCompiler)
		aliasRoot := filepath.Join(t.TempDir(), "llvm-alias")
		command := exec.Command(os.Getenv("ComSpec"), "/d", "/s", "/c", "mklink", "/J", aliasRoot, realRoot)
		if output, err := command.CombinedOutput(); err != nil {
			t.Skipf("directory junctions are unavailable: %v: %s", err, output)
		}
		instance.CCompiler = filepath.Join(aliasRoot, "clang-cl.exe")
		instance.CXXCompiler = instance.CCompiler
		instance.Coverage.LLVMProfdata = filepath.Join(aliasRoot, "llvm-profdata.exe")
		instance.Coverage.LLVMCov = filepath.Join(aliasRoot, "llvm-cov.exe")
		if toolset, err := PinToolset(instance); err == nil {
			_ = toolset.Close()
			t.Fatal("PinToolset accepted a junction installation alias")
		}
	})
}

func llvmToolchainFixture(t *testing.T) toolchain.Instance {
	t.Helper()
	root := filepath.Join(t.TempDir(), "LLVM", "bin")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	paths := []string{
		filepath.Join(root, "clang-cl.exe"),
		filepath.Join(root, "llvm-profdata.exe"),
		filepath.Join(root, "llvm-cov.exe"),
	}
	for index, path := range paths {
		if err := os.WriteFile(path, []byte(strings.Repeat(string(rune('a'+index)), 128)), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	instance := toolchain.Instance{
		ID: "verified-clang-cl", Family: toolchain.FamilyClangCL,
		CCompiler: paths[0], CXXCompiler: paths[0], Version: "20.1.8",
		Coverage: toolchain.CoverageCapability{LLVMProfdata: paths[1], LLVMCov: paths[2]},
	}
	instance.Coverage = coverageEvidenceForTest(t, instance)
	return instance
}

func coverageEvidenceForTest(t *testing.T, instance toolchain.Instance) toolchain.CoverageCapability {
	t.Helper()
	result := instance.Coverage
	paths := []string{instance.CXXCompiler, result.LLVMProfdata, result.LLVMCov}
	evidence := make([]toolchain.ExecutableEvidence, len(paths))
	for index, path := range paths {
		contents, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(contents)
		pointer, err := windows.UTF16PtrFromString(path)
		if err != nil {
			t.Fatal(err)
		}
		handle, err := windows.CreateFile(pointer, windows.FILE_READ_ATTRIBUTES, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
		if err != nil {
			t.Fatal(err)
		}
		var info windows.ByHandleFileInformation
		err = windows.GetFileInformationByHandle(handle, &info)
		_ = windows.CloseHandle(handle)
		if err != nil {
			t.Fatal(err)
		}
		evidence[index] = toolchain.ExecutableEvidence{
			FileIdentity: windowsFileIdentity(info.VolumeSerialNumber, info.FileIndexHigh, info.FileIndexLow),
			SHA256:       hex.EncodeToString(sum[:]),
		}
	}
	result.CompilerEvidence = evidence[0]
	result.ProfdataEvidence = evidence[1]
	result.CovEvidence = evidence[2]
	result.ToolsetIdentity = toolchain.LLVMToolsetIdentity(instance.Version, paths, evidence)
	return result
}

func windowsFileIdentity(volume, high, low uint32) string {
	return fmt.Sprintf("windows:%08x:%08x%08x", volume, high, low)
}

func renamedTool(t *testing.T, source, name string) string {
	t.Helper()
	return copiedTool(t, source, filepath.Join(filepath.Dir(source), name))
}

func copiedTool(t *testing.T, source, destination string) string {
	t.Helper()
	contents, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(destination, contents, 0o700); err != nil {
		t.Fatal(err)
	}
	return destination
}
