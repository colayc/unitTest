package coveragecoord

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"unit-test-ide.local/test-service/internal/coveragedomain"
	"unit-test-ide.local/test-service/internal/task"
	"unit-test-ide.local/test-service/internal/testdomain"
)

func TestCoordinatorEnqueueUsesTrustedDefaultsAndPersistsClosedAggregate(t *testing.T) {
	created := time.Date(2026, 8, 19, 5, 6, 7, 123_000_000, time.FixedZone("local", 8*60*60))
	store := &recordingCoverageStore{}
	coordinator, err := NewCoordinator(store, fixedCoverageClock{at: created}, sequentialIDs("a", "b"))
	if err != nil {
		t.Fatal(err)
	}
	result, err := coordinator.Enqueue(context.Background(), QueuedInput{
		Request:        validRequest(),
		Selection:      validSelectionSnapshot(),
		BuildProfileID: strings.Repeat("b", 64),
		ToolchainID:    "gcc-linux",
		Toolchain:      validToolchain(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Task.ID != strings.Repeat("a", 32) || result.Run.TaskID != result.Task.ID || result.TestRun.TaskID != result.Task.ID {
		t.Fatalf("identity graph = %#v", result)
	}
	if !result.Task.CreatedAt.Equal(created.UTC()) || !result.Run.CreatedAt.Equal(created.UTC()) {
		t.Fatalf("createdAt not normalized: task=%v run=%v", result.Task.CreatedAt, result.Run.CreatedAt)
	}
	if store.calls != 1 || len(result.Events) != 1 {
		t.Fatalf("persist calls/events = %d/%d", store.calls, len(result.Events))
	}
}

func TestCoordinatorEnqueuePreservesExplicitCreationTime(t *testing.T) {
	created := time.Date(2026, 8, 19, 5, 6, 7, 0, time.UTC)
	store := &recordingCoverageStore{}
	coordinator, err := NewCoordinator(store, fixedCoverageClock{at: created.Add(time.Hour)}, sequentialIDs("c", "d"))
	if err != nil {
		t.Fatal(err)
	}
	result, err := coordinator.Enqueue(context.Background(), QueuedInput{
		Request:        validRequest(),
		Selection:      validSelectionSnapshot(),
		BuildProfileID: strings.Repeat("b", 64),
		ToolchainID:    "gcc-linux",
		Toolchain:      validToolchain(),
		CreatedAt:      created,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Task.CreatedAt.Equal(created) {
		t.Fatalf("explicit createdAt = %v, want %v", result.Task.CreatedAt, created)
	}
}

func TestCoordinatorRejectsInvalidConstructionAndStorageFailure(t *testing.T) {
	if _, err := NewCoordinator(nil, fixedCoverageClock{}, sequentialIDs("e", "f")); !errors.Is(err, ErrInvalidCoordinator) {
		t.Fatalf("nil store error = %v", err)
	}
	store := &recordingCoverageStore{err: errors.New("storage down")}
	coordinator, err := NewCoordinator(store, fixedCoverageClock{at: time.Now().UTC()}, sequentialIDs("1", "2"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := coordinator.Enqueue(context.Background(), QueuedInput{
		Request:        validRequest(),
		Selection:      validSelectionSnapshot(),
		BuildProfileID: strings.Repeat("b", 64),
		ToolchainID:    "gcc-linux",
		Toolchain:      validToolchain(),
	}); !errors.Is(err, store.err) {
		t.Fatalf("storage error = %v, want %v", err, store.err)
	}
}

func TestCoordinatorRejectsDuplicateAggregateIDs(t *testing.T) {
	coordinator, err := NewCoordinator(&recordingCoverageStore{}, fixedCoverageClock{at: time.Now().UTC()}, sequentialIDs("a", "a"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = coordinator.Enqueue(context.Background(), QueuedInput{
		Request:        validRequest(),
		Selection:      validSelectionSnapshot(),
		BuildProfileID: strings.Repeat("b", 64),
		ToolchainID:    "gcc-linux",
		Toolchain:      validToolchain(),
	})
	if !errors.Is(err, ErrInvalidQueuedInput) {
		t.Fatalf("duplicate IDs error = %v", err)
	}
}

func TestCoordinatorReloadsCanonicalRelationsOnIdempotentReplay(t *testing.T) {
	created := time.Date(2026, 8, 19, 5, 6, 7, 0, time.UTC)
	store := &replayingCoverageStore{}
	coordinator, err := NewCoordinator(store, fixedCoverageClock{at: created}, sequentialIDs("a", "b", "c", "d"))
	if err != nil {
		t.Fatal(err)
	}
	input := QueuedInput{
		Request:        validRequest(),
		Selection:      validSelectionSnapshot(),
		BuildProfileID: strings.Repeat("b", 64),
		ToolchainID:    "gcc-linux",
		Toolchain:      validToolchain(),
	}
	first, err := coordinator.Enqueue(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	second, err := coordinator.Enqueue(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Events) != 0 || second.Task.ID != first.Task.ID || !reflect.DeepEqual(second.Run, first.Run) || !reflect.DeepEqual(second.TestRun, first.TestRun) {
		t.Fatalf("replay did not return persisted relations: first=%#v second=%#v", first, second)
	}
}

type replayingCoverageStore struct {
	recordingCoverageStore
	run     coveragedomain.Run
	testRun testdomain.TestRun
	task    task.Task
}

func (store *replayingCoverageStore) CreateCoverageTask(_ context.Context, input task.Task, _ []task.StepSnapshot, _ task.EventDraft, run coveragedomain.Run, testRun testdomain.TestRun) (task.Task, []task.Event, error) {
	store.calls++
	if store.calls == 1 {
		store.task, store.run, store.testRun = input, run, testRun
		return input, []task.Event{{ID: "event-1"}}, nil
	}
	return store.task, nil, nil
}

func (store *replayingCoverageStore) GetCoverageRun(_ context.Context, id string) (coveragedomain.Run, error) {
	if id != store.run.ID {
		return coveragedomain.Run{}, task.ErrNotFound
	}
	return store.run, nil
}

func (store *replayingCoverageStore) GetRunForTask(_ context.Context, id string) (testdomain.TestRun, error) {
	if id != store.task.ID {
		return testdomain.TestRun{}, task.ErrNotFound
	}
	return store.testRun, nil
}

type fixedCoverageClock struct{ at time.Time }

func (clock fixedCoverageClock) Now() time.Time { return clock.at }

func (clock fixedCoverageClock) After(time.Duration) <-chan time.Time { return make(chan time.Time) }
