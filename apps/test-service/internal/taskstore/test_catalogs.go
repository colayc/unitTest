package taskstore

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"path"
	"time"

	"unit-test-ide.local/test-service/internal/task"
	"unit-test-ide.local/test-service/internal/testdomain"
)

type catalogEntry struct {
	kind string
	id   testdomain.ID
	json []byte
}

type catalogHeader struct {
	projectID       string
	profileID       string
	revision        string
	generatedAt     time.Time
	containerCount  int
	itemCount       int
	diagnosticCount int
	diagnostics     []testdomain.Diagnostic
}

func (s *Store) PublishCatalog(
	ctx context.Context,
	value testdomain.Catalog,
	artifact task.Artifact,
) error {
	if s == nil || ctx == nil {
		return task.ErrInvalidArgument
	}
	catalog, err := testdomain.NewCatalog(value)
	if err != nil {
		return task.ErrInvalidArgument
	}
	encoded, err := testdomain.EncodeCatalog(catalog)
	if err != nil || !catalogArtifactMatches(artifact, encoded) {
		return task.ErrInvalidArgument
	}
	diagnosticsJSON, err := json.Marshal(catalog.Diagnostics)
	if err != nil {
		return task.ErrInvalidArgument
	}
	entries, err := encodeCatalogEntries(catalog)
	if err != nil {
		return task.ErrInvalidArgument
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return storageError("begin Catalog publication", err)
	}
	defer tx.Rollback()
	if err := insertArtifact(ctx, tx, artifact); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO test_catalogs(
		project_id, profile_id, revision, generated_at, artifact_id,
		container_count, item_count, diagnostic_count, diagnostics_json, partial
	) VALUES(?,?,?,?,?,?,?,?,?,0)`,
		catalog.ProjectID, catalog.ProfileID, catalog.Revision, formatTime(catalog.GeneratedAt),
		artifact.ID, len(catalog.Containers), len(catalog.Items), len(catalog.Diagnostics),
		string(diagnosticsJSON),
	); err != nil {
		return storageError("insert Catalog metadata", err)
	}
	for ordinal, entry := range entries {
		if _, err := tx.ExecContext(ctx, `INSERT INTO test_catalog_entries(
			project_id, profile_id, revision, ordinal, entry_kind, stable_id, payload_json
		) VALUES(?,?,?,?,?,?,?)`,
			catalog.ProjectID, catalog.ProfileID, catalog.Revision, ordinal,
			entry.kind, entry.id.String(), string(entry.json),
		); err != nil {
			return storageError("insert Catalog index", err)
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO current_test_catalogs(project_id, profile_id, revision)
		VALUES(?,?,?)
		ON CONFLICT(project_id, profile_id) DO UPDATE SET revision=excluded.revision`,
		catalog.ProjectID, catalog.ProfileID, catalog.Revision,
	); err != nil {
		return storageError("switch current Catalog", err)
	}
	if err := tx.Commit(); err != nil {
		return storageError("commit Catalog publication", err)
	}
	return nil
}

func (s *Store) GetCatalog(ctx context.Context, projectID, profileID string) (testdomain.Catalog, error) {
	if s == nil || ctx == nil || !validProjectID(projectID) || !lowerHex(profileID, 64) {
		return testdomain.Catalog{}, task.ErrInvalidArgument
	}
	header, err := s.loadCatalogHeader(ctx, projectID, profileID)
	if err != nil {
		return testdomain.Catalog{}, err
	}
	entries, err := s.readCatalogEntries(ctx, header, 0, header.containerCount+header.itemCount)
	if err != nil {
		return testdomain.Catalog{}, err
	}
	catalog := testdomain.Catalog{
		ProjectID: header.projectID, ProfileID: header.profileID, Revision: header.revision,
		GeneratedAt: header.generatedAt, Diagnostics: header.diagnostics,
		Containers: make([]testdomain.Container, 0, header.containerCount),
		Items:      make([]testdomain.Item, 0, header.itemCount),
	}
	for _, entry := range entries {
		switch value := entry.(type) {
		case testdomain.Container:
			catalog.Containers = append(catalog.Containers, value)
		case testdomain.Item:
			catalog.Items = append(catalog.Items, value)
		}
	}
	if len(catalog.Containers) != header.containerCount || len(catalog.Items) != header.itemCount {
		return testdomain.Catalog{}, storageError("validate Catalog index", errors.New("count mismatch"))
	}
	validated, err := testdomain.NewCatalog(catalog)
	if err != nil {
		return testdomain.Catalog{}, storageError("validate Catalog index", err)
	}
	return validated, nil
}

