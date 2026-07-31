package taskstore

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"time"

	"unit-test-ide.local/test-service/internal/task"
	"unit-test-ide.local/test-service/internal/testdomain"
)

const testRunSelect = `SELECT
	run_id, task_id, idempotency_key, project_id, profile_id, toolchain_id,
	catalog_revision, selection_json, status, outcome, created_at, started_at,
	finished_at, summary_json, result_revision, incomplete
	FROM test_runs`

func (s *Store) CreateRun(
	ctx context.Context,
	value testdomain.TestRun,
) error {
	if s == nil || ctx == nil {
		return task.ErrInvalidArgument
	}
	run, err := testdomain.NewTestRun(value)
	if err != nil || len(run.Results) != 0 {
		return task.ErrInvalidArgument
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return storageError("begin TestRun creation", err)
	}
	defer tx.Rollback()
	existing, findErr := scanTestRun(tx.QueryRowContext(
		ctx,
		testRunSelect+` WHERE idempotency_key=?`,
		run.IdempotencyKey,
	))
	if findErr == nil {
		if equivalentCreatedRun(existing, run) {
			return nil
		}
		return task.ErrIdempotencyConflict
	}
	if !isNoRows(findErr) {
		return storageError("find TestRun idempotency key", findErr)
	}
	if err := insertTestRun(ctx, tx, run); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return storageError("commit TestRun creation", err)
	}
	return nil
}

func insertTestRun(
	ctx context.Context,
	tx *sql.Tx,
	run testdomain.TestRun,
) error {
	selectionJSON, summaryJSON, err := encodeRunMetadata(run)
	if err != nil {
		return task.ErrInvalidArgument
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO test_runs(
		run_id, task_id, idempotency_key, project_id, profile_id, toolchain_id,
		catalog_revision, selection_json, status, outcome, created_at, started_at,
		finished_at, summary_json, result_revision, incomplete
	) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		run.RunID, run.TaskID, run.IdempotencyKey, run.ProjectID, run.ProfileID,
		run.ToolchainID, run.CatalogRevision, string(selectionJSON), string(run.Status),
		nullableRunOutcome(run), formatTime(run.CreatedAt), nullableTime(run.StartedAt),
		nullableTime(run.FinishedAt), string(summaryJSON), run.ResultRevision,
		run.Incomplete,
	); err != nil {
		var count int
		if countErr := tx.QueryRowContext(
			ctx,
			`SELECT COUNT(*) FROM test_runs WHERE run_id=? OR task_id=?`,
			run.RunID,
			run.TaskID,
		).Scan(&count); countErr == nil && count != 0 {
			return task.ErrConflict
		}
		return storageError("insert TestRun", err)
	}
	return nil
}

func (s *Store) GetRun(
	ctx context.Context,
	runID string,
) (testdomain.TestRun, error) {
	if s == nil || ctx == nil || !lowerHex(runID, 32) {
		return testdomain.TestRun{}, task.ErrInvalidArgument
	}
	run, err := scanTestRun(s.db.QueryRowContext(
		ctx,
		testRunSelect+` WHERE run_id=?`,
		runID,
	))
	if isNoRows(err) {
		return testdomain.TestRun{}, task.ErrNotFound
	}
	if err != nil {
		return testdomain.TestRun{}, storageError("get TestRun", err)
	}
	results, err := loadRunResults(ctx, s.db, runID)
	if err != nil {
		return testdomain.TestRun{}, err
	}
	run.Results = results
	validated, err := testdomain.NewTestRun(run)
	if err != nil {
		return testdomain.TestRun{}, storageError("validate TestRun", err)
	}
	return validated, nil
}

