package runtime

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"unit-test-ide.local/test-service/internal/task"
	"unit-test-ide.local/test-service/internal/taskstore"
	"unit-test-ide.local/test-service/internal/testgencoord"
	"unit-test-ide.local/test-service/internal/testgendomain"
	"unit-test-ide.local/test-service/internal/testgenpublish"
)

type generationFailureWorkerDriver struct {
	generationDriverFixture
	failAt       testgendomain.State
	cause        error
	entered      chan struct{}
	release      chan struct{}
	waitDeadline bool
	result       *GenerationStageResult
	budget       *testgencoord.BudgetAmount
}

func (d *generationFailureWorkerDriver) StageBudget(r testgendomain.Run, next testgendomain.State) testgencoord.BudgetAmount {
	if next == d.failAt && d.budget != nil {
		return *d.budget
	}
	return d.generationDriverFixture.StageBudget(r, next)
}
func (d *generationFailureWorkerDriver) RunStage(ctx context.Context, r testgendomain.Run, next testgendomain.State, authorize func(context.Context) error) (GenerationStageResult, error) {
	if next != d.failAt {
		return d.generationDriverFixture.RunStage(ctx, r, next, authorize)
	}
	if err := authorize(ctx); err != nil {
		return GenerationStageResult{}, err
	}
	if _, ok := ctx.Deadline(); !ok {
		panic("missing stage deadline")
	}
	if d.entered != nil {
		close(d.entered)
	}
	if d.release != nil {
		select {
		case <-d.release:
		case <-ctx.Done():
			return GenerationStageResult{}, ctx.Err()
		}
	}
	if d.waitDeadline {
		<-ctx.Done()
		return GenerationStageResult{}, task.ErrConflict
	}
	if d.result != nil {
		return *d.result, nil
	}
	return GenerationStageResult{}, d.cause
}

