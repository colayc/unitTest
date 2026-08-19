package coveragecoord

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"unit-test-ide.local/test-service/internal/coveragedomain"
	"unit-test-ide.local/test-service/internal/task"
	"unit-test-ide.local/test-service/internal/testdomain"
)

func TestQueuedBackendStartsOnlyAQueuedAggregateAndDelegatesReads(t *testing.T) {
	store := &backendStore{}
	coordinator, err := NewCoordinator(store, fixedCoverageClock{at: time.Date(2026, 8, 19, 8, 9, 10, 0, time.UTC)}, sequentialIDs("a", "b"))
	if err != nil {
		t.Fatal(err)
	}
	backend, err := NewQueuedBackend(coordinator, store)
	if err != nil {
		t.Fatal(err)
	}
	started, err := backend.Start(context.Background(), QueuedStartInput{
		Request:        validRequest(),
		Selection:      validSelectionSnapshot(),
		BuildProfileID: strings.Repeat("b", 64),
		ToolchainID:    "gcc-linux",
		Toolchain:      validToolchain(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if started.Task.Status != task.StatusQueued || started.Run.Status != coveragedomain.StatusQueued || started.TestRun.Status != testdomain.RunQueued {
		t.Fatalf("queued result = %#v", started)
	}
	if started.Task.ID != started.Run.TaskID || started.Task.ID != started.TestRun.TaskID {
		t.Fatalf("queued identity graph = %#v", started)
	}
	if _, err := backend.Get(context.Background(), started.Run.ID); !errors.Is(err, task.ErrNotFound) {
		// The fixture intentionally does not implement a read row. The call
		// must still be delegated rather than synthesized from the start input.
		t.Fatalf("Get() error = %v, want delegated not-found", err)
	}
}

func TestNewQueuedBackendRejectsMissingCoordinatorOrRepository(t *testing.T) {
	if _, err := NewQueuedBackend(nil, &backendStore{}); !errors.Is(err, ErrInvalidBackend) {
		t.Fatalf("nil coordinator error = %v", err)
	}
	coordinator, err := NewCoordinator(&recordingCoverageStore{}, fixedCoverageClock{}, sequentialIDs("c", "d"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewQueuedBackend(coordinator, nil); !errors.Is(err, ErrInvalidBackend) {
		t.Fatalf("nil repository error = %v", err)
	}
}

type backendStore struct {
	replayingCoverageStore
}

func (*backendStore) GetCoverageRun(context.Context, string) (coveragedomain.Run, error) {
	return coveragedomain.Run{}, task.ErrNotFound
}

func (*backendStore) ListCoverageRuns(context.Context, coveragedomain.RunPageRequest) (coveragedomain.RunPage, error) {
	return coveragedomain.RunPage{}, task.ErrNotFound
}

func (*backendStore) GetCoverageReport(context.Context, string) (coveragedomain.Report, error) {
	return coveragedomain.Report{}, task.ErrNotFound
}