func (s *Store) ListRuns(
	ctx context.Context,
	request testdomain.RunPageRequest,
) (testdomain.RunPage, error) {
	if s == nil || ctx == nil ||
		request.ProjectID != "" && !validProjectID(request.ProjectID) ||
		request.ProfileID != "" && !lowerHex(request.ProfileID, 64) {
		return testdomain.RunPage{}, task.ErrInvalidArgument
	}
	limit := request.Limit
	if limit == 0 {
		limit = testdomain.DefaultRunPageSize
	}
	if limit < 1 || limit > testdomain.MaxRunPageSize {
		return testdomain.RunPage{}, task.ErrInvalidArgument
	}
	query := testRunSelect
	conditions := make([]string, 0, 3)
	args := make([]any, 0, 7)
	if request.ProjectID != "" {
		conditions = append(conditions, "project_id=?")
		args = append(args, request.ProjectID)
	}
	if request.ProfileID != "" {
		conditions = append(conditions, "profile_id=?")
		args = append(args, request.ProfileID)
	}
	if request.Cursor != "" {
		cursor, err := decodeRunCursor(request.Cursor)
		if err != nil ||
			cursor.ProjectID != request.ProjectID ||
			cursor.ProfileID != request.ProfileID {
			return testdomain.RunPage{}, task.ErrInvalidArgument
		}
		conditions = append(
			conditions,
			"(created_at < ? OR (created_at = ? AND run_id < ?))",
		)
		args = append(
			args,
			formatTime(cursor.CreatedAt),
			formatTime(cursor.CreatedAt),
			cursor.RunID,
		)
	}
	if len(conditions) != 0 {
		query += " WHERE " + strings.Join(conditions, " AND ")
	}
	query += " ORDER BY created_at DESC, run_id DESC LIMIT ?"
	args = append(args, limit+1)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return testdomain.RunPage{}, storageError("list TestRuns", err)
	}
	defer rows.Close()
	items := make([]testdomain.TestRun, 0, limit+1)
	for rows.Next() {
		run, err := scanTestRun(rows)
		if err != nil {
			return testdomain.RunPage{}, storageError("read TestRuns", err)
		}
		items = append(items, run)
	}
	if err := rows.Err(); err != nil {
		return testdomain.RunPage{}, storageError("read TestRuns", err)
	}
	page := testdomain.RunPage{Items: items}
	if len(items) > limit {
		last := items[limit-1]
		page.Items = items[:limit]
		page.NextCursor = encodeRunCursor(runCursor{
			ProjectID: request.ProjectID,
			ProfileID: request.ProfileID,
			CreatedAt: last.CreatedAt,
			RunID:     last.RunID,
		})
	}
	return page, nil
}

func scanTestRun(row rowScanner) (testdomain.TestRun, error) {
	var (
		run                        testdomain.TestRun
		selectionJSON, summaryJSON []byte
		status                     string
		outcome                    sql.NullString
		createdAt                  string
		startedAt, finishedAt      sql.NullString
		incomplete                 bool
	)
	if err := row.Scan(
		&run.RunID,
		&run.TaskID,
		&run.IdempotencyKey,
		&run.ProjectID,
		&run.ProfileID,
		&run.ToolchainID,
		&run.CatalogRevision,
		&selectionJSON,
		&status,
		&outcome,
		&createdAt,
		&startedAt,
		&finishedAt,
		&summaryJSON,
		&run.ResultRevision,
		&incomplete,
	); err != nil {
		return testdomain.TestRun{}, err
	}
	run.Status = testdomain.RunStatus(status)
	if outcome.Valid {
		run.Outcome = testdomain.RunOutcome(outcome.String)
	}
	run.Incomplete = incomplete
	var err error
	run.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAt)
	if err != nil {
		return testdomain.TestRun{}, err
	}
	if run.StartedAt, err = parseNullableRunTime(startedAt); err != nil {
		return testdomain.TestRun{}, err
	}
	if run.FinishedAt, err = parseNullableRunTime(finishedAt); err != nil {
		return testdomain.TestRun{}, err
	}
	if err := decodeStrictJSON(selectionJSON, &run.SelectionSnapshot); err != nil {
		return testdomain.TestRun{}, err
	}
	if err := decodeStrictJSON(summaryJSON, &run.Summary); err != nil {
		return testdomain.TestRun{}, err
	}
	validated, err := testdomain.NewTestRun(run)
	if err != nil {
		return testdomain.TestRun{}, err
	}
	validated.Results = nil
	return validated, nil
}

func encodeRunMetadata(
	run testdomain.TestRun,
) ([]byte, []byte, error) {
	selection, err := json.Marshal(run.SelectionSnapshot)
	if err != nil {
		return nil, nil, err
	}
	summary, err := json.Marshal(run.Summary)
	if err != nil {
		return nil, nil, err
	}
	return selection, summary, nil
}

func nullableRunOutcome(value testdomain.TestRun) any {
	if value.Outcome == "" {
		return nil
	}
	return string(value.Outcome)
}

func parseNullableRunTime(value sql.NullString) (*time.Time, error) {
	if !value.Valid {
		return nil, nil
	}
	parsed, err := time.Parse(time.RFC3339Nano, value.String)
	if err != nil {
		return nil, err
	}
	return &parsed, nil
}

func equivalentCreatedRun(
	first testdomain.TestRun,
	second testdomain.TestRun,
) bool {
	first.Results = nil
	second.Results = nil
	return reflect.DeepEqual(first, second)
}

type runCursor struct {
	ProjectID string    `json:"projectId"`
	ProfileID string    `json:"profileId"`
	CreatedAt time.Time `json:"createdAt"`
	RunID     string    `json:"runId"`
}

func encodeRunCursor(value runCursor) string {
	encoded, _ := json.Marshal(value)
	return base64.RawURLEncoding.EncodeToString(encoded)
}

func decodeRunCursor(value string) (runCursor, error) {
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return runCursor{}, err
	}
	var result runCursor
	if err := decodeStrictJSON(decoded, &result); err != nil ||
		result.CreatedAt.IsZero() ||
		!lowerHex(result.RunID, 32) {
		return runCursor{}, errors.New("invalid TestRun cursor")
	}
	return result, nil
}

var _ task.TestRunRepository = (*Store)(nil)
