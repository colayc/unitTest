package taskstore

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"

	"unit-test-ide.local/test-service/internal/coveragedomain"
	"unit-test-ide.local/test-service/internal/task"
)

const coverageReportSelect = `SELECT
	report_id, coverage_run_id, test_run_id, schema_version, created_at,
	completeness_json, summary_json, toolchain_json, artifact_id
	FROM coverage_reports`

type coverageCompletenessWire struct {
	Outcome coveragedomain.Outcome              `json:"outcome"`
	Reasons []coveragedomain.CompletenessReason `json:"reasons"`
}

func (s *Store) GetCoverageReport(ctx context.Context, reportID string) (coveragedomain.Report, error) {
	if s == nil || ctx == nil || !lowerHex(reportID, 32) {
		return coveragedomain.Report{}, task.ErrInvalidArgument
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return coveragedomain.Report{}, storageError("begin CoverageReport read", err)
	}
	defer tx.Rollback()
	report, err := scanCoverageReport(tx.QueryRowContext(ctx, coverageReportSelect+` WHERE report_id=?`, reportID))
	if isNoRows(err) {
		return coveragedomain.Report{}, task.ErrNotFound
	}
	if err != nil {
		return coveragedomain.Report{}, storageError("get CoverageReport", err)
	}
	var taskID string
	if err := tx.QueryRowContext(
		ctx,
		`SELECT task_id FROM coverage_reports WHERE report_id=?`,
		reportID,
	).Scan(&taskID); err != nil {
		return coveragedomain.Report{}, storageError("get CoverageReport owner", err)
	}
	owner, err := scanTask(tx.QueryRowContext(ctx, taskSelect+` WHERE task_id=?`, taskID))
	if err != nil || validateTask(owner) != nil || owner.Kind != task.KindCoverageRun ||
		owner.Status != task.StatusFinished {
		return coveragedomain.Report{}, storageError("validate CoverageReport owner", err)
	}
	run, err := scanCoverageRun(tx.QueryRowContext(
		ctx,
		coverageRunSelect+` WHERE coverage_run_id=? AND task_id=?`,
		report.RunID,
		taskID,
	))
	if err != nil {
		return coveragedomain.Report{}, storageError("get CoverageReport run", err)
	}
	validated, err := validateCoverageReportForRun(report, run, taskID)
	if err != nil || owner.Outcome != task.CoverageTaskOutcome(run.Outcome, run.Reason) ||
		owner.LastSequence != run.LastSequence {
		return coveragedomain.Report{}, storageError("validate CoverageReport graph", err)
	}
	artifact, err := scanArtifact(tx.QueryRowContext(
		ctx,
		artifactSelect+` WHERE artifact_id=? AND task_id=?`,
		validated.ArtifactID,
		taskID,
	))
	if err != nil || !validArtifact(artifact) ||
		artifact.ID != run.Artifacts.CoverageJSONID ||
		artifact.Kind != coverageJSONArtifactKind ||
		artifact.MIMEType != applicationJSONMIMEType {
		return coveragedomain.Report{}, storageError("validate CoverageReport artifact", err)
	}
	if err := tx.Commit(); err != nil {
		return coveragedomain.Report{}, storageError("commit CoverageReport read", err)
	}
	return validated.Clone(), nil
}

