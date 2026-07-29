package build

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"unit-test-ide.local/test-service/internal/cmake"
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

func newPlannerFixture(t *testing.T) plannerFixture {
	t.Helper()
	workspaceDir := filepath.Join(t.TempDir(), "workspace")
	sourceDir := filepath.Join(workspaceDir, "project")
	dataRoot := filepath.Join(t.TempDir(), "service-data")
	buildDir := filepath.Join(dataRoot, "build", "profile")
	for _, directory := range []string{sourceDir, buildDir} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(sourceDir, "CMakeLists.txt"), []byte("project(fixture)"), 0o600); err != nil {
		t.Fatal(err)
	}
	cmakePath := filepath.Join(t.TempDir(), "cmake.exe")
	cCompiler := filepath.Join(t.TempDir(), "cc.exe")
	cxxCompiler := filepath.Join(t.TempDir(), "cxx.exe")
	for _, path := range []string{cmakePath, cCompiler, cxxCompiler} {
		if err := os.WriteFile(path, []byte(path), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	root, err := workspace.OpenRoot(workspaceDir)
	if err != nil {
		t.Fatal(err)
	}
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
			Executable: cmakePath, Root: filepath.Dir(cmakePath),
			Version: "4.3.0", Source: cmake.SourceBundle,
			Identity: strings.Repeat("c", 64),
		},
		generation: strings.Repeat("d", 64),
		targetID:   strings.Repeat("f", 64),
	}
}
