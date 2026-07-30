package workspace

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadConfigLoadsMinimalStructuredConfig(t *testing.T) {
	result, err := loadConfigBytes(t, configFixture(t, "minimal.valid.json"))
	if err != nil {
		t.Fatal(err)
	}

	if result.Config.Version != 1 {
		t.Fatalf("Version = %d, want 1", result.Config.Version)
	}
	if len(result.Config.Projects) != 1 {
		t.Fatalf("Projects = %#v, want one project", result.Config.Projects)
	}
	project := result.Config.Projects[0]
	if project.ID != "root" || project.SourceDir != "." {
		t.Fatalf("project = %#v, want root project at current directory", project)
	}
	if got := project.Fallback.Configurations; len(got) != 2 || got[0] != "Debug" || got[1] != "Release" {
		t.Fatalf("fallback configurations = %#v, want Debug and Release", got)
	}
	if project.Fallback.PreferredGenerator != "Ninja" {
		t.Fatalf("preferred generator = %q, want Ninja", project.Fallback.PreferredGenerator)
	}
	if len(result.Issues) != 0 {
		t.Fatalf("Issues = %#v, want none", result.Issues)
	}
}

func TestLoadConfigLoadsFamilyDiscriminatedManualToolchains(t *testing.T) {
	result, err := loadConfigBytes(t, configFixture(t, "manual-toolchains.valid.json"))
	if err != nil {
		t.Fatal(err)
	}

	if result.Config.CMake.Executable != "C:/Tools/CMake/bin/cmake.exe" {
		t.Fatalf("CMake executable = %q", result.Config.CMake.Executable)
	}
	if len(result.Config.Toolchains) != 2 {
		t.Fatalf("Toolchains = %#v, want two toolchains", result.Config.Toolchains)
	}
	clang := result.Config.Toolchains[0]
	if clang.ID != "linux-clang" || clang.Family != "clang" ||
		clang.CCompiler != "/usr/bin/clang" || clang.CPPCompiler != "/usr/bin/clang++" {
		t.Fatalf("Clang toolchain = %#v", clang)
	}
	msvc := result.Config.Toolchains[1]
	if msvc.ID != "windows-msvc" || msvc.Family != "msvc" ||
		msvc.InstallationID != "VisualStudio.18.Release" ||
		msvc.ToolsetVersion != "14.50" ||
		msvc.HostArchitecture != "x64" ||
		msvc.TargetArchitecture != "arm64" {
		t.Fatalf("MSVC toolchain = %#v", msvc)
	}
}

func TestLoadConfigLoadsV2TestContainerMappings(t *testing.T) {
	result, err := loadConfigBytes(t, configFixture(t, "tests-v2.valid.json"))
	if err != nil {
		t.Fatal(err)
	}

	if result.Config.Version != 2 {
		t.Fatalf("Version = %d, want 2", result.Config.Version)
	}
	if len(result.Config.Projects) != 1 {
		t.Fatalf("Projects = %#v, want one project", result.Config.Projects)
	}
	containers := result.Config.Projects[0].Tests.Containers
	want := []TestContainerMapping{
		{CTestName: "core.cpputest[debug]", Framework: FrameworkCppUTest},
		{CTestName: "core-unity", Framework: FrameworkUnity},
	}
	if len(containers) != len(want) {
		t.Fatalf("Test containers = %#v, want %#v", containers, want)
	}
	for index := range want {
		if containers[index] != want[index] {
			t.Fatalf("Test containers[%d] = %#v, want %#v", index, containers[index], want[index])
		}
	}
}

