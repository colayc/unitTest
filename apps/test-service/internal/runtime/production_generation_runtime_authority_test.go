package runtime

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"unit-test-ide.local/test-service/internal/build"
	"unit-test-ide.local/test-service/internal/cmake"
	"unit-test-ide.local/test-service/internal/coveragedetail"
	"unit-test-ide.local/test-service/internal/coveragedomain"
	"unit-test-ide.local/test-service/internal/discovery"
	"unit-test-ide.local/test-service/internal/workspace"
)

type productionRuntimeContextFixture struct {
	snapshots []discovery.Snapshot
	targets   []cmake.Target
	run       coveragedomain.Run
	report    coveragedomain.Report
	index     coveragedetail.Index
	requests  []build.TargetsRequest
}

func (fixture *productionRuntimeContextFixture) InspectWorkspace(context.Context) (discovery.Snapshot, error) {
	if len(fixture.snapshots) == 1 {
		return fixture.snapshots[0], nil
	}
	result := fixture.snapshots[0]
	fixture.snapshots = fixture.snapshots[1:]
	return result, nil
}
func (fixture *productionRuntimeContextFixture) ListTargets(_ context.Context, request build.TargetsRequest) ([]cmake.Target, error) {
	fixture.requests = append(fixture.requests, request)
	return cmake.CloneTargets(fixture.targets), nil
}
func (fixture *productionRuntimeContextFixture) GetCoverageRun(context.Context, string) (coveragedomain.Run, error) {
	return fixture.run, nil
}
func (fixture *productionRuntimeContextFixture) GetCoverageReport(context.Context, string) (coveragedomain.Report, error) {
	return fixture.report, nil
}
func (fixture *productionRuntimeContextFixture) ReadCurrentCoverageIndex(context.Context, coveragedetail.CurrentIndexQuery) (coveragedetail.Index, error) {
	return fixture.index, nil
}

func TestRuntimeProductionResolverAuthorityReconstructsCurrentBuildContext(t *testing.T) {
	fixture := productionRuntimeAuthorityFixture()
	authority := runtimeProductionResolverAuthority{reader: fixture}

	resolved, err := authority.ResolveProductionContext(context.Background(), "core", fixture.run.Request.WorkspaceGeneration, fixture.report.ID)
	if err != nil {
		t.Fatalf("ResolveProductionContext() error = %v", err)
	}
	if resolved.project.ID != "core" || resolved.profile.ID != fixture.snapshots[0].Profiles[0].ID ||
		!reflect.DeepEqual(resolved.targets, fixture.targets) || resolved.index.ReportID != fixture.report.ID ||
		!validProductionDigest(resolved.baselineReportDigest) || len(fixture.requests) != 1 ||
		fixture.requests[0].BuildProfileID != fixture.snapshots[0].Profiles[0].ID {
		t.Fatalf("resolved context = %+v; requests = %+v", resolved, fixture.requests)
	}
}

func TestRuntimeProductionResolverAuthorityRejectsWorkspaceChangeAndWrongReportLineage(t *testing.T) {
	fixture := productionRuntimeAuthorityFixture()
	changed := fixture.snapshots[0]
	changed.Generation = strings.Repeat("f", 64)
	fixture.snapshots = []discovery.Snapshot{fixture.snapshots[0], changed}
	if _, err := (runtimeProductionResolverAuthority{reader: fixture}).ResolveProductionContext(context.Background(), "core", fixture.run.Request.WorkspaceGeneration, fixture.report.ID); err == nil {
		t.Fatal("workspace change was accepted")
	}

	fixture = productionRuntimeAuthorityFixture()
	fixture.run.ReportID = strings.Repeat("0", 32)
	if _, err := (runtimeProductionResolverAuthority{reader: fixture}).ResolveProductionContext(context.Background(), "core", fixture.run.Request.WorkspaceGeneration, fixture.report.ID); err == nil {
		t.Fatal("wrong report lineage was accepted")
	}
}

func productionRuntimeAuthorityFixture() *productionRuntimeContextFixture {
	generation := strings.Repeat("a", 64)
	profileID := strings.Repeat("b", 64)
	reportID := strings.Repeat("c", 32)
	runID := strings.Repeat("d", 32)
	snapshot := discovery.Snapshot{
		Generation:       generation,
		Projects:         []workspace.ProjectConfig{{ID: "core", SourceDir: "."}},
		Profiles:         []cmake.BuildProfile{{ID: profileID, ProjectID: "core", BinaryDir: "C:/build"}},
		CoverageProfiles: []workspace.CoverageProfile{{ID: "coverage", BaseBuildProfileID: profileID}},
	}
	return &productionRuntimeContextFixture{
		snapshots: []discovery.Snapshot{snapshot},
		targets:   []cmake.Target{{ID: strings.Repeat("e", 64), Name: "core", ProfileID: profileID, ProjectID: "core"}},
		run: coveragedomain.Run{
			ID: runID, ReportID: reportID, Outcome: coveragedomain.OutcomeAvailable,
			Request: coveragedomain.Request{WorkspaceGeneration: generation, ProjectID: "core", CoverageProfileID: "coverage"},
		},
		report: coveragedomain.Report{ID: reportID, RunID: runID, SchemaVersion: coveragedomain.SchemaVersion10},
		index:  coveragedetail.Index{WorkspaceGeneration: generation, ProjectID: "core", ReportID: reportID, RunID: runID},
	}
}
