package runtime

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	generationv16 "unit-test-ide.local/test-service/internal/protocolmodel/v1_6/testgeneration"
	"unit-test-ide.local/test-service/internal/task"
	"unit-test-ide.local/test-service/internal/taskstore"
	"unit-test-ide.local/test-service/internal/testgencoord"
	"unit-test-ide.local/test-service/internal/testgendomain"
)

type managedDispatchEntry struct {
	Run      testgendomain.Run
	Next     testgendomain.State
	Deadline time.Time
	Done     <-chan struct{}
	Err      error
}

type managedDispatchDriver struct {
	*generationDriverFixture
	provider        *ManagedRuntimeProvider
	entered         chan managedDispatchEntry
	releaseBaseline chan struct{}
	calls           atomic.Int64
}

func (d *managedDispatchDriver) RunStage(ctx context.Context, run testgendomain.Run, next testgendomain.State, authorize func(context.Context) error) (GenerationStageResult, error) {
	d.calls.Add(1)
	entry := managedDispatchEntry{Next: next}
	deadline, ok := ctx.Deadline()
	entry.Deadline = deadline
	if !ok {
		entry.Err = errors.New("stage lacks deadline")
	} else if err := authorize(ctx); err != nil {
		entry.Err = err
	} else {
		stored, err := d.provider.coord.Get(ctx, run.ID)
		entry.Run, entry.Err = stored, err
		if err == nil {
			item, readErr := d.provider.store.Get(ctx, run.TaskID)
			entry.Err = readErr
			if readErr == nil && (item.ID != stored.TaskID || stored.ID != run.ID || stored.Revision != run.Revision || !reflect.DeepEqual(stored.Request, run.Request)) {
				entry.Err = errors.New("stage entered without exact durable run/task")
			}
		}
	}
	d.provider.mu.Lock()
	execution := d.provider.running[run.ID]
	if execution != nil {
		entry.Done = execution.done
	}
	d.provider.mu.Unlock()
	if entry.Done == nil && entry.Err == nil {
		entry.Err = errors.New("stage lacks registered worker")
	}
	d.entered <- entry
	if entry.Err != nil {
		return GenerationStageResult{}, entry.Err
	}
	if next == testgendomain.StateBaseline {
		select {
		case <-d.releaseBaseline:
			return GenerationStageResult{Next: next}, nil
		case <-ctx.Done():
			return GenerationStageResult{}, ctx.Err()
		}
	}
	<-ctx.Done()
	return GenerationStageResult{}, ctx.Err()
}

type managedDispatchFixture struct {
	provider *ManagedRuntimeProvider
	reads    *managedReadFixture
	prepared *managedBackendDriverFixture
	worker   *managedDispatchDriver
	owner    string
	input    generationv16.TestGenerationStartRequestV16
}

func newManagedDispatchFixture(t *testing.T, scope testgendomain.Scope) *managedDispatchFixture {
	t.Helper()
	provider, reads, prepared := managedBackendFixture(t)
	report := reads.index.ReportID
	f := &managedDispatchFixture{provider: provider, reads: reads, prepared: prepared, owner: strings.Repeat("6", 64)}
	f.input = generationv16.TestGenerationStartRequestV16{
		IdempotencyKey: strings.Repeat("1", 32), ProjectID: reads.index.ProjectID,
		WorkspaceGeneration: reads.index.WorkspaceGeneration, CoverageReportID: &report,
		Scope: generationv16.TestGenerationScopeV16(scope), Framework: generationv16.Auto,
		Budgets: generationv16.TestGenerationBudgetsV16{WallTimeMS: 60000, CandidateCount: 2, MemoryMiB: 64, Concurrency: 1},
	}
	switch scope {
	case testgendomain.ScopeCoverageGap:
		f.input.CoverageGapID = &reads.index.Gaps[0].ID
	case testgendomain.ScopeSymbol:
		f.input.FunctionID = &reads.index.Files[0].Functions[0].ID
	case testgendomain.ScopeFile:
		f.input.FileID = &reads.index.Files[0].ID
	default:
		t.Fatal("unsupported dispatch fixture scope")
	}
	prepared.start = f.prepare
	f.worker = &managedDispatchDriver{generationDriverFixture: &generationDriverFixture{}, provider: provider,
		entered: make(chan managedDispatchEntry, 16), releaseBaseline: make(chan struct{})}
	provider.driver = f.worker
	return f
}

