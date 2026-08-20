package coverageexec

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"time"

	"unit-test-ide.local/test-service/internal/coveragedomain"
	"unit-test-ide.local/test-service/internal/coveragenormalize"
	"unit-test-ide.local/test-service/internal/coveragereport"
	"unit-test-ide.local/test-service/internal/coveragerun"
	"unit-test-ide.local/test-service/internal/task"
	"unit-test-ide.local/test-service/internal/testdomain"
	"unit-test-ide.local/test-service/internal/testrun"
)

func projectCoverageOutcome(
	outcome task.Outcome,
	failedPhase coveragerun.Phase,
	state coveragerun.State,
) (coveragedomain.Outcome, coveragedomain.Reason, error) {
	switch outcome {
	case task.OutcomeSucceeded:
		if !state.Terminal ||
			(state.Outcome != coveragedomain.OutcomeAvailable &&
				state.Outcome != coveragedomain.OutcomePartial) ||
			state.Reason != "" {
			return "", "", task.ErrInvalidArgument
		}
		return state.Outcome, "", nil
	case task.OutcomeCancelled:
		return coveragedomain.OutcomeCancelled,
			coveragedomain.ReasonUserCancelled, nil
	case task.OutcomeTimedOut:
		return coveragedomain.OutcomeCancelled,
			coveragedomain.ReasonTaskTimedOut, nil
	case task.OutcomeInterrupted:
		return coveragedomain.OutcomeUnavailable,
			coveragedomain.ReasonServiceRestarted, nil
	case task.OutcomeCommandFailed, task.OutcomeInfrastructureFailed:
		reason, ok := failureReason(failedPhase)
		if !ok {
			return "", "", task.ErrInvalidArgument
		}
		return coveragedomain.OutcomeUnavailable, reason, nil
	default:
		return "", "", task.ErrInvalidArgument
	}
}

func failureReason(phase coveragerun.Phase) (coveragedomain.Reason, bool) {
	switch phase {
	case coveragerun.PhaseConfigure:
		return coveragedomain.ReasonInstrumentationFailed, true
	case coveragerun.PhaseBuild:
		return coveragedomain.ReasonBuildFailed, true
	case coveragerun.PhaseTest:
		return coveragedomain.ReasonProfileCollectionFailed, true
	case coveragerun.PhaseMerge:
		return coveragedomain.ReasonMergeFailed, true
	case coveragerun.PhaseNormalize:
		return coveragedomain.ReasonNormalizationFailed, true
	case coveragerun.PhaseReport:
		return coveragedomain.ReasonReportGenerationFailed, true
	case coveragerun.PhasePublish:
		return coveragedomain.ReasonPersistenceFailed, true
	default:
		return "", false
	}
}

func (execution *execution) PrepareCompletion(
	ctx context.Context,
	current task.Task,
	finishedAt time.Time,
	outcome task.Outcome,
	sink task.ArtifactSink,
	newID task.IDGenerator,
) (task.DomainCompletion, error) {
	if execution == nil || ctx == nil || current.ID != execution.taskID ||
		current.Kind != task.KindCoverageRun || current.Status != task.StatusFinished ||
		finishedAt.IsZero() || sink == nil || newID == nil {
		return task.DomainCompletion{}, task.ErrInvalidArgument
	}
	execution.completionMu.Lock()
	defer execution.completionMu.Unlock()
	replay := execution.completionReplayRequest(current, finishedAt, outcome)
	if execution.completion != nil {
		if !reflect.DeepEqual(execution.completion.request, replay) {
			return task.DomainCompletion{}, task.ErrConflict
		}
		return cloneDomainCompletion(execution.completion.value), nil
	}
	completion, err := execution.prepareCompletion(
		ctx, current, finishedAt, outcome, sink, newID,
	)
	if err != nil {
		return task.DomainCompletion{}, err
	}
	execution.completion = &completionReplay{
		request: replay,
		value:   cloneDomainCompletion(completion),
	}
	return cloneDomainCompletion(completion), nil
}

