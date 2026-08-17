package taskstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"

	"unit-test-ide.local/test-service/internal/task"
	"unit-test-ide.local/test-service/internal/testdomain"
)

func (s *Store) AppendResult(
	ctx context.Context,
	runID string,
	value testdomain.TestItemResult,
) error {
	if s == nil || ctx == nil || !lowerHex(runID, 32) {
		return task.ErrInvalidArgument
	}
	result, err := testdomain.NewTestItemResult(value)
	if err != nil {
		return task.ErrInvalidArgument
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		return task.ErrInvalidArgument
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return storageError("begin TestResult append", err)
	}
	defer tx.Rollback()
	run, err := scanTestRun(tx.QueryRowContext(
		ctx,
		testRunSelect+` WHERE run_id=?`,
		runID,
	))
	if isNoRows(err) {
		return task.ErrNotFound
	}
	if err != nil {
		return storageError("get TestRun for append", err)
	}
	if run.Status == testdomain.RunCompleted {
		return task.ErrConflict
	}
	if result.Iteration > run.Summary.Iterations ||
		!resultInSelection(run.SelectionSnapshot, result) {
		return task.ErrInvalidArgument
	}

	var existingJSON []byte
	err = tx.QueryRowContext(ctx, `SELECT payload_json
		FROM test_run_results
		WHERE run_id=? AND item_id=? AND iteration=?`,
		runID,
		result.ItemID.String(),
		result.Iteration,
	).Scan(&existingJSON)
	switch {
	case err == nil:
		var existing testdomain.TestItemResult
		if err := decodeStrictJSON(existingJSON, &existing); err != nil {
			return storageError("decode existing TestResult", err)
		}
		existing, err = testdomain.NewTestItemResult(existing)
		if err != nil {
			return storageError("validate existing TestResult", err)
		}
		if reflect.DeepEqual(existing, result) {
			return nil
		}
		if !existing.Partial || result.Partial ||
			existing.ContainerID != result.ContainerID {
			return task.ErrConflict
		}
		if _, err := tx.ExecContext(ctx, `UPDATE test_run_results SET
			container_id=?, outcome=?, partial=?, payload_json=?
			WHERE run_id=? AND item_id=? AND iteration=?`,
			result.ContainerID.String(),
			string(result.Outcome),
			result.Partial,
			string(encoded),
			runID,
			result.ItemID.String(),
			result.Iteration,
		); err != nil {
			return storageError("advance TestResult", err)
		}
	case isNoRows(err):
		if _, err := tx.ExecContext(ctx, `INSERT INTO test_run_results(
			run_id, item_id, container_id, iteration, outcome, partial, payload_json
		) VALUES(?,?,?,?,?,?,?)`,
			runID,
			result.ItemID.String(),
			result.ContainerID.String(),
			result.Iteration,
			string(result.Outcome),
			result.Partial,
			string(encoded),
		); err != nil {
			return storageError("insert TestResult", err)
		}
	default:
		return storageError("find existing TestResult", err)
	}
	results, err := loadRunResults(ctx, tx, runID)
	if err != nil {
		return err
	}
	revision, err := testdomain.ResultRevision(results)
	if err != nil {
		return storageError("revision TestResults", err)
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE test_runs SET result_revision=? WHERE run_id=?`,
		revision,
		runID,
	); err != nil {
		return storageError("update TestRun revision", err)
	}
	if err := tx.Commit(); err != nil {
		return storageError("commit TestResult append", err)
	}
	return nil
}

func (s *Store) FinishRun(
	ctx context.Context,
	value testdomain.TestRun,
	artifacts []task.Artifact,
) error {
	if s == nil || ctx == nil {
		return task.ErrInvalidArgument
	}
	run, err := testdomain.NewTestRun(value)
	if err != nil || run.Status != testdomain.RunCompleted {
		return task.ErrInvalidArgument
	}
	if err := validateRunArtifacts(run.TaskID, artifacts); err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return storageError("begin TestRun finish", err)
	}
	defer tx.Rollback()
	var ownerTaskID string
	var ownerKind sql.NullString
	err = tx.QueryRowContext(ctx, `SELECT r.task_id, t.kind
		FROM test_runs r
		LEFT JOIN tasks t ON t.task_id=r.task_id
		WHERE r.run_id=?`, run.RunID).Scan(&ownerTaskID, &ownerKind)
	if isNoRows(err) {
		return task.ErrNotFound
	}
	if err != nil || !ownerKind.Valid || !validTaskKind(task.Kind(ownerKind.String)) {
		return storageError("resolve TestRun owner", err)
	}
	if task.Kind(ownerKind.String) == task.KindCoverageRun {
		return task.ErrInvalidArgument
	}
	if ownerTaskID != run.TaskID {
		return task.ErrConflict
	}
	if err := finishRunTx(ctx, tx, run, artifacts, true); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return storageError("commit TestRun finish", err)
	}
	return nil
}

func finishRunTx(
	ctx context.Context,
	tx *sql.Tx,
	run testdomain.TestRun,
	artifacts []task.Artifact,
	insertArtifacts bool,
) error {
	persisted, err := scanTestRun(tx.QueryRowContext(
		ctx,
		testRunSelect+` WHERE run_id=?`,
		run.RunID,
	))
	if isNoRows(err) {
		return task.ErrNotFound
	}
	if err != nil {
		return storageError("get TestRun for finish", err)
	}
	if persisted.Status == testdomain.RunCompleted {
		equivalentArtifacts, artifactErr := runArtifactsEqual(
			ctx,
			tx,
			run.RunID,
			artifacts,
		)
		if artifactErr != nil {
			return artifactErr
		}
		if equivalentFinishedRun(persisted, run) && equivalentArtifacts {
			return nil
		}
		return task.ErrConflict
	}
	if !sameRunIdentity(persisted, run) {
		return task.ErrConflict
	}
	results, err := loadRunResults(ctx, tx, run.RunID)
	if err != nil {
		return err
	}
	revision, err := testdomain.ResultRevision(results)
	if err != nil || revision != run.ResultRevision {
		return task.ErrConflict
	}
	summary, incomplete := summarizeResults(results, run.Summary.Iterations)
	if !reflect.DeepEqual(summary, run.Summary) ||
		incomplete && !run.Incomplete {
		return fmt.Errorf(
			"TestRun terminal summary mismatch: %w",
			task.ErrInvalidArgument,
		)
	}
	for _, artifact := range artifacts {
		if artifact.TaskID != run.TaskID {
			return fmt.Errorf(
				"TestRun artifact owner mismatch: %w",
				task.ErrInvalidArgument,
			)
		}
		if insertArtifacts {
			if err := insertArtifact(ctx, tx, artifact); err != nil {
				return err
			}
		} else {
			var owner string
			if err := tx.QueryRowContext(
				ctx,
				`SELECT task_id FROM artifacts WHERE artifact_id=?`,
				artifact.ID,
			).Scan(&owner); err != nil {
				return storageError("find TestRun artifact", err)
			}
			if owner != run.TaskID {
				return task.ErrConflict
			}
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO test_run_artifacts(
			run_id, artifact_id
		) VALUES(?,?)`, run.RunID, artifact.ID); err != nil {
			return storageError("link TestRun artifact", err)
		}
	}
	_, summaryJSON, err := encodeRunMetadata(run)
	if err != nil {
		return task.ErrInvalidArgument
	}
	update, err := tx.ExecContext(ctx, `UPDATE test_runs SET
		status=?, outcome=?, started_at=?, finished_at=?, summary_json=?,
		result_revision=?, incomplete=?
		WHERE run_id=? AND status<>'completed'`,
		string(run.Status),
		string(run.Outcome),
		nullableTime(run.StartedAt),
		nullableTime(run.FinishedAt),
		string(summaryJSON),
		run.ResultRevision,
		run.Incomplete,
		run.RunID,
	)
	if err != nil {
		return storageError("finish TestRun", err)
	}
	if affected, err := update.RowsAffected(); err != nil || affected != 1 {
		return task.ErrConflict
	}
	return nil
}

