package taskstore

import (
	"context"
	"database/sql"
	"encoding/hex"
	"path"
	"strings"
	"time"

	"unit-test-ide.local/test-service/internal/coveragedomain"
	"unit-test-ide.local/test-service/internal/task"
)

const (
	coverageJSONArtifactKind = "coverage-json"
	junitXMLArtifactKind     = "junit-xml"
	coverageHTMLArtifactKind = "coverage-html"
	applicationJSONMIMEType  = "application/json"
	applicationXMLMIMEType   = "application/xml"
	textHTMLMIMEType         = "text/html"
)

func insertArtifact(ctx context.Context, tx *sql.Tx, artifact task.Artifact) error {
	if !validArtifact(artifact) {
		return task.ErrInvalidArgument
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO artifacts(
		artifact_id, task_id, kind, relative_path, mime_type, size_bytes, sha256, created_at, complete
	) VALUES(?,?,?,?,?,?,?,?,1)`, artifact.ID, artifact.TaskID, artifact.Kind, artifact.RelativePath,
		artifact.MIMEType, artifact.Size, artifact.SHA256, formatTime(artifact.CreatedAt)); err != nil {
		return storageError("insert artifact metadata", err)
	}
	return nil
}

func validArtifact(value task.Artifact) bool {
	if value.ID == "" || value.TaskID == "" || value.Kind == "" || value.RelativePath == "" || value.MIMEType == "" ||
		value.Size < 0 || value.CreatedAt.IsZero() || len(value.SHA256) != 64 || !canonicalArtifactPath(value.RelativePath) {
		return false
	}
	_, err := hex.DecodeString(value.SHA256)
	return err == nil
}

func validateCoverageArtifacts(run coveragedomain.Run, artifacts []task.Artifact) error {
	if err := validateRunArtifacts(run.TaskID, artifacts); err != nil {
		return err
	}
	byID := make(map[string]task.Artifact, len(artifacts))
	for _, artifact := range artifacts {
		byID[artifact.ID] = artifact
	}
	public := map[string]string{
		coverageJSONArtifactKind: run.Artifacts.CoverageJSONID,
		junitXMLArtifactKind:     run.Artifacts.JUnitXMLID,
		coverageHTMLArtifactKind: run.Artifacts.CoverageHTMLID,
	}
	for _, artifact := range artifacts {
		switch artifact.Kind {
		case coverageJSONArtifactKind, junitXMLArtifactKind, coverageHTMLArtifactKind,
			"diagnostics", "stdout", "stderr":
		default:
			return task.ErrInvalidArgument
		}
	}
	if run.Outcome != coveragedomain.OutcomeAvailable && run.Outcome != coveragedomain.OutcomePartial {
		for _, artifact := range artifacts {
			if _, exists := public[artifact.Kind]; exists {
				return task.ErrInvalidArgument
			}
		}
		return nil
	}
	expected := []struct {
		id, kind, mime string
	}{
		{run.Artifacts.CoverageJSONID, coverageJSONArtifactKind, applicationJSONMIMEType},
		{run.Artifacts.JUnitXMLID, junitXMLArtifactKind, applicationXMLMIMEType},
		{run.Artifacts.CoverageHTMLID, coverageHTMLArtifactKind, textHTMLMIMEType},
	}
	for _, reference := range expected {
		artifact, exists := byID[reference.id]
		if !exists || artifact.Kind != reference.kind || artifact.MIMEType != reference.mime {
			return task.ErrInvalidArgument
		}
	}
	for _, artifact := range artifacts {
		if expectedID, exists := public[artifact.Kind]; exists && artifact.ID != expectedID {
			return task.ErrInvalidArgument
		}
	}
	return nil
}

func canonicalArtifactPath(value string) bool {
	if value == "" || path.IsAbs(value) {
		return false
	}
	clean := path.Clean(value)
	if clean != value || clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return false
	}
	for _, segment := range strings.Split(value, "/") {
		if !portableArtifactSegment(segment) {
			return false
		}
	}
	return true
}

func portableArtifactSegment(segment string) bool {
	if segment == "" || strings.HasSuffix(segment, ".") || strings.HasSuffix(segment, " ") {
		return false
	}
	for index := range len(segment) {
		value := segment[index]
		if value <= 0x1f || strings.ContainsRune(`<>:"|?*\`, rune(value)) {
			return false
		}
	}
	base, _, _ := strings.Cut(segment, ".")
	upper := strings.ToUpper(base)
	if upper == "CON" || upper == "PRN" || upper == "AUX" || upper == "NUL" {
		return false
	}
	if len(upper) == 4 && (strings.HasPrefix(upper, "COM") || strings.HasPrefix(upper, "LPT")) && upper[3] >= '1' && upper[3] <= '9' {
		return false
	}
	return true
}

