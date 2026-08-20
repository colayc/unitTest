//go:build windows

package coveragellvm

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"unit-test-ide.local/test-service/internal/toolchain"
)

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
	return toolchain.Instance{
		ID: "verified-clang-cl", Family: toolchain.FamilyClangCL,
		CCompiler: paths[0], CXXCompiler: paths[0], Version: "20.1.8",
		Coverage: toolchain.CoverageCapability{LLVMProfdata: paths[1], LLVMCov: paths[2]},
	}
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
