package testrun

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"sync"
	"time"

	"unit-test-ide.local/test-service/internal/buildcontract"
	"unit-test-ide.local/test-service/internal/cmake"
	"unit-test-ide.local/test-service/internal/task"
	"unit-test-ide.local/test-service/internal/testdomain"
)

type DiscoveryRequest struct {
	IdempotencyKey      string
	WorkspaceGeneration string
	ProjectID           string
	BuildProfileID      string
	TargetIDs           []string
	Jobs                int
	Timeout             time.Duration
}

func (coordinator *Coordinator) StartDiscovery(
	ctx context.Context,
	request DiscoveryRequest,
) (task.Task, error) {
	prepared, execution, encoded, err :=
		coordinator.prepareDiscovery(ctx, request)
	if err != nil {
		return task.Task{}, err
	}
	defer prepared.ReleaseIfUnadopted()
	started, err := coordinator.config.Tasks.Start(
		ctx,
		task.StartRequest{
			IdempotencyKey:      request.IdempotencyKey,
			Kind:                task.KindTestDiscovery,
			Request:             encoded,
			WorkspaceGeneration: request.WorkspaceGeneration,
			Timeout:             request.Timeout,
			Plan:                prepared.Plan(),
			Boundary:            prepared.Boundary(),
			Continuation:        execution,
			ResultInterpreter:   execution,
		},
	)
	if err != nil {
		return task.Task{}, fmt.Errorf(
			"start test discovery task: %w",
			err,
		)
	}
	return started, nil
}

func (coordinator *Coordinator) prepareDiscovery(
	ctx context.Context,
	request DiscoveryRequest,
) (PreparedBuild, *discoveryExecution, []byte, error) {
	if coordinator == nil || ctx == nil ||
		!lowerHexID(request.IdempotencyKey, 32) ||
		!lowerHexID(request.WorkspaceGeneration, 64) ||
		request.ProjectID == "" ||
		!lowerHexID(request.BuildProfileID, 64) ||
		request.Jobs < 1 || request.Jobs > 256 ||
		request.Timeout < time.Millisecond ||
		request.Timeout > 24*time.Hour ||
		request.Timeout%time.Millisecond != 0 {
		return nil, nil, nil, fmt.Errorf(
			"validate test discovery request: %w",
			task.ErrInvalidArgument,
		)
	}
	targetIDs, err := canonicalCoordinatorTargetIDs(
		request.TargetIDs,
	)
	if err != nil {
		return nil, nil, nil, fmt.Errorf(
			"canonicalize test discovery targets: %w",
			err,
		)
	}
	request.TargetIDs = targetIDs
	prepared, err := coordinator.config.PrepareBuild(
		ctx,
		BuildRequest{
			IdempotencyKey:      request.IdempotencyKey,
			WorkspaceGeneration: request.WorkspaceGeneration,
			ProjectID:           request.ProjectID,
			BuildProfileID:      request.BuildProfileID,
			TargetIDs: append(
				[]string(nil),
				request.TargetIDs...,
			),
			Jobs:    request.Jobs,
			Timeout: request.Timeout,
		},
	)
	if err != nil {
		return nil, nil, nil, fmt.Errorf(
			"prepare test discovery build: %w",
			err,
		)
	}
	fail := func(cause error) (
		PreparedBuild,
		*discoveryExecution,
		[]byte,
		error,
	) {
		if !nilCoordinatorPort(prepared) {
			prepared.ReleaseIfUnadopted()
		}
		return nil, nil, nil, cause
	}
	if nilCoordinatorPort(prepared) {
		return fail(fmt.Errorf(
			"test discovery build is unavailable: %w",
			task.ErrStorageUnavailable,
		))
	}
	project := prepared.Project()
	profile := prepared.Profile()
	instance := prepared.Toolchain()
	if prepared.WorkspaceGeneration() !=
		request.WorkspaceGeneration {
		return fail(fmt.Errorf(
			"test discovery workspace generation changed: %w",
			buildcontract.ErrWorkspaceChanged,
		))
	}
	if project.ID != request.ProjectID {
		return fail(fmt.Errorf(
			"test discovery project changed: %w",
			buildcontract.ErrProjectNotFound,
		))
	}
	if profile.ID != request.BuildProfileID ||
		profile.ProjectID != project.ID {
		return fail(fmt.Errorf(
			"test discovery build profile changed: %w",
			buildcontract.ErrBuildProfileNotFound,
		))
	}
	if instance.ID == "" {
		return fail(fmt.Errorf(
			"test discovery toolchain is unavailable: %w",
			task.ErrInvalidArgument,
		))
	}
	plan := prepared.Plan()
	if len(plan.Steps) == 0 {
		return fail(fmt.Errorf(
			"validate test discovery build plan: %w",
			task.ErrInvalidArgument,
		))
	}
	encoded, err := encodeDiscoveryRequest(request)
	if err != nil {
		return fail(fmt.Errorf(
			"encode test discovery request: %w",
			task.ErrInvalidArgument,
		))
	}
	execution := &discoveryExecution{
		lastBuildStep: plan.Steps[len(plan.Steps)-1].ID,
		prepared:      prepared,
		refresher:     coordinator.config.Refresher,
		runtimeSteps:  len(plan.Steps),
	}
	return prepared, execution, encoded, nil
}

