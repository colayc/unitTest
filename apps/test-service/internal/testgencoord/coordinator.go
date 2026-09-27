package testgencoord

import (
	"context"
	"errors"
	"time"

	"unit-test-ide.local/test-service/internal/task"
	"unit-test-ide.local/test-service/internal/taskstore"
	"unit-test-ide.local/test-service/internal/testgendomain"
)

var ErrStaleSnapshot = testgendomain.ErrStaleSnapshot
var ErrUnavailable = errors.New("test generation coordinator unavailable")

// SnapshotVerifier resolves trusted, current workspace/compile/coverage
// identities. A nil verifier fails closed; callers never supply these via IPC.
type SnapshotVerifier func(context.Context, testgendomain.Request) (testgendomain.SnapshotIdentity, error)

// ArtifactVerifier must reread the immutable artifact bytes and compare their
// SHA-256 with the supplied task-owned metadata before returning nil.
type ArtifactVerifier func(context.Context, task.Artifact) error

// ProcessOwnerVerifier attests that no process from this owner remains live.
// A successful resume never adopts an old process; it only returns a durable
// completed checkpoint from which the runtime may start a fresh stage.
type ProcessOwnerVerifier func(context.Context, string, string) error

type Coordinator struct {
	store          *taskstore.Store
	verify         SnapshotVerifier
	verifyArtifact ArtifactVerifier
	verifyProcess  ProcessOwnerVerifier
}

func NewWithProcessVerifier(store *taskstore.Store, verify SnapshotVerifier, artifactVerifier ArtifactVerifier, processVerifier ProcessOwnerVerifier) *Coordinator {
	return &Coordinator{store: store, verify: verify, verifyArtifact: artifactVerifier, verifyProcess: processVerifier}
}

func New(store *taskstore.Store, verify SnapshotVerifier, artifactVerifier ...ArtifactVerifier) *Coordinator {
	c := &Coordinator{store: store, verify: verify}
	if len(artifactVerifier) > 0 {
		c.verifyArtifact = artifactVerifier[0]
	}
	return c
}

func (c *Coordinator) check(ctx context.Context, r testgendomain.Request) error {
	if c == nil || c.store == nil || c.verify == nil || ctx == nil {
		return ErrUnavailable
	}
	now, err := c.verify(ctx, r)
	if err != nil {
		return err
	}
	return r.SnapshotMatches(now)
}

func (c *Coordinator) Start(ctx context.Context, request testgendomain.Request) (testgendomain.Run, error) {
	if testgendomain.ValidateRequest(request) != nil {
		return testgendomain.Run{}, task.ErrInvalidArgument
	}
	if err := c.check(ctx, request); err != nil {
		return testgendomain.Run{}, err
	}
	r := testgendomain.Run{ID: task.NewID(), TaskID: task.NewID(), Request: request, State: testgendomain.StateQueued, Revision: 1, CreatedAt: time.Now().UTC(), Record: testgendomain.NewGenerationRecord(request)}
	return c.store.CreateGeneration(ctx, r)
}
func (c *Coordinator) Get(ctx context.Context, runID string) (testgendomain.Run, error) {
	if c == nil || c.store == nil {
		return testgendomain.Run{}, ErrUnavailable
	}
	return c.store.GetGeneration(ctx, runID)
}
func (c *Coordinator) ListCandidates(ctx context.Context, runID string) ([]testgendomain.Candidate, error) {
	if c == nil || c.store == nil {
		return nil, ErrUnavailable
	}
	return c.store.ListGenerationCandidates(ctx, runID)
}

// Checkpoint commits one completed stage, its newly owned artifacts, and its
// candidates in one database transaction. The expected revision is the
// cross-process writer lease; stale writers cannot overwrite newer progress.
func (c *Coordinator) Checkpoint(ctx context.Context, runID string, expectedRevision int64, nextState testgendomain.State, candidates []testgendomain.Candidate, artifacts []task.Artifact) (testgendomain.Run, error) {
	return c.checkpoint(ctx, runID, expectedRevision, nextState, candidates, artifacts, nil)
}

func (c *Coordinator) CheckpointWithRecord(ctx context.Context, runID string, expectedRevision int64, nextState testgendomain.State, candidates []testgendomain.Candidate, artifacts []task.Artifact, record testgendomain.GenerationRecord) (testgendomain.Run, error) {
	return c.checkpoint(ctx, runID, expectedRevision, nextState, candidates, artifacts, &record)
}

