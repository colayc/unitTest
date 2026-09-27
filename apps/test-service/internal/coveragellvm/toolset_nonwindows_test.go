//go:build linux

package coveragellvm

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"unit-test-ide.local/test-service/internal/toolchain"
)

func TestLinuxPinToolsetKeepsDistinctCompilersAndRejectsReplacement(t *testing.T) {
	instance := linuxLLVMFixture(t)
	toolset, err := PinToolset(instance)
	if err != nil {
		t.Fatal(err)
	}
	defer toolset.Close()
	if toolset.CCompiler().Path() != instance.CCompiler || toolset.CXXCompiler().Path() != instance.CXXCompiler || toolset.CCompiler().Path() == toolset.CXXCompiler().Path() {
		t.Fatal("C and C++ compiler paths were aliased")
	}
	if len(toolset.Tools()) != 4 || toolset.Verify() != nil {
		t.Fatal("four-tool pin did not verify")
	}
	if err := os.WriteFile(instance.CXXCompiler, []byte("replacement"), 0o755); err != nil {
		t.Fatal(err)
	}
	if toolset.Verify() == nil {
		t.Fatal("mutated clang++ remained verified")
	}
}

func TestLinuxPinToolsetRejectsMissingMixedAndSyntheticEvidence(t *testing.T) {
	for _, variant := range []string{"missing-clang++", "mixed-root", "synthetic", "mixed-version"} {
		t.Run(variant, func(t *testing.T) {
			instance := linuxLLVMFixture(t)
			switch variant {
			case "missing-clang++":
				instance.CXXCompiler = ""
			case "mixed-root":
				instance.Coverage.LLVMCov = filepath.Join(t.TempDir(), "llvm-cov")
			case "synthetic":
				instance.Coverage.CXXCompilerEvidence.FileIdentity = "synthetic"
			case "mixed-version":
				instance.Version = "19.0.0"
			}
			if pinned, err := PinToolset(instance); err == nil {
				pinned.Close()
				t.Fatal("accepted invalid LLVM descriptor")
			}
		})
	}
}

func linuxLLVMFixture(t *testing.T) toolchain.Instance {
	t.Helper()
	root := t.TempDir()
	paths := []string{filepath.Join(root, "clang"), filepath.Join(root, "clang++"), filepath.Join(root, "llvm-profdata"), filepath.Join(root, "llvm-cov")}
	evidence := make([]toolchain.ExecutableEvidence, len(paths))
	roles := []string{"clang", "clang++", "llvm-profdata", "llvm-cov"}
	tools := make([]toolchain.LLVMToolEvidence, len(paths))
	for index, path := range paths {
		content := []byte(roles[index])
		if err := os.WriteFile(path, content, 0o755); err != nil {
			t.Fatal(err)
		}
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		stat := info.Sys().(*syscall.Stat_t)
		sum := sha256.Sum256(content)
		evidence[index] = toolchain.ExecutableEvidence{FileIdentity: fmt.Sprintf("unix:%d:%d", stat.Dev, stat.Ino), SHA256: hex.EncodeToString(sum[:])}
		tools[index] = toolchain.LLVMToolEvidence{Role: roles[index], Path: path, Evidence: evidence[index]}
	}
	identity, err := toolchain.LLVMToolsetIdentityForTools("18.1.3", tools)
	if err != nil {
		t.Fatal(err)
	}
	return toolchain.Instance{ID: "linux-clang", Family: toolchain.FamilyClang, Version: "18.1.3", CCompiler: paths[0], CXXCompiler: paths[1], Coverage: toolchain.CoverageCapability{LLVMProfdata: paths[2], LLVMCov: paths[3], CompilerEvidence: evidence[0], CXXCompilerEvidence: evidence[1], ProfdataEvidence: evidence[2], CovEvidence: evidence[3], ToolsetIdentity: identity}}
}
