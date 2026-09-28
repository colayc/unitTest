package taskstore

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"hash"
	"sort"

	"unit-test-ide.local/test-service/internal/coveragedetail"
	"unit-test-ide.local/test-service/internal/task"
)

type sourceCommitment struct {
	path, sha256 string
}

func sourceCommitments(files []coveragedetail.File) ([]sourceCommitment, error) {
	if len(files) > maxAttestedFiles {
		return nil, task.ErrInvalidArgument
	}
	values := make([]sourceCommitment, len(files))
	for i, file := range files {
		values[i] = sourceCommitment{path: file.RelativePath, sha256: file.SourceSHA256}
	}
	sort.Slice(values, func(i, j int) bool { return values[i].path < values[j].path })
	for i, value := range values {
		if value.path == "" || len(value.path) > 4096 || !lowerHex(value.sha256, 64) || i > 0 && values[i-1].path == value.path {
			return nil, task.ErrInvalidArgument
		}
	}
	return values, nil
}

func writeManifestPart(h hash.Hash, value string) {
	var size [8]byte
	binary.BigEndian.PutUint64(size[:], uint64(len(value)))
	h.Write(size[:])
	h.Write([]byte(value))
}

func sourceManifestDigest(projectID, reportID, generation string, values []sourceCommitment) string {
	h := sha256.New()
	writeManifestPart(h, "coverage-source-manifest-v1")
	writeManifestPart(h, projectID)
	writeManifestPart(h, reportID)
	writeManifestPart(h, generation)
	var count [8]byte
	binary.BigEndian.PutUint64(count[:], uint64(len(values)))
	h.Write(count[:])
	for _, value := range values {
		writeManifestPart(h, value.path)
		writeManifestPart(h, value.sha256)
	}
	return hex.EncodeToString(h.Sum(nil))
}

func insertCoverageSourceManifest(ctx context.Context, tx *sql.Tx, index coveragedetail.Index) error {
	values, err := sourceCommitments(index.Files)
	if err != nil {
		return err
	}
	digest := sourceManifestDigest(index.ProjectID, index.ReportID, index.WorkspaceGeneration, values)
	if _, err := tx.ExecContext(ctx, `INSERT INTO coverage_detail_manifests(report_id,project_id,workspace_generation,file_count,manifest_sha256) VALUES(?,?,?,?,?)`, index.ReportID, index.ProjectID, index.WorkspaceGeneration, len(values), digest); err != nil {
		return storageError("insert CoverageDetail source manifest", err)
	}
	for _, value := range values {
		if _, err := tx.ExecContext(ctx, `INSERT INTO coverage_detail_manifest_files(report_id,relative_path,source_sha256) VALUES(?,?,?)`, index.ReportID, value.path, value.sha256); err != nil {
			return storageError("insert CoverageDetail source manifest file", err)
		}
	}
	return nil
}

func validateCoverageSourceManifest(ctx context.Context, tx *sql.Tx, index coveragedetail.Index) error {
	var projectID, generation, digest string
	var count int
	err := tx.QueryRowContext(ctx, `SELECT project_id,workspace_generation,file_count,manifest_sha256 FROM coverage_detail_manifests WHERE report_id=?`, index.ReportID).Scan(&projectID, &generation, &count, &digest)
	if err != nil {
		return storageError("read CoverageDetail source manifest", err)
	}
	if projectID != index.ProjectID || generation != index.WorkspaceGeneration || count < 0 || count > maxAttestedFiles || count != len(index.Files) || !lowerHex(digest, 64) {
		return storageError("validate CoverageDetail source manifest header", nil)
	}
	rows, err := tx.QueryContext(ctx, `SELECT relative_path,source_sha256 FROM coverage_detail_manifest_files WHERE report_id=? ORDER BY relative_path LIMIT ?`, index.ReportID, maxAttestedFiles+1)
	if err != nil {
		return storageError("read CoverageDetail manifest files", err)
	}
	values := make([]sourceCommitment, 0, count)
	for rows.Next() {
		var value sourceCommitment
		if err := rows.Scan(&value.path, &value.sha256); err != nil {
			rows.Close()
			return storageError("scan CoverageDetail manifest file", err)
		}
		values = append(values, value)
	}
	if err := rows.Err(); err != nil || len(values) != count {
		rows.Close()
		return storageError("CoverageDetail manifest file count", err)
	}
	rows.Close()
	if sourceManifestDigest(projectID, index.ReportID, generation, values) != digest {
		return storageError("CoverageDetail source manifest digest", nil)
	}
	actual, err := sourceCommitments(index.Files)
	if err != nil || len(actual) != len(values) {
		return storageError("CoverageDetail source set", err)
	}
	for i := range values {
		if actual[i] != values[i] {
			return storageError("CoverageDetail source set mismatch", nil)
		}
	}
	return nil
}
