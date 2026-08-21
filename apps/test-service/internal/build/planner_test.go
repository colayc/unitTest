package build

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"

	"unit-test-ide.local/test-service/internal/cmake"
	"unit-test-ide.local/test-service/internal/coveragebundle"
	"unit-test-ide.local/test-service/internal/task"
	"unit-test-ide.local/test-service/internal/toolchain"
	"unit-test-ide.local/test-service/internal/workspace"
)

func TestPlannerBuildsValidatedConfigureAndBuildSteps(t *testing.T) {
	fixture := newPlannerFixture(t)
	state := json.RawMessage(`{"workspaceGeneration":"` + fixture.generation + `"}`)
	plan, err := Plan(PlanInput{
		Installation:   fixture.installation,
		WorkspaceRoot:  fixture.root,
		Project:        fixture.project,
		Profile:        fixture.profile,
		Toolchain:      fixture.toolchain,
		Targets:        []cmake.Target{{ID: fixture.targetID, Name: "unit-tests"}},
		TargetIDs:      []string{fixture.targetID},
		Jobs:           8,
		Configure:      true,
		ConfigureState: state,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Steps) != 2 || plan.Steps[0].Kind != task.StepConfigure ||
		plan.Steps[1].Kind != task.StepBuild {
		t.Fatalf("Plan() steps = %#v", plan.Steps)
	}
	wantConfigure := []string{
		"-S", fixture.sourceDir,
		"-B", fixture.profile.BinaryDir,
		"-G", "Ninja",
		"-DCMAKE_BUILD_TYPE=Debug",
		"-DCMAKE_C_COMPILER=" + filepath.ToSlash(fixture.toolchain.CCompiler),
		"-DCMAKE_CXX_COMPILER=" + filepath.ToSlash(fixture.toolchain.CXXCompiler),
	}
	if !reflect.DeepEqual(plan.Steps[0].Process.Args, wantConfigure) {
		t.Fatalf("configure args = %#v, want %#v", plan.Steps[0].Process.Args, wantConfigure)
	}
	wantBuild := []string{
		"--build", fixture.profile.BinaryDir,
		"--config", "Debug",
		"--parallel", strconv.Itoa(8),
		"--target", "unit-tests",
	}
	if !reflect.DeepEqual(plan.Steps[1].Process.Args, wantBuild) {
		t.Fatalf("build args = %#v, want %#v", plan.Steps[1].Process.Args, wantBuild)
	}
	if !reflect.DeepEqual(plan.Steps[0].State, state) {
		t.Fatalf("configure state = %s, want %s", plan.Steps[0].State, state)
	}
	if plan.Fingerprint == "" || plan.Fingerprint != task.FingerprintPlan(plan) {
		t.Fatalf("plan fingerprint = %q", plan.Fingerprint)
	}
	boundary, err := NewExecutionBoundary(fixture.installation, fixture.root, fixture.dataRoot)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = boundary.(task.ManagedExecutionBoundary).Release()
	})
	if err := task.ValidatePlan(plan, boundary); err != nil {
		t.Fatalf("ValidatePlan() error = %v", err)
	}
}

func TestNativeBuildLaunchPlanDeclaresClosedWindowsCoverageTree(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows launch declaration")
	}
	root := `C:\fixture\cmake`
	toolRoot := `C:\fixture\llvm\bin`
	shell := `C:\Windows\System32\cmd.exe`
	t.Setenv("ComSpec", shell)
	input := PlanInput{
		Installation: cmake.Installation{
			Root: root, Executable: filepath.Join(root, "bin", "cmake.exe"),
			CTestExecutable:      filepath.Join(root, "bin", "ctest.exe"),
			UnityRunnerGenerator: cmake.ProductExecutable{Path: `C:\fixture\tools\unity-runner-generator.exe`},
		},
		Profile: cmake.BuildProfile{Generator: "Ninja"},
		Toolchain: toolchain.Instance{
			Family:      toolchain.FamilyClangCL,
			CCompiler:   filepath.Join(toolRoot, "clang-cl.exe"),
			CXXCompiler: filepath.Join(toolRoot, "clang-cl.exe"),
			Coverage: toolchain.CoverageCapability{
				LLVMProfdata: filepath.Join(toolRoot, "llvm-profdata.exe"),
				LLVMCov:      filepath.Join(toolRoot, "llvm-cov.exe"),
			},
		},
		Targets: []cmake.Target{{Type: "EXECUTABLE", Artifacts: []string{`C:\fixture\build\coverage-tests.exe`}}},
	}
	want := []string{
		filepath.Join(root, "bin", "cmake.exe"),
		filepath.Join(root, "bin", "ctest.exe"),
		`C:\fixture\tools\unity-runner-generator.exe`,
		filepath.Join(toolRoot, "clang-cl.exe"),
		filepath.Join(root, "bin", "ninja.exe"),
		filepath.Join(toolRoot, "lld-link.exe"),
		filepath.Join(toolRoot, "llvm-lib.exe"),
		filepath.Join(toolRoot, "llvm-profdata.exe"),
		filepath.Join(toolRoot, "llvm-cov.exe"),
		shell,
		`C:\fixture\build\coverage-tests.exe`,
	}
	got, err := nativeBuildLaunchPlan(input)
	if err != nil {
		t.Fatalf("nativeBuildLaunchPlan() error = %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("nativeBuildLaunchPlan() = %#v, want closed declaration %#v", got, want)
	}
}

func TestPlannerRejectsUnknownCustomCommandBeforeCreatingExecutionPlan(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows launch declaration")
	}
	activatePlannerWFPRegistration(t)
	for _, test := range []struct {
		name    string
		fixture string
		wantErr bool
	}{
		{name: "declared CMake command", fixture: "custom-command-known"},
		{name: "unknown executable", fixture: "custom-command-unknown", wantErr: true},
		{name: "configure execute process", fixture: "execute-process-unknown", wantErr: true},
		{name: "configure try run", fixture: "try-run-unknown", wantErr: true},
		{name: "compiler launcher", fixture: "compiler-launcher-unknown", wantErr: true},
		{name: "rule launcher", fixture: "rule-launcher-unknown", wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newPlannerFixture(t)
			if err := os.RemoveAll(fixture.sourceDir); err != nil {
				t.Fatal(err)
			}
			if err := os.CopyFS(fixture.sourceDir, os.DirFS(filepath.Join("testdata", test.fixture))); err != nil {
				t.Fatal(err)
			}
			_, err := Plan(PlanInput{
				Installation: fixture.installation, WorkspaceRoot: fixture.root,
				Project: fixture.project, Profile: fixture.profile,
				Toolchain: fixture.toolchain, Jobs: 1, Configure: true,
			})
			if test.wantErr && !errors.Is(err, task.ErrInvalidArgument) {
				t.Fatalf("Plan() error = %v, want task.ErrInvalidArgument before execution plan", err)
			}
			if !test.wantErr && err != nil {
				t.Fatalf("Plan() error = %v, want declared CMake command accepted", err)
			}
		})
	}
}

func TestPlannerRejectsPresetLauncherAndConfigureTimeGraphMutation(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows launch declaration")
	}
	activatePlannerWFPRegistration(t)
	for _, fixtureName := range []string{"preset-launcher-unknown", "preset-toolchain-unknown", "configure-write-unknown"} {
		t.Run(fixtureName, func(t *testing.T) {
			fixture := newPlannerFixture(t)
			if err := os.RemoveAll(fixture.sourceDir); err != nil {
				t.Fatal(err)
			}
			if err := os.CopyFS(fixture.sourceDir, os.DirFS(filepath.Join("testdata", fixtureName))); err != nil {
				t.Fatal(err)
			}
			if strings.HasPrefix(fixtureName, "preset-") {
				fixture.profile.Origin = "preset"
				fixture.profile.ConfigurePreset = "child"
			}
			if _, err := Plan(PlanInput{
				Installation: fixture.installation, WorkspaceRoot: fixture.root,
				Project: fixture.project, Profile: fixture.profile,
				Toolchain: fixture.toolchain, Jobs: 1, Configure: true,
			}); !errors.Is(err, task.ErrInvalidArgument) {
				t.Fatalf("Plan() error = %v, want fail-closed rejection", err)
			}
		})
	}
}