func TestLoadConfigRejectsUnsafeOrAmbiguousTestMappings(t *testing.T) {
	tooManyMappings := make([]map[string]any, 257)
	for index := range tooManyMappings {
		tooManyMappings[index] = map[string]any{
			"ctestName": fmt.Sprintf("test-%d", index),
			"framework": "cpputest",
		}
	}
	invalidUTF8Name := []byte(
		`{"version":2,"projects":[{"id":"root","sourceDir":".","tests":{"containers":[{"ctestName":"`,
	)
	invalidUTF8Name = append(invalidUTF8Name, 0xff)
	invalidUTF8Name = append(
		invalidUTF8Name,
		[]byte(`","framework":"cpputest"}]}}]}`)...,
	)

	testCases := map[string][]byte{
		"v1 tests field": []byte(
			`{"version":1,"projects":[{"id":"root","sourceDir":".","tests":{"containers":[]}}]}`,
		),
		"command fixture":   configFixture(t, "tests-command.invalid.json"),
		"duplicate fixture": configFixture(t, "tests-duplicate.invalid.json"),
		"duplicate name with different framework": []byte(
			`{"version":2,"projects":[{"id":"root","sourceDir":".","tests":{"containers":[{"ctestName":"same","framework":"cpputest"},{"ctestName":"same","framework":"unity"}]}}]}`,
		),
		"pattern field": []byte(
			`{"version":2,"projects":[{"id":"root","sourceDir":".","tests":{"containers":[{"ctestPattern":".*","framework":"cpputest"}]}}]}`,
		),
		"unsupported framework": []byte(
			`{"version":2,"projects":[{"id":"root","sourceDir":".","tests":{"containers":[{"ctestName":"tests","framework":"gtest"}]}}]}`,
		),
		"empty name": []byte(
			`{"version":2,"projects":[{"id":"root","sourceDir":".","tests":{"containers":[{"ctestName":"","framework":"cpputest"}]}}]}`,
		),
		"NUL name": []byte(
			"{\"version\":2,\"projects\":[{\"id\":\"root\",\"sourceDir\":\".\",\"tests\":{\"containers\":[{\"ctestName\":\"bad\\u0000name\",\"framework\":\"cpputest\"}]}}]}",
		),
		"invalid UTF-8 name": invalidUTF8Name,
		"too long name": []byte(
			`{"version":2,"projects":[{"id":"root","sourceDir":".","tests":{"containers":[{"ctestName":"` +
				strings.Repeat("x", 513) +
				`","framework":"cpputest"}]}}]}`,
		),
		"too many mappings": mustJSON(t, map[string]any{
			"version": 2,
			"projects": []map[string]any{{
				"id":        "root",
				"sourceDir": ".",
				"tests": map[string]any{
					"containers": tooManyMappings,
				},
			}},
		}),
		"null tests": []byte(
			`{"version":2,"projects":[{"id":"root","sourceDir":".","tests":null}]}`,
		),
		"null containers": []byte(
			`{"version":2,"projects":[{"id":"root","sourceDir":".","tests":{"containers":null}}]}`,
		),
	}
	for field, value := range map[string]any{
		"args":             []string{"--run", "all"},
		"environment":      map[string]string{"TOKEN": "secret"},
		"executable":       "C:/unsafe.exe",
		"glob":             "*",
		"hook":             "before",
		"shell":            true,
		"workingDirectory": "C:/outside",
	} {
		testCases["unsafe field "+field] = mustJSON(t, map[string]any{
			"version": 2,
			"projects": []map[string]any{{
				"id":        "root",
				"sourceDir": ".",
				"tests": map[string]any{
					"containers": []map[string]any{{
						"ctestName": "tests",
						"framework": "cpputest",
						field:       value,
					}},
				},
			}},
		})
	}

	for name, data := range testCases {
		t.Run(name, func(t *testing.T) {
			if _, err := loadConfigBytes(t, data); !errors.Is(err, ErrInvalidConfig) {
				t.Fatalf("LoadConfig error = %v, want ErrInvalidConfig", err)
			}
		})
	}
}

func TestLoadConfigMatchesSchemaOptionalFieldPresence(t *testing.T) {
	missingOptionalFields := map[string][]byte{
		"cmake": []byte(
			`{"version":1,"projects":[],"toolchains":[]}`,
		),
		"projects": []byte(
			`{"version":1,"cmake":{},"toolchains":[]}`,
		),
		"toolchains": []byte(
			`{"version":1,"cmake":{},"projects":[]}`,
		),
		"cmake executable": []byte(
			`{"version":1,"cmake":{}}`,
		),
		"project fallback": []byte(
			`{"version":1,"projects":[{"id":"root","sourceDir":"."}]}`,
		),
		"fallback configurations": []byte(
			`{"version":1,"projects":[{"id":"root","sourceDir":".","fallback":{"preferredGenerator":"Ninja"}}]}`,
		),
		"fallback preferred generator": []byte(
			`{"version":1,"projects":[{"id":"root","sourceDir":".","fallback":{"configurations":["Debug"]}}]}`,
		),
	}
	for name, data := range missingOptionalFields {
		t.Run("missing "+name, func(t *testing.T) {
			if _, err := loadConfigBytes(t, data); err != nil {
				t.Fatalf("LoadConfig error = %v, want missing optional field accepted", err)
			}
		})
	}

	nullOptionalFields := map[string][]byte{
		"cmake": []byte(
			`{"version":1,"cmake":null}`,
		),
		"projects": []byte(
			`{"version":1,"projects":null}`,
		),
		"toolchains": []byte(
			`{"version":1,"toolchains":null}`,
		),
		"cmake executable": []byte(
			`{"version":1,"cmake":{"executable":null}}`,
		),
		"project fallback": []byte(
			`{"version":1,"projects":[{"id":"root","sourceDir":".","fallback":null}]}`,
		),
		"fallback configurations": []byte(
			`{"version":1,"projects":[{"id":"root","sourceDir":".","fallback":{"configurations":null}}]}`,
		),
		"fallback preferred generator": []byte(
			`{"version":1,"projects":[{"id":"root","sourceDir":".","fallback":{"preferredGenerator":null}}]}`,
		),
	}
	for name, data := range nullOptionalFields {
		t.Run("null "+name, func(t *testing.T) {
			if _, err := loadConfigBytes(t, data); !errors.Is(err, ErrInvalidConfig) {
				t.Fatalf("LoadConfig error = %v, want ErrInvalidConfig for explicit null", err)
			}
		})
	}
}

