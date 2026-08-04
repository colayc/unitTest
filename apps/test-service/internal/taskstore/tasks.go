package taskstore

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"time"

	"unit-test-ide.local/test-service/internal/coveragedomain"
	"unit-test-ide.local/test-service/internal/task"
	"unit-test-ide.local/test-service/internal/testdomain"
)

const maxPageSize = 200

type rowScanner interface {
	Scan(...any) error
}

func (s *Store) Create(ctx context.Context, input task.Task, steps []task.StepSnapshot, event task.EventDraft) (task.Task, []task.Event, error) {
	if input.Kind == task.KindCoverageRun {
		return task.Task{}, nil, task.ErrInvalidArgument
	}
	return s.createTask(ctx, input, steps, event, nil)
}

func (s *Store) CreateTestTask(
	ctx context.Context,
	input task.Task,
	steps []task.StepSnapshot,
	event task.EventDraft,
	run testdomain.TestRun,
) (task.Task, []task.Event, error) {
	if input.Kind != task.KindTestRun ||
		run.TaskID != input.ID {
		return task.Task{}, nil, task.ErrInvalidArgument
	}
	validated, err := testdomain.NewTestRun(run)
	if err != nil || len(validated.Results) != 0 ||
		validated.Status != testdomain.RunQueued {
		return task.Task{}, nil, task.ErrInvalidArgument
	}
	return s.createTask(ctx, input, steps, event, &taskCreationRelations{
		testRun: &validated,
	})
}

func (s *Store) CreateCoverageTask(
	ctx context.Context,
	input task.Task,
	steps []task.StepSnapshot,
	event task.EventDraft,
	run coveragedomain.Run,
	testRun testdomain.TestRun,
) (task.Task, []task.Event, error) {
	if s == nil || ctx == nil {
		return task.Task{}, nil, task.ErrInvalidArgument
	}
	validatedRun, err := coveragedomain.NewRun(run)
	if err != nil {
		return task.Task{}, nil, task.ErrInvalidArgument
	}
	validatedTestRun, err := testdomain.NewTestRun(testRun)
	if err != nil {
		return task.Task{}, nil, task.ErrInvalidArgument
	}
	if err := validateCoverageTaskCreation(
		input,
		steps,
		event,
		validatedRun,
		validatedTestRun,
	); err != nil {
		return task.Task{}, nil, err
	}
	return s.createTask(ctx, input, steps, event, &taskCreationRelations{
		coverageRun: &validatedRun,
		testRun:     &validatedTestRun,
	})
}

type taskCreationRelations struct {
	coverageRun *coveragedomain.Run
	testRun     *testdomain.TestRun
}

