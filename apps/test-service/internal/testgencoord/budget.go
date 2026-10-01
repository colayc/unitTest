package testgencoord

import (
	"context"
	"errors"
	"sync"
	"time"
)

var ErrBudgetExceeded = errors.New("test generation budget exceeded")
var ErrBudgetTimeout = errors.New("test generation wall time exhausted")
var ErrBudgetInvalid = errors.New("invalid test generation budget")

type TerminalReason string

const (
	TerminalBudgetExceeded TerminalReason = "budget-exceeded"
	TerminalTimedOut       TerminalReason = "timed-out"
	TerminalCancelled      TerminalReason = "cancelled"
)

func BudgetTerminalReason(err error) TerminalReason {
	switch {
	case errors.Is(err, context.Canceled):
		return TerminalCancelled
	case errors.Is(err, ErrBudgetTimeout), errors.Is(err, context.DeadlineExceeded):
		return TerminalTimedOut
	case errors.Is(err, ErrBudgetExceeded):
		return TerminalBudgetExceeded
	default:
		return ""
	}
}

// BudgetAmount is a closed, path-free reservation. All counters are checked
// before mutation; active reservations are shared across all run stages.
type BudgetAmount struct {
	WallTime                                                         time.Duration
	Candidates, MemoryMiB, Processes, OutputBytes, Events, Artifacts int64
}

type BudgetLimits BudgetAmount

type BudgetLedger struct {
	mu       sync.Mutex
	limits   BudgetAmount
	reserved BudgetAmount
	used     BudgetAmount
	started  time.Time
	now      func() time.Time
}

type BudgetReservation struct {
	ledger *BudgetLedger
	amount BudgetAmount
	ctx    context.Context
	done   bool
	result error
}

func NewBudgetLedger(limits BudgetLimits, started time.Time, now func() time.Time) (*BudgetLedger, error) {
	return NewBudgetLedgerFromUsage(limits, started, now, BudgetAmount{})
}

func NewBudgetLedgerFromUsage(limits BudgetLimits, started time.Time, now func() time.Time, used BudgetAmount) (*BudgetLedger, error) {
	l := BudgetAmount(limits)
	if started.IsZero() || l.WallTime <= 0 || l.Candidates <= 0 || l.MemoryMiB <= 0 || l.Processes <= 0 || l.OutputBytes <= 0 || l.Events <= 0 || l.Artifacts <= 0 || !nonnegative(used) || used.WallTime != 0 || used.MemoryMiB != 0 || used.Processes != 0 || !fits(BudgetAmount{}, used, l) {
		return nil, ErrBudgetInvalid
	}
	if now == nil {
		now = time.Now
	}
	return &BudgetLedger{limits: l, started: started, now: now, used: used}, nil
}

func nonnegative(a BudgetAmount) bool {
	return a.WallTime >= 0 && a.Candidates >= 0 && a.MemoryMiB >= 0 && a.Processes >= 0 && a.OutputBytes >= 0 && a.Events >= 0 && a.Artifacts >= 0
}

func fits(current, requested, limit BudgetAmount) bool {
	return requested.WallTime <= limit.WallTime-current.WallTime &&
		requested.Candidates <= limit.Candidates-current.Candidates &&
		requested.MemoryMiB <= limit.MemoryMiB-current.MemoryMiB &&
		requested.Processes <= limit.Processes-current.Processes &&
		requested.OutputBytes <= limit.OutputBytes-current.OutputBytes &&
		requested.Events <= limit.Events-current.Events &&
		requested.Artifacts <= limit.Artifacts-current.Artifacts
}

