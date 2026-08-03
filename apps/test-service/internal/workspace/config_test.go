package workspace

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
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
			`{"version":4,"projects":[]}`,
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

func TestLoadConfigLoadsCanonicalCoverageV3WithoutChangingV1V2(t *testing.T) {
	result, err := loadConfigBytes(t, configFixture(t, "coverage-v3.valid.json"))
	if err != nil {
		t.Fatal(err)
	}
	if result.Config.Version != 3 || len(result.Config.CoverageProfiles) != 1 {
		t.Fatalf("Config = %#v", result.Config)
	}
	got := result.Config.CoverageProfiles[0]
	want := CoverageProfile{
		ID: "coverage-debug", BaseBuildProfileID: "debug-clang",
		Include: []string{"include/**", "src/**"},
		Exclude: []string{"tests/**", "third_party/**"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("CoverageProfile = %#v, want %#v", got, want)
	}

	for _, fixtureName := range []string{"minimal.valid.json", "tests-v2.valid.json"} {
		loaded, loadErr := loadConfigBytes(t, configFixture(t, fixtureName))
		if loadErr != nil {
			t.Fatalf("%s: %v", fixtureName, loadErr)
		}
		if len(loaded.Config.CoverageProfiles) != 0 {
			t.Fatalf("%s coverage profiles = %#v", fixtureName, loaded.Config.CoverageProfiles)
		}
	}
}

func TestLoadConfigCanonicalizesCoverageDefaultsNFCAndOrder(t *testing.T) {
	data := []byte(`{"version":3,"coverageProfiles":[` +
		`{"id":"z","baseBuildProfileId":"base","exclude":["tests/**"]},` +
		`{"id":"a","baseBuildProfileId":"base","include":["src/e\u0301.cpp","include/**"],"exclude":[]}` +
		`]}`)
	result, err := loadConfigBytes(t, data)
	if err != nil {
		t.Fatal(err)
	}
	if got := result.Config.CoverageProfiles[0].ID; got != "a" {
		t.Fatalf("first ID = %q", got)
	}
	if got := result.Config.CoverageProfiles[0].Include; !reflect.DeepEqual(got, []string{"include/**", "src/é.cpp"}) {
		t.Fatalf("NFC include = %#v", got)
	}
	if got := result.Config.CoverageProfiles[0].Exclude; got == nil || len(got) != 0 {
		t.Fatalf("empty exclude = %#v, want non-nil empty slice", got)
	}
	if got := result.Config.CoverageProfiles[1].Include; !reflect.DeepEqual(got, []string{"**"}) {
		t.Fatalf("default include = %#v", got)
	}
}

func TestLoadConfigAcceptsSupportedCoverageMetacharacters(t *testing.T) {
	result, err := loadConfigBytes(t, coverageConfigJSON(
		t,
		[]string{"**", "include/?.hpp", "src/*.cpp"},
		[]string{},
	))
	if err != nil {
		t.Fatal(err)
	}
	got := result.Config.CoverageProfiles[0]
	if !reflect.DeepEqual(got.Include, []string{"**", "include/?.hpp", "src/*.cpp"}) ||
		got.Exclude == nil || len(got.Exclude) != 0 {
		t.Fatalf("CoverageProfile = %#v", got)
	}
}

func TestLoadConfigRejectsUnsafeCoverageProfiles(t *testing.T) {
	cases := map[string][]byte{
		"v2 coverage field":                 []byte(`{"version":2,"coverageProfiles":[]}`),
		"command fixture":                   configFixture(t, "coverage-command.invalid.json"),
		"path fixture":                      configFixture(t, "coverage-path.invalid.json"),
		"duplicate fixture":                 configFixture(t, "coverage-duplicate.invalid.json"),
		"duplicate ID with different globs": []byte(`{"version":3,"coverageProfiles":[{"id":"coverage","baseBuildProfileId":"base","include":["src/**"]},{"id":"coverage","baseBuildProfileId":"base","include":["include/**"]}]}`),
		"missing base":                      []byte(`{"version":3,"coverageProfiles":[{"id":"coverage"}]}`),
		"empty include":                     []byte(`{"version":3,"coverageProfiles":[{"id":"coverage","baseBuildProfileId":"base","include":[]}]}`),
		"empty glob":                        coverageConfigJSON(t, []string{""}, nil),
		"backslash":                         coverageConfigJSON(t, []string{`src\\**`}, nil),
		"absolute":                          coverageConfigJSON(t, []string{"/src/**"}, nil),
		"drive":                             coverageConfigJSON(t, []string{"C:/src/**"}, nil),
		"UNC":                               coverageConfigJSON(t, []string{"//server/share/**"}, nil),
		"URI scheme":                        coverageConfigJSON(t, []string{"file:src/**"}, nil),
		"dot segment":                       coverageConfigJSON(t, []string{"src/./**"}, nil),
		"empty segment":                     coverageConfigJSON(t, []string{"src//**"}, nil),
		"trailing slash":                    coverageConfigJSON(t, []string{"src/"}, nil),
		"embedded globstar":                 coverageConfigJSON(t, []string{"src/**.cpp"}, nil),
		"class expansion":                   coverageConfigJSON(t, []string{"src/[ab].cpp"}, nil),
		"brace expansion":                   coverageConfigJSON(t, []string{"src/{a,b}.cpp"}, nil),
		"command substitution":              coverageConfigJSON(t, []string{"src/$(whoami).cpp"}, nil),
		"environment substitution":          coverageConfigJSON(t, []string{"src/${TOKEN}.cpp"}, nil),
		"backtick":                          coverageConfigJSON(t, []string{"src/`whoami`.cpp"}, nil),
		"NUL":                               coverageConfigJSON(t, []string{"src/\x00.cpp"}, nil),
		"control":                           coverageConfigJSON(t, []string{"src/\x01.cpp"}, nil),
		"NFC duplicate":                     coverageConfigJSON(t, []string{"src/é.cpp", "src/e\u0301.cpp"}, nil),
		"long ASCII glob":                   coverageConfigJSON(t, []string{"src/" + strings.Repeat("x", 509)}, nil),
		"long multibyte glob":               coverageConfigJSON(t, []string{strings.Repeat("界", 171)}, nil),
	}
	for name, data := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := loadConfigBytes(t, data); !errors.Is(err, ErrInvalidConfig) {
				t.Fatalf("error = %v, want ErrInvalidConfig", err)
			}
		})
	}
}

