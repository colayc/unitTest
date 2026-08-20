package coverageexec

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"unit-test-ide.local/test-service/internal/coveragedomain"
	coveragemodelv1 "unit-test-ide.local/test-service/internal/coveragemodel/v1"
	"unit-test-ide.local/test-service/internal/coveragenormalize"
	"unit-test-ide.local/test-service/internal/coveragereport"
	"unit-test-ide.local/test-service/internal/coveragerun"
	"unit-test-ide.local/test-service/internal/diagnostic"
	"unit-test-ide.local/test-service/internal/task"
	"unit-test-ide.local/test-service/internal/testdomain"
)

func TestOutcomeProjectionUsesExactClosedCoverageReason(t *testing.T) {
	tests := []struct {
		name        string
		outcome     task.Outcome
		failedPhase coveragerun.Phase
		wantOutcome coveragedomain.Outcome
		wantReason  coveragedomain.Reason
	}{
		{"cancel", task.OutcomeCancelled, coveragerun.PhaseTest, coveragedomain.OutcomeCancelled, coveragedomain.ReasonUserCancelled},
		{"task timeout", task.OutcomeTimedOut, coveragerun.PhaseTest, coveragedomain.OutcomeCancelled, coveragedomain.ReasonTaskTimedOut},
		{"restart", task.OutcomeInterrupted, coveragerun.PhaseBuild, coveragedomain.OutcomeUnavailable, coveragedomain.ReasonServiceRestarted},
		{"configure", task.OutcomeInfrastructureFailed, coveragerun.PhaseConfigure, coveragedomain.OutcomeUnavailable, coveragedomain.ReasonInstrumentationFailed},
		{"build", task.OutcomeCommandFailed, coveragerun.PhaseBuild, coveragedomain.OutcomeUnavailable, coveragedomain.ReasonBuildFailed},
		{"profile", task.OutcomeInfrastructureFailed, coveragerun.PhaseTest, coveragedomain.OutcomeUnavailable, coveragedomain.ReasonProfileCollectionFailed},
		{"merge", task.OutcomeInfrastructureFailed, coveragerun.PhaseMerge, coveragedomain.OutcomeUnavailable, coveragedomain.ReasonMergeFailed},
		{"normalize", task.OutcomeInfrastructureFailed, coveragerun.PhaseNormalize, coveragedomain.OutcomeUnavailable, coveragedomain.ReasonNormalizationFailed},
		{"report", task.OutcomeInfrastructureFailed, coveragerun.PhaseReport, coveragedomain.OutcomeUnavailable, coveragedomain.ReasonReportGenerationFailed},
		{"publish", task.OutcomeInfrastructureFailed, coveragerun.PhasePublish, coveragedomain.OutcomeUnavailable, coveragedomain.ReasonPersistenceFailed},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gotOutcome, gotReason, err := projectCoverageOutcome(tc.outcome, tc.failedPhase, coveragerun.State{})
			if err != nil || gotOutcome != tc.wantOutcome || gotReason != tc.wantReason {
				t.Fatalf("projectCoverageOutcome() = %q/%q, %v", gotOutcome, gotReason, err)
			}
		})
	}
}

