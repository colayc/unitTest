package testgensolver

import (
	"context"
	"testing"

	analysis "unit-test-ide.local/test-service/internal/testgenanalysis"
)

func entryProgram(types ...analysis.Type) analysis.Program {
	parameters := make([]analysis.Parameter, len(types))
	for i, typ := range types {
		parameters[i] = analysis.Parameter{Name: string(rune('a' + i)), Type: typ}
	}
	return analysis.Program{Version: analysis.IRVersion, Digest: identity, Functions: []analysis.Function{{SymbolID: identity, Name: "entry", Parameters: parameters, Decision: analysis.Decision{Kind: analysis.DecisionSupported, Reason: analysis.ReasonNone}}}}
}

func entryGap() CoverageGap {
	return CoverageGap{Kind: GapFunction, SymbolID: identity, CompileSnapshot: identity, AnalyzerVersion: "clang-ir-v1"}
}

func TestEntryEnumeratesSignedAndUnsignedBoundariesWithoutOverflow(t *testing.T) {
	for _, tc := range []struct {
		typeValue analysis.Type
		want      []string
	}{
		{analysis.Type{Kind: analysis.TypeInteger, BitWidth: 8, Signed: true}, []string{"-128", "-127", "-1", "0", "1", "126", "127"}},
		{analysis.Type{Kind: analysis.TypeInteger, BitWidth: 8, Signed: false}, []string{"0", "1", "254", "255"}},
	} {
		vectors, diagnostics, err := (Solver{}).Solve(context.Background(), entryProgram(tc.typeValue), entryGap(), standardBudget())
		if err != nil || len(diagnostics) != 0 {
			t.Fatalf("solve: %v %v", diagnostics, err)
		}
		seen := map[string]bool{}
		for _, v := range vectors {
			seen[v.Inputs[0].Value.Integer] = true
		}
		for _, value := range tc.want {
			if !seen[value] {
				t.Fatalf("missing %s from %v", value, seen)
			}
		}
	}
}

func TestEntryRequiresProvenFiniteComplexDomains(t *testing.T) {
	for _, tc := range []struct {
		name string
		typ  analysis.Type
		kind analysis.TypeKind
	}{
		{"boolean", analysis.Type{Kind: analysis.TypeBoolean}, analysis.TypeBoolean},
		{"enum", analysis.Type{Kind: analysis.TypeEnum, EnumValues: []string{"red", "blue"}}, analysis.TypeEnum},
		{"finite float", analysis.Type{Kind: analysis.TypeFloating, BitWidth: 64}, analysis.TypeFloating},
		{"owned pointer", analysis.Type{Kind: analysis.TypePointer, Owned: true, Element: &analysis.Type{Kind: analysis.TypeInteger, BitWidth: 8, Signed: true}}, analysis.TypePointer},
		{"bounded string", analysis.Type{Kind: analysis.TypeString, MaxLength: 2}, analysis.TypeString},
		{"bounded array", analysis.Type{Kind: analysis.TypeArray, Bound: 2, Element: &analysis.Type{Kind: analysis.TypeBoolean}}, analysis.TypeArray},
		{"bounded record", analysis.Type{Kind: analysis.TypeRecord, Proven: true, Fields: []analysis.Field{{Name: "ok", Type: analysis.Type{Kind: analysis.TypeBoolean}}}}, analysis.TypeRecord},
	} {
		t.Run(tc.name, func(t *testing.T) {
			vectors, diagnostics, err := (Solver{}).Solve(context.Background(), entryProgram(tc.typ), entryGap(), standardBudget())
			if err != nil || len(diagnostics) != 0 || len(vectors) == 0 {
				t.Fatalf("no finite domain: %v %v %v", vectors, diagnostics, err)
			}
			for _, v := range vectors {
				if v.Inputs[0].Value.Kind != tc.kind {
					t.Fatalf("wrong typed value: %+v", v.Inputs[0].Value)
				}
			}
		})
	}
	for _, typ := range []analysis.Type{
		{Kind: analysis.TypeInteger, BitWidth: 0},
		{Kind: analysis.TypeEnum},
		{Kind: analysis.TypePointer, Owned: false, Element: &analysis.Type{Kind: analysis.TypeInteger, BitWidth: 8, Signed: true}},
		{Kind: analysis.TypeArray, Bound: 2},
		{Kind: analysis.TypeRecord, Proven: true},
		{Kind: analysis.TypeString, MaxLength: 0},
	} {
		v, d, err := (Solver{}).Solve(context.Background(), entryProgram(typ), entryGap(), standardBudget())
		if err != nil || len(v) != 0 || len(d) != 1 || d[0].Code != DiagnosticUnsupported {
			t.Fatalf("unproven complex type must fail closed: %v %v %v", v, d, err)
		}
	}
}

func TestFiniteFloatNeverEmitsNaNOrInfinityWithoutProof(t *testing.T) {
	v, d, err := (Solver{}).Solve(context.Background(), entryProgram(analysis.Type{Kind: analysis.TypeFloating, BitWidth: 32}), entryGap(), standardBudget())
	if err != nil || len(d) != 0 || len(v) == 0 {
		t.Fatalf("solve: %v %v", d, err)
	}
	for _, candidate := range v {
		s := candidate.Inputs[0].Value.Float
		if s == "NaN" || s == "+Inf" || s == "-Inf" {
			t.Fatalf("non-finite candidate without proof: %s", s)
		}
	}
}