func TestResolveLaunchPresetUsesCMakeMultipleInheritancePrecedence(t *testing.T) {
	presets := map[string]launchPreset{
		"first": {
			name: "first",
			cache: map[string]json.RawMessage{
				"CONFLICT": json.RawMessage(`"first"`),
			},
			environment: map[string]json.RawMessage{
				"ENV_CONFLICT": json.RawMessage(`"first"`),
			},
		},
		"second": {
			name: "second",
			cache: map[string]json.RawMessage{
				"CONFLICT": json.RawMessage(`"second"`),
			},
			environment: map[string]json.RawMessage{
				"ENV_CONFLICT": json.RawMessage(`"second"`),
			},
		},
		"inherited": {
			name:        "inherited",
			inherits:    []string{"first", "second"},
			cache:       map[string]json.RawMessage{},
			environment: map[string]json.RawMessage{},
		},
		"child": {
			name:     "child",
			inherits: []string{"first", "second"},
			cache: map[string]json.RawMessage{
				"CONFLICT": json.RawMessage(`"child"`),
			},
			environment: map[string]json.RawMessage{
				"ENV_CONFLICT": json.RawMessage(`"child"`),
			},
		},
	}
	for _, test := range []struct {
		name string
		want string
	}{
		{name: "inherited", want: "first"},
		{name: "child", want: "child"},
	} {
		t.Run(test.name, func(t *testing.T) {
			resolved, err := resolveLaunchPreset(test.name, presets, make(map[string]bool), 0)
			if err != nil {
				t.Fatal(err)
			}
			cacheValue, err := launchPresetValue(resolved.cache["CONFLICT"])
			if err != nil || cacheValue != test.want {
				t.Fatalf("cache conflict = %q, %v, want %q", cacheValue, err, test.want)
			}
			environmentValue, err := launchPresetValue(resolved.environment["ENV_CONFLICT"])
			if err != nil || environmentValue != test.want {
				t.Fatalf("environment conflict = %q, %v, want %q", environmentValue, err, test.want)
			}
		})
	}
}

func TestPlannerRejectsPresetCompilerAndLinkerOptionOverrides(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows launch declaration")
	}
	activatePlannerWFPRegistration(t)
	for _, test := range []struct {
		name       string
		presets    []map[string]any
		presetName string
	}{
		{
			name: "cache compiler plugin",
			presets: []map[string]any{{
				"name": "debug", "cacheVariables": map[string]string{"CMAKE_CXX_FLAGS": "-fpass-plugin=unknown.dll"},
			}},
			presetName: "debug",
		},
		{
			name: "environment compiler plugin",
			presets: []map[string]any{{
				"name": "debug", "environment": map[string]string{"CXXFLAGS": "/clang:-fplugin=unknown.dll"},
			}},
			presetName: "debug",
		},
		{
			name: "environment linker plugin",
			presets: []map[string]any{{
				"name": "debug", "environment": map[string]string{"LDFLAGS": "-Wl,-plugin,unknown.dll"},
			}},
			presetName: "debug",
		},
		{
			name: "inherited cache flags",
			presets: []map[string]any{
				{"name": "parent", "cacheVariables": map[string]string{"CMAKE_EXE_LINKER_FLAGS": "-Wl,-plugin,unknown.dll"}},
				{"name": "child", "inherits": "parent"},
			},
			presetName: "child",
		},
		{
			name: "inherited environment flags",
			presets: []map[string]any{
				{"name": "parent", "environment": map[string]string{"CPPFLAGS": "-fpass-plugin=unknown.dll"}},
				{"name": "child", "inherits": "parent"},
			},
			presetName: "child",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newPlannerFixture(t)
			fixture.profile.Origin = "preset"
			fixture.profile.ConfigurePreset = test.presetName
			document, err := json.Marshal(map[string]any{"version": 6, "configurePresets": test.presets})
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(fixture.sourceDir, "CMakePresets.json"), document, 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := Plan(PlanInput{
				Installation: fixture.installation, WorkspaceRoot: fixture.root,
				Project: fixture.project, Profile: fixture.profile,
				Toolchain: fixture.toolchain, Jobs: 1, Configure: true,
			}); !errors.Is(err, task.ErrInvalidArgument) {
				t.Fatalf("Plan() error = %v, want preset option override rejected", err)
			}
		})
	}
}

func TestPlannerRejectsPresetCompilerSelectorsOutsideClosedEnvironment(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows launch declaration")
	}
	activatePlannerWFPRegistration(t)
	planWithSelector := func(t *testing.T, variable string, value func(plannerFixture) string) error {
		t.Helper()
		fixture := newPlannerFixture(t)
		fixture.profile.Origin = "preset"
		fixture.profile.ConfigurePreset = "debug"
		selectorValue := value(fixture)
		fixture.toolchain.Environment = append(fixture.toolchain.Environment, variable+"="+selectorValue)
		document, err := json.Marshal(map[string]any{
			"version": 6,
			"configurePresets": []map[string]any{{
				"name": "debug", "environment": map[string]string{variable: selectorValue},
			}},
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(fixture.sourceDir, "CMakePresets.json"), document, 0o600); err != nil {
			t.Fatal(err)
		}
		_, err = Plan(PlanInput{
			Installation: fixture.installation, WorkspaceRoot: fixture.root,
			Project: fixture.project, Profile: fixture.profile,
			Toolchain: fixture.toolchain, Jobs: 1, Configure: true,
		})
		return err
	}

	for _, variable := range []string{"CC", "CXX", "FC", "OBJC", "OBJCXX", "ASM", "CUDA", "HIP", "ISPC", "SWIFT"} {
		t.Run("rejects unknown "+variable, func(t *testing.T) {
			if err := planWithSelector(t, variable, func(plannerFixture) string { return "unknown.exe" }); !errors.Is(err, task.ErrInvalidArgument) {
				t.Fatalf("Plan() error = %v, want unregistered %s compiler rejected", err, variable)
			}
		})
	}

	for _, test := range []struct {
		name  string
		value func(plannerFixture) string
	}{
		{name: "dynamic", value: func(plannerFixture) string { return "$env{TRUSTED_CXX}" }},
		{name: "registered non-compiler", value: func(fixture plannerFixture) string { return fixture.installation.Executable }},
	} {
		t.Run("rejects "+test.name+" CXX", func(t *testing.T) {
			if err := planWithSelector(t, "CXX", test.value); !errors.Is(err, task.ErrInvalidArgument) {
				t.Fatalf("Plan() error = %v, want %s CXX rejected", err, test.name)
			}
		})
	}

	t.Run("rejects exact captured registered CXX compiler", func(t *testing.T) {
		if err := planWithSelector(t, "CXX", func(fixture plannerFixture) string { return fixture.toolchain.CXXCompiler }); !errors.Is(err, task.ErrInvalidArgument) {
			t.Fatalf("Plan() error = %v, want compiler selector rejected by closed environment", err)
		}
	})
}

func TestPlannerValidatesPresetExecutableDiscoveryEnvironmentAgainstToolchainCapture(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows launch declaration")
	}
	activatePlannerWFPRegistration(t)
	planWithEnvironment := func(t *testing.T, captured []string, preset map[string]string) error {
		t.Helper()
		fixture := newPlannerFixture(t)
		fixture.profile.Origin = "preset"
		fixture.profile.ConfigurePreset = "debug"
		fixture.toolchain.Environment = append([]string(nil), captured...)
		document, err := json.Marshal(map[string]any{
			"version": 6,
			"configurePresets": []map[string]any{{
				"name": "debug", "environment": preset,
			}},
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(fixture.sourceDir, "CMakePresets.json"), document, 0o600); err != nil {
			t.Fatal(err)
		}
		_, err = Plan(PlanInput{
			Installation: fixture.installation, WorkspaceRoot: fixture.root,
			Project: fixture.project, Profile: fixture.profile,
			Toolchain: fixture.toolchain, Jobs: 1, Configure: true,
		})
		return err
	}

	for _, test := range []struct {
		name     string
		captured []string
		preset   map[string]string
	}{
		{
			name: "PATH prepend", captured: []string{"PATH=trusted"},
			preset: map[string]string{"PATH": "evilbin;trusted"},
		},
		{
			name: "COMSPEC replacement", captured: []string{"PATH=trusted"},
			preset: map[string]string{"COMSPEC": "evil.exe"},
		},
		{
			name: "PATHEXT replacement", captured: []string{"PATH=trusted", "PATHEXT=.COM;.EXE"},
			preset: map[string]string{"PATHEXT": ".COM;.EXE;.EVIL"},
		},
		{
			name: "COMPILER_PATH even when captured", captured: []string{"PATH=trusted", "COMPILER_PATH=evilbin"},
			preset: map[string]string{"COMPILER_PATH": "evilbin"},
		},
		{
			name: "GCC_EXEC_PREFIX even when captured", captured: []string{"PATH=trusted", "GCC_EXEC_PREFIX=evilprefix"},
			preset: map[string]string{"GCC_EXEC_PREFIX": "evilprefix"},
		},
		{
			name: "CCC_OVERRIDE_OPTIONS even when captured", captured: []string{"PATH=trusted", "CCC_OVERRIDE_OPTIONS=+-load +evil"},
			preset: map[string]string{"CCC_OVERRIDE_OPTIONS": "+-load +evil"},
		},
	} {
		t.Run("rejects "+test.name, func(t *testing.T) {
			if err := planWithEnvironment(t, test.captured, test.preset); !errors.Is(err, task.ErrInvalidArgument) {
				t.Fatalf("Plan() error = %v, want preset %s rejected", err, test.name)
			}
		})
	}

	t.Run("accepts exact captured PATH", func(t *testing.T) {
		if err := planWithEnvironment(t, []string{"PATH=trusted"}, map[string]string{"Path": "trusted"}); err != nil {
			t.Fatalf("Plan() error = %v, want exact captured PATH accepted", err)
		}
	})
}

