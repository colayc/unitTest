package runtime

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"unit-test-ide.local/test-service/internal/cmake"
	"unit-test-ide.local/test-service/internal/testgenrender"
	"unit-test-ide.local/test-service/internal/workspace"
)

func TestResolveProductionCompileBindingUsesExactCMakeUnit(t *testing.T) {
	root, target := productionCompileBindingFixture(t)

	binding, err := resolveProductionCompileBinding(root, "src/choose.c", []cmake.Target{target}, strings.Repeat("a", 64))
	if err != nil {
		t.Fatalf("resolveProductionCompileBinding() error = %v", err)
	}
	if binding.target.ID != target.ID || binding.language != testgenrender.LanguageC || binding.headerRelative != "include/choose.h" {
		t.Fatalf("binding = %+v", binding)
	}
	wantArguments := []string{"-std=c17", "-DFEATURE=1", "-Iinclude"}
	if !reflect.DeepEqual(binding.arguments, wantArguments) {
		t.Fatalf("arguments = %#v, want %#v", binding.arguments, wantArguments)
	}
	if !validProductionDigest(binding.compileSnapshotDigest) || binding.cmakeTargetDigest != target.ID {
		t.Fatalf("compile identities = (%q, %q)", binding.compileSnapshotDigest, binding.cmakeTargetDigest)
	}

	again, err := resolveProductionCompileBinding(root, "src/choose.c", cmake.CloneTargets([]cmake.Target{target}), strings.Repeat("a", 64))
	if err != nil || !reflect.DeepEqual(again, binding) {
		t.Fatalf("deterministic resolve = %+v, %v", again, err)
	}
}

func TestResolveProductionCompileBindingRejectsAmbiguousOrUnsafeMetadata(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(workspace.Root, cmake.Target) []cmake.Target
	}{
		{
			name: "source belongs to two targets",
			mutate: func(_ workspace.Root, target cmake.Target) []cmake.Target {
				other := cmake.CloneTargets([]cmake.Target{target})[0]
				other.ID = strings.Repeat("b", 64)
				other.Name = "other"
				return []cmake.Target{target, other}
			},
		},
		{
			name: "unsupported definition",
			mutate: func(_ workspace.Root, target cmake.Target) []cmake.Target {
				target.CompileUnits[0].Defines = []string{"NAME=text"}
				return []cmake.Target{target}
			},
		},
		{
			name: "unsupported language standard",
			mutate: func(_ workspace.Root, target cmake.Target) []cmake.Target {
				target.CompileUnits[0].Standard = "23"
				return []cmake.Target{target}
			},
		},
		{
			name: "missing matching header",
			mutate: func(_ workspace.Root, target cmake.Target) []cmake.Target {
				target.Sources = target.Sources[1:]
				return []cmake.Target{target}
			},
		},
		{
			name: "generated source",
			mutate: func(_ workspace.Root, target cmake.Target) []cmake.Target {
				target.CompileUnits[0].Generated = true
				return []cmake.Target{target}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root, target := productionCompileBindingFixture(t)
			if _, err := resolveProductionCompileBinding(root, "src/choose.c", test.mutate(root, target), strings.Repeat("a", 64)); err == nil {
				t.Fatal("resolveProductionCompileBinding() error = nil, want fail-closed rejection")
			}
		})
	}
}

func productionCompileBindingFixture(t *testing.T) (workspace.Root, cmake.Target) {
	t.Helper()
	native := t.TempDir()
	for path, content := range map[string]string{
		filepath.Join(native, "src", "choose.c"):         "int choose(int value) { return value; }\n",
		filepath.Join(native, "include", "choose.h"):     "int choose(int value);\n",
		filepath.Join(native, "tests", "CMakeLists.txt"): "add_executable(unit_tests)\n",
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	root, err := workspace.OpenRoot(native)
	if err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(root.NativePath, "src", "choose.c")
	header := filepath.Join(root.NativePath, "include", "choose.h")
	include := filepath.Join(root.NativePath, "include")
	return root, cmake.Target{
		ID: strings.Repeat("c", 64), Name: "core", Type: "STATIC_LIBRARY",
		ProjectID: "core", ProfileID: strings.Repeat("d", 64), Configuration: "Debug",
		SourceDir: root.NativePath, ProjectSourceDir: root.NativePath,
		Sources: []cmake.TargetSource{{Path: header}, {Path: source, Compiled: true}},
		CompileUnits: []cmake.CompileUnit{{
			Source: source, Language: "C", Standard: "17",
			Includes: []string{include}, Defines: []string{"FEATURE=1"},
		}},
	}
}
