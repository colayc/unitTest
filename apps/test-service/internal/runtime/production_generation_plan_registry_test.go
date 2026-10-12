package runtime

import (
	"context"
	"strings"
	"testing"

	"unit-test-ide.local/test-service/internal/cmake"
	"unit-test-ide.local/test-service/internal/testgenvalidate"
	"unit-test-ide.local/test-service/internal/toolchain"
	"unit-test-ide.local/test-service/internal/workspace"
)

type productionValidationStageCompilerFixture struct {
	registration productionValidationPlanRegistration
	stage        testgenvalidate.Stage
	roots        testgenvalidate.Roots
	plan         productionValidationProcessPlan
}

func productionBuildProjectFixture(target generationTarget) workspace.ProjectConfig {
	return workspace.ProjectConfig{ID: target.projectID, Tests: workspace.ProjectTestsConfig{Containers: []workspace.TestContainerMapping{{CTestName: target.ctestName, Framework: workspace.Framework(target.framework)}}}}
}

func productionBuildProfileFixture(target generationTarget) cmake.BuildProfile {
	return cmake.BuildProfile{ID: target.buildProfileID, ProjectID: target.projectID}
}

func productionBuildToolchainFixture(target generationTarget) toolchain.Instance {
	return toolchain.Instance{ID: target.toolchainID}
}

func productionBuildTargetsFixture(target generationTarget) []cmake.Target {
	return []cmake.Target{
		{ID: target.cmakeTargetDigest, Name: target.renderTarget.ProductionTarget, ProjectID: target.projectID, ProfileID: target.buildProfileID},
		{ID: target.testTargetID, Name: target.renderTarget.TestTarget, ProjectID: target.projectID, ProfileID: target.buildProfileID},
	}
}

func (fixture *productionValidationStageCompilerFixture) CompileValidationStage(_ context.Context, registration productionValidationPlanRegistration, stage testgenvalidate.Stage, roots testgenvalidate.Roots) (productionValidationProcessPlan, error) {
	fixture.registration, fixture.stage, fixture.roots = registration, stage, roots
	return fixture.plan, nil
}

func TestProductionValidationPlanRegistryBindsCandidateTaskAndRoots(t *testing.T) {
	_, target, _, _ := productionPipelineFixture(t)
	candidateID := strings.Repeat("a", 64)
	taskID := strings.Repeat("b", 32)
	compiler := &productionValidationStageCompilerFixture{plan: productionValidationProcessPlan{taskID: taskID}}
	registry, err := newProductionValidationPlanRegistry(compiler)
	if err != nil {
		t.Fatal(err)
	}
	registration := productionValidationPlanRegistration{candidateID: candidateID, processTaskID: taskID, target: target, build: productionBuildSnapshot{
		generation: target.workspaceGeneration, project: productionBuildProjectFixture(target), profile: productionBuildProfileFixture(target),
		toolchain: productionBuildToolchainFixture(target), targets: productionBuildTargetsFixture(target),
	}, baseline: []byte("baseline")}
	if err := registry.RegisterValidationPlan(registration); err != nil {
		t.Fatal(err)
	}
	roots := testgenvalidate.Roots{Source: t.TempDir(), Build: t.TempDir(), Artifacts: t.TempDir(), TaskID: strings.Repeat("c", 64), ProcessTaskID: taskID, CandidateID: candidateID}
	plan, err := registry.ResolveValidationProcessPlan(context.Background(), candidateID, taskID, testgenvalidate.StageCompile, roots)
	if err != nil || plan.taskID != taskID || compiler.registration.candidateID != candidateID || compiler.stage != testgenvalidate.StageCompile || compiler.roots != roots {
		t.Fatalf("ResolveValidationProcessPlan() plan=%+v error=%v compiler=%+v", plan, err, compiler)
	}
	registry.ReleaseValidationPlan(candidateID, taskID)
	if _, err := registry.ResolveValidationProcessPlan(context.Background(), candidateID, taskID, testgenvalidate.StageCompile, roots); err == nil {
		t.Fatal("released plan remained available")
	}
}

func TestProductionValidationPlanRegistryRejectsReplacementAndWrongOwner(t *testing.T) {
	_, target, _, _ := productionPipelineFixture(t)
	candidateID := strings.Repeat("a", 64)
	taskID := strings.Repeat("b", 32)
	registry, err := newProductionValidationPlanRegistry(&productionValidationStageCompilerFixture{})
	if err != nil {
		t.Fatal(err)
	}
	registration := productionValidationPlanRegistration{candidateID: candidateID, processTaskID: taskID, target: target, build: productionBuildSnapshot{
		generation: target.workspaceGeneration, project: productionBuildProjectFixture(target), profile: productionBuildProfileFixture(target),
		toolchain: productionBuildToolchainFixture(target), targets: productionBuildTargetsFixture(target),
	}, baseline: []byte("baseline")}
	if err := registry.RegisterValidationPlan(registration); err != nil {
		t.Fatal(err)
	}
	replacement := registration
	replacement.processTaskID = strings.Repeat("d", 32)
	if err := registry.RegisterValidationPlan(replacement); err == nil {
		t.Fatal("candidate owner replacement accepted")
	}
	roots := testgenvalidate.Roots{Source: t.TempDir(), Build: t.TempDir(), Artifacts: t.TempDir(), TaskID: strings.Repeat("c", 64), ProcessTaskID: replacement.processTaskID, CandidateID: candidateID}
	if _, err := registry.ResolveValidationProcessPlan(context.Background(), candidateID, replacement.processTaskID, testgenvalidate.StageCompile, roots); err == nil {
		t.Fatal("wrong owner resolved plan")
	}
}
