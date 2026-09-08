package runtime

import (
	"bytes"
	"context"
	"errors"
	"io"
	"path/filepath"
	"sort"
	"sync"

	"unit-test-ide.local/test-service/internal/build"
	"unit-test-ide.local/test-service/internal/coveragedomain"
	"unit-test-ide.local/test-service/internal/coveragebundle"
	"unit-test-ide.local/test-service/internal/coverageexec"
	"unit-test-ide.local/test-service/internal/coveragegcc"
	"unit-test-ide.local/test-service/internal/coveragellvm"
	coveragemodelv1 "unit-test-ide.local/test-service/internal/coveragemodel/v1"
	"unit-test-ide.local/test-service/internal/coveragenormalize"
	"unit-test-ide.local/test-service/internal/coverageparser/llvm"
	coverageparsergcovr "unit-test-ide.local/test-service/internal/coverageparser/gcovr"
	"unit-test-ide.local/test-service/internal/coverageplatform"
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
	// CoverageBundleRoot is an internal, canonical exact bundle root seam.
	// It is intentionally absent from protocol and workspace configuration.
	CoverageBundleRoot string
	Clock         task.Clock
	NewID         task.IDGenerator
}

func newRuntimeCoverageExecutor(config coverageExecutionConfig) (coverageExecutor, error) {
	var adapter coverageexec.Adapter = unsupportedCoverageAdapter{}
	native := false
	switch config.Platform {
	case "windows":
		native = true
		adapter = llvmCoverageAdapter{}
	case "linux":
		native = true
		adapter = gccCoverageAdapter{bundleRoot: config.CoverageBundleRoot}
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

type gccCoverageAdapter struct{ bundleRoot string }

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
		toolset: toolset, toolsetCloser: toolset, ownsToolset: true,
		instrumentation: instrumentation,
		allocator:       allocator, profileRoot: input.ProfileRoot,
	}, nil
}

type llvmPreparedCoverageAdapter struct {
	toolset         *coveragellvm.Toolset
	toolsetCloser   io.Closer
	ownershipMu     sync.Mutex
	ownsToolset     bool
	instrumentation coveragellvm.Instrumentation
	allocator       testrun.ProfileAllocator
	profileRoot     string
	manifest        *coveragellvm.Manifest
	closeOnce       sync.Once
	closeErr        error
}

// RelinquishToolsetOwnership is called only after Build Boundary commits the
// toolset ownership claim. The adapter retains a non-owning operational view
// for collector construction, but Close must not close the transferred owner.
func (adapter *llvmPreparedCoverageAdapter) RelinquishToolsetOwnership() {
	if adapter == nil {
		return
	}
	adapter.ownershipMu.Lock()
	adapter.ownsToolset = false
	adapter.ownershipMu.Unlock()
}

