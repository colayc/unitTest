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
		"set(CMAKE_CXX_COMPILER unknown.exe)\n",
		"set(CMAKE_CXX_COMPILER_LAUNCHER \"${UNDECLARED_TOOL}\")\n",
		"set_property(GLOBAL PROPERTY RULE_LAUNCH_CUSTOM \"${UNDECLARED_TOOL}\")\n",
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
