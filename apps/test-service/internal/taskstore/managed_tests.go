package taskstore

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"runtime"
	"strings"
	"time"

	"unit-test-ide.local/test-service/internal/coveragedetail"
	"unit-test-ide.local/test-service/internal/managedtest"
	"unit-test-ide.local/test-service/internal/task"
)

// ManagedRegistry deliberately has its own Get/List names: Store.Get/List are
// the long-standing v1 task APIs and cannot be changed by v1.6.
type ManagedRegistry struct{ store *Store }

var _ managedtest.Registry = (*ManagedRegistry)(nil)
var _ managedtest.AcceptanceJournal = (*ManagedRegistry)(nil)

func (s *Store) ManagedTestRegistry() *ManagedRegistry { return &ManagedRegistry{store: s} }

func (s *Store) ManagedTestsReady() bool {
	if s == nil || !s.managedAvailable || s.managedInvalid {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var count, pending int
	var key []byte
	if err := s.db.QueryRowContext(ctx, `SELECT count(*),cursor_key FROM managed_test_registry_meta WHERE singleton=1`).Scan(&count, &key); err != nil || count != 1 || len(key) != 32 {
		return false
	}
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM managed_test_pending_acceptances`).Scan(&pending); err != nil || pending != 0 {
		return false
	}
	for _, table := range []string{"managed_test_records", "managed_test_transitions", "managed_test_commits"} {
		if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM `+table).Scan(&count); err != nil {
			return false
		}
	}
	rows, err := s.db.QueryContext(ctx, managedRecordSelect+` ORDER BY case_id`)
	if err != nil {
		return false
	}
	type transitionHead struct {
		record   managedtest.Record
		revision int
	}
	heads := []transitionHead{}
	for rows.Next() {
		record, revision, err := scanManagedRecord(rows)
		if err != nil {
			rows.Close()
			return false
		}
		heads = append(heads, transitionHead{record, revision})
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return false
	}
	if err := rows.Close(); err != nil {
		return false
	}
	for _, head := range heads {
		record := head.record
		chain, err := s.db.QueryContext(ctx, `SELECT revision,from_status,to_status,reason,receipt_digest,acceptance_id,record_digest,occurred_at FROM managed_test_transitions WHERE case_id=? ORDER BY revision`, record.CaseID)
		if err != nil {
			return false
		}
		previous := "none"
		number := 0
		lastAcceptedReceipt := ""
		type acceptedCommit struct{ id, digest string }
		accepted := []acceptedCommit{}
		for chain.Next() {
			var revision int
			var from, to, reason, receipt, at string
			var acceptanceID, recordDigest sql.NullString
			if err := chain.Scan(&revision, &from, &to, &reason, &receipt, &acceptanceID, &recordDigest, &at); err != nil || revision != number+1 || from != previous || !managedtest.ValidStatus(managedtest.Status(to)) || !managedtest.ValidDigest(receipt) || reason == "" {
				chain.Close()
				return false
			}
			if _, err := time.Parse(time.RFC3339Nano, at); err != nil {
				chain.Close()
				return false
			}
			if to == "current" && (!acceptanceID.Valid || !lowerHex(acceptanceID.String, 32) || !recordDigest.Valid || !managedtest.ValidDigest(recordDigest.String)) {
				chain.Close()
				return false
			}
			if to != "current" && (acceptanceID.Valid || recordDigest.Valid) {
				chain.Close()
				return false
			}
			if to == "current" {
				if reason != "accepted_verified" {
					chain.Close()
					return false
				}
				lastAcceptedReceipt = receipt
				accepted = append(accepted, acceptedCommit{acceptanceID.String, recordDigest.String})
			} else if !validManagedTransitionReason(reason) {
				chain.Close()
				return false
			}
			previous = to
			number++
		}
		if err := chain.Err(); err != nil {
			chain.Close()
			return false
		}
		if err := chain.Close(); err != nil || number != head.revision || previous != string(record.Status) || lastAcceptedReceipt != record.ValidationReceiptDigest {
			return false
		}
		for _, commit := range accepted {
			var digest, caseID string
			if err := s.db.QueryRowContext(ctx, `SELECT acceptance_digest,case_id FROM managed_test_commits WHERE acceptance_id=?`, commit.id).Scan(&digest, &caseID); err != nil || digest != commit.digest || caseID != record.CaseID {
				return false
			}
		}
		if err := validateManagedRecordBinding(ctx, s.db, record); err != nil {
			return false
		}
	}
	return true
}

