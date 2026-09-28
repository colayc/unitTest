package coverageexec

import (
	"context"

	"unit-test-ide.local/test-service/internal/coveragedetail"
	"unit-test-ide.local/test-service/internal/coveragedomain"
	"unit-test-ide.local/test-service/internal/task"
)

// CompletionCommitted runs only after the canonical report transaction. Its
// result is intentionally non-fatal to the already durable v1 graph.
func (execution *execution) CompletionCommitted(ctx context.Context, completion task.DomainCompletion) error {
	if execution == nil || completion.Coverage == nil || completion.Coverage.Report == nil || execution.config.DetailStore == nil {
		return nil
	}
	execution.mu.Lock()
	normalized := execution.normalized
	detailNormalized := execution.detailNormalized
	observations := append([]coveragedomain.FunctionObservation(nil), execution.observations...)
	request := execution.run.Request
	execution.mu.Unlock()
	if !normalized || !detailNormalized {
		return nil
	}
	report := completion.Coverage.Report.Clone()
	index, err := coveragedetail.Build(coveragedetail.BuildInput{WorkspaceGeneration: request.WorkspaceGeneration, ProjectID: request.ProjectID, Report: report, Sources: report.Sources, Functions: observations})
	if err == nil {
		err = execution.config.DetailStore.PutCoverageDetail(ctx, index)
	}
	if err != nil && execution.config.DetailFailure != nil {
		execution.config.DetailFailure(err)
	}
	return nil
}
