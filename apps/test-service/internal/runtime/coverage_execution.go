package runtime

import (
	"context"
	"errors"
	"io"
	"sort"
	"sync"

	"unit-test-ide.local/test-service/internal/build"
	"unit-test-ide.local/test-service/internal/coverageexec"
	"unit-test-ide.local/test-service/internal/coveragellvm"
	"unit-test-ide.local/test-service/internal/coveragerun"
	"unit-test-ide.local/test-service/internal/task"
	"unit-test-ide.local/test-service/internal/testrun"
	"unit-test-ide.local/test-service/internal/workspace"
)

type coverageExecutionCoordinator interface {
	Resume(context.Context, task.Task) (task.Task, error)
	FinishUnsupported(context.Context, task.Task) (task.Task, error)
	Close() error
}

type platformCoverageExecutor struct {
	coordinator coverageExecutionCoordinator
	native      bool
}

func (executor *platformCoverageExecutor) Resume(ctx context.Context, persisted task.Task) (task.Task, error) {
	if executor == nil || executor.coordinator == nil {
		return task.Task{}, task.ErrStorageUnavailable
	}
	if !executor.native {
		return executor.coordinator.FinishUnsupported(ctx, persisted)
	}
	return executor.coordinator.Resume(ctx, persisted)
}

func (executor *platformCoverageExecutor) FinishUnsupported(ctx context.Context, persisted task.Task) (task.Task, error) {
	if executor == nil || executor.coordinator == nil {
		return task.Task{}, task.ErrStorageUnavailable
	}
	return executor.coordinator.FinishUnsupported(ctx, persisted)
}

func (executor *platformCoverageExecutor) Close() error {
	if executor == nil || executor.coordinator == nil {
		return nil
	}
	return executor.coordinator.Close()
}

type coverageExecutionConfig struct {
	Platform      string
	Tasks         coverageexec.TaskController
	Store         coverageexec.Store
	Build         coverageexec.BuildPreparer
	Tests         coverageexec.EmbeddedTestPreparer
	WorkspaceRoot workspace.Root
	ExecutionRoot string
	Clock         task.Clock
	NewID         task.IDGenerator
}

func newRuntimeCoverageExecutor(config coverageExecutionConfig) (coverageExecutor, error) {
	var adapter coverageexec.Adapter = unsupportedCoverageAdapter{}
	native := config.Platform == "windows"
	if native {
		adapter = llvmCoverageAdapter{}
	}
	coordinator, err := coverageexec.NewCoordinator(coverageexec.Config{
		Tasks: config.Tasks, Store: config.Store, Build: config.Build,
		Tests: config.Tests, Adapter: adapter, WorkspaceRoot: config.WorkspaceRoot,
		ExecutionRoot: config.ExecutionRoot, Clock: config.Clock, NewID: config.NewID,
	})
	if err != nil {
		return nil, err
	}
	return &platformCoverageExecutor{coordinator: coordinator, native: native}, nil
}

type coveragePlanPreparer interface {
	PreparePlan(context.Context, build.StartRequest) (*build.PreparedPlan, error)
}

type coverageBuildPreparer struct {
	delegate coveragePlanPreparer
}

func (preparer coverageBuildPreparer) PreparePlan(ctx context.Context, request build.StartRequest) (coverageexec.PreparedBuild, error) {
	if preparer.delegate == nil {
		return nil, task.ErrStorageUnavailable
	}
	return preparer.delegate.PreparePlan(ctx, request)
}

type llvmCoverageAdapter struct{}

