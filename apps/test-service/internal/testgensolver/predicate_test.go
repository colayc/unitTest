package testgensolver

import (
	"context"
	"reflect"
	"strings"
	"testing"

	analysis "unit-test-ide.local/test-service/internal/testgenanalysis"
)

func TestSolveNestedBooleanLogicAndSwitchDefault(t *testing.T) {
	p := baseProgram(intType(), analysis.Predicate{Operator: "&&", Left: "(x>0)", Right: "(x<3)"})
	v, d, err := (Solver{}).Solve(context.Background(), p, standardGap(OutcomeTrue), standardBudget())
	if err != nil || len(d) != 0 || len(v) == 0 {
		t.Fatalf("nested goal: %v %v", d, err)
	}
	for _, candidate := range v {
		got := candidate.Inputs[0].Value.Integer
		if got != "1" && got != "2" {
			t.Fatalf("candidate does not satisfy conjunction: %s", got)
		}
	}
	p.Functions[0].Branches = []analysis.Branch{
		{Kind: analysis.BranchSwitch, Predicate: analysis.Predicate{Operator: "switch", Left: "x"}, LocationDigest: branchID},
		{Kind: analysis.BranchCase, Predicate: analysis.Predicate{Operator: "==", Left: "x", Right: "1"}, OwnerSwitchID: branchID, LocationDigest: strings.Repeat("c", 64), PathVerified: true},
		{Kind: analysis.BranchCase, Predicate: analysis.Predicate{Operator: "==", Left: "x", Right: "2"}, OwnerSwitchID: branchID, LocationDigest: strings.Repeat("d", 64), PathVerified: true},
		{Kind: analysis.BranchDefault, Predicate: analysis.Predicate{Operator: "default", Left: "x"}, OwnerSwitchID: branchID, LocationDigest: strings.Repeat("e", 64), PathVerified: true},
	}
	gap := standardGap(OutcomeCase)
	gap.BranchID = strings.Repeat("d", 64)
	v, d, err = (Solver{}).Solve(context.Background(), p, gap, standardBudget())
	if err != nil || len(d) != 0 || len(v) == 0 || v[0].Inputs[0].Value.Integer != "2" {
		t.Fatalf("switch case: %v %v %v", v, d, err)
	}
	gap.BranchID = strings.Repeat("e", 64)
	gap.Outcome = OutcomeDefault
	v, d, err = (Solver{}).Solve(context.Background(), p, gap, standardBudget())
	if err != nil || len(d) != 0 || len(v) == 0 {
		t.Fatalf("switch default: %v %v %v", v, d, err)
	}
	for _, candidate := range v {
		got := candidate.Inputs[0].Value.Integer
		if got == "1" || got == "2" {
			t.Fatalf("default duplicates case: %s", got)
		}
	}
}

func TestSolveVerifiedLoopEntryAndExit(t *testing.T) {
	p := baseProgram(intType(), analysis.Predicate{Operator: "<", Left: "x", Right: "3"})
	p.Functions[0].Branches[0].Kind = analysis.BranchLoop
	p.Functions[0].Branches[0].BoundVerified = true
	for _, outcome := range []Outcome{OutcomeLoopEntry, OutcomeLoopExit} {
		v, d, err := (Solver{}).Solve(context.Background(), p, standardGap(outcome), standardBudget())
		if err != nil || len(v) != 0 || len(d) != 1 || d[0].Code != DiagnosticUnsupported {
			t.Fatalf("loop %s lacks iteration proof: %v %v %v", outcome, v, d, err)
		}
	}
	p.Functions[0].Branches[0].BoundVerified = false
	v, d, err := (Solver{}).Solve(context.Background(), p, standardGap(OutcomeLoopEntry), standardBudget())
	if err != nil || len(v) != 0 || len(d) != 1 || d[0].Code != DiagnosticUnsupported {
		t.Fatalf("unbounded loop must close: %v %v %v", v, d, err)
	}
}

func TestDuplicateInputAndConcurrentCallsAreStable(t *testing.T) {
	p := baseProgram(intType(), analysis.Predicate{Operator: "==", Left: "x", Right: "0"})
	budget := standardBudget()
	budget.Concurrency = 2
	first, d, err := (Solver{}).Solve(context.Background(), p, standardGap(OutcomeTrue), budget)
	if err != nil || len(d) != 0 || len(first) != 1 {
		t.Fatalf("duplicate partitions not minimized: %v %v %v", first, d, err)
	}
	results := make(chan []InputVector, 8)
	for i := 0; i < 8; i++ {
		go func() {
			v, _, _ := (Solver{}).Solve(context.Background(), p, standardGap(OutcomeTrue), budget)
			results <- v
		}()
	}
	for i := 0; i < 8; i++ {
		if got := <-results; !reflect.DeepEqual(first, got) {
			t.Fatalf("concurrent solve changed result: %v vs %v", first, got)
		}
	}
}

func FuzzPredicateDecoder(f *testing.F) {
	for _, seed := range []string{"x<3", "(x>0)&&(x<3)", "!(x==1)", "x||y", "system(1)", strings.Repeat("(", 40)} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, input string) {
		if len(input) > 2048 {
			return
		}
		_, _ = parsePredicateExpression(input)
	})
}