func newGenerationFailureWorker(t *testing.T, d *generationFailureWorkerDriver, wallMS int64) (*generationService, *taskstore.Store, testgendomain.Run) {
	t.Helper()
	s, err := taskstore.Open(filepath.Join(t.TempDir(), "failure.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	service, err := newGenerationService(GenerationServiceConfig{
		Store: s, Driver: d, Publisher: &generationPublisherFixture{complete: d.complete}, Trusted: true, CoverageReady: true,
		VerifySnapshot: func(_ context.Context, r testgendomain.Request) (testgendomain.SnapshotIdentity, error) {
			return r.SnapshotIdentity(), nil
		},
		VerifyArtifact: func(context.Context, task.Artifact) error { return nil }, VerifyProcess: func(context.Context, string, string) error { return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(service.Close)
	digest := strings.Repeat("a", 64)
	request := testgendomain.Request{IdempotencyKey: strings.Repeat("1", 32), WorkspaceGeneration: digest, ProjectID: "core", Scope: testgendomain.ScopeWorkspace, Framework: testgendomain.FrameworkAuto,
		Goals: testgendomain.Goals{FunctionPercent: 100, LinePercent: 100, BranchPercent: 100}, Budgets: testgendomain.Budgets{WallTimeMS: wallMS, CandidateCount: 4, MemoryMiB: 256, Concurrency: 1},
		CompileSnapshotDigest: digest, CoverageSnapshotDigest: digest, SourceDigest: digest, CMakeTargetDigest: digest, FrameworkBundleDigest: digest, AnalyzerBundleDigest: digest, BaselineReportDigest: digest, ProcessOwnerDigest: digest, SessionOwnerDigest: strings.Repeat("f", 64),
	}
	r, err := service.coord.Start(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	return service, s, r
}

func generationFailureDone(t *testing.T, s *generationService, r testgendomain.Run, d *generationFailureWorkerDriver) <-chan struct{} {
	t.Helper()
	s.launch(r.ID)
	select {
	case <-d.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("worker stage not entered")
	}
	s.mu.Lock()
	execution := s.running[r.ID]
	s.mu.Unlock()
	if execution == nil {
		t.Fatal("worker registration missing while stage blocked")
	}
	return execution.done
}
func waitGenerationFailureDone(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("worker did not finish")
	}
}

func TestGenerationFailureWorkerPersistsAfterBaseline(t *testing.T) {
	d := &generationFailureWorkerDriver{failAt: testgendomain.StateAnalyzing, cause: task.ErrStorageUnavailable, entered: make(chan struct{}), release: make(chan struct{})}
	s, store, r := newGenerationFailureWorker(t, d, 60000)
	done := generationFailureDone(t, s, r, d)
	baseline, err := store.GetGeneration(context.Background(), r.ID)
	if err != nil || baseline.State != testgendomain.StateBaseline || baseline.Revision != 2 {
		t.Fatal("failure stage began before baseline checkpoint")
	}
	close(d.release)
	waitGenerationFailureDone(t, done)
	after, err := store.GetGeneration(context.Background(), r.ID)
	if err != nil || after.State != testgendomain.StateFailed || after.Revision != 3 || after.CandidateCount != 0 {
		t.Fatal("worker failure lifecycle differs")
	}
	row, err := store.Get(context.Background(), r.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	if row.ErrorCode != "TG1:analyzing:stage_run:storage_unavailable" {
		t.Fatalf("worker dropped safe failure diagnostic: %q", row.ErrorCode)
	}
	if row.ErrorMessage != "" {
		t.Fatal("raw error message persisted")
	}
	events, err := store.ReplayGenerationEvents(context.Background(), r.ID, 0, 200)
	if err != nil || len(events) != 5 || events[4].Type != task.EventTaskFinished {
		t.Fatal("worker terminal events differ")
	}
}

func TestGenerationFailureWorkerCancellationAndClose(t *testing.T) {
	for _, cancelRun := range []bool{true, false} {
		name := "close"
		if cancelRun {
			name = "cancel"
		}
		t.Run(name, func(t *testing.T) {
			d := &generationFailureWorkerDriver{failAt: testgendomain.StateBaseline, cause: task.ErrStorageUnavailable, entered: make(chan struct{}), release: make(chan struct{})}
			s, store, r := newGenerationFailureWorker(t, d, 60000)
			done := generationFailureDone(t, s, r, d)
			if cancelRun {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				if _, err := s.CancelTestGeneration(ctx, r.Request.SessionOwnerDigest, r.ID); err != nil {
					t.Fatal("registered worker cancellation rejected")
				}
			} else {
				s.Close()
			}
			waitGenerationFailureDone(t, done)
			after, err := store.GetGeneration(context.Background(), r.ID)
			if err != nil || after.State == testgendomain.StateFailed {
				t.Fatal("cancel/close converted to failed")
			}
			if cancelRun && after.State != testgendomain.StateCancelled {
				t.Fatal("cancelled terminal missing")
			}
			row, err := store.Get(context.Background(), r.TaskID)
			if err != nil || row.ErrorCode != "" {
				t.Fatal("cancel/close wrote failure diagnosis")
			}
		})
	}
}

type generationFailurePlanPublisher struct {
	*generationPublisherFixture
	cause   error
	badDiff bool
}

func (p *generationFailurePlanPublisher) Plan(ctx context.Context, set testgenpublish.CandidateSet) (testgenpublish.PublishPlan, error) {
	if p.cause != nil {
		return testgenpublish.PublishPlan{}, p.cause
	}
	plan, err := p.generationPublisherFixture.Plan(ctx, set)
	if p.badDiff {
		plan.Diff = ""
	}
	return plan, err
}

func TestGenerationFailureWorkerBoundaryFixtures(t *testing.T) {
	cases := []struct {
		name, code string
		driver     *generationFailureWorkerDriver
		publisher  *generationFailurePlanPublisher
	}{
		{"reserve-exceeded", "TG1:baseline:reserve_budget:budget_exceeded", &generationFailureWorkerDriver{failAt: testgendomain.StateBaseline, budget: &testgencoord.BudgetAmount{MemoryMiB: 257}}, nil},
		{"reserve-invalid", "TG1:baseline:reserve_budget:budget_invalid", &generationFailureWorkerDriver{failAt: testgendomain.StateBaseline, budget: &testgencoord.BudgetAmount{MemoryMiB: -1}}, nil},
		{"invalid-transition", "TG1:baseline:transition:invalid_transition", &generationFailureWorkerDriver{failAt: testgendomain.StateBaseline, result: &GenerationStageResult{Next: testgendomain.StateAccepted}}, nil},
		{"preview-missing", "TG1:awaiting_confirmation:preview_set:invalid_preview", &generationFailureWorkerDriver{generationDriverFixture: generationDriverFixture{complete: true}, failAt: testgendomain.StateAwaitingConfirmation, result: &GenerationStageResult{Next: testgendomain.StateAwaitingConfirmation}}, nil},
		{"set-missing", "TG1:awaiting_confirmation:authoritative_set:conflict", &generationFailureWorkerDriver{generationDriverFixture: generationDriverFixture{complete: true}, failAt: testgendomain.StateAwaitingConfirmation, result: &GenerationStageResult{Next: testgendomain.StateAwaitingConfirmation, PreviewSet: &testgenpublish.CandidateSet{}}}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, store, r := newGenerationFailureWorker(t, tc.driver, 60000)
			if tc.publisher != nil {
				s.publisher = tc.publisher
			}
			s.run(context.Background(), r.ID)
			after, err := store.GetGeneration(context.Background(), r.ID)
			if err != nil || after.State != testgendomain.StateFailed {
				t.Fatal("controlled boundary did not fail")
			}
			row, err := store.Get(context.Background(), r.TaskID)
			if err != nil || row.ErrorCode != tc.code || row.ErrorMessage != "" {
				t.Fatal("wrong controlled boundary diagnosis")
			}
		})
	}
}

type diagnosticNoFormatError struct{}

func (diagnosticNoFormatError) Error() string {
	panic("FAKE_SECRET_PATH_SOURCE_COMMAND_MARKER must not be formatted")
}

func TestGenerationFailureMapperIdentityPriorityPrivacy(t *testing.T) {
	cases := []struct {
		cause  error
		reason string
	}{
		{context.DeadlineExceeded, "timed_out"}, {testgencoord.ErrBudgetTimeout, "timed_out"}, {context.Canceled, "cancelled"},
		{testgencoord.ErrBudgetExceeded, "budget_exceeded"}, {testgencoord.ErrBudgetInvalid, "budget_invalid"},
		{testgendomain.ErrStaleSnapshot, "stale_snapshot"}, {task.ErrInvalidArgument, "invalid_argument"}, {testgendomain.ErrInvalid, "invalid_argument"},
		{task.ErrStorageUnavailable, "storage_unavailable"}, {testgencoord.ErrUnavailable, "unavailable"}, {errProductionGenerationUnavailable, "unavailable"},
		{task.ErrConflict, "conflict"}, {diagnosticNoFormatError{}, "unknown"}, {nil, "unknown"},
	}
	for _, tc := range cases {
		got := generationFailureDiagnostic("analyzing", "stage_run", tc.cause)
		if got.Reason != tc.reason || !got.Valid() {
			t.Fatal("error identity classification differs")
		}
		if tc.cause != nil {
			wrapped := withGenerationOperation("resolve_target", tc.cause)
			if !errors.Is(wrapped, tc.cause) {
				t.Fatal("wrapper lost error identity")
			}
			got = generationFailureDiagnostic("analyzing", "stage_run", wrapped)
			if got.Operation != "resolve_target" || got.Reason != tc.reason {
				t.Fatal("safe wrapped boundary lost")
			}
		}
	}
	ordered := []struct {
		first, second error
		reason        string
	}{
		{context.DeadlineExceeded, context.Canceled, "timed_out"},
		{context.Canceled, testgencoord.ErrBudgetExceeded, "cancelled"},
		{testgencoord.ErrBudgetExceeded, testgencoord.ErrBudgetInvalid, "budget_exceeded"},
		{testgencoord.ErrBudgetInvalid, testgendomain.ErrStaleSnapshot, "budget_invalid"},
		{testgendomain.ErrStaleSnapshot, task.ErrInvalidArgument, "stale_snapshot"},
		{task.ErrInvalidArgument, task.ErrStorageUnavailable, "invalid_argument"},
		{task.ErrStorageUnavailable, testgencoord.ErrUnavailable, "storage_unavailable"},
		{testgencoord.ErrUnavailable, task.ErrConflict, "unavailable"},
	}
	for _, tc := range ordered {
		if got := generationFailureDiagnostic("baseline", "stage_run", errors.Join(tc.second, tc.first)); got.Reason != tc.reason {
			t.Fatal("classification priority differs")
		}
	}
	sanitized := generationFailureDiagnostic("failed", "FAKE_SECRET_PATH_MARKER", diagnosticNoFormatError{})
	if sanitized != (testgendomain.FailureDiagnostic{"unknown", "unknown", "unknown"}) {
		t.Fatal("invalid metadata not reduced to unknown")
	}
	cause := withGenerationOperation("resolve_target", context.DeadlineExceeded)
	if got := generationFailureDiagnostic("analyzing", "stage_context", cause); got.Operation != "stage_context" {
		t.Fatal("context boundary overridden")
	}
	if withGenerationOperation("stage_run", nil) != nil {
		t.Fatal("nil error became failure")
	}
}

func TestGenerationFailureWorkerUnknownNeverFormatsRawError(t *testing.T) {
	d := &generationFailureWorkerDriver{failAt: testgendomain.StateAnalyzing, cause: diagnosticNoFormatError{}, entered: make(chan struct{}), release: make(chan struct{})}
	s, store, r := newGenerationFailureWorker(t, d, 60000)
	done := generationFailureDone(t, s, r, d)
	close(d.release)
	waitGenerationFailureDone(t, done)
	row, err := store.Get(context.Background(), r.TaskID)
	if err != nil || row.ErrorCode != "TG1:analyzing:stage_run:unknown" || row.ErrorMessage != "" {
		t.Fatal("unknown diagnosis differs")
	}
	events, err := store.ReplayGenerationEvents(context.Background(), r.ID, 0, 200)
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		if bytes.Contains(event.Payload, []byte("FAKE_SECRET")) {
			t.Fatal("raw marker reached journal")
		}
	}
}

func TestGenerationFailureWorkerStageContextPrecedence(t *testing.T) {
	d := &generationFailureWorkerDriver{failAt: testgendomain.StateBaseline, waitDeadline: true, entered: make(chan struct{})}
	s, store, r := newGenerationFailureWorker(t, d, 3000)
	done := generationFailureDone(t, s, r, d)
	waitGenerationFailureDone(t, done)
	row, err := store.Get(context.Background(), r.TaskID)
	if err != nil || row.ErrorCode != "TG1:baseline:stage_context:timed_out" {
		t.Fatal("stage context lost to returned conflict")
	}
}

func TestGenerationFailureWorkerPublishBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name, code string
		planErr    error
		badDiff    bool
	}{
		{"plan", "TG1:awaiting_confirmation:publish_plan:conflict", task.ErrConflict, false},
		{"diff", "TG1:awaiting_confirmation:preview_diff:invalid_preview", nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := &generationFailureWorkerDriver{generationDriverFixture: generationDriverFixture{complete: true}, failAt: testgendomain.StateAwaitingConfirmation, result: &GenerationStageResult{Next: testgendomain.StateAwaitingConfirmation, MinimizedCaseIDs: []string{strings.Repeat("5", 32)}, PreviewSet: &testgenpublish.CandidateSet{}}}
			s, store, r := newGenerationFailureWorker(t, d, 60000)
			s.publisher = &generationFailurePlanPublisher{generationPublisherFixture: &generationPublisherFixture{complete: true}, cause: tc.planErr, badDiff: tc.badDiff}
			s.run(context.Background(), r.ID)
			row, err := store.Get(context.Background(), r.TaskID)
			if err != nil || row.ErrorCode != tc.code {
				t.Fatal("publish boundary diagnosis differs")
			}
		})
	}
}

func TestGenerationFailureExplicitAdapterBoundaries(t *testing.T) {
	for _, tc := range []struct {
		stage, operation string
		cause            error
	}{
		{"baseline", "ledger", testgencoord.ErrBudgetInvalid},
		{"analyzing", "commit_budget", testgencoord.ErrBudgetTimeout},
		{"awaiting_confirmation", "finalize_review", task.ErrConflict},
		{"unknown", "resume", testgendomain.ErrStaleSnapshot},
	} {
		t.Run(tc.operation, func(t *testing.T) {
			d := &generationFailureWorkerDriver{}
			s, store, r := newGenerationFailureWorker(t, d, 60000)
			diagnostic := generationFailureDiagnostic(tc.stage, tc.operation, tc.cause)
			s.fail(r, diagnostic)
			row, err := store.Get(context.Background(), r.TaskID)
			raw, encodeErr := diagnostic.Encode()
			if err != nil || encodeErr != nil || row.ErrorCode != raw {
				t.Fatal("explicit adapter lost safe metadata")
			}
		})
	}
}
