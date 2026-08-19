package session

import (
	"strings"
	"testing"
	"time"

	"unit-test-ide.local/test-service/internal/coveragedomain"
	coveragev14 "unit-test-ide.local/test-service/internal/protocolmodel/v1_4/coverage"
	"unit-test-ide.local/test-service/internal/testdomain"
)

func TestCoverageProjectionValidatesAndMapsQueuedRun(t *testing.T) {
	run := coverageProjectionRun()
	projected, err := toProtocolCoverageRun(run)
	if err != nil {
		t.Fatal(err)
	}
	if projected.CoverageRunID != run.ID || projected.Status != coveragev14.CoverageRunQueuedV14 || projected.Outcome != nil || projected.ReportID != nil {
		t.Fatalf("queued projection = %#v", projected)
	}
	if projected.SelectionSnapshot.Mode != "items" || len(projected.SelectionSnapshot.ItemIDS) != 1 {
		t.Fatalf("selection projection = %#v", projected.SelectionSnapshot)
	}
	if projected.TimeoutMS != run.Request.Timeout.Milliseconds() || !projected.CreatedAt.Equal(run.CreatedAt) {
		t.Fatalf("request projection = %#v", projected)
	}
}

func TestCoverageProjectionMapsFinishedRunAndReportWithoutPaths(t *testing.T) {
	run := coverageProjectionRun()
	started := run.CreatedAt.Add(time.Second)
	finished := started.Add(time.Second)
	run.Status = coveragedomain.StatusFinished
	run.Outcome = coveragedomain.OutcomeAvailable
	run.StartedAt, run.FinishedAt = &started, &finished
	run.ReportID = strings.Repeat("d", 32)
	run.Summary = &coveragedomain.Summary{Lines: coveragedomain.Metric{Covered: 2, Total: 3}}
	run.Artifacts = coveragedomain.ArtifactRefs{CoverageJSONID: strings.Repeat("e", 32), JUnitXMLID: strings.Repeat("f", 32), CoverageHTMLID: strings.Repeat("a", 32)}
	projected, err := toProtocolCoverageRun(run)
	if err != nil {
		t.Fatal(err)
	}
	if projected.Outcome == nil || *projected.Outcome != coveragev14.CoverageAvailableV14 || projected.ReportID == nil || *projected.ReportID != run.ReportID {
		t.Fatalf("finished projection = %#v", projected)
	}
	report := coveragedomain.Report{
		ID: strings.Repeat("1", 32), RunID: run.ID, TestRunID: run.TestRunID,
		SchemaVersion: coveragedomain.SchemaVersion10, CreatedAt: finished,
		Completeness: coveragedomain.Completeness{Outcome: coveragedomain.OutcomeAvailable},
		Summary:      coveragedomain.Summary{Lines: coveragedomain.Metric{Covered: 2, Total: 3}},
		Toolchain:    run.Toolchain, ArtifactID: strings.Repeat("2", 32),
		Sources: []coveragedomain.SourceSnapshot{
			{URI: "src/a.cpp", SHA256: strings.Repeat("a", 64)},
			{URI: "src/z.cpp", SHA256: strings.Repeat("b", 64)},
		},
	}
	projectedReport, err := toProtocolCoverageReport(report)
	if err != nil {
		t.Fatal(err)
	}
	if projectedReport.ReportID != report.ID || projectedReport.ArtifactID != report.ArtifactID || projectedReport.ToolProvenance.Platform != coveragev14.CoverageLinuxV14 {
		t.Fatalf("report projection = %#v", projectedReport)
	}
	if len(projectedReport.Sources) != 2 || projectedReport.Sources[0].URI != "src/a.cpp" || projectedReport.Sources[0].SHA256 != strings.Repeat("a", 64) {
		t.Fatalf("source projection = %#v", projectedReport.Sources)
	}
}

func TestCoverageProjectionRejectsInvalidRowsAndPreservesCursor(t *testing.T) {
	invalid := coverageProjectionRun()
	invalid.TaskID = "not-a-task-id"
	if _, err := toProtocolCoverageRun(invalid); err == nil {
		t.Fatal("invalid coverage run was projected")
	}
	page, err := toProtocolCoverageRunPage(coveragedomain.RunPage{Items: []coveragedomain.Run{coverageProjectionRun()}, NextCursor: "next"})
	if err != nil {
		t.Fatal(err)
	}
	if page.NextCursor == nil || *page.NextCursor != "next" || len(page.Items) != 1 {
		t.Fatalf("page projection = %#v", page)
	}
}

func coverageProjectionRun() coveragedomain.Run {
	request := coveragedomain.Request{
		IdempotencyKey: strings.Repeat("1", 32), WorkspaceGeneration: strings.Repeat("2", 64),
		ProjectID: "core", CoverageProfileID: "coverage-debug", CatalogRevision: strings.Repeat("3", 64),
		Selection:   testdomain.Selection{Mode: testdomain.SelectionItems, ItemIDs: []testdomain.ID{testdomain.ID("utid-v1-" + strings.Repeat("4", 64))}},
		RepeatCount: 1, Timeout: time.Second,
	}
	id, err := coveragedomain.CoverageRunID(request)
	if err != nil {
		panic(err)
	}
	return coveragedomain.Run{
		ID: id, TaskID: strings.Repeat("5", 32), TestRunID: strings.Repeat("6", 32), Status: coveragedomain.StatusQueued,
		Request: request, SelectionSnapshot: testdomain.SelectionSnapshot{Mode: testdomain.SelectionItems, ItemIDs: []testdomain.ID{testdomain.ID("utid-v1-" + strings.Repeat("4", 64))}},
		Toolchain: coveragedomain.ToolchainSnapshot{
			Platform: coveragedomain.PlatformLinux, Architecture: coveragedomain.ArchitectureX64,
			Compiler:          coveragedomain.CompilerSnapshot{Family: coveragedomain.CompilerFamilyGCC, Version: "15.1.0"},
			Driver:            coveragedomain.DriverSnapshot{Name: coveragedomain.DriverGCov, Version: "15.1.0"},
			Collector:         coveragedomain.CollectorSnapshot{Name: coveragedomain.CollectorGCovr, Version: "8.6"},
			NormalizerVersion: "1.0.0", InstrumentationFingerprint: strings.Repeat("7", 64),
		},
		CreatedAt: time.Date(2026, 8, 19, 6, 7, 8, 0, time.UTC),
	}
}