func (execution *execution) DiscardPreparedCompletion() {
	if execution == nil {
		return
	}
	execution.completionMu.Lock()
	execution.completion = nil
	execution.mu.Lock()
	execution.failedPhase = coveragerun.PhasePublish
	execution.mu.Unlock()
	execution.completionMu.Unlock()
}

func (execution *execution) prepareCompletion(
	ctx context.Context,
	current task.Task,
	finishedAt time.Time,
	outcome task.Outcome,
	sink task.ArtifactSink,
	newID task.IDGenerator,
) (task.DomainCompletion, error) {
	coverageSink, ok := sink.(task.CoverageArtifactSink)
	if !ok || nilPort(coverageSink) {
		return task.DomainCompletion{}, task.ErrInvalidArgument
	}
	if err := ctx.Err(); err != nil {
		return task.DomainCompletion{}, err
	}

	execution.mu.Lock()
	state := execution.state
	failedPhase := execution.failedPhase
	reportSet := execution.reportSet
	bindings := append([]coveragenormalize.SourceBinding(nil), execution.bindings...)
	priorStatus := execution.run.Status
	embedded := execution.embedded
	outcomeCount := len(execution.outcomes)
	execution.mu.Unlock()

	coverageOutcome, reason, err := projectCoverageOutcome(outcome, failedPhase, state)
	if err != nil || task.CoverageTaskOutcome(coverageOutcome, reason) != outcome {
		return task.DomainCompletion{}, task.ErrInvalidArgument
	}
	testOutcome := outcome
	completedInvocations := embedded != nil && outcomeCount > 0 && outcomeCount == len(embedded.Expectations())
	if (stateReachedTests(state, failedPhase) || completedInvocations) &&
		outcome != task.OutcomeCancelled && outcome != task.OutcomeTimedOut &&
		outcome != task.OutcomeInterrupted {
		testOutcome = task.OutcomeSucceeded
	}
	finishedTestRun, err := execution.finishEmbedded(ctx, finishedAt, testOutcome)
	if err != nil {
		return task.DomainCompletion{}, err
	}

	finishedCoverage := execution.run.Clone()
	finishedCoverage.Status = coveragedomain.StatusFinished
	finishedCoverage.Outcome = coverageOutcome
	finishedCoverage.Reason = reason
	finishedCoverage.StartedAt = cloneTimePointer(current.StartedAt)
	finishedCoverage.FinishedAt = cloneTimePointer(&finishedAt)
	finishedCoverage.Summary = nil
	finishedCoverage.ReportID = ""
	finishedCoverage.Artifacts = coveragedomain.ArtifactRefs{}

	var (
		report    *coveragedomain.Report
		publicSet *coveragereport.Set
		publicIDs completionIDs
	)
	if coverageOutcome == coveragedomain.OutcomeAvailable ||
		coverageOutcome == coveragedomain.OutcomePartial {
		if reportSet == nil {
			return task.DomainCompletion{}, task.ErrInvalidArgument
		}
		if err := coveragereport.Validate(*reportSet); err != nil {
			return task.DomainCompletion{}, err
		}
		ids, err := newCompletionIDs(newID)
		if err != nil {
			return task.DomainCompletion{}, err
		}
		built, err := coveragerun.BuildReport(coveragerun.ReportInput{
			State: state, RunID: finishedCoverage.ID,
			TestRunID: finishedCoverage.TestRunID,
			ReportID:  ids.report, ArtifactID: ids.coverageJSON,
			CreatedAt: finishedAt, Summary: reportSet.Summary,
			Toolchain: finishedCoverage.Toolchain, Sources: bindings,
		})
		if err != nil {
			return task.DomainCompletion{}, err
		}
		if !slices.Equal(built.Sources, reportSet.Sources) {
			return task.DomainCompletion{}, task.ErrInvalidArgument
		}
		report = &built
		setCopy := *reportSet
		publicSet = &setCopy
		publicIDs = ids
		finishedCoverage.Summary = cloneCoverageSummary(&built.Summary)
		finishedCoverage.ReportID = built.ID
		finishedCoverage.Artifacts = coveragedomain.ArtifactRefs{
			CoverageJSONID: ids.coverageJSON,
			JUnitXMLID:     ids.junitXML,
			CoverageHTMLID: ids.coverageHTML,
		}
	}
	validatedCoverage, err := coveragedomain.NewRun(finishedCoverage)
	if err != nil {
		return task.DomainCompletion{}, err
	}
	if publicSet != nil {
		if err := coverageSink.CommitBlob(ctx, publicIDs.coverageJSON, "coverage-json", publicSet.CoverageJSON); err != nil {
			return task.DomainCompletion{}, err
		}
		if err := coverageSink.CommitBlob(ctx, publicIDs.junitXML, "junit-xml", publicSet.JUnitXML); err != nil {
			return task.DomainCompletion{}, err
		}
		if err := coverageSink.CommitBlob(ctx, publicIDs.coverageHTML, "coverage-html", publicSet.CoverageHTML); err != nil {
			return task.DomainCompletion{}, err
		}
	}

	events, err := execution.completionDomainEvents(finishedTestRun, validatedCoverage, report)
	if err != nil {
		return task.DomainCompletion{}, err
	}
	finishedTestRun.Results = nil
	return task.DomainCompletion{
		TestRun: &finishedTestRun,
		Coverage: &task.CoverageCompletion{
			Run: validatedCoverage, Expected: priorStatus, Report: report,
		},
		Events: events,
	}, nil
}

