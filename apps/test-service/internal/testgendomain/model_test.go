package testgendomain

import (
	"strings"
	"testing"
	"time"
)

func candidateFixture() Candidate {
	return Candidate{CaseID: strings.Repeat("1", 32), Kind: KindVerified, TargetSymbol: "fn:classify",
		Assertions:           []Assertion{{Kind: AssertionIndependentOracle, EvidenceDigest: strings.Repeat("a", 64)}},
		StagedSourceArtifact: ArtifactRef{ID: strings.Repeat("2", 32), Digest: strings.Repeat("b", 64)},
		CodeDigest:           strings.Repeat("c", 64), CoverageDelta: Coverage{Functions: 1, Lines: 2, Branches: 1},
		PlannedEdits: []PlannedEdit{{Path: "tests/classify_test.c", Operation: EditCreate, AfterDigest: strings.Repeat("d", 64)}}}
}

func TestCandidateRejectsUnsafeAndDuplicateValues(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*Candidate)
	}{
		{"kind", func(c *Candidate) { c.Kind = "speculative" }},
		{"path", func(c *Candidate) { c.PlannedEdits[0].Path = "C:/secret.c" }},
		{"duplicate edit", func(c *Candidate) { c.PlannedEdits = append(c.PlannedEdits, c.PlannedEdits[0]) }},
		{"unproven verified", func(c *Candidate) { c.Assertions[0].Kind = AssertionObservedOutput }},
		{"bad digest", func(c *Candidate) { c.StagedSourceArtifact.Digest = "bad" }},
		{"bad coverage percent", func(c *Candidate) { c.BaselineCoverage.LinePercent = 101 }},
		{"bad diagnostic reason", func(c *Candidate) {
			c.Diagnostics = []Diagnostic{{Code: DiagnosticCoverageGap, Severity: SeverityWarning, Reason: "secret source"}}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := candidateFixture()
			tc.change(&c)
			if ValidateCandidate(c) == nil {
				t.Fatal("accepted invalid candidate")
			}
		})
	}
	if err := ValidateCandidate(candidateFixture()); err != nil {
		t.Fatal(err)
	}
	if err := ValidateCandidates([]Candidate{candidateFixture(), candidateFixture()}); err == nil {
		t.Fatal("accepted duplicate case IDs")
	}
}

func TestRunRejectsMismatchesAndImpossibleTransitions(t *testing.T) {
	r := Run{ID: strings.Repeat("1", 32), TaskID: strings.Repeat("2", 32), Request: validRequest(), State: StateQueued, Revision: 1, CreatedAt: time.Now().UTC()}
	if err := ValidateRun(r); err != nil {
		t.Fatal(err)
	}
	r.TaskID = r.ID
	if err := ValidateRun(r); err == nil {
		t.Fatal("accepted task/run mismatch")
	}
	if ValidTransition(StateQueued, StateValidating) {
		t.Fatal("skipped stages")
	}
	if !ValidTransition(StateQueued, StateBaseline) {
		t.Fatal("rejected next stage")
	}
	if ValidTransition(StateCancelled, StateBaseline) {
		t.Fatal("resurrected terminal run")
	}
	if !ValidTransition(StateValidating, StateRejected) || !ValidTransition(StateMinimizing, StateRejected) {
		t.Fatal("rejected an allowed no-candidate terminal transition")
	}
}
