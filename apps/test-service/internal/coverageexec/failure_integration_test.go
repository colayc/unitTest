package coverageexec

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"unit-test-ide.local/test-service/internal/artifactstore"
	"unit-test-ide.local/test-service/internal/coveragedomain"
	coveragemodelv1 "unit-test-ide.local/test-service/internal/coveragemodel/v1"
	"unit-test-ide.local/test-service/internal/coveragenormalize"
	"unit-test-ide.local/test-service/internal/coveragereport"
	"unit-test-ide.local/test-service/internal/coveragerun"
	"unit-test-ide.local/test-service/internal/task"
	"unit-test-ide.local/test-service/internal/testdomain"
)

func TestCoverageBlobStageFailureFallsBackToOneUnavailableRealAggregate(t *testing.T) {
	for _, failedKind := range []string{"coverage-json", "junit-xml", "coverage-html"} {
		t.Run(failedKind, func(t *testing.T) {
			var writer *failingCoverageBlobWriter
			fixture := newSQLiteCoverageFixtureWithArtifacts(
				t, unusedProcessFactory{},
				func(store *artifactstore.Store) task.ArtifactWriter {
					writer = &failingCoverageBlobWriter{delegate: store, failedKind: failedKind}
					return writer
				},
			)
			execution := availableCompletionExecution(t, fixture)
			driver := &terminalCompletionDriver{execution: execution}
			plan := task.ExecutionPlan{Version: 1, Steps: []task.ExecutionStep{publishActionStep()}}
			plan.Fingerprint = task.FingerprintPlan(plan)
			if _, err := fixture.manager.ResumeQueued(context.Background(), task.ResumeRequest{
				Task: fixture.persisted, Plan: plan,
				Boundary:          permissiveCoverageBoundary{},
				ResultInterpreter: driver, ActionExecutor: driver,
			}); err != nil {
				t.Fatal(err)
			}
			finished := fixture.awaitFinished(t)
			if finished.Outcome != task.OutcomeInfrastructureFailed {
				t.Fatalf("blob failure Task = %#v", finished)
			}
			run, err := fixture.store.GetCoverageRun(context.Background(), fixture.aggregate.Run.ID)
			if err != nil || run.Outcome != coveragedomain.OutcomeUnavailable ||
				run.Reason != coveragedomain.ReasonPersistenceFailed || run.ReportID != "" {
				t.Fatalf("blob failure CoverageRun = %#v, %v", run, err)
			}
			page, err := fixture.store.ListArtifacts(context.Background(), fixture.persisted.ID, "", 100)
			if err != nil || len(page.Items) != 0 {
				t.Fatalf("blob failure artifacts = %#v, %v", page.Items, err)
			}
			if writer.abortCalls != 1 || writer.openCalls != 2 ||
				fixture.publisher.count(task.EventCoverageReportAvailable) != 0 ||
				fixture.publisher.count(task.EventCoverageRunFinished) != 1 {
				t.Fatalf("blob fallback ownership: opens=%d aborts=%d events=%#v",
					writer.openCalls, writer.abortCalls, fixture.publisher.snapshot())
			}
		})
	}
}

type terminalCompletionDriver struct {
	execution *execution
}

func (driver *terminalCompletionDriver) Interpret(context.Context, task.Task, task.ExecutionStep, task.ProcessResult) (task.StepVerdict, error) {
	return task.StepVerdictSucceeded, nil
}

func (driver *terminalCompletionDriver) ExecuteServiceAction(context.Context, task.Task, task.ExecutionStep) (task.StepResult, error) {
	return task.StepResult{Verdict: task.StepVerdictSucceeded}, nil
}

func (driver *terminalCompletionDriver) PrepareCompletion(
	ctx context.Context, current task.Task, at time.Time, outcome task.Outcome,
	sink task.ArtifactSink, newID task.IDGenerator,
) (task.DomainCompletion, error) {
	completion, err := driver.execution.PrepareCompletion(ctx, current, at, outcome, sink, newID)
	return completion, err
}