type completionReplay struct {
	request completionReplayRequest
	value   task.DomainCompletion
}

type completionReplayRequest struct {
	task        task.Task
	finishedAt  time.Time
	outcome     task.Outcome
	state       coveragerun.State
	failedPhase coveragerun.Phase
	run         coveragedomain.Run
	testRun     testdomain.TestRun
	reportSet   *coveragereport.Set
	bindings    []coveragenormalize.SourceBinding
}

func (execution *execution) completionReplayRequest(
	current task.Task,
	finishedAt time.Time,
	outcome task.Outcome,
) completionReplayRequest {
	execution.mu.Lock()
	defer execution.mu.Unlock()
	return completionReplayRequest{
		task: cloneCompletionTask(current), finishedAt: finishedAt.UTC(), outcome: outcome,
		state: execution.state, failedPhase: execution.failedPhase,
		run: execution.run.Clone(), testRun: execution.testRun.Clone(),
		reportSet: cloneReportSet(execution.reportSet),
		bindings:  append([]coveragenormalize.SourceBinding(nil), execution.bindings...),
	}
}

func cloneCompletionTask(value task.Task) task.Task {
	value.Request = append([]byte(nil), value.Request...)
	value.StartedAt = cloneTimePointer(value.StartedAt)
	value.FinishedAt = cloneTimePointer(value.FinishedAt)
	value.Steps = make([]task.StepSnapshot, len(value.Steps))
	for index, step := range value.Steps {
		value.Steps[index] = step
		value.Steps[index].StartedAt = cloneTimePointer(step.StartedAt)
		value.Steps[index].FinishedAt = cloneTimePointer(step.FinishedAt)
		if step.ExitCode != nil {
			exitCode := *step.ExitCode
			value.Steps[index].ExitCode = &exitCode
		}
	}
	return value
}

func cloneReportSet(value *coveragereport.Set) *coveragereport.Set {
	if value == nil {
		return nil
	}
	copy := *value
	copy.CoverageJSON = append([]byte(nil), value.CoverageJSON...)
	copy.JUnitXML = append([]byte(nil), value.JUnitXML...)
	copy.CoverageHTML = append([]byte(nil), value.CoverageHTML...)
	if value.Sources != nil {
		copy.Sources = append([]coveragedomain.SourceSnapshot{}, value.Sources...)
	}
	return &copy
}