func validManagedTransitionReason(reason string) bool {
	switch reason {
	case "marker_corrupt_or_missing", "source_function_missing", "managed_block_edited", "source_digest_changed", "toolchain_changed":
		return true
	default:
		return false
	}
}

func (r *ManagedRegistry) usable() bool {
	return r != nil && r.store != nil && r.store.managedAvailable && !r.store.managedInvalid
}

func (r *ManagedRegistry) BeginManagedAcceptance(ctx context.Context, a managedtest.Acceptance) error {
	if !r.usable() {
		return task.ErrStorageUnavailable
	}
	if ctx == nil || !managedtest.ValidAcceptance(a) || !validManagedFileID(a.Record) {
		return task.ErrInvalidArgument
	}
	encoded, err := json.Marshal(a)
	if err != nil {
		return task.ErrInvalidArgument
	}
	tx, err := r.store.db.BeginTx(ctx, nil)
	if err != nil {
		return storageError("begin managed acceptance", err)
	}
	defer tx.Rollback()
	var previous string
	err = tx.QueryRowContext(ctx, `SELECT record_json FROM managed_test_pending_acceptances WHERE acceptance_id=?`, a.AcceptanceID).Scan(&previous)
	if err == nil {
		if previous != string(encoded) {
			return task.ErrConflict
		}
		return tx.Commit()
	}
	if !isNoRows(err) {
		return storageError("read managed acceptance", err)
	}
	var committed string
	var priorJSON, priorMAC sql.NullString
	err = tx.QueryRowContext(ctx, `SELECT acceptance_digest,acceptance_json,acceptance_mac FROM managed_test_commits WHERE acceptance_id=?`, a.AcceptanceID).Scan(&committed, &priorJSON, &priorMAC)
	if err == nil {
		key, keyErr := managedRegistryKey(ctx, tx)
		if keyErr != nil {
			return keyErr
		}
		if committed != managedAcceptanceDigest(encoded) || !priorJSON.Valid || priorJSON.String != string(encoded) || !priorMAC.Valid || !validManagedAcceptanceMAC(key, encoded, priorMAC.String) {
			return task.ErrConflict
		}
		return tx.Commit()
	}
	if !isNoRows(err) {
		return storageError("read managed commit", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO managed_test_pending_acceptances(acceptance_id,case_id,review_digest,preimage_digest,published_file_digest,record_json,phase,created_at) VALUES(?,?,?,?,?,?,'prepared',?)`,
		a.AcceptanceID, a.Record.CaseID, a.ReviewDigest, a.PreimageDigest, a.PublishedFileDigest, string(encoded), formatTime(a.At)); err != nil {
		return task.ErrConflict
	}
	if err := tx.Commit(); err != nil {
		return storageError("commit pending acceptance", err)
	}
	return nil
}

func (r *ManagedRegistry) MarkManagedFileWritten(ctx context.Context, acceptanceID, actualDigest string) error {
	if !r.usable() {
		return task.ErrStorageUnavailable
	}
	if ctx == nil || !lowerHex(acceptanceID, 32) || !managedtest.ValidDigest(actualDigest) {
		return task.ErrInvalidArgument
	}
	result, err := r.store.db.ExecContext(ctx, `UPDATE managed_test_pending_acceptances SET phase='file_written' WHERE acceptance_id=? AND published_file_digest=? AND phase IN ('prepared','file_written')`, acceptanceID, actualDigest)
	if err != nil {
		return storageError("mark managed file written", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return storageError("read managed file mark", err)
	}
	if count != 1 {
		return task.ErrConflict
	}
	return nil
}

func (r *ManagedRegistry) ListPendingManagedAcceptances(ctx context.Context) ([]managedtest.PendingAcceptance, error) {
	if !r.usable() {
		return nil, task.ErrStorageUnavailable
	}
	if ctx == nil {
		return nil, task.ErrInvalidArgument
	}
	rows, err := r.store.db.QueryContext(ctx, `SELECT acceptance_id,case_id,review_digest,preimage_digest,published_file_digest,record_json,phase FROM managed_test_pending_acceptances ORDER BY created_at,acceptance_id`)
	if err != nil {
		return nil, storageError("list pending managed acceptances", err)
	}
	defer rows.Close()
	result := []managedtest.PendingAcceptance{}
	for rows.Next() {
		var acceptanceID, caseID, reviewDigest, preimageDigest, publishedDigest, encoded, phase string
		if err := rows.Scan(&acceptanceID, &caseID, &reviewDigest, &preimageDigest, &publishedDigest, &encoded, &phase); err != nil {
			return nil, storageError("scan pending managed acceptance", err)
		}
		var a managedtest.Acceptance
		if err := decodeStrictJSON([]byte(encoded), &a); err != nil || !managedtest.ValidAcceptance(a) || phase != "prepared" && phase != "file_written" ||
			a.AcceptanceID != acceptanceID || a.Record.CaseID != caseID || a.ReviewDigest != reviewDigest || a.PreimageDigest != preimageDigest || a.PublishedFileDigest != publishedDigest {
			return nil, task.ErrStorageUnavailable
		}
		result = append(result, managedtest.PendingAcceptance{Acceptance: a, Phase: phase})
	}
	if err := rows.Err(); err != nil {
		return nil, storageError("list pending managed acceptances", err)
	}
	return result, nil
}

// ResolvePendingManagedAcceptance never infers successful validation from disk.
// The publisher must compare the actual on-disk digest with a journal preimage
// or published image before calling this method.
func (r *ManagedRegistry) ResolvePendingManagedAcceptance(ctx context.Context, acceptanceID, observedDigest string) error {
	if !r.usable() {
		return task.ErrStorageUnavailable
	}
	if ctx == nil || !lowerHex(acceptanceID, 32) || !managedtest.ValidDigest(observedDigest) {
		return task.ErrInvalidArgument
	}
	tx, err := r.store.db.BeginTx(ctx, nil)
	if err != nil {
		return storageError("begin managed recovery", err)
	}
	defer tx.Rollback()
	var preimage, published string
	err = tx.QueryRowContext(ctx, `SELECT preimage_digest,published_file_digest FROM managed_test_pending_acceptances WHERE acceptance_id=?`, acceptanceID).Scan(&preimage, &published)
	if isNoRows(err) {
		return task.ErrNotFound
	}
	if err != nil {
		return storageError("read pending managed acceptance", err)
	}
	if observedDigest == preimage {
		_, err = tx.ExecContext(ctx, `DELETE FROM managed_test_pending_acceptances WHERE acceptance_id=?`, acceptanceID)
	} else if observedDigest == published {
		_, err = tx.ExecContext(ctx, `UPDATE managed_test_pending_acceptances SET phase='file_written' WHERE acceptance_id=?`, acceptanceID)
	} else {
		return task.ErrConflict
	}
	if err != nil {
		return storageError("resolve pending managed acceptance", err)
	}
	if err := tx.Commit(); err != nil {
		return storageError("commit managed recovery", err)
	}
	return nil
}

func (r *ManagedRegistry) CommitAccepted(ctx context.Context, a managedtest.Acceptance) error {
	if !r.usable() {
		return task.ErrStorageUnavailable
	}
	if ctx == nil || !managedtest.ValidAcceptance(a) || !validManagedFileID(a.Record) {
		return task.ErrInvalidArgument
	}
	encoded, err := json.Marshal(a)
	if err != nil {
		return task.ErrInvalidArgument
	}
	digest := managedAcceptanceDigest(encoded)
	tx, err := r.store.db.BeginTx(ctx, nil)
	if err != nil {
		return storageError("begin managed commit", err)
	}
	defer tx.Rollback()
	var committed string
	var priorJSON, priorMAC sql.NullString
	err = tx.QueryRowContext(ctx, `SELECT acceptance_digest,acceptance_json,acceptance_mac FROM managed_test_commits WHERE acceptance_id=?`, a.AcceptanceID).Scan(&committed, &priorJSON, &priorMAC)
	if err == nil {
		key, keyErr := managedRegistryKey(ctx, tx)
		if keyErr != nil {
			return keyErr
		}
		if committed != digest || !priorJSON.Valid || priorJSON.String != string(encoded) || !priorMAC.Valid || !validManagedAcceptanceMAC(key, encoded, priorMAC.String) {
			return task.ErrConflict
		}
		return tx.Commit()
	}
	if !isNoRows(err) {
		return storageError("read managed commit", err)
	}
	var pending, phase string
	err = tx.QueryRowContext(ctx, `SELECT record_json,phase FROM managed_test_pending_acceptances WHERE acceptance_id=?`, a.AcceptanceID).Scan(&pending, &phase)
	if err != nil {
		if isNoRows(err) {
			return task.ErrConflict
		}
		return storageError("read pending managed commit", err)
	}
	if pending != string(encoded) || phase != "file_written" {
		return task.ErrConflict
	}
	conflict, err := conflictingManagedPaths(ctx, tx, a.Record)
	if err != nil {
		return err
	}
	if conflict {
		return task.ErrConflict
	}
	var old managedtest.Record
	var revision int
	old, revision, err = getManagedRecordTx(ctx, tx, a.Record.CaseID)
	if err != nil && !errors.Is(err, task.ErrNotFound) {
		return err
	}
	from := "none"
	if err == nil {
		if err := validateManagedRecordBinding(ctx, tx, old); err != nil {
			return err
		}
		if old.ProjectID != a.Record.ProjectID || old.SourceFileID != a.Record.SourceFileID || old.FunctionID != a.Record.FunctionID ||
			old.SourceRelativePath != a.Record.SourceRelativePath || old.ScenarioID != a.Record.ScenarioID || old.TestRelativePath != a.Record.TestRelativePath {
			return task.ErrConflict
		}
		if !a.Record.LastVerifiedAt.After(old.LastVerifiedAt) {
			return task.ErrConflict
		}
		from = string(old.Status)
	}
	revision++
	if _, err := tx.ExecContext(ctx, `INSERT INTO managed_test_records(case_id,project_id,source_file_id,function_id,source_relative_path,scenario_id,test_relative_path,accepted_block_digest,generator_version,framework,toolchain_id,source_digest,validation_receipt_digest,status,last_verified_at,revision) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(case_id) DO UPDATE SET accepted_block_digest=excluded.accepted_block_digest,generator_version=excluded.generator_version,framework=excluded.framework,toolchain_id=excluded.toolchain_id,source_digest=excluded.source_digest,validation_receipt_digest=excluded.validation_receipt_digest,status=excluded.status,last_verified_at=excluded.last_verified_at,revision=excluded.revision`,
		a.Record.CaseID, a.Record.ProjectID, a.Record.SourceFileID, a.Record.FunctionID, a.Record.SourceRelativePath, a.Record.ScenarioID, a.Record.TestRelativePath, a.Record.AcceptedBlockDigest, a.Record.GeneratorVersion, a.Record.Framework, a.Record.ToolchainID, a.Record.SourceDigest, a.Record.ValidationReceiptDigest, a.Record.Status, formatTime(a.Record.LastVerifiedAt), revision); err != nil {
		return storageError("write managed record", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO managed_test_transitions(case_id,revision,from_status,to_status,reason,receipt_digest,acceptance_id,record_digest,occurred_at) VALUES(?,?,?,'current','accepted_verified',?,?,?,?)`,
		a.Record.CaseID, revision, from, a.Record.ValidationReceiptDigest, a.AcceptanceID, digest, formatTime(a.At)); err != nil {
		return storageError("write managed transition", err)
	}
	key, err := managedRegistryKey(ctx, tx)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO managed_test_commits(acceptance_id,case_id,acceptance_digest,acceptance_json,acceptance_mac) VALUES(?,?,?,?,?)`, a.AcceptanceID, a.Record.CaseID, digest, string(encoded), managedAcceptanceMAC(key, encoded)); err != nil {
		return storageError("write managed commit", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM managed_test_pending_acceptances WHERE acceptance_id=?`, a.AcceptanceID); err != nil {
		return storageError("clear managed pending", err)
	}
	if err := tx.Commit(); err != nil {
		return storageError("commit managed acceptance", err)
	}
	return nil
}

