// Package testgensolver enumerates finite, proven inputs for safe analysis IR.
// It never reads source, invokes a compiler, or accepts executable metadata.
package testgensolver

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"time"

	analysis "unit-test-ide.local/test-service/internal/testgenanalysis"
)

const SolverVersion = "bounded-v1"

type GapKind string
type Outcome string
type DiagnosticCode string

const (
	GapFunction GapKind = "function"
	GapBranch   GapKind = "branch"

	OutcomeTrue      Outcome = "true"
	OutcomeFalse     Outcome = "false"
	OutcomeCase      Outcome = "case"
	OutcomeDefault   Outcome = "default"
	OutcomeLoopEntry Outcome = "loop-entry"
	OutcomeLoopExit  Outcome = "loop-exit"

	DiagnosticUnsupported    DiagnosticCode = "unsupported"
	DiagnosticUnsatisfiable  DiagnosticCode = "unsatisfiable"
	DiagnosticBudgetExceeded DiagnosticCode = "budget-exceeded"
	DiagnosticCancelled      DiagnosticCode = "cancelled"
	DiagnosticNoProgress     DiagnosticCode = "no-progress"
)

type CoverageGap struct {
	Kind            GapKind `json:"kind"`
	SymbolID        string  `json:"symbolId"`
	BranchID        string  `json:"branchId,omitempty"`
	Outcome         Outcome `json:"outcome,omitempty"`
	CompileSnapshot string  `json:"compileSnapshot"`
	AnalyzerVersion string  `json:"analyzerVersion"`
}

type Budget struct {
	WallTime       time.Duration
	CandidateLimit int
	MemoryBytes    int64
	Concurrency    int
}

type Diagnostic struct {
	Code DiagnosticCode `json:"code"`
}

type Input struct {
	Name  string `json:"name"`
	Value Value  `json:"value"`
}

type InputVector struct {
	ID     string  `json:"id"`
	Inputs []Input `json:"inputs"`
}

type Solver struct{}