func TestPlannerValidatesPresetDiscoveryAndPerLanguageToolCacheVariables(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows launch declaration")
	}
	activatePlannerWFPRegistration(t)
	planWithCache := func(t *testing.T, variable string, value func(*plannerFixture) string) error {
		t.Helper()
		fixture := newPlannerFixture(t)
		contents := "project(fixture LANGUAGES CXX)\nadd_executable(fixture-test main.cpp)\nenable_testing()\nadd_test(NAME fixture-test COMMAND fixture-test)\n"
		if err := os.WriteFile(filepath.Join(fixture.sourceDir, "CMakeLists.txt"), []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
		fixture.profile.Origin = "preset"
		fixture.profile.ConfigurePreset = "debug"
		cacheValue := value(&fixture)
		document, err := json.Marshal(map[string]any{
			"version": 6,
			"configurePresets": []map[string]any{{
				"name": "debug", "cacheVariables": map[string]string{variable: cacheValue},
			}},
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(fixture.sourceDir, "CMakePresets.json"), document, 0o600); err != nil {
			t.Fatal(err)
		}
		_, err = Plan(PlanInput{
			Installation: fixture.installation, WorkspaceRoot: fixture.root,
			Project: fixture.project, Profile: fixture.profile,
			Toolchain: fixture.toolchain, Jobs: 1, Configure: true,
		})
		return err
	}

	for _, test := range []struct {
		variable string
		value    string
	}{
		{variable: "CMAKE_PROGRAM_PATH", value: "evilbin"},
		{variable: "CMAKE_CXX_COMPILER_AR", value: "unknown-wrapper.exe"},
		{variable: "CMAKE_CXX_COMPILER_RANLIB", value: "unknown-wrapper.exe"},
		{variable: "CMAKE_CXX_COMPILER_TARGET", value: "unknown-target"},
		{variable: "CMAKE_CROSSCOMPILING_EMULATOR", value: "unknown-emulator.exe"},
		{variable: "CMAKE_TEST_LAUNCHER", value: "unknown-test-launcher.exe"},
		{variable: "CMAKE_MT", value: "unknown-mt.exe"},
		{variable: "CMAKE_OBJCOPY", value: "unknown-objcopy.exe"},
	} {
		t.Run("rejects "+test.variable, func(t *testing.T) {
			if err := planWithCache(t, test.variable, func(*plannerFixture) string { return test.value }); !errors.Is(err, task.ErrInvalidArgument) {
				t.Fatalf("Plan() error = %v, want %s rejected", err, test.variable)
			}
		})
	}

	t.Run("accepts exact registered compiler archiver", func(t *testing.T) {
		if err := planWithCache(t, "CMAKE_CXX_COMPILER_AR", func(fixture *plannerFixture) string {
			return filepath.Join(filepath.Dir(fixture.toolchain.CXXCompiler), "ar.exe")
		}); err != nil {
			t.Fatalf("Plan() error = %v, want exact registered compiler archiver accepted", err)
		}
	})

	t.Run("accepts exact toolchain target", func(t *testing.T) {
		if err := planWithCache(t, "CMAKE_CXX_COMPILER_TARGET", func(fixture *plannerFixture) string {
			fixture.toolchain.TargetTriple = "x86_64-pc-windows-gnu"
			return fixture.toolchain.TargetTriple
		}); err != nil {
			t.Fatalf("Plan() error = %v, want exact toolchain target accepted", err)
		}
	})
}

func TestPlannerPinsEarlierInheritedPresetScriptLikeCMake(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows launch declaration")
	}
	activatePlannerWFPRegistration(t)
	fixture := newPlannerFixture(t)
	safe := filepath.Join(fixture.sourceDir, "safe.cmake")
	evil := filepath.Join(fixture.sourceDir, "evil.cmake")
	if err := os.WriteFile(safe, []byte("# pinned inherited script\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(evil, []byte("execute_process(COMMAND unknown.exe)\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	document := map[string]any{
		"version": 4,
		"configurePresets": []any{
			map[string]any{"name": "first", "cacheVariables": map[string]string{"CMAKE_PROJECT_TOP_LEVEL_INCLUDES": "safe.cmake"}},
			map[string]any{"name": "second", "cacheVariables": map[string]string{"CMAKE_PROJECT_TOP_LEVEL_INCLUDES": "evil.cmake"}},
			map[string]any{"name": "child", "inherits": []string{"first", "second"}},
		},
	}
	content, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fixture.sourceDir, "CMakePresets.json"), content, 0o600); err != nil {
		t.Fatal(err)
	}
	fixture.profile.Origin = "preset"
	fixture.profile.ConfigurePreset = "child"
	plan, err := Plan(PlanInput{
		Installation: fixture.installation, WorkspaceRoot: fixture.root,
		Project: fixture.project, Profile: fixture.profile,
		Toolchain: fixture.toolchain, Jobs: 1, Configure: true,
	})
	if err != nil {
		t.Fatalf("Plan() error = %v, want first inherited preset preferred", err)
	}
	inputs := plan.Steps[0].Process.LaunchInputs
	if !slices.ContainsFunc(inputs, func(value cmake.FingerprintFile) bool { return strings.EqualFold(value.Path, safe) }) {
		t.Fatalf("LaunchInputs = %#v, want selected inherited script %q pinned", inputs, safe)
	}
	if slices.ContainsFunc(inputs, func(value cmake.FingerprintFile) bool { return strings.EqualFold(value.Path, evil) }) {
		t.Fatalf("LaunchInputs = %#v, later conflicting parent script must not win", inputs)
	}
}

func TestPlannerPinsCMakeScriptLoadersFromPresetsAndSource(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows launch declaration")
	}
	activatePlannerWFPRegistration(t)
	for _, test := range []struct {
		name        string
		cmakeLists  string
		presetField string
		presetValue string
		environment bool
	}{
		{
			name:        "preset top-level include",
			cmakeLists:  "project(fixture)\n",
			presetField: "CMAKE_PROJECT_TOP_LEVEL_INCLUDES",
			presetValue: "evil.cmake",
		},
		{
			name:        "preset environment toolchain",
			cmakeLists:  "project(fixture)\n",
			presetField: "CMAKE_TOOLCHAIN_FILE",
			presetValue: "evil.cmake",
			environment: true,
		},
		{
			name:       "source top-level include",
			cmakeLists: "set(CMAKE_PROJECT_TOP_LEVEL_INCLUDES evil.cmake)\nproject(fixture)\n",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newPlannerFixture(t)
			if err := os.WriteFile(filepath.Join(fixture.sourceDir, "CMakeLists.txt"), []byte(test.cmakeLists), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(fixture.sourceDir, "evil.cmake"), []byte("execute_process(COMMAND unknown.exe)\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			if test.presetField != "" {
				preset := map[string]any{
					"name": "child",
				}
				if test.environment {
					preset["environment"] = map[string]string{test.presetField: test.presetValue}
				} else {
					preset["cacheVariables"] = map[string]string{test.presetField: test.presetValue}
				}
				document := map[string]any{
					"version":          4,
					"configurePresets": []any{preset},
				}
				content, err := json.Marshal(document)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(fixture.sourceDir, "CMakePresets.json"), content, 0o600); err != nil {
					t.Fatal(err)
				}
				fixture.profile.Origin = "preset"
				fixture.profile.ConfigurePreset = "child"
			}
			if _, err := Plan(PlanInput{
				Installation: fixture.installation, WorkspaceRoot: fixture.root,
				Project: fixture.project, Profile: fixture.profile,
				Toolchain: fixture.toolchain, Jobs: 1, Configure: true,
			}); !errors.Is(err, task.ErrInvalidArgument) {
				t.Fatalf("Plan() error = %v, want script loader rejected before execution plan", err)
			}
		})
	}
}

