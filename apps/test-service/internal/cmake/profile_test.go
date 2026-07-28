package cmake

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"unit-test-ide.local/test-service/internal/probe"
)

func TestNewGeneratedProfileConstructsStableControlledProfile(t *testing.T) {
	buildRoot := t.TempDir()
	spec := GeneratedProfileSpec{
		ProjectID:     "app",
		ToolchainID:   "toolchain-0123",
		Generator:     "Ninja",
		Configuration: "Debug",
		BuildRoot:     buildRoot,
	}

	first, err := NewGeneratedProfile(spec)
	if err != nil {
		t.Fatalf("NewGeneratedProfile(first) error = %v", err)
	}
	second, err := NewGeneratedProfile(spec)
	if err != nil {
		t.Fatalf("NewGeneratedProfile(second) error = %v", err)
	}

	const wantID = "fb080d0bb678643a4b3cd01833d9dab76f833526611d2c0f0387575db7ae55fa"
	if first.ID != wantID || second.ID != wantID {
		t.Fatalf("IDs = %q and %q, want stable %q", first.ID, second.ID, wantID)
	}
	if first.ProjectID != spec.ProjectID ||
		first.Origin != "generated" ||
		first.ConfigurePreset != "" ||
		first.BuildPreset != "" ||
		first.ToolchainID != spec.ToolchainID ||
		first.Generator != spec.Generator ||
		first.Configuration != spec.Configuration {
		t.Fatalf("profile = %#v", first)
	}
	if want := filepath.Join(filepath.Clean(buildRoot), wantID); first.BinaryDir != want {
		t.Fatalf("BinaryDir = %q, want %q", first.BinaryDir, want)
	}
}

func TestNewGeneratedProfileIDChangesForEverySemanticField(t *testing.T) {
	buildRoot := t.TempDir()
	base := GeneratedProfileSpec{
		ProjectID:     "app",
		ToolchainID:   "toolchain-0123",
		Generator:     "Ninja",
		Configuration: "Debug",
		BuildRoot:     buildRoot,
	}
	baseProfile, err := NewGeneratedProfile(base)
	if err != nil {
		t.Fatalf("NewGeneratedProfile(base) error = %v", err)
	}

	mutations := []struct {
		name   string
		mutate func(*GeneratedProfileSpec)
	}{
		{"project", func(value *GeneratedProfileSpec) { value.ProjectID = "other" }},
		{"toolchain", func(value *GeneratedProfileSpec) { value.ToolchainID = "toolchain-4567" }},
		{"generator", func(value *GeneratedProfileSpec) { value.Generator = "Unix Makefiles" }},
		{"configuration", func(value *GeneratedProfileSpec) { value.Configuration = "Release" }},
	}
	for _, mutation := range mutations {
		t.Run(mutation.name, func(t *testing.T) {
			changed := base
			mutation.mutate(&changed)
			changedProfile, err := NewGeneratedProfile(changed)
			if err != nil {
				t.Fatalf("NewGeneratedProfile(changed) error = %v", err)
			}
			if changedProfile.ID == baseProfile.ID {
				t.Fatalf("semantic field change kept profile ID %q", baseProfile.ID)
			}
		})
	}
}

func TestNewGeneratedProfileRejectsInvalidSemanticFields(t *testing.T) {
	valid := GeneratedProfileSpec{
		ProjectID:     "app",
		ToolchainID:   "toolchain-0123",
		Generator:     "Ninja",
		Configuration: "Debug",
		BuildRoot:     t.TempDir(),
	}
	fields := []struct {
		name string
		set  func(*GeneratedProfileSpec, string)
	}{
		{"project", func(spec *GeneratedProfileSpec, value string) { spec.ProjectID = value }},
		{"toolchain", func(spec *GeneratedProfileSpec, value string) { spec.ToolchainID = value }},
		{"generator", func(spec *GeneratedProfileSpec, value string) { spec.Generator = value }},
		{"configuration", func(spec *GeneratedProfileSpec, value string) { spec.Configuration = value }},
	}
	invalidValues := []struct {
		name  string
		value string
	}{
		{"empty", ""},
		{"control", "value\nnext"},
		{"nul", "value\x00next"},
		{"invalid UTF-8", string([]byte{'v', 0xff})},
		{"over limit", strings.Repeat("x", 257)},
	}

	for _, field := range fields {
		for _, invalid := range invalidValues {
			t.Run(field.name+"/"+invalid.name, func(t *testing.T) {
				spec := valid
				field.set(&spec, invalid.value)
				if profile, err := NewGeneratedProfile(spec); err == nil {
					t.Fatalf("NewGeneratedProfile() = %#v, want error", profile)
				}
			})
		}
	}
}

