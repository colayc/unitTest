package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	goruntime "runtime"
	"strings"
	"sync"

	"unit-test-ide.local/test-service/internal/cmake"
	"unit-test-ide.local/test-service/internal/coveragecoord"
	"unit-test-ide.local/test-service/internal/coveragedomain"
	"unit-test-ide.local/test-service/internal/coveragellvm"
	"unit-test-ide.local/test-service/internal/session"
	"unit-test-ide.local/test-service/internal/task"
	"unit-test-ide.local/test-service/internal/testdomain"
	"unit-test-ide.local/test-service/internal/toolchain"
	"unit-test-ide.local/test-service/internal/workspace"
)

var errInvalidCoverageBackend = errors.New("invalid runtime coverage backend")

type coverageQueue interface {
	Start(context.Context, coveragecoord.QueuedStartInput) (coveragecoord.QueuedStartResult, error)
}

type coverageRepository interface {
	Get(context.Context, string) (task.Task, error)
	GetRunForTask(context.Context, string) (testdomain.TestRun, error)
	GetCoverageRun(context.Context, string) (coveragedomain.Run, error)
	ListCoverageRuns(context.Context, coveragedomain.RunPageRequest) (coveragedomain.RunPage, error)
	GetCoverageReport(context.Context, string) (coveragedomain.Report, error)
}

type coverageExecutor interface {
	Resume(context.Context, task.Task) (task.Task, error)
	FinishUnsupported(context.Context, task.Task) (task.Task, error)
	Close() error
}

type coverageStartResolver func(context.Context, session.CoverageRunStart) (coveragecoord.QueuedStartInput, error)

type queuedCoverageBackend struct {
	mu         sync.RWMutex
	stopped    bool
	queue      coverageQueue
	repository coverageRepository
	resolve    coverageStartResolver
	executor   coverageExecutor
}

func newRuntimeCoverageBackend(queue coverageQueue, repository coverageRepository, resolve coverageStartResolver, executor coverageExecutor) (*queuedCoverageBackend, error) {
	if queue == nil || repository == nil || resolve == nil || executor == nil {
		return nil, errInvalidCoverageBackend
	}
	return &queuedCoverageBackend{queue: queue, repository: repository, resolve: resolve, executor: executor}, nil
}

func (backend *queuedCoverageBackend) StartCoverageRun(
	ctx context.Context,
	input session.CoverageRunStart,
) (task.Task, coveragedomain.Run, testdomain.TestRun, error) {
	if backend == nil || ctx == nil {
		return task.Task{}, coveragedomain.Run{}, testdomain.TestRun{}, errInvalidCoverageBackend
	}
	backend.mu.RLock()
	if backend.stopped {
		backend.mu.RUnlock()
		return task.Task{}, coveragedomain.Run{}, testdomain.TestRun{}, task.ErrStorageUnavailable
	}
	if backend.queue == nil || backend.repository == nil || backend.resolve == nil || backend.executor == nil {
		backend.mu.RUnlock()
		return task.Task{}, coveragedomain.Run{}, testdomain.TestRun{}, errInvalidCoverageBackend
	}
	queued, err := backend.resolve(ctx, input)
	if err != nil {
		backend.mu.RUnlock()
		return task.Task{}, coveragedomain.Run{}, testdomain.TestRun{}, err
	}
	result, err := backend.queue.Start(ctx, queued)
	if err != nil {
		backend.mu.RUnlock()
		return task.Task{}, coveragedomain.Run{}, testdomain.TestRun{}, err
	}
	if result.Task.ID == "" || result.Run.ID == "" || result.TestRun.RunID == "" ||
		result.Run.TaskID != result.Task.ID || result.Run.TestRunID != result.TestRun.RunID ||
		result.TestRun.TaskID != result.Task.ID ||
		result.Task.Status != task.StatusQueued || result.Run.Status != coveragedomain.StatusQueued ||
		result.TestRun.Status != testdomain.RunQueued {
		backend.mu.RUnlock()
		return task.Task{}, coveragedomain.Run{}, testdomain.TestRun{}, errInvalidCoverageBackend
	}
	backend.mu.RUnlock()
	_, resumeErr := backend.executor.Resume(ctx, result.Task)
	canonicalTask, canonicalRun, canonicalTestRun, reloadErr := backend.reloadGraph(ctx, result)
	if reloadErr != nil {
		return result.Task, result.Run, result.TestRun, errors.Join(resumeErr, reloadErr)
	}
	return canonicalTask, canonicalRun, canonicalTestRun, resumeErr
}

