package taskstore

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"unit-test-ide.local/test-service/internal/task"
)

const maxPageSize = 200

type rowScanner interface {
	Scan(...any) error
}

func (s *Store) Create(ctx context.Context, input task.Task, steps []task.StepSnapshot, event task.EventDraft) (task.Task, []task.Event, error) {
	if err := validateTask(input); err != nil {
		return task.Task{}, nil, err
	}
	if err := validateSteps(steps); err != nil {
		return task.Task{}, nil, err
	}
	if event.TaskID != input.ID || !validEventDraft(event) {
		return task.Task{}, nil, task.ErrInvalidArgument
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return task.Task{}, nil, storageError("begin create", err)
	}
	defer tx.Rollback()

	_, err = tx.ExecContext(ctx, `INSERT INTO tasks(
		task_id, idempotency_key, request_hash, kind, scenario, request_json, workspace_generation,
		plan_fingerprint, active_step, timeout_ms, status, outcome, created_at, started_at,
		finished_at, last_sequence, error_code, error_message
	) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		input.ID, input.IdempotencyKey, input.RequestHash, string(input.Kind), nullableScenario(input),
		string(input.Request), input.WorkspaceGeneration, input.PlanFingerprint, input.ActiveStep, input.Timeout.Milliseconds(),
		string(input.Status), nullableOutcome(input), formatTime(input.CreatedAt), nullableTime(input.StartedAt),
		nullableTime(input.FinishedAt), input.LastSequence, input.ErrorCode, input.ErrorMessage,
	)
	if err != nil {
		existing, findErr := findTaskByIdempotencyKey(ctx, tx, input.IdempotencyKey)
		if findErr == nil {
			if existing.RequestHash == input.RequestHash {
				return existing, nil, nil
			}
			return task.Task{}, nil, task.ErrIdempotencyConflict
		}
		return task.Task{}, nil, storageError("create task", err)
	}
	if err := insertSteps(ctx, tx, input.ID, steps); err != nil {
		return task.Task{}, nil, err
	}
	events, err := insertEvents(ctx, tx, []task.EventDraft{event}, s.newID)
	if err != nil {
		return task.Task{}, nil, err
	}
	input.LastSequence = events[len(events)-1].Sequence
	if _, err := tx.ExecContext(ctx, `UPDATE tasks SET last_sequence=? WHERE task_id=?`, input.LastSequence, input.ID); err != nil {
		return task.Task{}, nil, storageError("update task sequence", err)
	}
	if err := tx.Commit(); err != nil {
		return task.Task{}, nil, storageError("commit create", err)
	}
	input.Steps = cloneSteps(steps)
	return input, events, nil
}

func (s *Store) FindByIdempotencyKey(ctx context.Context, key string) (task.Task, error) {
	result, err := findTaskByIdempotencyKey(ctx, s.db, key)
	if isNoRows(err) {
		return task.Task{}, task.ErrNotFound
	}
	if err != nil {
		return task.Task{}, storageError("find task", err)
	}
	return result, nil
}

type taskQueryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func findTaskByIdempotencyKey(ctx context.Context, queryer taskQueryer, key string) (task.Task, error) {
	result, err := scanTask(queryer.QueryRowContext(ctx, taskSelect+` WHERE idempotency_key=?`, key))
	if err != nil {
		return task.Task{}, err
	}
	if err := hydrateTaskSteps(ctx, queryer, &result); err != nil {
		return task.Task{}, err
	}
	return result, nil
}

func (s *Store) Get(ctx context.Context, taskID string) (task.Task, error) {
	result, err := scanTask(s.db.QueryRowContext(ctx, taskSelect+` WHERE task_id=?`, taskID))
	if isNoRows(err) {
		return task.Task{}, task.ErrNotFound
	}
	if err != nil {
		return task.Task{}, storageError("get task", err)
	}
	if err := hydrateTaskSteps(ctx, s.db, &result); err != nil {
		return task.Task{}, storageError("get task steps", err)
	}
	return result, nil
}

func (s *Store) List(ctx context.Context, cursor string, limit int) (task.Page[task.Task], error) {
	if limit < 1 || limit > maxPageSize {
		return task.Page[task.Task]{}, task.ErrInvalidArgument
	}
	query := taskSelect
	args := make([]any, 0, 3)
	if cursor != "" {
		createdAt, taskID, err := decodeCursor(cursor)
		if err != nil {
			return task.Page[task.Task]{}, task.ErrInvalidArgument
		}
		query += ` WHERE (created_at < ? OR (created_at = ? AND task_id < ?))`
		args = append(args, formatTime(createdAt), formatTime(createdAt), taskID)
	}
	query += ` ORDER BY created_at DESC, task_id DESC LIMIT ?`
	args = append(args, limit+1)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return task.Page[task.Task]{}, storageError("list tasks", err)
	}
	defer rows.Close()
	items := make([]task.Task, 0, limit+1)
	for rows.Next() {
		item, err := scanTask(rows)
		if err != nil {
			return task.Page[task.Task]{}, storageError("read tasks", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return task.Page[task.Task]{}, storageError("list tasks", err)
	}
	if err := rows.Close(); err != nil {
		return task.Page[task.Task]{}, storageError("close tasks", err)
	}
	for index := range items {
		if err := hydrateTaskSteps(ctx, s.db, &items[index]); err != nil {
			return task.Page[task.Task]{}, storageError("read task steps", err)
		}
	}
	page := task.Page[task.Task]{Items: items}
	if len(items) > limit {
		last := items[limit-1]
		page.Items = items[:limit]
		page.NextCursor = encodeCursor(last.CreatedAt, last.ID)
	}
	return page, nil
}

func (s *Store) Apply(ctx context.Context, mutation task.Mutation) (task.Task, []task.Event, error) {
	if mutation.Task.ID == "" || !validStatus(mutation.Expected) {
		return task.Task{}, nil, task.ErrInvalidArgument
	}
	if err := validateTask(mutation.Task); err != nil {
		return task.Task{}, nil, err
	}
	if err := validateStepMutations(mutation.Steps); err != nil {
		return task.Task{}, nil, err
	}
	for _, event := range mutation.Events {
		if event.TaskID != mutation.Task.ID || !validEventDraft(event) {
			return task.Task{}, nil, task.ErrInvalidArgument
		}
	}
	if mutation.PutLease != nil {
		if mutation.PutLease.TaskID != mutation.Task.ID || mutation.Task.Status == task.StatusFinished {
			return task.Task{}, nil, task.ErrInvalidArgument
		}
	}
	for _, artifact := range mutation.Artifacts {
		if artifact.TaskID != mutation.Task.ID {
			return task.Task{}, nil, task.ErrInvalidArgument
		}
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return task.Task{}, nil, storageError("begin mutation", err)
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `UPDATE tasks SET
		kind=?, scenario=?, request_json=?, workspace_generation=?, plan_fingerprint=?, active_step=?,
		timeout_ms=?, status=?, outcome=?, started_at=?, finished_at=?, error_code=?, error_message=?
		WHERE task_id=? AND status=?`,
		string(mutation.Task.Kind), nullableScenario(mutation.Task), string(mutation.Task.Request),
		mutation.Task.WorkspaceGeneration, mutation.Task.PlanFingerprint, mutation.Task.ActiveStep,
		mutation.Task.Timeout.Milliseconds(), string(mutation.Task.Status), nullableOutcome(mutation.Task),
		nullableTime(mutation.Task.StartedAt), nullableTime(mutation.Task.FinishedAt), mutation.Task.ErrorCode,
		mutation.Task.ErrorMessage, mutation.Task.ID, string(mutation.Expected))
	if err != nil {
		return task.Task{}, nil, storageError("update task", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return task.Task{}, nil, storageError("read mutation result", err)
	}
	if affected != 1 {
		return task.Task{}, nil, task.ErrConflict
	}
	if err := applyStepMutations(ctx, tx, mutation.Task.ID, mutation.Steps); err != nil {
		return task.Task{}, nil, err
	}
	events, err := insertEvents(ctx, tx, mutation.Events, s.newID)
	if err != nil {
		return task.Task{}, nil, err
	}
	if mutation.PutLease != nil {
		if err := upsertLease(ctx, tx, *mutation.PutLease); err != nil {
			return task.Task{}, nil, err
		}
	}
	if mutation.DeleteLease {
		if _, err := tx.ExecContext(ctx, `DELETE FROM process_leases WHERE task_id=?`, mutation.Task.ID); err != nil {
			return task.Task{}, nil, storageError("delete lease", err)
		}
	}
	for _, artifact := range mutation.Artifacts {
		if err := insertArtifact(ctx, tx, artifact); err != nil {
			return task.Task{}, nil, err
		}
	}
	var last int64
	if len(events) > 0 {
		last = events[len(events)-1].Sequence
	} else if err := tx.QueryRowContext(ctx, `SELECT last_sequence FROM tasks WHERE task_id=?`, mutation.Task.ID).Scan(&last); err != nil {
		return task.Task{}, nil, storageError("read task sequence", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE tasks SET last_sequence=? WHERE task_id=?`, last, mutation.Task.ID); err != nil {
		return task.Task{}, nil, storageError("update task sequence", err)
	}
	mutation.Task.LastSequence = last
	if err := hydrateTaskSteps(ctx, tx, &mutation.Task); err != nil {
		return task.Task{}, nil, storageError("read mutated task steps", err)
	}
	if err := tx.Commit(); err != nil {
		return task.Task{}, nil, storageError("commit mutation", err)
	}
	return mutation.Task, events, nil
}

