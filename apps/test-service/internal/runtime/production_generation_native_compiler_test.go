package runtime

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"unit-test-ide.local/test-service/internal/cmake"
	"unit-test-ide.local/test-service/internal/processcontrol"
	"unit-test-ide.local/test-service/internal/task"
	"unit-test-ide.local/test-service/internal/testgenvalidate"
)

type productionGenerationLeaseFixture struct{}

func (productionGenerationLeaseFixture) PutGenerationProcessLease(context.Context, task.ProcessLease) error {
	return nil
}
func (productionGenerationLeaseFixture) ReleaseGenerationProcessLease(context.Context, task.ProcessLease) error {
	return nil
}

func TestProductionValidationNativeCompilerBuildsFixedOrderedStages(t *testing.T) {
	root := t.TempDir()
	bin := filepath.Join(root, "bin")
	if err := os.Mkdir(bin, 0700); err != nil {
		t.Fatal(err)
	}
	cmakePath := writeProductionCompilerTool(t, bin, "cmake.exe", "cmake")
	ctestPath := writeProductionCompilerTool(t, bin, "ctest.exe", "ctest")
	testBinary := writeProductionCompilerTool(t, root, "unit_tests.exe", "tests")
	mergeTool := writeProductionCompilerTool(t, bin, "llvm-profdata.exe", "merge")
	exportTool := writeProductionCompilerTool(t, bin, "llvm-cov.exe", "export")
	roots := testgenvalidate.Roots{
		Source: filepath.Join(root, "source"), Build: filepath.Join(root, "build"), Artifacts: filepath.Join(root, "artifacts"),
		TaskID: strings.Repeat("1", 64), ProcessTaskID: strings.Repeat("2", 32), CandidateID: strings.Repeat("3", 64),
	}
	for _, path := range []string{roots.Source, roots.Build, roots.Artifacts} {
		if err := os.Mkdir(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	_, target, _, _ := productionPipelineFixture(t)
	registration := productionValidationPlanRegistration{
		candidateID: roots.CandidateID, processTaskID: roots.ProcessTaskID, target: target,
		build: productionBuildSnapshot{generation: target.workspaceGeneration, project: productionBuildProjectFixture(target), profile: productionBuildProfileFixture(target), toolchain: productionBuildToolchainFixture(target), targets: productionBuildTargetsFixture(target)},
	}
	buildFactory := func(context.Context, productionValidationPlanRegistration, testgenvalidate.Roots) (productionValidationBuildPreparation, error) {
		return productionValidationBuildPreparation{
			configure:  processcontrol.Spec{Executable: cmakePath, Dir: roots.Source},
			compile:    processcontrol.Spec{Executable: cmakePath, Dir: roots.Build},
			profile:    cmake.BuildProfile{ID: target.buildProfileID, ProjectID: target.projectID, BinaryDir: roots.Build, Configuration: "Debug"},
			testBinary: testBinary, environment: []string{"PATH=" + bin},
			tools: map[string]string{cmakePath: productionBytesDigest([]byte("cmake"))},
		}, nil
	}
	coverageFactory := func(context.Context, productionValidationPlanRegistration, productionValidationBuildPreparation, testgenvalidate.Roots) (productionValidationCoveragePreparation, error) {
		return productionValidationCoveragePreparation{
			specs: []processcontrol.Spec{{Executable: mergeTool, Dir: roots.Artifacts}, {Executable: exportTool, Dir: roots.Artifacts}},
			tools: map[string]string{mergeTool: productionBytesDigest([]byte("merge")), exportTool: productionBytesDigest([]byte("export"))},
			unset: []string{"LLVM_PROFILE_FILE"},
			interpret: func(testgenvalidate.Stage, testgenvalidate.Roots, []byte) (testgenvalidate.StageEvidence, error) {
				return testgenvalidate.StageEvidence{CoverageJSON: []byte("coverage")}, nil
			},
		}, nil
	}
	compiler, err := newProductionValidationNativeCompiler(productionValidationNativeCompilerConfig{
		installation:      cmake.Installation{Executable: cmakePath, CTestExecutable: ctestPath, Version: "4.3.0", Identity: strings.Repeat("4", 64)},
		serviceInstanceID: strings.Repeat("5", 32), leases: productionGenerationLeaseFixture{},
		prepareBuild: buildFactory, prepareCoverage: coverageFactory,
	})
	if err != nil {
		t.Fatal(err)
	}
	stages := []testgenvalidate.Stage{testgenvalidate.StageConfigure, testgenvalidate.StageCompile, testgenvalidate.StageDiscover, testgenvalidate.StageCandidate, testgenvalidate.StageSuite, testgenvalidate.StageCoverage}
	for _, stage := range stages {
		plan, err := compiler.CompileValidationStage(context.Background(), registration, stage, roots)
		if err != nil {
			t.Fatalf("CompileValidationStage(%s) error = %v", stage, err)
		}
		if plan.taskID != roots.ProcessTaskID || plan.serviceInstanceID != strings.Repeat("5", 32) || plan.recordLease == nil || plan.releaseLease == nil {
			t.Fatalf("stage %s ownership plan=%+v", stage, plan)
		}
		switch stage {
		case testgenvalidate.StageConfigure, testgenvalidate.StageCompile:
			if plan.specs[stage].Executable != cmakePath || !reflect.DeepEqual(plan.allowedEnvironment, []string{"PATH=" + bin}) {
				t.Fatalf("stage %s plan=%+v", stage, plan)
			}
		case testgenvalidate.StageDiscover:
			if plan.specs[stage].Executable != ctestPath || plan.interpret == nil {
				t.Fatalf("discovery plan=%+v", plan)
			}
		case testgenvalidate.StageCandidate, testgenvalidate.StageSuite:
			if plan.specs[stage].Executable != ctestPath || !reflect.DeepEqual(plan.specs[stage].LaunchPlan, []string{testBinary}) {
				t.Fatalf("test plan=%+v", plan)
			}
		case testgenvalidate.StageCoverage:
			if len(plan.sequences[stage]) != 2 || plan.interpret == nil || !reflect.DeepEqual(plan.allowedEnvUnset, []string{"LLVM_PROFILE_FILE"}) {
				t.Fatalf("coverage plan=%+v", plan)
			}
		}
	}
	compiler.ReleaseValidationStage(roots.CandidateID, roots.ProcessTaskID)
	if _, err := compiler.CompileValidationStage(context.Background(), registration, testgenvalidate.StageCompile, roots); err == nil {
		t.Fatal("released compiler state accepted non-configure stage")
	}
}

func writeProductionCompilerTool(t *testing.T, root, name, content string) string {
	t.Helper()
	path := filepath.Join(root, name)
	if err := os.WriteFile(path, []byte(content), 0700); err != nil {
		t.Fatal(err)
	}
	return path
}
