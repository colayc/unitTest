package coveragerun

import (
	"errors"
	"path/filepath"
	"testing"

	"unit-test-ide.local/test-service/internal/processcontrol"
)

type fakeTrustedPath struct {
	path string
	err  error
}

func (value fakeTrustedPath) Path() string  { return value.path }
func (value fakeTrustedPath) Verify() error { return value.err }

func TestBuildLLVMInvocationCreatesFixedMergeAndExportSpecs(t *testing.T) {
	root := filepath.Join(t.TempDir(), "profiles")
	invocation, err := BuildLLVMInvocation(LLVMInputs{
		Profdata:         fakeTrustedPath{path: filepath.Join(root, "llvm-profdata.exe")},
		Cov:              fakeTrustedPath{path: filepath.Join(root, "llvm-cov.exe")},
		Binary:           fakeTrustedPath{path: filepath.Join(root, "unit-tests.exe")},
		ProfileDirectory: fakeTrustedPath{path: root},
		ProfileFiles:     []string{"first.profraw", "second.profraw"},
		MergedProfile:    "merged.profdata",
	})
	if err != nil {
		t.Fatal(err)
	}
	if invocation.Merge.Executable != filepath.Join(root, "llvm-profdata.exe") ||
		invocation.Export.Executable != filepath.Join(root, "llvm-cov.exe") {
		t.Fatalf("executables = %#v", invocation)
	}
	if invocation.Merge.Args[0] != "merge" || invocation.Merge.Args[1] != "-sparse" ||
		invocation.Merge.Args[len(invocation.Merge.Args)-2] != "-o" ||
		invocation.Merge.Args[len(invocation.Merge.Args)-1] != filepath.Join(root, "merged.profdata") {
		t.Fatalf("merge args = %#v", invocation.Merge.Args)
	}
	if invocation.Export.Args[0] != "export" || invocation.Export.Args[1] != "-format=text" ||
		invocation.Export.Args[2] != "-instr-profile="+filepath.Join(root, "merged.profdata") ||
		invocation.Export.Args[3] != filepath.Join(root, "unit-tests.exe") {
		t.Fatalf("export args = %#v", invocation.Export.Args)
	}
	if invocation.Merge.Dir != root || invocation.Export.Dir != root ||
		len(invocation.Merge.Env) != 0 || len(invocation.Export.Env) != 0 {
		t.Fatalf("process isolation = %#v", invocation)
	}
	if len(invocation.Merge.EnvUnset) == 0 || len(invocation.Merge.EnvUnset) != len(invocation.Export.EnvUnset) {
		t.Fatalf("env unset = %#v", invocation)
	}
	if invocation.Merge.Batch != nil || invocation.Export.Batch != nil {
		t.Fatal("LLVM invocation unexpectedly used a batch")
	}
}

func TestBuildLLVMInvocationRejectsUnverifiedToolsAndUnsafeProfileNames(t *testing.T) {
	root := filepath.Join(t.TempDir(), "profiles")
	base := LLVMInputs{
		Profdata:         fakeTrustedPath{path: filepath.Join(root, "llvm-profdata.exe")},
		Cov:              fakeTrustedPath{path: filepath.Join(root, "llvm-cov.exe")},
		Binary:           fakeTrustedPath{path: filepath.Join(root, "unit-tests.exe")},
		ProfileDirectory: fakeTrustedPath{path: root},
		ProfileFiles:     []string{"profile.profraw"}, MergedProfile: "merged.profdata",
	}
	for name, input := range map[string]LLVMInputs{
		"tool verification": func() LLVMInputs {
			value := base
			value.Cov = fakeTrustedPath{path: base.Cov.Path(), err: errors.New("replaced")}
			return value
		}(),
		"profile traversal":  func() LLVMInputs { value := base; value.ProfileFiles = []string{"..\\outside.profraw"}; return value }(),
		"unsafe merged name": func() LLVMInputs { value := base; value.MergedProfile = "nested/merged.profdata"; return value }(),
		"wrong profdata executable": func() LLVMInputs {
			value := base
			value.Profdata = fakeTrustedPath{path: filepath.Join(root, "python.exe")}
			return value
		}(),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := BuildLLVMInvocation(input); err == nil {
				t.Fatal("BuildLLVMInvocation accepted unsafe input")
			}
		})
	}
}

func TestLLVMInvocationUsesOnlySupportedProcessFields(t *testing.T) {
	var _ processcontrol.Spec
}