func (s *Store) createTask(
	ctx context.Context,
	input task.Task,
	steps []task.StepSnapshot,
	event task.EventDraft,
	relations *taskCreationRelations,
) (task.Task, []task.Event, error) {
	if s == nil || ctx == nil {
		return task.Task{}, nil, task.ErrInvalidArgument
	}
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
			if relations != nil && relations.coverageRun != nil {
				if relations.testRun != nil && equivalentCoverageTaskCreation(ctx, tx, existing, input, *relations.coverageRun, *relations.testRun) {
					return existing, nil, nil
				}
				return task.Task{}, nil, task.ErrIdempotencyConflict
			}
			if existing.RequestHash == input.RequestHash ||
				task.EquivalentIdempotencyRequest(existing, input) {
				return existing, nil, nil
			}
			return task.Task{}, nil, task.ErrIdempotencyConflict
		}
		return task.Task{}, nil, storageError("create task", err)
	}
	if relations != nil && relations.coverageRun != nil {
		if err := insertCoverageRun(ctx, tx, *relations.coverageRun); err != nil {
			return task.Task{}, nil, err
		}
	}
	if relations != nil && relations.testRun != nil {
		if err := insertTestRun(ctx, tx, *relations.testRun); err != nil {
			return task.Task{}, nil, err
		}
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
	if relations != nil && relations.coverageRun != nil {
		if _, err := tx.ExecContext(ctx, `UPDATE coverage_runs SET last_sequence=? WHERE coverage_run_id=? AND task_id=?`, input.LastSequence, relations.coverageRun.ID, input.ID); err != nil {
			return task.Task{}, nil, storageError("update CoverageRun sequence", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return task.Task{}, nil, storageError("commit create", err)
	}
	input.Steps = cloneSteps(steps)
	return input, events, nil
}

func validateCoverageTaskCreation(
	input task.Task,
	steps []task.StepSnapshot,
	event task.EventDraft,
	run coveragedomain.Run,
	testRun testdomain.TestRun,
) error {
	if err := validateTask(input); err != nil ||
		input.Kind != task.KindCoverageRun || input.Status != task.StatusQueued || input.LastSequence != 0 ||
		validateSteps(steps) != nil || !validCoverageSteps(steps) ||
		event.TaskID != input.ID || event.Type != task.EventTaskCreated || !validEventDraft(event) ||
		run.Status != coveragedomain.StatusQueued || run.LastSequence != 0 ||
		testRun.Status != testdomain.RunQueued || len(testRun.Results) != 0 ||
		!zeroQueuedTestRunSummary(testRun.Summary) ||
		run.TaskID != input.ID || testRun.TaskID != input.ID || run.TestRunID != testRun.RunID ||
		run.Request.IdempotencyKey != input.IdempotencyKey || run.Request.IdempotencyKey != testRun.IdempotencyKey ||
		run.Request.WorkspaceGeneration != input.WorkspaceGeneration ||
		run.Request.ProjectID != testRun.ProjectID ||
		run.Request.CatalogRevision != testRun.CatalogRevision ||
		run.Request.Timeout != input.Timeout ||
		run.Request.RepeatCount != testRun.Summary.Iterations ||
		!reflect.DeepEqual(run.SelectionSnapshot, testRun.SelectionSnapshot) ||
		!input.CreatedAt.Equal(run.CreatedAt) || !input.CreatedAt.Equal(testRun.CreatedAt) || !input.CreatedAt.Equal(event.At) {
		return task.ErrInvalidArgument
	}
	canonical, err := run.Request.CanonicalJSON()
	if err != nil || !bytesEqual(input.Request, canonical) {
		return task.ErrInvalidArgument
	}
	return nil
}

func zeroQueuedTestRunSummary(value testdomain.RunSummary) bool {
	return value.Total == 0 && value.Completed == 0 && value.Passed == 0 &&
		value.Failed == 0 && value.Skipped == 0 && value.Errored == 0 &&
		value.Cancelled == 0 && value.TimedOut == 0 && value.NotRun == 0
}

func validCoverageSteps(steps []task.StepSnapshot) bool {
	for _, step := range steps {
		switch step.Kind {
		case task.StepCoverageConfigure, task.StepCoverageBuild,
			task.StepCoverageTest, task.StepCoverageMerge,
			task.StepCoverageNormalize, task.StepCoverageReport,
			task.StepCoveragePublish:
		default:
			return false
		}
	}
	return true
}

func bytesEqual(first, second []byte) bool {
	if len(first) != len(second) {
		return false
	}
	for index := range first {
		if first[index] != second[index] {
			return false
		}
	}
	return true
}

func equivalentCoverageTaskCreation(
	ctx context.Context,
	tx *sql.Tx,
	existing task.Task,
	incoming task.Task,
	incomingRun coveragedomain.Run,
	incomingTestRun testdomain.TestRun,
) bool {
	if !task.EquivalentIdempotencyRequest(existing, incoming) {
		return false
	}
	existingRun, err := scanCoverageRun(tx.QueryRowContext(
		ctx,
		coverageRunSelect+` WHERE task_id=?`,
		existing.ID,
	))
	if err != nil || existingRun.Status != coveragedomain.StatusQueued ||
		existingRun.TaskID != existing.ID || existingRun.LastSequence != existing.LastSequence {
		return false
	}
	existingTestRun, err := scanTestRun(tx.QueryRowContext(
		ctx,
		testRunSelect+` WHERE run_id=? AND task_id=?`,
		existingRun.TestRunID,
		existing.ID,
	))
	if err != nil || existingTestRun.Status != testdomain.RunQueued ||
		existingTestRun.TaskID != existing.ID ||
		existingTestRun.IdempotencyKey != existing.IdempotencyKey {
		return false
	}
	var resultCount int
	if err := tx.QueryRowContext(
		ctx,
		`SELECT COUNT(*) FROM test_run_results WHERE run_id=?`,
		existingTestRun.RunID,
	).Scan(&resultCount); err != nil || resultCount != 0 {
		return false
	}

	incomingRun.TaskID = existing.ID
	incomingRun.TestRunID = existingTestRun.RunID
	incomingRun.CreatedAt = existingRun.CreatedAt
	incomingRun.LastSequence = existingRun.LastSequence
	incomingTestRun.RunID = existingTestRun.RunID
	incomingTestRun.TaskID = existing.ID
	incomingTestRun.CreatedAt = existingTestRun.CreatedAt
	incomingTestRun.Results = nil
	return reflect.DeepEqual(existingRun, incomingRun) &&
		reflect.DeepEqual(existingTestRun, incomingTestRun)
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

func (s *Store) List(ctx context.Context, cursor string, limit int, kinds ...task.Kind) (task.Page[task.Task], error) {
	if limit < 1 || limit > maxPageSize || len(kinds) > 5 {
		return task.Page[task.Task]{}, task.ErrInvalidArgument
	}
	query := taskSelect
	args := make([]any, 0, 5)
	conditions := make([]string, 0, 2)
	if len(kinds) > 0 {
		seen := make(map[task.Kind]struct{}, len(kinds))
		placeholders := make([]string, 0, len(kinds))
		for _, kind := range kinds {
			if !validTaskKind(kind) {
				return task.Page[task.Task]{}, task.ErrInvalidArgument
			}
			if _, exists := seen[kind]; exists {
				continue
			}
			seen[kind] = struct{}{}
			placeholders = append(placeholders, "?")
			args = append(args, string(kind))
		}
		conditions = append(conditions, "kind IN ("+strings.Join(placeholders, ",")+")")
	}
	if cursor != "" {
		createdAt, taskID, err := decodeCursor(cursor)
		if err != nil {
			return task.Page[task.Task]{}, task.ErrInvalidArgument
		}
		conditions = append(conditions, `(created_at < ? OR (created_at = ? AND task_id < ?))`)
		args = append(args, formatTime(createdAt), formatTime(createdAt), taskID)
	}
	if len(conditions) > 0 {
		query += ` WHERE ` + strings.Join(conditions, ` AND `)
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
	if err := validateAppendedSteps(mutation.AppendSteps); err != nil {
		return task.Task{}, nil, err
	}
	if len(mutation.AppendSteps) != 0 &&
		(mutation.Expected != task.StatusRunning ||
			mutation.Task.Status != task.StatusRunning) {
		return task.Task{}, nil, task.ErrInvalidArgument
	}
	mutatedStepIDs := make(map[string]struct{}, len(mutation.Steps))
	for _, changed := range mutation.Steps {
		mutatedStepIDs[changed.Step.ID] = struct{}{}
	}
	for _, appended := range mutation.AppendSteps {
		if _, exists := mutatedStepIDs[appended.ID]; exists {
			return task.Task{}, nil, task.ErrInvalidArgument
		}
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
	if mutation.FinishRun != nil {
		run, err := testdomain.NewTestRun(mutation.FinishRun.Clone())
		if err != nil {
			return task.Task{}, nil, fmt.Errorf(
				"validate terminal TestRun: %w",
				task.ErrInvalidArgument,
			)
		}
		if run.Status != testdomain.RunCompleted ||
			mutation.Task.Kind != task.KindTestRun ||
			mutation.Task.Status != task.StatusFinished ||
			run.TaskID != mutation.Task.ID {
			return task.Task{}, nil, fmt.Errorf(
				"match terminal TestRun to Task: %w",
				task.ErrInvalidArgument,
			)
		}
		if err := validateRunArtifacts(run.TaskID, mutation.Artifacts); err != nil {
			return task.Task{}, nil, err
		}
		hasRunFinished := false
		for _, event := range mutation.Events {
			if event.Type == task.EventTestRunFinished {
				hasRunFinished = true
				break
			}
		}
		if !hasRunFinished {
			return task.Task{}, nil, fmt.Errorf(
				"terminal TestRun event is missing: %w",
				task.ErrInvalidArgument,
			)
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
	if err := appendSteps(
		ctx,
		tx,
		mutation.Task.ID,
		mutation.AppendSteps,
	); err != nil {
		return task.Task{}, nil, err
	}
	events, err := insertEvents(ctx, tx, mutation.Events, s.newID)
	if err != nil {
		return task.Task{}, nil, fmt.Errorf(
			"insert Task mutation events: %w",
			err,
		)
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
			return task.Task{}, nil, fmt.Errorf(
				"insert Task mutation artifact: %w",
				err,
			)
		}
	}
	if mutation.FinishRun != nil {
		if err := finishRunTx(
			ctx,
			tx,
			mutation.FinishRun.Clone(),
			mutation.Artifacts,
			false,
		); err != nil {
			return task.Task{}, nil, fmt.Errorf(
				"finish TestRun in Task mutation: %w",
				err,
			)
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
	case task.KindCMakeBuild, task.KindTestDiscovery, task.KindTestRun, task.KindCoverageRun:
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

func validTaskKind(value task.Kind) bool {
	switch value {
	case task.KindSimulation, task.KindCMakeBuild,
		task.KindTestDiscovery, task.KindTestRun, task.KindCoverageRun:
		return true
	default:
		return false
	}
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