func (llvmCoverageAdapter) Prepare(ctx context.Context, input coverageexec.AdapterInput) (coverageexec.PreparedAdapter, error) {
	if ctx == nil {
		return nil, task.ErrInvalidArgument
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// Toolchain is the retained Instance returned by the current build/Inspector
	// revalidation. Never reconstruct native tools from persisted provenance.
	toolset, err := coveragellvm.PinToolset(input.Toolchain)
	if err != nil {
		return nil, err
	}
	instrumentation, err := coveragellvm.WriteInstrumentation(input.TaskRoot)
	if err != nil {
		_ = toolset.Close()
		return nil, err
	}
	allocator, err := coveragellvm.NewProfileAllocator(input.ProfileRoot)
	if err != nil {
		_ = toolset.Close()
		return nil, err
	}
	return &llvmPreparedCoverageAdapter{
		toolset: toolset, instrumentation: instrumentation,
		allocator: allocator, profileRoot: input.ProfileRoot,
	}, nil
}

type llvmPreparedCoverageAdapter struct {
	toolset         *coveragellvm.Toolset
	instrumentation coveragellvm.Instrumentation
	allocator       testrun.ProfileAllocator
	profileRoot     string
	closeOnce       sync.Once
	closeErr        error
}

func (adapter *llvmPreparedCoverageAdapter) Toolset() *coveragellvm.Toolset {
	if adapter == nil {
		return nil
	}
	return adapter.toolset
}

func (adapter *llvmPreparedCoverageAdapter) Instrumentation() coveragellvm.Instrumentation {
	if adapter == nil {
		return coveragellvm.Instrumentation{}
	}
	return adapter.instrumentation
}

func (adapter *llvmPreparedCoverageAdapter) Allocator() testrun.ProfileAllocator {
	if adapter == nil {
		return nil
	}
	return adapter.allocator
}

func (adapter *llvmPreparedCoverageAdapter) SealProfiles(expectations []testrun.ProfileExpectation, outcomes []testrun.InvocationOutcome) (coveragellvm.Manifest, error) {
	if adapter == nil {
		return coveragellvm.Manifest{}, coveragellvm.ErrInvalidProfiles
	}
	return coveragellvm.SealProfiles(adapter.profileRoot, expectations, outcomes)
}

func (adapter *llvmPreparedCoverageAdapter) Collector(manifest coveragellvm.Manifest, binaries []coveragerun.TrustedPath) (task.ProcessSpec, task.ProcessSpec, error) {
	if adapter == nil {
		return task.ProcessSpec{}, task.ProcessSpec{}, coveragellvm.ErrInvalidProfiles
	}
	return coveragellvm.BuildCollectorInvocation(adapter.toolset, manifest, binaries)
}

func (adapter *llvmPreparedCoverageAdapter) Close() error {
	if adapter == nil {
		return nil
	}
	adapter.closeOnce.Do(func() {
		if closer, ok := adapter.allocator.(io.Closer); ok {
			adapter.closeErr = errors.Join(adapter.closeErr, closer.Close())
		}
		if adapter.toolset != nil {
			adapter.closeErr = errors.Join(adapter.closeErr, adapter.toolset.Close())
		}
	})
	return adapter.closeErr
}

type unsupportedCoverageAdapter struct{}

func (unsupportedCoverageAdapter) Prepare(context.Context, coverageexec.AdapterInput) (coverageexec.PreparedAdapter, error) {
	// Non-Windows execution is dispatched to FinishUnsupported before Prepare.
	// Keep this guard side-effect free if that invariant is ever violated.
	return nil, coveragellvm.ErrUnsupportedPlatform
}

func resumeQueuedCoverage(ctx context.Context, store runtimeStore, executor coverageExecutor) error {
	if ctx == nil || store == nil || executor == nil {
		return task.ErrStorageUnavailable
	}
	cursor := ""
	queued := make([]task.Task, 0)
	for {
		page, err := store.List(ctx, cursor, brokerPageSize, task.KindCoverageRun)
		if err != nil {
			return err
		}
		for _, persisted := range page.Items {
			if persisted.Status == task.StatusQueued {
				queued = append(queued, persisted)
			}
		}
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
	}
	sort.Slice(queued, func(left, right int) bool {
		if queued[left].CreatedAt.Equal(queued[right].CreatedAt) {
			return queued[left].ID < queued[right].ID
		}
		return queued[left].CreatedAt.Before(queued[right].CreatedAt)
	})
	for _, persisted := range queued {
		if _, err := executor.Resume(ctx, persisted); err != nil {
			return err
		}
	}
	return nil
}

var _ coverageexec.BuildPreparer = coverageBuildPreparer{}
var _ coverageexec.Adapter = llvmCoverageAdapter{}
var _ coverageexec.PreparedAdapter = (*llvmPreparedCoverageAdapter)(nil)
var _ coverageexec.Adapter = unsupportedCoverageAdapter{}
var _ coverageExecutor = (*platformCoverageExecutor)(nil)
