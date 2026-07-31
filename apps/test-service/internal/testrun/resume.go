package testrun

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"reflect"
	"time"

	"unit-test-ide.local/test-service/internal/build"
	"unit-test-ide.local/test-service/internal/task"
	"unit-test-ide.local/test-service/internal/testdomain"
)

const maximumPersistedTimeoutMS int64 = 24 * 60 * 60 * 1000

type queuedTaskResumer interface {
	ResumeQueued(context.Context, task.ResumeRequest) (task.Task, error)
}

func (coordinator *Coordinator) ResumeDiscovery(
	ctx context.Context,
	persisted task.Task,
) (task.Task, error) {
	if coordinator == nil || ctx == nil ||
		persisted.ID == "" ||
		persisted.Kind != task.KindTestDiscovery ||
		persisted.Status != task.StatusQueued {
		return task.Task{}, task.ErrInvalidArgument
	}
	request, err := decodeDiscoveryRequest(
		persisted.Request,
	)
	if err != nil {
		return task.Task{}, task.ErrInvalidArgument
	}
	request.IdempotencyKey = persisted.IdempotencyKey
	request.WorkspaceGeneration =
		persisted.WorkspaceGeneration
	prepared, execution, encoded, err :=
		coordinator.prepareDiscovery(ctx, request)
	if err != nil {
		return task.Task{}, err
	}
	defer prepared.ReleaseIfUnadopted()
	if !bytes.Equal(encoded, persisted.Request) {
		return task.Task{}, task.ErrInvalidArgument
	}
	resumer, ok := coordinator.config.Tasks.(queuedTaskResumer)
	if !ok {
		return task.Task{}, task.ErrStorageUnavailable
	}
	return resumer.ResumeQueued(
		ctx,
		task.ResumeRequest{
			Task:              persisted,
			Plan:              prepared.Plan(),
			Boundary:          prepared.Boundary(),
			Continuation:      execution,
			ResultInterpreter: execution,
		},
	)
}

