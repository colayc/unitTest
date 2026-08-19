package coveragecoord

import (
	"context"
	"errors"

	"unit-test-ide.local/test-service/internal/coveragedomain"
	"unit-test-ide.local/test-service/internal/task"
	"unit-test-ide.local/test-service/internal/testdomain"
)

var ErrInvalidCoordinator = errors.New("invalid coverage coordinator")

// Coordinator owns only the trusted queue boundary. It does not construct
// process specifications, start processes, or publish a report.
type Coordinator struct {
	store task.CoverageTaskStore
	clock task.Clock
	newID task.IDGenerator
}

type EnqueueResult struct {
	Task    task.Task
	Run     coveragedomain.Run
	TestRun testdomain.TestRun
	Events  []task.Event
}

func NewCoordinator(store task.CoverageTaskStore, clock task.Clock, newID task.IDGenerator) (*Coordinator, error) {
	if store == nil {
		return nil, ErrInvalidCoordinator
	}
	if clock == nil {
		clock = task.RealClock{}
	}
	if newID == nil {
		newID = task.NewID
	}
	return &Coordinator{store: store, clock: clock, newID: newID}, nil
}

func (coordinator *Coordinator) Enqueue(ctx context.Context, input QueuedInput) (EnqueueResult, error) {
	if coordinator == nil || ctx == nil || coordinator.store == nil || coordinator.clock == nil || coordinator.newID == nil {
		return EnqueueResult{}, ErrInvalidCoordinator
	}
	if input.CreatedAt.IsZero() {
		input.CreatedAt = coordinator.clock.Now()
	}
	input.NewID = coordinator.newID
	aggregate, err := NewQueuedAggregate(input)
	if err != nil {
		return EnqueueResult{}, err
	}
	taskValue, events, err := aggregate.Persist(ctx, coordinator.store)
	if err != nil {
		return EnqueueResult{}, err
	}
	return EnqueueResult{Task: taskValue, Run: aggregate.Run, TestRun: aggregate.TestRun, Events: events}, nil
}
