package cmake

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"unit-test-ide.local/test-service/internal/probe"
)

func TestDiscoverPresetsExpandsVersionedIncludeMacros(t *testing.T) {
	t.Run("version 7 penv uses frozen empty probe environment", func(t *testing.T) {
		root, project, sourceDir := newPresetWorkspace(t)
		writePresetJSON(t, filepath.Join(sourceDir, "included.json"), map[string]any{"version": 7})
		writePresetJSON(t, filepath.Join(sourceDir, "CMakePresets.json"), map[string]any{
			"version": 7,
			"include": []string{"$penv{OPTIONAL_PREFIX}included.json"},
			"configurePresets": []map[string]any{{
				"name": "debug", "generator": "Ninja", "binaryDir": "out/debug",
			}},
		})

		discovery, err := DiscoverPresets(
			context.Background(),
			successfulPresetRunner("debug"),
			presetTestInstallation(root),
			root,
			project,
		)
		if err != nil {
			t.Fatalf("DiscoverPresets() error = %v", err)
		}
		want := []string{
			canonicalRelativePath("project/CMakePresets.json"),
			canonicalRelativePath("project/included.json"),
		}
		if !reflect.DeepEqual(discovery.Inputs, want) {
			t.Fatalf("Inputs = %#v, want %#v", discovery.Inputs, want)
		}
	})

	t.Run("version 9 sourceDir and nested fileDir", func(t *testing.T) {
		root, project, sourceDir := newPresetWorkspace(t)
		writePresetJSON(t, filepath.Join(sourceDir, "shared", "source.json"), map[string]any{"version": 9})
		writePresetJSON(t, filepath.Join(sourceDir, "sub", "nested.json"), map[string]any{"version": 9})
		writePresetJSON(t, filepath.Join(sourceDir, "sub", "bridge.json"), map[string]any{
			"version": 9,
			"include": []string{"${fileDir}/nested.json"},
		})
		writePresetJSON(t, filepath.Join(sourceDir, "CMakePresets.json"), map[string]any{
			"version": 9,
			"include": []string{
				"${sourceDir}/shared/source.json",
				"sub/bridge.json",
			},
			"configurePresets": []map[string]any{{
				"name": "debug", "generator": "Ninja", "binaryDir": "out/debug",
			}},
		})

		discovery, err := DiscoverPresets(
			context.Background(),
			successfulPresetRunner("debug"),
			presetTestInstallation(root),
			root,
			project,
		)
		if err != nil {
			t.Fatalf("DiscoverPresets() error = %v", err)
		}
		want := []string{
			canonicalRelativePath("project/CMakePresets.json"),
			canonicalRelativePath("project/shared/source.json"),
			canonicalRelativePath("project/sub/bridge.json"),
			canonicalRelativePath("project/sub/nested.json"),
		}
		if !reflect.DeepEqual(discovery.Inputs, want) {
			t.Fatalf("Inputs = %#v, want %#v", discovery.Inputs, want)
		}
	})
}

func TestDiscoverPresetsAcceptsLiteralDollarsInPaths(t *testing.T) {
	root, project, sourceDir := newPresetWorkspace(t)
	writePresetJSON(t, filepath.Join(sourceDir, "$shared.json"), map[string]any{"version": 9})
	writePresetJSON(t, filepath.Join(sourceDir, "CMakePresets.json"), map[string]any{
		"version": 9,
		"include": []string{"$shared.json"},
		"configurePresets": []map[string]any{
			{
				"name": "named", "generator": "Ninja",
				"binaryDir": "${sourceDir}/out/$cache",
			},
			{
				"name": "trailing", "generator": "Ninja",
				"binaryDir": "out/cache$",
			},
			{
				"name": "double", "generator": "Ninja",
				"binaryDir": "out/$$/cache",
			},
		},
	})

	discovery, err := DiscoverPresets(
		context.Background(),
		successfulPresetRunner("named", "trailing", "double"),
		presetTestInstallation(root),
		root,
		project,
	)
	if err != nil {
		t.Fatalf("DiscoverPresets() error = %v", err)
	}
	wantInputs := []string{
		canonicalRelativePath("project/$shared.json"),
		canonicalRelativePath("project/CMakePresets.json"),
	}
	if !reflect.DeepEqual(discovery.Inputs, wantInputs) {
		t.Fatalf("Inputs = %#v, want %#v", discovery.Inputs, wantInputs)
	}
	got := make(map[string]string, len(discovery.Profiles))
	for _, profile := range discovery.Profiles {
		got[profile.ConfigurePreset] = profile.BinaryDir
	}
	want := map[string]string{
		"named":    filepath.Join(sourceDir, "out", "$cache"),
		"trailing": filepath.Join(sourceDir, "out", "cache$"),
		"double":   filepath.Join(sourceDir, "out", "$$", "cache"),
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("BinaryDirs = %#v, want %#v", got, want)
	}
}