func TestLoadConfigUsesLastRepeatedObjectMemberWithoutStaleFields(t *testing.T) {
	t.Run("cmake", func(t *testing.T) {
		result, err := loadConfigBytes(t, []byte(
			`{"version":1,"cmake":{"executable":"C:/Tools/CMake/bin/cmake.exe"},"cmake":{}}`,
		))
		if err != nil {
			t.Fatal(err)
		}
		if result.Config.CMake.Executable != "" {
			t.Fatalf("CMake executable = %q, want last empty cmake object to clear it", result.Config.CMake.Executable)
		}
	})

	t.Run("project fallback", func(t *testing.T) {
		result, err := loadConfigBytes(t, []byte(
			`{"version":1,"projects":[{"id":"root","sourceDir":".","fallback":{"configurations":["Debug"],"preferredGenerator":"Ninja"},"fallback":{}}]}`,
		))
		if err != nil {
			t.Fatal(err)
		}
		fallback := result.Config.Projects[0].Fallback
		if len(fallback.Configurations) != 0 || fallback.PreferredGenerator != "" {
			t.Fatalf("Fallback = %#v, want last empty fallback object to clear it", fallback)
		}
	})
}

func TestLoadConfigRejectsInvalidStructuredInput(t *testing.T) {
	tooManyProjects := make([]map[string]any, 65)
	tooManyToolchains := make([]map[string]any, 65)
	for index := range 65 {
		tooManyProjects[index] = map[string]any{
			"id":        fmt.Sprintf("project-%d", index),
			"sourceDir": fmt.Sprintf("project-%d", index),
		}
		tooManyToolchains[index] = map[string]any{
			"id":          fmt.Sprintf("toolchain-%d", index),
			"family":      "clang",
			"cCompiler":   "/usr/bin/clang",
			"cppCompiler": "/usr/bin/clang++",
		}
	}

	testCases := map[string][]byte{
		"unknown shell fields": configFixture(t, "shell.invalid.json"),
		"unknown top-level field": []byte(
			`{"version":1,"projects":[],"unexpected":true}`,
		),
		"wrong version": []byte(
			`{"version":3,"projects":[]}`,
		),
		"absolute POSIX source directory": []byte(
			`{"version":1,"projects":[{"id":"outside","sourceDir":"/outside"}]}`,
		),
		"absolute Windows source directory": []byte(
			`{"version":1,"projects":[{"id":"outside","sourceDir":"C:/outside"}]}`,
		),
		"escaping source directory": []byte(
			`{"version":1,"projects":[{"id":"outside","sourceDir":"../outside"}]}`,
		),
		"duplicate project ID": []byte(
			`{"version":1,"projects":[{"id":"same","sourceDir":"one"},{"id":"same","sourceDir":"two"}]}`,
		),
		"duplicate toolchain ID": []byte(
			`{"version":1,"toolchains":[{"id":"same","family":"clang","cCompiler":"/one/clang","cppCompiler":"/one/clang++"},{"id":"same","family":"gcc","cCompiler":"/two/gcc","cppCompiler":"/two/g++"}]}`,
		),
		"relative CMake executable": []byte(
			`{"version":1,"cmake":{"executable":"tools/cmake"}}`,
		),
		"root-relative Windows CMake executable": []byte(
			`{"version":1,"cmake":{"executable":"\\tools\\cmake.exe"}}`,
		),
		"relative compiler executable": []byte(
			`{"version":1,"toolchains":[{"id":"clang","family":"clang","cCompiler":"bin/clang","cppCompiler":"/usr/bin/clang++"}]}`,
		),
		"compiler fields on MSVC": []byte(
			`{"version":1,"toolchains":[{"id":"msvc","family":"msvc","cCompiler":"C:/cl.exe","installationId":"vs","toolsetVersion":"14.50","hostArchitecture":"x64","targetArchitecture":"x64"}]}`,
		),
		"MSVC fields on Clang": []byte(
			`{"version":1,"toolchains":[{"id":"clang","family":"clang","cCompiler":"/usr/bin/clang","cppCompiler":"/usr/bin/clang++","installationId":"vs"}]}`,
		),
		"trailing JSON value": []byte(
			`{"version":1} {"version":1}`,
		),
		"too many projects":   mustJSON(t, map[string]any{"version": 1, "projects": tooManyProjects}),
		"too many toolchains": mustJSON(t, map[string]any{"version": 1, "toolchains": tooManyToolchains}),
	}

	for name, data := range testCases {
		t.Run(name, func(t *testing.T) {
			if _, err := loadConfigBytes(t, data); !errors.Is(err, ErrInvalidConfig) {
				t.Fatalf("LoadConfig error = %v, want ErrInvalidConfig", err)
			}
		})
	}
}

