package runtime

import (
	"context"

	"unit-test-ide.local/test-service/internal/coveragedetail"
	generationv15 "unit-test-ide.local/test-service/internal/protocolmodel/v1_5/testgeneration"
	generationv16 "unit-test-ide.local/test-service/internal/protocolmodel/v1_6/testgeneration"
	"unit-test-ide.local/test-service/internal/task"
	"unit-test-ide.local/test-service/internal/testgendomain"
)

// v1.6 is an explicit managed facade. Its read methods never expose a legacy
// run and never perform publication; reviews/apply is the sole managed writer.
func (p *ManagedRuntimeProvider) managedRun(ctx context.Context, owner, runID string) (testgendomain.Run, error) {
	if !p.ManagedTestsReady() {
		return testgendomain.Run{}, task.ErrStorageUnavailable
	}
	run, err := p.generationService.owned(ctx, owner, runID)
	if err != nil {
		return testgendomain.Run{}, err
	}
	if run.Request.ManagedSelectionID() == "" || !validGenerationRunID(run.Request.CoverageReportID) {
		return testgendomain.Run{}, task.ErrNotFound
	}
	index, err := p.config.CurrentIndex.ReadCurrentCoverageIndex(ctx, coveragedetail.CurrentIndexQuery{ProjectID: run.Request.ProjectID, ReportID: run.Request.CoverageReportID, WorkspaceGeneration: run.Request.WorkspaceGeneration})
	if err != nil {
		return testgendomain.Run{}, err
	}
	target, err := testgendomain.ResolveManagedTarget(testgendomain.ManagedSelector{ProjectID: run.Request.ProjectID, WorkspaceGeneration: run.Request.WorkspaceGeneration,
		CoverageReportID: run.Request.CoverageReportID, Scope: run.Request.Scope, ID: run.Request.ManagedSelectionID()}, index)
	if err != nil {
		return testgendomain.Run{}, err
	}
	if target.SourceDigest != run.Request.SourceDigest {
		return testgendomain.Run{}, testgendomain.ErrStaleSnapshot
	}
	return run, nil
}

func (p *ManagedRuntimeProvider) ListManagedTargets(ctx context.Context, owner string, input generationv16.TestGenerationTargetListRequestV16) (generationv16.TestGenerationTargetListV16, error) {
	if !p.ManagedTestsReady() {
		return generationv16.TestGenerationTargetListV16{}, task.ErrStorageUnavailable
	}
	if ctx == nil || !validGenerationOwner(owner) || input.ProjectID == "" || !validGenerationDigest(input.WorkspaceGeneration) {
		return generationv16.TestGenerationTargetListV16{}, task.ErrInvalidArgument
	}
	// Only report-bound coverage gaps are currently resolvable. A build-target
	// listing here would claim a selector that StartManaged cannot attest.
	return generationv16.TestGenerationTargetListV16{Items: []generationv16.TestGenerationTargetV16{}}, ctx.Err()
}

func (p *ManagedRuntimeProvider) GetManagedRun(ctx context.Context, owner, runID string) (generationv16.TestGenerationRunV16, error) {
	run, err := p.managedRun(ctx, owner, runID)
	if err != nil {
		return generationv16.TestGenerationRunV16{}, err
	}
	return projectManagedRun(run), nil
}

func (p *ManagedRuntimeProvider) CancelManagedRun(ctx context.Context, owner, runID string) (generationv16.TestGenerationRunV16, error) {
	if _, err := p.managedRun(ctx, owner, runID); err != nil {
		return generationv16.TestGenerationRunV16{}, err
	}
	if _, err := p.generationService.CancelTestGeneration(ctx, owner, runID); err != nil {
		return generationv16.TestGenerationRunV16{}, err
	}
	return p.GetManagedRun(ctx, owner, runID)
}

func (p *ManagedRuntimeProvider) ReplayManagedEvents(ctx context.Context, owner string, input generationv16.TestGenerationEventReplayRequestV16) (generationv16.TestGenerationEventPageV16, error) {
	if _, err := p.managedRun(ctx, owner, input.RunID); err != nil {
		return generationv16.TestGenerationEventPageV16{}, err
	}
	page, err := p.generationService.ReplayTestGenerationEvents(ctx, owner, generationv15.TestGenerationEventReplayRequestV15{RunID: input.RunID, AfterSequence: input.AfterSequence, Limit: input.Limit})
	if err != nil {
		return generationv16.TestGenerationEventPageV16{}, err
	}
	result := generationv16.TestGenerationEventPageV16{Items: make([]generationv16.TestGenerationProgressEventV16, 0, len(page.Items)), NextAfterSequence: page.NextAfterSequence}
	for _, item := range page.Items {
		result.Items = append(result.Items, generationv16.TestGenerationProgressEventV16{Sequence: item.Sequence, State: generationv16.TestGenerationStateV16(item.State), OccurredAt: item.OccurredAt})
	}
	return result, nil
}

