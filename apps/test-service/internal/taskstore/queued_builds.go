package taskstore

import (
	"context"
	"encoding/json"
	"time"

	"unit-test-ide.local/test-service/internal/task"
)

func (s *Store) ReplaceQueuedPlan(
	ctx context.Context,
	taskID string,
	requestHash string,
	planFingerprint string,
	steps []task.StepSnapshot,
) (task.Task, error) {
	if s == nil || ctx == nil || taskID == "" || requestHash == "" ||
		!lowerHex(planFingerprint, 64) || validateSteps(steps) != nil {
		return task.Task{}, task.ErrInvalidArgument
	}
	for _, step := range steps {
		if step.Status != task.StepPending || step.StartedAt != nil ||
			step.FinishedAt != nil || step.ExitCode != nil || step.ErrorCode != "" {
			return task.Task{}, task.ErrInvalidArgument
		}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return task.Task{}, storageError("begin queued plan replacement", err)
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `UPDATE tasks
		SET plan_fingerprint=?, active_step=''
		WHERE task_id=? AND request_hash=?
		AND kind IN ('cmake_build','test_discovery','test_run','coverage_run')
		AND status='queued'`,
		planFingerprint, taskID, requestHash,
	)
	if err != nil {
		return task.Task{}, storageError("replace queued task plan", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return task.Task{}, storageError("read queued plan replacement", err)
	}
	if affected != 1 {
		return task.Task{}, task.ErrConflict
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM task_steps WHERE task_id=?`, taskID); err != nil {
		return task.Task{}, storageError("delete queued task steps", err)
	}
	if err := insertSteps(ctx, tx, taskID, steps); err != nil {
		return task.Task{}, err
	}
	updated, err := scanTask(tx.QueryRowContext(ctx, taskSelect+` WHERE task_id=?`, taskID))
	if err != nil {
		return task.Task{}, storageError("read replaced queued task", err)
	}
	if err := hydrateTaskSteps(ctx, tx, &updated); err != nil {
		return task.Task{}, storageError("read replaced queued task steps", err)
	}
	if err := tx.Commit(); err != nil {
		return task.Task{}, storageError("commit queued plan replacement", err)
	}
	return updated, nil
}

func (s *Store) FailQueuedBuild(
	ctx context.Context,
	taskID string,
	errorCode string,
	at time.Time,
) (task.Task, []task.Event, error) {
	return s.failQueuedTask(
		ctx,
		taskID,
		errorCode,
		at,
		task.KindCMakeBuild,
	)
}

func (s *Store) FailQueuedTask(
	ctx context.Context,
	taskID string,
	errorCode string,
	at time.Time,
) (task.Task, []task.Event, error) {
	return s.failQueuedTask(
		ctx,
		taskID,
		errorCode,
		at,
		"",
	)
}

func (s *Store) failQueuedTask(
	ctx context.Context,
	taskID string,
	errorCode string,
	at time.Time,
	expectedKind task.Kind,
) (task.Task, []task.Event, error) {
	if s == nil || ctx == nil || taskID == "" ||
		!validQueuedTaskErrorCode(errorCode) ||
		at.IsZero() {
		return task.Task{}, nil, task.ErrInvalidArgument
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return task.Task{}, nil, storageError("begin queued build failure", err)
	}
	defer tx.Rollback()
	var kind task.Kind
	if err := tx.QueryRowContext(
		ctx,
		`SELECT kind FROM tasks WHERE task_id=? AND status='queued'`,
		taskID,
	).Scan(&kind); isNoRows(err) {
		return task.Task{}, nil, task.ErrConflict
	} else if err != nil {
		return task.Task{}, nil,
			storageError("read queued task kind", err)
	}
	if expectedKind != "" && kind != expectedKind ||
		kind != task.KindCMakeBuild &&
			kind != task.KindTestDiscovery &&
			kind != task.KindTestRun {
		return task.Task{}, nil, task.ErrConflict
	}
	result, err := tx.ExecContext(ctx, `UPDATE tasks SET
		status='finished', outcome='interrupted', finished_at=?, active_step='',
		error_code=?, error_message=''
		WHERE task_id=? AND status='queued'`,
		formatTime(at), errorCode, taskID,
	)
	if err != nil {
		return task.Task{}, nil, storageError("fail queued build", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return task.Task{}, nil, storageError("read queued build failure", err)
	}
	if affected != 1 {
		return task.Task{}, nil, task.ErrConflict
	}
	if _, err := tx.ExecContext(ctx, `UPDATE task_steps SET
		status='skipped', finished_at=?, exit_code=NULL, error_code=''
		WHERE task_id=? AND status='pending'`, formatTime(at), taskID); err != nil {
		return task.Task{}, nil, storageError("skip invalid queued build steps", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM process_leases WHERE task_id=?`, taskID); err != nil {
		return task.Task{}, nil, storageError("delete invalid queued build lease", err)
	}
	drafts := make([]task.EventDraft, 0, 2)
	if kind == task.KindTestRun {
		finishedRun, err := recoverInterruptedTestRun(
			ctx,
			tx,
			taskID,
			at,
		)
		if err != nil {
			return task.Task{}, nil, err
		}
		drafts = append(drafts, finishedRun)
	}
	payload, _ := json.Marshal(
		map[string]any{"outcome": task.OutcomeInterrupted},
	)
	drafts = append(drafts, task.EventDraft{
		TaskID:  taskID,
		Type:    task.EventTaskFinished,
		At:      at,
		Payload: payload,
	})
	events, err := insertEvents(ctx, tx, drafts, s.newID)
	if err != nil {
		return task.Task{}, nil, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE tasks SET last_sequence=? WHERE task_id=?`,
		events[len(events)-1].Sequence, taskID,
	); err != nil {
		return task.Task{}, nil,
			storageError("update invalid queued task sequence", err)
	}
	updated, err := scanTask(tx.QueryRowContext(ctx, taskSelect+` WHERE task_id=?`, taskID))
	if err != nil {
		return task.Task{}, nil, storageError("read failed queued build", err)
	}
	if err := hydrateTaskSteps(ctx, tx, &updated); err != nil {
		return task.Task{}, nil, storageError("read failed queued build steps", err)
	}
	if err := tx.Commit(); err != nil {
		return task.Task{}, nil, storageError("commit queued build failure", err)
	}
	return updated, events, nil
}

func validQueuedBuildErrorCode(value string) bool {
	return validQueuedTaskErrorCode(value)
}

func validQueuedTaskErrorCode(value string) bool {
	switch value {
	case "WORKSPACE_CHANGED", "PROJECT_NOT_FOUND", "BUILD_PROFILE_NOT_FOUND",
		"TARGET_NOT_FOUND", "CONFIGURE_REQUIRED", "INVALID_TASK_SPEC",
		"WORKSPACE_TRUST_REQUIRED", "CATALOG_STALE",
		"EMPTY_TEST_SELECTION", "TEST_SELECTION_INVALID":
		return true
	default:
		return false
	}
}
