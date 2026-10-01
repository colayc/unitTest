package testgencoord

import (
	"context"
	"reflect"
	"testing"
	"time"

	"unit-test-ide.local/test-service/internal/testgendomain"
)

func FuzzMinimizeReordering(f *testing.F) {
	f.Add(uint8(0), uint8(0), uint8(0))
	f.Add(uint8(1), uint8(2), uint8(3))
	f.Fuzz(func(t *testing.T, a, b, c uint8) {
		cs := []ValidatedCandidate{
			{CaseID: minCase("a"), Kind: testgendomain.KindVerified, Coverage: Coverage{Functions: []string{"f1"}, Lines: []string{"l1"}}, AssertionDigest: minProof("1"), Complexity: int64(a % 8)},
			{CaseID: minCase("b"), Kind: testgendomain.KindCharacterization, Coverage: Coverage{Lines: []string{"l1"}, Branches: []string{"b1"}}, AssertionDigest: minProof("2"), Complexity: int64(b % 8)},
			{CaseID: minCase("c"), Kind: testgendomain.KindVerified, Coverage: Coverage{Branches: []string{"b1"}}, AssertionDigest: minProof("3"), Complexity: int64(c % 8)},
		}
		want := candidateIDs(Minimize(Coverage{}, cs))
		for shift := 1; shift < len(cs); shift++ {
			rotated := append(append([]ValidatedCandidate{}, cs[shift:]...), cs[:shift]...)
			if got := candidateIDs(Minimize(Coverage{}, rotated)); !reflect.DeepEqual(got, want) {
				t.Fatalf("reorder=%v want=%v", got, want)
			}
		}
	})
}

func FuzzBudgetReservationBounded(f *testing.F) {
	f.Add(uint8(1), uint8(1))
	f.Add(uint8(255), uint8(0))
	f.Fuzz(func(t *testing.T, requested, repeat uint8) {
		start := time.Unix(100, 0)
		ledger, err := NewBudgetLedger(BudgetLimits{WallTime: time.Second, Candidates: 8, MemoryMiB: 64, Processes: 2, OutputBytes: 16, Events: 8, Artifacts: 8}, start, func() time.Time { return start })
		if err != nil {
			t.Fatal(err)
		}
		amount := BudgetAmount{OutputBytes: int64(requested), Processes: int64(repeat % 3)}
		r, err := ledger.Reserve(context.Background(), amount)
		if err == nil {
			r.Release()
		}
		if got := ledger.Reserved(); got != (BudgetAmount{}) {
			t.Fatalf("leaked %+v", got)
		}
	})
}
