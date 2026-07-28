package cmake

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"unit-test-ide.local/test-service/internal/probe"
	"unit-test-ide.local/test-service/internal/workspace"
)

func TestDiscoverPresetsUsesImplicitUserRelationshipLegalIncludeAndFixedListings(t *testing.T) {
	root, project, sourceDir := newPresetWorkspace(t)
	installPresetFixtures(t, sourceDir)

	installation := presetTestInstallation(root)
	runner := &presetTestRunner{results: []probe.Result{
		{
			ExitCode: 0,
			Stdout: []byte(
				"Available configure presets:\n\n" +
					"  \"user-debug\" - User Debug\n" +
					"  \"included-release\"\n" +
					"  \"project-debug\" - Project Debug\n",
			),
		},
		{
			ExitCode: 0,
			Stdout: []byte(
				"Available build presets:\n\n" +
					"  \"user-build\" - User Build\n" +
					"  \"included-build\"\n" +
					"  \"project-build\" - Project Build\n",
			),
		},
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

	wantSpecs := []probe.Spec{
		{
			Executable: installation.Executable,
			Args:       []string{"--list-presets=configure"},
			Env:        []string{},
			Dir:        sourceDir,
			Timeout:    5 * time.Second,
			MaxOutput:  256 * 1024,
		},
		{
			Executable: installation.Executable,
			Args:       []string{"--build", "--list-presets"},
			Env:        []string{},
			Dir:        sourceDir,
			Timeout:    5 * time.Second,
			MaxOutput:  256 * 1024,
		},
	}
	if !reflect.DeepEqual(runner.specs, wantSpecs) {
		t.Fatalf("probe specs = %#v, want %#v", runner.specs, wantSpecs)
	}

	wantInputs := []string{
		canonicalRelativePath("project/CMakePresets.json"),
		canonicalRelativePath("project/CMakeUserPresets.json"),
		canonicalRelativePath("project/included.json"),
	}
	if !reflect.DeepEqual(discovery.Inputs, wantInputs) ||
		!sort.StringsAreSorted(discovery.Inputs) {
		t.Fatalf("Inputs = %#v, want canonical sorted relative paths %#v", discovery.Inputs, wantInputs)
	}
	if len(discovery.Profiles) != 3 {
		t.Fatalf("Profiles = %#v, want three authoritative combinations", discovery.Profiles)
	}
	gotPairs := make([]string, 0, len(discovery.Profiles))
	for _, profile := range discovery.Profiles {
		if len(profile.ID) != 64 || strings.ToLower(profile.ID) != profile.ID {
			t.Fatalf("profile ID = %q, want lowercase SHA-256", profile.ID)
		}
		gotPairs = append(gotPairs, profile.ConfigurePreset+"/"+profile.BuildPreset)
	}
	wantPairs := []string{
		"included-release/included-build",
		"project-debug/project-build",
		"user-debug/user-build",
	}
	if !reflect.DeepEqual(gotPairs, wantPairs) {
		t.Fatalf("profile pairs = %#v, want %#v", gotPairs, wantPairs)
	}
}

func TestDiscoverPresetsTreatsMissingTopLevelFilesAsNoPresets(t *testing.T) {
	root, project, _ := newPresetWorkspace(t)
	runner := &presetTestRunner{}

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
	if len(discovery.Profiles) != 0 || len(discovery.Inputs) != 0 || len(discovery.Issues) != 0 {
		t.Fatalf("Discovery = %#v, want normal empty result", discovery)
	}
	const emptyGraphGeneration = "62b512d8c76bbed3048fc81bd94bd2aeaab2de4d3b36edbed2f3d3dd8ff3e33e"
	if discovery.InputGeneration != emptyGraphGeneration {
		t.Fatalf("InputGeneration = %q, want %q", discovery.InputGeneration, emptyGraphGeneration)
	}
	if len(runner.specs) != 0 {
		t.Fatalf("probe calls = %d, want 0", len(runner.specs))
	}
}

func TestDiscoverPresetsSupportsUserPresetsWithoutProjectPresets(t *testing.T) {
	root, project, sourceDir := newPresetWorkspace(t)
	writePresetJSON(t, filepath.Join(sourceDir, "CMakeUserPresets.json"), map[string]any{
		"version": 6,
		"configurePresets": []map[string]any{{
			"name":      "user-debug",
			"generator": "Ninja",
			"binaryDir": "${sourceDir}/out/user-debug",
		}},
	})
	runner := &presetTestRunner{results: []probe.Result{
		{
			ExitCode: 0,
			Stdout:   []byte("Available configure presets:\n  \"user-debug\"\n"),
		},
		{
			ExitCode: 0,
			Stdout:   []byte("Available build presets:\n"),
		},
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
	if len(runner.specs) != 2 {
		t.Fatalf("probe calls = %d, want 2", len(runner.specs))
	}
	if len(discovery.Profiles) != 1 ||
		discovery.Profiles[0].ConfigurePreset != "user-debug" {
		t.Fatalf("Profiles = %#v, want user-debug", discovery.Profiles)
	}
}

func TestDiscoverPresetsRejectsMalformedInputsBeforeProbe(t *testing.T) {
	tests := []struct {
		name  string
		build func(*testing.T, string)
	}{
		{
			name: "malformed JSON",
			build: func(t *testing.T, sourceDir string) {
				t.Helper()
				writePresetBytes(t, filepath.Join(sourceDir, "CMakePresets.json"), []byte(`{"version":`))
			},
		},
		{
			name: "unknown include shape",
			build: func(t *testing.T, sourceDir string) {
				t.Helper()
				writePresetJSON(t, filepath.Join(sourceDir, "CMakePresets.json"), map[string]any{
					"version": 6,
					"include": map[string]any{"path": "included.json"},
				})
			},
		},
		{
			name: "null include",
			build: func(t *testing.T, sourceDir string) {
				t.Helper()
				writePresetBytes(
					t,
					filepath.Join(sourceDir, "CMakePresets.json"),
					[]byte(`{"version":6,"include":null}`),
				)
			},
		},
		{
			name: "trailing JSON value",
			build: func(t *testing.T, sourceDir string) {
				t.Helper()
				writePresetBytes(
					t,
					filepath.Join(sourceDir, "CMakePresets.json"),
					[]byte(`{"version":6} {"version":6}`),
				)
			},
		},
		{
			name: "duplicate include key",
			build: func(t *testing.T, sourceDir string) {
				t.Helper()
				writePresetBytes(
					t,
					filepath.Join(sourceDir, "CMakePresets.json"),
					[]byte(`{"version":6,"include":["../../outside.json"],"include":[]}`),
				)
			},
		},
		{
			name: "include is not a regular file",
			build: func(t *testing.T, sourceDir string) {
				t.Helper()
				writePresetJSON(t, filepath.Join(sourceDir, "CMakePresets.json"), map[string]any{
					"version": 6,
					"include": []string{"directory.json"},
				})
				if err := os.Mkdir(filepath.Join(sourceDir, "directory.json"), 0o755); err != nil {
					t.Fatalf("Mkdir() error = %v", err)
				}
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root, project, sourceDir := newPresetWorkspace(t)
			test.build(t, sourceDir)
			runner := &presetTestRunner{}

			_, err := DiscoverPresets(
				context.Background(),
				runner,
				presetTestInstallation(root),
				root,
				project,
			)
			if !errors.Is(err, ErrInvalidPresets) {
				t.Fatalf("error = %v, want ErrInvalidPresets", err)
			}
			if len(runner.specs) != 0 {
				t.Fatalf("probe calls = %d, want 0", len(runner.specs))
			}
		})
	}
}

func TestDiscoverPresetsStopsAtFailingProbeStage(t *testing.T) {
	probeFailure := errors.New("probe unavailable")
	tests := []struct {
		name      string
		results   []probe.Result
		errs      []error
		wantError error
		wantCalls int
	}{
		{
			name:      "configure runner error",
			errs:      []error{probeFailure},
			wantError: probeFailure,
			wantCalls: 1,
		},
		{
			name:      "configure output limit",
			errs:      []error{probe.ErrOutputLimit},
			wantError: probe.ErrOutputLimit,
			wantCalls: 1,
		},
		{
			name: "configure nonzero exit",
			results: []probe.Result{{
				ExitCode: 1,
				Stderr:   []byte("configure listing failed"),
			}},
			wantError: ErrPresetListing,
			wantCalls: 1,
		},
		{
			name: "configure stderr on success",
			results: []probe.Result{{
				ExitCode: 0,
				Stdout:   []byte("Available configure presets:\n  \"project-debug\"\n"),
				Stderr:   []byte("unexpected warning"),
			}},
			wantError: ErrPresetListing,
			wantCalls: 1,
		},
		{
			name: "build output limit after configure success",
			results: []probe.Result{
				{
					ExitCode: 0,
					Stdout:   []byte("Available configure presets:\n  \"project-debug\"\n"),
				},
			},
			errs:      []error{nil, probe.ErrOutputLimit},
			wantError: probe.ErrOutputLimit,
			wantCalls: 2,
		},
		{
			name: "build nonzero after configure success",
			results: []probe.Result{
				{
					ExitCode: 0,
					Stdout:   []byte("Available configure presets:\n  \"project-debug\"\n"),
				},
				{
					ExitCode: 2,
					Stderr:   []byte("build listing failed"),
				},
			},
			wantError: ErrPresetListing,
			wantCalls: 2,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root, project, sourceDir := newPresetWorkspace(t)
			writePresetJSON(t, filepath.Join(sourceDir, "CMakePresets.json"), map[string]any{
				"version": 6,
				"configurePresets": []map[string]any{{
					"name": "project-debug",
				}},
			})
			runner := &presetTestRunner{results: test.results, errs: test.errs}

			_, err := DiscoverPresets(
				context.Background(),
				runner,
				presetTestInstallation(root),
				root,
				project,
			)
			if !errors.Is(err, test.wantError) {
				t.Fatalf("error = %v, want %v", err, test.wantError)
			}
			if len(runner.specs) != test.wantCalls {
				t.Fatalf("probe calls = %d, want %d", len(runner.specs), test.wantCalls)
			}
		})
	}
}

func TestDiscoverPresetsFailsClosedWhenInputChangesDuringListing(t *testing.T) {
	root, project, sourceDir := newPresetWorkspace(t)
	presetPath := filepath.Join(sourceDir, "CMakePresets.json")
	writePresetJSON(t, presetPath, map[string]any{
		"version": 6,
		"configurePresets": []map[string]any{{
			"name":      "project-debug",
			"generator": "Ninja",
		}},
	})
	runner := &mutatingPresetTestRunner{
		presetTestRunner: presetTestRunner{results: []probe.Result{
			{
				ExitCode: 0,
				Stdout:   []byte("Available configure presets:\n  \"project-debug\"\n"),
			},
			{
				ExitCode: 0,
				Stdout:   []byte("Available build presets:\n"),
			},
		}},
		mutate: func() error {
			data, err := json.Marshal(map[string]any{
				"version": 6,
				"configurePresets": []map[string]any{{
					"name":      "project-debug",
					"generator": "Unix Makefiles",
				}},
			})
			if err != nil {
				return err
			}
			return os.WriteFile(presetPath, data, 0o600)
		},
	}

	_, err := DiscoverPresets(
		context.Background(),
		runner,
		presetTestInstallation(root),
		root,
		project,
	)
	if runner.mutationErr == nil {
		if !errors.Is(err, ErrInvalidPresets) {
			t.Fatalf("mutation succeeded; error = %v, want ErrInvalidPresets", err)
		}
		return
	}
	if err != nil {
		t.Fatalf("mutation was blocked (%v), but discovery failed: %v", runner.mutationErr, err)
	}
}

func TestDiscoverPresetsRejectsBoundedGraphViolationsBeforeProbe(t *testing.T) {
	tests := []struct {
		name  string
		build func(*testing.T, string)
	}{
		{
			name: "single file size",
			build: func(t *testing.T, sourceDir string) {
				t.Helper()
				writePresetJSON(t, filepath.Join(sourceDir, "CMakePresets.json"), map[string]any{
					"version": 6,
					"padding": strings.Repeat("x", 256*1024),
				})
			},
		},
		{
			name: "file count",
			build: func(t *testing.T, sourceDir string) {
				t.Helper()
				includes := make([]string, 64)
				for index := range includes {
					includes[index] = filepath.ToSlash(
						filepath.Join("leaves", fmt.Sprintf("leaf-%02d.json", index)),
					)
					writePresetJSON(t, filepath.Join(sourceDir, filepath.FromSlash(includes[index])), map[string]any{
						"version": 6,
					})
				}
				writePresetJSON(t, filepath.Join(sourceDir, "CMakePresets.json"), map[string]any{
					"version": 6,
					"include": includes,
				})
			},
		},
		{
			name: "include depth",
			build: func(t *testing.T, sourceDir string) {
				t.Helper()
				const finalDepth = 17
				for depth := 0; depth <= finalDepth; depth++ {
					name := "CMakePresets.json"
					if depth > 0 {
						name = fmt.Sprintf("depth-%02d.json", depth)
					}
					document := map[string]any{"version": 6}
					if depth < finalDepth {
						document["include"] = []string{fmt.Sprintf("depth-%02d.json", depth+1)}
					}
					writePresetJSON(t, filepath.Join(sourceDir, name), document)
				}
			},
		},
		{
			name: "total input bytes",
			build: func(t *testing.T, sourceDir string) {
				t.Helper()
				includes := make([]string, 5)
				for index := range includes {
					includes[index] = fmt.Sprintf("large-%02d.json", index)
					writePresetJSON(t, filepath.Join(sourceDir, includes[index]), map[string]any{
						"version": 6,
						"padding": strings.Repeat("x", 240*1024),
					})
				}
				writePresetJSON(t, filepath.Join(sourceDir, "CMakePresets.json"), map[string]any{
					"version": 6,
					"include": includes,
				})
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root, project, sourceDir := newPresetWorkspace(t)
			test.build(t, sourceDir)
			runner := &presetTestRunner{}

			_, err := DiscoverPresets(
				context.Background(),
				runner,
				presetTestInstallation(root),
				root,
				project,
			)
			if !errors.Is(err, ErrPresetLimit) {
				t.Fatalf("error = %v, want ErrPresetLimit", err)
			}
			if len(runner.specs) != 0 {
				t.Fatalf("probe calls = %d, want 0", len(runner.specs))
			}
		})
	}
}

func TestDiscoverPresetsRejectsCycleBeforeProbe(t *testing.T) {
	root, project, sourceDir := newPresetWorkspace(t)
	writePresetJSON(t, filepath.Join(sourceDir, "CMakePresets.json"), map[string]any{
		"version": 6,
		"include": []string{"included.json"},
	})
	writePresetJSON(t, filepath.Join(sourceDir, "included.json"), map[string]any{
		"version": 6,
		"include": []string{"CMakePresets.json"},
	})
	runner := &presetTestRunner{}

	_, err := DiscoverPresets(
		context.Background(),
		runner,
		presetTestInstallation(root),
		root,
		project,
	)
	if !errors.Is(err, ErrPresetCycle) {
		t.Fatalf("error = %v, want ErrPresetCycle", err)
	}
	if len(runner.specs) != 0 {
		t.Fatalf("probe calls = %d, want 0", len(runner.specs))
	}
}

func TestDiscoverPresetsRejectsEscapesBeforeProbe(t *testing.T) {
	tests := []struct {
		name    string
		include func(*testing.T, workspace.Root, string) string
	}{
		{
			name: "direct escape",
			include: func(t *testing.T, root workspace.Root, sourceDir string) string {
				t.Helper()
				writePresetJSON(t, filepath.Join(filepath.Dir(root.NativePath), "outside.json"), map[string]any{
					"version": 6,
				})
				return "../../outside.json"
			},
		},
		{
			name: "directory link escape",
			include: func(t *testing.T, _ workspace.Root, sourceDir string) string {
				t.Helper()
				outside := t.TempDir()
				writePresetJSON(t, filepath.Join(outside, "included.json"), map[string]any{
					"version": 6,
				})
				createDirectoryLink(t, filepath.Join(sourceDir, "external"), outside)
				return "external/included.json"
			},
		},
		{
			name: "directory link missing tail escape",
			include: func(t *testing.T, _ workspace.Root, sourceDir string) string {
				t.Helper()
				outside := t.TempDir()
				createDirectoryLink(t, filepath.Join(sourceDir, "external"), outside)
				return "external/not-created.json"
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root, project, sourceDir := newPresetWorkspace(t)
			include := test.include(t, root, sourceDir)
			writePresetJSON(t, filepath.Join(sourceDir, "CMakePresets.json"), map[string]any{
				"version": 6,
				"include": []string{include},
			})
			runner := &presetTestRunner{}

			_, err := DiscoverPresets(
				context.Background(),
				runner,
				presetTestInstallation(root),
				root,
				project,
			)
			if !errors.Is(err, ErrPresetBoundary) {
				t.Fatalf("error = %v, want ErrPresetBoundary", err)
			}
			if len(runner.specs) != 0 {
				t.Fatalf("probe calls = %d, want 0", len(runner.specs))
			}
		})
	}
}

func TestDiscoverPresetsRejectsAmbiguousListings(t *testing.T) {
	tests := []struct {
		name   string
		output string
	}{
		{
			name:   "missing quotes",
			output: "Available configure presets:\n  project-debug\n",
		},
		{
			name:   "multiple quoted fields",
			output: "Available configure presets:\n  \"project-debug\" - \"Display\"\n",
		},
		{
			name:   "tab alignment",
			output: "Available configure presets:\n  \"project-debug\"\t- Display\n",
		},
		{
			name:   "tab in display",
			output: "Available configure presets:\n  \"project-debug\" - Dis\tplay\n",
		},
		{
			name:   "trailing display whitespace",
			output: "Available configure presets:\n  \"project-debug\" - Display \n",
		},
		{
			name:   "duplicate name",
			output: "Available configure presets:\n  \"project-debug\"\n  \"project-debug\" - Project Debug\n",
		},
		{
			name:   "stray line",
			output: "Available configure presets:\n  \"project-debug\"\nwarning: ignored\n",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root, project, sourceDir := newPresetWorkspace(t)
			writePresetJSON(t, filepath.Join(sourceDir, "CMakePresets.json"), map[string]any{
				"version": 6,
				"configurePresets": []map[string]any{{
					"name": "project-debug",
				}},
			})
			runner := &presetTestRunner{results: []probe.Result{{
				ExitCode: 0,
				Stdout:   []byte(test.output),
			}}}

			_, err := DiscoverPresets(
				context.Background(),
				runner,
				presetTestInstallation(root),
				root,
				project,
			)
			if !errors.Is(err, ErrPresetListing) {
				t.Fatalf("error = %v, want ErrPresetListing", err)
			}
			if len(runner.specs) != 1 {
				t.Fatalf("probe calls = %d, want parser rejection before build listing", len(runner.specs))
			}
		})
	}
}

func TestDiscoverPresetsAcceptsSpaceAlignedDisplayColumns(t *testing.T) {
	root, project, sourceDir := newPresetWorkspace(t)
	writePresetJSON(t, filepath.Join(sourceDir, "CMakePresets.json"), map[string]any{
		"version": 6,
		"configurePresets": []map[string]any{
			{"name": "short", "generator": "Ninja", "binaryDir": "out/short"},
			{"name": "substantially-long", "generator": "Ninja", "binaryDir": "out/long"},
		},
	})
	runner := &presetTestRunner{results: []probe.Result{
		{
			ExitCode: 0,
			Stdout: []byte(
				"Available configure presets:\n" +
					"  \"short\"              - Short Display\n" +
					"  \"substantially-long\" - Long Display\n",
			),
		},
		{
			ExitCode: 0,
			Stdout:   []byte("Available build presets:\n"),
		},
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
	if len(discovery.Profiles) != 2 {
		t.Fatalf("Profiles = %#v, want two aligned listing names", discovery.Profiles)
	}
}

type presetTestRunner struct {
	results []probe.Result
	errs    []error
	specs   []probe.Spec
}

type mutatingPresetTestRunner struct {
	presetTestRunner
	mutate      func() error
	mutationErr error
}

func (runner *mutatingPresetTestRunner) Run(
	ctx context.Context,
	spec probe.Spec,
) (probe.Result, error) {
	result, err := runner.presetTestRunner.Run(ctx, spec)
	if len(runner.specs) == 1 {
		runner.mutationErr = runner.mutate()
	}
	return result, err
}

func (runner *presetTestRunner) Run(_ context.Context, spec probe.Spec) (probe.Result, error) {
	runner.specs = append(runner.specs, spec)
	index := len(runner.specs) - 1
	var result probe.Result
	if index < len(runner.results) {
		result = runner.results[index]
	}
	var err error
	if index < len(runner.errs) {
		err = runner.errs[index]
	}
	return result, err
}

func newPresetWorkspace(t *testing.T) (workspace.Root, workspace.ProjectConfig, string) {
	t.Helper()
	rootDir := t.TempDir()
	sourceDir := filepath.Join(rootDir, "project")
	if err := os.MkdirAll(sourceDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	root, err := workspace.OpenRoot(rootDir)
	if err != nil {
		t.Fatalf("OpenRoot() error = %v", err)
	}
	sourceDir, err = root.ResolveRelative("project")
	if err != nil {
		t.Fatalf("ResolveRelative(project) error = %v", err)
	}
	return root, workspace.ProjectConfig{ID: "app", SourceDir: "project"}, sourceDir
}

func presetTestInstallation(root workspace.Root) Installation {
	return Installation{
		Executable: filepath.Join(root.NativePath, "tools", "cmake"),
		Version:    "4.3.4",
		Source:     SourceBundle,
		Identity:   strings.Repeat("a", 64),
	}
}

func writePresetJSON(t *testing.T, path string, value any) {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("Marshal(%q) error = %v", path, err)
	}
	writePresetBytes(t, path, data)
}

func writePresetBytes(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll(%q) error = %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("WriteFile(%q) error = %v", path, err)
	}
}

func installPresetFixtures(t *testing.T, sourceDir string) {
	t.Helper()
	for _, name := range []string{"CMakePresets.json", "CMakeUserPresets.json", "included.json"} {
		data, err := os.ReadFile(filepath.Join("testdata", "presets", name))
		if err != nil {
			t.Fatalf("ReadFile(%q) error = %v", name, err)
		}
		writePresetBytes(t, filepath.Join(sourceDir, name), data)
	}
}
