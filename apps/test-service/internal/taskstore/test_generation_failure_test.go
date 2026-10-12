package taskstore

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"unit-test-ide.local/test-service/internal/task"
	"unit-test-ide.local/test-service/internal/testgendomain"
)

func diagnosticFailedNext(r testgendomain.Run) testgendomain.Run {
	next := testgendomain.CloneRun(r)
	next.State = testgendomain.StateFailed
	next.Revision++
	now := time.Now().UTC()
	next.FinishedAt = &now
	return next
}

func diagnosticBaseline(t *testing.T, s *Store) testgendomain.Run {
	t.Helper()
	r, err := s.CreateGeneration(context.Background(), generationRunFixture())
	if err != nil {
		t.Fatal(err)
	}
	next := r
	next.State = testgendomain.StateBaseline
	next.Revision++
	r, err = s.CheckpointGeneration(context.Background(), r.Revision, next, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestGenerationFailureStoreAtomicRestart(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "failure.sqlite")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if s != nil {
			_ = s.Close()
		}
	}()
	r := diagnosticBaseline(t, s)
	d := testgendomain.FailureDiagnostic{"analyzing", "resolve_target", "stale_snapshot"}
	failed, err := s.CheckpointGenerationWithFailure(ctx, r.Revision, diagnosticFailedNext(r), d)
	if err != nil || failed.State != testgendomain.StateFailed {
		t.Fatal("valid failure did not commit")
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	row, err := s.Get(ctx, r.TaskID)
	if err != nil || row.ErrorCode != "TG1:analyzing:resolve_target:stale_snapshot" || row.ErrorMessage != "" || row.Outcome != task.OutcomeInfrastructureFailed || row.ActiveStep != "failed" {
		t.Fatal("reopened task diagnosis differs")
	}
	run, err := s.GetGeneration(ctx, r.ID)
	if err != nil || run.Revision != r.Revision+1 || run.State != testgendomain.StateFailed {
		t.Fatal("reopened run differs")
	}
	events, err := s.ReplayGenerationEvents(ctx, r.ID, 0, 200)
	if err != nil || len(events) != 5 || events[4].Type != task.EventTaskFinished || row.LastSequence != 5 {
		t.Fatal("terminal journal differs")
	}
	if _, err := s.CheckpointGenerationWithFailure(ctx, r.Revision, diagnosticFailedNext(r), d); !errors.Is(err, task.ErrConflict) {
		t.Fatal("stale storage writer accepted")
	}
}

func TestGenerationFailureStoreRollsBackWholeTransaction(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	r := diagnosticBaseline(t, s)
	_, err := s.db.Exec(`CREATE TRIGGER diagnostic_finish_fault BEFORE INSERT ON test_generation_events WHEN NEW.event_type='task.finished' BEGIN SELECT RAISE(ABORT,'diagnostic-fixture-fault'); END`)
	if err != nil {
		t.Fatal(err)
	}
	d := testgendomain.FailureDiagnostic{"analyzing", "stage_run", "unknown"}
	if _, err = s.CheckpointGenerationWithFailure(ctx, r.Revision, diagnosticFailedNext(r), d); err == nil {
		t.Fatal("injected transaction fault ignored")
	}
	run, err := s.GetGeneration(ctx, r.ID)
	if err != nil || run.State != r.State || run.Revision != r.Revision || run.LastSequence != r.LastSequence {
		t.Fatal("run partially committed")
	}
	row, err := s.Get(ctx, r.TaskID)
	if err != nil || row.ErrorCode != "" || row.ErrorMessage != "" || row.FinishedAt != nil || row.LastSequence != r.LastSequence {
		t.Fatal("task partially committed")
	}
	events, err := s.ReplayGenerationEvents(ctx, r.ID, 0, 200)
	if err != nil || len(events) != 3 {
		t.Fatal("private journal partially committed")
	}
	if _, err = s.db.Exec(`DROP TRIGGER diagnostic_finish_fault`); err != nil {
		t.Fatal(err)
	}
	if _, err = s.CheckpointGenerationWithFailure(ctx, r.Revision, diagnosticFailedNext(r), d); err != nil {
		t.Fatal("valid retry after fixture fault rejected")
	}
}

func TestGenerationFailureStoreInvalidAndLegacy(t *testing.T) {
	for _, legacy := range []string{"", "stale-snapshot"} {
		t.Run("legacy-"+legacy, func(t *testing.T) {
			s := openTestStore(t)
			r := diagnosticBaseline(t, s)
			ctx := context.Background()
			if _, err := s.db.Exec(`UPDATE tasks SET error_code=?,error_message='legacy-fixture' WHERE task_id=?`, legacy, r.TaskID); err != nil {
				t.Fatal(err)
			}
			if _, err := s.CheckpointGeneration(ctx, r.Revision, diagnosticFailedNext(r), nil, nil); err != nil {
				t.Fatal(err)
			}
			row, err := s.Get(ctx, r.TaskID)
			if err != nil || row.ErrorCode != legacy || row.ErrorMessage != "legacy-fixture" {
				t.Fatal("old interface rewrote legacy fields")
			}
		})
	}
	for _, tc := range []struct {
		name      string
		nextState testgendomain.State
		d         testgendomain.FailureDiagnostic
	}{
		{"invalid-value", testgendomain.StateFailed, testgendomain.FailureDiagnostic{"analyzing", "FAKE_SECRET_PATH_MARKER", "unknown"}},
		{"nonfailed", testgendomain.StateAnalyzing, testgendomain.FailureDiagnostic{"analyzing", "stage_run", "unknown"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := openTestStore(t)
			r := diagnosticBaseline(t, s)
			next := diagnosticFailedNext(r)
			next.State = tc.nextState
			if next.State != testgendomain.StateFailed {
				next.FinishedAt = nil
			}
			if _, err := s.CheckpointGenerationWithFailure(context.Background(), r.Revision, next, tc.d); !errors.Is(err, task.ErrInvalidArgument) {
				t.Fatal("invalid diagnosis write accepted")
			}
			row, err := s.Get(context.Background(), r.TaskID)
			if err != nil || row.LastSequence != r.LastSequence || row.ErrorCode != "" {
				t.Fatal("invalid write changed task")
			}
		})
	}
}

func TestGenerationFailureStorePreservesMinimizedConstraint(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	original := generationRunFixture()
	original.Record = testgendomain.NewGenerationRecord(original.Request)
	r, err := s.CreateGeneration(ctx, original)
	if err != nil {
		t.Fatal(err)
	}
	for _, state := range []testgendomain.State{testgendomain.StateBaseline, testgendomain.StateAnalyzing, testgendomain.StateSolving, testgendomain.StateRendering} {
		next := r
		next.State = state
		next.Revision++
		r, err = s.CheckpointGeneration(ctx, r.Revision, next, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
	}
	a := task.Artifact{ID: strings.Repeat("4", 32), TaskID: r.TaskID, Kind: "test-generation-source", RelativePath: "tasks/" + r.TaskID + "/" + strings.Repeat("4", 32) + ".source", MIMEType: "application/octet-stream", Size: 12, SHA256: strings.Repeat("e", 64), CreatedAt: r.CreatedAt}
	c := testgendomain.Candidate{CaseID: strings.Repeat("5", 32), Kind: testgendomain.KindVerified, TargetSymbol: "fn:classify", Assertions: []testgendomain.Assertion{{Kind: testgendomain.AssertionIndependentOracle, EvidenceDigest: strings.Repeat("6", 64)}}, StagedSourceArtifact: testgendomain.ArtifactRef{ID: a.ID, Digest: a.SHA256}, CodeDigest: strings.Repeat("7", 64), PlannedEdits: []testgendomain.PlannedEdit{{Path: "tests/classify.c", Operation: testgendomain.EditCreate, AfterDigest: strings.Repeat("8", 64)}}}
	next := testgendomain.CloneRun(r)
	next.State = testgendomain.StateValidating
	next.Revision++
	next.CandidateCount = 1
	next.ArtifactDigests = []testgendomain.ArtifactRef{{ID: a.ID, Digest: a.SHA256}}
	r, err = s.CheckpointGeneration(ctx, r.Revision, next, []testgendomain.Candidate{c}, []task.Artifact{a})
	if err != nil {
		t.Fatal(err)
	}
	for _, state := range []testgendomain.State{testgendomain.StateMinimizing, testgendomain.StateAwaitingConfirmation} {
		next = testgendomain.CloneRun(r)
		next.State = state
		next.Revision++
		if state == testgendomain.StateAwaitingConfirmation {
			next.Record.MinimizedCaseIDs = []string{c.CaseID}
		}
		r, err = s.CheckpointGeneration(ctx, r.Revision, next, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
	}
	d := testgendomain.FailureDiagnostic{"awaiting_confirmation", "finalize_review", "unknown"}
	if _, err = s.CheckpointGenerationWithFailure(ctx, r.Revision, diagnosticFailedNext(r), d); !errors.Is(err, task.ErrInvalidArgument) {
		t.Fatal("minimized-record safety weakened")
	}
	after, err := s.GetGeneration(ctx, r.ID)
	if err != nil || after.Revision != r.Revision || after.State != r.State {
		t.Fatal("rejected terminalization changed run")
	}
	row, err := s.Get(ctx, r.TaskID)
	if err != nil || row.ErrorCode != "" {
		t.Fatal("rejected terminalization wrote diagnosis")
	}
}
