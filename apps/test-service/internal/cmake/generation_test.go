package cmake

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"unit-test-ide.local/test-service/internal/probe"
	"unit-test-ide.local/test-service/internal/workspace"
)

func TestWorkspaceGenerationCanonicalizesCollectionAndPathOrder(t *testing.T) {
	configA := workspace.Config{
		Version: 1,
		CMake:   workspace.CMakeConfig{Executable: `C:\Tools\CMake\bin\cmake.exe`},
		Projects: []workspace.ProjectConfig{
			{
				ID:        "zeta",
				SourceDir: `projects\zeta`,
				Fallback: workspace.FallbackConfig{
					Configurations:     []string{"Release", "Debug"},
					PreferredGenerator: "Ninja",
				},
			},
			{ID: "alpha", SourceDir: "projects/alpha"},
		},
		Toolchains: []workspace.ToolchainConfig{
			{ID: "z-gcc", Family: "gcc", CCompiler: "/usr/bin/gcc", CPPCompiler: "/usr/bin/g++"},
			{ID: "a-clang", Family: "clang", CCompiler: "/usr/bin/clang", CPPCompiler: "/usr/bin/clang++"},
		},
	}
	configB := workspace.Config{
		Version: 1,
		CMake:   workspace.CMakeConfig{Executable: `C:/Tools/CMake/bin/cmake.exe`},
		Projects: []workspace.ProjectConfig{
			{ID: "alpha", SourceDir: "projects/./alpha"},
			{
				ID:        "zeta",
				SourceDir: "projects/zeta",
				Fallback: workspace.FallbackConfig{
					Configurations:     []string{"Debug", "Release"},
					PreferredGenerator: "Ninja",
				},
			},
		},
		Toolchains: []workspace.ToolchainConfig{
			{ID: "a-clang", Family: "clang", CCompiler: "/usr/bin/clang", CPPCompiler: "/usr/bin/clang++"},
			{ID: "z-gcc", Family: "gcc", CCompiler: "/usr/bin/gcc", CPPCompiler: "/usr/bin/g++"},
		},
	}
	installA := Installation{
		Executable:  `C:\Tools\CMake\bin\cmake.exe`,
		Version:     "4.3.4",
		Source:      SourceBundle,
		Identity:    strings.Repeat("a", 64),
		LicensePath: `C:\Tools\CMake\LICENSE.rst`,
	}
	installB := installA
	installB.Executable = `C:/Tools/CMake/bin/cmake.exe`
	installB.LicensePath = `C:/Tools/CMake/LICENSE.rst`
	profilesA := []BuildProfile{
		{ID: strings.Repeat("b", 64), ProjectID: "zeta", Origin: "preset", ConfigurePreset: "default"},
		{ID: strings.Repeat("a", 64), ProjectID: "alpha", Origin: "generated", ToolchainID: "a-clang"},
	}
	profilesB := []BuildProfile{profilesA[1], profilesA[0]}

	first := WorkspaceGeneration(configA, installA, profilesA, []string{"z-gcc", "a-clang"})
	second := WorkspaceGeneration(configB, installB, profilesB, []string{"a-clang", "z-gcc"})
	if first != second {
		t.Fatalf("equivalent inputs produced %q and %q", first, second)
	}
	if len(first) != 64 || strings.ToLower(first) != first {
		t.Fatalf("generation = %q, want lowercase SHA-256", first)
	}
}

func TestWorkspaceGenerationCanonicalizesAndTracksTestMappings(t *testing.T) {
	projectWithMappings := func(containers []workspace.TestContainerMapping) workspace.ProjectConfig {
		return workspace.ProjectConfig{
			ID:        "app",
			SourceDir: ".",
			Tests: workspace.ProjectTestsConfig{
				Containers: containers,
			},
		}
	}
	configA := workspace.Config{
		Version: 2,
		Projects: []workspace.ProjectConfig{projectWithMappings([]workspace.TestContainerMapping{
			{CTestName: "z-unity", Framework: workspace.FrameworkUnity},
			{CTestName: "a-cpputest", Framework: workspace.FrameworkCppUTest},
		})},
	}
	configB := workspace.Config{
		Version: 2,
		Projects: []workspace.ProjectConfig{projectWithMappings([]workspace.TestContainerMapping{
			{CTestName: "a-cpputest", Framework: workspace.FrameworkCppUTest},
			{CTestName: "z-unity", Framework: workspace.FrameworkUnity},
		})},
	}
	configChanged := workspace.Config{
		Version: 2,
		Projects: []workspace.ProjectConfig{projectWithMappings([]workspace.TestContainerMapping{
			{CTestName: "a-cpputest", Framework: workspace.FrameworkCppUTest},
			{CTestName: "z-unity", Framework: workspace.FrameworkCppUTest},
		})},
	}

	first := WorkspaceGeneration(configA, Installation{}, nil, nil)
	reordered := WorkspaceGeneration(configB, Installation{}, nil, nil)
	changed := WorkspaceGeneration(configChanged, Installation{}, nil, nil)
	if first != reordered {
		t.Fatalf("mapping order produced %q and %q", first, reordered)
	}
	if first == changed {
		t.Fatalf("framework mapping change kept generation %q", first)
	}
}