func cloneDomainCompletion(value task.DomainCompletion) task.DomainCompletion {
	result := value
	if value.TestRun != nil {
		copy := value.TestRun.Clone()
		result.TestRun = &copy
	}
	if value.Coverage != nil {
		copy := *value.Coverage
		copy.Run = value.Coverage.Run.Clone()
		if value.Coverage.Report != nil {
			report := value.Coverage.Report.Clone()
			copy.Report = &report
		}
		result.Coverage = &copy
	}
	result.Events = make([]task.DomainEvent, len(value.Events))
	for index, event := range value.Events {
		result.Events[index] = task.DomainEvent{
			Type: event.Type, Payload: append(json.RawMessage(nil), event.Payload...),
		}
	}
	return result
}

type completionIDs struct {
	report       string
	coverageJSON string
	junitXML     string
	coverageHTML string
}

func newCompletionIDs(newID task.IDGenerator) (completionIDs, error) {
	ids := completionIDs{
		report: newID(), coverageJSON: newID(),
		junitXML: newID(), coverageHTML: newID(),
	}
	values := []string{ids.report, ids.coverageJSON, ids.junitXML, ids.coverageHTML}
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if !lowerHex(value, 32) {
			return completionIDs{}, task.ErrInvalidArgument
		}
		if _, duplicate := seen[value]; duplicate {
			return completionIDs{}, task.ErrConflict
		}
		seen[value] = struct{}{}
	}
	return ids, nil
}

func (execution *execution) finishEmbedded(
	ctx context.Context,
	finishedAt time.Time,
	outcome task.Outcome,
) (testdomain.TestRun, error) {
	execution.mu.Lock()
	if execution.finishedTestRun != nil {
		result := execution.finishedTestRun.Clone()
		execution.mu.Unlock()
		return result, nil
	}
	embedded := execution.embedded
	execution.mu.Unlock()

	var result testdomain.TestRun
	var err error
	if embedded != nil {
		result, err = embedded.Finish(ctx, finishedAt, outcome)
	} else {
		result, err = execution.finishUnstartedTestRun(finishedAt, outcome)
	}
	if err != nil {
		return testdomain.TestRun{}, err
	}
	execution.mu.Lock()
	if execution.finishedTestRun == nil {
		copy := result.Clone()
		execution.finishedTestRun = &copy
	}
	result = execution.finishedTestRun.Clone()
	execution.mu.Unlock()
	return result, nil
}

func (execution *execution) finishUnstartedTestRun(
	finishedAt time.Time,
	outcome task.Outcome,
) (testdomain.TestRun, error) {
	run := execution.testRun.Clone()
	if run.Status != testdomain.RunQueued || finishedAt.Before(run.CreatedAt) {
		return testdomain.TestRun{}, task.ErrConflict
	}
	summary, incomplete, err := testrun.Summarize(run.Results, run.Summary.Iterations)
	if err != nil {
		return testdomain.TestRun{}, err
	}
	if outcome != task.OutcomeSucceeded {
		incomplete = true
	}
	run.Status = testdomain.RunCompleted
	run.Outcome = coverageTestRunOutcome(outcome, summary, incomplete)
	run.FinishedAt = cloneTimePointer(&finishedAt)
	run.Summary = summary
	run.Incomplete = incomplete
	run.ResultRevision, err = testdomain.ResultRevision(run.Results)
	if err != nil {
		return testdomain.TestRun{}, err
	}
	return testdomain.NewTestRun(run)
}