func TestCompletionPublishesUnavailableGraphWithoutPublicReportArtifacts(t *testing.T) {
	execution, current, finishedAt := unavailableCompletionFixture(t)
	sink := &completionRecordingSink{}
	completion, err := execution.PrepareCompletion(
		context.Background(), current, finishedAt,
		task.OutcomeCommandFailed, sink,
		func() string { return strings.Repeat("f", 32) },
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(sink.blobs) != 0 || completion.Coverage == nil ||
		completion.Coverage.Report != nil ||
		completion.Coverage.Expected != coveragedomain.StatusQueued ||
		completion.Coverage.Run.Outcome != coveragedomain.OutcomeUnavailable ||
		completion.Coverage.Run.Reason != coveragedomain.ReasonBuildFailed {
		t.Fatalf("completion = %#v, blobs = %v", completion, sink.blobs)
	}
	if completion.TestRun == nil ||
		completion.TestRun.Status != testdomain.RunCompleted ||
		completion.TestRun.Outcome != testdomain.RunBlocked ||
		!completion.TestRun.Incomplete || completion.TestRun.Results != nil {
		t.Fatalf("finished TestRun = %#v", completion.TestRun)
	}
	if len(completion.Events) != 2 ||
		completion.Events[0].Type != task.EventTestRunFinished ||
		completion.Events[1].Type != task.EventCoverageRunFinished ||
		!strings.Contains(string(completion.Events[1].Payload), `"reason":"build_failed"`) {
		t.Fatalf("terminal events = %#v", completion.Events)
	}
}

func TestCompletionCommitsClosedReportSetBeforeReportBearingGraph(t *testing.T) {
	execution, current, finishedAt := unavailableCompletionFixture(t)
	itemID := execution.testRun.SelectionSnapshot.ItemIDs[0]
	result := testdomain.TestItemResult{
		ItemID: itemID, ContainerID: itemID, Iteration: 1,
		Outcome:        testdomain.ItemPassed,
		FailureDetails: []testdomain.FailureDetail{}, OutputRefs: []string{},
	}
	revision, err := testdomain.ResultRevision([]testdomain.TestItemResult{result})
	if err != nil {
		t.Fatal(err)
	}
	testFinishedAt := finishedAt.Add(-time.Millisecond)
	finishedTest, err := testdomain.NewTestRun(testdomain.TestRun{
		RunID: execution.testRun.RunID, TaskID: execution.testRun.TaskID,
		IdempotencyKey: execution.testRun.IdempotencyKey,
		ProjectID:      execution.testRun.ProjectID, ProfileID: execution.testRun.ProfileID,
		ToolchainID:       execution.testRun.ToolchainID,
		CatalogRevision:   execution.testRun.CatalogRevision,
		SelectionSnapshot: execution.testRun.SelectionSnapshot,
		Status:            testdomain.RunCompleted, Outcome: testdomain.RunPassed,
		FinishedAt: &testFinishedAt,
		Summary: testdomain.RunSummary{
			Total: 1, Completed: 1, Passed: 1, Iterations: 1,
		},
		ResultRevision: revision, CreatedAt: execution.testRun.CreatedAt,
		Results: []testdomain.TestItemResult{result},
	})
	if err != nil {
		t.Fatal(err)
	}
	document := coveragemodelv1.CoverageDocumentV1{
		SchemaVersion: coveragemodelv1.The10,
		Provenance: coveragemodelv1.CoverageProvenanceV1{
			Platform: coveragemodelv1.Windows, Architecture: coveragemodelv1.X64,
			Compiler: coveragemodelv1.CoverageCompilerV1{
				Family: coveragemodelv1.ClangCl, Version: "18.1.8",
			},
			Driver: coveragemodelv1.CoverageDriverV1{
				Name: coveragemodelv1.FluffyLlvmCov, Version: "18.1.8",
			},
			Collector: coveragemodelv1.CoverageCollectorV1{
				Name: coveragemodelv1.PurpleLlvmCov, Version: "18.1.8",
			},
			NormalizerVersion:          "1.0.0",
			InstrumentationFingerprint: strings.Repeat("8", 64),
		},
		Completeness: coveragemodelv1.CoverageCompletenessV1{
			Outcome: coveragemodelv1.Available, Reasons: []coveragemodelv1.Reason{},
		},
		Summary: coveragemodelv1.CoverageSummaryV1{
			Lines:     coveragemodelv1.CoverageMetricV1{},
			Branches:  coveragemodelv1.CoverageMetricV1{},
			Functions: coveragemodelv1.CoverageMetricV1{},
		},
		Files: []coveragemodelv1.CoverageFileV1{},
	}
	coverageJSON, err := coveragenormalize.EncodeCanonical(document)
	if err != nil {
		t.Fatal(err)
	}
	set, err := coveragereport.Render(coveragereport.Input{
		CoverageJSON: coverageJSON, Document: document,
		TestRun: finishedTest, Sources: []coveragenormalize.SourceBinding{},
	})
	if err != nil {
		t.Fatal(err)
	}
	execution.state = coveragerun.State{
		Terminal: true, Outcome: coveragedomain.OutcomeAvailable,
	}
	execution.failedPhase = ""
	execution.finishedTestRun = &finishedTest
	execution.reportSet = &set
	sink := &completionRecordingSink{}
	seed := byte('a')
	completion, err := execution.PrepareCompletion(
		context.Background(), current, finishedAt, task.OutcomeSucceeded, sink,
		func() string {
			value := strings.Repeat(string(seed), 32)
			seed++
			return value
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(sink.blobs, ","); got != "coverage-json,junit-xml,coverage-html" {
		t.Fatalf("blob commit order = %q", got)
	}
	if completion.Coverage == nil || completion.Coverage.Report == nil ||
		completion.Coverage.Run.Outcome != coveragedomain.OutcomeAvailable ||
		completion.Coverage.Run.ReportID != strings.Repeat("a", 32) ||
		completion.Coverage.Run.Artifacts.CoverageJSONID != strings.Repeat("b", 32) {
		t.Fatalf("report-bearing completion = %#v", completion.Coverage)
	}
	if len(completion.Events) != 3 ||
		completion.Events[1].Type != task.EventCoverageReportAvailable ||
		completion.Events[2].Type != task.EventCoverageRunFinished ||
		!strings.Contains(string(completion.Events[1].Payload), `"completeness":{"outcome":"available","reasons":[]}`) {
		t.Fatalf("report terminal events = %#v", completion.Events)
	}
}

func unavailableCompletionFixture(t *testing.T) (*execution, task.Task, time.Time) {
	t.Helper()
	created := time.Date(2026, 8, 20, 1, 0, 0, 0, time.UTC)
	started := created.Add(time.Second)
	finished := started.Add(time.Second)
	taskID := strings.Repeat("1", 32)
	testRunID := strings.Repeat("2", 32)
	idempotencyKey := strings.Repeat("3", 32)
	itemID := testdomain.ID("utid-v1-" + strings.Repeat("4", 64))
	selection := testdomain.SelectionSnapshot{
		Mode: testdomain.SelectionItems, ItemIDs: []testdomain.ID{itemID},
	}
	request, err := coveragedomain.NewRequest(coveragedomain.Request{
		IdempotencyKey:      idempotencyKey,
		WorkspaceGeneration: strings.Repeat("5", 64),
		ProjectID:           "core", CoverageProfileID: "coverage-default",
		CatalogRevision: strings.Repeat("6", 64),
		Selection: testdomain.Selection{
			Mode: testdomain.SelectionItems, ItemIDs: []testdomain.ID{itemID},
		},
		RepeatCount: 1, Timeout: time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	runID, err := coveragedomain.CoverageRunID(request)
	if err != nil {
		t.Fatal(err)
	}
	testRun, err := testdomain.NewTestRun(testdomain.TestRun{
		RunID: testRunID, TaskID: taskID, IdempotencyKey: idempotencyKey,
		ProjectID: "core", ProfileID: strings.Repeat("7", 64),
		ToolchainID: "clang-cl", CatalogRevision: request.CatalogRevision,
		SelectionSnapshot: selection, Status: testdomain.RunQueued,
		Summary:        testdomain.RunSummary{Iterations: 1},
		ResultRevision: testdomain.EmptyResultRevision(), Incomplete: true,
		CreatedAt: created,
	})
	if err != nil {
		t.Fatal(err)
	}
	coverageRun, err := coveragedomain.NewRun(coveragedomain.Run{
		ID: runID, TaskID: taskID, TestRunID: testRunID,
		Status: coveragedomain.StatusQueued, Request: request,
		SelectionSnapshot: selection,
		Toolchain: coveragedomain.ToolchainSnapshot{
			Platform:     coveragedomain.PlatformWindows,
			Architecture: coveragedomain.ArchitectureX64,
			Compiler: coveragedomain.CompilerSnapshot{
				Family: coveragedomain.CompilerFamilyClangCL, Version: "18.1.8",
			},
			Driver: coveragedomain.DriverSnapshot{
				Name: coveragedomain.DriverLLVMCov, Version: "18.1.8",
			},
			Collector: coveragedomain.CollectorSnapshot{
				Name: coveragedomain.CollectorLLVMCov, Version: "18.1.8",
			},
			NormalizerVersion:          "1.0.0",
			InstrumentationFingerprint: strings.Repeat("8", 64),
		},
		CreatedAt: created,
	})
	if err != nil {
		t.Fatal(err)
	}
	return &execution{
			taskID: taskID, run: coverageRun, testRun: testRun,
			state:       coveragerun.State{Phase: coveragerun.PhaseBuild},
			failedPhase: coveragerun.PhaseBuild,
		}, task.Task{
			ID: taskID, Kind: task.KindCoverageRun, Status: task.StatusFinished,
			StartedAt: &started, Outcome: task.OutcomeCommandFailed,
		}, finished
}

type completionRecordingSink struct{ blobs []string }

func (*completionRecordingSink) AppendOutput(context.Context, string, string, []byte) error {
	return nil
}

func (*completionRecordingSink) AppendDiagnostic(context.Context, diagnostic.Diagnostic) error {
	return nil
}

func (*completionRecordingSink) CommitJSON(context.Context, string, string, any) error {
	return nil
}

func (*completionRecordingSink) CommitJSONLines(context.Context, string, string, []json.RawMessage) error {
	return nil
}

func (sink *completionRecordingSink) CommitBlob(_ context.Context, _ string, kind string, _ []byte) error {
	sink.blobs = append(sink.blobs, kind)
	return nil
}

func (*completionRecordingSink) Finalize(context.Context, time.Time) ([]task.Artifact, error) {
	return nil, nil
}

func (*completionRecordingSink) Abort(context.Context) error { return nil }

func TestOutcomeProjectionPreservesAvailableAndPartialTerminalState(t *testing.T) {
	for _, want := range []coveragedomain.Outcome{
		coveragedomain.OutcomeAvailable,
		coveragedomain.OutcomePartial,
	} {
		got, reason, err := projectCoverageOutcome(
			task.OutcomeSucceeded,
			"",
			coveragerun.State{Terminal: true, Outcome: want},
		)
		if err != nil || got != want || reason != "" {
			t.Fatalf("projectCoverageOutcome() = %q/%q, %v; want %q", got, reason, err, want)
		}
	}
}