func TestPlannerPinsLiteralListAppendedCMakeScriptLoader(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows launch declaration")
	}
	activatePlannerWFPRegistration(t)
	for _, operation := range []string{
		"APPEND CMAKE_PROJECT_TOP_LEVEL_INCLUDES safe.cmake",
		"PREPEND CMAKE_PROJECT_TOP_LEVEL_INCLUDES safe.cmake",
		"INSERT CMAKE_PROJECT_TOP_LEVEL_INCLUDES 0 safe.cmake",
	} {
		t.Run(strings.Fields(operation)[0], func(t *testing.T) {
			fixture := newPlannerFixture(t)
			safe := filepath.Join(fixture.sourceDir, "safe.cmake")
			if err := os.WriteFile(safe, []byte("# list-mutated safe script\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			contents := "list(" + operation + ")\nproject(fixture)\n"
			if err := os.WriteFile(filepath.Join(fixture.sourceDir, "CMakeLists.txt"), []byte(contents), 0o600); err != nil {
				t.Fatal(err)
			}
			plan, err := Plan(PlanInput{
				Installation: fixture.installation, WorkspaceRoot: fixture.root,
				Project: fixture.project, Profile: fixture.profile,
				Toolchain: fixture.toolchain, Jobs: 1, Configure: true,
			})
			if err != nil {
				t.Fatalf("Plan() error = %v, want literal loader accepted", err)
			}
			if !slices.ContainsFunc(plan.Steps[0].Process.LaunchInputs, func(value cmake.FingerprintFile) bool {
				return strings.EqualFold(value.Path, safe)
			}) {
				t.Fatalf("LaunchInputs = %#v, want list-mutated script %q pinned", plan.Steps[0].Process.LaunchInputs, safe)
			}
		})
	}
}

func TestPlannerRejectsUnsafeControlledCMakeListMutation(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows launch declaration")
	}
	activatePlannerWFPRegistration(t)
	for _, test := range []struct {
		name     string
		contents string
		evilFile bool
	}{
		{
			name:     "script loader append",
			contents: "list(APPEND CMAKE_PROJECT_TOP_LEVEL_INCLUDES evil.cmake)\nproject(fixture)\n",
			evilFile: true,
		},
		{
			name:     "compiler launcher append",
			contents: "list(APPEND CMAKE_CXX_COMPILER_LAUNCHER unknown.exe)\nadd_executable(fixture main.cpp)\n",
		},
		{
			name:     "pinned compiler append",
			contents: "list(APPEND CMAKE_CXX_COMPILER unknown.exe)\nproject(fixture)\n",
		},
		{
			name:     "pinned make program pop",
			contents: "list(POP_BACK CMAKE_MAKE_PROGRAM)\nproject(fixture)\n",
		},
		{
			name:     "pinned linker set",
			contents: "list(SET CMAKE_LINKER 0 unknown.exe)\nproject(fixture)\n",
		},
		{
			name:     "pinned archiver remove",
			contents: "list(REMOVE_ITEM CMAKE_AR unknown.exe)\nproject(fixture)\n",
		},
		{
			name:     "unknown set operation",
			contents: "list(SET CMAKE_PROJECT_TOP_LEVEL_INCLUDES 0 evil.cmake)\nproject(fixture)\n",
			evilFile: true,
		},
		{
			name:     "dynamic removal",
			contents: "set(CMAKE_PROJECT_TOP_LEVEL_INCLUDES safe.cmake)\nlist(REMOVE_ITEM CMAKE_PROJECT_TOP_LEVEL_INCLUDES \"${DYNAMIC}\")\nproject(fixture)\n",
		},
		{
			name:     "pop into controlled launcher",
			contents: "set(CMAKE_PROJECT_TOP_LEVEL_INCLUDES safe.cmake)\nlist(POP_BACK CMAKE_PROJECT_TOP_LEVEL_INCLUDES CMAKE_CXX_COMPILER_LAUNCHER)\nproject(fixture)\n",
		},
		{
			name:     "indirect controlled variable name",
			contents: "set(LAUNCH_VAR CMAKE_PROJECT_TOP_LEVEL_INCLUDES)\nlist(APPEND \"${LAUNCH_VAR}\" evil.cmake)\nproject(fixture)\n",
			evilFile: true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newPlannerFixture(t)
			if err := os.WriteFile(filepath.Join(fixture.sourceDir, "CMakeLists.txt"), []byte(test.contents), 0o600); err != nil {
				t.Fatal(err)
			}
			if test.evilFile {
				if err := os.WriteFile(filepath.Join(fixture.sourceDir, "evil.cmake"), []byte("execute_process(COMMAND unknown.exe)\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if strings.Contains(test.contents, "safe.cmake") {
				if err := os.WriteFile(filepath.Join(fixture.sourceDir, "safe.cmake"), []byte("# safe script\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := Plan(PlanInput{
				Installation: fixture.installation, WorkspaceRoot: fixture.root,
				Project: fixture.project, Profile: fixture.profile,
				Toolchain: fixture.toolchain, Jobs: 1, Configure: true,
			}); !errors.Is(err, task.ErrInvalidArgument) {
				t.Fatalf("Plan() error = %v, want controlled list mutation rejected", err)
			}
		})
	}
}

func TestPlannerRejectsProtectedVariableWritesFromCMakeTransformCommands(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows launch declaration")
	}
	activatePlannerWFPRegistration(t)
	for _, test := range []struct {
		name     string
		contents string
		evilFile bool
	}{
		{
			name:     "string append script loader",
			contents: "string(APPEND CMAKE_PROJECT_TOP_LEVEL_INCLUDES evil.cmake)\nproject(fixture)\n",
			evilFile: true,
		},
		{
			name:     "string concat compiler launcher",
			contents: "string(CONCAT CMAKE_CXX_COMPILER_LAUNCHER unknown.exe)\nadd_executable(fixture main.cpp)\n",
		},
		{
			name:     "file path conversion script loader",
			contents: "file(TO_CMAKE_PATH evil.cmake CMAKE_PROJECT_TOP_LEVEL_INCLUDES)\nproject(fixture)\n",
			evilFile: true,
		},
		{
			name:     "file read script loader",
			contents: "file(READ evil.cmake CMAKE_PROJECT_TOP_LEVEL_INCLUDES)\nproject(fixture)\n",
			evilFile: true,
		},
		{
			name:     "cmake path set compiler launcher",
			contents: "cmake_path(SET CMAKE_CXX_COMPILER_LAUNCHER unknown.exe)\nadd_executable(fixture main.cpp)\n",
		},
		{
			name:     "string concat pinned compiler",
			contents: "string(CONCAT CMAKE_C_COMPILER unknown.exe)\nproject(fixture)\n",
		},
		{
			name:     "file path conversion pinned make program",
			contents: "file(TO_CMAKE_PATH unknown.exe CMAKE_MAKE_PROGRAM)\nproject(fixture)\n",
		},
		{
			name:     "cmake path set pinned fortran compiler",
			contents: "cmake_path(SET CMAKE_Fortran_COMPILER unknown.exe)\nproject(fixture)\n",
		},
		{
			name:     "string prepend pinned ranlib",
			contents: "string(PREPEND CMAKE_RANLIB unknown.exe)\nproject(fixture)\n",
		},
		{
			name:     "indirect string output",
			contents: "set(OUTPUT_NAME CMAKE_PROJECT_TOP_LEVEL_INCLUDES)\nstring(APPEND \"${OUTPUT_NAME}\" evil.cmake)\nproject(fixture)\n",
			evilFile: true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newPlannerFixture(t)
			if err := os.WriteFile(filepath.Join(fixture.sourceDir, "CMakeLists.txt"), []byte(test.contents), 0o600); err != nil {
				t.Fatal(err)
			}
			if test.evilFile {
				if err := os.WriteFile(filepath.Join(fixture.sourceDir, "evil.cmake"), []byte("execute_process(COMMAND unknown.exe)\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := Plan(PlanInput{
				Installation: fixture.installation, WorkspaceRoot: fixture.root,
				Project: fixture.project, Profile: fixture.profile,
				Toolchain: fixture.toolchain, Jobs: 1, Configure: true,
			}); !errors.Is(err, task.ErrInvalidArgument) {
				t.Fatalf("Plan() error = %v, want protected variable writer rejected", err)
			}
		})
	}
}

func TestPlannerRejectsUnknownCMakeCommandWriters(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows launch declaration")
	}
	activatePlannerWFPRegistration(t)
	for _, test := range []struct {
		name     string
		contents string
	}{
		{
			name:     "find program compiler launcher",
			contents: "find_program(CMAKE_CXX_COMPILER_LAUNCHER NAMES unknown.exe)\nproject(fixture)\n",
		},
		{
			name:     "get filename component script loader",
			contents: "get_filename_component(CMAKE_PROJECT_TOP_LEVEL_INCLUDES evil.cmake ABSOLUTE)\nproject(fixture)\n",
		},
		{
			name:     "deferred cmake language call",
			contents: "cmake_language(DEFER CALL execute_process COMMAND unknown.exe)\nproject(fixture)\n",
		},
		{
			name:     "unclassified command",
			contents: "unclassified_command(CMAKE_CXX_COMPILER unknown.exe)\nproject(fixture)\n",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newPlannerFixture(t)
			if err := os.WriteFile(filepath.Join(fixture.sourceDir, "CMakeLists.txt"), []byte(test.contents), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := Plan(PlanInput{
				Installation: fixture.installation, WorkspaceRoot: fixture.root,
				Project: fixture.project, Profile: fixture.profile,
				Toolchain: fixture.toolchain, Jobs: 1, Configure: true,
			}); !errors.Is(err, task.ErrInvalidArgument) {
				t.Fatalf("Plan() error = %v, want unknown CMake writer rejected", err)
			}
		})
	}
}

