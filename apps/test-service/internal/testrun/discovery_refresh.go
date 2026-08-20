package testrun

import (
	"context"

	"unit-test-ide.local/test-service/internal/task"
	"unit-test-ide.local/test-service/internal/testdiscovery"
)

type TaskDiscoveryInputFactory func(
	context.Context,
	RefreshRequest,
) (testdiscovery.DiscoveryInput, error)

type TaskCatalogRefresher struct {
	service *testdiscovery.Service
	input   TaskDiscoveryInputFactory
}

func NewTaskCatalogRefresher(
	service *testdiscovery.Service,
	input TaskDiscoveryInputFactory,
) (*TaskCatalogRefresher, error) {
	if service == nil || input == nil {
		return nil, task.ErrInvalidArgument
	}
	return &TaskCatalogRefresher{
		service: service,
		input:   input,
	}, nil
}

func (refresher *TaskCatalogRefresher) PrepareAfterBuild(
	ctx context.Context,
	request RefreshRequest,
) (CatalogRefresh, RefreshProgress, error) {
	if refresher == nil || ctx == nil {
		return nil, RefreshProgress{}, task.ErrInvalidArgument
	}
	input, err := refresher.input(ctx, request)
	if err != nil {
		return nil, RefreshProgress{}, err
	}
	if input.TaskID != request.TaskID ||
		input.Profile != request.Profile {
		return nil, RefreshProgress{}, task.ErrInvalidArgument
	}
	execution, progress, err :=
		refresher.service.PrepareTaskDiscovery(ctx, input)
	if err != nil {
		return nil, RefreshProgress{}, err
	}
	session := &taskCatalogRefresh{execution: execution}
	converted, err := convertTaskDiscoveryProgress(progress)
	if err != nil {
		return nil, RefreshProgress{}, err
	}
	return session, converted, nil
}

// PrepareEmbedded reuses the existing synchronous discovery path to obtain
// fresh runtime-only descriptors and framework adapters for an already-owned
// Coverage Task. It does not create or start a Task or TestRun.
func (refresher *TaskCatalogRefresher) PrepareEmbedded(
	ctx context.Context,
	request RefreshRequest,
) (RefreshedCatalog, error) {
	if refresher == nil || ctx == nil {
		return RefreshedCatalog{}, task.ErrInvalidArgument
	}
	input, err := refresher.input(ctx, request)
	if err != nil {
		return RefreshedCatalog{}, err
	}
	if input.TaskID != request.TaskID || input.Profile != request.Profile {
		return RefreshedCatalog{}, task.ErrInvalidArgument
	}
	snapshot, err := refresher.service.DiscoverSnapshotAfterBuild(ctx, input)
	if err != nil {
		return RefreshedCatalog{}, err
	}
	converted, err := convertTaskDiscoveryProgress(
		testdiscovery.TaskProgress{Snapshot: &snapshot},
	)
	if err != nil || converted.Snapshot == nil {
		return RefreshedCatalog{}, task.ErrInvalidArgument
	}
	return converted.Snapshot.Clone(), nil
}

type taskCatalogRefresh struct {
	execution *testdiscovery.TaskExecution
}

func (refresh *taskCatalogRefresh) ObserveOutput(
	ctx context.Context,
	step task.ExecutionStep,
	output task.ProcessOutput,
) error {
	if refresh == nil || refresh.execution == nil {
		return task.ErrInvalidArgument
	}
	return refresh.execution.ObserveOutput(ctx, step, output)
}

func (refresh *taskCatalogRefresh) Interpret(
	ctx context.Context,
	step task.ExecutionStep,
	result task.ProcessResult,
) (task.StepVerdict, error) {
	if refresh == nil || refresh.execution == nil {
		return task.StepVerdictDefault,
			task.ErrInvalidArgument
	}
	return refresh.execution.Interpret(ctx, step, result)
}

func (refresh *taskCatalogRefresh) AfterStep(
	ctx context.Context,
	step task.ExecutionStep,
) (RefreshProgress, error) {
	if refresh == nil || refresh.execution == nil {
		return RefreshProgress{}, task.ErrInvalidArgument
	}
	progress, err := refresh.execution.AfterStep(ctx, step)
	if err != nil {
		return RefreshProgress{}, err
	}
	return convertTaskDiscoveryProgress(progress)
}

func convertTaskDiscoveryProgress(
	value testdiscovery.TaskProgress,
) (RefreshProgress, error) {
	value = value.Clone()
	result := RefreshProgress{
		Steps: value.Steps,
		Pins:  value.Pins,
	}
	if value.Snapshot == nil {
		return result, nil
	}
	snapshot := RefreshedCatalog{
		Catalog: value.Snapshot.Catalog.Clone(),
		Bindings: make(
			[]ContainerBinding,
			len(value.Snapshot.Bindings),
		),
	}
	for index, binding := range value.Snapshot.Bindings {
		snapshot.Bindings[index] = ContainerBinding{
			ContainerID: binding.ContainerID,
			Descriptor: cloneExecutionDescriptor(
				binding.Descriptor,
			),
			Adapter: binding.Adapter,
		}
	}
	result.Snapshot = &snapshot
	return result, nil
}

var _ CatalogRefresher = (*TaskCatalogRefresher)(nil)
var _ CatalogRefresh = (*taskCatalogRefresh)(nil)
