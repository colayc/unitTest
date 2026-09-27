package testgencoord

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestBudgetExactBoundaryAndRelease(t *testing.T) {
	start := time.Unix(100, 0)
	ledger, err := NewBudgetLedger(BudgetLimits{WallTime: time.Second, Candidates: 1, MemoryMiB: 64, Processes: 2, OutputBytes: 8, Events: 1, Artifacts: 1}, start, func() time.Time { return start })
	if err != nil {
		t.Fatal(err)
	}
	all := BudgetAmount{WallTime: time.Second, Candidates: 1, MemoryMiB: 64, Processes: 2, OutputBytes: 8, Events: 1, Artifacts: 1}
	r, err := ledger.Reserve(context.Background(), all)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ledger.Reserve(context.Background(), BudgetAmount{Processes: 1}); !errors.Is(err, ErrBudgetExceeded) {
		t.Fatalf("nested process overrun: %v", err)
	}
	r.Release()
	r.Release()
	if got := ledger.Reserved(); got != (BudgetAmount{}) {
		t.Fatalf("leaked or double released: %+v", got)
	}
	if _, err := ledger.Reserve(context.Background(), all); err != nil {
		t.Fatalf("exact budget after cleanup: %v", err)
	}
}

func TestBudgetRaceHasOneWinner(t *testing.T) {
	start := time.Unix(100, 0)
	ledger, err := NewBudgetLedger(BudgetLimits{WallTime: time.Second, Candidates: 1, MemoryMiB: 64, Processes: 1, OutputBytes: 8, Events: 1, Artifacts: 1}, start, func() time.Time { return start })
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	results := make(chan error, 20)
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, e := ledger.Reserve(context.Background(), BudgetAmount{Processes: 1})
			results <- e
		}()
	}
	wg.Wait()
	close(results)
	winners := 0
	for e := range results {
		if e == nil {
			winners++
		} else if !errors.Is(e, ErrBudgetExceeded) {
			t.Fatal(e)
		}
	}
	if winners != 1 {
		t.Fatalf("winners=%d", winners)
	}
}

func TestBudgetCancellationTimeoutAndOverflow(t *testing.T) {
	start := time.Unix(100, 0)
	now := start
	ledger, err := NewBudgetLedger(BudgetLimits{WallTime: time.Second, Candidates: 1, MemoryMiB: 64, Processes: 1, OutputBytes: 8, Events: 1, Artifacts: 1}, start, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := ledger.Reserve(ctx, BudgetAmount{Processes: 1}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel precedence: %v", err)
	}
	now = start.Add(time.Second)
	if _, err := ledger.Reserve(context.Background(), BudgetAmount{Processes: 1}); !errors.Is(err, ErrBudgetTimeout) {
		t.Fatalf("timeout: %v", err)
	}
	if got := ledger.Reserved(); got != (BudgetAmount{}) {
		t.Fatalf("error leaked reservations: %+v", got)
	}
}

func TestBudgetExhaustionMapsToClosedTerminalReason(t *testing.T) {
	for _, amount := range []BudgetAmount{{OutputBytes: 9}, {Events: 2}, {Artifacts: 2}, {Candidates: 2}} {
		start := time.Unix(100, 0)
		ledger, err := NewBudgetLedger(BudgetLimits{WallTime: time.Second, Candidates: 1, MemoryMiB: 64, Processes: 1, OutputBytes: 8, Events: 1, Artifacts: 1}, start, func() time.Time { return start })
		if err != nil {
			t.Fatal(err)
		}
		if _, err := ledger.Reserve(context.Background(), amount); !errors.Is(err, ErrBudgetExceeded) || BudgetTerminalReason(err) != TerminalBudgetExceeded {
			t.Fatalf("amount %+v: %v", amount, err)
		}
	}
}

func TestBudgetCommittedUsageSurvivesRebuild(t *testing.T) {
	start := time.Unix(100, 0)
	limits := BudgetLimits{WallTime: time.Second, Candidates: 2, MemoryMiB: 64, Processes: 1, OutputBytes: 8, Events: 2, Artifacts: 2}
	ledger, err := NewBudgetLedger(limits, start, func() time.Time { return start })
	if err != nil {
		t.Fatal(err)
	}
	r, err := ledger.Reserve(context.Background(), BudgetAmount{Candidates: 1, Processes: 1, OutputBytes: 5, Events: 1, Artifacts: 1})
	if err != nil {
		t.Fatal(err)
	}
	r.Commit()
	r.Release()
	used := ledger.Usage()
	if used.Candidates != 1 || used.OutputBytes != 5 || used.Events != 1 || used.Artifacts != 1 || used.Processes != 0 {
		t.Fatalf("usage=%+v", used)
	}
	rebuilt, err := NewBudgetLedgerFromUsage(limits, start, func() time.Time { return start }, used)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rebuilt.Reserve(context.Background(), BudgetAmount{OutputBytes: 4}); !errors.Is(err, ErrBudgetExceeded) {
		t.Fatalf("lost output accounting: %v", err)
	}
	if _, err := rebuilt.Reserve(context.Background(), BudgetAmount{Candidates: 1, Processes: 1, OutputBytes: 3, Events: 1, Artifacts: 1}); err != nil {
		t.Fatalf("exact remaining: %v", err)
	}
}

func TestBudgetRejectsCounterOverflowAndNegativeReservation(t *testing.T) {
	start := time.Unix(100, 0)
	ledger, err := NewBudgetLedger(BudgetLimits{WallTime: time.Second, Candidates: 1, MemoryMiB: 64, Processes: 1, OutputBytes: 8, Events: 1, Artifacts: 1}, start, func() time.Time { return start })
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ledger.Reserve(context.Background(), BudgetAmount{OutputBytes: -1}); !errors.Is(err, ErrBudgetInvalid) {
		t.Fatalf("negative: %v", err)
	}
	if _, err := ledger.Reserve(context.Background(), BudgetAmount{OutputBytes: int64(^uint64(0) >> 1)}); !errors.Is(err, ErrBudgetExceeded) {
		t.Fatalf("overflow: %v", err)
	}
	if got := ledger.Reserved(); got != (BudgetAmount{}) {
		t.Fatalf("overflow mutated ledger: %+v", got)
	}
}