func insertCoverageReport(
	ctx context.Context,
	tx *sql.Tx,
	report coveragedomain.Report,
	run coveragedomain.Run,
	taskID string,
) error {
	validated, err := validateCoverageReportForRun(report, run, taskID)
	if err != nil {
		return err
	}
	completenessJSON, summaryJSON, toolchainJSON, err := encodeCoverageReportMetadata(validated)
	if err != nil {
		return task.ErrInvalidArgument
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO coverage_reports(
		report_id, coverage_run_id, task_id, test_run_id, schema_version,
		created_at, completeness_json, summary_json, toolchain_json, artifact_id
	) VALUES(?,?,?,?,?,?,?,?,?,?)`,
		validated.ID, validated.RunID, taskID, validated.TestRunID,
		validated.SchemaVersion, formatCoverageTime(validated.CreatedAt),
		string(completenessJSON), string(summaryJSON), string(toolchainJSON),
		validated.ArtifactID,
	); err != nil {
		return storageError("insert CoverageReport", err)
	}
	return nil
}

func validateCoverageReportForRun(
	report coveragedomain.Report,
	run coveragedomain.Run,
	taskID string,
) (coveragedomain.Report, error) {
	validatedRun, err := coveragedomain.NewRun(run)
	if err != nil || validatedRun.Status != coveragedomain.StatusFinished ||
		(validatedRun.Outcome != coveragedomain.OutcomeAvailable && validatedRun.Outcome != coveragedomain.OutcomePartial) ||
		validatedRun.TaskID != taskID || validatedRun.Summary == nil {
		return coveragedomain.Report{}, task.ErrInvalidArgument
	}
	validated, err := coveragedomain.NewReport(report)
	if err != nil || validated.ID != validatedRun.ReportID ||
		validated.RunID != validatedRun.ID || validated.TestRunID != validatedRun.TestRunID ||
		validated.Completeness.Outcome != validatedRun.Outcome ||
		!reflect.DeepEqual(validated.Summary, *validatedRun.Summary) ||
		!reflect.DeepEqual(validated.Toolchain, validatedRun.Toolchain) ||
		validated.ArtifactID != validatedRun.Artifacts.CoverageJSONID ||
		validated.CreatedAt.Before(validatedRun.CreatedAt) ||
		validatedRun.FinishedAt == nil || validated.CreatedAt.After(*validatedRun.FinishedAt) {
		return coveragedomain.Report{}, task.ErrInvalidArgument
	}
	return validated, nil
}

func scanCoverageReport(row rowScanner) (coveragedomain.Report, error) {
	var report coveragedomain.Report
	var createdAt string
	var completenessJSON, summaryJSON, toolchainJSON []byte
	if err := row.Scan(
		&report.ID,
		&report.RunID,
		&report.TestRunID,
		&report.SchemaVersion,
		&createdAt,
		&completenessJSON,
		&summaryJSON,
		&toolchainJSON,
		&report.ArtifactID,
	); err != nil {
		return coveragedomain.Report{}, err
	}
	var err error
	if report.CreatedAt, err = parseCoverageTime(createdAt); err != nil {
		return coveragedomain.Report{}, err
	}
	if report.Completeness, err = decodeCoverageCompleteness(completenessJSON); err != nil {
		return coveragedomain.Report{}, err
	}
	if report.Summary, err = decodeCoverageSummary(summaryJSON); err != nil {
		return coveragedomain.Report{}, err
	}
	if err := decodeCoverageToolchain(toolchainJSON, &report.Toolchain); err != nil {
		return coveragedomain.Report{}, err
	}
	validated, err := coveragedomain.NewReport(report)
	if err != nil {
		return coveragedomain.Report{}, err
	}
	canonicalCompleteness, canonicalSummary, canonicalToolchain, err := encodeCoverageReportMetadata(validated)
	if err != nil || !bytes.Equal(canonicalCompleteness, completenessJSON) ||
		!bytes.Equal(canonicalSummary, summaryJSON) || !bytes.Equal(canonicalToolchain, toolchainJSON) {
		return coveragedomain.Report{}, errors.New("CoverageReport metadata is not canonical")
	}
	return validated, nil
}

func encodeCoverageReportMetadata(report coveragedomain.Report) ([]byte, []byte, []byte, error) {
	completenessJSON, err := json.Marshal(completenessWireFrom(report.Completeness))
	if err != nil {
		return nil, nil, nil, err
	}
	summaryJSON, err := json.Marshal(summaryWireFrom(report.Summary))
	if err != nil {
		return nil, nil, nil, err
	}
	toolchainJSON, err := json.Marshal(toolchainWireFrom(report.Toolchain))
	if err != nil {
		return nil, nil, nil, err
	}
	return completenessJSON, summaryJSON, toolchainJSON, nil
}

func decodeCoverageCompleteness(encoded []byte) (coveragedomain.Completeness, error) {
	var wire coverageCompletenessWire
	if err := decodeStrictJSON(encoded, &wire); err != nil {
		return coveragedomain.Completeness{}, err
	}
	return coveragedomain.Completeness{
		Outcome: wire.Outcome,
		Reasons: append([]coveragedomain.CompletenessReason{}, wire.Reasons...),
	}, nil
}

func completenessWireFrom(value coveragedomain.Completeness) coverageCompletenessWire {
	return coverageCompletenessWire{
		Outcome: value.Outcome,
		Reasons: append([]coveragedomain.CompletenessReason{}, value.Reasons...),
	}
}

var _ task.CoverageTaskStore = (*Store)(nil)
var _ task.CoverageRepository = (*Store)(nil)
