package taskstore

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"unit-test-ide.local/test-service/internal/coveragedomain"
	"unit-test-ide.local/test-service/internal/task"
)

func TestCoverageReportRoundTripsStrictMetadata(t *testing.T) {
	ctx := context.Background()
	for index, outcome := range []coveragedomain.Outcome{
		coveragedomain.OutcomeAvailable,
		coveragedomain.OutcomePartial,
	} {
		t.Run(string(outcome), func(t *testing.T) {
			store := openTestStore(t)
			mutation := coverageCompletionFixture(t, store, 300+index, outcome, "")
			if _, _, err := store.Apply(ctx, mutation); err != nil {
				t.Fatal(err)
			}
			got, err := store.GetCoverageReport(ctx, mutation.FinishCoverage.Report.ID)
			if err != nil || !reflect.DeepEqual(got, *mutation.FinishCoverage.Report) {
				t.Fatalf("GetCoverageReport() = %#v, %v; want %#v", got, err, *mutation.FinishCoverage.Report)
			}
			if got.CreatedAt.Location() != time.UTC {
				t.Fatalf("CreatedAt location = %v, want UTC", got.CreatedAt.Location())
			}
			persistedRun, err := store.GetCoverageRun(ctx, mutation.FinishCoverage.Run.ID)
			if err != nil || persistedRun.ReportID != got.ID ||
				persistedRun.Artifacts.CoverageJSONID != got.ArtifactID {
				t.Fatalf("CoverageRun report linkage = %#v, %v", persistedRun, err)
			}
		})
	}
}

func TestCoverageReportMissingAndCorruptRowsFailClosed(t *testing.T) {
	ctx := context.Background()
	var nilStore *Store
	if _, err := nilStore.GetCoverageReport(ctx, coverageHex(1)); !errors.Is(err, task.ErrInvalidArgument) {
		t.Fatalf("nil GetCoverageReport error = %v", err)
	}
	store := openTestStore(t)
	if _, err := store.GetCoverageReport(nil, coverageHex(1)); !errors.Is(err, task.ErrInvalidArgument) {
		t.Fatalf("nil context error = %v", err)
	}
	if _, err := store.GetCoverageReport(ctx, "bad"); !errors.Is(err, task.ErrInvalidArgument) {
		t.Fatalf("invalid ID error = %v", err)
	}
	if _, err := store.GetCoverageReport(ctx, coverageHex(1)); !errors.Is(err, task.ErrNotFound) {
		t.Fatalf("missing report error = %v", err)
	}

	cases := []struct {
		name    string
		outcome coveragedomain.Outcome
		update  string
	}{
		{name: "created timestamp", outcome: coveragedomain.OutcomeAvailable, update: `created_at='2026-08-04T09:00:02Z'`},
		{name: "completeness unknown", outcome: coveragedomain.OutcomeAvailable, update: `completeness_json=json_set(completeness_json, '$.unknown', true)`},
		{name: "completeness unsorted", outcome: coveragedomain.OutcomePartial, update: `completeness_json='{"outcome":"partial","reasons":["test_timed_out","test_crashed"]}'`},
		{name: "summary unknown", outcome: coveragedomain.OutcomeAvailable, update: `summary_json=json_set(summary_json, '$.unknown', true)`},
		{name: "toolchain unknown", outcome: coveragedomain.OutcomeAvailable, update: `toolchain_json=json_set(toolchain_json, '$.unknown', true)`},
	}
	for index, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := openTestStore(t)
			mutation := coverageCompletionFixture(t, store, 320+index, tc.outcome, "")
			if _, _, err := store.Apply(ctx, mutation); err != nil {
				t.Fatal(err)
			}
			if _, err := store.db.Exec(`UPDATE coverage_reports SET `+tc.update+` WHERE report_id=?`, mutation.FinishCoverage.Report.ID); err != nil {
				t.Fatal(err)
			}
			if _, err := store.GetCoverageReport(ctx, mutation.FinishCoverage.Report.ID); !errors.Is(err, task.ErrStorageUnavailable) {
				t.Fatalf("GetCoverageReport corrupt row error = %v", err)
			}
		})
	}
}