func (s *Store) PageCatalog(
	ctx context.Context,
	request testdomain.CatalogPageRequest,
) (testdomain.CatalogPage, error) {
	if s == nil || ctx == nil || !validProjectID(request.ProjectID) || !lowerHex(request.ProfileID, 64) {
		return testdomain.CatalogPage{}, task.ErrInvalidArgument
	}
	limit := request.Limit
	if limit == 0 {
		limit = testdomain.DefaultCatalogPageSize
	}
	if limit < 1 || limit > testdomain.MaxCatalogPageSize {
		return testdomain.CatalogPage{}, task.ErrInvalidArgument
	}
	header, err := s.loadCatalogHeader(ctx, request.ProjectID, request.ProfileID)
	if err != nil {
		return testdomain.CatalogPage{}, err
	}
	offset := 0
	if request.Cursor != "" {
		cursor, err := decodeCatalogCursor(request.Cursor)
		if err != nil {
			return testdomain.CatalogPage{}, task.ErrInvalidArgument
		}
		if cursor.ProjectID != request.ProjectID || cursor.ProfileID != request.ProfileID ||
			cursor.Revision != header.revision {
			return testdomain.CatalogPage{}, testdomain.ErrCatalogStale
		}
		offset = cursor.Offset
	}
	total := header.containerCount + header.itemCount
	if offset < 0 || offset > total {
		return testdomain.CatalogPage{}, task.ErrInvalidArgument
	}
	entries, err := s.readCatalogEntries(ctx, header, offset, min(limit+1, total-offset))
	if err != nil {
		return testdomain.CatalogPage{}, err
	}
	hasMore := len(entries) > limit
	if hasMore {
		entries = entries[:limit]
	}
	page := testdomain.CatalogPage{
		ProjectID: header.projectID, ProfileID: header.profileID, Revision: header.revision,
		GeneratedAt: header.generatedAt, Diagnostics: append([]testdomain.Diagnostic(nil), header.diagnostics...),
		Containers: []testdomain.Container{}, Items: []testdomain.Item{},
	}
	for _, entry := range entries {
		switch value := entry.(type) {
		case testdomain.Container:
			page.Containers = append(page.Containers, value)
		case testdomain.Item:
			page.Items = append(page.Items, value)
		}
	}
	if hasMore {
		page.NextCursor = encodeCatalogCursor(catalogCursor{
			ProjectID: request.ProjectID, ProfileID: request.ProfileID,
			Revision: header.revision, Offset: offset + limit,
		})
	}
	return page, nil
}

func (s *Store) loadCatalogHeader(ctx context.Context, projectID, profileID string) (catalogHeader, error) {
	var result catalogHeader
	var generatedAt string
	var diagnosticsJSON []byte
	err := s.db.QueryRowContext(ctx, `SELECT
		c.project_id, c.profile_id, c.revision, c.generated_at,
		c.container_count, c.item_count, c.diagnostic_count, c.diagnostics_json
		FROM current_test_catalogs AS current
		JOIN test_catalogs AS c
		  ON c.project_id=current.project_id
		 AND c.profile_id=current.profile_id
		 AND c.revision=current.revision
		WHERE current.project_id=? AND current.profile_id=?`,
		projectID, profileID,
	).Scan(
		&result.projectID, &result.profileID, &result.revision, &generatedAt,
		&result.containerCount, &result.itemCount, &result.diagnosticCount, &diagnosticsJSON,
	)
	if isNoRows(err) {
		return catalogHeader{}, task.ErrNotFound
	}
	if err != nil {
		return catalogHeader{}, storageError("get current Catalog", err)
	}
	result.generatedAt, err = time.Parse(time.RFC3339Nano, generatedAt)
	if err != nil {
		return catalogHeader{}, storageError("decode Catalog metadata", err)
	}
	if err := decodeStrictJSON(diagnosticsJSON, &result.diagnostics); err != nil {
		return catalogHeader{}, storageError("decode Catalog metadata", err)
	}
	if len(result.diagnostics) != result.diagnosticCount {
		return catalogHeader{}, storageError("decode Catalog metadata", errors.New("diagnostic count mismatch"))
	}
	return result, nil
}