func TestCanonicalPortablePathNormalizesSeparatorsBeforeCleaning(t *testing.T) {
	want := "C:/Tools/CMake/bin"
	if runtime.GOOS == "windows" {
		want = strings.ToLower(want)
	}

	got := canonicalPortablePath(`C:\Tools\SDK\..\CMake/bin`)
	if got != want {
		t.Fatalf("canonicalPortablePath() = %q, want %q", got, want)
	}
}

func TestCanonicalPortablePathPreservesUNCPrefix(t *testing.T) {
	want := "//Server/Share/CMake/bin"
	if runtime.GOOS == "windows" {
		want = strings.ToLower(want)
	}

	got := canonicalPortablePath(`\\Server\Share\SDK\..\CMake/bin`)
	if got != want {
		t.Fatalf("canonicalPortablePath() = %q, want %q", got, want)
	}
}

func TestCanonicalPathIdentityUsesPlatformCaseSemantics(t *testing.T) {
	firstSource := "Project/Src"
	firstBinary := "Build/Debug"
	if runtime.GOOS == "windows" {
		firstSource = `Project\Src`
		firstBinary = `Build\Debug`
	}
	secondSource := "project/src"
	secondBinary := "build/debug"

	firstConfig := workspace.Config{
		Version:  1,
		Projects: []workspace.ProjectConfig{{ID: "app", SourceDir: firstSource}},
	}
	secondConfig := workspace.Config{
		Version:  1,
		Projects: []workspace.ProjectConfig{{ID: "app", SourceDir: secondSource}},
	}
	firstProfile := BuildProfile{ProjectID: "app", Origin: "preset", BinaryDir: firstBinary}
	secondProfile := BuildProfile{ProjectID: "app", Origin: "preset", BinaryDir: secondBinary}

	firstProfileID, err := profileID(firstProfile)
	if err != nil {
		t.Fatalf("profileID(first) error = %v", err)
	}
	secondProfileID, err := profileID(secondProfile)
	if err != nil {
		t.Fatalf("profileID(second) error = %v", err)
	}
	firstGeneration := WorkspaceGeneration(firstConfig, Installation{}, []BuildProfile{firstProfile}, nil)
	secondGeneration := WorkspaceGeneration(secondConfig, Installation{}, []BuildProfile{secondProfile}, nil)
	relativePathsEqual := canonicalRelativePath(firstSource) == canonicalRelativePath(secondSource)

	if runtime.GOOS == "windows" {
		if firstProfileID != secondProfileID ||
			firstGeneration != secondGeneration ||
			!relativePathsEqual {
			t.Fatalf(
				"Windows-equivalent paths differ: profile IDs %q/%q, generations %q/%q, relativeEqual=%t",
				firstProfileID,
				secondProfileID,
				firstGeneration,
				secondGeneration,
				relativePathsEqual,
			)
		}
		return
	}
	if firstProfileID == secondProfileID ||
		firstGeneration == secondGeneration ||
		relativePathsEqual {
		t.Fatalf(
			"case-sensitive paths collapsed: profile IDs %q/%q, generations %q/%q, relativeEqual=%t",
			firstProfileID,
			secondProfileID,
			firstGeneration,
			secondGeneration,
			relativePathsEqual,
		)
	}
}