func TestConfigCloneIsolatesCoverageSlices(t *testing.T) {
	result, err := loadConfigBytes(t, configFixture(t, "coverage-v3.valid.json"))
	if err != nil {
		t.Fatal(err)
	}
	cloned := result.Config.Clone()
	cloned.CoverageProfiles[0].Include[0] = "mutated/**"
	cloned.CoverageProfiles[0].Exclude[0] = "mutated/**"
	cloned.CoverageProfiles = append(cloned.CoverageProfiles, CoverageProfile{ID: "extra"})
	if result.Config.CoverageProfiles[0].Include[0] != "include/**" ||
		result.Config.CoverageProfiles[0].Exclude[0] != "tests/**" ||
		len(result.Config.CoverageProfiles) != 1 {
		t.Fatalf("source Config mutated: %#v", result.Config)
	}
}

func coverageConfigJSON(t *testing.T, include, exclude []string) []byte {
	t.Helper()
	profile := map[string]any{
		"id": "coverage", "baseBuildProfileId": "base", "include": include,
	}
	if exclude != nil {
		profile["exclude"] = exclude
	}
	return mustJSON(t, map[string]any{"version": 3, "coverageProfiles": []any{profile}})
}

func literalCoverageGlobs(prefix string, lengths ...int) []string {
	result := make([]string, 0, len(lengths))
	for index, length := range lengths {
		head := fmt.Sprintf("%s-%02d-", prefix, index)
		result = append(result, head+strings.Repeat("x", length-len(head)))
	}
	return result
}

func repeatedLength(length, count int) []int {
	result := make([]int, count)
	for index := range result {
		result[index] = length
	}
	return result
}

func TestLoadConfigRejectsCoverageBounds(t *testing.T) {
	profiles := make([]any, 65)
	for index := range profiles {
		profiles[index] = map[string]any{
			"id": fmt.Sprintf("coverage-%02d", index), "baseBuildProfileId": "base",
		}
	}
	includes := make([]string, 129)
	for index := range includes {
		includes[index] = fmt.Sprintf("src/file-%03d.cpp", index)
	}

	perProfile := literalCoverageGlobs("per", repeatedLength(511, 15)...)
	perProfile = append(perProfile, literalCoverageGlobs("tail", 510)...)
	perProfile = append(perProfile, "z")

	totalProfiles := make([]any, 0, 9)
	for index := 0; index < 7; index++ {
		totalProfiles = append(totalProfiles, map[string]any{
			"id": fmt.Sprintf("total-%02d", index), "baseBuildProfileId": "base",
			"include": literalCoverageGlobs(fmt.Sprintf("p%02d", index), repeatedLength(511, 16)...),
		})
	}
	totalProfiles = append(totalProfiles, map[string]any{
		"id": "total-07", "baseBuildProfileId": "base",
		"include": append(
			literalCoverageGlobs("p07", repeatedLength(511, 15)...),
			literalCoverageGlobs("p07-tail", 510)...,
		),
	})
	totalProfiles = append(totalProfiles, map[string]any{
		"id": "total-08", "baseBuildProfileId": "base", "include": []string{"z"},
	})

	cases := map[string][]byte{
		"65 profiles":         mustJSON(t, map[string]any{"version": 3, "coverageProfiles": profiles}),
		"129 includes":        coverageConfigJSON(t, includes, nil),
		"8193 profile states": coverageConfigJSON(t, perProfile, nil),
		"65537 total states":  mustJSON(t, map[string]any{"version": 3, "coverageProfiles": totalProfiles}),
	}
	for name, data := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := loadConfigBytes(t, data); !errors.Is(err, ErrInvalidConfig) {
				t.Fatalf("error = %v, want ErrInvalidConfig", err)
			}
		})
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
