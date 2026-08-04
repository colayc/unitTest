package taskstore

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"unit-test-ide.local/test-service/internal/coveragedomain"
	"unit-test-ide.local/test-service/internal/task"
	"unit-test-ide.local/test-service/internal/testdomain"
)

const coverageRunSelect = `SELECT
	coverage_run_id, task_id, test_run_id, idempotency_key, request_json,
	workspace_generation, project_id, coverage_profile_id, catalog_revision,
	selection_snapshot_json, repeat_count, timeout_ms, status, outcome, reason,
	toolchain_json, summary_json, report_id, coverage_json_artifact_id,
	junit_xml_artifact_id, coverage_html_artifact_id, created_at, started_at,
	finished_at, last_sequence
	FROM coverage_runs`

const coverageTimeLayout = "2006-01-02T15:04:05.000000000Z"

// coverageRunCursor deliberately binds a cursor to the complete history scope.
type coverageRunCursor struct {
	WorkspaceGeneration string    `json:"workspaceGeneration"`
	ProjectID           string    `json:"projectId"`
	CoverageProfileID   string    `json:"coverageProfileId"`
	CreatedAt           time.Time `json:"createdAt"`
	CoverageRunID       string    `json:"coverageRunId"`
}

type coverageRequestWire struct {
	IdempotencyKey      string                `json:"idempotencyKey"`
	WorkspaceGeneration string                `json:"workspaceGeneration"`
	ProjectID           string                `json:"projectId"`
	CoverageProfileID   string                `json:"coverageProfileId"`
	CatalogRevision     string                `json:"catalogRevision"`
	Selection           coverageSelectionWire `json:"selection"`
	RepeatCount         int64                 `json:"repeatCount"`
	TimeoutMS           int64                 `json:"timeoutMs"`
}

type coverageSelectionWire struct {
	Mode         testdomain.SelectionMode `json:"mode"`
	ContainerIDs []testdomain.ID          `json:"containerIds,omitempty"`
	ItemIDs      []testdomain.ID          `json:"itemIds,omitempty"`
	Filter       *coverageFilterWire      `json:"filter,omitempty"`
	RunID        string                   `json:"runId,omitempty"`
}

type coverageFilterWire struct {
	Group          string          `json:"group,omitempty"`
	Suite          string          `json:"suite,omitempty"`
	Label          string          `json:"label,omitempty"`
	NameContains   string          `json:"nameContains,omitempty"`
	IncludeItemIDs []testdomain.ID `json:"includeItemIds,omitempty"`
	ExcludeItemIDs []testdomain.ID `json:"excludeItemIds,omitempty"`
}

type coverageSelectionSnapshotWire struct {
	Mode         testdomain.SelectionMode `json:"mode"`
	ContainerIDs []testdomain.ID          `json:"containerIds"`
	ItemIDs      []testdomain.ID          `json:"itemIds"`
	SourceRunID  string                   `json:"sourceRunId,omitempty"`
}

type coverageToolchainWire struct {
	Platform                   coveragedomain.Platform     `json:"platform"`
	Architecture               coveragedomain.Architecture `json:"architecture"`
	Compiler                   coverageCompilerWire        `json:"compiler"`
	Driver                     coverageDriverWire          `json:"driver"`
	Collector                  coverageCollectorWire       `json:"collector"`
	NormalizerVersion          string                      `json:"normalizerVersion"`
	InstrumentationFingerprint string                      `json:"instrumentationFingerprint"`
}

type coverageCompilerWire struct {
	Family  coveragedomain.CompilerFamily `json:"family"`
	Version string                        `json:"version"`
}

type coverageDriverWire struct {
	Name    coveragedomain.DriverName `json:"name"`
	Version string                    `json:"version"`
}

type coverageCollectorWire struct {
	Name    coveragedomain.CollectorName `json:"name"`
	Version string                       `json:"version"`
}

type coverageMetricWire struct {
	Covered int64 `json:"covered"`
	Total   int64 `json:"total"`
}

type coverageSummaryWire struct {
	Lines     coverageMetricWire `json:"lines"`
	Branches  coverageMetricWire `json:"branches"`
	Functions coverageMetricWire `json:"functions"`
}

type coverageArtifactRefsWire struct {
	CoverageJSONID string `json:"coverageJsonId"`
	JUnitXMLID     string `json:"junitXmlId"`
	CoverageHTMLID string `json:"coverageHtmlId"`
}

