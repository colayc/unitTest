package testgensolver

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	analysis "unit-test-ide.local/test-service/internal/testgenanalysis"
)

const identity = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
const branchID = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

func baseProgram(t analysis.Type, p analysis.Predicate) analysis.Program {
	return analysis.Program{
		Version: analysis.IRVersion, Digest: identity,
		Functions: []analysis.Function{{
			SymbolID: identity, Name: "choose", Parameters: []analysis.Parameter{{Name: "x", Type: t}},
			Decision: analysis.Decision{Kind: analysis.DecisionSupported, Reason: analysis.ReasonNone},
			Branches: []analysis.Branch{{Kind: analysis.BranchIf, Predicate: p, LocationDigest: branchID}},
		}},
	}
}

func standardGap(outcome Outcome) CoverageGap {
	return CoverageGap{Kind: GapBranch, SymbolID: identity, BranchID: branchID, Outcome: outcome, CompileSnapshot: identity, AnalyzerVersion: "clang-ir-v1"}
}

func standardBudget() Budget {
	return Budget{WallTime: time.Second, CandidateLimit: 16, MemoryBytes: 1 << 20, Concurrency: 1}
}

func intType() analysis.Type {
	return analysis.Type{Kind: analysis.TypeInteger, Spelling: "int", BitWidth: 32, Signed: true}
}

func TestSolveComparisonOutcomesAndCanonicalIdentity(t *testing.T) {
	p := baseProgram(intType(), analysis.Predicate{Operator: "<", Left: "x", Right: "3"})
	solver := Solver{}
	for _, tc := range []struct {
		outcome Outcome
		want    string
	}{
		{OutcomeTrue, "2"}, {OutcomeFalse, "3"},
	} {
		vectors, diagnostics, err := solver.Solve(context.Background(), p, standardGap(tc.outcome), standardBudget())
		if err != nil || len(diagnostics) != 0 || len(vectors) == 0 {
			t.Fatalf("%s: vectors=%v diagnostics=%v err=%v", tc.outcome, vectors, diagnostics, err)
		}
		found := false
		for _, v := range vectors {
			if len(v.Inputs) != 1 || v.Inputs[0].Name != "x" || v.ID == "" || len(v.ID) != 64 {
				t.Fatalf("invalid typed vector: %+v", v)
			}
			if v.Inputs[0].Value.Integer == tc.want {
				found = true
			}
		}
		if !found {
			t.Fatalf("%s missing boundary %s: %+v", tc.outcome, tc.want, vectors)
		}
		again, _, err := solver.Solve(context.Background(), p, standardGap(tc.outcome), standardBudget())
		if err != nil || !reflect.DeepEqual(vectors, again) {
			t.Fatalf("nondeterministic vectors: %v vs %v, %v", vectors, again, err)
		}
	}
	gap := standardGap(OutcomeTrue)
	a, _, _ := solver.Solve(context.Background(), p, gap, standardBudget())
	gap.CompileSnapshot = strings.Repeat("c", 64)
	b, _, _ := solver.Solve(context.Background(), p, gap, standardBudget())
	if a[0].ID == b[0].ID {
		t.Fatal("compile snapshot omitted from candidate identity")
	}
}