func (f *managedDispatchFixture) prepare(_ context.Context, owner string, input generationv16.TestGenerationStartRequestV16, target testgendomain.ManagedTarget) (testgendomain.Request, error) {
	hash := strings.Repeat("d", 64)
	scope := testgendomain.Scope(input.Scope)
	request := testgendomain.Request{
		SessionOwnerDigest: owner, IdempotencyKey: input.IdempotencyKey,
		ProjectID: input.ProjectID, WorkspaceGeneration: input.WorkspaceGeneration,
		Scope: scope, CoverageReportID: *input.CoverageReportID, Framework: testgendomain.Framework(input.Framework),
		Budgets:      testgendomain.Budgets{WallTimeMS: input.Budgets.WallTimeMS, CandidateCount: input.Budgets.CandidateCount, MemoryMiB: input.Budgets.MemoryMiB, Concurrency: input.Budgets.Concurrency},
		Goals:        testgendomain.Goals{FunctionPercent: input.Goals.FunctionPercent, LinePercent: input.Goals.LinePercent, BranchPercent: input.Goals.BranchPercent},
		SourceDigest: target.SourceDigest, CompileSnapshotDigest: hash, CoverageSnapshotDigest: hash,
		CMakeTargetDigest: hash, FrameworkBundleDigest: hash, AnalyzerBundleDigest: hash,
		BaselineReportDigest: hash, ProcessOwnerDigest: hash,
	}
	if scope == testgendomain.ScopeCoverageGap {
		request.ManagedGapID = target.GapID
	} else {
		request.ManagedTargetID = target.SelectionID(scope)
	}
	return request, nil
}

func waitManagedDispatchDone(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("managed dispatch worker did not settle")
	}
}

func waitManagedDispatchWorkers(t *testing.T, provider *ManagedRuntimeProvider) {
	t.Helper()
	done := make(chan struct{})
	go func() { provider.wg.Wait(); close(done) }()
	waitManagedDispatchDone(t, done)
}

func waitManagedDispatchEntry(t *testing.T, f *managedDispatchFixture) managedDispatchEntry {
	t.Helper()
	select {
	case entry := <-f.worker.entered:
		if entry.Err != nil {
			t.Fatalf("actual stage entry failed: %v", entry.Err)
		}
		return entry
	case <-time.After(5 * time.Second):
		t.Fatalf("managed start persisted but never entered RunStage; entries=%d", f.worker.calls.Load())
		return managedDispatchEntry{}
	}
}

func cancelManagedDispatch(t *testing.T, f *managedDispatchFixture, runID string, done <-chan struct{}) {
	t.Helper()
	result, err := f.provider.CancelManagedRun(context.Background(), f.owner, runID)
	if err != nil || testgendomain.State(result.State) != testgendomain.StateCancelled {
		t.Fatalf("cancel state=%s err=%v", result.State, err)
	}
	waitManagedDispatchDone(t, done)
	f.provider.mu.Lock()
	remaining, pending := len(f.provider.running), len(f.provider.cancelPending)
	f.provider.mu.Unlock()
	if remaining != 0 || pending != 0 {
		t.Fatalf("worker registrations leaked: running=%d pending=%d", remaining, pending)
	}
}

func TestManagedRuntimeDispatchStartsAfterPersistence(t *testing.T) {
	for _, scope := range []testgendomain.Scope{testgendomain.ScopeCoverageGap, testgendomain.ScopeSymbol, testgendomain.ScopeFile} {
		t.Run(string(scope), func(t *testing.T) {
			f := newManagedDispatchFixture(t, scope)
			f.input.Goals = generationv16.TestGenerationGoalsV16{FunctionPercent: 20, LinePercent: 30, BranchPercent: 40}
			ctx, cancelRequest := context.WithCancel(context.Background())
			defer cancelRequest()
			started, err := f.provider.StartManaged(ctx, f.owner, f.input)
			if err != nil {
				t.Fatal(err)
			}
			first := waitManagedDispatchEntry(t, f)
			target, err := f.provider.ResolveManagedStart(context.Background(), f.owner, f.input)
			if err != nil {
				t.Fatal(err)
			}
			expected, err := f.prepare(context.Background(), f.owner, f.input, target)
			if err != nil {
				t.Fatal(err)
			}
			if first.Run.ID != started.RunID || first.Run.TaskID != started.TaskID || first.Next != testgendomain.StateBaseline || !reflect.DeepEqual(first.Run.Request, expected) {
				t.Fatal("stage did not bind exact started run/request")
			}
			if first.Run.State != testgendomain.StateQueued || !first.Deadline.After(time.Now()) || first.Deadline.After(first.Run.CreatedAt.Add(time.Duration(expected.Budgets.WallTimeMS)*time.Millisecond)) {
				t.Fatal("first stage lost durable queued checkpoint or bounded deadline")
			}
			cancelRequest()
			close(f.worker.releaseBaseline)
			second := waitManagedDispatchEntry(t, f)
			if second.Next != testgendomain.StateAnalyzing || second.Run.State != testgendomain.StateBaseline || second.Run.Revision <= first.Run.Revision {
				t.Fatal("released actual baseline did not commit progress independently of request context")
			}
			cancelManagedDispatch(t, f, started.RunID, second.Done)
		})
	}
}

