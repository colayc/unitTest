package taskstore

import (
	"context"
	"database/sql"

	"unit-test-ide.local/test-service/internal/task"
	"unit-test-ide.local/test-service/internal/testgendomain"
)

// PutGenerationProcessLease durably binds a native validation process to one
// active generation task. A different host process or service instance cannot
// replace an existing owner for the same task.
func (s *Store) PutGenerationProcessLease(ctx context.Context, lease task.ProcessLease) error {
	if s == nil || ctx == nil || !validLease(lease) {
		return task.ErrInvalidArgument
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return storageError("begin generation lease", err)
	}
	defer tx.Rollback()
	run, err := getGeneration(ctx, tx, "", lease.TaskID)
	if err != nil {
		return err
	}
	if run.State == testgendomain.StateQueued || testgendomain.IsTerminal(run.State) {
		return task.ErrConflict
	}
	var hostPID int
	var hostStartIdentity, serviceInstanceID string
	err = tx.QueryRowContext(ctx, `SELECT host_pid,host_start_identity,service_instance_id FROM process_leases WHERE task_id=?`, lease.TaskID).Scan(&hostPID, &hostStartIdentity, &serviceInstanceID)
	if err != nil && err != sql.ErrNoRows {
		return storageError("read generation lease", err)
	}
	if err == nil && (hostPID != lease.HostPID || hostStartIdentity != lease.HostStartIdentity || serviceInstanceID != lease.ServiceInstanceID) {
		return task.ErrConflict
	}
	if err := upsertLease(ctx, tx, lease); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return storageError("commit generation lease", err)
	}
	return nil
}

// ReleaseGenerationProcessLease deletes only the exact owner that was
// recorded. It remains available after terminalization so cancellation and
// shutdown can always clean up a process that was already started.
func (s *Store) ReleaseGenerationProcessLease(ctx context.Context, lease task.ProcessLease) error {
	if s == nil || ctx == nil || !validLease(lease) {
		return task.ErrInvalidArgument
	}
	groups, err := encodeLeaseTargetGroups(lease.TargetProcessGroups)
	if err != nil {
		return task.ErrInvalidArgument
	}
	result, err := s.db.ExecContext(ctx, `DELETE FROM process_leases WHERE task_id=? AND host_pid=? AND host_start_identity=? AND target_process_group=? AND target_process_groups_json=? AND service_instance_id=?`,
		lease.TaskID, lease.HostPID, lease.HostStartIdentity, lease.TargetProcessGroup, string(groups), lease.ServiceInstanceID)
	if err != nil {
		return storageError("release generation lease", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return storageError("read released generation lease", err)
	}
	if affected != 1 {
		return task.ErrConflict
	}
	return nil
}