func conflictingManagedPaths(ctx context.Context, tx *sql.Tx, incoming managedtest.Record) (bool, error) {
	rows, err := tx.QueryContext(ctx, `SELECT source_file_id,source_relative_path,test_relative_path FROM managed_test_records WHERE project_id=? AND case_id<>?`, incoming.ProjectID, incoming.CaseID)
	if err != nil {
		return false, storageError("list managed path bindings", err)
	}
	defer rows.Close()
	for rows.Next() {
		var sourceID, sourcePath, targetPath string
		if err := rows.Scan(&sourceID, &sourcePath, &targetPath); err != nil {
			return false, storageError("read managed path binding", err)
		}
		sameTarget := targetPath == incoming.TestRelativePath || runtime.GOOS == "windows" && strings.EqualFold(targetPath, incoming.TestRelativePath)
		if sourceID == incoming.SourceFileID && (sourcePath != incoming.SourceRelativePath || targetPath != incoming.TestRelativePath) ||
			sourceID != incoming.SourceFileID && sameTarget {
			return true, nil
		}
	}
	if err := rows.Err(); err != nil {
		return false, storageError("list managed path bindings", err)
	}
	return false, nil
}

const managedRecordSelect = `SELECT case_id,project_id,source_file_id,function_id,source_relative_path,scenario_id,test_relative_path,accepted_block_digest,generator_version,framework,toolchain_id,source_digest,validation_receipt_digest,status,last_verified_at,revision FROM managed_test_records`

