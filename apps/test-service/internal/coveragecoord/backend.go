package coveragecoord

import (
	"context"
	"errors"

	"unit-test-ide.local/test-service/internal/coveragedomain"
	"unit-test-ide.local/test-service/internal/task"
	"unit-test-ide.local/test-service/internal/testdomain"
)

var ErrInvalidBackend = errors.New("invalid queued coverage backend")

// QueuedStartInput is the immutable, already-resolved input required to
// persist a coverage run. Toolchain and selection metadata must come from a
// trusted runtime resolver; this adapter never invents provenance.
type QueuedStartInput struct {
	Request        coveragedomain.Request
	Selection      testdomain.SelectionSnapshot
	BuildProfileID string
	ToolchainID    string
	Toolchain      coveragedomain.ToolchainSnapshot
}

type QueuedStartResult struct {
	Task    task.Task
	Run     coveragedomain.Run
	TestRun testdomain.TestRun
	Events  []task.Event
}

// QueuedBackend is intentionally limited to durable queue creation and
// canonical read delegation. It must not be exposed as an execution provider
// until a real coverage executor is attached to the queued task lifecycle.
type QueuedBackend struct {
	coordinator *Coordinator
	repository  task.CoverageRepository
}

func NewQueuedBackend(coordinator *Coordinator, repository task.CoverageRepository) (*QueuedBackend, error) {
	if coordinator == nil || repository == nil {
		return nil, ErrInvalidBackend
	}
	return &QueuedBackend{coordinator: coordinator, repository: repository}, nil
}

func (backend *QueuedBackend) Start(ctx context.Context, input QueuedStartInput) (QueuedStartResult, error) {
	if backend == nil || backend.coordinator == nil {
		return QueuedStartResult{}, ErrInvalidBackend
	}
	result, err := backend.coordinator.Enqueue(ctx, QueuedInput{
		Request: input.Request, Selection: input.Selection,
		BuildProfileID: input.BuildProfileID, ToolchainID: input.ToolchainID,
		Toolchain: input.Toolchain,
	})
	if err != nil {
		return QueuedStartResult{}, err
	}
	return QueuedStartResult{Task: result.Task, Run: result.Run, TestRun: result.TestRun, Events: result.Events}, nil
}

func (backend *QueuedBackend) Get(ctx context.Context, runID string) (coveragedomain.Run, error) {
	if backend == nil || backend.repository == nil {
		return coveragedomain.Run{}, ErrInvalidBackend
	}
	return backend.repository.GetCoverageRun(ctx, runID)
}

func (backend *QueuedBackend) List(ctx context.Context, request coveragedomain.RunPageRequest) (coveragedomain.RunPage, error) {
	if backend == nil || backend.repository == nil {
		return coveragedomain.RunPage{}, ErrInvalidBackend
	}
	return backend.repository.ListCoverageRuns(ctx, request)
}

func (backend *QueuedBackend) Report(ctx context.Context, reportID string) (coveragedomain.Report, error) {
	if backend == nil || backend.repository == nil {
		return coveragedomain.Report{}, ErrInvalidBackend
	}
	return backend.repository.GetCoverageReport(ctx, reportID)
}