func (s *Store) GetCoverageRun(ctx context.Context, runID string) (coveragedomain.Run, error) {
	if s == nil || ctx == nil || !lowerHex(runID, 32) {
		return coveragedomain.Run{}, task.ErrInvalidArgument
	}
	run, err := scanCoverageRun(s.db.QueryRowContext(ctx, coverageRunSelect+` WHERE coverage_run_id=?`, runID))
	if isNoRows(err) {
		return coveragedomain.Run{}, task.ErrNotFound
	}
	if err != nil {
		return coveragedomain.Run{}, storageError("get CoverageRun", err)
	}
	return run.Clone(), nil
}

func (s *Store) ListCoverageRuns(ctx context.Context, request coveragedomain.RunPageRequest) (coveragedomain.RunPage, error) {
	if s == nil || ctx == nil || !lowerHex(request.WorkspaceGeneration, 64) ||
		(request.ProjectID != "" && !validProjectID(request.ProjectID)) ||
		(request.CoverageProfileID != "" && !validProjectID(request.CoverageProfileID)) {
		return coveragedomain.RunPage{}, task.ErrInvalidArgument
	}
	limit := request.Limit
	if limit == 0 {
		limit = coveragedomain.DefaultRunPageSize
	}
	if limit < 1 || limit > coveragedomain.MaxRunPageSize {
		return coveragedomain.RunPage{}, task.ErrInvalidArgument
	}

	var cursor coverageRunCursor
	if request.Cursor != "" {
		decoded, err := decodeCoverageRunCursor(request.Cursor)
		if err != nil || decoded.WorkspaceGeneration != request.WorkspaceGeneration ||
			decoded.ProjectID != request.ProjectID || decoded.CoverageProfileID != request.CoverageProfileID {
			return coveragedomain.RunPage{}, task.ErrInvalidArgument
		}
		cursor = decoded
	}

	conditions := []string{"workspace_generation=?"}
	args := []any{request.WorkspaceGeneration}
	if request.ProjectID != "" {
		conditions = append(conditions, "project_id=?")
		args = append(args, request.ProjectID)
	}
	if request.CoverageProfileID != "" {
		conditions = append(conditions, "coverage_profile_id=?")
		args = append(args, request.CoverageProfileID)
	}
	if request.Cursor != "" {
		conditions = append(conditions, "(created_at < ? OR (created_at = ? AND coverage_run_id < ?))")
		args = append(args, formatCoverageTime(cursor.CreatedAt), formatCoverageTime(cursor.CreatedAt), cursor.CoverageRunID)
	}
	query := coverageRunSelect + " WHERE " + strings.Join(conditions, " AND ") + " ORDER BY created_at DESC, coverage_run_id DESC LIMIT ?"
	args = append(args, limit+1)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return coveragedomain.RunPage{}, storageError("list CoverageRuns", err)
	}
	defer rows.Close()
	items := make([]coveragedomain.Run, 0, limit+1)
	for rows.Next() {
		run, err := scanCoverageRun(rows)
		if err != nil {
			return coveragedomain.RunPage{}, storageError("read CoverageRuns", err)
		}
		items = append(items, run.Clone())
	}
	if err := rows.Err(); err != nil {
		return coveragedomain.RunPage{}, storageError("read CoverageRuns", err)
	}
	page := coveragedomain.RunPage{Items: items}
	if len(items) > limit {
		last := items[limit-1]
		page.Items = append([]coveragedomain.Run(nil), items[:limit]...)
		page.NextCursor = encodeCoverageRunCursor(coverageRunCursor{
			WorkspaceGeneration: request.WorkspaceGeneration,
			ProjectID:           request.ProjectID, CoverageProfileID: request.CoverageProfileID,
			CreatedAt: last.CreatedAt, CoverageRunID: last.ID,
		})
	}
	return page, nil
}