func TestWorkspaceGenerationChangesWithSemanticInputs(t *testing.T) {
	config := workspace.Config{
		Version:  1,
		Projects: []workspace.ProjectConfig{{ID: "app", SourceDir: "."}},
	}
	install := Installation{
		Executable:      "/opt/cmake/bin/cmake",
		CTestExecutable: "/opt/cmake/bin/ctest",
		Version:         "4.3.4",
		Source:          SourceBundle,
		Identity:        strings.Repeat("a", 64),
	}
	profiles := []BuildProfile{{
		ID:              strings.Repeat("b", 64),
		ProjectID:       "app",
		Origin:          "preset",
		ConfigurePreset: "debug",
		Generator:       "Ninja",
	}}
	base := WorkspaceGeneration(config, install, profiles, []string{"clang"})

	tests := []struct {
		name       string
		config     workspace.Config
		install    Installation
		profiles   []BuildProfile
		toolchains []string
	}{
		{
			name: "workspace config",
			config: workspace.Config{
				Version:  1,
				Projects: []workspace.ProjectConfig{{ID: "app", SourceDir: "src"}},
			},
			install:    install,
			profiles:   profiles,
			toolchains: []string{"clang"},
		},
		{
			name:       "cmake identity",
			config:     config,
			install:    Installation{Executable: install.Executable, Version: install.Version, Source: install.Source, Identity: strings.Repeat("c", 64)},
			profiles:   profiles,
			toolchains: []string{"clang"},
		},
		{
			name:   "ctest executable",
			config: config,
			install: Installation{
				Executable: install.Executable, CTestExecutable: "/opt/other/bin/ctest",
				Version: install.Version, Source: install.Source, Identity: install.Identity,
			},
			profiles: profiles, toolchains: []string{"clang"},
		},
		{
			name:    "profile field",
			config:  config,
			install: install,
			profiles: []BuildProfile{{
				ID: strings.Repeat("b", 64), ProjectID: "app", Origin: "preset",
				ConfigurePreset: "debug", Generator: "Unix Makefiles",
			}},
			toolchains: []string{"clang"},
		},
		{
			name:       "toolchain identity",
			config:     config,
			install:    install,
			profiles:   profiles,
			toolchains: []string{"gcc"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := WorkspaceGeneration(test.config, test.install, test.profiles, test.toolchains)
			if got == base {
				t.Fatalf("semantic change kept generation %q", base)
			}
		})
	}
}

func TestWorkspaceGenerationDoesNotMutateInputs(t *testing.T) {
	config := workspace.Config{
		Version: 3,
		CoverageProfiles: []workspace.CoverageProfile{{
			ID: "coverage", BaseBuildProfileID: "base",
			Include: []string{"src/**", "include/**"}, Exclude: []string{"tests/**"},
		}},
		Projects: []workspace.ProjectConfig{
			{
				ID:        "b",
				SourceDir: "b",
				Tests: workspace.ProjectTestsConfig{Containers: []workspace.TestContainerMapping{
					{CTestName: "z", Framework: workspace.FrameworkUnity},
					{CTestName: "a", Framework: workspace.FrameworkCppUTest},
				}},
			},
			{ID: "a", SourceDir: "a"},
		},
	}
	profiles := []BuildProfile{
		{ID: strings.Repeat("b", 64), ProjectID: "b"},
		{ID: strings.Repeat("a", 64), ProjectID: "a"},
	}
	toolchains := []string{"b", "a"}
	wantConfig := config
	wantConfig.Projects = append([]workspace.ProjectConfig(nil), config.Projects...)
	wantConfig.Projects[0].Tests.Containers = append(
		[]workspace.TestContainerMapping(nil),
		config.Projects[0].Tests.Containers...,
	)
	wantConfig.CoverageProfiles = append([]workspace.CoverageProfile(nil), config.CoverageProfiles...)
	wantConfig.CoverageProfiles[0].Include = append([]string(nil), config.CoverageProfiles[0].Include...)
	wantConfig.CoverageProfiles[0].Exclude = append([]string(nil), config.CoverageProfiles[0].Exclude...)
	wantProfiles := append([]BuildProfile(nil), profiles...)
	wantToolchains := append([]string(nil), toolchains...)

	_ = WorkspaceGeneration(config, Installation{}, profiles, toolchains)

	if !reflect.DeepEqual(config, wantConfig) {
		t.Fatalf("config mutated: %#v", config)
	}
	if !reflect.DeepEqual(profiles, wantProfiles) {
		t.Fatalf("profiles mutated: %#v", profiles)
	}
	if !reflect.DeepEqual(toolchains, wantToolchains) {
		t.Fatalf("toolchains mutated: %#v", toolchains)
	}
}