func TestDiscoverPresetsRejectsEscapingOrForbiddenIncludeMacrosBeforeProbe(t *testing.T) {
	tests := []struct {
		name    string
		version int
		include string
		want    error
	}{
		{"version 6 has no include macros", 6, "$penv{PREFIX}included.json", ErrInvalidPresets},
		{"version 7 rejects sourceDir", 7, "${sourceDir}/included.json", ErrInvalidPresets},
		{"version 8 rejects fileDir", 8, "${fileDir}/included.json", ErrInvalidPresets},
		{"sourceParent escapes root", 9, "${sourceParentDir}/../outside.json", ErrPresetBoundary},
		{"env forbidden", 9, "$env{PREFIX}included.json", ErrInvalidPresets},
		{"presetName forbidden", 9, "${presetName}/included.json", ErrInvalidPresets},
		{"generator forbidden", 9, "${generator}/included.json", ErrInvalidPresets},
		{"vendor forbidden", 9, "$vendor{example}/included.json", ErrInvalidPresets},
		{"unknown namespace forbidden", 9, "$foo{bar}/included.json", ErrInvalidPresets},
		{"unknown forbidden", 9, "${unknown}/included.json", ErrInvalidPresets},
		{"malformed forbidden", 9, "${sourceDir/included.json", ErrInvalidPresets},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root, project, sourceDir := newPresetWorkspace(t)
			writePresetJSON(t, filepath.Join(sourceDir, "CMakePresets.json"), map[string]any{
				"version": test.version,
				"include": []string{test.include},
			})
			runner := &presetTestRunner{}

			_, err := DiscoverPresets(
				context.Background(),
				runner,
				presetTestInstallation(root),
				root,
				project,
			)
			if !errors.Is(err, test.want) {
				t.Fatalf("error = %v, want %v", err, test.want)
			}
			if len(runner.specs) != 0 {
				t.Fatalf("probe calls = %d, want 0", len(runner.specs))
			}
		})
	}
}

func TestDiscoverPresetsResolvesBinaryDirectoryMetadata(t *testing.T) {
	tests := []struct {
		name      string
		binaryDir string
		want      func(string) string
	}{
		{
			name:      "sourceDir and presetName",
			binaryDir: "${sourceDir}/.native-e2e/build/${presetName}",
			want: func(sourceDir string) string {
				return filepath.Join(sourceDir, ".native-e2e", "build", "debug")
			},
		},
		{
			name:      "relative to sourceDir",
			binaryDir: "out/debug",
			want: func(sourceDir string) string {
				return filepath.Join(sourceDir, "out", "debug")
			},
		},
		{
			name:      "empty defaults to sourceDir",
			binaryDir: "",
			want:      func(sourceDir string) string { return sourceDir },
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root, project, sourceDir := newPresetWorkspace(t)
			writePresetJSON(t, filepath.Join(sourceDir, "CMakePresets.json"), map[string]any{
				"version": 9,
				"configurePresets": []map[string]any{{
					"name": "debug", "generator": "Ninja", "binaryDir": test.binaryDir,
				}},
			})

			discovery, err := DiscoverPresets(
				context.Background(),
				successfulPresetRunner("debug"),
				presetTestInstallation(root),
				root,
				project,
			)
			if err != nil {
				t.Fatalf("DiscoverPresets() error = %v", err)
			}
			if len(discovery.Profiles) != 1 {
				t.Fatalf("Profiles = %#v, want one", discovery.Profiles)
			}
			if got, want := discovery.Profiles[0].BinaryDir, test.want(sourceDir); got != want {
				t.Fatalf("BinaryDir = %q, want %q", got, want)
			}
		})
	}
}

func TestDiscoverPresetsResolvesInheritedEnvironmentAndDefinitionFileDir(t *testing.T) {
	root, project, sourceDir := newPresetWorkspace(t)
	includeDir := filepath.Join(sourceDir, "presets")
	writePresetJSON(t, filepath.Join(includeDir, "base.json"), map[string]any{
		"version": 9,
		"configurePresets": []map[string]any{
			{
				"name":        "first",
				"hidden":      true,
				"environment": map[string]any{"ROOT": "${sourceDir}/first"},
			},
			{
				"name":   "second",
				"hidden": true,
				"environment": map[string]any{
					"ROOT": "${sourceDir}/second",
					"LEAF": "$env{ROOT}/leaf",
				},
				"generator": "Ninja",
			},
			{
				"name":      "file-base",
				"hidden":    true,
				"generator": "Ninja",
				"binaryDir": "${fileDir}/build/${presetName}",
			},
		},
	})
	writePresetJSON(t, filepath.Join(sourceDir, "CMakePresets.json"), map[string]any{
		"version": 9,
		"include": []string{"presets/base.json"},
		"configurePresets": []map[string]any{
			{
				"name":      "env-child",
				"inherits":  []string{"first", "second"},
				"binaryDir": "$env{LEAF}",
			},
			{
				"name":     "file-child",
				"inherits": "file-base",
			},
			{
				"name":        "null-child",
				"inherits":    "first",
				"generator":   "Ninja",
				"binaryDir":   "null-$env{ROOT}",
				"environment": map[string]any{"ROOT": nil},
			},
			{
				"name":        "override-child",
				"inherits":    "first",
				"generator":   "Ninja",
				"binaryDir":   "$env{ROOT}/leaf",
				"environment": map[string]any{"ROOT": "${sourceDir}/child"},
			},
		},
	})

	discovery, err := DiscoverPresets(
		context.Background(),
		successfulPresetRunner("env-child", "file-child", "null-child", "override-child"),
		presetTestInstallation(root),
		root,
		project,
	)
	if err != nil {
		t.Fatalf("DiscoverPresets() error = %v", err)
	}
	got := map[string]string{}
	for _, profile := range discovery.Profiles {
		got[profile.ConfigurePreset] = profile.BinaryDir
	}
	want := map[string]string{
		"env-child":      filepath.Join(sourceDir, "first", "leaf"),
		"file-child":     filepath.Join(sourceDir, "build", "file-child"),
		"null-child":     filepath.Join(sourceDir, "null-"),
		"override-child": filepath.Join(sourceDir, "child", "leaf"),
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("BinaryDirs = %#v, want %#v", got, want)
	}
}

