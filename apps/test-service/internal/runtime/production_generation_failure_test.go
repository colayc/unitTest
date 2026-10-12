package runtime

import (
	"context"
	"errors"
	"testing"
	"time"
	"unit-test-ide.local/test-service/internal/task"
	analysis "unit-test-ide.local/test-service/internal/testgenanalysis"
	"unit-test-ide.local/test-service/internal/testgendomain"
)

type diagnosticProductionResolver struct {
	productionTargetResolverFixture
	cause error
}

func (r *diagnosticProductionResolver) ResolveRequest(context.Context, testgendomain.Request) (generationTarget, error) {
	if r.cause != nil {
		return generationTarget{}, r.cause
	}
	return r.target, nil
}

type diagnosticProductionAnalyzer struct{ cause error }

func (a *diagnosticProductionAnalyzer) Analyze(context.Context, analysis.AnalysisRequest) (analysis.Program, error) {
	return analysis.Program{}, a.cause
}

type diagnosticProductionValidator struct {
	productionValidatorFixture
	cause error
}

func (v *diagnosticProductionValidator) Validate(context.Context, testgendomain.Run, generationTarget, productionPipelineResult) (GenerationStageResult, error) {
	return GenerationStageResult{}, v.cause
}
func (v *diagnosticProductionValidator) Minimize(context.Context, testgendomain.Run, generationTarget) (GenerationStageResult, error) {
	return GenerationStageResult{}, v.cause
}
func (v *diagnosticProductionValidator) Preview(context.Context, testgendomain.Run, generationTarget) (GenerationStageResult, error) {
	return GenerationStageResult{}, v.cause
}

func TestProductionGenerationFailureBoundariesPreserveIdentity(t *testing.T) {
	for _, tc := range []struct {
		name, operation string
		stage           testgendomain.State
		cause           error
	}{
		{"resolve", "resolve_target", testgendomain.StateAnalyzing, testgendomain.ErrStaleSnapshot},
		{"authorize", "authorize_process", testgendomain.StateAnalyzing, task.ErrConflict},
		{"pipeline", "pipeline_generate", testgendomain.StateAnalyzing, task.ErrInvalidArgument},
		{"validate", "validate_candidates", testgendomain.StateValidating, task.ErrConflict},
		{"minimize", "minimize_candidates", testgendomain.StateMinimizing, testgendomain.ErrStaleSnapshot},
		{"preview", "preview_set", testgendomain.StateAwaitingConfirmation, task.ErrConflict},
		{"validator-unavailable", "validate_candidates", testgendomain.StateValidating, task.ErrStorageUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pipeline, target, _, _ := productionPipelineFixture(t)
			resolver := &diagnosticProductionResolver{productionTargetResolverFixture: productionTargetResolverFixture{target: target}}
			validator := &diagnosticProductionValidator{cause: tc.cause}
			driver := newProductionGenerationDriver(resolver, pipeline, validator)
			run := testgendomain.Run{ID: "11111111111111111111111111111111", TaskID: "22222222222222222222222222222222", Request: target.request, CreatedAt: time.Now()}
			authorize := func(context.Context) error { return nil }
			switch tc.name {
			case "resolve":
				resolver.cause = tc.cause
			case "authorize":
				authorize = func(context.Context) error { return tc.cause }
			case "pipeline":
				pipeline.analyzer = &diagnosticProductionAnalyzer{cause: tc.cause}
			default:
				driver.cache[run.ID] = cachedProductionPipeline{request: run.Request, result: productionPipelineResult{}}
				if tc.name == "validator-unavailable" {
					driver.validator = nil
				}
			}
			_, err := driver.RunStage(context.Background(), run, tc.stage, authorize)
			if !errors.Is(err, tc.cause) {
				t.Fatal("boundary wrapper lost original identity")
			}
			got := generationFailureDiagnostic(string(tc.stage), "stage_run", err)
			if got.Operation != tc.operation {
				t.Fatal("production boundary misidentified")
			}
		})
	}
}