func (s *Store) readCatalogEntries(
	ctx context.Context,
	header catalogHeader,
	offset int,
	limit int,
) ([]any, error) {
	if limit == 0 {
		return []any{}, nil
	}
	rows, err := s.db.QueryContext(ctx, `SELECT entry_kind, payload_json
		FROM test_catalog_entries
		WHERE project_id=? AND profile_id=? AND revision=? AND ordinal>=?
		ORDER BY ordinal LIMIT ?`,
		header.projectID, header.profileID, header.revision, offset, limit,
	)
	if err != nil {
		return nil, storageError("page Catalog index", err)
	}
	defer rows.Close()
	result := make([]any, 0, limit)
	for rows.Next() {
		var kind string
		var encoded []byte
		if err := rows.Scan(&kind, &encoded); err != nil {
			return nil, storageError("read Catalog index", err)
		}
		switch kind {
		case "container":
			var value testdomain.Container
			if err := decodeStrictJSON(encoded, &value); err != nil {
				return nil, storageError("decode Catalog container", err)
			}
			result = append(result, value)
		case "item":
			var value testdomain.Item
			if err := decodeStrictJSON(encoded, &value); err != nil {
				return nil, storageError("decode Catalog item", err)
			}
			result = append(result, value)
		default:
			return nil, storageError("decode Catalog entry kind", errors.New("invalid kind"))
		}
	}
	if err := rows.Err(); err != nil {
		return nil, storageError("page Catalog index", err)
	}
	return result, nil
}

func encodeCatalogEntries(catalog testdomain.Catalog) ([]catalogEntry, error) {
	result := make([]catalogEntry, 0, len(catalog.Containers)+len(catalog.Items))
	for _, container := range catalog.Containers {
		encoded, err := json.Marshal(container)
		if err != nil {
			return nil, err
		}
		result = append(result, catalogEntry{kind: "container", id: container.ID, json: encoded})
	}
	for _, item := range catalog.Items {
		encoded, err := json.Marshal(item)
		if err != nil {
			return nil, err
		}
		result = append(result, catalogEntry{kind: "item", id: item.ID, json: encoded})
	}
	return result, nil
}

func catalogArtifactMatches(artifact task.Artifact, encoded []byte) bool {
	sum := sha256.Sum256(encoded)
	return validArtifact(artifact) &&
		artifact.Kind == "test-catalog" &&
		artifact.MIMEType == "application/json" &&
		artifact.RelativePath == path.Join("tasks", artifact.TaskID, artifact.ID+".json") &&
		artifact.Size == int64(len(encoded)) &&
		artifact.SHA256 == hex.EncodeToString(sum[:])
}

type catalogCursor struct {
	ProjectID string `json:"projectId"`
	ProfileID string `json:"profileId"`
	Revision  string `json:"revision"`
	Offset    int    `json:"offset"`
}

func encodeCatalogCursor(value catalogCursor) string {
	encoded, _ := json.Marshal(value)
	return base64.RawURLEncoding.EncodeToString(encoded)
}

func decodeCatalogCursor(value string) (catalogCursor, error) {
	if len(value) > 4096 {
		return catalogCursor{}, errors.New("cursor too long")
	}
	encoded, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return catalogCursor{}, err
	}
	var result catalogCursor
	if err := decodeStrictJSON(encoded, &result); err != nil ||
		!validProjectID(result.ProjectID) || !lowerHex(result.ProfileID, 64) ||
		!lowerHex(result.Revision, 64) || result.Offset < 0 {
		return catalogCursor{}, errors.New("invalid Catalog cursor")
	}
	return result, nil
}

func decodeStrictJSON(encoded []byte, destination any) error {
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("multiple JSON values")
	}
	return nil
}

var _ task.TestCatalogRepository = (*Store)(nil)