// insertCoverageRun is transaction-only plumbing for the atomic coverage task path.
// It intentionally has no public Store counterpart.
func insertCoverageRun(ctx context.Context, tx *sql.Tx, value coveragedomain.Run) error {
	run, err := coveragedomain.NewRun(value)
	if err != nil {
		return task.ErrInvalidArgument
	}
	requestJSON, err := run.Request.CanonicalJSON()
	if err != nil {
		return task.ErrInvalidArgument
	}
	selectionJSON, err := json.Marshal(selectionSnapshotWireFrom(run.SelectionSnapshot))
	if err != nil {
		return task.ErrInvalidArgument
	}
	toolchainJSON, err := json.Marshal(toolchainWireFrom(run.Toolchain))
	if err != nil {
		return task.ErrInvalidArgument
	}
	var summaryJSON any
	if run.Summary != nil {
		encoded, err := json.Marshal(summaryWireFrom(*run.Summary))
		if err != nil {
			return task.ErrInvalidArgument
		}
		summaryJSON = string(encoded)
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO coverage_runs(
		coverage_run_id, task_id, test_run_id, idempotency_key, request_json,
		workspace_generation, project_id, coverage_profile_id, catalog_revision,
		selection_snapshot_json, repeat_count, timeout_ms, status, outcome, reason,
		toolchain_json, summary_json, report_id, coverage_json_artifact_id,
		junit_xml_artifact_id, coverage_html_artifact_id, created_at, started_at,
		finished_at, last_sequence
	) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		run.ID, run.TaskID, run.TestRunID, run.Request.IdempotencyKey, string(requestJSON),
		run.Request.WorkspaceGeneration, run.Request.ProjectID, run.Request.CoverageProfileID, run.Request.CatalogRevision,
		string(selectionJSON), run.Request.RepeatCount, run.Request.Timeout.Milliseconds(), string(run.Status),
		nullableCoverageString(string(run.Outcome)), nullableCoverageString(string(run.Reason)), string(toolchainJSON), summaryJSON,
		nullableCoverageString(run.ReportID), nullableCoverageString(run.Artifacts.CoverageJSONID),
		nullableCoverageString(run.Artifacts.JUnitXMLID), nullableCoverageString(run.Artifacts.CoverageHTMLID),
		formatCoverageTime(run.CreatedAt), nullableCoverageTime(run.StartedAt), nullableCoverageTime(run.FinishedAt), run.LastSequence,
	)
	if err != nil {
		return storageError("insert CoverageRun", err)
	}
	return nil
}

func scanCoverageRun(row rowScanner) (coveragedomain.Run, error) {
	var (
		run                                                                                coveragedomain.Run
		requestJSON, selectionJSON, toolchainJSON                                          []byte
		idempotencyKey, workspaceGeneration, projectID, coverageProfileID, catalogRevision string
		repeatCount, timeoutMS                                                             int64
		status                                                                             string
		outcome, reason, summaryJSON, reportID                                             sql.NullString
		coverageJSONID, junitXMLID, coverageHTMLID                                         sql.NullString
		createdAt                                                                          string
		startedAt, finishedAt                                                              sql.NullString
	)
	if err := row.Scan(&run.ID, &run.TaskID, &run.TestRunID, &idempotencyKey, &requestJSON,
		&workspaceGeneration, &projectID, &coverageProfileID, &catalogRevision, &selectionJSON,
		&repeatCount, &timeoutMS, &status, &outcome, &reason, &toolchainJSON, &summaryJSON,
		&reportID, &coverageJSONID, &junitXMLID, &coverageHTMLID, &createdAt, &startedAt,
		&finishedAt, &run.LastSequence); err != nil {
		return coveragedomain.Run{}, err
	}
	request, err := decodeCoverageRequest(requestJSON)
	if err != nil || request.IdempotencyKey != idempotencyKey || request.WorkspaceGeneration != workspaceGeneration ||
		request.ProjectID != projectID || request.CoverageProfileID != coverageProfileID || request.CatalogRevision != catalogRevision ||
		request.RepeatCount != repeatCount || request.Timeout.Milliseconds() != timeoutMS {
		return coveragedomain.Run{}, errors.New("invalid persisted CoverageRun request")
	}
	if err := decodeCoverageSelectionSnapshot(selectionJSON, &run.SelectionSnapshot); err != nil {
		return coveragedomain.Run{}, err
	}
	if err := decodeCoverageToolchain(toolchainJSON, &run.Toolchain); err != nil {
		return coveragedomain.Run{}, err
	}
	if summaryJSON.Valid {
		summary, err := decodeCoverageSummary([]byte(summaryJSON.String))
		if err != nil {
			return coveragedomain.Run{}, err
		}
		run.Summary = &summary
	}
	run.Request = request
	run.Status = coveragedomain.Status(status)
	if outcome.Valid {
		run.Outcome = coveragedomain.Outcome(outcome.String)
	}
	if reason.Valid {
		run.Reason = coveragedomain.Reason(reason.String)
	}
	if reportID.Valid {
		run.ReportID = reportID.String
	}
	run.Artifacts = artifactRefsFromWire(coverageArtifactRefsWire{CoverageJSONID: nullString(coverageJSONID), JUnitXMLID: nullString(junitXMLID), CoverageHTMLID: nullString(coverageHTMLID)})
	if run.CreatedAt, err = parseCoverageTime(createdAt); err != nil {
		return coveragedomain.Run{}, err
	}
	if run.StartedAt, err = parseNullableCoverageTime(startedAt); err != nil {
		return coveragedomain.Run{}, err
	}
	if run.FinishedAt, err = parseNullableCoverageTime(finishedAt); err != nil {
		return coveragedomain.Run{}, err
	}
	validated, err := coveragedomain.NewRun(run)
	if err != nil {
		return coveragedomain.Run{}, err
	}
	return validated, nil
}

