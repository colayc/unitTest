package taskstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"sort"
	"time"

	"unit-test-ide.local/test-service/internal/task"
)

func upsertLease(ctx context.Context, tx *sql.Tx, lease task.ProcessLease) error {
	if !validLease(lease) {
		return task.ErrInvalidArgument
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO process_leases(
		task_id, host_pid, host_start_identity, target_process_group, service_instance_id
	) VALUES(?,?,?,?,?) ON CONFLICT(task_id) DO UPDATE SET
		host_pid=excluded.host_pid,
		host_start_identity=excluded.host_start_identity,
		target_process_group=excluded.target_process_group,
		service_instance_id=excluded.service_instance_id`,
		lease.TaskID, lease.HostPID, lease.HostStartIdentity, lease.TargetProcessGroup, lease.ServiceInstanceID); err != nil {
		return storageError("put process lease", err)
	}
	return nil
}

func validLease(lease task.ProcessLease) bool {
	return lease.TaskID != "" && lease.HostPID > 0 && lease.HostStartIdentity != "" && lease.ServiceInstanceID != ""
}

func (s *Store) UpdateLease(ctx context.Context, lease task.ProcessLease) error {
	if !validLease(lease) {
		return task.ErrInvalidArgument
	}
	result, err := s.db.ExecContext(ctx, `UPDATE process_leases SET
		host_pid=?, host_start_identity=?, target_process_group=?, service_instance_id=?
		WHERE task_id=? AND EXISTS (
			SELECT 1 FROM tasks WHERE tasks.task_id=process_leases.task_id
			AND tasks.status IN ('queued','running','cancelling')
		)`, lease.HostPID, lease.HostStartIdentity, lease.TargetProcessGroup, lease.ServiceInstanceID, lease.TaskID)
	if err != nil {
		return storageError("update process lease", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return storageError("read lease update", err)
	}
	if affected != 1 {
		return task.ErrConflict
	}
	return nil
}

func (s *Store) ActiveLeases(ctx context.Context) ([]task.ProcessLease, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT process_leases.task_id, host_pid, host_start_identity,
		target_process_group, service_instance_id
		FROM process_leases JOIN tasks ON tasks.task_id=process_leases.task_id
		WHERE tasks.status IN ('queued','running','cancelling') ORDER BY process_leases.task_id`)
	if err != nil {
		return nil, storageError("list active leases", err)
	}
	defer rows.Close()
	leases := make([]task.ProcessLease, 0)
	for rows.Next() {
		var lease task.ProcessLease
		if err := rows.Scan(&lease.TaskID, &lease.HostPID, &lease.HostStartIdentity, &lease.TargetProcessGroup, &lease.ServiceInstanceID); err != nil {
			return nil, storageError("read active lease", err)
		}
		leases = append(leases, lease)
	}
	if err := rows.Err(); err != nil {
		return nil, storageError("list active leases", err)
	}
	return leases, nil
}

func (s *Store) RecoverInterrupted(ctx context.Context, at time.Time) ([]task.Event, error) {
	if at.IsZero() {
		return nil, task.ErrInvalidArgument
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, storageError("begin recovery", err)
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `SELECT task_id, kind, status FROM tasks
		WHERE status IN ('queued','running','cancelling') ORDER BY created_at, task_id`)
	if err != nil {
		return nil, storageError("list interrupted tasks", err)
	}
	type recoveryCandidate struct {
		taskID string
		kind   task.Kind
		status task.Status
	}
	candidates := make([]recoveryCandidate, 0)
	for rows.Next() {
		var candidate recoveryCandidate
		if err := rows.Scan(&candidate.taskID, &candidate.kind, &candidate.status); err != nil {
			_ = rows.Close()
			return nil, storageError("read interrupted task", err)
		}
		candidates = append(candidates, candidate)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, storageError("list interrupted tasks", err)
	}
	if err := rows.Close(); err != nil {
		return nil, storageError("close interrupted tasks", err)
	}

	events := make([]task.Event, 0, len(candidates))
	for _, candidate := range candidates {
		interrupt := candidate.kind == task.KindSimulation ||
			candidate.status == task.StatusRunning ||
			candidate.status == task.StatusCancelling
		if !interrupt {
			continue
		}
		if _, err := tx.ExecContext(ctx, `UPDATE task_steps SET
			status='failed', finished_at=?, exit_code=NULL, error_code='SERVICE_RESTARTED'
			WHERE task_id=? AND status='running'`, formatTime(at), candidate.taskID); err != nil {
			return nil, storageError("fail interrupted running step", err)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE task_steps SET
			status='skipped', finished_at=?, exit_code=NULL, error_code=''
			WHERE task_id=? AND status='pending'`, formatTime(at), candidate.taskID); err != nil {
			return nil, storageError("skip interrupted pending steps", err)
		}
		result, err := tx.ExecContext(ctx, `UPDATE tasks SET
			status='finished', outcome='interrupted', finished_at=?, active_step=''
			WHERE task_id=? AND status IN ('queued','running','cancelling')`,
			formatTime(at), candidate.taskID)
		if err != nil {
			return nil, storageError("finish interrupted task", err)
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return nil, storageError("read recovery update", err)
		}
		if affected != 1 {
			return nil, task.ErrConflict
		}
		inserted, err := insertEvents(ctx, tx, []task.EventDraft{{
			TaskID:  candidate.taskID,
			Type:    task.EventTaskFinished,
			At:      at,
			Payload: json.RawMessage(`{"outcome":"interrupted"}`),
		}}, s.newID)
		if err != nil {
			return nil, err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE tasks SET last_sequence=? WHERE task_id=?`, inserted[0].Sequence, candidate.taskID); err != nil {
			return nil, storageError("update recovered task sequence", err)
		}
		events = append(events, inserted[0])
	}
	for _, candidate := range candidates {
		if _, err := tx.ExecContext(ctx, `DELETE FROM process_leases WHERE task_id=?`, candidate.taskID); err != nil {
			return nil, storageError("delete recovered lease", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, storageError("commit recovery", err)
	}
	sort.Slice(events, func(i, j int) bool { return events[i].Sequence < events[j].Sequence })
	return events, nil
}