func (p *ManagedRuntimeProvider) ListManagedCandidates(ctx context.Context, owner string, input generationv16.TestGenerationCandidateListRequestV16) (generationv16.TestGenerationCandidatePageV16, error) {
	if _, err := p.managedRun(ctx, owner, input.RunID); err != nil {
		return generationv16.TestGenerationCandidatePageV16{}, err
	}
	page, err := p.generationService.ListTestGenerationCandidates(ctx, owner, generationv15.TestGenerationCandidateListRequestV15{RunID: input.RunID, Cursor: input.Cursor, Limit: input.Limit})
	if err != nil {
		return generationv16.TestGenerationCandidatePageV16{}, err
	}
	result := generationv16.TestGenerationCandidatePageV16{Items: make([]generationv16.TestGenerationCandidateV16, 0, len(page.Items)), NextCursor: page.NextCursor}
	for _, item := range page.Items {
		out := generationv16.TestGenerationCandidateV16{CandidateID: item.CandidateID, Kind: generationv16.TestGenerationCandidateKindV16(item.Kind), CodeDigest: item.CodeDigest,
			ArtifactDigest: item.ArtifactDigest, CharacterizationConfirmed: item.CharacterizationConfirmed,
			AssertionProvenance: generationv16.TestGenerationAssertionProvenanceV16{Kind: generationv16.Kind(item.AssertionProvenance.Kind), EvidenceDigest: item.AssertionProvenance.EvidenceDigest},
			BaselineCoverage:    generationv16.TestGenerationCoverageV16{FunctionPercent: item.BaselineCoverage.FunctionPercent, LinePercent: item.BaselineCoverage.LinePercent, BranchPercent: item.BaselineCoverage.BranchPercent},
			DeltaCoverage:       generationv16.TestGenerationCoverageV16{FunctionPercent: item.DeltaCoverage.FunctionPercent, LinePercent: item.DeltaCoverage.LinePercent, BranchPercent: item.DeltaCoverage.BranchPercent},
			Diagnostics:         make([]generationv16.TestGenerationDiagnosticV16, 0, len(item.Diagnostics)), PlannedEdits: make([]generationv16.TestGenerationPlannedEditV16, 0, len(item.PlannedEdits))}
		for _, diagnostic := range item.Diagnostics {
			var reason *generationv16.TestGenerationDiagnosticReasonV16
			if diagnostic.Reason != nil {
				value := generationv16.TestGenerationDiagnosticReasonV16(*diagnostic.Reason)
				reason = &value
			}
			out.Diagnostics = append(out.Diagnostics, generationv16.TestGenerationDiagnosticV16{Code: generationv16.TestGenerationDiagnosticCodeV16(diagnostic.Code), Severity: generationv16.Severity(diagnostic.Severity), Reason: reason})
		}
		for _, edit := range item.PlannedEdits {
			out.PlannedEdits = append(out.PlannedEdits, generationv16.TestGenerationPlannedEditV16{Path: edit.Path, Operation: generationv16.Operation(edit.Operation), BeforeDigest: edit.BeforeDigest, AfterDigest: edit.AfterDigest})
		}
		result.Items = append(result.Items, out)
	}
	return result, nil
}

func projectManagedRun(run testgendomain.Run) generationv16.TestGenerationRunV16 {
	old := generationRunV15(run)
	result := generationv16.TestGenerationRunV16{RunID: old.RunID, TaskID: old.TaskID, ProjectID: old.ProjectID, WorkspaceGeneration: old.WorkspaceGeneration,
		State: generationv16.TestGenerationStateV16(old.State), CreatedAt: old.CreatedAt, FinishedAt: old.FinishedAt, LastSequence: old.LastSequence, CandidateCount: old.CandidateCount}
	if old.Preview != nil {
		result.Preview = &generationv16.TestGenerationPreviewV16{CandidateSetDigest: old.Preview.CandidateSetDigest, CharacterizationDigest: old.Preview.CharacterizationDigest, ConfirmationDigest: old.Preview.ConfirmationDigest, Diff: old.Preview.Diff, DiffDigest: old.Preview.DiffDigest}
	}
	return result
}
