package runtime

import (
	"context"
	"reflect"

	"unit-test-ide.local/test-service/internal/build"
	"unit-test-ide.local/test-service/internal/cmake"
	"unit-test-ide.local/test-service/internal/coveragedetail"
	"unit-test-ide.local/test-service/internal/coveragedomain"
	"unit-test-ide.local/test-service/internal/discovery"
	"unit-test-ide.local/test-service/internal/task"
)

type productionRuntimeContextReader interface {
	InspectWorkspace(context.Context) (discovery.Snapshot, error)
	ListTargets(context.Context, build.TargetsRequest) ([]cmake.Target, error)
	GetCoverageRun(context.Context, string) (coveragedomain.Run, error)
	GetCoverageReport(context.Context, string) (coveragedomain.Report, error)
	ReadCurrentCoverageIndex(context.Context, coveragedetail.CurrentIndexQuery) (coveragedetail.Index, error)
}

type runtimeProductionResolverAuthority struct {
	reader productionRuntimeContextReader
}

func (authority runtimeProductionResolverAuthority) ResolveProductionContext(ctx context.Context, projectID, generation, reportID string) (productionResolverContext, error) {
	if authority.reader == nil || ctx == nil || projectID == "" || !validProductionDigest(generation) || !validProductionObjectID(reportID) {
		return productionResolverContext{}, task.ErrInvalidArgument
	}
	snapshot, err := authority.reader.InspectWorkspace(ctx)
	if err != nil {
		return productionResolverContext{}, err
	}
	if snapshot.Generation != generation {
		return productionResolverContext{}, coveragedetail.ErrStale
	}
	projectIndex := -1
	for index := range snapshot.Projects {
		if snapshot.Projects[index].ID == projectID {
			if projectIndex != -1 {
				return productionResolverContext{}, errProductionGenerationUnavailable
			}
			projectIndex = index
		}
	}
	if projectIndex == -1 {
		return productionResolverContext{}, task.ErrNotFound
	}
	report, err := authority.reader.GetCoverageReport(ctx, reportID)
	if err != nil {
		return productionResolverContext{}, err
	}
	run, err := authority.reader.GetCoverageRun(ctx, report.RunID)
	if err != nil {
		return productionResolverContext{}, err
	}
	if report.ID != reportID || run.ID != report.RunID || run.ReportID != reportID ||
		run.Request.ProjectID != projectID || run.Request.WorkspaceGeneration != generation ||
		run.Outcome != coveragedomain.OutcomeAvailable && run.Outcome != coveragedomain.OutcomePartial {
		return productionResolverContext{}, coveragedetail.ErrStale
	}
	baseProfileID := ""
	for _, coverageProfile := range snapshot.CoverageProfiles {
		if coverageProfile.ID != run.Request.CoverageProfileID {
			continue
		}
		if baseProfileID != "" {
			return productionResolverContext{}, errProductionGenerationUnavailable
		}
		baseProfileID = coverageProfile.BaseBuildProfileID
	}
	if baseProfileID == "" {
		return productionResolverContext{}, errProductionGenerationUnavailable
	}
	profileIndex := -1
	for index := range snapshot.Profiles {
		if snapshot.Profiles[index].ID == baseProfileID && snapshot.Profiles[index].ProjectID == projectID {
			if profileIndex != -1 {
				return productionResolverContext{}, errProductionGenerationUnavailable
			}
			profileIndex = index
		}
	}
	if profileIndex == -1 {
		return productionResolverContext{}, errProductionGenerationUnavailable
	}
	targets, err := authority.reader.ListTargets(ctx, build.TargetsRequest{
		WorkspaceGeneration: generation, ProjectID: projectID, BuildProfileID: baseProfileID,
	})
	if err != nil {
		return productionResolverContext{}, err
	}
	index, err := authority.reader.ReadCurrentCoverageIndex(ctx, coveragedetail.CurrentIndexQuery{
		ProjectID: projectID, ReportID: reportID, WorkspaceGeneration: generation,
	})
	if err != nil {
		return productionResolverContext{}, err
	}
	baselineDigest, err := productionCanonicalDigest("coverage-report-v1", report)
	if err != nil || !validProductionDigest(baselineDigest) {
		return productionResolverContext{}, errProductionGenerationUnavailable
	}
	current, err := authority.reader.InspectWorkspace(ctx)
	if err != nil {
		return productionResolverContext{}, err
	}
	if current.Generation != generation || !reflect.DeepEqual(current.Projects, snapshot.Projects) ||
		!reflect.DeepEqual(current.Profiles, snapshot.Profiles) || !reflect.DeepEqual(current.CoverageProfiles, snapshot.CoverageProfiles) {
		return productionResolverContext{}, coveragedetail.ErrStale
	}
	return productionResolverContext{
		snapshot: snapshot, project: snapshot.Projects[projectIndex], profile: snapshot.Profiles[profileIndex],
		targets: cmake.CloneTargets(targets), index: index, baselineReportDigest: baselineDigest,
	}, nil
}

var _ productionResolverAuthority = runtimeProductionResolverAuthority{}
var _ productionRuntimeContextReader = (*Runtime)(nil)
