//go:build !windows

package coveragegcc

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
	"testing"

	"unit-test-ide.local/test-service/internal/coverageplatform"
	"unit-test-ide.local/test-service/internal/toolchain"
)

func TestGCCPinToolsetRetainsDistinctCompilerAndGCovCapabilities(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux-only GCC toolset")
	}
	instance := gccToolchainFixture(t)
	toolset, err := PinToolset(instance)
	if err != nil {
		t.Fatal(err)
	}
	defer toolset.Close()
	if toolset.Version() != "13.2.0" || toolset.Identity() != instance.Coverage.ToolsetIdentity {
		t.Fatalf("retained toolset = %#v", toolset)
	}
	tools := toolset.Tools()
	if len(tools) != 3 || tools[0].Path() != instance.CCompiler || tools[1].Path() != instance.CXXCompiler || tools[2].Path() != instance.Coverage.GCov {
		t.Fatalf("Tools() = %#v", tools)
	}
	tools[0] = nil
	if toolset.Tools()[0] == nil || toolset.Verify() != nil {
		t.Fatal("tool capability slice aliases or cannot verify")
	}
	var contract coverageplatform.Toolset = toolset
	if contract.CCompiler().Path() != instance.CCompiler || contract.CXXCompiler().Path() != instance.CXXCompiler {
		t.Fatal("compiler capabilities were not preserved")
	}
}

func TestGCCPinToolsetRejectsReplacementAfterDiscovery(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux-only GCC toolset")
	}
	instance := gccToolchainFixture(t)
	if err := os.WriteFile(instance.Coverage.GCov, []byte("replacement"), 0o755); err != nil {
		t.Fatal(err)
	}
	if toolset, err := PinToolset(instance); err == nil {
		_ = toolset.Close()
		t.Fatal("PinToolset accepted a replacement after discovery")
	}
}

func TestGCCPinToolsetRejectsSymlinkAndNonRegularPaths(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux-only GCC toolset")
	}
	for name, alter := range map[string]func(*toolchain.Instance){
		"symlink": func(instance *toolchain.Instance) {
			link := filepath.Join(filepath.Dir(instance.Coverage.GCov), "gcov-link")
			if err := os.Symlink(instance.Coverage.GCov, link); err != nil {
				t.Fatal(err)
			}
			instance.Coverage.GCov = link
		},
		"directory": func(instance *toolchain.Instance) {
			directory := filepath.Join(filepath.Dir(instance.Coverage.GCov), "gcov-directory")
			if err := os.Mkdir(directory, 0o755); err != nil {
				t.Fatal(err)
			}
			instance.Coverage.GCov = directory
		},
	} {
		t.Run(name, func(t *testing.T) {
			instance := gccToolchainFixture(t)
			alter(&instance)
			if toolset, err := PinToolset(instance); err == nil {
				_ = toolset.Close()
				t.Fatal("PinToolset accepted an unsafe path")
			}
		})
	}
}

func gccToolchainFixture(t *testing.T) toolchain.Instance {
	t.Helper()
	root := filepath.Join(t.TempDir(), "bin")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	paths := []string{filepath.Join(root, "gcc"), filepath.Join(root, "g++"), filepath.Join(root, "gcov")}
	for index, path := range paths {
		if err := os.WriteFile(path, []byte{byte('a' + index)}, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	evidence := make([]toolchain.ExecutableEvidence, len(paths))
	for index, path := range paths {
		evidence[index] = gccEvidence(t, path)
	}
	identity := toolchain.GCCToolsetIdentity("13.2.0", paths, evidence)
	return toolchain.Instance{Family: toolchain.FamilyGCC, CCompiler: paths[0], CXXCompiler: paths[1], Version: "13.2.0", TargetArchitecture: "x64", Coverage: toolchain.CoverageCapability{GCov: paths[2], CompilerEvidence: evidence[0], CXXCompilerEvidence: evidence[1], GCovEvidence: evidence[2], GCovVersion: "13.2.0", ToolsetIdentity: identity}}
}

func gccEvidence(t *testing.T, path string) toolchain.ExecutableEvidence {
	t.Helper()
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		t.Fatal("missing Unix stat")
	}
	sum := sha256.Sum256(contents)
	return toolchain.ExecutableEvidence{FileIdentity: "unix:" + strconvFormat(uint64(stat.Dev)) + ":" + strconvFormat(uint64(stat.Ino)), SHA256: hex.EncodeToString(sum[:])}
}
