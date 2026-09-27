package taskstore

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"

	"unit-test-ide.local/test-service/internal/task"
	"unit-test-ide.local/test-service/internal/testgendomain"
)

type unrecordedGeneration struct {
	runID, taskID, state string
	revision, sequence   int64
	request              []byte
	requestHash          string
}

// reconcileGenerationRecords runs after v11 schema migration and is
// idempotent across a crash between schema upgrade and data reconciliation.
// Identity-complete rows retain their checkpoint; pre-v11 rows lacking new
// trusted identities are terminalized atomically, never falsely backfilled.
func (s *Store) reconcileGenerationRecords(ctx context.Context) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return storageError("begin generation reconciliation", err)
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `SELECT g.run_id,g.task_id,g.state,g.revision,t.last_sequence,t.request_json,t.request_hash FROM test_generation_runs g JOIN tasks t ON t.task_id=g.task_id WHERE g.record_json='{}' ORDER BY g.run_id`)
	if err != nil {
		return storageError("list unrecorded generation", err)
	}
	var pending []unrecordedGeneration
	for rows.Next() {
		var r unrecordedGeneration
		if err := rows.Scan(&r.runID, &r.taskID, &r.state, &r.revision, &r.sequence, &r.request, &r.requestHash); err != nil {
			rows.Close()
			return storageError("read unrecorded generation", err)
		}
		pending = append(pending, r)
		if len(pending) > 100000 {
			rows.Close()
			return task.ErrStorageUnavailable
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return storageError("scan unrecorded generation", err)
	}
	if err := rows.Close(); err != nil {
		return storageError("close unrecorded generation", err)
	}
	for _, row := range pending {
		var request testgendomain.Request
		if err := strictGenerationJSON(row.request, &request); err != nil {
			return task.ErrStorageUnavailable
		}
		sum := sha256.Sum256(row.request)
		if row.requestHash != hex.EncodeToString(sum[:]) {
			return task.ErrStorageUnavailable
		}
		if testgendomain.ValidateRequest(request) == nil {
			canonical, _ := json.Marshal(request)
			if !bytes.Equal(row.request, canonical) {
				return task.ErrStorageUnavailable
			}
			recordJSON, recordHash, err := generationRecordBytes(testgendomain.NewGenerationRecord(request))
			if err != nil {
				return err
			}
			result, err := tx.ExecContext(ctx, `UPDATE test_generation_runs SET record_json=?,record_sha256=? WHERE run_id=? AND revision=? AND record_json='{}'`, string(recordJSON), recordHash, row.runID, row.revision)
			if err != nil {
				return storageError("backfill generation record", err)
			}
			if n, _ := result.RowsAffected(); n != 1 {
				return task.ErrConflict
			}
			continue
		}
		if !request.IsLegacySnapshot() {
			return task.ErrStorageUnavailable
		}
		if testgendomain.IsTerminal(testgendomain.State(row.state)) {
			continue
		}
		if !testgendomain.ValidTransition(testgendomain.State(row.state), testgendomain.StateFailed) {
			return task.ErrStorageUnavailable
		}
		at := time.Now().UTC()
		result, err := tx.ExecContext(ctx, `UPDATE test_generation_runs SET state='failed',revision=revision+1 WHERE run_id=? AND revision=? AND record_json='{}'`, row.runID, row.revision)
		if err != nil {
			return storageError("terminalize legacy generation", err)
		}
		if n, _ := result.RowsAffected(); n != 1 {
			return task.ErrConflict
		}
		result, err = tx.ExecContext(ctx, `UPDATE tasks SET status='finished',outcome=?,finished_at=?,active_step='failed',error_code='stale-snapshot',error_message='' WHERE task_id=? AND last_sequence=?`, string(task.OutcomeInfrastructureFailed), formatTime(at), row.taskID, row.sequence)
		if err != nil {
			return storageError("terminalize legacy task", err)
		}
		if n, _ := result.RowsAffected(); n != 1 {
			return task.ErrConflict
		}
		payload, _ := json.Marshal(struct {
			RunID string              `json:"runId"`
			From  testgendomain.State `json:"from"`
			To    testgendomain.State `json:"to"`
		}{row.runID, testgendomain.State(row.state), testgendomain.StateFailed})
		events, err := insertGenerationEvents(ctx, tx, row.sequence, []task.EventDraft{{TaskID: row.taskID, Type: task.EventTestGenerationStateChanged, At: at, Payload: payload}, {TaskID: row.taskID, Type: task.EventTaskFinished, At: at, Payload: json.RawMessage(`{"outcome":"infrastructure_failed"}`)}}, s.newID)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE tasks SET last_sequence=? WHERE task_id=? AND last_sequence=?`, events[len(events)-1].Sequence, row.taskID, row.sequence); err != nil {
			return storageError("legacy terminal sequence", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return storageError("commit generation reconciliation", err)
	}
	return nil
}
