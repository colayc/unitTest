package runtime

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"unit-test-ide.local/test-service/internal/build"
	"unit-test-ide.local/test-service/internal/cmake"
	"unit-test-ide.local/test-service/internal/toolchain"
	"unit-test-ide.local/test-service/internal/workspace"
)

type productionPreparedBuildFixture struct {
	generation string
	project    workspace.ProjectConfig
	profile    cmake.BuildProfile
	toolchain  toolchain.Instance
	targets    []cmake.Target
	releases   int
}

func (fixture *productionPreparedBuildFixture) WorkspaceGeneration() string {
	return fixture.generation
}
func (fixture *productionPreparedBuildFixture) Project() workspace.ProjectConfig {
	return fixture.project
}
func (fixture *productionPreparedBuildFixture) Profile() cmake.BuildProfile { return fixture.profile }
func (fixture *productionPreparedBuildFixture) Toolchain() toolchain.Instance {
	return fixture.toolchain
}
func (fixture *productionPreparedBuildFixture) Targets() []cmake.Target {
	return cmake.CloneTargets(fixture.targets)
}
func (fixture *productionPreparedBuildFixture) ReleaseIfUnadopted() { fixture.releases++ }

type productionBuildPreparerFixture struct {
	prepared productionPreparedBuild
	request  build.StartRequest
}

func (fixture *productionBuildPreparerFixture) PrepareProductionBuild(_ context.Context, request build.StartRequest) (productionPreparedBuild, error) {
	fixture.request = request
	return fixture.prepared, nil
}

func TestProductionBuildAuthorityResolvesExactCurrentBuild(t *testing.T) {
	_, target, _, _ := productionPipelineFixture(t)
	prepared := &productionPreparedBuildFixture{
		generation: target.workspaceGeneration,
		project: workspace.ProjectConfig{
			ID: "core", Tests: workspace.ProjectTestsConfig{Containers: []workspace.TestContainerMapping{{CTestName: target.ctestName, Framework: workspace.FrameworkCppUTest}}},
		},
		profile:   cmake.BuildProfile{ID: target.buildProfileID, ProjectID: target.projectID},
		toolchain: toolchain.Instance{ID: target.toolchainID},
		targets: []cmake.Target{
			{ID: target.cmakeTargetDigest, Name: target.renderTarget.ProductionTarget, ProjectID: target.projectID, ProfileID: target.buildProfileID},
			{ID: target.testTargetID, Name: target.renderTarget.TestTarget, ProjectID: target.projectID, ProfileID: target.buildProfileID},
		},
	}
	preparer := &productionBuildPreparerFixture{prepared: prepared}
	authority, err := newProductionBuildAuthority(preparer)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := authority.ResolveProductionBuild(context.Background(), target)
	if err != nil {
		t.Fatalf("ResolveProductionBuild() error = %v", err)
	}
	if snapshot.generation != target.workspaceGeneration || snapshot.project.ID != target.projectID ||
		snapshot.profile.ID != target.buildProfileID || snapshot.toolchain.ID != target.toolchainID || len(snapshot.targets) != 2 || prepared.releases != 1 {
		t.Fatalf("snapshot=%+v releases=%d", snapshot, prepared.releases)
	}
	wantTargets := []string{target.cmakeTargetDigest, target.testTargetID}
	if preparer.request.WorkspaceGeneration != target.workspaceGeneration || preparer.request.ProjectID != target.projectID ||
		preparer.request.BuildProfileID != target.buildProfileID || preparer.request.ToolchainID != target.toolchainID ||
		!reflect.DeepEqual(preparer.request.TargetIDs, wantTargets) || preparer.request.Jobs != target.concurrency ||
		preparer.request.Timeout != target.wallTime || len(preparer.request.IdempotencyKey) != 32 {
		t.Fatalf("request=%+v", preparer.request)
	}
}

func TestProductionBuildAuthorityRejectsChangedLineageAndReleases(t *testing.T) {
	_, target, _, _ := productionPipelineFixture(t)
	base := func() *productionPreparedBuildFixture {
		return &productionPreparedBuildFixture{
			generation: target.workspaceGeneration,
			project:    workspace.ProjectConfig{ID: target.projectID, Tests: workspace.ProjectTestsConfig{Containers: []workspace.TestContainerMapping{{CTestName: target.ctestName, Framework: workspace.FrameworkCppUTest}}}},
			profile:    cmake.BuildProfile{ID: target.buildProfileID, ProjectID: target.projectID}, toolchain: toolchain.Instance{ID: target.toolchainID},
			targets: []cmake.Target{
				{ID: target.cmakeTargetDigest, Name: target.renderTarget.ProductionTarget, ProjectID: target.projectID, ProfileID: target.buildProfileID},
				{ID: target.testTargetID, Name: target.renderTarget.TestTarget, ProjectID: target.projectID, ProfileID: target.buildProfileID},
			},
		}
	}
	for _, test := range []struct {
		name   string
		mutate func(*productionPreparedBuildFixture)
	}{
		{name: "workspace", mutate: func(value *productionPreparedBuildFixture) { value.generation = strings.Repeat("f", 64) }},
		{name: "toolchain", mutate: func(value *productionPreparedBuildFixture) { value.toolchain.ID = strings.Repeat("e", 64) }},
		{name: "test target", mutate: func(value *productionPreparedBuildFixture) { value.targets[1].Name = "other_tests" }},
		{name: "ctest mapping", mutate: func(value *productionPreparedBuildFixture) { value.project.Tests.Containers[0].CTestName = "other" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			prepared := base()
			test.mutate(prepared)
			authority, err := newProductionBuildAuthority(&productionBuildPreparerFixture{prepared: prepared})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := authority.ResolveProductionBuild(context.Background(), target); err == nil || prepared.releases != 1 {
				t.Fatalf("changed lineage accepted: %v releases=%d", err, prepared.releases)
			}
		})
	}
}