func validateTask(value task.Task) error {
	if value.ID == "" || value.IdempotencyKey == "" || value.RequestHash == "" || !json.Valid(value.Request) ||
		value.Timeout < time.Millisecond || value.Timeout > 24*time.Hour || value.Timeout%time.Millisecond != 0 ||
		!validStatus(value.Status) || value.CreatedAt.IsZero() {
		return task.ErrInvalidArgument
	}
	switch value.Kind {
	case task.KindSimulation:
		if !task.ValidScenario(value.Scenario) || value.WorkspaceGeneration != "" {
			return task.ErrInvalidArgument
		}
	case task.KindCMakeBuild:
		if value.Scenario != "" || !validWorkspaceGeneration(value.WorkspaceGeneration) {
			return task.ErrInvalidArgument
		}
	default:
		return task.ErrInvalidArgument
	}
	if value.Status == task.StatusFinished {
		if !validOutcome(value.Outcome) || value.FinishedAt == nil {
			return task.ErrInvalidArgument
		}
	} else if value.Outcome != "" || value.FinishedAt != nil {
		return task.ErrInvalidArgument
	}
	return nil
}

func validWorkspaceGeneration(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, character := range value {
		if character < '0' || character > '9' {
			if character < 'a' || character > 'f' {
				return false
			}
		}
	}
	return true
}