func TestDiscoverPresetsFailsClosedForUnsafeBinaryDirectoryMetadata(t *testing.T) {
	tests := []struct {
		name        string
		binaryDir   string
		environment map[string]any
	}{
		{"outside root", "${sourceParentDir}/../outside", nil},
		{"unknown macro", "${unknown}/build", nil},
		{"parent environment", "$penv{HOME}/build", nil},
		{"vendor macro", "$vendor{example}/build", nil},
		{"malformed macro", "${sourceDir/build", nil},
		{"environment cycle", "$env{FIRST}", map[string]any{
			"FIRST":  "$env{SECOND}",
			"SECOND": "$env{THIRD}",
			"THIRD":  "$env{FIRST}",
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root, project, sourceDir := newPresetWorkspace(t)
			writePresetJSON(t, filepath.Join(sourceDir, "CMakePresets.json"), map[string]any{
				"version": 9,
				"configurePresets": []map[string]any{{
					"name":        "debug",
					"generator":   "Ninja",
					"binaryDir":   test.binaryDir,
					"environment": test.environment,
				}},
			})
			runner := successfulPresetRunner("debug")

			_, err := DiscoverPresets(
				context.Background(),
				runner,
				presetTestInstallation(root),
				root,
				project,
			)
			if !errors.Is(err, ErrInvalidPresets) &&
				!errors.Is(err, ErrPresetBoundary) {
				t.Fatalf("error = %v, want fail closed", err)
			}
			if len(runner.specs) != 2 {
				t.Fatalf("probe calls = %d, want authoritative listings first", len(runner.specs))
			}
		})
	}
}

func TestCanonicalPortablePathFoldsWindowsPathIdentity(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows path identity")
	}
	first := BuildProfile{ProjectID: "app", Origin: "preset", BinaryDir: `C:\Work\Build\..\BUILD\Debug`}
	second := BuildProfile{ProjectID: "app", Origin: "preset", BinaryDir: `c:/work/build/debug`}
	firstID, err := profileID(first)
	if err != nil {
		t.Fatalf("profileID(first) error = %v", err)
	}
	secondID, err := profileID(second)
	if err != nil {
		t.Fatalf("profileID(second) error = %v", err)
	}
	if firstID != secondID {
		t.Fatalf("equivalent Windows paths produced IDs %q and %q", firstID, secondID)
	}
}

func TestPresetMacroExpansionIsSinglePassBoundedAndRejectsMalformedInput(t *testing.T) {
	expanded, err := expandPresetMacroString(
		"${dollar}{sourceDir}",
		func(macro presetMacro) (string, error) {
			if macro.Family == "builtin" && macro.Name == "dollar" {
				return "$", nil
			}
			return "unexpected recursive expansion", nil
		},
	)
	if err != nil {
		t.Fatalf("expandPresetMacroString(single pass) error = %v", err)
	}
	if expanded != "${sourceDir}" {
		t.Fatalf("single-pass expansion = %q, want literal ${sourceDir}", expanded)
	}

	for _, malformed := range []string{
		"${sourceDir",
		"$env{UNFINISHED",
		"${source${dollar}}",
		"$unknown{value}",
	} {
		t.Run(malformed, func(t *testing.T) {
			_, err := expandPresetMacroString(
				malformed,
				func(presetMacro) (string, error) { return "", nil },
			)
			if err == nil {
				t.Fatalf("expandPresetMacroString(%q) succeeded, want error", malformed)
			}
		})
	}

	_, err = expandPresetMacroString(
		"${large}",
		func(presetMacro) (string, error) {
			return strings.Repeat("x", maxExpandedPathBytes+1), nil
		},
	)
	if err == nil {
		t.Fatal("oversized expansion succeeded, want error")
	}
}

func successfulPresetRunner(configureNames ...string) *presetTestRunner {
	configureOutput := "Available configure presets:\n"
	for _, name := range configureNames {
		configureOutput += "  \"" + name + "\"\n"
	}
	return &presetTestRunner{results: []probe.Result{
		{ExitCode: 0, Stdout: []byte(configureOutput)},
		{ExitCode: 0, Stdout: []byte("Available build presets:\n")},
	}}
}