func validateRunArtifacts(taskID string, artifacts []task.Artifact) error {
	ids := make(map[string]struct{}, len(artifacts))
	paths := make(map[string]struct{}, len(artifacts))
	for _, artifact := range artifacts {
		if artifact.TaskID != taskID || !validArtifact(artifact) {
			return task.ErrInvalidArgument
		}
		if _, exists := ids[artifact.ID]; exists {
			return task.ErrInvalidArgument
		}
		if _, exists := paths[artifact.RelativePath]; exists {
			return task.ErrInvalidArgument
		}
		ids[artifact.ID] = struct{}{}
		paths[artifact.RelativePath] = struct{}{}
	}
	return nil
}

func runArtifactsEqual(
	ctx context.Context,
	tx *sql.Tx,
	runID string,
	expected []task.Artifact,
) (bool, error) {
	rows, err := tx.QueryContext(ctx, `SELECT
		a.artifact_id, a.task_id, a.kind, a.relative_path, a.mime_type,
		a.size_bytes, a.sha256, a.created_at
		FROM artifacts a
		INNER JOIN test_run_artifacts r ON r.artifact_id=a.artifact_id
		WHERE r.run_id=?
		ORDER BY a.artifact_id`,
		runID,
	)
	if err != nil {
		return false, storageError("list TestRun artifacts", err)
	}
	defer rows.Close()
	persisted := make([]task.Artifact, 0, len(expected))
	for rows.Next() {
		artifact, err := scanArtifact(rows)
		if err != nil {
			return false, storageError("read TestRun artifacts", err)
		}
		persisted = append(persisted, artifact)
	}
	if err := rows.Err(); err != nil {
		return false, storageError("read TestRun artifacts", err)
	}
	sortedExpected := append([]task.Artifact(nil), expected...)
	sort.Slice(sortedExpected, func(left, right int) bool {
		return sortedExpected[left].ID < sortedExpected[right].ID
	})
	return reflect.DeepEqual(persisted, sortedExpected), nil
}

type runResultQueryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func loadRunResults(
	ctx context.Context,
	queryer runResultQueryer,
	runID string,
) ([]testdomain.TestItemResult, error) {
	rows, err := queryer.QueryContext(ctx, `SELECT payload_json
		FROM test_run_results
		WHERE run_id=?
		ORDER BY item_id, iteration`,
		runID,
	)
	if err != nil {
		return nil, storageError("list TestResults", err)
	}
	defer rows.Close()
	results := make([]testdomain.TestItemResult, 0)
	for rows.Next() {
		var encoded []byte
		if err := rows.Scan(&encoded); err != nil {
			return nil, storageError("read TestResults", err)
		}
		var candidate testdomain.TestItemResult
		if err := decodeStrictJSON(encoded, &candidate); err != nil {
			return nil, storageError("decode TestResult", err)
		}
		result, err := testdomain.NewTestItemResult(candidate)
		if err != nil {
			return nil, storageError("validate TestResult", err)
		}
		results = append(results, result)
	}
	if err := rows.Err(); err != nil {
		return nil, storageError("read TestResults", err)
	}
	return results, nil
}

func resultInSelection(
	snapshot testdomain.SelectionSnapshot,
	result testdomain.TestItemResult,
) bool {
	for _, id := range snapshot.ItemIDs {
		if id == result.ItemID {
			return true
		}
	}
	for _, id := range snapshot.ContainerIDs {
		if id == result.ContainerID {
			return true
		}
	}
	return false
}

func sameRunIdentity(first, second testdomain.TestRun) bool {
	return first.RunID == second.RunID &&
		first.TaskID == second.TaskID &&
		first.IdempotencyKey == second.IdempotencyKey &&
		first.ProjectID == second.ProjectID &&
		first.ProfileID == second.ProfileID &&
		first.ToolchainID == second.ToolchainID &&
		first.CatalogRevision == second.CatalogRevision &&
		first.CreatedAt.Equal(second.CreatedAt) &&
		reflect.DeepEqual(first.SelectionSnapshot, second.SelectionSnapshot)
}

func equivalentFinishedRun(first, second testdomain.TestRun) bool {
	first.Results = nil
	second.Results = nil
	return reflect.DeepEqual(first, second)
}

func summarizeResults(
	results []testdomain.TestItemResult,
	iterations int64,
) (testdomain.RunSummary, bool) {
	summary := testdomain.RunSummary{
		Total:      int64(len(results)),
		Iterations: iterations,
	}
	incomplete := false
	for _, result := range results {
		incomplete = incomplete || result.Partial
		switch result.Outcome {
		case testdomain.ItemPassed:
			summary.Passed++
			summary.Completed++
		case testdomain.ItemFailed:
			summary.Failed++
			summary.Completed++
		case testdomain.ItemSkipped:
			summary.Skipped++
			summary.Completed++
		case testdomain.ItemErrored:
			summary.Errored++
			summary.Completed++
		case testdomain.ItemCancelled:
			summary.Cancelled++
			summary.Completed++
		case testdomain.ItemTimedOut:
			summary.TimedOut++
			summary.Completed++
		case testdomain.ItemNotRun:
			summary.NotRun++
			incomplete = true
		}
	}
	return summary, incomplete
}