func (l *BudgetLedger) Reserve(ctx context.Context, amount BudgetAmount) (*BudgetReservation, error) {
	if l == nil || ctx == nil || !nonnegative(amount) {
		return nil, ErrBudgetInvalid
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	elapsed := l.now().Sub(l.started)
	if elapsed < 0 {
		return nil, ErrBudgetInvalid
	}
	if elapsed >= l.limits.WallTime || amount.WallTime > l.limits.WallTime-elapsed {
		return nil, ErrBudgetTimeout
	}
	current := l.reserved
	current.Candidates += l.used.Candidates
	current.OutputBytes += l.used.OutputBytes
	current.Events += l.used.Events
	current.Artifacts += l.used.Artifacts
	if !fits(current, amount, l.limits) {
		return nil, ErrBudgetExceeded
	}
	l.reserved.WallTime += amount.WallTime
	l.reserved.Candidates += amount.Candidates
	l.reserved.MemoryMiB += amount.MemoryMiB
	l.reserved.Processes += amount.Processes
	l.reserved.OutputBytes += amount.OutputBytes
	l.reserved.Events += amount.Events
	l.reserved.Artifacts += amount.Artifacts
	return &BudgetReservation{ledger: l, amount: amount, ctx: ctx}, nil
}

// StageContext binds the external stage process tree to the run's absolute
// deadline. The stage runner must pass this context into processcontrol and
// defer Release; Commit independently refuses elapsed work.
func (r *BudgetReservation) StageContext(parent context.Context) (context.Context, context.CancelFunc) {
	if r == nil || r.ledger == nil || parent == nil {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		return ctx, func() {}
	}
	l := r.ledger
	l.mu.Lock()
	done := r.done
	l.mu.Unlock()
	ctx, cancel := context.WithDeadline(parent, l.started.Add(l.limits.WallTime))
	if done {
		cancel()
		return ctx, func() {}
	}
	stop := context.AfterFunc(r.ctx, cancel)
	return ctx, func() { stop(); cancel() }
}

func (r *BudgetReservation) Release() {
	if r == nil || r.ledger == nil {
		return
	}
	l := r.ledger
	l.mu.Lock()
	defer l.mu.Unlock()
	if !r.done {
		r.done = true
		r.result = ErrBudgetInvalid
		l.reserved.WallTime -= r.amount.WallTime
		l.reserved.Candidates -= r.amount.Candidates
		l.reserved.MemoryMiB -= r.amount.MemoryMiB
		l.reserved.Processes -= r.amount.Processes
		l.reserved.OutputBytes -= r.amount.OutputBytes
		l.reserved.Events -= r.amount.Events
		l.reserved.Artifacts -= r.amount.Artifacts
	}
}

// Commit consumes count/output/event/artifact budgets permanently and frees
// active wall-time, memory and process reservations. It is idempotent.
func (r *BudgetReservation) Commit() error {
	if r == nil || r.ledger == nil {
		return ErrBudgetInvalid
	}
	l := r.ledger
	l.mu.Lock()
	defer l.mu.Unlock()
	if r.done {
		return r.result
	}
	if err := r.ctx.Err(); err != nil {
		r.releaseLocked(err)
		return err
	}
	if l.now().Sub(l.started) >= l.limits.WallTime {
		r.releaseLocked(ErrBudgetTimeout)
		return ErrBudgetTimeout
	}
	r.done = true
	l.reserved.WallTime -= r.amount.WallTime
	l.reserved.Candidates -= r.amount.Candidates
	l.reserved.MemoryMiB -= r.amount.MemoryMiB
	l.reserved.Processes -= r.amount.Processes
	l.reserved.OutputBytes -= r.amount.OutputBytes
	l.reserved.Events -= r.amount.Events
	l.reserved.Artifacts -= r.amount.Artifacts
	l.used.Candidates += r.amount.Candidates
	l.used.OutputBytes += r.amount.OutputBytes
	l.used.Events += r.amount.Events
	l.used.Artifacts += r.amount.Artifacts
	return nil
}

func (r *BudgetReservation) releaseLocked(result error) {
	l := r.ledger
	r.done = true
	r.result = result
	l.reserved.WallTime -= r.amount.WallTime
	l.reserved.Candidates -= r.amount.Candidates
	l.reserved.MemoryMiB -= r.amount.MemoryMiB
	l.reserved.Processes -= r.amount.Processes
	l.reserved.OutputBytes -= r.amount.OutputBytes
	l.reserved.Events -= r.amount.Events
	l.reserved.Artifacts -= r.amount.Artifacts
}

func (l *BudgetLedger) Usage() BudgetAmount {
	if l == nil {
		return BudgetAmount{}
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.used
}

func (l *BudgetLedger) Reserved() BudgetAmount {
	if l == nil {
		return BudgetAmount{}
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.reserved
}