func TestManagedRuntimeDispatchActiveReplayDoesNotDuplicateStage(t *testing.T) {
	f := newManagedDispatchFixture(t, testgendomain.ScopeCoverageGap)
	started, err := f.provider.StartManaged(context.Background(), f.owner, f.input)
	if err != nil {
		t.Fatal(err)
	}
	entry := waitManagedDispatchEntry(t, f)
	replay, err := f.provider.StartManaged(context.Background(), f.owner, f.input)
	if err != nil || replay.RunID != started.RunID || replay.TaskID != started.TaskID {
		t.Fatalf("replay identity changed: %v", err)
	}
	cancelManagedDispatch(t, f, started.RunID, entry.Done)
	waitManagedDispatchWorkers(t, f.provider)
	if got := f.worker.calls.Load(); got != 1 {
		t.Fatalf("active replay executed %d stages before baseline release", got)
	}
}

func TestManagedRuntimeDispatchStoppedReplayDoesNotExecute(t *testing.T) {
	for _, state := range []testgendomain.State{testgendomain.StateAwaitingConfirmation, testgendomain.StateCancelled} {
		t.Run(string(state), func(t *testing.T) {
			f := newManagedDispatchFixture(t, testgendomain.ScopeCoverageGap)
			run := awaitingManagedRun(t, f.provider, f.reads, f.owner)
			if state == testgendomain.StateCancelled {
				var err error
				run, err = f.provider.coord.Cancel(context.Background(), run.TaskID)
				if err != nil {
					t.Fatal(err)
				}
			}
			replay, err := f.provider.StartManaged(context.Background(), f.owner, f.input)
			if err != nil || replay.RunID != run.ID || replay.TaskID != run.TaskID || testgendomain.State(replay.State) != state {
				t.Fatalf("stopped replay state=%s err=%v", replay.State, err)
			}
			waitManagedDispatchWorkers(t, f.provider)
			if f.worker.calls.Load() != 0 {
				t.Fatal("stopped replay entered actual stage")
			}
		})
	}
}

