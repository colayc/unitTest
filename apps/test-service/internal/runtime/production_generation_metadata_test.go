package runtime

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"unit-test-ide.local/test-service/internal/cmake"
	"unit-test-ide.local/test-service/internal/testgenrender"
	"unit-test-ide.local/test-service/internal/workspace"
)

func productionToolchainIdentityTestIDs() []string {
	return []string{
		"clang-cl-6c3869c0ae502e45d43c7cdb0aaead95681ee493e9489b710cbd48a",
		"clang-" + strings.Repeat("a", 58), "Local.Clang_1",
		"local.clang_1", "x", strings.Repeat("x", 128), "._-",
		strings.Repeat("a", 64),
	}
}

func TestProductionCompileBindingPreservesToolchainIdentity(t *testing.T) {
	seen := map[string]string{}
	root, target := productionCompileBindingFixture(t)
	for _, id := range productionToolchainIdentityTestIDs() {
		t.Run(id, func(t *testing.T) {
			got, err := resolveProductionCompileBinding(root, "src/choose.c", []cmake.Target{target}, id)
			if err != nil {
				t.Fatal(err)
			}
			identity := struct {
				Version       int      `json:"version"`
				ToolchainID   string   `json:"toolchainId"`
				TargetID      string   `json:"targetId"`
				TargetName    string   `json:"targetName"`
				Configuration string   `json:"configuration"`
				Source        string   `json:"source"`
				Header        string   `json:"header"`
				Arguments     []string `json:"arguments"`
			}{1, id, target.ID, target.Name, target.Configuration, "src/choose.c", "include/choose.h", []string{"-std=c17", "-DFEATURE=1", "-Iinclude"}}
			encoded, err := json.Marshal(identity)
			if err != nil {
				t.Fatal(err)
			}
			want := productionBytesDigest(append([]byte("production-compile-snapshot-v1\x00"), encoded...))
			if got.compileSnapshotDigest != want || !validProductionDigest(want) {
				t.Fatalf("ID was not bound verbatim: got=%s want=%s", got.compileSnapshotDigest, want)
			}
			if prior, ok := seen[want]; ok && prior != id {
				t.Fatalf("different IDs collide: %q %q", prior, id)
			}
			seen[want] = id
		})
	}
}

func TestProductionCompileBindingRejectsMalformedToolchainIdentity(t *testing.T) {
	for _, id := range []string{"", strings.Repeat("x", 129), " leading", "trailing ", "a\tb", "a\nb", "a\x00b", "a/b", "a\\b", "工具链", "aé"} {
		root, target := productionCompileBindingFixture(t)
		if _, err := resolveProductionCompileBinding(root, "src/choose.c", []cmake.Target{target}, id); err == nil {
			t.Fatalf("malformed ID accepted: %q", id)
		}
	}
}

func TestProductionCompileBindingKeepsTargetAndProfileDigestsStrict(t *testing.T) {
	for _, bad := range []string{"clang-" + strings.Repeat("a", 58), strings.Repeat("A", 64), strings.Repeat("g", 64), strings.Repeat("a", 63), strings.Repeat("a", 65)} {
		for _, field := range []string{"target", "profile"} {
			root, target := productionCompileBindingFixture(t)
			if field == "target" {
				target.ID = bad
			} else {
				target.ProfileID = bad
			}
			if _, err := resolveProductionCompileBinding(root, "src/choose.c", []cmake.Target{target}, "Local.Clang_1"); err == nil {
				t.Fatalf("bad %s digest accepted: %q", field, bad)
			}
		}
	}
}

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
