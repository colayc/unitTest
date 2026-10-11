package runtime

import (
	"context"
	"unit-test-ide.local/test-service/internal/testgendomain"
)

func (a *productionGenerationValidation) Minimize(ctx context.Context, run testgendomain.Run, target generationTarget) (GenerationStageResult, error) {
	_, retained, err := a.checkpointRecord(ctx, run, target, testgendomain.StateValidating)
	if err != nil {
		return GenerationStageResult{}, err
	}
	if !retained {
		return GenerationStageResult{Next: testgendomain.StateRejected}, nil
	}
	return GenerationStageResult{Next: testgendomain.StateMinimizing}, nil
}
func (a *productionGenerationValidation) Preview(ctx context.Context, run testgendomain.Run, target generationTarget) (GenerationStageResult, error) {
	record, retained, err := a.checkpointRecord(ctx, run, target, testgendomain.StateMinimizing)
	if err != nil {
		return GenerationStageResult{}, err
	}
	if !retained {
		return GenerationStageResult{}, errProductionValidationUnavailable
	}
	set := cloneProductionCandidateSet(record.set)
	return GenerationStageResult{Next: testgendomain.StateAwaitingConfirmation,
		MinimizedCaseIDs: []string{record.candidate.CaseID}, PreviewSet: &set}, nil
}