func (backend *queuedCoverageBackend) reloadGraph(ctx context.Context, queued coveragecoord.QueuedStartResult) (task.Task, coveragedomain.Run, testdomain.TestRun, error) {
	canonicalTask, err := backend.repository.Get(ctx, queued.Task.ID)
	if err != nil {
		return task.Task{}, coveragedomain.Run{}, testdomain.TestRun{}, err
	}
	canonicalRun, err := backend.repository.GetCoverageRun(ctx, queued.Run.ID)
	if err != nil {
		return task.Task{}, coveragedomain.Run{}, testdomain.TestRun{}, err
	}
	canonicalTestRun, err := backend.repository.GetRunForTask(ctx, queued.Task.ID)
	if err != nil {
		return task.Task{}, coveragedomain.Run{}, testdomain.TestRun{}, err
	}
	if canonicalTask.ID != queued.Task.ID || canonicalRun.ID != queued.Run.ID ||
		canonicalRun.TaskID != canonicalTask.ID || canonicalRun.TestRunID != canonicalTestRun.RunID ||
		canonicalTestRun.TaskID != canonicalTask.ID {
		return task.Task{}, coveragedomain.Run{}, testdomain.TestRun{}, errInvalidCoverageBackend
	}
	return canonicalTask, canonicalRun, canonicalTestRun, nil
}

func (backend *queuedCoverageBackend) stopAdmission() {
	if backend == nil {
		return
	}
	backend.mu.Lock()
	backend.stopped = true
	backend.mu.Unlock()
}

func (backend *queuedCoverageBackend) GetCoverageRun(ctx context.Context, id string) (coveragedomain.Run, error) {
	if backend == nil || backend.repository == nil || ctx == nil {
		return coveragedomain.Run{}, errInvalidCoverageBackend
	}
	return backend.repository.GetCoverageRun(ctx, id)
}

func (backend *queuedCoverageBackend) ListCoverageRuns(ctx context.Context, request coveragedomain.RunPageRequest) (coveragedomain.RunPage, error) {
	if backend == nil || backend.repository == nil || ctx == nil {
		return coveragedomain.RunPage{}, errInvalidCoverageBackend
	}
	return backend.repository.ListCoverageRuns(ctx, request)
}

func (backend *queuedCoverageBackend) GetCoverageReport(ctx context.Context, id string) (coveragedomain.Report, error) {
	if backend == nil || backend.repository == nil || ctx == nil {
		return coveragedomain.Report{}, errInvalidCoverageBackend
	}
	return backend.repository.GetCoverageReport(ctx, id)
}

var _ session.CoverageBackend = (*queuedCoverageBackend)(nil)

func (r *Runtime) resolveCoverageStart(ctx context.Context, input session.CoverageRunStart) (coveragecoord.QueuedStartInput, error) {
	if err := r.requireCoverageRuntime(); err != nil {
		return coveragecoord.QueuedStartInput{}, err
	}
	if input.WorkspaceGeneration == "" || input.ProjectID == "" || input.CoverageProfileID == "" || input.CatalogRevision == "" {
		return coveragecoord.QueuedStartInput{}, task.ErrInvalidArgument
	}
	snapshot, err := r.coordinator.Inspect(ctx)
	if err != nil {
		return coveragecoord.QueuedStartInput{}, err
	}
	if snapshot.Generation != input.WorkspaceGeneration {
		return coveragecoord.QueuedStartInput{}, task.ErrConflict
	}
	var project workspace.ProjectConfig
	for _, candidate := range snapshot.Projects {
		if candidate.ID == input.ProjectID {
			project = candidate
			break
		}
	}
	if project.ID == "" {
		return coveragecoord.QueuedStartInput{}, task.ErrNotFound
	}
	var coverageProfile workspace.CoverageProfile
	for _, candidate := range snapshot.CoverageProfiles {
		if candidate.ID == input.CoverageProfileID {
			coverageProfile = candidate
			break
		}
	}
	if coverageProfile.ID == "" {
		return coveragecoord.QueuedStartInput{}, task.ErrNotFound
	}
	var profile cmake.BuildProfile
	for _, candidate := range snapshot.Profiles {
		if candidate.ID == coverageProfile.BaseBuildProfileID && candidate.ProjectID == project.ID {
			profile = candidate
			break
		}
	}
	if profile.ID == "" || profile.ToolchainID == "" {
		return coveragecoord.QueuedStartInput{}, task.ErrNotFound
	}
	var instance toolchain.Instance
	for _, candidate := range snapshot.Toolchains {
		if candidate.ID == profile.ToolchainID {
			instance = candidate
			break
		}
	}
	if instance.ID == "" {
		return coveragecoord.QueuedStartInput{}, task.ErrNotFound
	}
	catalog, err := r.loadCoverageCatalog(ctx, project.ID, profile.ID)
	if err != nil {
		return coveragecoord.QueuedStartInput{}, err
	}
	if catalog.Revision != input.CatalogRevision {
		return coveragecoord.QueuedStartInput{}, task.ErrConflict
	}
	selection, err := testdomain.ResolveSelection(catalog, input.Selection, testdomain.Limits{MaxSelectionSize: 100_000})
	if err != nil {
		return coveragecoord.QueuedStartInput{}, err
	}
	toolchainSnapshot, err := coverageToolchainSnapshot(instance, r.platform)
	if err != nil {
		return coveragecoord.QueuedStartInput{}, err
	}
	return coveragecoord.QueuedStartInput{
		Request: coveragedomain.Request{
			IdempotencyKey: input.IdempotencyKey, WorkspaceGeneration: input.WorkspaceGeneration,
			ProjectID: input.ProjectID, CoverageProfileID: input.CoverageProfileID,
			CatalogRevision: input.CatalogRevision, Selection: input.Selection,
			RepeatCount: input.RepeatCount, Timeout: input.Timeout,
		},
		Selection: selection, BuildProfileID: profile.ID, ToolchainID: instance.ID,
		Toolchain: toolchainSnapshot,
	}, nil
}