func TestCoverageProfileReferencesAndProjectBinding(t *testing.T) {
	coverageProfiles := []workspace.CoverageProfile{{
		ID: "coverage-debug", BaseBuildProfileID: "build-debug",
		Include: []string{"src/**"}, Exclude: []string{"tests/**"},
	}}
	buildProfiles := []BuildProfile{{ID: "build-debug", ProjectID: "app"}}
	if err := ValidateCoverageProfileReferences(coverageProfiles, buildProfiles); err != nil {
		t.Fatal(err)
	}
	for name, profiles := range map[string][]BuildProfile{
		"missing base": nil,
		"duplicate build ID": {
			{ID: "build-debug", ProjectID: "app"},
			{ID: "build-debug", ProjectID: "other"},
		},
	} {
		t.Run("validate "+name, func(t *testing.T) {
			if validateErr := ValidateCoverageProfileReferences(coverageProfiles, profiles); !errors.Is(validateErr, ErrInvalidCoverageProfile) {
				t.Fatalf("error = %v", validateErr)
			}
		})
	}
	coverage, base, err := ResolveCoverageProfile(coverageProfiles, buildProfiles, "app", "coverage-debug")
	if err != nil || coverage.ID != "coverage-debug" || base.ID != "build-debug" {
		t.Fatalf("coverage/base/error = %#v / %#v / %v", coverage, base, err)
	}
	coverage.Include[0] = "mutated/**"
	if coverageProfiles[0].Include[0] != "src/**" {
		t.Fatal("ResolveCoverageProfile returned an alias")
	}

	tests := []struct {
		name          string
		projectID     string
		coverageID    string
		buildProfiles []BuildProfile
	}{
		{name: "missing base", projectID: "app", coverageID: "coverage-debug"},
		{name: "wrong project", projectID: "other", coverageID: "coverage-debug", buildProfiles: buildProfiles},
		{name: "missing coverage", projectID: "app", coverageID: "unknown", buildProfiles: buildProfiles},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, _, resolveErr := ResolveCoverageProfile(coverageProfiles, test.buildProfiles, test.projectID, test.coverageID)
			if !errors.Is(resolveErr, ErrInvalidCoverageProfile) {
				t.Fatalf("error = %v", resolveErr)
			}
		})
	}
	duplicateCoverage := append(append([]workspace.CoverageProfile{}, coverageProfiles...), coverageProfiles[0])
	if _, _, duplicateErr := ResolveCoverageProfile(duplicateCoverage, buildProfiles, "app", "coverage-debug"); !errors.Is(duplicateErr, ErrInvalidCoverageProfile) {
		t.Fatalf("duplicate coverage error = %v", duplicateErr)
	}
}

func TestWorkspaceGenerationCanonicalizesCoverageProfiles(t *testing.T) {
	configA := workspace.Config{Version: 3, CoverageProfiles: []workspace.CoverageProfile{
		{ID: "z", BaseBuildProfileID: "build-z", Include: []string{"src/**", "include/**"}},
		{ID: "a", BaseBuildProfileID: "build-a", Exclude: []string{"tests/**", "third_party/**"}},
	}}
	configB := workspace.Config{Version: 3, CoverageProfiles: []workspace.CoverageProfile{
		{ID: "a", BaseBuildProfileID: "build-a", Exclude: []string{"third_party/**", "tests/**"}},
		{ID: "z", BaseBuildProfileID: "build-z", Include: []string{"include/**", "src/**"}},
	}}
	first := WorkspaceGeneration(configA, Installation{}, nil, nil)
	second := WorkspaceGeneration(configB, Installation{}, nil, nil)
	if first != second {
		t.Fatalf("coverage order changed generation: %q / %q", first, second)
	}
	configB.CoverageProfiles[0].Exclude[0] = "generated/**"
	if changed := WorkspaceGeneration(configB, Installation{}, nil, nil); changed == first {
		t.Fatalf("coverage semantic change kept generation %q", first)
	}
}

func TestCanonicalGenerationOmitsCoverageFieldForV1V2(t *testing.T) {
	for _, version := range []int{1, 2} {
		encoded, err := json.Marshal(canonicalGenerationConfig(workspace.Config{Version: version, CoverageProfiles: []workspace.CoverageProfile{{ID: "must-be-ignored", BaseBuildProfileID: "base", Include: []string{"**"}}}}))
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(encoded, []byte("coverageProfiles")) {
			t.Fatalf("v%d canonical config widened: %s", version, encoded)
		}
	}
}