func availableCompletionExecution(t *testing.T, fixture *sqliteCoverageFixture) *execution {
	t.Helper()
	persistedCoverage, err := fixture.store.GetCoverageRun(context.Background(), fixture.aggregate.Run.ID)
	if err != nil {
		t.Fatal(err)
	}
	finishedAt := time.Date(2026, 8, 20, 2, 0, 0, 0, time.UTC)
	finishedTest := fixture.aggregate.TestRun.Clone()
	finishedTest.Status = testdomain.RunCompleted
	finishedTest.Outcome = testdomain.RunPassed
	finishedTest.StartedAt = &finishedAt
	finishedTest.FinishedAt = &finishedAt
	finishedTest.Summary = testdomain.RunSummary{Iterations: 1}
	finishedTest.Results = []testdomain.TestItemResult{}
	finishedTest.Incomplete = false
	finishedTest.ResultRevision = testdomain.EmptyResultRevision()
	validated, err := testdomain.NewTestRun(finishedTest)
	if err != nil {
		t.Fatal(err)
	}
	document := coveragemodelv1.CoverageDocumentV1{
		SchemaVersion: coveragemodelv1.The10,
		Provenance: coveragemodelv1.CoverageProvenanceV1{
			Platform: coveragemodelv1.Windows, Architecture: coveragemodelv1.X64,
			Compiler:                   coveragemodelv1.CoverageCompilerV1{Family: coveragemodelv1.ClangCl, Version: "18.1.8"},
			Driver:                     coveragemodelv1.CoverageDriverV1{Name: coveragemodelv1.FluffyLlvmCov, Version: "18.1.8"},
			Collector:                  coveragemodelv1.CoverageCollectorV1{Name: coveragemodelv1.PurpleLlvmCov, Version: "18.1.8"},
			NormalizerVersion:          fixture.aggregate.Run.Toolchain.NormalizerVersion,
			InstrumentationFingerprint: fixture.aggregate.Run.Toolchain.InstrumentationFingerprint,
		},
		Completeness: coveragemodelv1.CoverageCompletenessV1{Outcome: coveragemodelv1.Available, Reasons: []coveragemodelv1.Reason{}},
		Summary: coveragemodelv1.CoverageSummaryV1{
			Lines: coveragemodelv1.CoverageMetricV1{}, Branches: coveragemodelv1.CoverageMetricV1{}, Functions: coveragemodelv1.CoverageMetricV1{},
		},
		Files: []coveragemodelv1.CoverageFileV1{},
	}
	coverageJSON, err := coveragenormalize.EncodeCanonical(document)
	if err != nil {
		t.Fatal(err)
	}
	set, err := coveragereport.Render(coveragereport.Input{
		CoverageJSON: coverageJSON, Document: document,
		TestRun: validated, Sources: []coveragenormalize.SourceBinding{},
	})
	if err != nil {
		t.Fatal(err)
	}
	return &execution{
		taskID: fixture.persisted.ID, run: persistedCoverage,
		testRun: fixture.aggregate.TestRun.Clone(), finishedTestRun: &validated,
		state:       coveragerun.State{Terminal: true, Outcome: coveragedomain.OutcomeAvailable},
		failedPhase: coveragerun.PhasePublish, reportSet: &set,
		bindings: []coveragenormalize.SourceBinding{},
	}
}

type failingCoverageBlobWriter struct {
	delegate   *artifactstore.Store
	failedKind string
	mu         sync.Mutex
	failed     bool
	openCalls  int
	abortCalls int
}

func (writer *failingCoverageBlobWriter) OpenTask(ctx context.Context, id string, kind task.Kind) (task.ArtifactSink, error) {
	writer.mu.Lock()
	writer.openCalls++
	writer.mu.Unlock()
	sink, err := writer.delegate.OpenTask(ctx, id, kind)
	if err != nil {
		return nil, err
	}
	return &failingCoverageBlobSink{CoverageArtifactSink: sink.(task.CoverageArtifactSink), owner: writer}, nil
}

type failingCoverageBlobSink struct {
	task.CoverageArtifactSink
	owner *failingCoverageBlobWriter
}

func (sink *failingCoverageBlobSink) CommitBlob(ctx context.Context, id, kind string, data []byte) error {
	sink.owner.mu.Lock()
	if !sink.owner.failed && kind == sink.owner.failedKind {
		sink.owner.failed = true
		sink.owner.mu.Unlock()
		return errors.New("injected coverage blob stage failure")
	}
	sink.owner.mu.Unlock()
	return sink.CoverageArtifactSink.CommitBlob(ctx, id, kind, data)
}

func (sink *failingCoverageBlobSink) Abort(ctx context.Context) error {
	sink.owner.mu.Lock()
	sink.owner.abortCalls++
	sink.owner.mu.Unlock()
	return sink.CoverageArtifactSink.Abort(ctx)
}