func (s *Store) ListArtifacts(ctx context.Context, taskID, cursor string, limit int) (task.Page[task.Artifact], error) {
	if taskID == "" || limit < 1 || limit > maxPageSize {
		return task.Page[task.Artifact]{}, task.ErrInvalidArgument
	}
	query := artifactSelect + ` WHERE task_id=?`
	args := []any{taskID}
	if cursor != "" {
		createdAt, artifactID, err := decodeCursor(cursor)
		if err != nil {
			return task.Page[task.Artifact]{}, task.ErrInvalidArgument
		}
		query += ` AND (created_at > ? OR (created_at = ? AND artifact_id > ?))`
		args = append(args, formatTime(createdAt), formatTime(createdAt), artifactID)
	}
	query += ` ORDER BY created_at, artifact_id LIMIT ?`
	args = append(args, limit+1)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return task.Page[task.Artifact]{}, storageError("list artifact metadata", err)
	}
	defer rows.Close()
	items := make([]task.Artifact, 0, limit+1)
	for rows.Next() {
		item, err := scanArtifact(rows)
		if err != nil {
			return task.Page[task.Artifact]{}, storageError("read artifact metadata", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return task.Page[task.Artifact]{}, storageError("list artifact metadata", err)
	}
	page := task.Page[task.Artifact]{Items: items}
	if len(items) > limit {
		last := items[limit-1]
		page.Items = items[:limit]
		page.NextCursor = encodeCursor(last.CreatedAt, last.ID)
	}
	return page, nil
}

func (s *Store) GetArtifact(ctx context.Context, artifactID string) (task.Artifact, error) {
	result, err := scanArtifact(s.db.QueryRowContext(ctx, artifactSelect+` WHERE artifact_id=?`, artifactID))
	if isNoRows(err) {
		return task.Artifact{}, task.ErrNotFound
	}
	if err != nil {
		return task.Artifact{}, storageError("get artifact metadata", err)
	}
	return result, nil
}

const artifactSelect = `SELECT artifact_id, task_id, kind, relative_path, mime_type, size_bytes, sha256, created_at FROM artifacts`

func scanArtifact(row rowScanner) (task.Artifact, error) {
	var result task.Artifact
	var createdAt string
	if err := row.Scan(&result.ID, &result.TaskID, &result.Kind, &result.RelativePath, &result.MIMEType,
		&result.Size, &result.SHA256, &createdAt); err != nil {
		return task.Artifact{}, err
	}
	parsed, err := time.Parse(time.RFC3339Nano, createdAt)
	if err != nil {
		return task.Artifact{}, err
	}
	result.CreatedAt = parsed
	return result, nil
}

func (s *Store) ReferencedArtifactPaths(ctx context.Context) (map[string]struct{}, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT relative_path FROM artifacts WHERE complete=1`)
	if err != nil {
		return nil, storageError("list artifact references", err)
	}
	defer rows.Close()
	paths := make(map[string]struct{})
	for rows.Next() {
		var relativePath string
		if err := rows.Scan(&relativePath); err != nil {
			return nil, storageError("read artifact reference", err)
		}
		paths[relativePath] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return nil, storageError("list artifact references", err)
	}
	return paths, nil
}