func TestPlannerAcceptsExplicitSafeCMakeCommandAllowlist(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows launch declaration")
	}
	activatePlannerWFPRegistration(t)
	fixture := newPlannerFixture(t)
	contents := `cmake_minimum_required(VERSION 3.25)
project(fixture LANGUAGES CXX)
add_library(helper STATIC helper.cpp)
add_executable(fixture main.cpp)
target_compile_features(fixture PRIVATE cxx_std_20)
target_link_libraries(fixture PRIVATE helper)
enable_testing()
add_test(NAME fixture COMMAND fixture)
`
	if err := os.WriteFile(filepath.Join(fixture.sourceDir, "CMakeLists.txt"), []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Plan(PlanInput{
		Installation: fixture.installation, WorkspaceRoot: fixture.root,
		Project: fixture.project, Profile: fixture.profile,
		Toolchain: fixture.toolchain, Jobs: 1, Configure: true,
	}); err != nil {
		t.Fatalf("Plan() error = %v, want explicit safe CMake command allowlist accepted", err)
	}
}

func TestPlannerRejectsUnregisteredProjectLanguages(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows launch declaration")
	}
	activatePlannerWFPRegistration(t)
	for _, language := range []string{"Fortran", "CUDA", "UnknownLanguage"} {
		t.Run(language, func(t *testing.T) {
			fixture := newPlannerFixture(t)
			contents := "project(fixture LANGUAGES " + language + ")\n"
			if err := os.WriteFile(filepath.Join(fixture.sourceDir, "CMakeLists.txt"), []byte(contents), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := Plan(PlanInput{
				Installation: fixture.installation, WorkspaceRoot: fixture.root,
				Project: fixture.project, Profile: fixture.profile,
				Toolchain: fixture.toolchain, Jobs: 1, Configure: true,
			}); !errors.Is(err, task.ErrInvalidArgument) {
				t.Fatalf("Plan() error = %v, want unregistered project language rejected", err)
			}
		})
	}
}

func TestPlannerDerivesFreshDeclaredTestExecutableBeforeFileAPIExists(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows launch declaration")
	}
	activatePlannerWFPRegistration(t)
	fixture := newPlannerFixture(t)
	contents := "add_executable(coverage-tests main.cpp)\nadd_test(NAME coverage-tests COMMAND coverage-tests)\n"
	if err := os.WriteFile(filepath.Join(fixture.sourceDir, "CMakeLists.txt"), []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	plan, err := Plan(PlanInput{
		Installation: fixture.installation, WorkspaceRoot: fixture.root,
		Project: fixture.project, Profile: fixture.profile,
		Toolchain: fixture.toolchain, Jobs: 1, Configure: true,
	})
	if err != nil {
		t.Fatalf("Plan() error = %v, want fresh declared target accepted", err)
	}
	want := filepath.Join(fixture.profile.BinaryDir, "coverage-tests.exe")
	if !slices.Contains(plan.Steps[0].Process.LaunchPlan, want) {
		t.Fatalf("LaunchPlan = %#v, want derived target %q", plan.Steps[0].Process.LaunchPlan, want)
	}
}