func TestSolveRejectsUnprovenAndContradictoryGoals(t *testing.T) {
	p := baseProgram(intType(), analysis.Predicate{Operator: "==", Left: "x", Right: "7"})
	p.Functions[0].Decision.Kind = analysis.DecisionRequiresConfirmation
	v, d, err := (Solver{}).Solve(context.Background(), p, standardGap(OutcomeTrue), standardBudget())
	if err != nil || len(v) != 0 || len(d) != 1 || d[0].Code != DiagnosticUnsupported {
		t.Fatalf("uncertain target must fail closed: %v %v %v", v, d, err)
	}
	p.Functions[0].Decision.Kind = analysis.DecisionSupported
	p.Functions[0].Branches[0].Predicate = analysis.Predicate{Operator: "&&", Left: "(x<3)", Right: "(x>5)"}
	v, d, err = (Solver{}).Solve(context.Background(), p, standardGap(OutcomeTrue), standardBudget())
	if err != nil || len(v) != 0 || len(d) != 1 || d[0].Code != DiagnosticUnsatisfiable {
		t.Fatalf("contradiction must not generate input: %v %v %v", v, d, err)
	}
	for _, bad := range []analysis.Predicate{{Operator: "unknown"}, {Operator: "==", Left: "x", Right: "system(1)"}} {
		p.Functions[0].Branches[0].Predicate = bad
		v, d, err = (Solver{}).Solve(context.Background(), p, standardGap(OutcomeTrue), standardBudget())
		if err != nil || len(v) != 0 || len(d) != 1 || d[0].Code != DiagnosticUnsupported {
			t.Fatalf("malformed predicate must fail closed: %v %v %v", v, d, err)
		}
	}
	p.Functions[0].Branches[0].Predicate = analysis.Predicate{Operator: "==", Left: "x", Right: "NaN"}
	v, d, err = (Solver{}).Solve(context.Background(), p, standardGap(OutcomeTrue), standardBudget())
	if err != nil || len(v) != 0 || len(d) != 1 || d[0].Code != DiagnosticUnsupported {
		t.Fatalf("non-finite/unknown identifier must be unsupported: %v %v %v", v, d, err)
	}
}

func TestMemoryBudgetCountsActualSerializedCandidate(t *testing.T) {
	record := analysis.Type{Kind: analysis.TypeRecord, Proven: true, Fields: []analysis.Field{
		{Name: "first", Type: analysis.Type{Kind: analysis.TypeBoolean}},
		{Name: "second", Type: analysis.Type{Kind: analysis.TypeBoolean}},
		{Name: "third", Type: analysis.Type{Kind: analysis.TypeBoolean}},
		{Name: "fourth", Type: analysis.Type{Kind: analysis.TypeBoolean}},
	}}
	p := entryProgram(analysis.Type{Kind: analysis.TypeArray, Bound: 16, Element: &record})
	budget := standardBudget()
	budget.MemoryBytes = 2048
	v, d, err := (Solver{}).Solve(context.Background(), p, entryGap(), budget)
	if err != nil || len(v) != 0 || len(d) != 1 || d[0].Code != DiagnosticBudgetExceeded {
		t.Fatalf("large canonical vector must respect byte budget: %v %v %v", v, d, err)
	}
}

func TestSolveBudgetsCancellationAndNoProgress(t *testing.T) {
	p := baseProgram(intType(), analysis.Predicate{Operator: "==", Left: "x", Right: "1"})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	v, d, err := (Solver{}).Solve(ctx, p, standardGap(OutcomeTrue), standardBudget())
	if !errors.Is(err, context.Canceled) || len(v) != 0 || len(d) != 1 || d[0].Code != DiagnosticCancelled {
		t.Fatalf("cancel ignored: %v %v %v", v, d, err)
	}
	for _, budget := range []Budget{
		{WallTime: -time.Nanosecond, CandidateLimit: 8, MemoryBytes: 1 << 20, Concurrency: 1},
		{WallTime: time.Second, CandidateLimit: 0, MemoryBytes: 1 << 20, Concurrency: 1},
		{WallTime: time.Second, CandidateLimit: 8, MemoryBytes: 1, Concurrency: 1},
		{WallTime: time.Second, CandidateLimit: 8, MemoryBytes: 1 << 20, Concurrency: 0},
	} {
		v, d, err = (Solver{}).Solve(context.Background(), p, standardGap(OutcomeTrue), budget)
		if err != nil || len(v) != 0 || len(d) != 1 || d[0].Code != DiagnosticBudgetExceeded {
			t.Fatalf("invalid/tiny budget must close: %v %v %v", v, d, err)
		}
	}
	p.Functions[0].Branches[0].Predicate = analysis.Predicate{Operator: "==", Left: "x", Right: "9999999999999999999999999999"}
	v, d, err = (Solver{}).Solve(context.Background(), p, standardGap(OutcomeTrue), standardBudget())
	if err != nil || len(v) != 0 || len(d) != 1 || d[0].Code != DiagnosticUnsatisfiable {
		t.Fatalf("unreachable value must stop: %v %v %v", v, d, err)
	}
}