func (c *Coordinator) checkpoint(ctx context.Context, runID string, expectedRevision int64, nextState testgendomain.State, candidates []testgendomain.Candidate, artifacts []task.Artifact, record *testgendomain.GenerationRecord) (testgendomain.Run, error) {
	if c == nil || c.store == nil {
		return testgendomain.Run{}, ErrUnavailable
	}
	current, err := c.store.GetGeneration(ctx, runID)
	if err != nil {
		return testgendomain.Run{}, err
	}
	if testgendomain.IsTerminal(current.State) && current.State == nextState &&
		current.Revision == expectedRevision+1 && len(candidates) == 0 && len(artifacts) == 0 {
		return current, nil
	}
	if current.Revision != expectedRevision {
		return testgendomain.Run{}, task.ErrConflict
	}
	if err := c.check(ctx, current.Request); err != nil {
		return testgendomain.Run{}, err
	}
	if err := c.verifyOwnedArtifacts(ctx, current); err != nil {
		return testgendomain.Run{}, err
	}
	next := testgendomain.CloneRun(current)
	if record != nil {
		next.Record = *record
		next.Record.MinimizedCaseIDs = append([]string(nil), record.MinimizedCaseIDs...)
	}
	next.State = nextState
	next.Revision++
	next.CandidateCount += len(candidates)
	if len(artifacts) > 0 && c.verifyArtifact == nil {
		return testgendomain.Run{}, ErrStaleSnapshot
	}
	for _, a := range artifacts {
		if err := c.verifyArtifact(ctx, a); err != nil {
			return testgendomain.Run{}, ErrStaleSnapshot
		}
	}
	for _, a := range artifacts {
		next.ArtifactDigests = append(next.ArtifactDigests, testgendomain.ArtifactRef{ID: a.ID, Digest: a.SHA256})
	}
	if testgendomain.IsTerminal(nextState) {
		now := time.Now().UTC()
		next.FinishedAt = &now
	}
	return c.store.CheckpointGeneration(ctx, expectedRevision, next, candidates, artifacts)
}

// Cancel uses the task ID, not a run ID, as the ownership capability.
func (c *Coordinator) Cancel(ctx context.Context, taskID string) (testgendomain.Run, error) {
	if c == nil || c.store == nil {
		return testgendomain.Run{}, ErrUnavailable
	}
	for attempts := 0; attempts < 3; attempts++ {
		r, err := c.store.GetGenerationByTask(ctx, taskID)
		if err != nil {
			return testgendomain.Run{}, err
		}
		if testgendomain.IsTerminal(r.State) {
			return r, nil
		}
		next := testgendomain.CloneRun(r)
		next.State = testgendomain.StateCancelled
		next.Revision++
		now := time.Now().UTC()
		next.FinishedAt = &now
		saved, err := c.store.CheckpointGeneration(ctx, r.Revision, next, nil, nil)
		if errors.Is(err, task.ErrConflict) {
			continue
		}
		return saved, err
	}
	return testgendomain.Run{}, task.ErrConflict
}

// Resume reads only persisted checkpoints; it does not launch an unfinished
// stage or reuse in-memory process state. The runtime owns stage execution.
func (c *Coordinator) Resume(ctx context.Context, taskID string) (testgendomain.Run, error) {
	if c == nil || c.store == nil {
		return testgendomain.Run{}, ErrUnavailable
	}
	r, err := c.store.GetGenerationByTask(ctx, taskID)
	if err != nil {
		return testgendomain.Run{}, err
	}
	if err := c.check(ctx, r.Request); err != nil {
		return testgendomain.Run{}, err
	}
	if r.Record.IsZero() || !r.Record.ValidFor(r.Request, r.CandidateCount) {
		return testgendomain.Run{}, ErrStaleSnapshot
	}
	if err := c.verifyOwnedArtifacts(ctx, r); err != nil {
		return testgendomain.Run{}, err
	}
	if r.Request.ProcessOwnerDigest != "" {
		if c.verifyProcess == nil || c.verifyProcess(ctx, r.TaskID, r.Request.ProcessOwnerDigest) != nil {
			return testgendomain.Run{}, ErrStaleSnapshot
		}
	}
	return r, nil
}

func (c *Coordinator) verifyOwnedArtifacts(ctx context.Context, r testgendomain.Run) error {
	for _, ref := range r.ArtifactDigests {
		a, err := c.store.GetArtifact(ctx, ref.ID)
		if err != nil || a.TaskID != r.TaskID || a.SHA256 != ref.Digest || c.verifyArtifact == nil {
			return ErrStaleSnapshot
		}
		if err := c.verifyArtifact(ctx, a); err != nil {
			return ErrStaleSnapshot
		}
	}
	return nil
}