func TestPlannerRejectsDynamicConfigureLaunchDeclarations(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows launch declaration")
	}
	activatePlannerWFPRegistration(t)
	for _, contents := range []string{
		"execute_process(COMMAND \"${UNDECLARED_TOOL}\" --escape)\n",
		"execute_process(\"COMMAND\" unknown.exe)\n",
		"set(CMAKE_CXX_COMPILER unknown.exe)\n",
		"set(CMAKE_CXX_COMPILER_LAUNCHER \"${UNDECLARED_TOOL}\")\n",
		"set_property(GLOBAL PROPERTY RULE_LAUNCH_CUSTOM \"${UNDECLARED_TOOL}\")\n",
		"set_property(GLOBAL \"PROPERTY\" RULE_LAUNCH_CUSTOM unknown.exe)\n",
		"add_executable(quoted-target main.cpp)\nset_target_properties(quoted-target \"PROPERTIES\" RULE_LAUNCH_CUSTOM unknown.exe)\n",
		"set_directory_properties(PROPERTIES RULE_LAUNCH_CUSTOM unknown.exe)\n",
		"add_custom_target(wrapper COMMAND \"${CMAKE_CTEST_COMMAND}\" --test-dir build)\n",
	} {
		fixture := newPlannerFixture(t)
		if err := os.WriteFile(filepath.Join(fixture.sourceDir, "CMakeLists.txt"), []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := Plan(PlanInput{
			Installation: fixture.installation, WorkspaceRoot: fixture.root,
			Project: fixture.project, Profile: fixture.profile,
			Toolchain: fixture.toolchain, Jobs: 1, Configure: true,
		}); !errors.Is(err, task.ErrInvalidArgument) {
			t.Fatalf("Plan() error = %v, want dynamic configure launch rejection for %q", err, contents)
		}
	}
	t.Run("Ninja wrapper", func(t *testing.T) {
		fixture := newPlannerFixture(t)
		ninja := filepath.Join(fixture.installation.Root, "bin", "ninja.exe")
		contents := fmt.Sprintf("add_custom_target(wrapper COMMAND %q -f generated.ninja)\n", filepath.ToSlash(ninja))
		if err := os.WriteFile(filepath.Join(fixture.sourceDir, "CMakeLists.txt"), []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := Plan(PlanInput{
			Installation: fixture.installation, WorkspaceRoot: fixture.root,
			Project: fixture.project, Profile: fixture.profile,
			Toolchain: fixture.toolchain, Jobs: 1, Configure: true,
		}); !errors.Is(err, task.ErrInvalidArgument) {
			t.Fatalf("Plan() error = %v, want Ninja wrapper rejection", err)
		}
	})
}

func TestPlannerRejectsDynamicCustomCommandsAndUnboundedCMakeGraphs(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows launch declaration")
	}
	activatePlannerWFPRegistration(t)
	t.Run("dynamic command executable", func(t *testing.T) {
		fixture := newPlannerFixture(t)
		contents := "add_custom_target(dynamic COMMAND \"${UNDECLARED_TOOL}\" --escape)\n"
		if err := os.WriteFile(filepath.Join(fixture.sourceDir, "CMakeLists.txt"), []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := Plan(PlanInput{
			Installation: fixture.installation, WorkspaceRoot: fixture.root,
			Project: fixture.project, Profile: fixture.profile,
			Toolchain: fixture.toolchain, Jobs: 1, Configure: true,
		}); !errors.Is(err, task.ErrInvalidArgument) {
			t.Fatalf("Plan() error = %v, want dynamic COMMAND rejection", err)
		}
	})
	t.Run("CMake executable wrapper", func(t *testing.T) {
		fixture := newPlannerFixture(t)
		contents := "add_custom_target(wrapper COMMAND \"${CMAKE_COMMAND}\" -E env -- unknown.exe)\n"
		if err := os.WriteFile(filepath.Join(fixture.sourceDir, "CMakeLists.txt"), []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := Plan(PlanInput{
			Installation: fixture.installation, WorkspaceRoot: fixture.root,
			Project: fixture.project, Profile: fixture.profile,
			Toolchain: fixture.toolchain, Jobs: 1, Configure: true,
		}); !errors.Is(err, task.ErrInvalidArgument) {
			t.Fatalf("Plan() error = %v, want spawning CMake wrapper rejection", err)
		}
	})
	t.Run("declared executable test target", func(t *testing.T) {
		fixture := newPlannerFixture(t)
		contents := "add_test(NAME known COMMAND \"$<TARGET_FILE:known-test>\")\n"
		if err := os.WriteFile(filepath.Join(fixture.sourceDir, "CMakeLists.txt"), []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
		target := cmake.Target{
			ID: strings.Repeat("e", 64), Name: "known-test", Type: "EXECUTABLE",
			Artifacts: []string{filepath.Join(fixture.profile.BinaryDir, "known-test.exe")},
		}
		if _, err := Plan(PlanInput{
			Installation: fixture.installation, WorkspaceRoot: fixture.root,
			Project: fixture.project, Profile: fixture.profile,
			Toolchain: fixture.toolchain, Targets: []cmake.Target{target}, Jobs: 1, Configure: true,
		}); err != nil {
			t.Fatalf("Plan() error = %v, want exact target artifact accepted", err)
		}
	})
	t.Run("file graph limit", func(t *testing.T) {
		fixture := newPlannerFixture(t)
		var root strings.Builder
		for index := 0; index < maxCMakeLaunchFiles; index++ {
			name := fmt.Sprintf("f%03d.cmake", index)
			root.WriteString("include(" + name + ")\n")
			if err := os.WriteFile(filepath.Join(fixture.sourceDir, name), []byte("# bounded include\n"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.WriteFile(filepath.Join(fixture.sourceDir, "CMakeLists.txt"), []byte(root.String()), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := Plan(PlanInput{
			Installation: fixture.installation, WorkspaceRoot: fixture.root,
			Project: fixture.project, Profile: fixture.profile,
			Toolchain: fixture.toolchain, Jobs: 1, Configure: true,
		}); !errors.Is(err, task.ErrInvalidArgument) {
			t.Fatalf("Plan() error = %v, want bounded CMake graph rejection", err)
		}
	})
}

func activatePlannerWFPRegistration(t *testing.T) {
	t.Helper()
	t.Setenv("UNIT_TEST_IDE_WFP_REGISTRATION_PIPE", `\\.\pipe\offlineboundary-register-planner-test`)
	t.Setenv("UNIT_TEST_IDE_WFP_REGISTRATION_NONCE", strings.Repeat("1", 64))
}

func TestCMakeLaunchParserBoundsCommandsAndArguments(t *testing.T) {
	tooManyCommands := []byte(strings.Repeat("message(x)\n", maxCMakeLaunchCommands+1))
	if _, err := parseCMakeInvocations(tooManyCommands); !errors.Is(err, errInvalidCMakeLaunchDeclaration) {
		t.Fatalf("parse command bound error = %v", err)
	}
	tooManyArguments := []byte("message(" + strings.Repeat("x ", maxCMakeLaunchArguments+1) + ")")
	if _, err := parseCMakeInvocations(tooManyArguments); !errors.Is(err, errInvalidCMakeLaunchDeclaration) {
		t.Fatalf("parse argument bound error = %v", err)
	}
}

func TestPlannerInjectsOnlyTypedCoveragePathsForPresetAndGeneratedProfiles(t *testing.T) {
	for _, origin := range []string{"generated", "preset"} {
		t.Run(origin, func(t *testing.T) {
			fixture := newPlannerFixture(t)
			baseBinaryDir := fixture.profile.BinaryDir
			fixture.profile.Origin = origin
			if origin == "preset" {
				fixture.profile.ConfigurePreset = "debug"
				writePlannerPreset(t, fixture.sourceDir, "debug")
			}
			coverageBinaryDir := filepath.Join(fixture.dataRoot, "coverage-build", origin)
			include := filepath.Join(fixture.dataRoot, "task", "coverage-instrumentation.cmake")
			if err := os.MkdirAll(filepath.Dir(include), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(include, []byte("# trusted instrumentation\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			options := &CoverageOptions{
				BinaryDir: coverageBinaryDir,
				TopLevelInclude: cmake.FingerprintFile{
					Path: include, Identity: strings.Repeat("4", 64), SHA256: strings.Repeat("5", 64),
				},
				InstrumentationFingerprint: strings.Repeat("4", 64),
			}
			plan, err := Plan(PlanInput{
				Installation: fixture.installation, WorkspaceRoot: fixture.root,
				Project: fixture.project, Profile: fixture.profile,
				Toolchain: fixture.toolchain, Jobs: 1, Configure: true,
				Coverage: options,
			})
			if err != nil {
				t.Fatal(err)
			}
			configure := plan.Steps[0].Process.Args
			wantBinaryPair := []string{"-B", coverageBinaryDir}
			if countArgumentPair(configure, wantBinaryPair) != 1 {
				t.Fatalf("coverage configure args = %#v, want exactly one %#v", configure, wantBinaryPair)
			}
			wantInclude := "-DCMAKE_PROJECT_TOP_LEVEL_INCLUDES:FILEPATH=" + filepath.ToSlash(include)
			if countArgument(configure, wantInclude) != 1 {
				t.Fatalf("coverage configure args = %#v, want exactly one %q", configure, wantInclude)
			}
			if countArgument(configure, baseBinaryDir) != 0 || strings.Contains(strings.Join(configure, "\n"), "extra-args") {
				t.Fatalf("coverage configure reused base binary dir or exposed generic args: %#v", configure)
			}
			build := plan.Steps[1]
			if build.Process.Args[1] != coverageBinaryDir || build.Process.Dir != coverageBinaryDir {
				t.Fatalf("coverage build step = %#v, want isolated binary dir %q", build, coverageBinaryDir)
			}
		})
	}
}

func countArgument(arguments []string, want string) int {
	count := 0
	for _, argument := range arguments {
		if argument == want {
			count++
		}
	}
	return count
}

func countArgumentPair(arguments, want []string) int {
	count := 0
	for index := 0; index+1 < len(arguments); index++ {
		if arguments[index] == want[0] && arguments[index+1] == want[1] {
			count++
		}
	}
	return count
}

func TestExecutionBoundaryAttachesAndRevalidatesFixedCoverageExecution(t *testing.T) {
	fixture := newPlannerFixture(t)
	boundaryValue, err := newExecutionBoundary(
		fixture.installation, fixture.root, fixture.dataRoot, nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	installRoot := filepath.Join(fixture.dataRoot, "coverage-install")
	python := filepath.Join(installRoot, "python.exe")
	runner := filepath.Join(installRoot, "runner.pyz")
	if err := os.MkdirAll(installRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	projectRoot := filepath.Join(fixture.dataRoot, "coverage", "project")
	objects := filepath.Join(fixture.dataRoot, "coverage", "objects")
	if err := os.MkdirAll(projectRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(objects, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{python, runner} {
		if err := os.WriteFile(path, []byte(path), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	gcov := filepath.Join(fixture.dataRoot, "coverage", "gcov.exe")
	if err := os.WriteFile(gcov, []byte(gcov), 0o700); err != nil {
		t.Fatal(err)
	}
	pin := &testCoveragePin{installation: coveragebundle.Installation{Root: fixture.dataRoot, Python: python, Runner: runner, PythonVersion: "3.14.6", GcovrVersion: "8.6", ManifestSHA256: strings.Repeat("a", 64)}}
	execution, err := coveragebundle.PrepareRunner(pin, filepath.Join(fixture.dataRoot, "coverage"), "task", coveragebundle.DescriptorInput{
		Root: projectRoot, ObjectDirectory: objects, GcovExecutable: gcov, OutputPath: filepath.Join(fixture.dataRoot, "coverage", "task", "coverage.json"),
	}, testCoverageCapabilities(t, filepath.Join(fixture.dataRoot, "coverage"), projectRoot, objects, gcov))
	if err != nil {
		t.Fatal(err)
	}
	if err := boundaryValue.AttachCoverageExecution(execution); err != nil {
		t.Fatalf("AttachCoverageExecution() = %v", err)
	}
	spec := execution.ProcessSpec()
	if err := boundaryValue.ValidateProcessTarget(
		spec.Executable, spec.Args, spec.Env, spec.EnvUnset, spec.Dir,
	); err != nil {
		t.Fatalf("ValidateProcessTarget() = %v", err)
	}
	if err := boundaryValue.ValidateProcessTarget(
		spec.Executable, []string{"-I", "-S", runner, "tampered.json"},
		nil, nil, spec.Dir,
	); err == nil {
		t.Fatal("ValidateProcessTarget accepted replaced descriptor")
	}
	if err := boundaryValue.Release(); err != nil {
		t.Fatal(err)
	}
	if err := boundaryValue.Release(); err != nil {
		t.Fatalf("second Release() = %v", err)
	}
	if err := boundaryValue.ValidateProcessTarget(
		spec.Executable, spec.Args,
		nil, nil, spec.Dir,
	); err == nil {
		t.Fatal("ValidateProcessTarget accepted released coverage execution")
	}
}

type testCoveragePin struct {
	installation coveragebundle.Installation
	closed       bool
}

func (pin *testCoveragePin) Installation() coveragebundle.Installation { return pin.installation }
func (pin *testCoveragePin) Verify() error {
	if pin.closed {
		return errors.New("closed")
	}
	return nil
}
func (pin *testCoveragePin) Close() error { pin.closed = true; return nil }

func testCoverageCapabilities(t *testing.T, coverageRoot, projectRoot, objects, gcov string) coveragebundle.DescriptorCapabilities {
	t.Helper()
	provenancePath := filepath.Dir(filepath.Dir(coverageRoot))
	if !pathWithinLocal(provenancePath, projectRoot) || !pathWithinLocal(provenancePath, objects) || !pathWithinLocal(provenancePath, gcov) {
		t.Fatalf("fixture paths escaped scratch anchor %q", provenancePath)
	}
	authority, err := testNewServiceAnchor(provenancePath)
	if err != nil {
		t.Fatal(err)
	}
	provenance, err := coveragebundle.NewVerifiedDirectoryFromAnchor(authority, ".")
	if err != nil {
		t.Fatal(err)
	}
	relative := func(path string) string { value, _ := filepath.Rel(provenancePath, path); return value }
	coverageCapability, err := coveragebundle.NewVerifiedDirectoryFrom(provenance, relative(coverageRoot))
	if err != nil {
		t.Fatal(err)
	}
	rootCapability, err := coveragebundle.NewVerifiedDirectoryFrom(provenance, relative(projectRoot))
	if err != nil {
		t.Fatal(err)
	}
	objectCapability, err := coveragebundle.NewVerifiedDirectoryFrom(provenance, relative(objects))
	if err != nil {
		t.Fatal(err)
	}
	gcovCapability, err := coveragebundle.NewVerifiedExecutableFrom(provenance, relative(gcov))
	if err != nil {
		t.Fatal(err)
	}
	return coveragebundle.DescriptorCapabilities{Anchor: authority, Provenance: provenance, CoverageRoot: coverageCapability, Root: rootCapability, ObjectDirectory: objectCapability, GcovExecutable: gcovCapability}
}

func pathWithinLocal(root, child string) bool {
	rel, err := filepath.Rel(filepath.Clean(root), filepath.Clean(child))
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

func TestPlannerSkipsConfigureAndKeepsTargetNamesAsIndependentArguments(t *testing.T) {
	fixture := newPlannerFixture(t)
	secondID := "e" + fixture.targetID[1:]
	plan, err := Plan(PlanInput{
		Installation:  fixture.installation,
		WorkspaceRoot: fixture.root,
		Project:       fixture.project,
		Profile:       fixture.profile,
		Toolchain:     fixture.toolchain,
		Targets: []cmake.Target{
			{ID: fixture.targetID, Name: "unit-tests"},
			{ID: secondID, Name: "helper library"},
		},
		TargetIDs: []string{fixture.targetID, secondID},
		Jobs:      3,
		Configure: false,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Steps) != 1 || plan.Steps[0].Kind != task.StepBuild {
		t.Fatalf("Plan() steps = %#v", plan.Steps)
	}
	wantTail := []string{"--target", "unit-tests", "helper library"}
	args := plan.Steps[0].Process.Args
	if !reflect.DeepEqual(args[len(args)-len(wantTail):], wantTail) {
		t.Fatalf("build target args = %#v, want tail %#v", args, wantTail)
	}
}

func TestPlannerNormalizesToolchainEnvironmentAndOmitsBuildTypeForMultiConfig(t *testing.T) {
	fixture := newPlannerFixture(t)
	fixture.profile.Generator = "Visual Studio 17 2022"
	fixture.toolchain.Family = toolchain.FamilyMSVC
	fixture.toolchain.Environment = []string{"Path=trusted", "INCLUDE=sdk"}
	plan, err := Plan(PlanInput{
		Installation:  fixture.installation,
		WorkspaceRoot: fixture.root,
		Project:       fixture.project,
		Profile:       fixture.profile,
		Toolchain:     fixture.toolchain,
		Jobs:          2,
		Configure:     true,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, argument := range plan.Steps[0].Process.Args {
		if strings.HasPrefix(argument, "-DCMAKE_BUILD_TYPE=") {
			t.Fatalf("multi-config configure args include %q", argument)
		}
	}
	if !reflect.DeepEqual(plan.Steps[0].Process.Env, []string{"INCLUDE=sdk", "PATH=trusted"}) {
		t.Fatalf("normalized environment = %#v", plan.Steps[0].Process.Env)
	}
}

func TestPlannerUsesMSVCEnvironmentWithNinjaFallback(t *testing.T) {
	fixture := newPlannerFixture(t)
	fixture.toolchain.Family = toolchain.FamilyMSVC
	fixture.toolchain.Environment = []string{"Path=trusted-msvc", "INCLUDE=sdk"}
	plan, err := Plan(PlanInput{
		Installation:  fixture.installation,
		WorkspaceRoot: fixture.root,
		Project:       fixture.project,
		Profile:       fixture.profile,
		Toolchain:     fixture.toolchain,
		Jobs:          2,
		Configure:     true,
	})
	if err != nil {
		t.Fatal(err)
	}
	arguments := strings.Join(plan.Steps[0].Process.Args, "\n")
	for _, expected := range []string{
		"-G\nNinja",
		"-DCMAKE_BUILD_TYPE=Debug",
		"-DCMAKE_C_COMPILER=" + filepath.ToSlash(fixture.toolchain.CCompiler),
		"-DCMAKE_CXX_COMPILER=" + filepath.ToSlash(fixture.toolchain.CXXCompiler),
	} {
		if !strings.Contains(arguments, expected) {
			t.Fatalf("MSVC Ninja configure args lack %q: %#v", expected, plan.Steps[0].Process.Args)
		}
	}
	if !reflect.DeepEqual(
		plan.Steps[0].Process.Env,
		[]string{"INCLUDE=sdk", "PATH=trusted-msvc"},
	) {
		t.Fatalf("MSVC Ninja environment = %#v", plan.Steps[0].Process.Env)
	}
}

func TestPlannerInjectsOnlyManifestBoundUnityRunnerForPresetAndGeneratedConfigure(t *testing.T) {
	for _, origin := range []string{"generated", "preset"} {
		t.Run(origin, func(t *testing.T) {
			fixture := newPlannerFixture(t)
			generatorPath := filepath.Join(t.TempDir(), "unity-runner-generator.exe")
			if err := os.WriteFile(generatorPath, []byte("generator"), 0o700); err != nil {
				t.Fatal(err)
			}
			generatorPath = canonicalPlannerFile(t, generatorPath)
			entry := cmake.ProductExecutableManifest{
				RelativePath: "bin/unity-runner-generator.exe",
				Version:      "1.0.0", SHA256: strings.Repeat("1", 64),
				Platform: "win32", Architecture: "x64",
			}
			fixture.installation.UnityRunnerGenerator = cmake.ProductExecutable{
				Path: generatorPath, RelativePath: "bin/unity-runner-generator.exe",
				Version: "1.0.0", SHA256: strings.Repeat("1", 64),
				Platform: "win32", Architecture: "x64",
				Identity: cmake.ProductExecutableIdentity(entry),
			}
			fixture.profile.Origin = origin
			if origin == "preset" {
				fixture.profile.ConfigurePreset = "debug"
				writePlannerPreset(t, fixture.sourceDir, "debug")
			}
			plan, err := Plan(PlanInput{
				Installation: fixture.installation, WorkspaceRoot: fixture.root,
				Project: fixture.project, Profile: fixture.profile,
				Toolchain: fixture.toolchain, Jobs: 1, Configure: true,
			})
			if err != nil {
				t.Fatal(err)
			}
			args := plan.Steps[0].Process.Args
			want := "-DUTIDE_UNITY_RUNNER_GENERATOR:FILEPATH=" + filepath.ToSlash(generatorPath)
			count := 0
			for _, argument := range args {
				if strings.HasPrefix(argument, "-DUTIDE_") {
					count++
					if argument != want {
						t.Fatalf("unexpected reserved cache argument %q", argument)
					}
				}
			}
			if count != 1 {
				t.Fatalf("reserved cache args = %#v, want exactly %q", args, want)
			}
		})
	}
}

func writePlannerPreset(t *testing.T, sourceDir, name string) {
	t.Helper()
	document := fmt.Sprintf(`{"version":6,"configurePresets":[{"name":%q,"generator":"Ninja","binaryDir":"${sourceDir}/build"}]}`, name)
	if err := os.WriteFile(filepath.Join(sourceDir, "CMakePresets.json"), []byte(document), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestPlannerRejectsPartialUnityRunnerInstallationIdentity(t *testing.T) {
	fixture := newPlannerFixture(t)
	fixture.installation.UnityRunnerGenerator = cmake.ProductExecutable{
		Path: filepath.Join(t.TempDir(), "generator.exe"),
	}
	if _, err := Plan(PlanInput{
		Installation: fixture.installation, WorkspaceRoot: fixture.root,
		Project: fixture.project, Profile: fixture.profile,
		Toolchain: fixture.toolchain, Jobs: 1, Configure: true,
	}); !errors.Is(err, task.ErrInvalidArgument) {
		t.Fatalf("Plan() error = %v, want task.ErrInvalidArgument", err)
	}
}

func TestExecutionBoundaryPinsManifestBoundUnityRunnerGenerator(t *testing.T) {
	fixture := newPlannerFixture(t)
	generatorBytes := []byte("manifest-bound generator")
	generatorPath := filepath.Join(t.TempDir(), "unity-runner-generator")
	platform := "linux"
	relativePath := "bin/unity-runner-generator"
	if runtime.GOOS == "windows" {
		platform = "win32"
		relativePath += ".exe"
		generatorPath += ".exe"
	}
	if err := os.WriteFile(generatorPath, generatorBytes, 0o700); err != nil {
		t.Fatal(err)
	}
	generatorPath = canonicalPlannerFile(t, generatorPath)
	sum := sha256.Sum256(generatorBytes)
	entry := cmake.ProductExecutableManifest{
		RelativePath: relativePath,
		Version:      "1.0.0",
		SHA256:       hex.EncodeToString(sum[:]),
		Platform:     platform,
		Architecture: "x64",
	}
	fixture.installation.UnityRunnerGenerator = cmake.ProductExecutable{
		Path:         generatorPath,
		RelativePath: entry.RelativePath,
		Version:      entry.Version,
		SHA256:       entry.SHA256,
		Platform:     entry.Platform,
		Architecture: entry.Architecture,
		Identity:     cmake.ProductExecutableIdentity(entry),
	}
	boundary, err := NewExecutionBoundary(
		fixture.installation,
		fixture.root,
		fixture.dataRoot,
	)
	if err != nil {
		t.Fatal(err)
	}
	managed := boundary.(task.ManagedExecutionBoundary)
	t.Cleanup(func() { _ = managed.Release() })
	if err := boundary.ValidateExecutable(fixture.installation.Executable); err != nil {
		t.Fatalf("CMake validation did not retain the generator pin: %v", err)
	}
	if err := boundary.ValidateExecutable(generatorPath); err != nil {
		t.Fatalf("generator validation failed: %v", err)
	}

	if err := managed.Release(); err != nil {
		t.Fatal(err)
	}
	fixture.installation.UnityRunnerGenerator.SHA256 = strings.Repeat("0", 64)
	entry.SHA256 = fixture.installation.UnityRunnerGenerator.SHA256
	fixture.installation.UnityRunnerGenerator.Identity = cmake.ProductExecutableIdentity(entry)
	if _, err := NewExecutionBoundary(
		fixture.installation,
		fixture.root,
		fixture.dataRoot,
	); !errors.Is(err, task.ErrInvalidArgument) {
		t.Fatalf("NewExecutionBoundary() error = %v, want task.ErrInvalidArgument", err)
	}
}

func TestExecutionBoundaryRejectsExecutableIdentityReplacement(t *testing.T) {
	fixture := newPlannerFixture(t)
	boundary, err := NewExecutionBoundary(fixture.installation, fixture.root, fixture.dataRoot)
	if err != nil {
		t.Fatal(err)
	}
	managed := boundary.(task.ManagedExecutionBoundary)
	t.Cleanup(func() { _ = managed.Release() })
	if err := boundary.ValidateExecutable(fixture.installation.Executable); err != nil {
		t.Fatal(err)
	}
	removeErr := os.Remove(fixture.installation.Executable)
	if removeErr != nil {
		if err := boundary.ValidateExecutable(fixture.installation.Executable); err != nil {
			t.Fatalf("blocked replacement changed the pinned executable: %v", err)
		}
		return
	}
	if err := os.WriteFile(fixture.installation.Executable, []byte("replacement"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := boundary.ValidateExecutable(fixture.installation.Executable); err == nil {
		t.Fatal("ValidateExecutable() accepted a replacement file at the trusted path")
	}
}

func TestExecutionBoundaryPinsPairedCTest(t *testing.T) {
	fixture := newPlannerFixture(t)
	boundary, err := NewExecutionBoundary(
		fixture.installation,
		fixture.root,
		fixture.dataRoot,
	)
	if err != nil {
		t.Fatal(err)
	}
	managed := boundary.(task.ManagedExecutionBoundary)
	defer managed.Release()
	if err := boundary.ValidateExecutable(fixture.installation.CTestExecutable); err != nil {
		t.Fatalf("paired CTest rejected: %v", err)
	}
	if err := boundary.ValidateExecutable(fixture.toolchain.CCompiler); err == nil {
		t.Fatal("unregistered executable accepted")
	}
}

func TestExecutionBoundaryReleaseClosesExecutablePin(t *testing.T) {
	fixture := newPlannerFixture(t)
	boundary, err := NewExecutionBoundary(fixture.installation, fixture.root, fixture.dataRoot)
	if err != nil {
		t.Fatal(err)
	}
	managed := boundary.(task.ManagedExecutionBoundary)
	if err := managed.Release(); err != nil {
		t.Fatal(err)
	}
	if err := managed.Release(); err != nil {
		t.Fatalf("second Release() error = %v", err)
	}
	if err := boundary.ValidateExecutable(fixture.installation.Executable); err == nil {
		t.Fatal("released boundary accepted executable validation")
	}
	if err := boundary.ValidateWorkingDirectory(fixture.sourceDir); err == nil {
		t.Fatal("released boundary accepted working directory validation")
	}
	if err := os.Remove(fixture.installation.Executable); err != nil {
		t.Fatalf("released executable remained pinned: %v", err)
	}
}

type plannerFixture struct {
	root         workspace.Root
	dataRoot     string
	sourceDir    string
	project      workspace.ProjectConfig
	profile      cmake.BuildProfile
	toolchain    toolchain.Instance
	installation cmake.Installation
	generation   string
	targetID     string
}

func plannerScratchDir(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(wd, "..", "..", "..", "..", ".task4-scratch")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	dir, err := os.MkdirTemp(root, "planner-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

func newPlannerFixture(t *testing.T) plannerFixture {
	t.Helper()
	base := plannerScratchDir(t)
	dataRoot := filepath.Join(base, "service-data")
	workspaceDir := filepath.Join(base, "workspace")
	sourceDir := filepath.Join(workspaceDir, "project")
	buildDir := filepath.Join(dataRoot, "build", "profile")
	for _, directory := range []string{sourceDir, buildDir} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(sourceDir, "CMakeLists.txt"), []byte("project(fixture)"), 0o600); err != nil {
		t.Fatal(err)
	}
	cmakePath := filepath.Join(dataRoot, "cmake.exe")
	ctestPath := filepath.Join(filepath.Dir(cmakePath), "ctest.exe")
	cCompiler := filepath.Join(dataRoot, "cc.exe")
	cxxCompiler := filepath.Join(dataRoot, "cxx.exe")
	for _, path := range []string{cmakePath, ctestPath, cCompiler, cxxCompiler} {
		if err := os.WriteFile(path, []byte(path), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	root, err := workspace.OpenRoot(workspaceDir)
	if err != nil {
		t.Fatal(err)
	}
	sourceDir, err = root.ResolveRelative("project")
	if err != nil {
		t.Fatal(err)
	}
	dataRootIdentity, err := workspace.OpenRoot(dataRoot)
	if err != nil {
		t.Fatal(err)
	}
	dataRoot = dataRootIdentity.NativePath
	buildDir = filepath.Join(dataRoot, "build", "profile")
	cmakePath = canonicalPlannerFile(t, cmakePath)
	ctestPath = canonicalPlannerFile(t, ctestPath)
	cCompiler = canonicalPlannerFile(t, cCompiler)
	cxxCompiler = canonicalPlannerFile(t, cxxCompiler)
	return plannerFixture{
		root: root, dataRoot: dataRoot, sourceDir: sourceDir,
		project: workspace.ProjectConfig{ID: "core", SourceDir: "project"},
		profile: cmake.BuildProfile{
			ID: strings.Repeat("a", 64), ProjectID: "core", Origin: "generated",
			ToolchainID: "gcc", Generator: "Ninja", Configuration: "Debug",
			BinaryDir: buildDir,
		},
		toolchain: toolchain.Instance{
			ID: "gcc", Family: toolchain.FamilyGCC,
			CCompiler: cCompiler, CXXCompiler: cxxCompiler,
			Environment: []string{"PATH=trusted"},
		},
		installation: cmake.Installation{
			Executable: cmakePath, CTestExecutable: ctestPath,
			Root:    filepath.Dir(cmakePath),
			Version: "4.3.0", Source: cmake.SourceBundle,
			Identity: strings.Repeat("c", 64),
		},
		generation: strings.Repeat("d", 64),
		targetID:   strings.Repeat("f", 64),
	}
}

func canonicalPlannerFile(t *testing.T, path string) string {
	t.Helper()
	parent, err := workspace.OpenRoot(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := parent.ResolveRelative(filepath.Base(path))
	if err != nil {
		t.Fatal(err)
	}
	return canonical
}