func TestPresetInputGenerationSaltsProfilesAndWorkspaceGeneration(t *testing.T) {
	root, project, sourceDir := newPresetWorkspace(t)
	installation := presetTestInstallation(root)
	discover := func(cacheValue string) PresetDiscovery {
		t.Helper()
		writePresetJSON(t, filepath.Join(sourceDir, "CMakePresets.json"), map[string]any{
			"version": 6,
			"configurePresets": []map[string]any{{
				"name":      "debug",
				"generator": "Ninja",
				"binaryDir": "out/debug",
				"cacheVariables": map[string]any{
					"UNCHANGED_NAME": cacheValue,
				},
			}},
		})
		runner := &presetTestRunner{results: []probe.Result{
			{ExitCode: 0, Stdout: []byte("Available configure presets:\n  \"debug\"\n")},
			{ExitCode: 0, Stdout: []byte("Available build presets:\n")},
		}}
		discovery, err := DiscoverPresets(
			context.Background(),
			runner,
			installation,
			root,
			project,
		)
		if err != nil {
			t.Fatalf("DiscoverPresets(%q) error = %v", cacheValue, err)
		}
		return discovery
	}

	first := discover("one")
	second := discover("two")
	if first.InputGeneration == "" || second.InputGeneration == "" {
		t.Fatalf("InputGeneration = %q, %q, want SHA-256 values", first.InputGeneration, second.InputGeneration)
	}
	if first.InputGeneration == second.InputGeneration {
		t.Fatalf("cacheVariables content change kept input generation %q", first.InputGeneration)
	}
	if len(first.Profiles) != 1 || len(second.Profiles) != 1 {
		t.Fatalf("Profiles = %#v and %#v, want one each", first.Profiles, second.Profiles)
	}
	if first.Profiles[0].ID == second.Profiles[0].ID {
		t.Fatalf("cacheVariables content change kept profile ID %q", first.Profiles[0].ID)
	}

	config := workspace.Config{Version: 1, Projects: []workspace.ProjectConfig{project}}
	firstGeneration := WorkspaceGeneration(
		config,
		installation,
		nil,
		nil,
		first.InputGeneration,
	)
	secondGeneration := WorkspaceGeneration(
		config,
		installation,
		nil,
		nil,
		second.InputGeneration,
	)
	if firstGeneration == secondGeneration {
		t.Fatalf("empty-profile workspace generation stayed %q after Preset content change", firstGeneration)
	}
}

func TestPresetInputGenerationAndWorkspaceGenerationAreOrderStable(t *testing.T) {
	root, project, sourceDir := newPresetWorkspace(t)
	writePresetJSON(t, filepath.Join(sourceDir, "CMakePresets.json"), map[string]any{
		"version": 6,
		"include": []string{"z-last.json", "a-first.json"},
		"configurePresets": []map[string]any{{
			"name":      "debug",
			"generator": "Ninja",
			"binaryDir": "out/debug",
		}},
	})
	writePresetJSON(t, filepath.Join(sourceDir, "z-last.json"), map[string]any{"version": 6})
	writePresetJSON(t, filepath.Join(sourceDir, "a-first.json"), map[string]any{"version": 6})
	installation := presetTestInstallation(root)
	discover := func() PresetDiscovery {
		t.Helper()
		runner := &presetTestRunner{results: []probe.Result{
			{ExitCode: 0, Stdout: []byte("Available configure presets:\n  \"debug\"\n")},
			{ExitCode: 0, Stdout: []byte("Available build presets:\n")},
		}}
		discovery, err := DiscoverPresets(
			context.Background(),
			runner,
			installation,
			root,
			project,
		)
		if err != nil {
			t.Fatalf("DiscoverPresets() error = %v", err)
		}
		return discovery
	}

	first := discover()
	second := discover()
	if first.InputGeneration != second.InputGeneration {
		t.Fatalf("same graph produced %q and %q", first.InputGeneration, second.InputGeneration)
	}
	if first.Profiles[0].ID != second.Profiles[0].ID {
		t.Fatalf("same graph produced profile IDs %q and %q", first.Profiles[0].ID, second.Profiles[0].ID)
	}

	config := workspace.Config{Version: 1, Projects: []workspace.ProjectConfig{project}}
	firstGeneration := WorkspaceGeneration(config, installation, nil, nil, "b", "a")
	secondGeneration := WorkspaceGeneration(config, installation, nil, nil, "a", "b")
	if firstGeneration != secondGeneration {
		t.Fatalf("input generation order produced %q and %q", firstGeneration, secondGeneration)
	}
}