func (coordinator *Coordinator) ResumeRun(
	ctx context.Context,
	persisted task.Task,
) (task.Task, error) {
	if coordinator == nil || ctx == nil ||
		persisted.ID == "" ||
		persisted.Kind != task.KindTestRun ||
		persisted.Status != task.StatusQueued {
		return task.Task{}, task.ErrInvalidArgument
	}
	request, err := decodeRunRequest(persisted.Request)
	if err != nil {
		return task.Task{}, task.ErrInvalidArgument
	}
	request.IdempotencyKey = persisted.IdempotencyKey
	request.WorkspaceGeneration =
		persisted.WorkspaceGeneration
	prepared, err := coordinator.config.PrepareBuild(
		ctx,
		build.StartRequest{
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
		return task.Task{}, err
	}
	if nilCoordinatorPort(prepared) {
		return task.Task{}, task.ErrStorageUnavailable
	}
	defer prepared.ReleaseIfUnadopted()
	if prepared.WorkspaceGeneration() !=
		request.WorkspaceGeneration ||
		prepared.Project().ID != request.ProjectID ||
		prepared.Profile().ID != request.BuildProfileID ||
		prepared.Profile().ProjectID != request.ProjectID ||
		prepared.Toolchain().ID == "" {
		return task.Task{}, task.ErrInvalidArgument
	}
	encoded, err := encodeRunRequest(request)
	if err != nil || !bytes.Equal(encoded, persisted.Request) {
		return task.Task{}, task.ErrInvalidArgument
	}
	runID := coordinatorRunID(
		persisted.IdempotencyKey,
		persisted.Request,
	)
	run, err := coordinator.config.Runs.GetRun(ctx, runID)
	if err != nil {
		return task.Task{}, err
	}
	if run.TaskID != persisted.ID ||
		run.Status != testdomain.RunQueued ||
		run.ProjectID != request.ProjectID ||
		run.ProfileID != request.BuildProfileID ||
		run.CatalogRevision != request.CatalogRevision ||
		run.Summary.Iterations != request.RepeatCount ||
		len(run.Results) != 0 {
		return task.Task{}, task.ErrConflict
	}
	catalog, err := coordinator.config.Catalogs.GetCatalog(
		ctx,
		request.ProjectID,
		request.BuildProfileID,
	)
	if err != nil {
		return task.Task{}, err
	}
	catalog, err = testdomain.NewCatalog(catalog)
	if err != nil {
		return task.Task{}, task.ErrInvalidArgument
	}
	if catalog.Revision != request.CatalogRevision {
		return task.Task{}, testdomain.ErrCatalogStale
	}
	selection, err := Resolve(
		ctx,
		catalog,
		request.Selection,
		coordinator.config.Runs,
		coordinator.config.Limits,
	)
	if err != nil {
		return task.Task{}, err
	}
	if !reflect.DeepEqual(
		selection,
		run.SelectionSnapshot,
	) {
		return task.Task{}, task.ErrConflict
	}
	execution, err := coordinator.newRunExecution(
		prepared,
		runID,
		selection,
		request,
	)
	if err != nil {
		return task.Task{}, err
	}
	resumer, ok := coordinator.config.Tasks.(queuedTaskResumer)
	if !ok {
		return task.Task{}, task.ErrStorageUnavailable
	}
	return resumer.ResumeQueued(
		ctx,
		task.ResumeRequest{
			Task:              persisted,
			Plan:              prepared.Plan(),
			Boundary:          prepared.Boundary(),
			Continuation:      execution,
			ResultInterpreter: execution,
		},
	)
}

type persistedDiscoveryRequest struct {
	ProjectID      string   `json:"projectId"`
	BuildProfileID string   `json:"buildProfileId"`
	TargetIDs      []string `json:"targetIds"`
	Jobs           int      `json:"jobs"`
	TimeoutMS      int64    `json:"timeoutMs"`
}

func decodeDiscoveryRequest(
	value json.RawMessage,
) (DiscoveryRequest, error) {
	var wire persistedDiscoveryRequest
	if err := decodePersistedRequest(value, &wire); err != nil {
		return DiscoveryRequest{}, err
	}
	if wire.TimeoutMS < 1 ||
		wire.TimeoutMS > maximumPersistedTimeoutMS {
		return DiscoveryRequest{}, task.ErrInvalidArgument
	}
	return DiscoveryRequest{
		ProjectID:      wire.ProjectID,
		BuildProfileID: wire.BuildProfileID,
		TargetIDs:      append([]string(nil), wire.TargetIDs...),
		Jobs:           wire.Jobs,
		Timeout:        time.Duration(wire.TimeoutMS) * time.Millisecond,
	}, nil
}

type persistedRunRequest struct {
	ProjectID       string                    `json:"projectId"`
	BuildProfileID  string                    `json:"buildProfileId"`
	CatalogRevision string                    `json:"catalogRevision"`
	TargetIDs       []string                  `json:"targetIds"`
	Jobs            int                       `json:"jobs"`
	TimeoutMS       int64                     `json:"timeoutMs"`
	RepeatCount     int64                     `json:"repeatCount"`
	MaxConcurrency  int                       `json:"maxConcurrency"`
	Selection       persistedSelectionRequest `json:"selection"`
}

type persistedSelectionRequest struct {
	Mode         testdomain.SelectionMode `json:"mode"`
	ContainerIDs []testdomain.ID          `json:"containerIds,omitempty"`
	ItemIDs      []testdomain.ID          `json:"itemIds,omitempty"`
	Filter       persistedFilterRequest   `json:"filter,omitempty"`
	RunID        string                   `json:"runId,omitempty"`
}

type persistedFilterRequest struct {
	Group          string          `json:"group,omitempty"`
	Suite          string          `json:"suite,omitempty"`
	Label          string          `json:"label,omitempty"`
	NameContains   string          `json:"nameContains,omitempty"`
	IncludeItemIDs []testdomain.ID `json:"includeItemIds,omitempty"`
	ExcludeItemIDs []testdomain.ID `json:"excludeItemIds,omitempty"`
}

func decodeRunRequest(
	value json.RawMessage,
) (RunRequest, error) {
	var wire persistedRunRequest
	if err := decodePersistedRequest(value, &wire); err != nil {
		return RunRequest{}, err
	}
	if wire.TimeoutMS < 1 ||
		wire.TimeoutMS > maximumPersistedTimeoutMS {
		return RunRequest{}, task.ErrInvalidArgument
	}
	selection, err := testdomain.NewSelection(
		testdomain.Selection{
			Mode:         wire.Selection.Mode,
			ContainerIDs: append([]testdomain.ID(nil), wire.Selection.ContainerIDs...),
			ItemIDs:      append([]testdomain.ID(nil), wire.Selection.ItemIDs...),
			Filter: testdomain.Filter{
				Group:        wire.Selection.Filter.Group,
				Suite:        wire.Selection.Filter.Suite,
				Label:        wire.Selection.Filter.Label,
				NameContains: wire.Selection.Filter.NameContains,
				IncludeItemIDs: append(
					[]testdomain.ID(nil),
					wire.Selection.Filter.IncludeItemIDs...,
				),
				ExcludeItemIDs: append(
					[]testdomain.ID(nil),
					wire.Selection.Filter.ExcludeItemIDs...,
				),
			},
			RunID: wire.Selection.RunID,
		},
	)
	if err != nil {
		return RunRequest{}, err
	}
	return RunRequest{
		ProjectID:       wire.ProjectID,
		BuildProfileID:  wire.BuildProfileID,
		CatalogRevision: wire.CatalogRevision,
		TargetIDs: append(
			[]string(nil),
			wire.TargetIDs...,
		),
		Jobs:           wire.Jobs,
		Timeout:        time.Duration(wire.TimeoutMS) * time.Millisecond,
		RepeatCount:    wire.RepeatCount,
		MaxConcurrency: wire.MaxConcurrency,
		Selection:      selection,
	}, nil
}

func decodePersistedRequest(
	value json.RawMessage,
	destination any,
) error {
	decoder := json.NewDecoder(bytes.NewReader(value))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return task.ErrInvalidArgument
	}
	return nil
}