func cloneSteps(steps []task.StepSnapshot) []task.StepSnapshot {
	cloned := make([]task.StepSnapshot, len(steps))
	copy(cloned, steps)
	return cloned
}

func validStatus(value task.Status) bool {
	switch value {
	case task.StatusQueued, task.StatusRunning, task.StatusCancelling, task.StatusFinished:
		return true
	default:
		return false
	}
}

func validOutcome(value task.Outcome) bool {
	switch value {
	case task.OutcomeSucceeded, task.OutcomeCommandFailed, task.OutcomeCancelled, task.OutcomeTimedOut, task.OutcomeInterrupted, task.OutcomeInfrastructureFailed:
		return true
	default:
		return false
	}
}

const taskSelect = `SELECT task_id, idempotency_key, request_hash, kind, request_json,
	workspace_generation, plan_fingerprint, active_step, scenario, timeout_ms, status, outcome,
	created_at, started_at, finished_at, last_sequence, error_code, error_message FROM tasks`

func scanTask(row rowScanner) (task.Task, error) {
	var result task.Task
	var timeoutMillis int64
	var kind, requestJSON, status string
	var scenario, outcome, startedAt, finishedAt sql.NullString
	var createdAt string
	if err := row.Scan(&result.ID, &result.IdempotencyKey, &result.RequestHash, &kind, &requestJSON,
		&result.WorkspaceGeneration, &result.PlanFingerprint, &result.ActiveStep, &scenario, &timeoutMillis,
		&status, &outcome, &createdAt, &startedAt, &finishedAt, &result.LastSequence, &result.ErrorCode, &result.ErrorMessage); err != nil {
		return task.Task{}, err
	}
	result.Kind = task.Kind(kind)
	result.Request = json.RawMessage(requestJSON)
	result.Scenario = task.Scenario(scenario.String)
	result.Timeout = time.Duration(timeoutMillis) * time.Millisecond
	result.Status = task.Status(status)
	result.Outcome = task.Outcome(outcome.String)
	parsedCreated, err := time.Parse(time.RFC3339Nano, createdAt)
	if err != nil {
		return task.Task{}, fmt.Errorf("invalid created time")
	}
	result.CreatedAt = parsedCreated
	result.StartedAt, err = parseNullableTime(startedAt)
	if err != nil {
		return task.Task{}, err
	}
	result.FinishedAt, err = parseNullableTime(finishedAt)
	if err != nil {
		return task.Task{}, err
	}
	return result, nil
}

func parseNullableTime(value sql.NullString) (*time.Time, error) {
	if !value.Valid {
		return nil, nil
	}
	parsed, err := time.Parse(time.RFC3339Nano, value.String)
	if err != nil {
		return nil, fmt.Errorf("invalid stored time")
	}
	return &parsed, nil
}

func encodeCursor(at time.Time, id string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(formatTime(at) + "\n" + id))
}

func decodeCursor(cursor string) (time.Time, string, error) {
	decoded, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return time.Time{}, "", err
	}
	parts := strings.Split(string(decoded), "\n")
	if len(parts) != 2 || parts[1] == "" {
		return time.Time{}, "", fmt.Errorf("invalid cursor")
	}
	at, err := time.Parse(time.RFC3339Nano, parts[0])
	if err != nil {
		return time.Time{}, "", err
	}
	return at, parts[1], nil
}