func decodeCoverageRequest(encoded []byte) (coveragedomain.Request, error) {
	var wire coverageRequestWire
	if err := decodeStrictJSON(encoded, &wire); err != nil {
		return coveragedomain.Request{}, err
	}
	if wire.TimeoutMS < 0 || wire.TimeoutMS > maxCoverageDurationMilliseconds {
		return coveragedomain.Request{}, errors.New("coverage request timeout milliseconds overflow")
	}
	request, err := coveragedomain.NewRequest(coveragedomain.Request{
		IdempotencyKey: wire.IdempotencyKey, WorkspaceGeneration: wire.WorkspaceGeneration,
		ProjectID: wire.ProjectID, CoverageProfileID: wire.CoverageProfileID, CatalogRevision: wire.CatalogRevision,
		Selection: selectionFromWire(wire.Selection), RepeatCount: wire.RepeatCount,
		Timeout: time.Duration(wire.TimeoutMS) * time.Millisecond,
	})
	if err != nil {
		return coveragedomain.Request{}, err
	}
	canonical, err := request.CanonicalJSON()
	if err != nil || !bytes.Equal(canonical, encoded) {
		return coveragedomain.Request{}, errors.New("coverage request is not canonical")
	}
	return request, nil
}

const maxCoverageDurationMilliseconds = int64(^uint64(0)>>1) / int64(time.Millisecond)

func decodeCoverageSelectionSnapshot(encoded []byte, destination *testdomain.SelectionSnapshot) error {
	var wire coverageSelectionSnapshotWire
	if err := decodeStrictJSON(encoded, &wire); err != nil {
		return err
	}
	*destination = testdomain.SelectionSnapshot{Mode: wire.Mode, ContainerIDs: append([]testdomain.ID(nil), wire.ContainerIDs...), ItemIDs: append([]testdomain.ID(nil), wire.ItemIDs...), SourceRunID: wire.SourceRunID}
	return nil
}

func decodeCoverageToolchain(encoded []byte, destination *coveragedomain.ToolchainSnapshot) error {
	var wire coverageToolchainWire
	if err := decodeStrictJSON(encoded, &wire); err != nil {
		return err
	}
	*destination = coveragedomain.ToolchainSnapshot{Platform: wire.Platform, Architecture: wire.Architecture,
		Compiler:          coveragedomain.CompilerSnapshot{Family: wire.Compiler.Family, Version: wire.Compiler.Version},
		Driver:            coveragedomain.DriverSnapshot{Name: wire.Driver.Name, Version: wire.Driver.Version},
		Collector:         coveragedomain.CollectorSnapshot{Name: wire.Collector.Name, Version: wire.Collector.Version},
		NormalizerVersion: wire.NormalizerVersion, InstrumentationFingerprint: wire.InstrumentationFingerprint}
	return nil
}

func decodeCoverageSummary(encoded []byte) (coveragedomain.Summary, error) {
	var wire coverageSummaryWire
	if err := decodeStrictJSON(encoded, &wire); err != nil {
		return coveragedomain.Summary{}, err
	}
	return coveragedomain.NewSummary(coveragedomain.Summary{Lines: metricFromWire(wire.Lines), Branches: metricFromWire(wire.Branches), Functions: metricFromWire(wire.Functions)})
}

