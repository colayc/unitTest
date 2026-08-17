package taskstore

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"unit-test-ide.local/test-service/internal/task"
)

type stepQueryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func insertSteps(ctx context.Context, tx *sql.Tx, taskID string, steps []task.StepSnapshot) error {
	for ordinal, step := range steps {
		if _, err := tx.ExecContext(ctx, `INSERT INTO task_steps(
			task_id, step_ordinal, step_id, step_kind, status, started_at, finished_at, exit_code, error_code
		) VALUES(?,?,?,?,?,?,?,?,?)`,
			taskID, ordinal, step.ID, string(step.Kind), string(step.Status), nullableTime(step.StartedAt),
			nullableTime(step.FinishedAt), nullableInt(step.ExitCode), step.ErrorCode,
		); err != nil {
			return storageError("create task step", err)
		}
	}
	return nil
}

func applyStepMutations(ctx context.Context, tx *sql.Tx, taskID string, mutations []task.StepMutation) error {
	for _, mutation := range mutations {
		result, err := tx.ExecContext(ctx, `UPDATE task_steps SET
			step_kind=?, status=?, started_at=?, finished_at=?, exit_code=?, error_code=?
			WHERE task_id=? AND step_id=? AND status=?`,
			string(mutation.Step.Kind), string(mutation.Step.Status), nullableTime(mutation.Step.StartedAt),
			nullableTime(mutation.Step.FinishedAt), nullableInt(mutation.Step.ExitCode), mutation.Step.ErrorCode,
			taskID, mutation.Step.ID, string(mutation.Expected),
		)
		if err != nil {
			return storageError("update task step", err)
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return storageError("read task step mutation", err)
		}
		if affected != 1 {
			return task.ErrConflict
		}
	}
	return nil
}

func appendSteps(
	ctx context.Context,
	tx *sql.Tx,
	taskID string,
	steps []task.StepSnapshot,
) error {
	if len(steps) == 0 {
		return nil
	}
	var ordinal int
	if err := tx.QueryRowContext(
		ctx,
		`SELECT COALESCE(MAX(step_ordinal) + 1, 0)
		FROM task_steps WHERE task_id=?`,
		taskID,
	).Scan(&ordinal); err != nil {
		return storageError("read next task step ordinal", err)
	}
	for _, step := range steps {
		var duplicate int
		if err := tx.QueryRowContext(
			ctx,
			`SELECT COUNT(*) FROM task_steps
			WHERE task_id=? AND step_id=?`,
			taskID,
			step.ID,
		).Scan(&duplicate); err != nil {
			return storageError("check appended task step", err)
		}
		if duplicate != 0 {
			return task.ErrConflict
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO task_steps(
			task_id, step_ordinal, step_id, step_kind, status, started_at,
			finished_at, exit_code, error_code
		) VALUES(?,?,?,?,?,?,?,?,?)`,
			taskID,
			ordinal,
			step.ID,
			string(step.Kind),
			string(step.Status),
			nullableTime(step.StartedAt),
			nullableTime(step.FinishedAt),
			nullableInt(step.ExitCode),
			step.ErrorCode,
		); err != nil {
			return storageError("append task step", err)
		}
		ordinal++
	}
	return nil
}

func hydrateTaskSteps(ctx context.Context, queryer stepQueryer, value *task.Task) error {
	rows, err := queryer.QueryContext(ctx, `SELECT
		step_id, step_kind, status, started_at, finished_at, exit_code, error_code
		FROM task_steps WHERE task_id=? ORDER BY step_ordinal`, value.ID)
	if err != nil {
		return err
	}
	defer rows.Close()
	steps := make([]task.StepSnapshot, 0)
	for rows.Next() {
		var step task.StepSnapshot
		var kind, status string
		var startedAt, finishedAt sql.NullString
		var exitCode sql.NullInt64
		if err := rows.Scan(&step.ID, &kind, &status, &startedAt, &finishedAt, &exitCode, &step.ErrorCode); err != nil {
			return err
		}
		step.Kind = task.StepKind(kind)
		step.Status = task.StepStatus(status)
		step.StartedAt, err = parseStepTime(startedAt)
		if err != nil {
			return err
		}
		step.FinishedAt, err = parseStepTime(finishedAt)
		if err != nil {
			return err
		}
		if exitCode.Valid {
			value := int(exitCode.Int64)
			step.ExitCode = &value
		}
		steps = append(steps, step)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	value.Steps = steps
	return nil
}

func parseStepTime(value sql.NullString) (*time.Time, error) {
	if !value.Valid {
		return nil, nil
	}
	parsed, err := time.Parse(time.RFC3339Nano, value.String)
	if err != nil {
		return nil, fmt.Errorf("invalid stored step time")
	}
	return &parsed, nil
}

func validateSteps(steps []task.StepSnapshot) error {
	ids := make(map[string]struct{}, len(steps))
	for _, step := range steps {
		if !validStepSnapshot(step) {
			return task.ErrInvalidArgument
		}
		if _, exists := ids[step.ID]; exists {
			return task.ErrInvalidArgument
		}
		ids[step.ID] = struct{}{}
	}
	return nil
}

func validateStepMutations(mutations []task.StepMutation) error {
	ids := make(map[string]struct{}, len(mutations))
	for _, mutation := range mutations {
		if !validStepSnapshot(mutation.Step) || !validStepStatus(mutation.Expected) {
			return task.ErrInvalidArgument
		}
		if _, exists := ids[mutation.Step.ID]; exists {
			return task.ErrInvalidArgument
		}
		ids[mutation.Step.ID] = struct{}{}
	}
	return nil
}

func validateAppendedSteps(steps []task.StepSnapshot) error {
	if err := validateSteps(steps); err != nil {
		return err
	}
	for _, step := range steps {
		if step.Status != task.StepPending ||
			step.StartedAt != nil ||
			step.FinishedAt != nil ||
			step.ExitCode != nil ||
			step.ErrorCode != "" {
			return task.ErrInvalidArgument
		}
	}
	return nil
}

func validStepSnapshot(step task.StepSnapshot) bool {
	return validStoredStepID(step.ID) && validStepKind(step.Kind) && validStepStatus(step.Status)
}

func validStoredStepID(value string) bool {
	if len(value) < 1 || len(value) > 64 {
		return false
	}
	for index := 0; index < len(value); index++ {
		character := value[index]
		if (character >= 'a' && character <= 'z') ||
			(index > 0 && character >= '0' && character <= '9') ||
			(index > 0 && (character == '-' || character == '_')) {
			continue
		}
		return false
	}
	return true
}

func validStepKind(value task.StepKind) bool {
	switch value {
	case task.StepSimulation, task.StepConfigure, task.StepBuild,
		task.StepTestDiscovery, task.StepTestRun,
		task.StepCoverageConfigure, task.StepCoverageBuild,
		task.StepCoverageTest, task.StepCoverageMerge,
		task.StepCoverageNormalize, task.StepCoverageReport,
		task.StepCoveragePublish:
		return true
	default:
		return false
	}
}

func validStepStatus(value task.StepStatus) bool {
	switch value {
	case task.StepPending, task.StepRunning, task.StepSucceeded, task.StepFailed, task.StepSkipped:
		return true
	default:
		return false
	}
}
