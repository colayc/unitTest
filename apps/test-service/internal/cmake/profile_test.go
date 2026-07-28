package cmake

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"unit-test-ide.local/test-service/internal/probe"
)

func TestPresetProfilesAreStableAcrossMachineListingOrder(t *testing.T) {
	root, project, sourceDir := newPresetWorkspace(t)
	writePresetJSON(t, sourceDir+"/CMakePresets.json", map[string]any{
		"version": 6,
		"configurePresets": []map[string]any{
			{"name": "debug", "generator": "Ninja", "binaryDir": "${sourceDir}/out/debug"},
			{"name": "release", "generator": "Ninja", "binaryDir": "${sourceDir}/out/release"},
		},
		"buildPresets": []map[string]any{
			{"name": "debug-build", "configurePreset": "debug", "configuration": "Debug"},
			{"name": "release-build", "configurePreset": "release", "configuration": "Release"},
		},
	})
	installation := presetTestInstallation(root)
	firstRunner := &presetTestRunner{results: []probe.Result{
		{ExitCode: 0, Stdout: []byte("Available configure presets:\n  \"release\"\n  \"debug\"\n")},
		{ExitCode: 0, Stdout: []byte("Available build presets:\n  \"release-build\"\n  \"debug-build\"\n")},
	}}
	secondRunner := &presetTestRunner{results: []probe.Result{
		{ExitCode: 0, Stdout: []byte("Available configure presets:\n  \"debug\"\n  \"release\"\n")},
		{ExitCode: 0, Stdout: []byte("Available build presets:\n  \"debug-build\"\n  \"release-build\"\n")},
	}}

	first, err := DiscoverPresets(context.Background(), firstRunner, installation, root, project)
	if err != nil {
		t.Fatalf("DiscoverPresets(first) error = %v", err)
	}
	second, err := DiscoverPresets(context.Background(), secondRunner, installation, root, project)
	if err != nil {
		t.Fatalf("DiscoverPresets(second) error = %v", err)
	}
	if !reflect.DeepEqual(first.Profiles, second.Profiles) {
		t.Fatalf("profiles differ by listing order:\nfirst  = %#v\nsecond = %#v", first.Profiles, second.Profiles)
	}
}

func TestPresetProfileIDChangesForEverySemanticField(t *testing.T) {
	base := BuildProfile{
		ProjectID:       "app",
		Origin:          "preset",
		ConfigurePreset: "debug",
		BuildPreset:     "debug-build",
		ToolchainID:     "clang",
		Generator:       "Ninja",
		Configuration:   "Debug",
		BinaryDir:       "${sourceDir}/out/debug",
	}
	baseID, err := profileID(base)
	if err != nil {
		t.Fatalf("profileID(base) error = %v", err)
	}
	if len(baseID) != 64 || strings.ToLower(baseID) != baseID {
		t.Fatalf("profileID(base) = %q, want lowercase SHA-256", baseID)
	}

	mutations := []struct {
		name   string
		mutate func(*BuildProfile)
	}{
		{"project", func(value *BuildProfile) { value.ProjectID = "other" }},
		{"origin", func(value *BuildProfile) { value.Origin = "generated" }},
		{"configure preset", func(value *BuildProfile) { value.ConfigurePreset = "release" }},
		{"build preset", func(value *BuildProfile) { value.BuildPreset = "release-build" }},
		{"toolchain", func(value *BuildProfile) { value.ToolchainID = "gcc" }},
		{"generator", func(value *BuildProfile) { value.Generator = "Unix Makefiles" }},
		{"configuration", func(value *BuildProfile) { value.Configuration = "Release" }},
		{"binary directory", func(value *BuildProfile) { value.BinaryDir = "${sourceDir}/out/release" }},
	}
	for _, mutation := range mutations {
		t.Run(mutation.name, func(t *testing.T) {
			changed := base
			mutation.mutate(&changed)
			changedID, err := profileID(changed)
			if err != nil {
				t.Fatalf("profileID(changed) error = %v", err)
			}
			if changedID == baseID {
				t.Fatalf("semantic field change kept profile ID %q", baseID)
			}
		})
	}
}

func TestPresetProfileExistsWithoutBuildPreset(t *testing.T) {
	root, project, sourceDir := newPresetWorkspace(t)
	writePresetJSON(t, sourceDir+"/CMakePresets.json", map[string]any{
		"version": 6,
		"configurePresets": []map[string]any{{
			"name":      "debug",
			"generator": "Ninja",
			"binaryDir": "${sourceDir}/out/debug",
		}},
	})
	runner := &presetTestRunner{results: []probe.Result{
		{ExitCode: 0, Stdout: []byte("Available configure presets:\n  \"debug\"\n")},
		{ExitCode: 0, Stdout: []byte("Available build presets:\n")},
	}}

	discovery, err := DiscoverPresets(
		context.Background(),
		runner,
		presetTestInstallation(root),
		root,
		project,
	)
	if err != nil {
		t.Fatalf("DiscoverPresets() error = %v", err)
	}
	if len(discovery.Profiles) != 1 {
		t.Fatalf("Profiles = %#v, want one configure-only profile", discovery.Profiles)
	}
	profile := discovery.Profiles[0]
	if profile.ConfigurePreset != "debug" || profile.BuildPreset != "" ||
		profile.Generator != "Ninja" || profile.BinaryDir != "${sourceDir}/out/debug" {
		t.Fatalf("profile = %#v", profile)
	}
}