func TestCoverageArtifactContractRejectsInvalidPublicationSets(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name   string
		mutate func(*task.Mutation)
	}{
		{name: "artifact from another Task", mutate: func(m *task.Mutation) { m.Artifacts[0].TaskID = coverageHex(9999) }},
		{name: "missing artifact ID", mutate: func(m *task.Mutation) { m.Artifacts[0].ID = "" }},
		{name: "duplicate artifact ID", mutate: func(m *task.Mutation) { m.Artifacts[1].ID = m.Artifacts[0].ID }},
		{name: "duplicate artifact path", mutate: func(m *task.Mutation) { m.Artifacts[1].RelativePath = m.Artifacts[0].RelativePath }},
		{name: "missing coverage JSON", mutate: func(m *task.Mutation) { m.Artifacts = m.Artifacts[1:] }},
		{name: "missing JUnit XML", mutate: func(m *task.Mutation) { m.Artifacts = append(m.Artifacts[:1], m.Artifacts[2:]...) }},
		{name: "missing coverage HTML", mutate: func(m *task.Mutation) { m.Artifacts = m.Artifacts[:2] }},
		{name: "wrong coverage JSON kind", mutate: func(m *task.Mutation) { m.Artifacts[0].Kind = "diagnostics" }},
		{name: "wrong coverage JSON MIME", mutate: func(m *task.Mutation) { m.Artifacts[0].MIMEType = "application/xml" }},
		{name: "wrong JUnit kind", mutate: func(m *task.Mutation) { m.Artifacts[1].Kind = "diagnostics" }},
		{name: "wrong JUnit MIME", mutate: func(m *task.Mutation) { m.Artifacts[1].MIMEType = "application/json" }},
		{name: "wrong HTML kind", mutate: func(m *task.Mutation) { m.Artifacts[2].Kind = "diagnostics" }},
		{name: "wrong HTML MIME", mutate: func(m *task.Mutation) { m.Artifacts[2].MIMEType = "application/json" }},
		{name: "extra public artifact", mutate: func(m *task.Mutation) {
			extra := m.Artifacts[0]
			extra.ID, extra.RelativePath = coverageHex(9998), "coverage/extra.json"
			m.Artifacts = append(m.Artifacts, extra)
		}},
		{name: "unapproved extra artifact", mutate: func(m *task.Mutation) {
			extra := m.Artifacts[0]
			extra.ID, extra.Kind, extra.RelativePath = coverageHex(9996), "test-results", "coverage/results.ndjson"
			m.Artifacts = append(m.Artifacts, extra)
		}},
	}
	for index, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := openTestStore(t)
			mutation := coverageCompletionFixture(t, store, 350+index, coveragedomain.OutcomeAvailable, "")
			before, _ := store.Watermark(ctx)
			tc.mutate(&mutation)
			if _, _, err := store.Apply(ctx, mutation); !errors.Is(err, task.ErrInvalidArgument) {
				t.Fatalf("Apply() error = %v", err)
			}
			assertCoverageTerminalRolledBack(t, store, mutation.Task.ID, mutation.FinishCoverage.Run.ID, mutation.FinishRun.RunID, before)
		})
	}
}

func TestCoverageArtifactContractAllowsDiagnosticOutput(t *testing.T) {
	ctx := context.Background()
	for index, outcome := range []coveragedomain.Outcome{
		coveragedomain.OutcomeAvailable,
		coveragedomain.OutcomeUnavailable,
	} {
		t.Run(string(outcome), func(t *testing.T) {
			store := openTestStore(t)
			reason := coveragedomain.Reason("")
			if outcome == coveragedomain.OutcomeUnavailable {
				reason = coveragedomain.ReasonBuildFailed
			}
			mutation := coverageCompletionFixture(t, store, 380+index, outcome, reason)
			diagnostic := task.Artifact{
				ID: coverageHex(9900 + index), TaskID: mutation.Task.ID, Kind: "diagnostics",
				RelativePath: "coverage/diagnostics.json", MIMEType: "application/json",
				Size: 2, SHA256: strings.Repeat("d", 64), CreatedAt: *mutation.Task.FinishedAt,
			}
			mutation.Artifacts = append(mutation.Artifacts, diagnostic)
			if _, _, err := store.Apply(ctx, mutation); err != nil {
				t.Fatal(err)
			}
			if got, err := store.GetArtifact(ctx, diagnostic.ID); err != nil || !reflect.DeepEqual(got, diagnostic) {
				t.Fatalf("diagnostic artifact = %#v, %v", got, err)
			}
		})
	}
}

func coverageReportFixture(t *testing.T, run coveragedomain.Run) coveragedomain.Report {
	t.Helper()
	reasons := []coveragedomain.CompletenessReason{}
	if run.Outcome == coveragedomain.OutcomePartial {
		reasons = []coveragedomain.CompletenessReason{
			coveragedomain.CompletenessReasonTestTimedOut,
			coveragedomain.CompletenessReasonTestCrashed,
		}
	}
	report, err := coveragedomain.NewReport(coveragedomain.Report{
		ID: run.ReportID, RunID: run.ID, TestRunID: run.TestRunID,
		SchemaVersion: coveragedomain.SchemaVersion10, CreatedAt: run.FinishedAt.Add(-time.Nanosecond),
		Completeness: coveragedomain.Completeness{Outcome: run.Outcome, Reasons: reasons},
		Summary:      *run.Summary, Toolchain: run.Toolchain, ArtifactID: run.Artifacts.CoverageJSONID,
	})
	if err != nil {
		t.Fatal(err)
	}
	return report
}

func coverageArtifactsFixture(run coveragedomain.Run) []task.Artifact {
	createdAt := *run.FinishedAt
	return []task.Artifact{
		{ID: run.Artifacts.CoverageJSONID, TaskID: run.TaskID, Kind: "coverage-json", RelativePath: "coverage/coverage.json", MIMEType: "application/json", Size: 10, SHA256: strings.Repeat("a", 64), CreatedAt: createdAt},
		{ID: run.Artifacts.JUnitXMLID, TaskID: run.TaskID, Kind: "junit-xml", RelativePath: "coverage/junit.xml", MIMEType: "application/xml", Size: 11, SHA256: strings.Repeat("b", 64), CreatedAt: createdAt},
		{ID: run.Artifacts.CoverageHTMLID, TaskID: run.TaskID, Kind: "coverage-html", RelativePath: "coverage/index.html", MIMEType: "text/html", Size: 12, SHA256: strings.Repeat("c", 64), CreatedAt: createdAt},
	}
}