type managedScanner interface{ Scan(...any) error }

func scanManagedRecord(row managedScanner) (managedtest.Record, int, error) {
	var v managedtest.Record
	var at, status string
	var revision int
	err := row.Scan(&v.CaseID, &v.ProjectID, &v.SourceFileID, &v.FunctionID, &v.SourceRelativePath, &v.ScenarioID, &v.TestRelativePath, &v.AcceptedBlockDigest, &v.GeneratorVersion, &v.Framework, &v.ToolchainID, &v.SourceDigest, &v.ValidationReceiptDigest, &status, &at, &revision)
	if err != nil {
		return v, 0, err
	}
	v.Status = managedtest.Status(status)
	v.LastVerifiedAt, err = time.Parse(time.RFC3339Nano, at)
	if err != nil || revision < 1 || !managedtest.ValidRecord(v) || !validManagedFileID(v) {
		return v, 0, task.ErrStorageUnavailable
	}
	return v, revision, nil
}

func getManagedRecordTx(ctx context.Context, tx *sql.Tx, caseID string) (managedtest.Record, int, error) {
	v, revision, err := scanManagedRecord(tx.QueryRowContext(ctx, managedRecordSelect+` WHERE case_id=?`, caseID))
	if isNoRows(err) {
		return managedtest.Record{}, 0, task.ErrNotFound
	}
	if err != nil {
		return managedtest.Record{}, 0, storageError("get managed record", err)
	}
	return v, revision, nil
}