type discoveryExecution struct {
	mu sync.Mutex

	lastBuildStep string
	prepared      PreparedBuild
	refresher     CatalogRefresher
	runtimeSteps  int

	taskID            string
	refresh           CatalogRefresh
	refreshLastIssued string
	pinned            map[string]struct{}
	complete          bool
	events            []task.DomainEvent
	discoveryStarted  bool
	catalogPublished  bool
}

func (execution *discoveryExecution) AfterStep(
	ctx context.Context,
	current task.Task,
	step task.ExecutionStep,
	result task.StepResult,
) (task.Continuation, error) {
	if execution == nil || ctx == nil ||
		current.ID == "" ||
		current.Kind != task.KindTestDiscovery ||
		result.Verdict != task.StepVerdictSucceeded {
		return task.Continuation{}, task.ErrInvalidArgument
	}
	execution.mu.Lock()
	defer execution.mu.Unlock()
	if execution.complete {
		return task.Continuation{}, task.ErrConflict
	}
	if execution.taskID == "" {
		execution.taskID = current.ID
	}
	if execution.taskID != current.ID {
		return task.Continuation{}, task.ErrInvalidArgument
	}
	if execution.refresh == nil {
		if step.ID != execution.lastBuildStep {
			return task.Continuation{}, nil
		}
		refresh, progress, err :=
			execution.refresher.PrepareAfterBuild(
				ctx,
				RefreshRequest{
					TaskID:              current.ID,
					WorkspaceGeneration: execution.prepared.WorkspaceGeneration(),
					Project:             execution.prepared.Project(),
					Profile:             execution.prepared.Profile(),
					Toolchain:           execution.prepared.Toolchain(),
					Targets:             execution.prepared.Targets(),
				},
			)
		if err != nil {
			return task.Continuation{}, err
		}
		if nilCoordinatorPort(refresh) {
			return task.Continuation{},
				task.ErrStorageUnavailable
		}
		execution.refresh = refresh
		if progress.Snapshot == nil &&
			len(progress.Steps) == 0 {
			return task.Continuation{},
				task.ErrInvalidArgument
		}
		return execution.applyProgress(progress)
	}
	if step.Kind != task.StepTestDiscovery {
		return task.Continuation{}, nil
	}
	progress, err := execution.refresh.AfterStep(ctx, step)
	if err != nil {
		return task.Continuation{}, err
	}
	if progress.Snapshot == nil &&
		len(progress.Steps) == 0 &&
		step.ID == execution.refreshLastIssued {
		return task.Continuation{},
			task.ErrInvalidArgument
	}
	return execution.applyProgress(progress)
}

func (execution *discoveryExecution) applyProgress(
	progress RefreshProgress,
) (task.Continuation, error) {
	if progress.Snapshot != nil &&
		len(progress.Steps) != 0 {
		return task.Continuation{}, task.ErrInvalidArgument
	}
	if len(progress.Steps) >
		maxCoordinatorRuntimeSteps-execution.runtimeSteps {
		return task.Continuation{}, task.ErrInvalidArgument
	}
	for _, step := range progress.Steps {
		if step.Kind != task.StepTestDiscovery {
			return task.Continuation{},
				task.ErrInvalidArgument
		}
	}
	if err := execution.pinFiles(progress.Pins); err != nil {
		return task.Continuation{}, err
	}
	if progress.Snapshot == nil {
		if len(progress.Steps) != 0 {
			execution.runtimeSteps += len(progress.Steps)
			execution.refreshLastIssued =
				progress.Steps[len(progress.Steps)-1].ID
		}
		return task.Continuation{
			Steps: cloneCoordinatorSteps(progress.Steps),
		}, nil
	}
	catalog, err := testdomain.NewCatalog(
		progress.Snapshot.Catalog,
	)
	if err != nil ||
		catalog.ProjectID != execution.prepared.Project().ID ||
		catalog.ProfileID != execution.prepared.Profile().ID {
		return task.Continuation{}, task.ErrInvalidArgument
	}
	if err := execution.ensureDiscoveryStarted(); err != nil {
		return task.Continuation{}, err
	}
	if err := execution.recordCatalogPublished(catalog); err != nil {
		return task.Continuation{}, err
	}
	execution.complete = true
	execution.refreshLastIssued = ""
	return task.Continuation{}, nil
}