func TestManagedRuntimeDispatchRejectedStartDoesNotExecuteOrReserveKey(t *testing.T) {
	prepareErr := errors.New("controlled preparation failure")
	cases := []struct {
		name   string
		want   error
		change func(*testing.T, *managedDispatchFixture)
	}{
		{"unknown-gap", testgendomain.ErrStaleSnapshot, func(_ *testing.T, f *managedDispatchFixture) {
			unknown := strings.Repeat("e", 32)
			f.input.CoverageGapID = &unknown
		}},
		{"unbound-report", task.ErrStorageUnavailable, func(_ *testing.T, f *managedDispatchFixture) { f.input.CoverageReportID = nil }},
		{"prepare-error", prepareErr, func(_ *testing.T, f *managedDispatchFixture) {
			f.prepared.start = func(context.Context, string, generationv16.TestGenerationStartRequestV16, testgendomain.ManagedTarget) (testgendomain.Request, error) {
				return testgendomain.Request{}, prepareErr
			}
		}},
		{"source-mismatch", testgendomain.ErrStaleSnapshot, func(_ *testing.T, f *managedDispatchFixture) {
			f.prepared.start = func(ctx context.Context, owner string, input generationv16.TestGenerationStartRequestV16, target testgendomain.ManagedTarget) (testgendomain.Request, error) {
				r, err := f.prepare(ctx, owner, input, target)
				r.SourceDigest = strings.Repeat("e", 64)
				return r, err
			}
		}},
		{"fresh-source-changed", testgendomain.ErrStaleSnapshot, func(_ *testing.T, f *managedDispatchFixture) {
			f.prepared.start = func(ctx context.Context, owner string, input generationv16.TestGenerationStartRequestV16, target testgendomain.ManagedTarget) (testgendomain.Request, error) {
				r, err := f.prepare(ctx, owner, input, target)
				f.reads.index.Files[0].SourceSHA256 = strings.Repeat("e", 64)
				return r, err
			}
		}},
		{"fresh-report-changed", task.ErrConflict, func(_ *testing.T, f *managedDispatchFixture) {
			f.prepared.start = func(ctx context.Context, owner string, input generationv16.TestGenerationStartRequestV16, target testgendomain.ManagedTarget) (testgendomain.Request, error) {
				r, err := f.prepare(ctx, owner, input, target)
				f.reads.index.ReportID = strings.Repeat("e", 32)
				return r, err
			}
		}},
		{"owner-conflict", task.ErrConflict, func(_ *testing.T, f *managedDispatchFixture) {
			f.prepared.start = func(ctx context.Context, owner string, input generationv16.TestGenerationStartRequestV16, target testgendomain.ManagedTarget) (testgendomain.Request, error) {
				r, err := f.prepare(ctx, owner, input, target)
				r.SessionOwnerDigest = strings.Repeat("7", 64)
				return r, err
			}
		}},
		{"budget-conflict", task.ErrConflict, func(_ *testing.T, f *managedDispatchFixture) {
			f.prepared.start = func(ctx context.Context, owner string, input generationv16.TestGenerationStartRequestV16, target testgendomain.ManagedTarget) (testgendomain.Request, error) {
				r, err := f.prepare(ctx, owner, input, target)
				r.Budgets.WallTimeMS++
				return r, err
			}
		}},
		{"goals-conflict", task.ErrConflict, func(_ *testing.T, f *managedDispatchFixture) {
			f.prepared.start = func(ctx context.Context, owner string, input generationv16.TestGenerationStartRequestV16, target testgendomain.ManagedTarget) (testgendomain.Request, error) {
				r, err := f.prepare(ctx, owner, input, target)
				r.Goals.LinePercent = 1
				return r, err
			}
		}},
		{"persist-failure", task.ErrStorageUnavailable, func(t *testing.T, f *managedDispatchFixture) {
			broken, err := taskstore.Open(filepath.Join(t.TempDir(), "closed.sqlite"))
			if err != nil {
				t.Fatal(err)
			}
			if err := broken.Close(); err != nil {
				t.Fatal(err)
			}
			f.provider.coord = testgencoord.NewWithProcessVerifier(broken,
				func(_ context.Context, r testgendomain.Request) (testgendomain.SnapshotIdentity, error) {
					return r.SnapshotIdentity(), nil
				},
				func(context.Context, task.Artifact) error { return nil },
				func(context.Context, string, string) error { return nil })
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newManagedDispatchFixture(t, testgendomain.ScopeCoverageGap)
			input, coord := f.input, f.provider.coord
			source, report := f.reads.index.Files[0].SourceSHA256, f.reads.index.ReportID
			tc.change(t, f)
			if _, err := f.provider.StartManaged(context.Background(), f.owner, f.input); !errors.Is(err, tc.want) {
				t.Fatalf("rejected start=%v want=%v", err, tc.want)
			}
			waitManagedDispatchWorkers(t, f.provider)
			if f.worker.calls.Load() != 0 {
				t.Fatal("rejected request entered stage")
			}
			f.input, f.provider.coord = input, coord
			f.reads.index.Files[0].SourceSHA256, f.reads.index.ReportID = source, report
			f.prepared.start = f.prepare
			started, err := f.provider.StartManaged(context.Background(), f.owner, f.input)
			if err != nil {
				t.Fatalf("rejection reserved idempotency key: %v", err)
			}
			entry := waitManagedDispatchEntry(t, f)
			cancelManagedDispatch(t, f, started.RunID, entry.Done)
		})
	}
}

func TestManagedRuntimeDispatchCloseCancelsAndJoinsRegisteredWorker(t *testing.T) {
	f := newManagedDispatchFixture(t, testgendomain.ScopeCoverageGap)
	if _, err := f.provider.StartManaged(context.Background(), f.owner, f.input); err != nil {
		t.Fatal(err)
	}
	entry := waitManagedDispatchEntry(t, f)
	closed := make(chan struct{})
	go func() { f.provider.Close(); close(closed) }()
	waitManagedDispatchDone(t, closed)
	waitManagedDispatchDone(t, entry.Done)
	f.provider.mu.Lock()
	remaining := len(f.provider.running)
	f.provider.mu.Unlock()
	if remaining != 0 || f.worker.calls.Load() != 1 {
		t.Fatal("Close failed to settle the registered stage")
	}
}
