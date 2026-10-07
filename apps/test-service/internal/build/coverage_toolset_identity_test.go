package build

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"unit-test-ide.local/test-service/internal/task"
	"unit-test-ide.local/test-service/internal/toolchain"
)

func TestCoverageToolsetIdentityAcceptsCompleteGCCCapability(t *testing.T) {
	paths := []string{"/usr/bin/gcc", "/usr/bin/g++", "/usr/bin/gcov"}
	evidence := []toolchain.ExecutableEvidence{
		{FileIdentity: "unix:1:10", SHA256: strings.Repeat("a", 64)},
		{FileIdentity: "unix:1:11", SHA256: strings.Repeat("b", 64)},
		{FileIdentity: "unix:1:12", SHA256: strings.Repeat("c", 64)},
	}
	identity := toolchain.GCCToolsetIdentity("14.2.0", paths, evidence)
	instance := toolchain.Instance{
		Family: toolchain.FamilyGCC, CCompiler: paths[0], CXXCompiler: paths[1], Version: "14.2.0",
		Coverage: toolchain.CoverageCapability{
			GCov: paths[2], GCovVersion: "14.2.0", ToolsetIdentity: identity,
			CompilerEvidence: evidence[0], CXXCompilerEvidence: evidence[1], GCovEvidence: evidence[2],
		},
	}
	got, err := coverageToolsetIdentity(instance, true)
	if err != nil || got != identity {
		t.Fatalf("complete GCC coverage capability = %q, %v; want %q", got, err, identity)
	}
}

func TestCoverageToolsetIdentityRejectsIncompleteGCCCapability(t *testing.T) {
	instance := toolchain.Instance{Family: toolchain.FamilyGCC, Version: "14.2.0"}
	if _, err := coverageToolsetIdentity(instance, true); err == nil {
		t.Fatal("incomplete GCC coverage capability was accepted")
	}
}

func TestCoverageToolsetIdentityAcceptsVerifiedLinuxClangCapability(t *testing.T) {
	instance := linuxClangCoverageIdentityFixture(t)
	got, err := coverageToolsetIdentity(instance, true)
	if err != nil || got != instance.Coverage.ToolsetIdentity {
		t.Fatalf("verified Linux Clang coverage capability = %q, %v; want %q", got, err, instance.Coverage.ToolsetIdentity)
	}
}

func TestCoverageToolsetIdentityRejectsUnverifiedLinuxClangCapability(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*toolchain.Instance)
	}{
		{"missing version", func(v *toolchain.Instance) { v.Version = "" }},
		{"missing C compiler", func(v *toolchain.Instance) { v.CCompiler = "" }},
		{"missing C++ compiler", func(v *toolchain.Instance) { v.CXXCompiler = "" }},
		{"missing profdata", func(v *toolchain.Instance) { v.Coverage.LLVMProfdata = "" }},
		{"missing cov", func(v *toolchain.Instance) { v.Coverage.LLVMCov = "" }},
		{"missing identity", func(v *toolchain.Instance) { v.Coverage.ToolsetIdentity = "" }},
		{"different identity", func(v *toolchain.Instance) { v.Coverage.ToolsetIdentity = strings.Repeat("f", 64) }},
		{"changed C compiler evidence", func(v *toolchain.Instance) { v.Coverage.CompilerEvidence.SHA256 = strings.Repeat("e", 64) }},
		{"changed C++ compiler evidence", func(v *toolchain.Instance) { v.Coverage.CXXCompilerEvidence.SHA256 = strings.Repeat("e", 64) }},
		{"changed profdata evidence", func(v *toolchain.Instance) { v.Coverage.ProfdataEvidence.SHA256 = strings.Repeat("e", 64) }},
		{"changed cov evidence", func(v *toolchain.Instance) { v.Coverage.CovEvidence.SHA256 = strings.Repeat("e", 64) }},
		{"unverified evidence", func(v *toolchain.Instance) { v.Coverage.CovEvidence.FileIdentity = "synthetic" }},
		{"mixed tool roots", func(v *toolchain.Instance) { v.Coverage.LLVMCov = filepath.Join(t.TempDir(), "llvm-cov") }},
		{"shared executable", func(v *toolchain.Instance) {
			v.Coverage.CXXCompilerEvidence.FileIdentity = v.Coverage.CompilerEvidence.FileIdentity
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			instance := linuxClangCoverageIdentityFixture(t)
			test.change(&instance)
			got, err := coverageToolsetIdentity(instance, true)
			if got != "" || !errors.Is(err, task.ErrInvalidArgument) {
				t.Fatalf("unverified Linux Clang capability = %q, %v; want invalid argument", got, err)
			}
		})
	}
}

func linuxClangCoverageIdentityFixture(t *testing.T) toolchain.Instance {
	t.Helper()
	root := t.TempDir()
	tools := []toolchain.LLVMToolEvidence{
		{Role: "clang", Path: filepath.Join(root, "clang"), Evidence: toolchain.ExecutableEvidence{FileIdentity: "unix:1:10", SHA256: strings.Repeat("a", 64)}},
		{Role: "clang++", Path: filepath.Join(root, "clang++"), Evidence: toolchain.ExecutableEvidence{FileIdentity: "unix:1:11", SHA256: strings.Repeat("b", 64)}},
		{Role: "llvm-profdata", Path: filepath.Join(root, "llvm-profdata"), Evidence: toolchain.ExecutableEvidence{FileIdentity: "unix:1:12", SHA256: strings.Repeat("c", 64)}},
		{Role: "llvm-cov", Path: filepath.Join(root, "llvm-cov"), Evidence: toolchain.ExecutableEvidence{FileIdentity: "unix:1:13", SHA256: strings.Repeat("d", 64)}},
	}
	identity, err := toolchain.LLVMToolsetIdentityForTools("22.1.8", tools)
	if err != nil {
		t.Fatal(err)
	}
	return toolchain.Instance{
		Family: toolchain.FamilyClang, Version: "22.1.8", CCompiler: tools[0].Path, CXXCompiler: tools[1].Path,
		Coverage: toolchain.CoverageCapability{
			LLVMProfdata: tools[2].Path, LLVMCov: tools[3].Path, ToolsetIdentity: identity,
			CompilerEvidence: tools[0].Evidence, CXXCompilerEvidence: tools[1].Evidence,
			ProfdataEvidence: tools[2].Evidence, CovEvidence: tools[3].Evidence,
		},
	}
}
