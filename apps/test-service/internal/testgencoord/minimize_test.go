package testgencoord

import (
	"reflect"
	"testing"

	"unit-test-ide.local/test-service/internal/testgendomain"
)

func TestMinimizePreservesBranchAndPrefersIndependentAssertion(t *testing.T) {
	cs := []ValidatedCandidate{
		{CaseID: "b", Kind: testgendomain.KindCharacterization, Coverage: Coverage{Lines: []string{"l1"}, Branches: []string{"br1"}}, AssertionDigest: "observed", Complexity: 1},
		{CaseID: "a", Kind: testgendomain.KindVerified, Coverage: Coverage{Lines: []string{"l1"}, Branches: []string{"br1"}}, AssertionDigest: "independent", Complexity: 4},
		{CaseID: "c", Kind: testgendomain.KindVerified, Coverage: Coverage{Branches: []string{"br2"}}, AssertionDigest: "proof", Complexity: 1},
	}
	got := Minimize(Coverage{}, cs)
	if ids := candidateIDs(got); !reflect.DeepEqual(ids, []string{"a", "c"}) {
		t.Fatalf("ids=%v", ids)
	}
}

func TestMinimizeOverlapTieAndReorderStability(t *testing.T) {
	cs := []ValidatedCandidate{
		{CaseID: "z", Kind: testgendomain.KindVerified, Coverage: Coverage{Functions: []string{"f1"}, Lines: []string{"l1"}}, AssertionDigest: "p1", Complexity: 2},
		{CaseID: "a", Kind: testgendomain.KindVerified, Coverage: Coverage{Functions: []string{"f1"}, Lines: []string{"l1"}}, AssertionDigest: "p2", Complexity: 2},
		{CaseID: "b", Kind: testgendomain.KindVerified, Coverage: Coverage{Lines: []string{"l2"}}, AssertionDigest: "p3", Complexity: 1},
	}
	for i := 0; i < 100; i++ {
		rotated := append(append([]ValidatedCandidate{}, cs[i%len(cs):]...), cs[:i%len(cs)]...)
		if ids := candidateIDs(Minimize(Coverage{}, rotated)); !reflect.DeepEqual(ids, []string{"a", "b"}) {
			t.Fatalf("seed=%d ids=%v", i, ids)
		}
	}
}

func TestMinimizeKeepsLockedAndErrorPath(t *testing.T) {
	cs := []ValidatedCandidate{
		{CaseID: "a", Kind: testgendomain.KindVerified, Coverage: Coverage{Lines: []string{"l1"}}, AssertionDigest: "p1"},
		{CaseID: "b", Kind: testgendomain.KindVerified, Coverage: Coverage{Lines: []string{"l1"}}, AssertionDigest: "p2", Locked: true},
		{CaseID: "c", Kind: testgendomain.KindVerified, Coverage: Coverage{Lines: []string{"l1"}}, AssertionDigest: "p3", IndependentErrorPath: true},
	}
	if ids := candidateIDs(Minimize(Coverage{}, cs)); !reflect.DeepEqual(ids, []string{"b", "c"}) {
		t.Fatalf("ids=%v", ids)
	}
}

func TestMinimizeLockedBaselineOnlyDoesNotHideNewBranch(t *testing.T) {
	cs := []ValidatedCandidate{
		{CaseID: "locked", Kind: testgendomain.KindVerified, Coverage: Coverage{Lines: []string{"old1", "old2"}}, AssertionDigest: "p1", Locked: true},
		{CaseID: "branch", Kind: testgendomain.KindVerified, Coverage: Coverage{Branches: []string{"new"}}, AssertionDigest: "p2"},
	}
	if ids := candidateIDs(Minimize(Coverage{Lines: []string{"old1", "old2"}}, cs)); !reflect.DeepEqual(ids, []string{"branch", "locked"}) {
		t.Fatalf("ids=%v", ids)
	}
}

func TestMinimizeRejectsDuplicateCaseIdentityInsteadOfDependingOnInputOrder(t *testing.T) {
	a := ValidatedCandidate{CaseID: "same", Kind: testgendomain.KindVerified, Coverage: Coverage{Lines: []string{"l1"}}, AssertionDigest: "proof1"}
	b := ValidatedCandidate{CaseID: "same", Kind: testgendomain.KindVerified, Coverage: Coverage{Branches: []string{"b1"}}, AssertionDigest: "proof2"}
	if got := Minimize(Coverage{}, []ValidatedCandidate{a, b}); len(got) != 0 {
		t.Fatalf("duplicate identity retained: %+v", got)
	}
	if got := Minimize(Coverage{}, []ValidatedCandidate{b, a}); len(got) != 0 {
		t.Fatalf("reordered duplicate retained: %+v", got)
	}
}

func candidateIDs(cs []ValidatedCandidate) []string {
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = c.CaseID
	}
	return out
}