func TestNewGeneratedProfileRejectsInvalidBuildRoot(t *testing.T) {
	valid := GeneratedProfileSpec{
		ProjectID:     "app",
		ToolchainID:   "toolchain-0123",
		Generator:     "Ninja",
		Configuration: "Debug",
	}
	root := t.TempDir()
	filePath := filepath.Join(root, "not-a-directory")
	if err := os.WriteFile(filePath, []byte("file"), 0o600); err != nil {
		t.Fatal(err)
	}

	cases := map[string]string{
		"empty":          "",
		"relative":       "relative-build-root",
		"not clean":      root + string(filepath.Separator) + "child" + string(filepath.Separator) + "..",
		"trailing slash": root + string(filepath.Separator),
		"file":           filePath,
		"missing":        filepath.Join(root, "missing"),
		"nul":            root + "\x00",
	}
	for name, buildRoot := range cases {
		t.Run(name, func(t *testing.T) {
			spec := valid
			spec.BuildRoot = buildRoot
			if profile, err := NewGeneratedProfile(spec); err == nil {
				t.Fatalf("NewGeneratedProfile() = %#v, want error", profile)
			}
		})
	}
}

func TestNewGeneratedProfileBuildRootRelocationPreservesID(t *testing.T) {
	firstRoot := t.TempDir()
	secondRoot := t.TempDir()
	spec := GeneratedProfileSpec{
		ProjectID:     "app",
		ToolchainID:   "toolchain-0123",
		Generator:     "Ninja",
		Configuration: "Debug",
		BuildRoot:     firstRoot,
	}

	first, err := NewGeneratedProfile(spec)
	if err != nil {
		t.Fatalf("NewGeneratedProfile(first) error = %v", err)
	}
	spec.BuildRoot = secondRoot
	second, err := NewGeneratedProfile(spec)
	if err != nil {
		t.Fatalf("NewGeneratedProfile(second) error = %v", err)
	}

	if first.ID != second.ID {
		t.Fatalf("relocation changed ID from %q to %q", first.ID, second.ID)
	}
	if first.BinaryDir == second.BinaryDir {
		t.Fatalf("relocation kept BinaryDir %q", first.BinaryDir)
	}
	if first.BinaryDir != filepath.Join(firstRoot, first.ID) {
		t.Fatalf("first BinaryDir = %q", first.BinaryDir)
	}
	if second.BinaryDir != filepath.Join(secondRoot, second.ID) {
		t.Fatalf("second BinaryDir = %q", second.BinaryDir)
	}
}

func TestGeneratedBuildDirectoryContainmentUsesPathComponents(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "build")
	inside := filepath.Join(root, "profile")
	prefixSibling := filepath.Join(parent, "build-other", "profile")

	if !generatedBuildDirectoryWithinRoot(root, inside) {
		t.Fatalf("inside path %q was rejected", inside)
	}
	if generatedBuildDirectoryWithinRoot(root, prefixSibling) {
		t.Fatalf("prefix sibling %q was accepted under %q", prefixSibling, root)
	}
}

func TestNewGeneratedProfileUsesPlatformPathCaseSemantics(t *testing.T) {
	spec := GeneratedProfileSpec{
		ProjectID:     "app",
		ToolchainID:   "toolchain-0123",
		Generator:     "Ninja",
		Configuration: "Debug",
	}

	if runtime.GOOS == "windows" {
		firstRoot := t.TempDir()
		secondRoot := strings.ToUpper(firstRoot)
		spec.BuildRoot = firstRoot
		first, err := NewGeneratedProfile(spec)
		if err != nil {
			t.Fatalf("NewGeneratedProfile(first) error = %v", err)
		}
		spec.BuildRoot = secondRoot
		second, err := NewGeneratedProfile(spec)
		if err != nil {
			t.Fatalf("NewGeneratedProfile(second) error = %v", err)
		}
		if first.ID != second.ID {
			t.Fatalf("Windows path spelling changed ID from %q to %q", first.ID, second.ID)
		}
		if !strings.EqualFold(first.BinaryDir, second.BinaryDir) {
			t.Fatalf("Windows BinaryDirs %q and %q are not case-equivalent", first.BinaryDir, second.BinaryDir)
		}
		return
	}

	parent := t.TempDir()
	firstRoot := filepath.Join(parent, "Build")
	secondRoot := filepath.Join(parent, "build")
	if err := os.Mkdir(firstRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(secondRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	spec.BuildRoot = firstRoot
	first, err := NewGeneratedProfile(spec)
	if err != nil {
		t.Fatalf("NewGeneratedProfile(first) error = %v", err)
	}
	spec.BuildRoot = secondRoot
	second, err := NewGeneratedProfile(spec)
	if err != nil {
		t.Fatalf("NewGeneratedProfile(second) error = %v", err)
	}
	if first.ID != second.ID {
		t.Fatalf("Linux relocation changed ID from %q to %q", first.ID, second.ID)
	}
	if first.BinaryDir == second.BinaryDir {
		t.Fatalf("Linux case-distinct roots produced the same BinaryDir %q", first.BinaryDir)
	}
}

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
		profile.Generator != "Ninja" ||
		profile.BinaryDir != filepath.Join(sourceDir, "out", "debug") {
		t.Fatalf("profile = %#v", profile)
	}
}
