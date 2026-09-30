//go:build linux && native_llvm_fixture

package coveragellvm

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"

	"unit-test-ide.local/test-service/internal/coveragedomain"
	coveragemodelv1 "unit-test-ide.local/test-service/internal/coveragemodel/v1"
	"unit-test-ide.local/test-service/internal/coveragenormalize"
	"unit-test-ide.local/test-service/internal/coverageparser/llvm"
	"unit-test-ide.local/test-service/internal/coveragerun"
	"unit-test-ide.local/test-service/internal/probe"
	"unit-test-ide.local/test-service/internal/task"
	"unit-test-ide.local/test-service/internal/testrun"
	"unit-test-ide.local/test-service/internal/toolchain"
)

// Run explicitly with: UTIDE_NATIVE_LLVM_BUNDLE=/approved/offline/bundle
// go test -tags native_llvm_fixture ./apps/test-service/internal/coveragellvm ./apps/test-service/internal/coverageexec -run '^TestNativeLinuxLLVMFixture' -count=1
// Missing bundle/tools are failures, not skips. The ordinary suite never runs
// this native-tool assertion.
func TestNativeLinuxLLVMFixture(t *testing.T) {
	toolset := nativeFixtureToolset(t)
	defer toolset.Close()
	workspace := t.TempDir()
	sourceDir, buildDir := filepath.Join(workspace, "src"), filepath.Join(workspace, "build")
	for _, dir := range []string{sourceDir, buildDir} {
		if err := os.Mkdir(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	cSource := filepath.Join(sourceDir, "helper.c")
	cxxSource := filepath.Join(sourceDir, "main.cpp")
	if err := os.WriteFile(cSource, []byte("int c_branch(int x) { if (x > 0) return 7; return 3; }\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cxxSource, []byte("extern \"C\" int c_branch(int);\nint cpp_branch(int x) { if (x == 1) return c_branch(x); return 0; }\nint main() { return cpp_branch(1) == 7 ? 0 : 1; }\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cObject, cxxObject, binary := filepath.Join(buildDir, "helper.o"), filepath.Join(buildDir, "main.o"), filepath.Join(buildDir, "fixture")
	nativeRun(t, toolset.CCompiler(), []string{"-fprofile-instr-generate", "-fcoverage-mapping", "-c", cSource, "-o", cObject}, nil)
	nativeRun(t, toolset.CXXCompiler(), []string{"-fprofile-instr-generate", "-fcoverage-mapping", "-c", cxxSource, "-o", cxxObject}, nil)
	nativeRun(t, toolset.CXXCompiler(), []string{"-fprofile-instr-generate", cObject, cxxObject, "-o", binary}, nil)
	first := nativeCoverageDocument(t, toolset, workspace, binary, 1)
	second := nativeCoverageDocument(t, toolset, workspace, binary, 2)
	if !reflect.DeepEqual(first.Summary, second.Summary) || first.Summary.Functions.Total < 2 || first.Summary.Lines.Total < 3 || first.Summary.Branches.Total < 2 || first.Summary.Functions.Covered == 0 || first.Summary.Lines.Covered == 0 || first.Summary.Branches.Covered == 0 {
		t.Fatalf("unstable or empty normalized totals: first=%#v second=%#v", first.Summary, second.Summary)
	}
	if len(first.Files) != 2 {
		t.Fatalf("normalized files = %d, want C and C++", len(first.Files))
	}
	for _, file := range first.Files {
		if strings.Contains(file.URI, workspace) || strings.HasPrefix(file.URI, "/") {
			t.Fatalf("host path leaked in normalized URI: %q", file.URI)
		}
	}
	canonical, err := coveragenormalize.EncodeCanonical(first)
	if err != nil || bytes.Contains(canonical, []byte(workspace)) {
		t.Fatalf("normalized coverage retained host path: %v", err)
	}
	// This marker is consumed only after go test itself passes. It carries
	// observed native coverage totals and digests, never host paths or source.
	evidence, err := json.Marshal(struct {
		CompilerVersion       string      `json:"compilerVersion"`
		CompilerSha256        string      `json:"compilerSha256"`
		Summary               interface{} `json:"summary"`
		CoverageDocumentSha256 string      `json:"coverageDocumentSha256"`
	}{toolset.Version(), toolset.compiler.sha256, first.Summary, fmt.Sprintf("%x", sha256.Sum256(canonical))})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("UTIDE_NATIVE_LLVM_EVIDENCE=%s", evidence)
}

func nativeFixtureToolset(t *testing.T) *Toolset {
	t.Helper()
	root := os.Getenv("UTIDE_NATIVE_LLVM_BUNDLE")
	if !filepath.IsAbs(root) {
		t.Fatal("UTIDE_NATIVE_LLVM_BUNDLE must name an approved absolute offline bundle")
	}
	root = filepath.Clean(root)
	paths := []string{filepath.Join(root, "bin", "clang"), filepath.Join(root, "bin", "clang++"), filepath.Join(root, "bin", "llvm-profdata"), filepath.Join(root, "bin", "llvm-cov")}
	roles := []string{"clang", "clang++", "llvm-profdata", "llvm-cov"}
	evidence := make([]toolchain.ExecutableEvidence, 4)
	tools := make([]toolchain.LLVMToolEvidence, 4)
	version := ""
	for i, path := range paths {
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() || info.Size() > maximumLLVMToolBytes {
			t.Fatalf("approved bundle lacks regular bounded %s", roles[i])
		}
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok {
			t.Fatal("missing native tool identity")
		}
		file, err := os.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		digest := sha256.New()
		_, err = io.Copy(digest, io.LimitReader(file, maximumLLVMToolBytes+1))
		closeErr := file.Close()
		if err != nil || closeErr != nil {
			t.Fatal(errors.Join(err, closeErr))
		}
		result, err := probe.NewRunner().Run(context.Background(), probe.Spec{Executable: path, Args: []string{"--version"}, Env: []string{}, Timeout: 5 * time.Second, MaxOutput: 64 * 1024})
		if err != nil || result.ExitCode != 0 {
			t.Fatalf("approved %s version probe failed: %v", roles[i], err)
		}
		found, err := toolchain.LLVMVersionFromBanner(roles[i], result.Stdout)
		if err != nil || version != "" && version != found {
			t.Fatalf("approved %s version mismatch: %v", roles[i], err)
		}
		version = found
		evidence[i] = toolchain.ExecutableEvidence{FileIdentity: fmt.Sprintf("unix:%d:%d", stat.Dev, stat.Ino), SHA256: hex.EncodeToString(digest.Sum(nil))}
		tools[i] = toolchain.LLVMToolEvidence{Role: roles[i], Path: path, Evidence: evidence[i]}
	}
	identity, err := toolchain.LLVMToolsetIdentityForTools(version, tools)
	if err != nil {
		t.Fatal(err)
	}
	instance := toolchain.Instance{Family: toolchain.FamilyClang, Version: version, CCompiler: paths[0], CXXCompiler: paths[1], Coverage: toolchain.CoverageCapability{LLVMProfdata: paths[2], LLVMCov: paths[3], CompilerEvidence: evidence[0], CXXCompilerEvidence: evidence[1], ProfdataEvidence: evidence[2], CovEvidence: evidence[3], ToolsetIdentity: identity}}
	toolset, err := PinToolset(instance)
	if err != nil {
		t.Fatalf("approved bundle could not be pinned: %v", err)
	}
	return toolset
}

func nativeRun(t *testing.T, executable interface {
	Path() string
	Verify() error
}, args, env []string) []byte {
	t.Helper()
	if err := executable.Verify(); err != nil {
		t.Fatal(err)
	}
	if env == nil {
		env = []string{"PATH=" + filepath.Dir(executable.Path())}
	}
	result, err := probe.NewRunner().Run(context.Background(), probe.Spec{Executable: executable.Path(), Args: args, Env: env, Timeout: 30 * time.Second, MaxOutput: 16 << 20})
	if err != nil || result.ExitCode != 0 {
		t.Fatalf("pinned native process failed: exit=%d err=%v stderr=%q", result.ExitCode, err, result.Stderr)
	}
	if err := executable.Verify(); err != nil {
		t.Fatal(err)
	}
	return result.Stdout
}

type nativeFixtureBinary struct {
	path string
	info os.FileInfo
}

func (value nativeFixtureBinary) Path() string { return value.path }
func (value nativeFixtureBinary) Verify() error {
	current, err := os.Stat(value.path)
	if err != nil || !os.SameFile(current, value.info) {
		return errors.New("native fixture binary changed")
	}
	return nil
}

func nativeCoverageDocument(t *testing.T, toolset *Toolset, workspace, binary string, sequence int) coveragemodelv1.CoverageDocumentV1 {
	t.Helper()
	profileRoot := filepath.Join(workspace, fmt.Sprintf("profiles-%d", sequence))
	if err := os.Mkdir(profileRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	allocator, err := NewProfileAllocator(profileRoot)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if closer, ok := allocator.(io.Closer); ok {
			_ = closer.Close()
		}
	}()
	expectation := testrun.ProfileExpectation{InvocationID: "native-fixture", Iteration: 1, Sequence: 1}
	expectation, spec, err := allocator.Decorate(expectation, task.ProcessSpec{Executable: binary, Dir: workspace})
	if err != nil {
		t.Fatal(err)
	}
	if result, err := probe.NewRunner().Run(context.Background(), probe.Spec{Executable: binary, Dir: workspace, Env: spec.Env, Timeout: 10 * time.Second, MaxOutput: 64 * 1024}); err != nil || result.ExitCode != 0 {
		t.Fatalf("instrumented fixture failed: %v exit=%d", err, result.ExitCode)
	}
	manifest, err := SealProfiles(profileRoot, []testrun.ProfileExpectation{expectation}, []testrun.InvocationOutcome{{InvocationID: expectation.InvocationID, Iteration: 1, ExitCode: 0}})
	if err != nil {
		t.Fatal(err)
	}
	defer manifest.Close()
	info, err := os.Stat(binary)
	if err != nil {
		t.Fatal(err)
	}
	merge, export, err := BuildCollectorInvocation(toolset, manifest, []coveragerun.TrustedPath{nativeFixtureBinary{binary, info}})
	if err != nil {
		t.Fatal(err)
	}
	nativeRun(t, toolset.Profdata(), merge.Args, []string{})
	output := nativeRun(t, toolset.Cov(), export.Args, []string{})
	parsed, err := llvm.Parse(bytes.NewReader(output), llvm.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	matcher, err := coveragenormalize.NewGlobMatcher([]string{"src/**"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	normalized, _, err := coveragenormalize.NormalizeLLVM(coveragenormalize.LLVMInput{Export: parsed, WorkspaceRoot: workspace, Matcher: matcher, Toolchain: coveragedomain.ToolchainSnapshot{Platform: coveragedomain.PlatformLinux, Architecture: coveragedomain.ArchitectureX64, Compiler: coveragedomain.CompilerSnapshot{Family: coveragedomain.CompilerFamilyClang, Version: toolset.Version()}, Driver: coveragedomain.DriverSnapshot{Name: coveragedomain.DriverLLVMCov, Version: toolset.Version()}, Collector: coveragedomain.CollectorSnapshot{Name: coveragedomain.CollectorLLVMCov, Version: toolset.Version()}, NormalizerVersion: "1.0.0", InstrumentationFingerprint: InstrumentationFingerprint()}, Completeness: coveragedomain.Completeness{Outcome: coveragedomain.OutcomeAvailable}, Limits: coveragenormalize.DefaultLimits()})
	if err != nil {
		t.Fatal(err)
	}
	return normalized
}
