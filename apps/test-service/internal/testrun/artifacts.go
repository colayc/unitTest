package testrun

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"unit-test-ide.local/test-service/internal/task"
	"unit-test-ide.local/test-service/internal/testdomain"
)

type runSummaryArtifact struct {
	RunID           string                `json:"runId"`
	TaskID          string                `json:"taskId"`
	Status          testdomain.RunStatus  `json:"status"`
	Outcome         testdomain.RunOutcome `json:"outcome"`
	StartedAt       *time.Time            `json:"startedAt,omitempty"`
	FinishedAt      time.Time             `json:"finishedAt"`
	Summary         testdomain.RunSummary `json:"summary"`
	ResultRevision  string                `json:"resultRevision"`
	Incomplete      bool                  `json:"incomplete"`
	CatalogRevision string                `json:"catalogRevision"`
}

func (execution *runExecution) PrepareCompletion(
	ctx context.Context,
	current task.Task,
	finishedAt time.Time,
	outcome task.Outcome,
	sink task.ArtifactSink,
	newID task.IDGenerator,
) (task.DomainCompletion, error) {
	if execution == nil || ctx == nil ||
		current.Kind != task.KindTestRun ||
		current.ID == "" || finishedAt.IsZero() ||
		sink == nil || newID == nil {
		return task.DomainCompletion{}, task.ErrInvalidArgument
	}
	run, err := execution.runs.GetRun(ctx, execution.runID)
	if err != nil {
		return task.DomainCompletion{}, err
	}
	if run.TaskID != current.ID ||
		run.Status == testdomain.RunCompleted {
		return task.DomainCompletion{}, task.ErrConflict
	}
	summary, incomplete, err := Summarize(
		run.Results,
		run.Summary.Iterations,
	)
	if err != nil {
		return task.DomainCompletion{}, err
	}
	execution.mu.Lock()
	expectedResults := execution.expectedResults
	execution.mu.Unlock()
	if expectedResults == 0 ||
		summary.Total != expectedResults {
		incomplete = true
	}
	run.Status = testdomain.RunCompleted
	run.Outcome = completedRunOutcome(
		outcome,
		summary,
		incomplete,
	)
	run.StartedAt = cloneTime(current.StartedAt)
	run.FinishedAt = &finishedAt
	run.Summary = summary
	run.Incomplete = incomplete
	run.ResultRevision, err = testdomain.ResultRevision(run.Results)
	if err != nil {
		return task.DomainCompletion{}, err
	}
	validated, err := testdomain.NewTestRun(run)
	if err != nil {
		return task.DomainCompletion{}, err
	}
	resultLines := make(
		[]json.RawMessage,
		len(validated.Results),
	)
	for index, result := range validated.Results {
		encoded, err := json.Marshal(result)
		if err != nil {
			return task.DomainCompletion{}, err
		}
		resultLines[index] = encoded
	}
	if err := sink.CommitJSON(
		ctx,
		newID(),
		"test-selection",
		validated.SelectionSnapshot,
	); err != nil {
		return task.DomainCompletion{}, err
	}
	if err := sink.CommitJSONLines(
		ctx,
		newID(),
		"test-results",
		resultLines,
	); err != nil {
		return task.DomainCompletion{}, err
	}
	if err := sink.CommitJSON(
		ctx,
		newID(),
		"test-run-summary",
		runSummaryArtifact{
			RunID:           validated.RunID,
			TaskID:          validated.TaskID,
			Status:          validated.Status,
			Outcome:         validated.Outcome,
			StartedAt:       cloneTime(validated.StartedAt),
			FinishedAt:      finishedAt,
			Summary:         validated.Summary,
			ResultRevision:  validated.ResultRevision,
			Incomplete:      validated.Incomplete,
			CatalogRevision: validated.CatalogRevision,
		},
	); err != nil {
		return task.DomainCompletion{}, err
	}
	finishedEvent, err := newDomainEvent(
		task.EventTestRunFinished,
		map[string]any{
			"runId":          validated.RunID,
			"outcome":        validated.Outcome,
			"summary":        validated.Summary,
			"resultRevision": validated.ResultRevision,
			"incomplete":     validated.Incomplete,
		},
	)
	if err != nil {
		return task.DomainCompletion{}, err
	}
	validated.Results = nil
	return task.DomainCompletion{
		TestRun: &validated,
		Events:  []task.DomainEvent{finishedEvent},
	}, nil
}

func completedRunOutcome(
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

func cloneTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}
