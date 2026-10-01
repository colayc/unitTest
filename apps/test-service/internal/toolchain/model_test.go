package toolchain

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestLLVMFourToolIdentityBindsRolesAndRejectsUnverifiedEvidence(t *testing.T) {
	root := t.TempDir()
	tools := []LLVMToolEvidence{
		{Role: "clang", Path: filepath.Join(root, "clang"), Evidence: ExecutableEvidence{"unix:1:10", strings.Repeat("a", 64)}},
		{Role: "clang++", Path: filepath.Join(root, "clang++"), Evidence: ExecutableEvidence{"unix:1:11", strings.Repeat("b", 64)}},
		{Role: "llvm-profdata", Path: filepath.Join(root, "llvm-profdata"), Evidence: ExecutableEvidence{"unix:1:12", strings.Repeat("c", 64)}},
		{Role: "llvm-cov", Path: filepath.Join(root, "llvm-cov"), Evidence: ExecutableEvidence{"unix:1:13", strings.Repeat("d", 64)}},
	}
	identity, err := LLVMToolsetIdentityForTools("18.1.3", tools)
	if err != nil || len(identity) != 64 || strings.Contains(identity, root) {
		t.Fatalf("identity = %q, error = %v", identity, err)
	}
	changed := append([]LLVMToolEvidence(nil), tools...)
	changed[1].Evidence.SHA256 = strings.Repeat("e", 64)
	other, err := LLVMToolsetIdentityForTools("18.1.3", changed)
	if err != nil || other == identity {
		t.Fatal("identity ignored C++ executable digest")
	}
	for _, change := range []func([]LLVMToolEvidence) []LLVMToolEvidence{
		func(v []LLVMToolEvidence) []LLVMToolEvidence { return v[:3] },
		func(v []LLVMToolEvidence) []LLVMToolEvidence { v[1].Role = "clang"; return v },
		func(v []LLVMToolEvidence) []LLVMToolEvidence { v[1].Evidence.FileIdentity = "synthetic"; return v },
		func(v []LLVMToolEvidence) []LLVMToolEvidence {
			v[1].Evidence.FileIdentity = v[0].Evidence.FileIdentity
			return v
		},
		func(v []LLVMToolEvidence) []LLVMToolEvidence {
			v[1].Path = filepath.Join(t.TempDir(), "clang++")
			return v
		},
	} {
		mutated := change(append([]LLVMToolEvidence(nil), tools...))
		if _, err := LLVMToolsetIdentityForTools("18.1.3", mutated); err == nil {
			t.Fatalf("accepted invalid four-tool evidence: %#v", mutated)
		}
	}
}
