package testgencoord

import (
	"reflect"
	"strings"
	"testing"

	"unit-test-ide.local/test-service/internal/testgendomain"
)

func TestMinimizePreservesBranchAndPrefersIndependentAssertion(t *testing.T) {
	cs := []ValidatedCandidate{
		{CaseID: minCase("b"), Kind: testgendomain.KindCharacterization, Coverage: Coverage{Lines: []string{"l1"}, Branches: []string{"br1"}}, AssertionDigest: minProof("b"), Complexity: 1},
		{CaseID: minCase("a"), Kind: testgendomain.KindVerified, Coverage: Coverage{Lines: []string{"l1"}, Branches: []string{"br1"}}, AssertionDigest: minProof("a"), Complexity: 4},
		{CaseID: minCase("c"), Kind: testgendomain.KindVerified, Coverage: Coverage{Branches: []string{"br2"}}, AssertionDigest: minProof("c"), Complexity: 1},
	}
	got := Minimize(Coverage{}, cs)
	if ids := candidateIDs(got); !reflect.DeepEqual(ids, []string{minCase("a"), minCase("c")}) {
		t.Fatalf("ids=%v", ids)
	}
}

func TestMinimizeOverlapTieAndReorderStability(t *testing.T) {
	cs := []ValidatedCandidate{
		{CaseID: minCase("d"), Kind: testgendomain.KindVerified, Coverage: Coverage{Functions: []string{"f1"}, Lines: []string{"l1"}}, AssertionDigest: minProof("1"), Complexity: 2},
		{CaseID: minCase("a"), Kind: testgendomain.KindVerified, Coverage: Coverage{Functions: []string{"f1"}, Lines: []string{"l1"}}, AssertionDigest: minProof("2"), Complexity: 2},
		{CaseID: minCase("b"), Kind: testgendomain.KindVerified, Coverage: Coverage{Lines: []string{"l2"}}, AssertionDigest: minProof("3"), Complexity: 1},
	}
	for i := 0; i < 100; i++ {
		rotated := append(append([]ValidatedCandidate{}, cs[i%len(cs):]...), cs[:i%len(cs)]...)
		if ids := candidateIDs(Minimize(Coverage{}, rotated)); !reflect.DeepEqual(ids, []string{minCase("a"), minCase("b")}) {
			t.Fatalf("seed=%d ids=%v", i, ids)
		}
	}
}

func TestMinimizeKeepsLockedAndErrorPath(t *testing.T) {
	cs := []ValidatedCandidate{
		{CaseID: minCase("a"), Kind: testgendomain.KindVerified, Coverage: Coverage{Lines: []string{"l1"}}, AssertionDigest: minProof("1")},
		{CaseID: minCase("b"), Kind: testgendomain.KindVerified, Coverage: Coverage{Lines: []string{"l1"}}, AssertionDigest: minProof("2"), Locked: true},
		{CaseID: minCase("c"), Kind: testgendomain.KindVerified, Coverage: Coverage{Lines: []string{"l1"}}, AssertionDigest: minProof("3"), IndependentErrorPath: true},
	}
	if ids := candidateIDs(Minimize(Coverage{}, cs)); !reflect.DeepEqual(ids, []string{minCase("b"), minCase("c")}) {
		t.Fatalf("ids=%v", ids)
	}
}

func TestMinimizeLockedBaselineOnlyDoesNotHideNewBranch(t *testing.T) {
	cs := []ValidatedCandidate{
		{CaseID: minCase("d"), Kind: testgendomain.KindVerified, Coverage: Coverage{Lines: []string{"old1", "old2"}}, AssertionDigest: minProof("1"), Locked: true},
		{CaseID: minCase("b"), Kind: testgendomain.KindVerified, Coverage: Coverage{Branches: []string{"new"}}, AssertionDigest: minProof("2")},
	}
	if ids := candidateIDs(Minimize(Coverage{Lines: []string{"old1", "old2"}}, cs)); !reflect.DeepEqual(ids, []string{minCase("b"), minCase("d")}) {
		t.Fatalf("ids=%v", ids)
	}
}

func TestMinimizeRejectsDuplicateCaseIdentityInsteadOfDependingOnInputOrder(t *testing.T) {
	a := ValidatedCandidate{CaseID: minCase("a"), Kind: testgendomain.KindVerified, Coverage: Coverage{Lines: []string{"l1"}}, AssertionDigest: minProof("1")}
	b := ValidatedCandidate{CaseID: minCase("a"), Kind: testgendomain.KindVerified, Coverage: Coverage{Branches: []string{"b1"}}, AssertionDigest: minProof("2")}
	if got := Minimize(Coverage{}, []ValidatedCandidate{a, b}); len(got) != 0 {
		t.Fatalf("duplicate identity retained: %+v", got)
	}
	if got := Minimize(Coverage{}, []ValidatedCandidate{b, a}); len(got) != 0 {
		t.Fatalf("reordered duplicate retained: %+v", got)
	}
}

func TestMinimizeRejectsNonDurableIdentity(t *testing.T) {
	invalid := ValidatedCandidate{CaseID: "a", Kind: testgendomain.KindVerified, Coverage: Coverage{Lines: []string{"l1"}}, AssertionDigest: "proof"}
	if got := Minimize(Coverage{}, []ValidatedCandidate{invalid}); len(got) != 0 {
		t.Fatalf("non-durable identity retained: %+v", got)
	}
}

func minCase(c string) string  { return strings.Repeat(c, 32) }
func minProof(c string) string { return strings.Repeat(c, 64) }

func candidateIDs(cs []ValidatedCandidate) []string {
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = c.CaseID
	}
	return out
}