func (r *Runtime) loadCoverageCatalog(ctx context.Context, projectID, profileID string) (testdomain.Catalog, error) {
	if r == nil || r.store == nil || ctx == nil {
		return testdomain.Catalog{}, task.ErrStorageUnavailable
	}
	pageRequest := testdomain.CatalogPageRequest{ProjectID: projectID, ProfileID: profileID, Limit: testdomain.MaxCatalogPageSize}
	result := testdomain.Catalog{ProjectID: projectID, ProfileID: profileID, Containers: []testdomain.Container{}, Items: []testdomain.Item{}, Diagnostics: []testdomain.Diagnostic{}}
	for pages := 0; pages < 1000; pages++ {
		page, err := r.store.PageCatalog(ctx, pageRequest)
		if err != nil {
			return testdomain.Catalog{}, err
		}
		if result.Revision == "" {
			result.Revision = page.Revision
			result.GeneratedAt = page.GeneratedAt
			result.Partial = page.Partial
		} else if result.Revision != page.Revision {
			return testdomain.Catalog{}, testdomain.ErrCatalogStale
		}
		result.Containers = append(result.Containers, page.Containers...)
		result.Items = append(result.Items, page.Items...)
		result.Diagnostics = append(result.Diagnostics, page.Diagnostics...)
		if page.NextCursor == "" {
			return result, nil
		}
		pageRequest.Cursor = page.NextCursor
	}
	return testdomain.Catalog{}, task.ErrInvalidArgument
}

func coverageToolchainSnapshot(instance toolchain.Instance, platform string) (coveragedomain.ToolchainSnapshot, error) {
	if instance.Version == "" || instance.ID == "" {
		return coveragedomain.ToolchainSnapshot{}, coveragedomain.ErrInvalidToolchain
	}
	result := coveragedomain.ToolchainSnapshot{
		Platform:          coveragedomain.Platform(platform),
		Architecture:      coverageArchitecture(instance.TargetArchitecture),
		Compiler:          coveragedomain.CompilerSnapshot{Version: instance.Version},
		NormalizerVersion: "coverage-normalize-v1",
	}
	switch {
	case result.Platform == coveragedomain.PlatformWindows && instance.Family == toolchain.FamilyClangCL:
		result.Compiler.Family = coveragedomain.CompilerFamilyClangCL
		result.Driver = coveragedomain.DriverSnapshot{Name: coveragedomain.DriverLLVMCov, Version: instance.Version}
		result.Collector = coveragedomain.CollectorSnapshot{Name: coveragedomain.CollectorLLVMCov, Version: instance.Version}
		result.InstrumentationFingerprint = coveragellvm.InstrumentationFingerprint()
	case result.Platform == coveragedomain.PlatformLinux && instance.Family == toolchain.FamilyGCC:
		result.Compiler.Family = coveragedomain.CompilerFamilyGCC
		result.Driver = coveragedomain.DriverSnapshot{Name: coveragedomain.DriverGCov, Version: instance.Version}
		result.Collector = coveragedomain.CollectorSnapshot{Name: coveragedomain.CollectorGCovr, Version: instance.Version}
	case result.Platform == coveragedomain.PlatformLinux && instance.Family == toolchain.FamilyClang:
		result.Compiler.Family = coveragedomain.CompilerFamilyClang
		result.Driver = coveragedomain.DriverSnapshot{Name: coveragedomain.DriverLLVMCov, Version: instance.Version}
		result.Collector = coveragedomain.CollectorSnapshot{Name: coveragedomain.CollectorLLVMCov, Version: instance.Version}
	default:
		return coveragedomain.ToolchainSnapshot{}, coveragedomain.ErrInvalidToolchain
	}
	if result.Architecture == "" {
		return coveragedomain.ToolchainSnapshot{}, coveragedomain.ErrInvalidToolchain
	}
	if result.InstrumentationFingerprint == "" {
		identity, _ := json.Marshal(struct {
			Family   toolchain.Family
			Contract string
		}{instance.Family, result.NormalizerVersion})
		sum := sha256.Sum256(identity)
		result.InstrumentationFingerprint = hex.EncodeToString(sum[:])
	}
	return result, nil
}

func coverageArchitecture(value string) coveragedomain.Architecture {
	switch strings.ToLower(value) {
	case "amd64", "x86_64", "x64":
		return coveragedomain.ArchitectureX64
	case "386", "x86", "i686":
		return coveragedomain.ArchitectureX86
	case "arm64", "aarch64":
		return coveragedomain.ArchitectureARM64
	default:
		if goruntime.GOARCH == "amd64" {
			return coveragedomain.ArchitectureX64
		}
		return ""
	}
}