func (execution *discoveryExecution) Interpret(
	ctx context.Context,
	_ task.Task,
	step task.ExecutionStep,
	result task.ProcessResult,
) (task.StepVerdict, error) {
	if execution == nil || ctx == nil {
		return task.StepVerdictDefault,
			task.ErrInvalidArgument
	}
	execution.mu.Lock()
	refresh := execution.refresh
	complete := execution.complete
	if step.Kind == task.StepTestDiscovery &&
		!complete && refresh != nil {
		if err := execution.ensureDiscoveryStarted(); err != nil {
			execution.mu.Unlock()
			return task.StepVerdictDefault, err
		}
	}
	execution.mu.Unlock()
	if step.Kind == task.StepTestDiscovery &&
		!complete && refresh != nil {
		return refresh.Interpret(ctx, step, result)
	}
	return task.StepVerdictDefault, nil
}

func (execution *discoveryExecution) ObserveOutput(
	ctx context.Context,
	_ task.Task,
	step task.ExecutionStep,
	output task.ProcessOutput,
) error {
	if execution == nil || ctx == nil {
		return task.ErrInvalidArgument
	}
	execution.mu.Lock()
	refresh := execution.refresh
	complete := execution.complete
	execution.mu.Unlock()
	if step.Kind == task.StepTestDiscovery &&
		!complete && refresh != nil {
		return refresh.ObserveOutput(ctx, step, output)
	}
	return nil
}

func (execution *discoveryExecution) DrainDomainEvents() []task.DomainEvent {
	if execution == nil {
		return nil
	}
	execution.mu.Lock()
	defer execution.mu.Unlock()
	return drainDomainEvents(&execution.events)
}

func (execution *discoveryExecution) pinFiles(
	files []cmake.FingerprintFile,
) error {
	if execution.pinned == nil {
		execution.pinned = make(map[string]struct{})
	}
	for _, state := range files {
		if state.Path == "" || state.Identity == "" ||
			state.SHA256 == "" {
			return task.ErrInvalidArgument
		}
		key := state.Path + "\x00" + state.Identity +
			"\x00" + state.SHA256
		if _, exists := execution.pinned[key]; exists {
			continue
		}
		if err := execution.prepared.AllowTestExecutable(
			state,
		); err != nil {
			return err
		}
		execution.pinned[key] = struct{}{}
	}
	return nil
}

func (execution *discoveryExecution) ensureDiscoveryStarted() error {
	if execution.discoveryStarted {
		return nil
	}
	if nilCoordinatorPort(execution.prepared) {
		return task.ErrInvalidArgument
	}
	if err := appendDomainEvent(
		&execution.events,
		task.EventTestDiscoveryStarted,
		map[string]any{
			"projectId": execution.prepared.Project().ID,
			"profileId": execution.prepared.Profile().ID,
		},
	); err != nil {
		return err
	}
	execution.discoveryStarted = true
	return nil
}

func (execution *discoveryExecution) recordCatalogPublished(
	catalog testdomain.Catalog,
) error {
	if execution.catalogPublished {
		return nil
	}
	for _, container := range catalog.Containers {
		if err := appendDomainEvent(
			&execution.events,
			task.EventTestContainerDiscovered,
			map[string]any{
				"containerId": container.ID,
				"framework":   container.Framework,
				"displayName": container.DisplayName,
			},
		); err != nil {
			return err
		}
	}
	if err := appendDomainEvent(
		&execution.events,
		task.EventTestCatalogPublished,
		map[string]any{
			"projectId":      catalog.ProjectID,
			"profileId":      catalog.ProfileID,
			"revision":       catalog.Revision,
			"containerCount": len(catalog.Containers),
			"itemCount":      len(catalog.Items),
		},
	); err != nil {
		return err
	}
	execution.catalogPublished = true
	return nil
}

func encodeDiscoveryRequest(
	request DiscoveryRequest,
) ([]byte, error) {
	targetIDs := append([]string{}, request.TargetIDs...)
	sort.Strings(targetIDs)
	return json.Marshal(struct {
		ProjectID      string   `json:"projectId"`
		BuildProfileID string   `json:"buildProfileId"`
		TargetIDs      []string `json:"targetIds"`
		Jobs           int      `json:"jobs"`
		TimeoutMS      int64    `json:"timeoutMs"`
	}{
		ProjectID:      request.ProjectID,
		BuildProfileID: request.BuildProfileID,
		TargetIDs:      targetIDs,
		Jobs:           request.Jobs,
		TimeoutMS:      request.Timeout.Milliseconds(),
	})
}