func TestLoadConfigEnforcesMaximumFileSize(t *testing.T) {
	const maximumConfigBytes = 256 * 1024
	minimal := []byte(`{"version":1}`)

	t.Run("accepts exact limit", func(t *testing.T) {
		data := append([]byte{}, minimal...)
		data = append(data, []byte(strings.Repeat(" ", maximumConfigBytes-len(data)))...)
		if _, err := loadConfigBytes(t, data); err != nil {
			t.Fatalf("LoadConfig at %d bytes: %v", len(data), err)
		}
	})

	t.Run("rejects one byte over limit", func(t *testing.T) {
		data := append([]byte{}, minimal...)
		data = append(data, []byte(strings.Repeat(" ", maximumConfigBytes+1-len(data)))...)
		if _, err := loadConfigBytes(t, data); !errors.Is(err, ErrConfigTooLarge) {
			t.Fatalf("LoadConfig at %d bytes error = %v, want ErrConfigTooLarge", len(data), err)
		}
	})
}

func TestLoadConfigFallsBackToRootProjectWhenConfigIsMissing(t *testing.T) {
	rootPath := t.TempDir()
	if err := os.WriteFile(filepath.Join(rootPath, "CMakeLists.txt"), []byte("cmake_minimum_required(VERSION 3.31)\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	root, err := OpenRoot(rootPath)
	if err != nil {
		t.Fatal(err)
	}

	result, err := LoadConfig(root)
	if err != nil {
		t.Fatal(err)
	}
	if result.Config.Version != 1 {
		t.Fatalf("Version = %d, want 1", result.Config.Version)
	}
	if len(result.Config.Projects) != 1 ||
		result.Config.Projects[0].ID != "root" ||
		result.Config.Projects[0].SourceDir != "." {
		t.Fatalf("Projects = %#v, want root fallback project", result.Config.Projects)
	}
	if len(result.Issues) != 0 {
		t.Fatalf("Issues = %#v, want none", result.Issues)
	}
}

func TestLoadConfigReturnsNonBlockingIssueWhenNoRootProjectExists(t *testing.T) {
	root, err := OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	result, err := LoadConfig(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Config.Projects) != 0 {
		t.Fatalf("Projects = %#v, want none", result.Config.Projects)
	}
	if len(result.Issues) != 1 {
		t.Fatalf("Issues = %#v, want one issue", result.Issues)
	}
	if result.Issues[0].Blocking {
		t.Fatalf("Issue = %#v, want non-blocking", result.Issues[0])
	}
	if result.Issues[0].Code == "" || result.Issues[0].Message == "" {
		t.Fatalf("Issue = %#v, want stable code and useful message", result.Issues[0])
	}
}

func TestLoadConfigUsesOnlyExplicitNestedProjects(t *testing.T) {
	rootPath := t.TempDir()
	for _, directory := range []string{
		filepath.Join(rootPath, "apps", "api"),
		filepath.Join(rootPath, "third_party", "dependency"),
	} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(directory, "CMakeLists.txt"), []byte("project(example)\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	config := []byte(`{
		"version": 1,
		"projects": [
			{"id": "api", "sourceDir": "apps/api"}
		]
	}`)

	result, err := loadConfigAtRoot(t, rootPath, config)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Config.Projects) != 1 ||
		result.Config.Projects[0].ID != "api" ||
		result.Config.Projects[0].SourceDir != "apps/api" {
		t.Fatalf("Projects = %#v, want only explicitly declared nested project", result.Config.Projects)
	}
}

func TestValidRelativeWorkspacePathRejectsWindowsRootedPathOnEveryPlatform(t *testing.T) {
	if validRelativeWorkspacePath(`\outside`) {
		t.Fatal(`validRelativeWorkspacePath("\\outside") = true, want false`)
	}
}

func loadConfigBytes(t *testing.T, data []byte) (LoadResult, error) {
	t.Helper()
	return loadConfigAtRoot(t, t.TempDir(), data)
}

func loadConfigAtRoot(t *testing.T, rootPath string, data []byte) (LoadResult, error) {
	t.Helper()
	configDirectory := filepath.Join(rootPath, ".unit-test-ide")
	if err := os.MkdirAll(configDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDirectory, "workspace.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	root, err := OpenRoot(rootPath)
	if err != nil {
		t.Fatal(err)
	}
	return LoadConfig(root)
}

func configFixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
