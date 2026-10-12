package runtime

import (
	"context"
	"strings"
	"testing"

	analysis "unit-test-ide.local/test-service/internal/testgenanalysis"
	assertion "unit-test-ide.local/test-service/internal/testgenassert"
	solver "unit-test-ide.local/test-service/internal/testgensolver"
)

func TestProductionStaticOracleProvesEveryScalarReturnPath(t *testing.T) {
	symbol := strings.Repeat("a", 64)
	source := strings.Repeat("b", 64)
	program := analysis.Program{Version: analysis.IRVersion, Digest: strings.Repeat("c", 64), Functions: []analysis.Function{{
		SymbolID: symbol, Name: "choose", ReturnType: analysis.Type{Kind: analysis.TypeInteger, BitWidth: 32, Signed: true},
		Parameters: []analysis.Parameter{{Name: "x", Type: analysis.Type{Kind: analysis.TypeInteger, BitWidth: 32, Signed: true}}},
		Excerpt:    analysis.SourceExcerpt{Digest: source}, Decision: analysis.Decision{Kind: analysis.DecisionSupported, Reason: analysis.ReasonNone},
		ReturnRules: []analysis.ReturnRule{
			{Conditions: []analysis.ClosedExpression{{Kind: analysis.ExpressionBinary, Operator: ">", Left: &analysis.ClosedExpression{Kind: analysis.ExpressionParameter, Name: "x"}, Right: &analysis.ClosedExpression{Kind: analysis.ExpressionInteger, Value: "0"}}}, Result: analysis.ClosedExpression{Kind: analysis.ExpressionInteger, Value: "1"}},
			{Conditions: []analysis.ClosedExpression{{Kind: analysis.ExpressionUnary, Operator: "!", Left: &analysis.ClosedExpression{Kind: analysis.ExpressionBinary, Operator: ">", Left: &analysis.ClosedExpression{Kind: analysis.ExpressionParameter, Name: "x"}, Right: &analysis.ClosedExpression{Kind: analysis.ExpressionInteger, Value: "0"}}}}, Result: analysis.ClosedExpression{Kind: analysis.ExpressionInteger, Value: "0"}},
		},
	}}}
	vectors := []solver.InputVector{
		{ID: strings.Repeat("d", 64), Inputs: []solver.Input{{Name: "x", Value: solver.Value{Kind: analysis.TypeInteger, Integer: "2"}}}},
		{ID: strings.Repeat("e", 64), Inputs: []solver.Input{{Name: "x", Value: solver.Value{Kind: analysis.TypeInteger, Integer: "-1"}}}},
	}
	oracle := productionStaticOracle{}
	bound, observations, err := oracle.Bind(context.Background(), program, symbol, vectors)
	if err != nil || len(observations) != 2 || len(bound.Functions[0].OracleProofs) != 2 {
		t.Fatalf("observations=%+v proofs=%+v err=%v", observations, bound.Functions[0].OracleProofs, err)
	}
	if observations[0].Evidence[0].Expected.Integer != "1" || observations[1].Evidence[0].Expected.Integer != "0" {
		t.Fatalf("unexpected values: %+v", observations)
	}
	for index := range vectors {
		assertions, kind, deriveErr := assertion.Derive(bound, vectors[index], observations[index])
		if deriveErr != nil || kind != assertion.KindVerified || len(assertions) != 1 {
			t.Fatalf("derive %d=%+v kind=%s err=%v", index, assertions, kind, deriveErr)
		}
	}
}

func TestProductionStaticOracleRejectsAmbiguousOrUnsafeEvaluation(t *testing.T) {
	program := analysis.Program{Version: analysis.IRVersion, Digest: strings.Repeat("a", 64), Functions: []analysis.Function{{
		SymbolID: strings.Repeat("b", 64), ReturnType: analysis.Type{Kind: analysis.TypeInteger, BitWidth: 32, Signed: true},
		Parameters: []analysis.Parameter{{Name: "x", Type: analysis.Type{Kind: analysis.TypeInteger, BitWidth: 32, Signed: true}}},
		Excerpt:    analysis.SourceExcerpt{Digest: strings.Repeat("c", 64)}, Decision: analysis.Decision{Kind: analysis.DecisionSupported, Reason: analysis.ReasonNone},
		ReturnRules: []analysis.ReturnRule{{Result: analysis.ClosedExpression{Kind: analysis.ExpressionBinary, Operator: "/", Left: &analysis.ClosedExpression{Kind: analysis.ExpressionInteger, Value: "1"}, Right: &analysis.ClosedExpression{Kind: analysis.ExpressionParameter, Name: "x"}}}},
	}}}
	vector := solver.InputVector{ID: strings.Repeat("d", 64), Inputs: []solver.Input{{Name: "x", Value: solver.Value{Kind: analysis.TypeInteger, Integer: "0"}}}}
	if _, _, err := (productionStaticOracle{}).Bind(context.Background(), program, program.Functions[0].SymbolID, []solver.InputVector{vector}); err == nil {
		t.Fatal("division by zero produced an oracle proof")
	}
}

func TestProductionPipelineRendersExecutableCasesFromStaticOracle(t *testing.T) {
	_, target, analyzer, _ := productionPipelineFixture(t)
	analyzer.program.Functions[0].ReturnRules = []analysis.ReturnRule{
		{Conditions: []analysis.ClosedExpression{{Kind: analysis.ExpressionBinary, Operator: "<", Left: &analysis.ClosedExpression{Kind: analysis.ExpressionParameter, Name: "x"}, Right: &analysis.ClosedExpression{Kind: analysis.ExpressionInteger, Value: "3"}}}, Result: analysis.ClosedExpression{Kind: analysis.ExpressionInteger, Value: "1"}},
		{Conditions: []analysis.ClosedExpression{{Kind: analysis.ExpressionUnary, Operator: "!", Left: &analysis.ClosedExpression{Kind: analysis.ExpressionBinary, Operator: "<", Left: &analysis.ClosedExpression{Kind: analysis.ExpressionParameter, Name: "x"}, Right: &analysis.ClosedExpression{Kind: analysis.ExpressionInteger, Value: "3"}}}}, Result: analysis.ClosedExpression{Kind: analysis.ExpressionInteger, Value: "0"}},
	}
	pipeline := newProductionGenerationPipeline(analyzer, productionStaticOracle{})
	result, err := pipeline.Generate(context.Background(), target)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.functions) != 1 || len(result.functions[0].cases) == 0 || len(result.editSet.Files) != 2 {
		t.Fatalf("pipeline result=%+v", result)
	}
	for _, observation := range result.observations {
		if len(observation.Evidence) != 1 || observation.Evidence[0].Expected.Integer != "1" {
			t.Fatalf("observation=%+v", observation)
		}
	}
}