// Solve produces a stable, finite set. A closed diagnostic means no candidate
// was proven; a budget diagnostic may accompany a bounded partial set.
func (Solver) Solve(parent context.Context, program analysis.Program, gap CoverageGap, budget Budget) ([]InputVector, []Diagnostic, error) {
	if parent == nil {
		return nil, []Diagnostic{{DiagnosticUnsupported}}, nil
	}
	if err := parent.Err(); err != nil {
		return nil, []Diagnostic{{DiagnosticCancelled}}, err
	}
	if budget.WallTime <= 0 || budget.WallTime > 24*time.Hour || budget.CandidateLimit <= 0 || budget.CandidateLimit > 1000 || budget.MemoryBytes < 1024 || budget.MemoryBytes > 1<<30 || budget.Concurrency < 1 || budget.Concurrency > 16 {
		return nil, []Diagnostic{{DiagnosticBudgetExceeded}}, nil
	}
	ctx, cancel := context.WithTimeout(parent, budget.WallTime)
	defer cancel()
	if program.Version != analysis.IRVersion || !digest(gap.SymbolID) || !digest(gap.CompileSnapshot) || !digest(program.Digest) || !version(gap.AnalyzerVersion) || gap.Kind != GapFunction && gap.Kind != GapBranch {
		return nil, []Diagnostic{{DiagnosticUnsupported}}, nil
	}
	var target *analysis.Function
	for i := range program.Functions {
		if program.Functions[i].SymbolID == gap.SymbolID {
			if target != nil {
				return nil, []Diagnostic{{DiagnosticUnsupported}}, nil
			}
			target = &program.Functions[i]
		}
	}
	if target == nil || target.Decision.Kind != analysis.DecisionSupported || target.Decision.Reason != analysis.ReasonNone || len(target.Parameters) > 8 {
		return nil, []Diagnostic{{DiagnosticUnsupported}}, nil
	}
	var goal goalPredicate
	if gap.Kind == GapFunction {
		if gap.BranchID != "" || gap.Outcome != "" {
			return nil, []Diagnostic{{DiagnosticUnsupported}}, nil
		}
	} else {
		if !digest(gap.BranchID) {
			return nil, []Diagnostic{{DiagnosticUnsupported}}, nil
		}
		var branch *analysis.Branch
		for i := range target.Branches {
			if target.Branches[i].LocationDigest == gap.BranchID {
				if branch != nil {
					return nil, []Diagnostic{{DiagnosticUnsupported}}, nil
				}
				branch = &target.Branches[i]
			}
		}
		if branch == nil {
			return nil, []Diagnostic{{DiagnosticUnsupported}}, nil
		}
		var err error
		goal, err = normalizeGoal(*branch, target.Branches, gap.Outcome)
		if err != nil {
			return nil, []Diagnostic{{DiagnosticUnsupported}}, nil
		}
	}
	parameters := append([]analysis.Parameter(nil), target.Parameters...)
	seenNames := map[string]bool{}
	for _, p := range parameters {
		if !identifier(p.Name) || seenNames[p.Name] {
			return nil, []Diagnostic{{DiagnosticUnsupported}}, nil
		}
		seenNames[p.Name] = true
	}
	if !goal.validReferences(parameters) || !goal.validArithmetic(parameters) {
		return nil, []Diagnostic{{DiagnosticUnsupported}}, nil
	}
	// ABI and ownership proof come from structured IR, never spelling guesses.
	domains := make([][]Value, len(parameters))
	meter := &domainMeter{ctx: ctx, limit: budget.MemoryBytes}
	for i, p := range parameters {
		values, err := finiteDomain(meter, p.Type, goal.constants(p.Name), 0)
		if err != nil {
			if parent.Err() != nil {
				return nil, []Diagnostic{{DiagnosticCancelled}}, parent.Err()
			}
			if errors.Is(err, errDomainBudget) || errors.Is(err, context.DeadlineExceeded) {
				return nil, []Diagnostic{{DiagnosticBudgetExceeded}}, nil
			}
			return nil, []Diagnostic{{DiagnosticUnsupported}}, nil
		}
		if len(values) == 0 {
			return nil, []Diagnostic{{DiagnosticUnsupported}}, nil
		}
		domains[i] = values
	}
	// Bounded search independently of user candidate cap. This also bounds
	// memory used by candidate partitions and prevents combinatorial stalls.
	product := 1
	for _, values := range domains {
		if product > 65536/len(values) {
			return nil, []Diagnostic{{DiagnosticBudgetExceeded}}, nil
		}
		product *= len(values)
	}
	if int64(product)*512 > budget.MemoryBytes-meter.used {
		return nil, []Diagnostic{{DiagnosticBudgetExceeded}}, nil
	}
	selected := make([]Input, len(parameters))
	for i, p := range parameters {
		selected[i].Name = p.Name
	}
	result := make([]InputVector, 0, min(product, budget.CandidateLimit))
	seen := map[string]bool{}
	var retainedBytes int64
	steps := 0
	var stopCode DiagnosticCode
	var search func(int)
	search = func(index int) {
		if stopCode != "" {
			return
		}
		if err := ctx.Err(); err != nil {
			if errors.Is(parent.Err(), context.Canceled) {
				stopCode = DiagnosticCancelled
			} else {
				stopCode = DiagnosticBudgetExceeded
			}
			return
		}
		if index == len(domains) {
			steps++
			if !goal.accept(selected) {
				return
			}
			inputs := append([]Input(nil), selected...)
			encoded, err := json.Marshal(inputs)
			if err != nil || int64(len(encoded)) > budget.MemoryBytes-meter.used-retainedBytes {
				stopCode = DiagnosticBudgetExceeded
				return
			}
			id := candidateID(gap, inputs)
			if seen[id] {
				return
			}
			if len(result) == budget.CandidateLimit {
				stopCode = DiagnosticBudgetExceeded
				return
			}
			seen[id] = true
			retainedBytes += int64(len(encoded))
			result = append(result, InputVector{ID: id, Inputs: inputs})
			return
		}
		for _, value := range domains[index] {
			selected[index].Value = value
			search(index + 1)
			if stopCode != "" {
				break
			}
		}
	}
	search(0)
	if stopCode == DiagnosticCancelled {
		return nil, []Diagnostic{{stopCode}}, parent.Err()
	}
	sort.Slice(result, func(i, j int) bool {
		a, _ := json.Marshal(result[i].Inputs)
		b, _ := json.Marshal(result[j].Inputs)
		return string(a) < string(b)
	})
	if stopCode != "" {
		return result, []Diagnostic{{stopCode}}, nil
	}
	if len(result) == 0 {
		if steps == 0 {
			return nil, []Diagnostic{{DiagnosticNoProgress}}, nil
		}
		return nil, []Diagnostic{{DiagnosticUnsatisfiable}}, nil
	}
	return result, nil, nil
}
