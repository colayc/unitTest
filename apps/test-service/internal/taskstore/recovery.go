package taskstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"sort"
	"time"

	"unit-test-ide.local/test-service/internal/coveragedomain"
	"unit-test-ide.local/test-service/internal/task"
	"unit-test-ide.local/test-service/internal/testdomain"
)

func upsertLease(ctx context.Context, tx *sql.Tx, lease task.ProcessLease) error {
	if !validLease(lease) {
		return task.ErrInvalidArgument
	}
	groups, err := encodeLeaseTargetGroups(
		lease.TargetProcessGroups,
	)
	if err != nil {
		return task.ErrInvalidArgument
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO process_leases(
		task_id, host_pid, host_start_identity, target_process_group,
		target_process_groups_json, service_instance_id
	) VALUES(?,?,?,?,?,?) ON CONFLICT(task_id) DO UPDATE SET
		host_pid=excluded.host_pid,
		host_start_identity=excluded.host_start_identity,
		target_process_group=excluded.target_process_group,
		target_process_groups_json=excluded.target_process_groups_json,
		service_instance_id=excluded.service_instance_id`,
		lease.TaskID, lease.HostPID, lease.HostStartIdentity,
		lease.TargetProcessGroup, string(groups),
		lease.ServiceInstanceID); err != nil {
		return storageError("put process lease", err)
	}
	return nil
}

func validLease(lease task.ProcessLease) bool {
	if lease.TaskID == "" || lease.HostPID <= 0 ||
		lease.HostStartIdentity == "" ||
		lease.ServiceInstanceID == "" ||
		lease.TargetProcessGroup < 0 ||
		len(lease.TargetProcessGroups) > 256 {
		return false
	}
	seen := make(map[int]struct{}, len(lease.TargetProcessGroups))
	for _, group := range lease.TargetProcessGroups {
		if group <= 1 {
			return false
		}
		if _, duplicate := seen[group]; duplicate {
			return false
		}
		seen[group] = struct{}{}
	}
	return true
}

func (s *Store) UpdateLease(ctx context.Context, lease task.ProcessLease) error {
	if !validLease(lease) {
		return task.ErrInvalidArgument
	}
	groups, err := encodeLeaseTargetGroups(
		lease.TargetProcessGroups,
	)
	if err != nil {
		return task.ErrInvalidArgument
	}
	result, err := s.db.ExecContext(ctx, `UPDATE process_leases SET
		host_pid=?, host_start_identity=?, target_process_group=?,
		target_process_groups_json=?, service_instance_id=?
		WHERE task_id=? AND EXISTS (
			SELECT 1 FROM tasks WHERE tasks.task_id=process_leases.task_id
			AND tasks.status IN ('queued','running','cancelling')
		)`, lease.HostPID, lease.HostStartIdentity,
		lease.TargetProcessGroup, string(groups),
		lease.ServiceInstanceID, lease.TaskID)
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
		target_process_group, target_process_groups_json,
		service_instance_id
		FROM process_leases JOIN tasks ON tasks.task_id=process_leases.task_id
		WHERE tasks.status IN ('queued','running','cancelling') ORDER BY process_leases.task_id`)
	if err != nil {
		return nil, storageError("list active leases", err)
	}
	defer rows.Close()
	leases := make([]task.ProcessLease, 0)
	for rows.Next() {
		var lease task.ProcessLease
		var groupsJSON string
		if err := rows.Scan(
			&lease.TaskID,
			&lease.HostPID,
			&lease.HostStartIdentity,
			&lease.TargetProcessGroup,
			&groupsJSON,
			&lease.ServiceInstanceID,
		); err != nil {
			return nil, storageError("read active lease", err)
		}
		if err := json.Unmarshal(
			[]byte(groupsJSON),
			&lease.TargetProcessGroups,
		); err != nil || !validLease(lease) {
			return nil, storageError(
				"validate active lease",
				task.ErrInvalidArgument,
			)
		}
		if len(lease.TargetProcessGroups) == 0 {
			lease.TargetProcessGroups = nil
		}
		leases = append(leases, lease)
	}
	if err := rows.Err(); err != nil {
		return nil, storageError("list active leases", err)
	}
	return leases, nil
}