func coverageTestRunOutcome(
	outcome task.Outcome,
	summary testdomain.RunSummary,
	incomplete bool,
) testdomain.RunOutcome {
	switch outcome {
	case task.OutcomeCancelled:
		return testdomain.RunCancelled
	case task.OutcomeTimedOut:
		return testdomain.RunTimedOut
	case task.OutcomeInterrupted:
		return testdomain.RunInterrupted
	case task.OutcomeInfrastructureFailed:
		return testdomain.RunErrored
	case task.OutcomeCommandFailed:
		return testdomain.RunBlocked
	case task.OutcomeSucceeded:
		switch {
		case summary.TimedOut != 0:
			return testdomain.RunTimedOut
		case summary.Cancelled != 0:
			return testdomain.RunCancelled
		case summary.Errored != 0 || incomplete:
			return testdomain.RunErrored
		case summary.Failed != 0:
			return testdomain.RunFailed
		default:
			return testdomain.RunPassed
		}
	default:
		panic(errors.New("unsupported task outcome"))
	}
}

func (execution *execution) completionDomainEvents(
	run testdomain.TestRun,
	coverage coveragedomain.Run,
	report *coveragedomain.Report,
) ([]task.DomainEvent, error) {
	// Pull the EmbeddedRun terminal event into the one terminal mutation. The
	// ordinary DomainEventSource intentionally defers it until this point.
	nonTerminal := execution.DrainDomainEvents()
	execution.mu.Lock()
	events := append(nonTerminal, cloneEvents(execution.completionEvents)...)
	execution.completionEvents = nil
	execution.mu.Unlock()
	hasFinishedTestRun := false
	for _, event := range events {
		hasFinishedTestRun = hasFinishedTestRun || event.Type == task.EventTestRunFinished
	}
	if !hasFinishedTestRun {
		event, err := coverageDomainEvent(task.EventTestRunFinished, map[string]any{
			"runId": run.RunID, "outcome": run.Outcome,
			"summary": run.Summary, "resultRevision": run.ResultRevision,
			"incomplete": run.Incomplete,
		})
		if err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	if report != nil {
		event, err := coverageDomainEvent(task.EventCoverageReportAvailable, map[string]any{
			"coverageRunId": coverage.ID, "reportId": report.ID,
			"artifactId": report.ArtifactID,
			"completeness": map[string]any{
				"outcome": report.Completeness.Outcome,
				"reasons": append([]coveragedomain.CompletenessReason{}, report.Completeness.Reasons...),
			},
			"summary": report.Summary,
		})
		if err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	payload := map[string]any{
		"coverageRunId": coverage.ID,
		"outcome":       coverage.Outcome,
	}
	if coverage.Reason != "" {
		payload["reason"] = coverage.Reason
	}
	event, err := coverageDomainEvent(task.EventCoverageRunFinished, payload)
	if err != nil {
		return nil, err
	}
	events = append(events, event)
	return events, nil
}

func coverageDomainEvent(kind task.EventType, value any) (task.DomainEvent, error) {
	payload, err := json.Marshal(value)
	if err != nil || !json.Valid(payload) {
		return task.DomainEvent{}, task.ErrInvalidArgument
	}
	return task.DomainEvent{Type: kind, Payload: payload}, nil
}

func stateReachedTests(state coveragerun.State, failedPhase coveragerun.Phase) bool {
	if state.TestFailed || len(state.PartialReasons) != 0 {
		return true
	}
	switch state.Phase {
	case coveragerun.PhaseMerge, coveragerun.PhaseNormalize,
		coveragerun.PhaseReport, coveragerun.PhasePublish:
		return true
	}
	switch failedPhase {
	case coveragerun.PhaseMerge, coveragerun.PhaseNormalize,
		coveragerun.PhaseReport, coveragerun.PhasePublish:
		return true
	default:
		return false
	}
}

var _ task.PreparedCompletionDiscarder = (*execution)(nil)

func cloneTimePointer(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	copy := value.UTC()
	return &copy
}

func cloneCoverageSummary(value *coveragedomain.Summary) *coveragedomain.Summary {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}
