package testgencoord

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"unit-test-ide.local/test-service/internal/artifactstore"
	"unit-test-ide.local/test-service/internal/task"
	"unit-test-ide.local/test-service/internal/taskstore"
	"unit-test-ide.local/test-service/internal/testgendomain"
)

func coordRequest() testgendomain.Request {
	return testgendomain.Request{IdempotencyKey: strings.Repeat("1", 32), WorkspaceGeneration: strings.Repeat("a", 64), ProjectID: "project", Scope: testgendomain.ScopeWorkspace, Framework: testgendomain.FrameworkAuto,
		Goals: testgendomain.Goals{FunctionPercent: 90, LinePercent: 80, BranchPercent: 70}, Budgets: testgendomain.Budgets{WallTimeMS: 1000, CandidateCount: 4, MemoryMiB: 256, Concurrency: 1}, CompileSnapshotDigest: strings.Repeat("b", 64), CoverageSnapshotDigest: strings.Repeat("c", 64)}
}

func TestRestartResumesFromEachCompletedStage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tasks.sqlite")
	ctx := context.Background()
	s, err := taskstore.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	c := New(s, func(context.Context, testgendomain.Request) (testgendomain.SnapshotIdentity, error) {
		r := coordRequest()
		return r.SnapshotIdentity(), nil
	})
	r, err := c.Start(ctx, coordRequest())
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []testgendomain.State{testgendomain.StateQueued, testgendomain.StateBaseline, testgendomain.StateAnalyzing, testgendomain.StateSolving, testgendomain.StateRendering, testgendomain.StateValidating, testgendomain.StateMinimizing, testgendomain.StateAwaitingConfirmation} {
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
		s, err = taskstore.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		c = New(s, func(context.Context, testgendomain.Request) (testgendomain.SnapshotIdentity, error) {
			return coordRequest().SnapshotIdentity(), nil
		})
		resumed, err := c.Resume(ctx, r.TaskID)
		if err != nil || resumed.State != expected {
			t.Fatalf("resume after %s: %+v, %v", expected, resumed, err)
		}
		if expected != testgendomain.StateAwaitingConfirmation {
			next, _ := NextStage(expected)
			r, err = c.Checkpoint(ctx, r.ID, r.Revision, next, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestCancelOwnershipIdempotencyAndStaleIdentity(t *testing.T) {
	s, err := taskstore.Open(filepath.Join(t.TempDir(), "tasks.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	c := New(s, func(context.Context, testgendomain.Request) (testgendomain.SnapshotIdentity, error) {
		return coordRequest().SnapshotIdentity(), nil
	})
	r, err := c.Start(ctx, coordRequest())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Cancel(ctx, r.ID); err == nil {
		t.Fatal("accepted run id as task owner")
	}
	cancelled, err := c.Cancel(ctx, r.TaskID)
	if err != nil || cancelled.State != testgendomain.StateCancelled {
		t.Fatalf("cancel = %+v, %v", cancelled, err)
	}
	again, err := c.Cancel(ctx, r.TaskID)
	if err != nil || again.Revision != cancelled.Revision {
		t.Fatalf("idempotent cancel = %+v, %v", again, err)
	}
	if _, err := c.Resume(ctx, r.TaskID); err != nil {
		t.Fatal(err)
	}
	stale := New(s, func(context.Context, testgendomain.Request) (testgendomain.SnapshotIdentity, error) {
		return testgendomain.SnapshotIdentity{WorkspaceGeneration: strings.Repeat("f", 64)}, nil
	})
	if _, err := stale.Resume(ctx, r.TaskID); !errors.Is(err, ErrStaleSnapshot) {
		t.Fatalf("stale resume: %v", err)
	}
}

func TestResumeFailsClosedWithoutArtifactByteVerification(t *testing.T) {
	s, err := taskstore.Open(filepath.Join(t.TempDir(), "tasks.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	c := New(s, func(context.Context, testgendomain.Request) (testgendomain.SnapshotIdentity, error) {
		return coordRequest().SnapshotIdentity(), nil
	})
	r, err := c.Start(ctx, coordRequest())
	if err != nil {
		t.Fatal(err)
	}
	a := task.Artifact{ID: strings.Repeat("4", 32), TaskID: r.TaskID, Kind: "test-generation-source", RelativePath: "tasks/" + r.TaskID + "/" + strings.Repeat("4", 32) + ".source", MIMEType: "application/octet-stream", Size: 4, SHA256: strings.Repeat("e", 64), CreatedAt: r.CreatedAt}
	next := r
	next.State = testgendomain.StateBaseline
	next.Revision++
	next.ArtifactDigests = []testgendomain.ArtifactRef{{ID: a.ID, Digest: a.SHA256}}
	if _, err = s.CheckpointGeneration(ctx, r.Revision, next, nil, []task.Artifact{a}); err != nil {
		t.Fatal(err)
	}
	if _, err = c.Resume(ctx, r.TaskID); !errors.Is(err, ErrStaleSnapshot) {
		t.Fatalf("unverified bytes: %v", err)
	}
	verified := New(s, func(context.Context, testgendomain.Request) (testgendomain.SnapshotIdentity, error) {
		return coordRequest().SnapshotIdentity(), nil
	}, func(context.Context, task.Artifact) error { return nil })
	if _, err = verified.Resume(ctx, r.TaskID); err != nil {
		t.Fatalf("verified resume: %v", err)
	}
	changed := New(s, func(context.Context, testgendomain.Request) (testgendomain.SnapshotIdentity, error) {
		return coordRequest().SnapshotIdentity(), nil
	}, func(context.Context, task.Artifact) error { return errors.New("bytes changed") })
	if _, err = changed.Resume(ctx, r.TaskID); !errors.Is(err, ErrStaleSnapshot) {
		t.Fatalf("changed bytes: %v", err)
	}
}

func TestTerminalCheckpointReplayIsIdempotent(t *testing.T) {
	s, err := taskstore.Open(filepath.Join(t.TempDir(), "tasks.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	c := New(s, func(context.Context, testgendomain.Request) (testgendomain.SnapshotIdentity, error) {
		return coordRequest().SnapshotIdentity(), nil
	})
	r, err := c.Start(ctx, coordRequest())
	if err != nil {
		t.Fatal(err)
	}
	finished, err := c.Checkpoint(ctx, r.ID, r.Revision, testgendomain.StateCancelled, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	again, err := c.Checkpoint(ctx, r.ID, r.Revision, testgendomain.StateCancelled, nil, nil)
	if err != nil || again.Revision != finished.Revision || again.LastSequence != finished.LastSequence {
		t.Fatalf("terminal replay = %+v, %v", again, err)
	}
	if _, err := c.Checkpoint(ctx, r.ID, r.Revision, testgendomain.StateFailed, nil, nil); !errors.Is(err, task.ErrConflict) {
		t.Fatalf("different terminal replay: %v", err)
	}
}

func TestOnlyOneWriterCanCommitARevision(t *testing.T) {
	s, err := taskstore.Open(filepath.Join(t.TempDir(), "tasks.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	c := New(s, func(context.Context, testgendomain.Request) (testgendomain.SnapshotIdentity, error) {
		return coordRequest().SnapshotIdentity(), nil
	})
	r, err := c.Start(ctx, coordRequest())
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, e := c.Checkpoint(ctx, r.ID, r.Revision, testgendomain.StateBaseline, nil, nil)
			results <- e
		}()
	}
	wg.Wait()
	close(results)
	success, conflicts := 0, 0
	for e := range results {
		if e == nil {
			success++
		} else if errors.Is(e, task.ErrConflict) {
			conflicts++
		} else {
			t.Fatalf("writer error: %v", e)
		}
	}
	if success != 1 || conflicts != 1 {
		t.Fatalf("writers: success=%d conflicts=%d", success, conflicts)
	}
}

func TestRestartRevalidatesStoredArtifactBytes(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "tasks.sqlite")
	artifactRoot := filepath.Join(dir, "artifacts")
	ctx := context.Background()
	s, err := taskstore.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	files, err := artifactstore.New(artifactRoot)
	if err != nil {
		t.Fatal(err)
	}
	c := New(s, func(context.Context, testgendomain.Request) (testgendomain.SnapshotIdentity, error) {
		return coordRequest().SnapshotIdentity(), nil
	}, files.VerifyGenerationSource)
	r, err := c.Start(ctx, coordRequest())
	if err != nil {
		t.Fatal(err)
	}
	a, err := files.CommitGenerationSource(ctx, r.TaskID, strings.Repeat("4", 32), r.CreatedAt, []byte("TEST(Classify, Positive){}\n"))
	if err != nil {
		t.Fatal(err)
	}
	checkpoint, err := c.Checkpoint(ctx, r.ID, r.Revision, testgendomain.StateBaseline, nil, []task.Artifact{a})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if err := files.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = taskstore.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	files, err = artifactstore.New(artifactRoot)
	if err != nil {
		t.Fatal(err)
	}
	defer files.Close()
	c = New(s, func(context.Context, testgendomain.Request) (testgendomain.SnapshotIdentity, error) {
		return coordRequest().SnapshotIdentity(), nil
	}, files.VerifyGenerationSource)
	resumed, err := c.Resume(ctx, r.TaskID)
	if err != nil || resumed.Revision != checkpoint.Revision || resumed.State != checkpoint.State {
		t.Fatalf("resumed = %+v, %v", resumed, err)
	}
	if err := os.WriteFile(filepath.Join(artifactRoot, filepath.FromSlash(a.RelativePath)), []byte(strings.Repeat("x", int(a.Size))), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Resume(ctx, r.TaskID); !errors.Is(err, ErrStaleSnapshot) {
		t.Fatalf("tampered artifact: %v", err)
	}
}
