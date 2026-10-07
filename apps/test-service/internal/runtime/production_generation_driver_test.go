package runtime

import (
	"context"
	"reflect"
	"testing"
	"time"

	generationv15 "unit-test-ide.local/test-service/internal/protocolmodel/v1_5/testgeneration"
	"unit-test-ide.local/test-service/internal/testgendomain"
	"unit-test-ide.local/test-service/internal/testgenpublish"
)

type productionTargetResolverFixture struct {
	target generationTarget
	calls  int
}

func (resolver *productionTargetResolverFixture) Targets(context.Context, generationv15.TestGenerationTargetListRequestV15) (generationv15.TestGenerationTargetListV15, error) {
	return generationv15.TestGenerationTargetListV15{}, nil
}

func (resolver *productionTargetResolverFixture) ResolveStart(context.Context, generationv15.TestGenerationStartRequestV15) (generationTarget, error) {
	resolver.calls++
	return resolver.target, nil
}

func (resolver *productionTargetResolverFixture) ResolveRequest(context.Context, testgendomain.Request) (generationTarget, error) {
	resolver.calls++
	return resolver.target, nil
}

type productionValidatorFixture struct{ validations int }

func (*productionValidatorFixture) Ready() bool { return true }
func (validator *productionValidatorFixture) Validate(_ context.Context, _ testgendomain.Run, _ generationTarget, _ productionPipelineResult) (GenerationStageResult, error) {
	validator.validations++
	return GenerationStageResult{Next: testgendomain.StateValidating}, nil
}
func (*productionValidatorFixture) Minimize(_ context.Context, _ testgendomain.Run, _ generationTarget, _ productionPipelineResult) (GenerationStageResult, error) {
	return GenerationStageResult{Next: testgendomain.StateAwaitingConfirmation, MinimizedCaseIDs: []string{}, PreviewSet: &testgenpublish.CandidateSet{}}, nil
}
func (*productionValidatorFixture) ValidateCandidate(context.Context, testgendomain.Run, testgendomain.Candidate) error {
	return nil
}
func (*productionValidatorFixture) CandidateSet(context.Context, testgendomain.Run, []testgendomain.Candidate) (testgenpublish.CandidateSet, error) {
	return testgenpublish.CandidateSet{}, nil
}

func TestProductionGenerationDriverResolvesAndRunsFixedStages(t *testing.T) {
	pipeline, target, analyzer, _ := productionPipelineFixture(t)
	resolver := &productionTargetResolverFixture{target: target}
	validator := &productionValidatorFixture{}
	driver := newProductionGenerationDriver(resolver, pipeline, validator)
	request, err := driver.Resolve(context.Background(), generationv15.TestGenerationStartRequestV15{})
	if err != nil || !reflect.DeepEqual(request, target.request) {
		t.Fatalf("Resolve()=%+v, %v", request, err)
	}
	run := testgendomain.Run{ID: "11111111111111111111111111111111", TaskID: "22222222222222222222222222222222", Request: request, CreatedAt: time.Now()}
	authorizations := 0
	authorize := func(context.Context) error { authorizations++; return nil }
	for _, stage := range []testgendomain.State{
		testgendomain.StateBaseline, testgendomain.StateAnalyzing, testgendomain.StateSolving,
		testgendomain.StateRendering, testgendomain.StateValidating,
	} {
		result, err := driver.RunStage(context.Background(), run, stage, authorize)
		if err != nil || result.Next != stage {
			t.Fatalf("stage %s result=%+v err=%v", stage, result, err)
		}
	}
	if analyzer.calls != 1 || validator.validations != 1 || authorizations != 2 {
		t.Fatalf("analyze=%d validate=%d authorize=%d", analyzer.calls, validator.validations, authorizations)
	}
	if resolver.calls != 6 {
		t.Fatalf("resolver calls=%d", resolver.calls)
	}
	if amount := driver.StageBudget(run, testgendomain.StateValidating); amount.Candidates != request.Budgets.CandidateCount || amount.Processes < 1 {
		t.Fatalf("validation budget=%+v", amount)
	}
}

func TestProductionGenerationDriverFailsClosedBeforeAnyStageProcess(t *testing.T) {
	pipeline, target, _, _ := productionPipelineFixture(t)
	target.sourceDigest = "changed"
	resolver := &productionTargetResolverFixture{target: target}
	driver := newProductionGenerationDriver(resolver, pipeline, &productionValidatorFixture{})
	run := testgendomain.Run{ID: "11111111111111111111111111111111", TaskID: "22222222222222222222222222222222", Request: target.request, CreatedAt: time.Now()}
	authorized := false
	_, err := driver.RunStage(context.Background(), run, testgendomain.StateAnalyzing, func(context.Context) error { authorized = true; return nil })
	if err == nil || authorized {
		t.Fatalf("err=%v authorized=%v", err, authorized)
	}
}

var _ GenerationDriver = (*productionGenerationDriver)(nil)
