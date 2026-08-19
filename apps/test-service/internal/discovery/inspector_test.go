package discovery

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"unit-test-ide.local/test-service/internal/cmake"
	"unit-test-ide.local/test-service/internal/diagnostic"
	"unit-test-ide.local/test-service/internal/probe"
	"unit-test-ide.local/test-service/internal/toolchain"
	"unit-test-ide.local/test-service/internal/workspace"
)

func TestNewInspectorValidatesDependenciesButDoesNotStatBuildRoot(t *testing.T) {
	root := openProjectRoot(t, ".")
	buildRoot := filepath.Join(t.TempDir(), "missing", "build")
	registry, err := toolchain.NewRegistry(fakeAdapter{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewInspector(root, fakeRunner{}, cmake.ResolverConfig{}, registry, buildRoot); err != nil {
		t.Fatalf("NewInspector with missing build root descriptor: %v", err)
	}
	if _, err := NewInspector(root, nil, cmake.ResolverConfig{}, registry, buildRoot); err == nil {
		t.Fatal("NewInspector accepted nil runner")
	}
	if _, err := NewInspector(root, fakeRunner{}, cmake.ResolverConfig{}, nil, buildRoot); err == nil {
		t.Fatal("NewInspector accepted nil registry")
	}
	if _, err := NewInspector(root, fakeRunner{}, cmake.ResolverConfig{}, registry, "relative"); err == nil {
		t.Fatal("NewInspector accepted relative build root")
	}
	if _, err := NewInspector(
		root, fakeRunner{}, cmake.ResolverConfig{}, registry,
		filepath.Join(root.NativePath, "service-build"),
	); err == nil {
		t.Fatal("NewInspector accepted a build root inside the workspace")
	}
}

func TestInspectorKeepsToolchainSuccessWhenCMakeFailsAndSanitizesLocalIssues(t *testing.T) {
	root := openProjectRoot(t, ".")
	tools := fakeToolchainDiscovery{
		instances: []toolchain.Instance{testToolchain("manual", toolchain.FamilyGCC, "Ninja")},
		issues: []toolchain.Issue{{
			Code: "TOOLCHAIN_PROBE_FAILED", Message: "GITHUB_TOKEN=token-secret C:\\service-data",
		}},
	}
	inspector := newTestInspector(t, root, tools, inspectorDependencies{
		loadConfig: func(workspace.Root) (workspace.LoadResult, error) {
			return workspace.LoadResult{Config: workspace.Config{
				Version:  1,
				Projects: []workspace.ProjectConfig{{ID: "root", SourceDir: "."}},
			}}, nil
		},
		resolve: func(context.Context, probe.Runner, cmake.ResolverConfig) (cmake.Installation, error) {
			return cmake.Installation{}, errors.New("token-secret C:\\service-data\\cmake.exe")
		},
		discoverPresets: noPresetDiscovery,
	})

	snapshot, err := inspector.Inspect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Toolchains) != 1 || snapshot.Toolchains[0].ID != "manual" {
		t.Fatalf("toolchains = %#v", snapshot.Toolchains)
	}
	if len(snapshot.Profiles) != 0 || len(snapshot.Generation) != 64 {
		t.Fatalf("snapshot = %#v", snapshot)
	}
	text := diagnosticsText(snapshot)
	if !strings.Contains(text, "CMAKE_UNAVAILABLE") ||
		!strings.Contains(text, "TOOLCHAIN_PROBE_FAILED") ||
		strings.Contains(text, "token-secret") ||
		strings.Contains(text, "service-data") {
		t.Fatalf("diagnostics = %#v", snapshot.Diagnostics)
	}
}

func TestInspectorUsesPresetPriorityAndGeneratedProfilesUseCMakeConstructor(t *testing.T) {
	root := openProjectRoot(t, ".")
	config := workspace.Config{
		Version: 1,
		Projects: []workspace.ProjectConfig{{
			ID: "root", SourceDir: ".",
			Fallback: workspace.FallbackConfig{Configurations: []string{"Debug"}},
		}},
	}
	install := testInstallation()
	presetProfile := cmake.BuildProfile{
		ID: "preset-profile", ProjectID: "root", Origin: "preset",
		ConfigurePreset: "dev", BinaryDir: root.NativePath,
	}
	presetInspector := newTestInspector(t, root, fakeToolchainDiscovery{
		instances: []toolchain.Instance{testToolchain("gcc", toolchain.FamilyGCC, "Ninja")},
	}, inspectorDependencies{
		loadConfig: func(workspace.Root) (workspace.LoadResult, error) {
			return workspace.LoadResult{Config: config}, nil
		},
		resolve: successfulResolve(install),
		discoverPresets: func(
			context.Context, probe.Runner, cmake.Installation,
			workspace.Root, workspace.ProjectConfig,
		) (cmake.PresetDiscovery, error) {
			return cmake.PresetDiscovery{
				Profiles: []cmake.BuildProfile{presetProfile},
				Inputs:   []string{"CMakePresets.json"}, InputGeneration: "preset-generation",
			}, nil
		},
	})
	presetSnapshot, err := presetInspector.Inspect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(presetSnapshot.Profiles, []cmake.BuildProfile{presetProfile}) {
		t.Fatalf("preset profiles = %#v", presetSnapshot.Profiles)
	}

	generatedInspector := newTestInspector(t, root, fakeToolchainDiscovery{
		instances: []toolchain.Instance{testToolchain("gcc", toolchain.FamilyGCC, "Ninja")},
	}, inspectorDependencies{
		loadConfig: func(workspace.Root) (workspace.LoadResult, error) {
			return workspace.LoadResult{Config: config}, nil
		},
		resolve:         successfulResolve(install),
		discoverPresets: noPresetDiscovery,
	})
	generatedSnapshot, err := generatedInspector.Inspect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want, err := cmake.NewGeneratedProfile(cmake.GeneratedProfileSpec{
		ProjectID: "root", ToolchainID: "gcc", Generator: "Ninja",
		Configuration: "Debug", BuildRoot: generatedInspector.buildRoot,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(generatedSnapshot.Profiles, []cmake.BuildProfile{want}) {
		t.Fatalf("generated profiles = %#v, want %#v", generatedSnapshot.Profiles, want)
	}
}

func TestInspectorReportsUnavailableCoverageBaseProfile(t *testing.T) {
	root := openProjectRoot(t, ".")
	config := workspace.Config{
		Version:  3,
		Projects: []workspace.ProjectConfig{{ID: "root", SourceDir: "."}},
		CoverageProfiles: []workspace.CoverageProfile{{
			ID: "coverage", BaseBuildProfileID: "missing-build", Include: []string{"**"},
		}},
	}
	inspector := newTestInspector(t, root, fakeToolchainDiscovery{
		instances: []toolchain.Instance{testToolchain("gcc", toolchain.FamilyGCC, "Ninja")},
	}, inspectorDependencies{
		loadConfig: func(workspace.Root) (workspace.LoadResult, error) {
			return workspace.LoadResult{Config: config}, nil
		},
		resolve: successfulResolve(testInstallation()),
		discoverPresets: func(
			context.Context, probe.Runner, cmake.Installation,
			workspace.Root, workspace.ProjectConfig,
		) (cmake.PresetDiscovery, error) {
			return cmake.PresetDiscovery{
				Profiles: []cmake.BuildProfile{{
					ID: "available-build", ProjectID: "root", Origin: "preset",
				}},
				Inputs: []string{"CMakePresets.json"}, InputGeneration: "coverage-profile-input",
			}, nil
		},
	})

	snapshot, err := inspector.Inspect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(diagnosticsText(snapshot), "COVERAGE_PROFILE_INVALID") {
		t.Fatalf("diagnostics = %#v", snapshot.Diagnostics)
	}
	if !reflect.DeepEqual(snapshot.CoverageProfiles, config.CoverageProfiles) {
		t.Fatalf("coverage profiles = %#v, want %#v", snapshot.CoverageProfiles, config.CoverageProfiles)
	}

	t.Run("valid reference", func(t *testing.T) {
		config.CoverageProfiles[0].BaseBuildProfileID = "available-build"
		snapshot, err := inspector.Inspect(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(diagnosticsText(snapshot), "COVERAGE_PROFILE_INVALID") {
			t.Fatalf("diagnostics = %#v", snapshot.Diagnostics)
		}
	})
}

func TestInspectorRunsCMakeAndToolchainDiscoveryConcurrently(t *testing.T) {
	root := openProjectRoot(t, ".")
	cmakeStarted := make(chan struct{})
	toolchainsStarted := make(chan struct{})
	tools := fakeToolchainDiscovery{discover: func(ctx context.Context) (
		[]toolchain.Instance, []toolchain.Issue,
	) {
		close(toolchainsStarted)
		select {
		case <-cmakeStarted:
			return nil, nil
		case <-ctx.Done():
			return nil, nil
		}
	}}
	inspector := newTestInspector(t, root, tools, inspectorDependencies{
		loadConfig: func(workspace.Root) (workspace.LoadResult, error) {
			return workspace.LoadResult{Config: workspace.Config{Version: 1}}, nil
		},
		resolve: func(ctx context.Context, _ probe.Runner, _ cmake.ResolverConfig) (
			cmake.Installation, error,
		) {
			close(cmakeStarted)
			select {
			case <-toolchainsStarted:
				return testInstallation(), nil
			case <-ctx.Done():
				return cmake.Installation{}, ctx.Err()
			}
		},
		discoverPresets: noPresetDiscovery,
	})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := inspector.Inspect(ctx); err != nil {
		t.Fatalf("Inspect: %v", err)
	}
}

func TestInspectorUsesAtMostFourPresetWorkersAndIsCompletionOrderStable(t *testing.T) {
	root, projects := openMultiProjectRoot(t, 64)
	config := workspace.Config{Version: 1, Projects: projects}
	var active atomic.Int32
	var maximum atomic.Int32
	discover := func(
		ctx context.Context, _ probe.Runner, _ cmake.Installation,
		_ workspace.Root, project workspace.ProjectConfig,
	) (cmake.PresetDiscovery, error) {
		now := active.Add(1)
		defer active.Add(-1)
		for {
			seen := maximum.Load()
			if now <= seen || maximum.CompareAndSwap(seen, now) {
				break
			}
		}
		select {
		case <-time.After(10 * time.Millisecond):
		case <-ctx.Done():
			return cmake.PresetDiscovery{}, ctx.Err()
		}
		return cmake.PresetDiscovery{
			Profiles: []cmake.BuildProfile{{
				ID: "preset-" + project.ID, ProjectID: project.ID,
				Origin: "preset", ConfigurePreset: "dev",
			}},
			Inputs:          []string{"CMakePresets.json"},
			InputGeneration: "generation-" + project.ID,
		}, nil
	}
	dependencies := inspectorDependencies{
		loadConfig: func(workspace.Root) (workspace.LoadResult, error) {
			return workspace.LoadResult{Config: config}, nil
		},
		resolve:         successfulResolve(testInstallation()),
		discoverPresets: discover,
	}
	first := newTestInspector(t, root, fakeToolchainDiscovery{}, dependencies)
	firstSnapshot, err := first.Inspect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if maximum.Load() < 2 || maximum.Load() > 4 {
		t.Fatalf("maximum preset concurrency = %d, want 2..4", maximum.Load())
	}

	maximum.Store(0)
	second := newTestInspector(t, root, fakeToolchainDiscovery{}, dependencies)
	secondSnapshot, err := second.Inspect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(firstSnapshot, secondSnapshot) {
		t.Fatalf("snapshots differ by completion order:\n%#v\n%#v", firstSnapshot, secondSnapshot)
	}
}

func TestInspectorPropagatesFoundationalAndCancellationErrors(t *testing.T) {
	root := openProjectRoot(t, ".")
	foundational := errors.New("workspace config boundary failed")
	failed := newTestInspector(t, root, fakeToolchainDiscovery{}, inspectorDependencies{
		loadConfig: func(workspace.Root) (workspace.LoadResult, error) {
			return workspace.LoadResult{}, foundational
		},
		resolve:         successfulResolve(testInstallation()),
		discoverPresets: noPresetDiscovery,
	})
	if _, err := failed.Inspect(context.Background()); !errors.Is(err, foundational) {
		t.Fatalf("foundational error = %v", err)
	}

	cancelInspector := newTestInspector(t, root, fakeToolchainDiscovery{
		discover: func(ctx context.Context) ([]toolchain.Instance, []toolchain.Issue) {
			<-ctx.Done()
			return nil, nil
		},
	}, inspectorDependencies{
		loadConfig: func(workspace.Root) (workspace.LoadResult, error) {
			return workspace.LoadResult{Config: workspace.Config{Version: 1}}, nil
		},
		resolve: func(ctx context.Context, _ probe.Runner, _ cmake.ResolverConfig) (
			cmake.Installation, error,
		) {
			<-ctx.Done()
			return cmake.Installation{}, ctx.Err()
		},
		discoverPresets: noPresetDiscovery,
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := cancelInspector.Inspect(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation error = %v", err)
	}
}

func TestInspectorProjectsInvalidWorkspaceConfigurationAsBlockingDiagnostic(t *testing.T) {
	root := openProjectRoot(t, ".")
	for name, test := range map[string]struct {
		loadErr error
		code    string
	}{
		"invalid": {
			loadErr: fmt.Errorf("decode workspace: %w", workspace.ErrInvalidConfig),
			code:    "WORKSPACE_INVALID_CONFIG",
		},
		"too-large": {
			loadErr: fmt.Errorf("read workspace: %w", workspace.ErrConfigTooLarge),
			code:    "WORKSPACE_CONFIG_TOO_LARGE",
		},
	} {
		t.Run(name, func(t *testing.T) {
			inspector := newTestInspector(t, root, fakeToolchainDiscovery{}, inspectorDependencies{
				loadConfig: func(workspace.Root) (workspace.LoadResult, error) {
					return workspace.LoadResult{}, test.loadErr
				},
				resolve:         successfulResolve(testInstallation()),
				discoverPresets: noPresetDiscovery,
			})
			snapshot, err := inspector.Inspect(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if len(snapshot.Projects) != 0 || len(snapshot.Profiles) != 0 ||
				len(snapshot.Diagnostics) != 1 ||
				snapshot.Diagnostics[0].Severity != "error" ||
				snapshot.Diagnostics[0].Code != test.code {
				t.Fatalf("snapshot = %#v", snapshot)
			}
		})
	}
}

func TestInspectorKeepsValidProjectWhenAnotherProjectIsInvalid(t *testing.T) {
	root := openProjectRoot(t, "good")
	config := workspace.Config{
		Version: 1,
		Projects: []workspace.ProjectConfig{
			{ID: "bad", SourceDir: "missing"},
			{ID: "good", SourceDir: "good"},
		},
	}
	inspector := newTestInspector(t, root, fakeToolchainDiscovery{}, inspectorDependencies{
		loadConfig: func(workspace.Root) (workspace.LoadResult, error) {
			return workspace.LoadResult{Config: config}, nil
		},
		resolve: successfulResolve(testInstallation()),
		discoverPresets: func(
			_ context.Context, _ probe.Runner, _ cmake.Installation,
			_ workspace.Root, project workspace.ProjectConfig,
		) (cmake.PresetDiscovery, error) {
			return cmake.PresetDiscovery{
				Profiles: []cmake.BuildProfile{{
					ID: "profile-" + project.ID, ProjectID: project.ID, Origin: "preset",
				}},
				Inputs: []string{"CMakePresets.json"}, InputGeneration: project.ID,
			}, nil
		},
	})
	snapshot, err := inspector.Inspect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Profiles) != 1 || snapshot.Profiles[0].ProjectID != "good" ||
		!strings.Contains(diagnosticsText(snapshot), "PROJECT_INVALID") {
		t.Fatalf("snapshot = %#v", snapshot)
	}
}

func TestInspectorAppliesFamilyGeneratorPolicyAndDoesNotGuessConfigurations(t *testing.T) {
	root := openProjectRoot(t, ".")
	tools := []toolchain.Instance{
		testToolchain("gcc", toolchain.FamilyGCC, "Unix Makefiles", "Ninja"),
		testToolchain("clang", toolchain.FamilyClang, "Unix Makefiles"),
		testToolchain("msvc", toolchain.FamilyMSVC, "Visual Studio 17 2022", "Ninja"),
		testToolchain("msvc-ninja", toolchain.FamilyMSVC, "Ninja"),
		testToolchain("clang-cl", toolchain.FamilyClangCL, "Visual Studio 17 2022", "Ninja"),
	}
	config := workspace.Config{
		Version: 1,
		Projects: []workspace.ProjectConfig{
			{ID: "empty", SourceDir: "."},
			{
				ID: "generated", SourceDir: ".",
				Fallback: workspace.FallbackConfig{Configurations: []string{"Debug"}},
			},
		},
	}
	inspector := newTestInspector(t, root, fakeToolchainDiscovery{instances: tools}, inspectorDependencies{
		loadConfig: func(workspace.Root) (workspace.LoadResult, error) {
			return workspace.LoadResult{Config: config}, nil
		},
		resolve:         successfulResolve(testInstallation()),
		discoverPresets: noPresetDiscovery,
	})
	snapshot, err := inspector.Inspect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	got := make(map[string]string)
	for _, profile := range snapshot.Profiles {
		got[profile.ToolchainID] = profile.Generator
		if profile.ProjectID != "generated" {
			t.Fatalf("guessed profile for empty config: %#v", profile)
		}
	}
	want := map[string]string{
		"gcc": "Ninja", "clang": "Unix Makefiles",
		"msvc": "Visual Studio 17 2022", "msvc-ninja": "Ninja", "clang-cl": "Ninja",
	}
	if !reflect.DeepEqual(got, want) ||
		!strings.Contains(diagnosticsText(snapshot), "PROJECT_HAS_NO_BUILD_PROFILE") {
		t.Fatalf("snapshot = %#v", snapshot)
	}
}

func TestInspectorReportsPreferredGeneratorAndMissingToolchainWithoutProfiles(t *testing.T) {
	root := openProjectRoot(t, ".")
	config := workspace.Config{
		Version: 1,
		Projects: []workspace.ProjectConfig{{
			ID: "root", SourceDir: ".",
			Fallback: workspace.FallbackConfig{
				Configurations: []string{"Debug"}, PreferredGenerator: "Ninja",
			},
		}},
	}
	missingGenerator := newTestInspector(t, root, fakeToolchainDiscovery{
		instances: []toolchain.Instance{
			testToolchain("gcc", toolchain.FamilyGCC, "Unix Makefiles"),
		},
	}, inspectorDependencies{
		loadConfig: func(workspace.Root) (workspace.LoadResult, error) {
			return workspace.LoadResult{Config: config}, nil
		},
		resolve:         successfulResolve(testInstallation()),
		discoverPresets: noPresetDiscovery,
	})
	snapshot, err := missingGenerator.Inspect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Profiles) != 0 ||
		!strings.Contains(diagnosticsText(snapshot), "GENERATOR_UNAVAILABLE") {
		t.Fatalf("snapshot = %#v", snapshot)
	}

	noTools := newTestInspector(t, root, fakeToolchainDiscovery{}, inspectorDependencies{
		loadConfig: func(workspace.Root) (workspace.LoadResult, error) {
			return workspace.LoadResult{Config: config}, nil
		},
		resolve:         successfulResolve(testInstallation()),
		discoverPresets: noPresetDiscovery,
	})
	snapshot, err = noTools.Inspect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Profiles) != 0 ||
		!strings.Contains(diagnosticsText(snapshot), "TOOLCHAIN_NOT_FOUND") {
		t.Fatalf("snapshot = %#v", snapshot)
	}
}

func TestInspectorGenerationTracksFullManualDescriptorAndPresetInputGeneration(t *testing.T) {
	root := openProjectRoot(t, ".")
	config := workspace.Config{
		Version: 1,
		Projects: []workspace.ProjectConfig{{
			ID: "root", SourceDir: ".",
			Fallback: workspace.FallbackConfig{Configurations: []string{"Debug"}},
		}},
	}
	makeInspector := func(instance toolchain.Instance, inputGeneration string) *Inspector {
		return newTestInspector(t, root, fakeToolchainDiscovery{
			instances: []toolchain.Instance{instance},
		}, inspectorDependencies{
			loadConfig: func(workspace.Root) (workspace.LoadResult, error) {
				return workspace.LoadResult{Config: config}, nil
			},
			resolve: successfulResolve(testInstallation()),
			discoverPresets: func(
				context.Context, probe.Runner, cmake.Installation,
				workspace.Root, workspace.ProjectConfig,
			) (cmake.PresetDiscovery, error) {
				return cmake.PresetDiscovery{
					Inputs:          []string{"CMakePresets.json"},
					InputGeneration: inputGeneration,
				}, nil
			},
		})
	}
	firstToolchain := testToolchain("manual", toolchain.FamilyGCC, "Ninja")
	firstToolchain.Environment = []string{"PATH=/tools/one"}
	secondToolchain := firstToolchain
	secondToolchain.Environment = []string{"PATH=/tools/two"}

	first, err := makeInspector(firstToolchain, "preset-a").Inspect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	descriptorChanged, err := makeInspector(secondToolchain, "preset-a").Inspect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	presetChanged, err := makeInspector(firstToolchain, "preset-b").Inspect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if first.Generation == descriptorChanged.Generation ||
		first.Generation == presetChanged.Generation ||
		len(first.Profiles) != 0 {
		t.Fatalf("generations = %q %q %q", first.Generation, descriptorChanged.Generation, presetChanged.Generation)
	}
}

func TestInspectorRejectsProfileLimitBeforeMultiplication(t *testing.T) {
	root := openProjectRoot(t, ".")
	configurations := make([]string, 64)
	for index := range configurations {
		configurations[index] = fmt.Sprintf("Config%02d", index)
	}
	instances := make([]toolchain.Instance, 65)
	for index := range instances {
		instances[index] = testToolchain(
			fmt.Sprintf("gcc-%02d", index), toolchain.FamilyGCC, "Ninja",
		)
	}
	config := workspace.Config{
		Version: 1,
		Projects: []workspace.ProjectConfig{{
			ID: "root", SourceDir: ".",
			Fallback: workspace.FallbackConfig{Configurations: configurations},
		}},
	}
	inspector := newTestInspector(t, root, fakeToolchainDiscovery{instances: instances}, inspectorDependencies{
		loadConfig: func(workspace.Root) (workspace.LoadResult, error) {
			return workspace.LoadResult{Config: config}, nil
		},
		resolve:         successfulResolve(testInstallation()),
		discoverPresets: noPresetDiscovery,
	})
	snapshot, err := inspector.Inspect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Profiles) != 0 ||
		!strings.Contains(diagnosticsText(snapshot), "PROFILE_LIMIT_EXCEEDED") {
		t.Fatalf("snapshot = %#v", snapshot)
	}
}

func TestInspectorBoundsDiagnosticsAndDefensivelyCopiesDependencies(t *testing.T) {
	root := openProjectRoot(t, ".")
	instance := testToolchain("manual", toolchain.FamilyGCC, "Ninja")
	instance.Environment = []string{"PATH=/toolchain"}
	issues := make([]toolchain.Issue, 5000)
	for index := range issues {
		issues[index] = toolchain.Issue{
			Code: "TOOLCHAIN_PROBE_FAILED", Message: "token-secret", Blocking: index%2 == 0,
		}
	}
	config := workspace.Config{Version: 1}
	inspector := newTestInspector(t, root, fakeToolchainDiscovery{
		instances: []toolchain.Instance{instance}, issues: issues,
	}, inspectorDependencies{
		loadConfig: func(workspace.Root) (workspace.LoadResult, error) {
			return workspace.LoadResult{Config: config}, nil
		},
		resolve:         successfulResolve(testInstallation()),
		discoverPresets: noPresetDiscovery,
	})
	snapshot, err := inspector.Inspect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Diagnostics) != 4096 ||
		!strings.Contains(diagnosticsText(snapshot), "DIAGNOSTIC_LIMIT_EXCEEDED") ||
		strings.Contains(diagnosticsText(snapshot), "token-secret") {
		t.Fatalf("diagnostic count = %d", len(snapshot.Diagnostics))
	}
	instance.Environment[0] = "MUTATED=1"
	issues[0].Code = "MUTATED"
	if snapshot.Toolchains[0].Environment[0] != "PATH=/toolchain" {
		t.Fatalf("dependency mutation leaked: %#v", snapshot.Toolchains)
	}
	instance.Environment[0] = "PATH=/toolchain"
	snapshot.Toolchains[0].Environment[0] = "CALLER=1"
	second, err := inspector.Inspect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if second.Toolchains[0].Environment[0] != "PATH=/toolchain" {
		t.Fatalf("caller mutation leaked: %#v", second.Toolchains)
	}
}

func TestInspectorPassesWorkspaceCMakeOverrideToResolver(t *testing.T) {
	root := openProjectRoot(t, ".")
	const override = `C:\Trusted Tools\cmake.exe`
	configs := make(chan cmake.ResolverConfig, 1)
	inspector := newTestInspector(t, root, fakeToolchainDiscovery{}, inspectorDependencies{
		loadConfig: func(workspace.Root) (workspace.LoadResult, error) {
			return workspace.LoadResult{Config: workspace.Config{
				Version: 1, CMake: workspace.CMakeConfig{Executable: override},
			}}, nil
		},
		resolve: func(
			_ context.Context, _ probe.Runner, config cmake.ResolverConfig,
		) (cmake.Installation, error) {
			configs <- config
			return testInstallation(), nil
		},
		discoverPresets: noPresetDiscovery,
	})
	if _, err := inspector.Inspect(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case config := <-configs:
		if config.Override != override {
			t.Fatalf("resolver override = %q, want %q", config.Override, override)
		}
	default:
		t.Fatal("resolver was not called")
	}
}

func TestInspectorPresetFailureDoesNotHideOtherProjectAndDoesNotLeakError(t *testing.T) {
	root, projects := openMultiProjectRoot(t, 2)
	config := workspace.Config{Version: 1, Projects: projects}
	inspector := newTestInspector(t, root, fakeToolchainDiscovery{}, inspectorDependencies{
		loadConfig: func(workspace.Root) (workspace.LoadResult, error) {
			return workspace.LoadResult{Config: config}, nil
		},
		resolve: successfulResolve(testInstallation()),
		discoverPresets: func(
			_ context.Context, _ probe.Runner, _ cmake.Installation,
			_ workspace.Root, project workspace.ProjectConfig,
		) (cmake.PresetDiscovery, error) {
			if project.ID == projects[0].ID {
				return cmake.PresetDiscovery{}, errors.New("token-secret C:\\service-data")
			}
			return cmake.PresetDiscovery{
				Profiles: []cmake.BuildProfile{{
					ID: "good-profile", ProjectID: project.ID, Origin: "preset",
				}},
				Inputs: []string{"CMakePresets.json"}, InputGeneration: "good-generation",
			}, nil
		},
	})
	snapshot, err := inspector.Inspect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	text := diagnosticsText(snapshot)
	if len(snapshot.Profiles) != 1 ||
		!strings.Contains(text, "CMAKE_PRESET_INVALID") ||
		strings.Contains(text, "token-secret") || strings.Contains(text, "service-data") {
		t.Fatalf("snapshot = %#v", snapshot)
	}
}

func TestInspectorToolchainOrderDoesNotChangeSnapshotOrGeneration(t *testing.T) {
	root := openProjectRoot(t, ".")
	config := workspace.Config{
		Version: 1,
		Projects: []workspace.ProjectConfig{{
			ID: "root", SourceDir: ".",
			Fallback: workspace.FallbackConfig{Configurations: []string{"Debug"}},
		}},
	}
	gcc := testToolchain("gcc", toolchain.FamilyGCC, "Ninja")
	clang := testToolchain("clang", toolchain.FamilyClang, "Unix Makefiles")
	dependencies := inspectorDependencies{
		loadConfig: func(workspace.Root) (workspace.LoadResult, error) {
			return workspace.LoadResult{Config: config}, nil
		},
		resolve:         successfulResolve(testInstallation()),
		discoverPresets: noPresetDiscovery,
	}
	firstInspector := newTestInspector(t, root, fakeToolchainDiscovery{
		instances: []toolchain.Instance{gcc, clang},
	}, dependencies)
	first, err := firstInspector.Inspect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	secondInspector, err := newInspector(
		root, fakeRunner{}, cmake.ResolverConfig{},
		fakeToolchainDiscovery{instances: []toolchain.Instance{clang, gcc}},
		firstInspector.buildRoot, dependencies,
	)
	if err != nil {
		t.Fatal(err)
	}
	second, err := secondInspector.Inspect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("snapshots differ:\n%#v\n%#v", first, second)
	}
}

func TestInspectorDependencyPanicReturnsBoundedInvariantError(t *testing.T) {
	root := openProjectRoot(t, ".")
	inspector := newTestInspector(t, root, fakeToolchainDiscovery{}, inspectorDependencies{
		loadConfig: func(workspace.Root) (workspace.LoadResult, error) {
			return workspace.LoadResult{Config: workspace.Config{Version: 1}}, nil
		},
		resolve: func(context.Context, probe.Runner, cmake.ResolverConfig) (
			cmake.Installation, error,
		) {
			panic("token-secret")
		},
		discoverPresets: noPresetDiscovery,
	})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err := inspector.Inspect(ctx)
	if !errors.Is(err, ErrInspectorInvariant) ||
		strings.Contains(fmt.Sprint(err), "token-secret") {
		t.Fatalf("panic error = %v", err)
	}
}

func TestInspectorFileURIHandlesWindowsUNCAndPOSIXForms(t *testing.T) {
	cases := map[string]string{
		`C:\work tree\src.cpp`:      "file:///C:/work%20tree/src.cpp",
		`\\server\share\sdk.hpp`:    "file://server/share/sdk.hpp",
		`/opt/sdk/include/header.h`: "file:///opt/sdk/include/header.h",
	}
	for input, want := range cases {
		if got := fileURI(input); got != want {
			t.Fatalf("fileURI(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestInspectorDiagnosticIDDoesNotDependOnWorkspaceTemporaryRoot(t *testing.T) {
	inspect := func(root workspace.Root) Snapshot {
		config := workspace.Config{
			Version:  1,
			Projects: []workspace.ProjectConfig{{ID: "missing", SourceDir: "missing"}},
		}
		inspector := newTestInspector(t, root, fakeToolchainDiscovery{}, inspectorDependencies{
			loadConfig: func(workspace.Root) (workspace.LoadResult, error) {
				return workspace.LoadResult{Config: config}, nil
			},
			resolve:         successfulResolve(testInstallation()),
			discoverPresets: noPresetDiscovery,
		})
		snapshot, err := inspector.Inspect(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		return snapshot
	}
	first := inspect(openProjectRoot(t, "."))
	second := inspect(openProjectRoot(t, "."))
	if len(first.Diagnostics) != 1 || len(second.Diagnostics) != 1 ||
		first.Diagnostics[0].ID != second.Diagnostics[0].ID {
		t.Fatalf("diagnostic IDs = %#v / %#v", first.Diagnostics, second.Diagnostics)
	}
}

func TestBoundDiagnosticsReservesLimitNoticeAcrossCountAndByteBoundary(t *testing.T) {
	values := make([]diagnostic.Diagnostic, 4097)
	for index := range values {
		values[index] = diagnostic.Diagnostic{
			Source: "workspace", Severity: "warning", Code: "X",
			Message: strings.Repeat("m", 2031),
		}
	}
	got := boundDiagnostics(values)
	if len(got) != 4096 {
		t.Fatalf("bounded diagnostics = %d, want 4096", len(got))
	}
	ordinary := 0
	for _, value := range got {
		if value.Code == "X" {
			ordinary++
		}
	}
	if ordinary != 4095 || got[len(got)-1].Code != "DIAGNOSTIC_LIMIT_EXCEEDED" {
		t.Fatalf("ordinary = %d, tail = %#v", ordinary, got[len(got)-1])
	}
}

func TestToolchainGenerationDescriptorPreservesUNCDoubleSlash(t *testing.T) {
	unc := testToolchain("manual", toolchain.FamilyGCC, "Ninja")
	unc.CCompiler = `\\server\share\bin\gcc.exe`
	unc.CXXCompiler = `\\server\share\bin\g++.exe`
	rooted := unc
	rooted.CCompiler = `/server/share/bin/gcc.exe`
	rooted.CXXCompiler = `/server/share/bin/g++.exe`

	if got := canonicalPath(unc.CCompiler); got != "//server/share/bin/gcc.exe" {
		t.Fatalf("canonical UNC = %q", got)
	}
	if toolchainGenerationDescriptors([]toolchain.Instance{unc})[0] ==
		toolchainGenerationDescriptors([]toolchain.Instance{rooted})[0] {
		t.Fatal("UNC descriptor collapsed into a single-root path")
	}
}

func TestAssignDiagnosticIDsDoesNotMutateRelatedURIAndIsRootStable(t *testing.T) {
	firstRoot := "file:///C:/temp/workspace-one"
	secondRoot := "file:///C:/temp/workspace-two"
	first := []diagnostic.Diagnostic{{
		Source: "workspace", Severity: "error", Code: "PROJECT_INVALID",
		Message: "broken", FileURI: firstRoot + "/src/main.cpp",
		Related: []diagnostic.Related{{
			Message: "declared here", FileURI: firstRoot + "/src/header.hpp",
		}},
	}}
	second := []diagnostic.Diagnostic{{
		Source: "workspace", Severity: "error", Code: "PROJECT_INVALID",
		Message: "broken", FileURI: secondRoot + "/src/main.cpp",
		Related: []diagnostic.Related{{
			Message: "declared here", FileURI: secondRoot + "/src/header.hpp",
		}},
	}}
	assignDiagnosticIDs(first, firstRoot)
	assignDiagnosticIDs(second, secondRoot)
	if first[0].Related[0].FileURI != firstRoot+"/src/header.hpp" {
		t.Fatalf("related URI mutated to %q", first[0].Related[0].FileURI)
	}
	if first[0].ID != second[0].ID {
		t.Fatalf("root-stable IDs differ: %q != %q", first[0].ID, second[0].ID)
	}
}

func TestInspectorBlockingPresetIssueSuppressesProfilesAndFallback(t *testing.T) {
	root := openProjectRoot(t, ".")
	config := workspace.Config{
		Version: 1,
		Projects: []workspace.ProjectConfig{{
			ID: "root", SourceDir: ".",
			Fallback: workspace.FallbackConfig{Configurations: []string{"Debug"}},
		}},
	}
	inspector := newTestInspector(t, root, fakeToolchainDiscovery{
		instances: []toolchain.Instance{testToolchain("gcc", toolchain.FamilyGCC, "Ninja")},
	}, inspectorDependencies{
		loadConfig: func(workspace.Root) (workspace.LoadResult, error) {
			return workspace.LoadResult{Config: config}, nil
		},
		resolve: successfulResolve(testInstallation()),
		discoverPresets: func(
			context.Context, probe.Runner, cmake.Installation,
			workspace.Root, workspace.ProjectConfig,
		) (cmake.PresetDiscovery, error) {
			return cmake.PresetDiscovery{
				Profiles: []cmake.BuildProfile{{
					ID: "unsafe", ProjectID: "root", Origin: "preset",
				}},
				Inputs: []string{"CMakePresets.json"}, InputGeneration: "blocked-input",
				Issues: []cmake.Issue{{
					Code: "CMAKE_PRESET_INVALID", Message: "blocked", Blocking: true,
				}},
			}, nil
		},
	})
	snapshot, err := inspector.Inspect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Profiles) != 0 ||
		!strings.Contains(diagnosticsText(snapshot), "CMAKE_PRESET_INVALID") {
		t.Fatalf("snapshot = %#v", snapshot)
	}
}

type fakeRunner struct{}

func (fakeRunner) Run(context.Context, probe.Spec) (probe.Result, error) {
	return probe.Result{}, errors.New("unexpected probe")
}

type fakeAdapter struct{}

func (fakeAdapter) Discover(context.Context) ([]toolchain.Instance, error) { return nil, nil }
func (fakeAdapter) Probe(context.Context, toolchain.Candidate) (toolchain.Instance, error) {
	return toolchain.Instance{}, nil
}

type fakeToolchainDiscovery struct {
	instances []toolchain.Instance
	issues    []toolchain.Issue
	discover  func(context.Context) ([]toolchain.Instance, []toolchain.Issue)
}

func (f fakeToolchainDiscovery) Discover(ctx context.Context) ([]toolchain.Instance, []toolchain.Issue) {
	if f.discover != nil {
		return f.discover(ctx)
	}
	return f.instances, f.issues
}

func openProjectRoot(t *testing.T, relative string) workspace.Root {
	t.Helper()
	rootPath := t.TempDir()
	source := filepath.Join(rootPath, relative)
	if err := os.MkdirAll(source, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "CMakeLists.txt"), []byte("cmake_minimum_required(VERSION 3.20)\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	root, err := workspace.OpenRoot(rootPath)
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func openMultiProjectRoot(t *testing.T, count int) (workspace.Root, []workspace.ProjectConfig) {
	t.Helper()
	rootPath := t.TempDir()
	projects := make([]workspace.ProjectConfig, 0, count)
	for index := 0; index < count; index++ {
		id := fmt.Sprintf("project-%02d", index)
		source := filepath.Join(rootPath, id)
		if err := os.MkdirAll(source, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(
			filepath.Join(source, "CMakeLists.txt"),
			[]byte("cmake_minimum_required(VERSION 3.20)\n"),
			0o600,
		); err != nil {
			t.Fatal(err)
		}
		projects = append(projects, workspace.ProjectConfig{ID: id, SourceDir: id})
	}
	root, err := workspace.OpenRoot(rootPath)
	if err != nil {
		t.Fatal(err)
	}
	return root, projects
}

func testToolchain(id string, family toolchain.Family, generators ...string) toolchain.Instance {
	return toolchain.Instance{
		ID: id, Family: family,
		CCompiler: "C:/toolchain/cc.exe", CXXCompiler: "C:/toolchain/cxx.exe",
		Version: "1.0", TargetTriple: "x86_64-test",
		HostArchitecture: "x64", TargetArchitecture: "x64",
		Generators: generators,
	}
}

func testInstallation() cmake.Installation {
	return cmake.Installation{
		Executable: "C:/cmake/bin/cmake.exe", Version: "4.3.4",
		Source: cmake.SourceBundle, Identity: "cmake-identity",
		LicensePath: "C:/cmake/LICENSE.rst",
	}
}

func successfulResolve(install cmake.Installation) func(
	context.Context, probe.Runner, cmake.ResolverConfig,
) (cmake.Installation, error) {
	return func(context.Context, probe.Runner, cmake.ResolverConfig) (cmake.Installation, error) {
		return install, nil
	}
}

func noPresetDiscovery(
	context.Context, probe.Runner, cmake.Installation,
	workspace.Root, workspace.ProjectConfig,
) (cmake.PresetDiscovery, error) {
	return cmake.PresetDiscovery{InputGeneration: "empty-preset-graph"}, nil
}

func newTestInspector(
	t *testing.T,
	root workspace.Root,
	tools toolchainDiscovery,
	dependencies inspectorDependencies,
) *Inspector {
	t.Helper()
	buildRoot := filepath.Join(t.TempDir(), "service-build")
	value, err := newInspector(
		root, fakeRunner{}, cmake.ResolverConfig{}, tools, buildRoot, dependencies,
	)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func diagnosticsText(snapshot Snapshot) string {
	var builder strings.Builder
	for _, value := range snapshot.Diagnostics {
		builder.WriteString(value.Code)
		builder.WriteByte(' ')
		builder.WriteString(value.Message)
		builder.WriteByte('\n')
	}
	return builder.String()
}
