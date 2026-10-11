package runtime

import (
	"context"
	"reflect"
	"sort"
	"unit-test-ide.local/test-service/internal/task"
	"unit-test-ide.local/test-service/internal/testgendomain"
)

// GetGeneration reads the authoritative completed checkpoint, not a caller's
// reconstructed state or budget record.
func (a runtimeProductionGenerationArtifacts) GetGeneration(ctx context.Context, id string) (testgendomain.Run, error) {
	if ctx == nil || a.metadata == nil {
		return testgendomain.Run{}, task.ErrStorageUnavailable
	}
	return a.metadata.GetGeneration(ctx, id)
}

func (a runtimeProductionGenerationArtifacts) ListGenerationCandidates(ctx context.Context, id string) ([]testgendomain.Candidate, error) {
	if ctx == nil || a.metadata == nil {
		return nil, task.ErrStorageUnavailable
	}
	return a.metadata.ListGenerationCandidates(ctx, id)
}
func checkpointTargetMatches(record productionValidatedRecord, target generationTarget) bool {
	b := record.binding
	if b.ProjectID != target.projectID || b.WorkspaceGeneration != target.workspaceGeneration ||
		b.CoverageReportID != target.coverageReportID || b.BaselineReportDigest != target.request.BaselineReportDigest ||
		b.SourceRelativePath != target.sourceRelativePath || b.SourceDigest != target.sourceDigest ||
		record.set.TestTarget != target.renderTarget.TestTarget ||
		record.set.ProductionTarget != target.renderTarget.ProductionTarget ||
		record.set.FrameworkTarget != target.renderTarget.FrameworkTarget ||
		!validProductionDigest(record.set.SymbolID) || record.candidate.Kind != b.Kind {
		return false
	}
	selected := target.selectedFunctions()
	ids := make([]string, 0, len(selected))
	knownSymbols := make([]string, 0, len(selected))
	allKnown := true
	for _, f := range selected {
		ids = append(ids, f.functionID)
		if !validProductionDigest(f.gap.SymbolID) {
			allKnown = false
		} else {
			knownSymbols = append(knownSymbols, f.gap.SymbolID)
		}
	}
	sort.Strings(ids)
	sort.Strings(knownSymbols)
	if !reflect.DeepEqual(ids, b.CoverageFunctionIDs) ||
		len(b.TargetFunctionIDs) != len(ids) {
		return false
	}
	previous := ""
	for _, id := range b.TargetFunctionIDs {
		if !validProductionDigest(id) || id <= previous {
			return false
		}
		previous = id
	}
	if allKnown && !reflect.DeepEqual(knownSymbols, b.TargetFunctionIDs) {
		return false
	}
	symbol := "fn:" + record.set.SymbolID
	if target.request.Scope == testgendomain.ScopeFile {
		symbol = "file:" + target.fileID
	}
	return b.TargetSymbol == symbol && record.candidate.TargetSymbol == symbol
}
func (a *productionGenerationValidation) checkpointRecord(ctx context.Context, run testgendomain.Run, target generationTarget, state testgendomain.State) (productionValidatedRecord, bool, error) {
	if ctx == nil {
		return productionValidatedRecord{}, false, task.ErrInvalidArgument
	}
	if err := ctx.Err(); err != nil {
		return productionValidatedRecord{}, false, err
	}
	if !a.Ready() || !target.valid() || !reflect.DeepEqual(run.Request, target.request) ||
		run.State != state || testgendomain.ValidateRun(run) != nil ||
		run.Record.IsZero() || !run.Record.ValidFor(run.Request, run.CandidateCount) ||
		run.Record.Preview != nil || len(run.Record.MinimizedCaseIDs) != 0 ||
		run.Record.BudgetUsed.Candidates != run.Request.Budgets.CandidateCount {
		return productionValidatedRecord{}, false, errProductionValidationUnavailable
	}
	reader, ok := a.artifacts.(productionManagedCandidateReader)
	if !ok {
		return productionValidatedRecord{}, false, task.ErrStorageUnavailable
	}
	runs, ok := a.artifacts.(productionManagedSelectionRunReader)
	if !ok {
		return productionValidatedRecord{}, false, task.ErrStorageUnavailable
	}
	persisted, err := runs.GetGeneration(ctx, run.ID)
	if contextErr := ctx.Err(); contextErr != nil {
		return productionValidatedRecord{}, false, contextErr
	}
	if err != nil {
		return productionValidatedRecord{}, false, err
	}
	if !reflect.DeepEqual(persisted, run) {
		return productionValidatedRecord{}, false, errProductionValidationUnavailable
	}
	candidates, err := reader.ListGenerationCandidates(ctx, run.ID)
	if contextErr := ctx.Err(); contextErr != nil {
		return productionValidatedRecord{}, false, contextErr
	}
	if err != nil {
		return productionValidatedRecord{}, false, err
	}
	if len(candidates) != run.CandidateCount || len(candidates) > 1 {
		return productionValidatedRecord{}, false, errProductionValidationUnavailable
	}
	if len(candidates) == 0 {
		if state != testgendomain.StateValidating || len(run.ArtifactDigests) != 0 {
			return productionValidatedRecord{}, false, errProductionValidationUnavailable
		}
		a.mu.Lock()
		delete(a.records, run.ID)
		a.mu.Unlock()
		return productionValidatedRecord{}, false, nil
	}
	if len(run.ArtifactDigests) != 2 || testgendomain.ValidateCandidate(candidates[0]) != nil {
		return productionValidatedRecord{}, false, errProductionValidationUnavailable
	}
	record, err := a.loadRecord(ctx, run, candidates[0])
	if contextErr := ctx.Err(); contextErr != nil {
		return productionValidatedRecord{}, false, contextErr
	}
	if err != nil {
		return productionValidatedRecord{}, false, err
	}
	if record.artifact.ID == record.evidence.ID || !checkpointTargetMatches(record, target) {
		return productionValidatedRecord{}, false, errProductionValidationUnavailable
	}
	err = a.ValidateCandidate(ctx, run, candidates[0])
	if contextErr := ctx.Err(); contextErr != nil {
		return productionValidatedRecord{}, false, contextErr
	}
	if err != nil {
		return productionValidatedRecord{}, false, err
	}
	if err := ctx.Err(); err != nil {
		return productionValidatedRecord{}, false, err
	}
	return record, true, nil
}
