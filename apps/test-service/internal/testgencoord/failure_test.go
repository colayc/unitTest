package testgencoord

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"unit-test-ide.local/test-service/internal/task"
	"unit-test-ide.local/test-service/internal/taskstore"
	"unit-test-ide.local/test-service/internal/testgendomain"
)

func diagnosticCoordinator(t *testing.T) (*Coordinator, *taskstore.Store, testgendomain.Run) {
	t.Helper()
	s, err := taskstore.Open(filepath.Join(t.TempDir(), "diagnostic.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	c := New(s, func(_ context.Context, r testgendomain.Request) (testgendomain.SnapshotIdentity, error) {
		return r.SnapshotIdentity(), nil
	})
	c.verifyProcess = func(context.Context, string, string) error { return nil }
	r, err := c.Start(context.Background(), coordRequest())
	if err != nil {
		t.Fatal(err)
	}
	return c, s, r
}

func TestFailureDiagnosticCoordinatorCASAndIdentity(t *testing.T) {
	c, s, r := diagnosticCoordinator(t)
	ctx := context.Background()
	d := testgendomain.FailureDiagnostic{"baseline", "stage_run", "unknown"}
	if _, err := c.FailWithDiagnostic(ctx, r.TaskID, r.TaskID, r.Revision, d); !errors.Is(err, task.ErrConflict) {
		t.Fatal("wrong run identity accepted")
	}
	advanced, err := c.Checkpoint(ctx, r.ID, r.Revision, testgendomain.StateBaseline, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = c.FailWithDiagnostic(ctx, r.ID, r.TaskID, r.Revision, d); !errors.Is(err, task.ErrConflict) {
		t.Fatal("stale nonterminal revision accepted")
	}
	row, err := s.Get(ctx, r.TaskID)
	if err != nil || row.ErrorCode != "" {
		t.Fatal("stale diagnosis written")
	}
	failed, err := c.FailWithDiagnostic(ctx, r.ID, r.TaskID, advanced.Revision, d)
	if err != nil || failed.State != testgendomain.StateFailed {
		t.Fatal("valid failure rejected")
	}
	other := testgendomain.FailureDiagnostic{"baseline", "stage_run", "conflict"}
	replay, err := c.FailWithDiagnostic(ctx, r.ID, r.TaskID, r.Revision, other)
	if err != nil || replay.Revision != failed.Revision || replay.LastSequence != failed.LastSequence {
		t.Fatal("terminal replay wrote progress")
	}
	row, err = s.Get(ctx, r.TaskID)
	if err != nil || row.ErrorCode != "TG1:baseline:stage_run:unknown" {
		t.Fatal("first diagnosis overwritten")
	}
}

func TestFailureDiagnosticCoordinatorKeepsExistingTerminals(t *testing.T) {
	for _, state := range []testgendomain.State{testgendomain.StateAccepted, testgendomain.StateRejected, testgendomain.StateCancelled, testgendomain.StateFailed} {
		t.Run(string(state), func(t *testing.T) {
			c, s, r := diagnosticCoordinator(t)
			ctx := context.Background()
			var terminal testgendomain.Run
			var err error
			switch state {
			case testgendomain.StateCancelled:
				terminal, err = c.Cancel(ctx, r.TaskID)
			case testgendomain.StateFailed:
				terminal, err = c.Fail(ctx, r.TaskID)
			default:
				for _, stage := range []testgendomain.State{testgendomain.StateBaseline, testgendomain.StateAnalyzing, testgendomain.StateSolving, testgendomain.StateRendering, testgendomain.StateValidating} {
					r, err = c.Checkpoint(ctx, r.ID, r.Revision, stage, nil, nil)
					if err != nil {
						t.Fatal(err)
					}
				}
				if state == testgendomain.StateAccepted {
					for _, stage := range []testgendomain.State{testgendomain.StateMinimizing, testgendomain.StateAwaitingConfirmation} {
						r, err = c.Checkpoint(ctx, r.ID, r.Revision, stage, nil, nil)
						if err != nil {
							t.Fatal(err)
						}
					}
				}
				terminal, err = c.Checkpoint(ctx, r.ID, r.Revision, state, nil, nil)
			}
			if err != nil {
				t.Fatal(err)
			}
			d := testgendomain.FailureDiagnostic{"unknown", "resume", "unknown"}
			again, err := c.FailWithDiagnostic(ctx, r.ID, r.TaskID, r.Revision, d)
			if err != nil || again.State != terminal.State || again.Revision != terminal.Revision || again.LastSequence != terminal.LastSequence {
				t.Fatal("existing terminal overwritten")
			}
			row, err := s.Get(ctx, r.TaskID)
			if err != nil || row.ErrorCode != "" {
				t.Fatal("historic terminal backfilled")
			}
		})
	}
}

func TestFailureDiagnosticCoordinatorConcurrentFirstCommit(t *testing.T) {
	c, s, r := diagnosticCoordinator(t)
	gate := make(chan struct{})
	results := make(chan error, 2)
	for _, reason := range []string{"unknown", "conflict"} {
		go func(reason string) {
			<-gate
			_, err := c.FailWithDiagnostic(context.Background(), r.ID, r.TaskID, r.Revision, testgendomain.FailureDiagnostic{"baseline", "stage_run", reason})
			results <- err
		}(reason)
	}
	close(gate)
	for i := 0; i < 2; i++ {
		if err := <-results; err != nil && !errors.Is(err, task.ErrConflict) {
			t.Fatal("unexpected concurrent error")
		}
	}
	after, err := s.GetGeneration(context.Background(), r.ID)
	if err != nil || after.Revision != 2 || after.LastSequence != 3 || after.State != testgendomain.StateFailed {
		t.Fatal("more than one terminal commit")
	}
	row, err := s.Get(context.Background(), r.TaskID)
	if err != nil || (row.ErrorCode != "TG1:baseline:stage_run:unknown" && row.ErrorCode != "TG1:baseline:stage_run:conflict") {
		t.Fatal("concurrent diagnosis inconsistent")
	}
}