func encodeLeaseTargetGroups(values []int) ([]byte, error) {
	if values == nil {
		values = []int{}
	}
	return json.Marshal(values)
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
		drafts := make([]task.EventDraft, 0, 3)
		if candidate.kind == task.KindTestRun || candidate.kind == task.KindCoverageRun {
			finished, err := recoverInterruptedTestRun(
				ctx,
				tx,
				candidate.taskID,
				at,
			)
			if err != nil {
				return nil, err
			}
			drafts = append(drafts, finished)
		}
		var recoveredCoverage *coveragedomain.Run
		if candidate.kind == task.KindCoverageRun {
			run, finished, err := recoverInterruptedCoverageRun(ctx, tx, candidate.taskID, at)
			if err != nil {
				return nil, err
			}
			recoveredCoverage = &run
			drafts = append(drafts, finished)
		}
		drafts = append(drafts, task.EventDraft{
			TaskID:  candidate.taskID,
			Type:    task.EventTaskFinished,
			At:      at,
			Payload: json.RawMessage(`{"outcome":"interrupted"}`),
		})
		inserted, err := insertEvents(ctx, tx, drafts, s.newID)
		if err != nil {
			return nil, err
		}
		lastSequence := inserted[len(inserted)-1].Sequence
		if _, err := tx.ExecContext(ctx, `UPDATE tasks SET last_sequence=? WHERE task_id=?`, lastSequence, candidate.taskID); err != nil {
			return nil, storageError("update recovered task sequence", err)
		}
		if recoveredCoverage != nil {
			recoveredCoverage.LastSequence = lastSequence
			canonical, err := coveragedomain.NewRun(recoveredCoverage.Clone())
			if err != nil {
				return nil, storageError("validate recovered CoverageRun sequence", err)
			}
			result, err := tx.ExecContext(ctx, `UPDATE coverage_runs SET last_sequence=?
				WHERE coverage_run_id=? AND task_id=? AND status='finished'`,
				canonical.LastSequence, canonical.ID, canonical.TaskID)
			if err != nil {
				return nil, storageError("update recovered CoverageRun sequence", err)
			}
			affected, err := result.RowsAffected()
			if err != nil || affected != 1 {
				return nil, storageError("read recovered CoverageRun sequence update", err)
			}
		}
		events = append(events, inserted...)
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

func recoverInterruptedCoverageRun(
	ctx context.Context,
	tx *sql.Tx,
	taskID string,
	at time.Time,
) (coveragedomain.Run, task.EventDraft, error) {
	run, err := scanCoverageRun(tx.QueryRowContext(
		ctx,
		coverageRunSelect+` WHERE task_id=?`,
		taskID,
	))
	if err != nil {
		return coveragedomain.Run{}, task.EventDraft{},
			storageError("get interrupted CoverageRun", err)
	}
	expected := run.Status
	if expected != coveragedomain.StatusQueued && expected != coveragedomain.StatusRunning {
		return coveragedomain.Run{}, task.EventDraft{},
			storageError("validate interrupted CoverageRun", task.ErrConflict)
	}
	run.Status = coveragedomain.StatusFinished
	run.Outcome = coveragedomain.OutcomeUnavailable
	run.Reason = coveragedomain.ReasonServiceRestarted
	run.Summary = nil
	run.ReportID = ""
	run.Artifacts = coveragedomain.ArtifactRefs{}
	run.FinishedAt = &at
	validated, err := coveragedomain.NewRun(run)
	if err != nil {
		return coveragedomain.Run{}, task.EventDraft{},
			storageError("validate recovered CoverageRun", err)
	}
	if err := finishCoverageRunTx(ctx, tx, validated, expected); err != nil {
		return coveragedomain.Run{}, task.EventDraft{}, err
	}
	payload, err := json.Marshal(map[string]any{
		"coverageRunId": validated.ID,
		"outcome":       validated.Outcome,
		"reason":        validated.Reason,
	})
	if err != nil {
		return coveragedomain.Run{}, task.EventDraft{},
			storageError("encode interrupted CoverageRun event", err)
	}
	return validated, task.EventDraft{
		TaskID: taskID, Type: task.EventCoverageRunFinished, At: at, Payload: payload,
	}, nil
}

type recoveryResultIdentity struct {
	itemID      testdomain.ID
	containerID testdomain.ID
}

type recoveryResultKey struct {
	itemID    testdomain.ID
	iteration int64
}

func recoverInterruptedTestRun(
	ctx context.Context,
	tx *sql.Tx,
	taskID string,
	at time.Time,
) (task.EventDraft, error) {
	run, err := scanTestRun(tx.QueryRowContext(
		ctx,
		testRunSelect+` WHERE task_id=?`,
		taskID,
	))
	if err != nil {
		return task.EventDraft{},
			storageError("get interrupted TestRun", err)
	}
	if run.Status == testdomain.RunCompleted {
		return task.EventDraft{},
			storageError("validate interrupted TestRun", task.ErrConflict)
	}
	identities, err := recoveryResultIdentities(ctx, tx, run)
	if err != nil {
		return task.EventDraft{}, err
	}
	results, err := loadRunResults(ctx, tx, run.RunID)
	if err != nil {
		return task.EventDraft{}, err
	}
	persisted := make(
		map[recoveryResultKey]struct{},
		len(results),
	)
	for _, result := range results {
		persisted[recoveryResultKey{
			itemID:    result.ItemID,
			iteration: result.Iteration,
		}] = struct{}{}
	}
	for iteration := int64(1); iteration <= run.Summary.Iterations; iteration++ {
		for _, identity := range identities {
			key := recoveryResultKey{
				itemID:    identity.itemID,
				iteration: iteration,
			}
			if _, exists := persisted[key]; exists {
				continue
			}
			result, err := testdomain.NewTestItemResult(
				testdomain.TestItemResult{
					ItemID:         identity.itemID,
					ContainerID:    identity.containerID,
					Iteration:      iteration,
					Outcome:        testdomain.ItemNotRun,
					FailureDetails: []testdomain.FailureDetail{},
					OutputRefs:     []string{},
					Reason:         testdomain.ReasonServiceRestarted,
				},
			)
			if err != nil {
				return task.EventDraft{},
					storageError("validate recovered TestResult", err)
			}
			encoded, err := json.Marshal(result)
			if err != nil {
				return task.EventDraft{},
					storageError("encode recovered TestResult", err)
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO test_run_results(
				run_id, item_id, container_id, iteration, outcome, partial, payload_json
			) VALUES(?,?,?,?,?,?,?)`,
				run.RunID,
				result.ItemID.String(),
				result.ContainerID.String(),
				result.Iteration,
				string(result.Outcome),
				result.Partial,
				string(encoded),
			); err != nil {
				return task.EventDraft{},
					storageError("insert recovered TestResult", err)
			}
			persisted[key] = struct{}{}
		}
	}
	results, err = loadRunResults(ctx, tx, run.RunID)
	if err != nil {
		return task.EventDraft{}, err
	}
	revision, err := testdomain.ResultRevision(results)
	if err != nil {
		return task.EventDraft{},
			storageError("revision recovered TestResults", err)
	}
	summary, _ := summarizeResults(
		results,
		run.Summary.Iterations,
	)
	run.Status = testdomain.RunCompleted
	run.Outcome = testdomain.RunInterrupted
	run.FinishedAt = &at
	run.Summary = summary
	run.ResultRevision = revision
	run.Incomplete = true
	run.Results = results
	validated, err := testdomain.NewTestRun(run)
	if err != nil {
		return task.EventDraft{},
			storageError("validate recovered TestRun", err)
	}
	_, summaryJSON, err := encodeRunMetadata(validated)
	if err != nil {
		return task.EventDraft{},
			storageError("encode recovered TestRun", err)
	}
	updated, err := tx.ExecContext(ctx, `UPDATE test_runs SET
		status='completed', outcome='interrupted', finished_at=?,
		summary_json=?, result_revision=?, incomplete=1
		WHERE run_id=? AND status IN ('queued','running')`,
		formatTime(at),
		string(summaryJSON),
		revision,
		run.RunID,
	)
	if err != nil {
		return task.EventDraft{},
			storageError("finish interrupted TestRun", err)
	}
	affected, err := updated.RowsAffected()
	if err != nil || affected != 1 {
		return task.EventDraft{},
			storageError("read interrupted TestRun update", err)
	}
	payload, err := json.Marshal(map[string]any{
		"runId":          run.RunID,
		"outcome":        testdomain.RunInterrupted,
		"summary":        summary,
		"resultRevision": revision,
		"incomplete":     true,
	})
	if err != nil {
		return task.EventDraft{},
			storageError("encode interrupted TestRun event", err)
	}
	return task.EventDraft{
		TaskID:  taskID,
		Type:    task.EventTestRunFinished,
		At:      at,
		Payload: payload,
	}, nil
}

func recoveryResultIdentities(
	ctx context.Context,
	tx *sql.Tx,
	run testdomain.TestRun,
) ([]recoveryResultIdentity, error) {
	rows, err := tx.QueryContext(ctx, `SELECT entry_kind, payload_json
		FROM test_catalog_entries
		WHERE project_id=? AND profile_id=? AND revision=?
		ORDER BY ordinal`,
		run.ProjectID,
		run.ProfileID,
		run.CatalogRevision,
	)
	if err != nil {
		return nil, storageError(
			"list interrupted TestRun Catalog",
			err,
		)
	}
	defer rows.Close()
	containers := make(
		map[testdomain.ID]struct{},
		len(run.SelectionSnapshot.ContainerIDs),
	)
	items := make(map[testdomain.ID]testdomain.Item)
	casesByContainer := make(map[testdomain.ID][]testdomain.Item)
	for rows.Next() {
		var kind string
		var encoded []byte
		if err := rows.Scan(&kind, &encoded); err != nil {
			return nil, storageError(
				"read interrupted TestRun Catalog",
				err,
			)
		}
		switch kind {
		case "container":
			var value testdomain.Container
			if err := decodeStrictJSON(encoded, &value); err != nil {
				return nil, storageError(
					"decode interrupted TestRun container",
					err,
				)
			}
			value, err = testdomain.NewContainer(value)
			if err != nil {
				return nil, storageError(
					"validate interrupted TestRun container",
					err,
				)
			}
			containers[value.ID] = struct{}{}
		case "item":
			var value testdomain.Item
			if err := decodeStrictJSON(encoded, &value); err != nil {
				return nil, storageError(
					"decode interrupted TestRun item",
					err,
				)
			}
			value, err = testdomain.NewItem(value)
			if err != nil {
				return nil, storageError(
					"validate interrupted TestRun item",
					err,
				)
			}
			items[value.ID] = value
			if value.Kind == testdomain.ItemCase {
				casesByContainer[value.ContainerID] = append(
					casesByContainer[value.ContainerID],
					value,
				)
			}
		default:
			return nil, storageError(
				"decode interrupted TestRun Catalog entry",
				task.ErrInvalidArgument,
			)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, storageError(
			"read interrupted TestRun Catalog",
			err,
		)
	}
	result := make(
		[]recoveryResultIdentity,
		0,
		len(run.SelectionSnapshot.ItemIDs)+
			len(run.SelectionSnapshot.ContainerIDs),
	)
	seen := make(map[testdomain.ID]struct{})
	for _, id := range run.SelectionSnapshot.ItemIDs {
		item, exists := items[id]
		if !exists || item.Kind != testdomain.ItemCase {
			return nil, storageError(
				"resolve interrupted TestRun item",
				task.ErrInvalidArgument,
			)
		}
		result = append(result, recoveryResultIdentity{
			itemID:      item.ID,
			containerID: item.ContainerID,
		})
		seen[item.ID] = struct{}{}
	}
	for _, id := range run.SelectionSnapshot.ContainerIDs {
		if _, exists := containers[id]; !exists {
			return nil, storageError(
				"resolve interrupted TestRun container",
				task.ErrInvalidArgument,
			)
		}
		cases := casesByContainer[id]
		if len(cases) == 0 {
			if _, exists := seen[id]; !exists {
				result = append(result, recoveryResultIdentity{
					itemID:      id,
					containerID: id,
				})
				seen[id] = struct{}{}
			}
			continue
		}
		for _, item := range cases {
			if _, exists := seen[item.ID]; exists {
				continue
			}
			result = append(result, recoveryResultIdentity{
				itemID:      item.ID,
				containerID: item.ContainerID,
			})
			seen[item.ID] = struct{}{}
		}
	}
	sort.Slice(result, func(left, right int) bool {
		if result[left].itemID == result[right].itemID {
			return result[left].containerID <
				result[right].containerID
		}
		return result[left].itemID < result[right].itemID
	})
	if len(result) == 0 {
		return nil, storageError(
			"resolve interrupted TestRun selection",
			task.ErrInvalidArgument,
		)
	}
	return result, nil
}