func selectionFromWire(value coverageSelectionWire) testdomain.Selection {
	result := testdomain.Selection{Mode: value.Mode, ContainerIDs: append([]testdomain.ID(nil), value.ContainerIDs...), ItemIDs: append([]testdomain.ID(nil), value.ItemIDs...), RunID: value.RunID}
	if value.Filter != nil {
		result.Filter = testdomain.Filter{Group: value.Filter.Group, Suite: value.Filter.Suite, Label: value.Filter.Label, NameContains: value.Filter.NameContains, IncludeItemIDs: append([]testdomain.ID(nil), value.Filter.IncludeItemIDs...), ExcludeItemIDs: append([]testdomain.ID(nil), value.Filter.ExcludeItemIDs...)}
	}
	return result
}

func selectionSnapshotWireFrom(value testdomain.SelectionSnapshot) coverageSelectionSnapshotWire {
	return coverageSelectionSnapshotWire{Mode: value.Mode, ContainerIDs: append([]testdomain.ID(nil), value.ContainerIDs...), ItemIDs: append([]testdomain.ID(nil), value.ItemIDs...), SourceRunID: value.SourceRunID}
}

func toolchainWireFrom(value coveragedomain.ToolchainSnapshot) coverageToolchainWire {
	return coverageToolchainWire{Platform: value.Platform, Architecture: value.Architecture, Compiler: coverageCompilerWire{Family: value.Compiler.Family, Version: value.Compiler.Version}, Driver: coverageDriverWire{Name: value.Driver.Name, Version: value.Driver.Version}, Collector: coverageCollectorWire{Name: value.Collector.Name, Version: value.Collector.Version}, NormalizerVersion: value.NormalizerVersion, InstrumentationFingerprint: value.InstrumentationFingerprint}
}

func summaryWireFrom(value coveragedomain.Summary) coverageSummaryWire {
	return coverageSummaryWire{Lines: metricWireFrom(value.Lines), Branches: metricWireFrom(value.Branches), Functions: metricWireFrom(value.Functions)}
}

func metricWireFrom(value coveragedomain.Metric) coverageMetricWire {
	return coverageMetricWire{Covered: value.Covered, Total: value.Total}
}
func metricFromWire(value coverageMetricWire) coveragedomain.Metric {
	return coveragedomain.Metric{Covered: value.Covered, Total: value.Total}
}
func artifactRefsFromWire(value coverageArtifactRefsWire) coveragedomain.ArtifactRefs {
	return coveragedomain.ArtifactRefs{CoverageJSONID: value.CoverageJSONID, JUnitXMLID: value.JUnitXMLID, CoverageHTMLID: value.CoverageHTMLID}
}
func nullString(value sql.NullString) string {
	if value.Valid {
		return value.String
	}
	return ""
}
func nullableCoverageString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func formatCoverageTime(value time.Time) string {
	return value.UTC().Format(coverageTimeLayout)
}

func nullableCoverageTime(value *time.Time) any {
	if value == nil {
		return nil
	}
	return formatCoverageTime(*value)
}

func parseCoverageTime(value string) (time.Time, error) {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil || parsed.IsZero() || !strings.HasSuffix(value, "Z") || parsed.Location() != time.UTC || value != formatCoverageTime(parsed) {
		return time.Time{}, errors.New("invalid CoverageRun timestamp")
	}
	return parsed, nil
}

func parseNullableCoverageTime(value sql.NullString) (*time.Time, error) {
	if !value.Valid {
		return nil, nil
	}
	parsed, err := parseCoverageTime(value.String)
	if err != nil {
		return nil, err
	}
	return &parsed, nil
}

func encodeCoverageRunCursor(value coverageRunCursor) string {
	encoded, _ := json.Marshal(value)
	return base64.RawURLEncoding.EncodeToString(encoded)
}

func decodeCoverageRunCursor(value string) (coverageRunCursor, error) {
	if len(value) > 4096 {
		return coverageRunCursor{}, errors.New("CoverageRun cursor too long")
	}
	encoded, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return coverageRunCursor{}, err
	}
	var result coverageRunCursor
	if err := decodeStrictJSON(encoded, &result); err != nil || !lowerHex(result.WorkspaceGeneration, 64) ||
		(result.ProjectID != "" && !validProjectID(result.ProjectID)) || (result.CoverageProfileID != "" && !validProjectID(result.CoverageProfileID)) ||
		!lowerHex(result.CoverageRunID, 32) || result.CreatedAt.IsZero() || result.CreatedAt.Location() != time.UTC {
		return coverageRunCursor{}, errors.New("invalid CoverageRun cursor")
	}
	return result, nil
}
