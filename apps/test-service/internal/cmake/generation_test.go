package cmake

import (
	"reflect"
	"strings"
	"testing"

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

func TestWorkspaceGenerationChangesWithSemanticInputs(t *testing.T) {
	config := workspace.Config{
		Version:  1,
		Projects: []workspace.ProjectConfig{{ID: "app", SourceDir: "."}},
	}
	install := Installation{
		Executable: "/opt/cmake/bin/cmake",
		Version:    "4.3.4",
		Source:     SourceBundle,
		Identity:   strings.Repeat("a", 64),
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
		Version: 1,
		Projects: []workspace.ProjectConfig{
			{ID: "b", SourceDir: "b"},
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