func (r *ManagedRegistry) Get(ctx context.Context, caseID string) (managedtest.Record, error) {
	if !r.usable() {
		return managedtest.Record{}, task.ErrStorageUnavailable
	}
	if ctx == nil || !strings.HasPrefix(caseID, "utc_") || !lowerHex(strings.TrimPrefix(caseID, "utc_"), 32) {
		return managedtest.Record{}, task.ErrInvalidArgument
	}
	tx, err := r.store.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return managedtest.Record{}, storageError("begin managed get", err)
	}
	defer tx.Rollback()
	if err := noPendingManagedReads(ctx, tx); err != nil {
		return managedtest.Record{}, err
	}
	v, _, err := scanManagedRecord(tx.QueryRowContext(ctx, managedRecordSelect+` WHERE case_id=?`, caseID))
	if isNoRows(err) {
		return managedtest.Record{}, task.ErrNotFound
	}
	if err != nil {
		return managedtest.Record{}, storageError("get managed record", err)
	}
	if err := validateManagedRecordBinding(ctx, tx, v); err != nil {
		return managedtest.Record{}, err
	}
	if err := tx.Commit(); err != nil {
		return managedtest.Record{}, storageError("commit managed get", err)
	}
	return v, nil
}

func noPendingManagedReads(ctx context.Context, tx *sql.Tx) error {
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM managed_test_pending_acceptances`).Scan(&count); err != nil {
		return storageError("check managed pending", err)
	}
	if count != 0 {
		return task.ErrStorageUnavailable
	}
	return nil
}

func validManagedFileID(v managedtest.Record) bool {
	id, err := coveragedetail.StableFileID(v.ProjectID, v.SourceRelativePath)
	return err == nil && id == v.SourceFileID
}

func managedAcceptanceDigest(value []byte) string {
	digest := sha256.Sum256(value)
	return hex.EncodeToString(digest[:])
}

func managedRegistryKey(ctx context.Context, q managedBindingQuerier) ([]byte, error) {
	var key []byte
	if err := q.QueryRowContext(ctx, `SELECT cursor_key FROM managed_test_registry_meta WHERE singleton=1`).Scan(&key); err != nil || len(key) != 32 {
		return nil, task.ErrStorageUnavailable
	}
	return key, nil
}

func managedAcceptanceMAC(key, encoded []byte) string {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte("managed-acceptance-v1\x00"))
	mac.Write(encoded)
	return hex.EncodeToString(mac.Sum(nil))
}

func validManagedAcceptanceMAC(key, encoded []byte, value string) bool {
	got, err := hex.DecodeString(value)
	if err != nil || value != hex.EncodeToString(got) {
		return false
	}
	want, _ := hex.DecodeString(managedAcceptanceMAC(key, encoded))
	return hmac.Equal(got, want)
}

type managedBindingQuerier interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// The last verified acceptance remains immutable while reconciliation changes
// only Status/Revision. Persisting its canonical bytes prevents a valid-looking
// edited record from being presented as an accepted generated test.
func validateManagedRecordBinding(ctx context.Context, q managedBindingQuerier, record managedtest.Record) error {
	var acceptanceID, transitionDigest, receiptDigest, commitDigest, encoded, authentication string
	err := q.QueryRowContext(ctx, `SELECT t.acceptance_id,t.record_digest,t.receipt_digest,c.acceptance_digest,c.acceptance_json,c.acceptance_mac FROM managed_test_transitions t JOIN managed_test_commits c ON c.acceptance_id=t.acceptance_id WHERE t.case_id=? AND t.to_status='current' ORDER BY t.revision DESC LIMIT 1`, record.CaseID).Scan(&acceptanceID, &transitionDigest, &receiptDigest, &commitDigest, &encoded, &authentication)
	if err != nil {
		return task.ErrStorageUnavailable
	}
	key, err := managedRegistryKey(ctx, q)
	if err != nil {
		return err
	}
	if !managedtest.ValidDigest(transitionDigest) || transitionDigest != commitDigest || transitionDigest != managedAcceptanceDigest([]byte(encoded)) || receiptDigest != record.ValidationReceiptDigest || !validManagedAcceptanceMAC(key, []byte(encoded), authentication) {
		return task.ErrStorageUnavailable
	}
	var a managedtest.Acceptance
	if err := decodeStrictJSON([]byte(encoded), &a); err != nil || !managedtest.ValidAcceptance(a) || !validManagedFileID(a.Record) || a.AcceptanceID != acceptanceID || a.Record.CaseID != record.CaseID {
		return task.ErrStorageUnavailable
	}
	canonical, err := json.Marshal(a)
	if err != nil || !bytes.Equal(canonical, []byte(encoded)) {
		return task.ErrStorageUnavailable
	}
	if !record.LastVerifiedAt.Equal(a.Record.LastVerifiedAt) {
		return task.ErrStorageUnavailable
	}
	expected := a.Record
	expected.Status = record.Status
	expected.LastVerifiedAt = record.LastVerifiedAt
	if expected != record {
		return task.ErrStorageUnavailable
	}
	return nil
}

func (r *ManagedRegistry) List(ctx context.Context, q managedtest.Query) (managedtest.Page, error) {
	if !r.usable() {
		return managedtest.Page{}, task.ErrStorageUnavailable
	}
	if ctx == nil || !validProjectID(q.ProjectID) || q.Limit < 1 || q.Limit > 200 ||
		q.SourceFileID != "" && !lowerHex(q.SourceFileID, 32) || q.Status != "" && !managedtest.ValidStatus(q.Status) {
		return managedtest.Page{}, task.ErrInvalidArgument
	}
	tx, err := r.store.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return managedtest.Page{}, storageError("begin managed list", err)
	}
	defer tx.Rollback()
	if err := noPendingManagedReads(ctx, tx); err != nil {
		return managedtest.Page{}, err
	}
	var key []byte
	if err := tx.QueryRowContext(ctx, `SELECT cursor_key FROM managed_test_registry_meta WHERE singleton=1`).Scan(&key); err != nil || len(key) != 32 {
		return managedtest.Page{}, task.ErrStorageUnavailable
	}
	scope := detailScope("managed-records", q.ProjectID, "", q.SourceFileID, string(q.Status), "asc", q.Limit)
	lastFile, lastCase := "", ""
	if q.Cursor != "" {
		last, err := decodeDetailCursor(key, scope, q.Cursor)
		if err != nil {
			return managedtest.Page{}, err
		}
		var parts [2]string
		if err := json.Unmarshal([]byte(last), &parts); err != nil || !lowerHex(parts[0], 32) || !strings.HasPrefix(parts[1], "utc_") || !lowerHex(strings.TrimPrefix(parts[1], "utc_"), 32) {
			return managedtest.Page{}, task.ErrInvalidArgument
		}
		lastFile, lastCase = parts[0], parts[1]
	}
	query := managedRecordSelect + ` WHERE project_id=?`
	args := []any{q.ProjectID}
	if q.SourceFileID != "" {
		query += ` AND source_file_id=?`
		args = append(args, q.SourceFileID)
	}
	if q.Status != "" {
		query += ` AND status=?`
		args = append(args, string(q.Status))
	}
	if lastFile != "" {
		query += ` AND (source_file_id>? OR (source_file_id=? AND case_id>?))`
		args = append(args, lastFile, lastFile, lastCase)
	}
	query += ` ORDER BY source_file_id,case_id LIMIT ?`
	args = append(args, q.Limit+1)
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return managedtest.Page{}, storageError("list managed records", err)
	}
	items := []managedtest.Record{}
	for rows.Next() {
		v, _, err := scanManagedRecord(rows)
		if err != nil {
			rows.Close()
			return managedtest.Page{}, storageError("scan managed record", err)
		}
		items = append(items, v)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return managedtest.Page{}, storageError("list managed records", err)
	}
	if err := rows.Close(); err != nil {
		return managedtest.Page{}, storageError("close managed list", err)
	}
	page := managedtest.Page{Items: items}
	if len(items) > q.Limit {
		page.Items = items[:q.Limit]
		last := page.Items[len(page.Items)-1]
		encoded, _ := json.Marshal([2]string{last.SourceFileID, last.CaseID})
		page.NextCursor = encodeDetailCursor(key, scope, string(encoded))
	}
	for _, item := range page.Items {
		if err := validateManagedRecordBinding(ctx, tx, item); err != nil {
			return managedtest.Page{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return managedtest.Page{}, storageError("commit managed list", err)
	}
	return page, nil
}

type managedSource struct {
	digest    string
	functions map[string]bool
}
type managedTestDocument struct {
	document managedtest.Document
	invalid  bool
}

func (r *ManagedRegistry) ReconcileWorkspace(ctx context.Context, snapshot managedtest.WorkspaceSnapshot) error {
	if !r.usable() {
		return task.ErrStorageUnavailable
	}
	if ctx == nil || !validProjectID(snapshot.ProjectID) || snapshot.ToolchainID == "" || len(snapshot.ToolchainID) > 256 ||
		!managedtest.ValidDigest(snapshot.ReceiptDigest) || snapshot.At.IsZero() || len(snapshot.Sources) > 200000 || len(snapshot.Tests) > 200000 {
		return task.ErrInvalidArgument
	}
	sources := map[string]managedSource{}
	for _, source := range snapshot.Sources {
		id, err := coveragedetail.StableFileID(snapshot.ProjectID, source.RelativePath)
		if err != nil || id != source.FileID || !managedtest.ValidDigest(source.Digest) || sources[source.FileID].digest != "" {
			return task.ErrInvalidArgument
		}
		functions := map[string]bool{}
		for _, fn := range source.FunctionIDs {
			if !lowerHex(fn, 32) || functions[fn] {
				return task.ErrInvalidArgument
			}
			functions[fn] = true
		}
		sources[source.FileID] = managedSource{digest: source.Digest, functions: functions}
	}
	tests := map[string]managedTestDocument{}
	for _, test := range snapshot.Tests {
		_, duplicate := tests[test.RelativePath]
		if !managedtest.ValidTestPath(test.RelativePath) || duplicate {
			return task.ErrInvalidArgument
		}
		doc, err := managedtest.ParseDocument(test.Bytes, 8*1024*1024, 4096)
		tests[test.RelativePath] = managedTestDocument{document: doc, invalid: err != nil}
	}
	tx, err := r.store.db.BeginTx(ctx, nil)
	if err != nil {
		return storageError("begin managed reconciliation", err)
	}
	defer tx.Rollback()
	var pending int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM managed_test_pending_acceptances WHERE json_extract(record_json,'$.Record.ProjectID')=?`, snapshot.ProjectID).Scan(&pending); err != nil {
		return storageError("check pending managed reconciliation", err)
	}
	if pending != 0 {
		return task.ErrConflict
	}
	rows, err := tx.QueryContext(ctx, managedRecordSelect+` WHERE project_id=? ORDER BY case_id`, snapshot.ProjectID)
	if err != nil {
		return storageError("list managed reconciliation records", err)
	}
	type currentRecord struct {
		value    managedtest.Record
		revision int
	}
	records := []currentRecord{}
	for rows.Next() {
		value, revision, err := scanManagedRecord(rows)
		if err != nil {
			rows.Close()
			return storageError("scan managed reconciliation record", err)
		}
		records = append(records, currentRecord{value, revision})
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return storageError("list managed reconciliation records", err)
	}
	rows.Close()
	for _, current := range records {
		value := current.value
		status, reason := classifyManagedRecord(value, sources, tests, snapshot.ToolchainID)
		if status == managedtest.StatusCurrent || status == value.Status {
			continue
		}
		if value.Status == managedtest.StatusInvalid || value.Status == managedtest.StatusOrphaned && status != managedtest.StatusInvalid ||
			value.Status == managedtest.StatusConflicted && status == managedtest.StatusStale {
			continue
		}
		if _, err := tx.ExecContext(ctx, `UPDATE managed_test_records SET status=?,revision=? WHERE case_id=? AND revision=?`, status, current.revision+1, value.CaseID, current.revision); err != nil {
			return storageError("update managed reconciliation status", err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO managed_test_transitions(case_id,revision,from_status,to_status,reason,receipt_digest,occurred_at) VALUES(?,?,?,?,?,?,?)`,
			value.CaseID, current.revision+1, value.Status, status, reason, snapshot.ReceiptDigest, formatTime(snapshot.At)); err != nil {
			return storageError("write managed reconciliation transition", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return storageError("commit managed reconciliation", err)
	}
	return nil
}

func classifyManagedRecord(v managedtest.Record, sources map[string]managedSource, tests map[string]managedTestDocument, toolchain string) (managedtest.Status, string) {
	test, exists := tests[v.TestRelativePath]
	if !exists || test.invalid {
		return managedtest.StatusInvalid, "marker_corrupt_or_missing"
	}
	var block *managedtest.Block
	for i := range test.document.Blocks {
		if test.document.Blocks[i].CaseID == v.CaseID {
			block = &test.document.Blocks[i]
			break
		}
	}
	if block == nil || block.FunctionID != v.FunctionID {
		return managedtest.StatusInvalid, "marker_corrupt_or_missing"
	}
	source, exists := sources[v.SourceFileID]
	if !exists || !source.functions[v.FunctionID] {
		return managedtest.StatusOrphaned, "source_function_missing"
	}
	if block.Digest != v.AcceptedBlockDigest {
		return managedtest.StatusConflicted, "managed_block_edited"
	}
	if source.digest != v.SourceDigest {
		return managedtest.StatusStale, "source_digest_changed"
	}
	if toolchain != v.ToolchainID {
		return managedtest.StatusStale, "toolchain_changed"
	}
	return managedtest.StatusCurrent, ""
}