func (adapter *llvmPreparedCoverageAdapter) Toolset() coverageplatform.Toolset {
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

func (adapter *llvmPreparedCoverageAdapter) PrepareTests(context.Context, coverageexec.PreparedBuild) error {
	return nil
}

func (adapter *llvmPreparedCoverageAdapter) SealEvidence(expectations []testrun.ProfileExpectation, outcomes []testrun.InvocationOutcome) ([]coveragedomain.CompletenessReason, error) {
	if adapter == nil {
		return nil, coveragellvm.ErrInvalidProfiles
	}
	manifest, err := coveragellvm.SealProfiles(adapter.profileRoot, expectations, outcomes)
	if err != nil { return nil, err }
	adapter.manifest = &manifest
	return append([]coveragedomain.CompletenessReason(nil), manifest.PartialReasons...), nil
}

func (adapter *llvmPreparedCoverageAdapter) PrepareCollector(_ context.Context, _ coverageexec.PreparedBuild, _ coverageplatform.DirectoryVerifier, binaries []coveragerun.TrustedPath) (coverageexec.CollectionPlan, error) {
	if adapter == nil || adapter.manifest == nil {
		return coverageexec.CollectionPlan{}, coveragellvm.ErrInvalidProfiles
	}
	merge, normalize, err := coveragellvm.BuildCollectorInvocation(adapter.toolset, *adapter.manifest, binaries)
	if err != nil { return coverageexec.CollectionPlan{}, err }
	return coverageexec.CollectionPlan{Aggregate: merge, Normalize: &normalize}, nil
}

func (adapter *llvmPreparedCoverageAdapter) Normalize(_ context.Context, input coverageexec.NormalizeInput) (coveragemodelv1.CoverageDocumentV1, []coveragenormalize.SourceBinding, error) {
	parsed, err := llvm.Parse(bytes.NewReader(input.ProcessOutput), llvm.Limits{MaxInputBytes: input.Limits.MaxInputBytes, MaxDepth: input.Limits.MaxDepth, MaxFiles: input.Limits.MaxFiles, MaxFunctions: input.Limits.MaxFunctions, MaxLines: input.Limits.MaxLines, MaxBranches: input.Limits.MaxBranches, MaxStringBytes: input.Limits.MaxStringBytes})
	if err != nil { return coveragemodelv1.CoverageDocumentV1{}, nil, err }
	return coveragenormalize.NormalizeLLVM(coveragenormalize.LLVMInput{Export: parsed, WorkspaceRoot: input.WorkspaceRoot, Matcher: input.Matcher, Toolchain: input.Toolchain, Completeness: input.Completeness, Limits: input.Limits})
}

func (adapter *llvmPreparedCoverageAdapter) Close() error {
	if adapter == nil {
		return nil
	}
	adapter.closeOnce.Do(func() {
		if adapter.manifest != nil {
			adapter.closeErr = errors.Join(adapter.closeErr, adapter.manifest.Close())
			adapter.manifest = nil
		}
		if closer, ok := adapter.allocator.(io.Closer); ok {
			adapter.closeErr = errors.Join(adapter.closeErr, closer.Close())
		}
		adapter.ownershipMu.Lock()
		toolsetCloser := adapter.toolsetCloser
		ownsToolset := adapter.ownsToolset
		adapter.ownsToolset = false
		adapter.ownershipMu.Unlock()
		if ownsToolset && toolsetCloser != nil {
			adapter.closeErr = errors.Join(adapter.closeErr, toolsetCloser.Close())
		}
	})
	return adapter.closeErr
}

func (adapter gccCoverageAdapter) Prepare(ctx context.Context, input coverageexec.AdapterInput) (coverageexec.PreparedAdapter, error) {
	if ctx == nil || ctx.Err() != nil || adapter.bundleRoot == "" || !filepath.IsAbs(adapter.bundleRoot) || filepath.Clean(adapter.bundleRoot) != adapter.bundleRoot {
		return nil, task.ErrInvalidArgument
	}
	toolset, err := coveragegcc.PinToolset(input.Toolchain)
	if err != nil { return nil, err }
	bundle, err := coveragebundle.ResolveExact(adapter.bundleRoot)
	if err != nil { _ = toolset.Close(); return nil, err }
	instrumentation, err := coveragegcc.WriteInstrumentation(input.TaskRoot)
	if err != nil { _ = bundle.Close(); _ = toolset.Close(); return nil, err }
	return &gccPreparedCoverageAdapter{toolset: toolset, bundle: bundle, instrumentation: instrumentation, allocator: coveragegcc.NewAllocator(), ownsToolset: true}, nil
}

type gccPreparedCoverageAdapter struct {
	toolset *coveragegcc.Toolset
	bundle coveragebundle.Pin
	instrumentation coverageplatform.Instrumentation
	allocator *coveragegcc.Allocator
	evidence *coveragegcc.PreparedEvidence
	manifest *coveragegcc.Manifest
	mu sync.Mutex
	ownsToolset bool
	closeOnce sync.Once
	closeErr error
}
func (a *gccPreparedCoverageAdapter) Toolset() coverageplatform.Toolset { if a == nil { return nil }; return a.toolset }
func (a *gccPreparedCoverageAdapter) RelinquishToolsetOwnership() { if a != nil { a.mu.Lock(); a.ownsToolset=false; a.mu.Unlock() } }
func (a *gccPreparedCoverageAdapter) Instrumentation() coverageplatform.Instrumentation { if a == nil { return coverageplatform.Instrumentation{} }; return a.instrumentation }
func (a *gccPreparedCoverageAdapter) Allocator() testrun.ProfileAllocator { if a == nil { return nil }; return a.allocator }
func (a *gccPreparedCoverageAdapter) PrepareTests(ctx context.Context, prepared coverageexec.PreparedBuild) error {
	if a == nil || ctx == nil || prepared == nil || coverageplatform.VerifyDirectory(prepared.CoverageObjectDirectory()) != nil { return task.ErrInvalidArgument }
	evidence, err := coveragegcc.PrepareBuildEvidence(prepared.CoverageObjectDirectory().Path())
	if err != nil { return err }
	a.mu.Lock(); old := a.evidence; a.evidence = evidence; a.mu.Unlock()
	if old != nil { return old.Close() }; return nil
}
func (a *gccPreparedCoverageAdapter) SealEvidence(_ []testrun.ProfileExpectation, outcomes []testrun.InvocationOutcome) ([]coveragedomain.CompletenessReason, error) {
	if a == nil { return nil, task.ErrInvalidArgument }
	a.mu.Lock(); evidence:=a.evidence; a.mu.Unlock()
	if evidence == nil { return nil, task.ErrInvalidArgument }
	manifest, err := evidence.Seal(context.Background(), outcomes)
	if err != nil { return nil, err }
	a.mu.Lock(); a.manifest=&manifest; a.mu.Unlock()
	return append([]coveragedomain.CompletenessReason(nil), manifest.PartialReasons...), nil
}
func (a *gccPreparedCoverageAdapter) PrepareCollector(ctx context.Context, prepared coverageexec.PreparedBuild, collector coverageplatform.DirectoryVerifier, _ []coveragerun.TrustedPath) (coverageexec.CollectionPlan, error) {
	if a == nil || ctx == nil || prepared == nil || a.bundle == nil || a.toolset == nil { return coverageexec.CollectionPlan{}, task.ErrInvalidArgument }
	if err := a.bundle.Verify(); err != nil { return coverageexec.CollectionPlan{}, err }
	execution, err := coveragegcc.PrepareCollector(a.bundle, collector, prepared.CoverageSourceRoot(), prepared.CoverageObjectDirectory(), a.toolset.GCov())
	if err != nil { return coverageexec.CollectionPlan{}, err }
	if err := prepared.AttachCoverageExecution(execution); err != nil { _ = execution.Close(); return coverageexec.CollectionPlan{}, err }
	return coverageexec.CollectionPlan{Aggregate: execution.ProcessSpec()}, nil
}
func (a *gccPreparedCoverageAdapter) Normalize(_ context.Context, input coverageexec.NormalizeInput) (coveragemodelv1.CoverageDocumentV1, []coveragenormalize.SourceBinding, error) {
	if input.PinnedOutput == nil { return coveragemodelv1.CoverageDocumentV1{}, nil, task.ErrInvalidArgument }
	raw, err := input.PinnedOutput.ReadAll(); if err != nil { return coveragemodelv1.CoverageDocumentV1{}, nil, err }
	export, err := coverageparsergcovr.Parse(bytes.NewReader(raw), coverageparsergcovr.Limits{MaxInputBytes: input.Limits.MaxInputBytes, MaxDepth: input.Limits.MaxDepth, MaxFiles: input.Limits.MaxFiles, MaxFunctions: input.Limits.MaxFunctions, MaxLines: input.Limits.MaxLines, MaxBranches: input.Limits.MaxBranches, MaxStringBytes: input.Limits.MaxStringBytes})
	if err != nil { return coveragemodelv1.CoverageDocumentV1{}, nil, err }
	return coveragenormalize.NormalizeGCC(coveragenormalize.GCCInput{Export: export, WorkspaceRoot: input.WorkspaceRoot, Matcher: input.Matcher, Toolchain: input.Toolchain, Completeness: input.Completeness, Limits: input.Limits})
}
func (a *gccPreparedCoverageAdapter) Close() error { if a == nil { return nil }; a.closeOnce.Do(func(){ a.mu.Lock(); evidence, manifest, bundle, owns := a.evidence,a.manifest,a.bundle,a.ownsToolset; a.evidence=nil; a.manifest=nil; a.bundle=nil; a.ownsToolset=false; a.mu.Unlock(); if manifest != nil { a.closeErr=errors.Join(a.closeErr,manifest.Close()) }; if evidence != nil { a.closeErr=errors.Join(a.closeErr,evidence.Close()) }; if bundle != nil { a.closeErr=errors.Join(a.closeErr,bundle.Close()) }; if owns && a.toolset != nil { a.closeErr=errors.Join(a.closeErr,a.toolset.Close()) }; if a.allocator != nil { a.closeErr=errors.Join(a.closeErr,a.allocator.Close()) } }); return a.closeErr }

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
