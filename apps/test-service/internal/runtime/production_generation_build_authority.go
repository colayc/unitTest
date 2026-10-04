package runtime

import (
	"context"

	"unit-test-ide.local/test-service/internal/build"
	"unit-test-ide.local/test-service/internal/cmake"
	"unit-test-ide.local/test-service/internal/task"
	"unit-test-ide.local/test-service/internal/toolchain"
	"unit-test-ide.local/test-service/internal/workspace"
)

type productionPreparedBuild interface {
	WorkspaceGeneration() string
	Project() workspace.ProjectConfig
	Profile() cmake.BuildProfile
	Toolchain() toolchain.Instance
	Targets() []cmake.Target
	ReleaseIfUnadopted()
}

type productionBuildPreparer interface {
	PrepareProductionBuild(context.Context, build.StartRequest) (productionPreparedBuild, error)
}

type coordinatorBuildPlanSource interface {
	PreparePlan(context.Context, build.StartRequest) (*build.PreparedPlan, error)
}

type coordinatorProductionBuildPreparer struct {
	delegate coordinatorBuildPlanSource
}

func newCoordinatorProductionBuildPreparer(value runtimeCoordinator) productionBuildPreparer {
	delegate, ok := value.(coordinatorBuildPlanSource)
	if !ok || delegate == nil {
		return nil
	}
	return coordinatorProductionBuildPreparer{delegate: delegate}
}

func (preparer coordinatorProductionBuildPreparer) PrepareProductionBuild(ctx context.Context, request build.StartRequest) (productionPreparedBuild, error) {
	return preparer.delegate.PreparePlan(ctx, request)
}

type productionBuildSnapshot struct {
	generation string
	project    workspace.ProjectConfig
	profile    cmake.BuildProfile
	toolchain  toolchain.Instance
	targets    []cmake.Target
}

type productionBuildAuthority struct {
	preparer productionBuildPreparer
}

func newProductionBuildAuthority(preparer productionBuildPreparer) (*productionBuildAuthority, error) {
	if preparer == nil {
		return nil, task.ErrStorageUnavailable
	}
	return &productionBuildAuthority{preparer: preparer}, nil
}

func (authority *productionBuildAuthority) ResolveProductionBuild(ctx context.Context, target generationTarget) (productionBuildSnapshot, error) {
	if authority == nil || authority.preparer == nil || ctx == nil || ctx.Err() != nil || !target.valid() || target.cmakeTargetDigest == target.testTargetID {
		return productionBuildSnapshot{}, errProductionGenerationUnavailable
	}
	digest, err := productionCanonicalDigest("production-validation-build-v1", struct {
		Generation, Project, Profile, Toolchain, ProductionTarget, TestTarget string
	}{target.workspaceGeneration, target.projectID, target.buildProfileID, target.toolchainID, target.cmakeTargetDigest, target.testTargetID})
	if err != nil {
		return productionBuildSnapshot{}, errProductionGenerationUnavailable
	}
	prepared, err := authority.preparer.PrepareProductionBuild(ctx, build.StartRequest{
		IdempotencyKey: digest[:32], WorkspaceGeneration: target.workspaceGeneration,
		ProjectID: target.projectID, BuildProfileID: target.buildProfileID, ToolchainID: target.toolchainID,
		TargetIDs: []string{target.cmakeTargetDigest, target.testTargetID}, Jobs: target.concurrency, Timeout: target.wallTime,
	})
	if err != nil || prepared == nil {
		return productionBuildSnapshot{}, errProductionGenerationUnavailable
	}
	defer prepared.ReleaseIfUnadopted()
	snapshot := productionBuildSnapshot{
		generation: prepared.WorkspaceGeneration(), project: prepared.Project(), profile: prepared.Profile(),
		toolchain: prepared.Toolchain(), targets: prepared.Targets(),
	}
	if !validProductionBuildSnapshot(snapshot, target) {
		return productionBuildSnapshot{}, errProductionGenerationUnavailable
	}
	snapshot.project.Tests.Containers = append([]workspace.TestContainerMapping(nil), snapshot.project.Tests.Containers...)
	snapshot.toolchain.Environment = append([]string(nil), snapshot.toolchain.Environment...)
	snapshot.toolchain.Generators = append([]string(nil), snapshot.toolchain.Generators...)
	snapshot.targets = cmake.CloneTargets(snapshot.targets)
	return snapshot, nil
}

func validProductionBuildSnapshot(snapshot productionBuildSnapshot, target generationTarget) bool {
	if snapshot.generation != target.workspaceGeneration || snapshot.project.ID != target.projectID ||
		snapshot.profile.ID != target.buildProfileID || snapshot.profile.ProjectID != target.projectID ||
		snapshot.toolchain.ID != target.toolchainID || len(snapshot.targets) == 0 || len(snapshot.targets) > 1024 {
		return false
	}
	mappings := 0
	for _, mapping := range snapshot.project.Tests.Containers {
		if mapping.CTestName == target.ctestName && string(mapping.Framework) == target.framework {
			mappings++
		}
	}
	if mappings != 1 {
		return false
	}
	productionMatches, testMatches := 0, 0
	for _, candidate := range snapshot.targets {
		if candidate.ID == target.cmakeTargetDigest && candidate.Name == target.renderTarget.ProductionTarget &&
			candidate.ProjectID == target.projectID && candidate.ProfileID == target.buildProfileID {
			productionMatches++
		}
		if candidate.ID == target.testTargetID && candidate.Name == target.renderTarget.TestTarget &&
			candidate.ProjectID == target.projectID && candidate.ProfileID == target.buildProfileID {
			testMatches++
		}
	}
	return productionMatches == 1 && testMatches == 1
}

var _ productionBuildPreparer = coordinatorProductionBuildPreparer{}
