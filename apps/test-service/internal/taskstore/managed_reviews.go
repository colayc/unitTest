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
	"sort"
	"time"

	"unit-test-ide.local/test-service/internal/managedtest"
	"unit-test-ide.local/test-service/internal/task"
)

// ManagedReviewStore is optional and never raises the service's v1.6 readiness
// by itself. The production route remains closed until source attestation and
// selected-output validation are wired independently.
type ManagedReviewStore interface {
	CommitManagedReview(context.Context, managedtest.ReviewDraft) error
	ListManagedReviews(context.Context, string, managedtest.ReviewListQuery) (managedtest.ReviewListPage, error)
	LookupManagedReviewBinding(context.Context, string, string) (managedtest.ReviewBinding, error)
	GetManagedReview(context.Context, managedtest.ReviewGetQuery) (managedtest.ReviewPage, error)
	LookupManagedReviewSelection(context.Context, managedtest.ReviewBinding, string, string, []string) ([]managedtest.ReviewCandidate, error)
	ReadManagedReviewDraft(context.Context, managedtest.ReviewBinding, string, string) (managedtest.ReviewDraft, error)
}

// LookupManagedReviewBinding derives the complete binding from authenticated
// durable state. The wire request deliberately supplies only a review ID.
func (s *Store) LookupManagedReviewBinding(ctx context.Context, owner, reviewID string) (managedtest.ReviewBinding, error) {
	if !s.ManagedReviewsReady() {
		return managedtest.ReviewBinding{}, task.ErrStorageUnavailable
	}
	if ctx == nil || !lowerHex(owner, 64) || !lowerHex(reviewID, 32) {
		return managedtest.ReviewBinding{}, task.ErrInvalidArgument
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return managedtest.ReviewBinding{}, storageError("begin managed review binding", err)
	}
	defer tx.Rollback()
	key, err := reviewKey(ctx, tx)
	if err != nil {
		return managedtest.ReviewBinding{}, err
	}
	stored, err := loadReview(ctx, tx, key, reviewID)
	if err != nil {
		return managedtest.ReviewBinding{}, err
	}
	if stored.manifest.OwnerDigest != owner {
		return managedtest.ReviewBinding{}, task.ErrNotFound
	}
	if stored.status != "current" {
		return managedtest.ReviewBinding{}, task.ErrConflict
	}
	binding := stored.manifest.Binding()
	if err := validateReviewRun(ctx, tx, binding, stored.manifest.SourceDigest); err != nil {
		return managedtest.ReviewBinding{}, err
	}
	if err := tx.Commit(); err != nil {
		return managedtest.ReviewBinding{}, storageError("commit managed review binding", err)
	}
	return binding, nil
}

var _ ManagedReviewStore = (*Store)(nil)

func (s *Store) ManagedReviewsReady() bool {
	if s == nil || !s.reviewAvailable || s.reviewInvalid || !s.CoverageDetailReady() || !s.attestationAvailable || s.attestationInvalid {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	key, err := reviewKey(ctx, s.db)
	return err == nil && len(key) == 32
}

func reviewKey(ctx context.Context, q managedBindingQuerier) ([]byte, error) {
	var key []byte
	if err := q.QueryRowContext(ctx, `SELECT mac_key FROM managed_review_meta WHERE singleton=1`).Scan(&key); err != nil || len(key) != 32 {
		return nil, task.ErrStorageUnavailable
	}
	return key, nil
}

func reviewMAC(key []byte, domain string, value []byte) string {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(domain))
	mac.Write([]byte{0})
	mac.Write(value)
	return hex.EncodeToString(mac.Sum(nil))
}

func validReviewMAC(key []byte, domain string, value []byte, got string) bool {
	decoded, err := hex.DecodeString(got)
	if err != nil || got != hex.EncodeToString(decoded) {
		return false
	}
	want, _ := hex.DecodeString(reviewMAC(key, domain, value))
	return hmac.Equal(decoded, want)
}

func (s *Store) CommitManagedReview(ctx context.Context, draft managedtest.ReviewDraft) error {
	if !s.ManagedReviewsReady() {
		return task.ErrStorageUnavailable
	}
	if ctx == nil || !managedtest.ValidReviewDraft(draft) {
		return task.ErrInvalidArgument
	}
	m := draft.Manifest
	current, generated := managedtest.ReviewPreimageSetDigests(draft.Candidates)
	if m.CurrentPreimageDigest != current || m.GeneratedPreimageDigest != generated {
		return task.ErrInvalidArgument
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return storageError("begin managed review", err)
	}
	defer tx.Rollback()
	key, err := reviewKey(ctx, tx)
	if err != nil {
		return err
	}
	if err := validateReviewRun(ctx, tx, m.Binding(), m.SourceDigest); err != nil {
		return err
	}
	manifest, err := json.Marshal(m)
	if err != nil || len(manifest) > 8192 {
		return task.ErrInvalidArgument
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO managed_review_manifests(review_id,owner_digest,run_id,run_revision,project_id,workspace_generation,report_id,toolchain_id,review_digest,candidate_set_digest,manifest_json,manifest_mac,candidate_count,artifact_ref,status,status_mac,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,'current',?,?)`,
		m.ReviewID, m.OwnerDigest, m.RunID, m.RunRevision, m.ProjectID, m.WorkspaceGeneration, m.ReportID, m.ToolchainID,
		m.Digest(), m.CandidateSetDigest, manifest, reviewMAC(key, "managed-review-manifest-v1", manifest), len(draft.Candidates), m.ArtifactRef, reviewStatusMAC(key, m.ReviewID, m.Digest(), "current"), formatTime(m.CreatedAt)); err != nil {
		return task.ErrConflict
	}
	for _, c := range draft.Candidates {
		encoded, err := json.Marshal(c)
		if err != nil {
			return task.ErrInvalidArgument
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO managed_review_candidates(review_id,candidate_id,candidate_json,candidate_mac) VALUES(?,?,?,?)`,
			m.ReviewID, c.CandidateID, encoded, reviewMAC(key, "managed-review-candidate-v1", encoded)); err != nil {
			return task.ErrConflict
		}
	}
	if err := tx.Commit(); err != nil {
		return storageError("commit managed review", err)
	}
	return nil
}

func validateReviewRun(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, b managedtest.ReviewBinding, sourceDigest string) error {
	if !b.Valid() {
		return task.ErrInvalidArgument
	}
	run, err := getGeneration(ctx, q, b.RunID, "")
	if errors.Is(err, task.ErrNotFound) {
		return task.ErrConflict
	}
	if err != nil {
		return err
	}
	if run.Request.SessionOwnerDigest != b.OwnerDigest || run.Revision != b.RunRevision ||
		run.Request.ProjectID != b.ProjectID || run.Request.WorkspaceGeneration != b.WorkspaceGeneration ||
		run.Request.CoverageReportID != "" && run.Request.CoverageReportID != b.ReportID ||
		sourceDigest != "" && run.Request.SourceDigest != sourceDigest {
		return task.ErrConflict
	}
	var project, workspace, status, toolchain string
	err = q.QueryRowContext(ctx, `SELECT d.project_id,d.workspace_generation,d.project_status,t.toolchain_id FROM coverage_detail_reports d JOIN coverage_reports c ON c.report_id=d.report_id JOIN test_runs t ON t.run_id=c.test_run_id WHERE d.report_id=?`, b.ReportID).
		Scan(&project, &workspace, &status, &toolchain)
	if isNoRows(err) || project != b.ProjectID || workspace != b.WorkspaceGeneration || status != "current" || toolchain != b.ToolchainID {
		return task.ErrConflict
	}
	if err != nil {
		return task.ErrStorageUnavailable
	}
	return nil
}

func reviewStatusMAC(key []byte, reviewID, digest, status string) string {
	return reviewMAC(key, "managed-review-status-v1", []byte(reviewID+"\x00"+digest+"\x00"+status))
}

type storedReview struct {
	manifest   managedtest.ReviewManifest
	candidates []managedtest.ReviewCandidate
	digest     string
	status     string
}

func loadReview(ctx context.Context, tx *sql.Tx, key []byte, reviewID string) (storedReview, error) {
	var result storedReview
	var owner, runID, project, workspace, report, toolchain, setDigest, mac, artifact, at, statusMAC string
	var revision int64
	var count int
	var manifest []byte
	err := tx.QueryRowContext(ctx, `SELECT owner_digest,run_id,run_revision,project_id,workspace_generation,report_id,toolchain_id,review_digest,candidate_set_digest,manifest_json,manifest_mac,candidate_count,artifact_ref,status,status_mac,created_at FROM managed_review_manifests WHERE review_id=?`, reviewID).
		Scan(&owner, &runID, &revision, &project, &workspace, &report, &toolchain, &result.digest, &setDigest, &manifest, &mac, &count, &artifact, &result.status, &statusMAC, &at)
	if isNoRows(err) {
		return storedReview{}, task.ErrNotFound
	}
	if err != nil {
		return storedReview{}, task.ErrStorageUnavailable
	}
	if len(manifest) == 0 || len(manifest) > 8192 || !validReviewMAC(key, "managed-review-manifest-v1", manifest, mac) ||
		decodeStrictJSON(manifest, &result.manifest) != nil {
		return storedReview{}, task.ErrStorageUnavailable
	}
	canonical, err := json.Marshal(result.manifest)
	if err != nil || !bytes.Equal(canonical, manifest) || !managedtest.ValidReviewManifest(result.manifest) ||
		result.manifest.ReviewID != reviewID || result.manifest.OwnerDigest != owner || result.manifest.RunID != runID ||
		result.manifest.RunRevision != revision || result.manifest.ProjectID != project || result.manifest.WorkspaceGeneration != workspace ||
		result.manifest.ReportID != report || result.manifest.ToolchainID != toolchain || result.manifest.CandidateSetDigest != setDigest ||
		result.manifest.ArtifactRef != artifact || formatTime(result.manifest.CreatedAt) != at || result.manifest.Digest() != result.digest ||
		(result.status != "current" && result.status != "stale") || !validReviewMAC(key, "managed-review-status-v1", []byte(reviewID+"\x00"+result.digest+"\x00"+result.status), statusMAC) {
		return storedReview{}, task.ErrStorageUnavailable
	}
	rows, err := tx.QueryContext(ctx, `SELECT candidate_id,candidate_json,candidate_mac FROM managed_review_candidates WHERE review_id=? ORDER BY candidate_id`, reviewID)
	if err != nil {
		return storedReview{}, task.ErrStorageUnavailable
	}
	defer rows.Close()
	size := len(manifest)
	for rows.Next() {
		var id, authentication string
		var encoded []byte
		if rows.Scan(&id, &encoded, &authentication) != nil || len(encoded) == 0 || len(encoded) > managedtest.MaxReviewBytes ||
			!validReviewMAC(key, "managed-review-candidate-v1", encoded, authentication) {
			return storedReview{}, task.ErrStorageUnavailable
		}
		var c managedtest.ReviewCandidate
		if decodeStrictJSON(encoded, &c) != nil || !managedtest.ValidReviewCandidate(c) || c.CandidateID != id {
			return storedReview{}, task.ErrStorageUnavailable
		}
		canonical, err := json.Marshal(c)
		if err != nil || !bytes.Equal(canonical, encoded) {
			return storedReview{}, task.ErrStorageUnavailable
		}
		result.candidates = append(result.candidates, c)
		size += len(encoded)
		if size > managedtest.MaxReviewBytes {
			return storedReview{}, task.ErrStorageUnavailable
		}
	}
	current, generated := managedtest.ReviewPreimageSetDigests(result.candidates)
	if rows.Err() != nil || len(result.candidates) != count || count < 1 || count > 200 ||
		managedtest.ReviewCandidateSetDigest(result.candidates) != setDigest ||
		result.manifest.CurrentPreimageDigest != current || result.manifest.GeneratedPreimageDigest != generated {
		return storedReview{}, task.ErrStorageUnavailable
	}
	return result, nil
}

func (s *Store) reviewRead(ctx context.Context, b managedtest.ReviewBinding, reviewID string) (*sql.Tx, storedReview, []byte, error) {
	if !s.ManagedReviewsReady() {
		return nil, storedReview{}, nil, task.ErrStorageUnavailable
	}
	if ctx == nil || !b.Valid() || !lowerHex(reviewID, 32) {
		return nil, storedReview{}, nil, task.ErrInvalidArgument
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, storedReview{}, nil, storageError("begin managed review read", err)
	}
	key, err := reviewKey(ctx, tx)
	if err != nil {
		tx.Rollback()
		return nil, storedReview{}, nil, err
	}
	stored, err := loadReview(ctx, tx, key, reviewID)
	if err != nil {
		tx.Rollback()
		return nil, storedReview{}, nil, err
	}
	if stored.manifest.OwnerDigest != b.OwnerDigest {
		tx.Rollback()
		return nil, storedReview{}, nil, task.ErrNotFound
	}
	if stored.manifest.Binding() != b || stored.status != "current" {
		tx.Rollback()
		return nil, storedReview{}, nil, task.ErrConflict
	}
	if err := validateReviewRun(ctx, tx, b, stored.manifest.SourceDigest); err != nil {
		tx.Rollback()
		return nil, storedReview{}, nil, err
	}
	return tx, stored, key, nil
}

func (s *Store) GetManagedReview(ctx context.Context, q managedtest.ReviewGetQuery) (managedtest.ReviewPage, error) {
	if q.Limit < 1 || q.Limit > 200 {
		return managedtest.ReviewPage{}, task.ErrInvalidArgument
	}
	tx, stored, key, err := s.reviewRead(ctx, q.Binding, q.ReviewID)
	if err != nil {
		return managedtest.ReviewPage{}, err
	}
	defer tx.Rollback()
	scope := detailScope("managed-review-cases", q.ReviewID, stored.digest, q.Binding.OwnerDigest, q.Binding.WorkspaceGeneration+q.Binding.ReportID+q.Binding.ToolchainID+q.Binding.RunID, "asc", q.Limit)
	last := ""
	if q.Cursor != "" {
		last, err = decodeDetailCursor(key, scope, q.Cursor)
		if err != nil || !validManagedCaseID(last) {
			return managedtest.ReviewPage{}, task.ErrInvalidArgument
		}
	}
	page := managedtest.ReviewPage{ReviewID: q.ReviewID, ReviewDigest: stored.digest, WorkspaceGeneration: q.Binding.WorkspaceGeneration, ReportID: q.Binding.ReportID, Cases: []managedtest.ReviewCase{}}
	for i, c := range stored.candidates {
		if c.CandidateID <= last {
			continue
		}
		if len(page.Cases) >= q.Limit {
			page.NextCursor = encodeDetailCursor(key, scope, page.Cases[len(page.Cases)-1].CandidateID)
			break
		}
		candidate := managedtest.ReviewCase{CandidateID: c.CandidateID, Status: c.Status, AcceptedDigest: c.AcceptedDigest, CurrentDigest: c.CurrentDigest, GeneratedDigest: c.GeneratedDigest}
		page.Cases = append(page.Cases, candidate)
		encoded, _ := json.Marshal(page)
		if len(encoded) > managedtest.MaxReviewBytes {
			page.Cases = page.Cases[:len(page.Cases)-1]
			if len(page.Cases) == 0 {
				return managedtest.ReviewPage{}, task.ErrStorageUnavailable
			}
			page.NextCursor = encodeDetailCursor(key, scope, page.Cases[len(page.Cases)-1].CandidateID)
			break
		}
		if i == len(stored.candidates)-1 {
			page.NextCursor = ""
		}
	}
	encoded, err := json.Marshal(page)
	if err != nil || len(encoded) > managedtest.MaxReviewBytes {
		return managedtest.ReviewPage{}, task.ErrStorageUnavailable
	}
	if err := tx.Commit(); err != nil {
		return managedtest.ReviewPage{}, storageError("commit managed review read", err)
	}
	return page, nil
}

func validManagedCaseID(value string) bool {
	return len(value) == 36 && value[:4] == "utc_" && lowerHex(value[4:], 32)
}

func (s *Store) LookupManagedReviewSelection(ctx context.Context, binding managedtest.ReviewBinding, reviewID, reviewDigest string, ids []string) ([]managedtest.ReviewCandidate, error) {
	if !managedtest.ValidDigest(reviewDigest) || len(ids) < 1 || len(ids) > 200 {
		return nil, task.ErrInvalidArgument
	}
	tx, stored, _, err := s.reviewRead(ctx, binding, reviewID)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if stored.digest != reviewDigest {
		return nil, task.ErrConflict
	}
	seen := make(map[string]bool, len(ids))
	selected := make([]managedtest.ReviewCandidate, 0, len(ids))
	for _, id := range ids {
		if !validManagedCaseID(id) || seen[id] {
			return nil, task.ErrInvalidArgument
		}
		seen[id] = true
		i := sort.Search(len(stored.candidates), func(i int) bool { return stored.candidates[i].CandidateID >= id })
		if i >= len(stored.candidates) || stored.candidates[i].CandidateID != id {
			return nil, task.ErrNotFound
		}
		selected = append(selected, stored.candidates[i])
	}
	if err := tx.Commit(); err != nil {
		return nil, storageError("commit managed selection read", err)
	}
	return selected, nil
}

// ReadManagedReviewDraft returns the complete, MAC-checked immutable review
// under its exact owner/run/revision/workspace/report binding. It is an
// internal publication input, never a protocol response containing source.
func (s *Store) ReadManagedReviewDraft(ctx context.Context, binding managedtest.ReviewBinding, reviewID, reviewDigest string) (managedtest.ReviewDraft, error) {
	if !managedtest.ValidDigest(reviewDigest) {
		return managedtest.ReviewDraft{}, task.ErrInvalidArgument
	}
	tx, stored, _, err := s.reviewRead(ctx, binding, reviewID)
	if err != nil {
		return managedtest.ReviewDraft{}, err
	}
	defer tx.Rollback()
	if stored.digest != reviewDigest {
		return managedtest.ReviewDraft{}, task.ErrConflict
	}
	result := managedtest.ReviewDraft{Manifest: stored.manifest, Candidates: stored.candidates}
	if !managedtest.ValidReviewDraft(result) {
		return managedtest.ReviewDraft{}, task.ErrStorageUnavailable
	}
	if err := tx.Commit(); err != nil {
		return managedtest.ReviewDraft{}, storageError("commit managed review draft read", err)
	}
	return result, nil
}

func (s *Store) MarkManagedReviewStale(ctx context.Context, owner, reviewID string) error {
	if !s.ManagedReviewsReady() {
		return task.ErrStorageUnavailable
	}
	if ctx == nil || !lowerHex(owner, 64) || !lowerHex(reviewID, 32) {
		return task.ErrInvalidArgument
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return storageError("begin stale managed review", err)
	}
	defer tx.Rollback()
	key, err := reviewKey(ctx, tx)
	if err != nil {
		return err
	}
	stored, err := loadReview(ctx, tx, key, reviewID)
	if err != nil {
		return err
	}
	if stored.manifest.OwnerDigest != owner {
		return task.ErrNotFound
	}
	if stored.status != "current" {
		return task.ErrConflict
	}
	result, err := tx.ExecContext(ctx, `UPDATE managed_review_manifests SET status='stale',status_mac=? WHERE review_id=? AND owner_digest=? AND status='current'`, reviewStatusMAC(key, reviewID, stored.digest, "stale"), reviewID, owner)
	if err != nil {
		return storageError("stale managed review", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return task.ErrStorageUnavailable
	}
	if count != 1 {
		return task.ErrNotFound
	}
	if err := tx.Commit(); err != nil {
		return storageError("commit stale managed review", err)
	}
	return nil
}

func (s *Store) ListManagedReviews(ctx context.Context, owner string, q managedtest.ReviewListQuery) (managedtest.ReviewListPage, error) {
	if !s.ManagedReviewsReady() {
		return managedtest.ReviewListPage{}, task.ErrStorageUnavailable
	}
	if ctx == nil || !lowerHex(owner, 64) || !validProjectID(q.ProjectID) || q.Limit < 1 || q.Limit > 200 {
		return managedtest.ReviewListPage{}, task.ErrInvalidArgument
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return managedtest.ReviewListPage{}, storageError("begin review list", err)
	}
	defer tx.Rollback()
	key, err := reviewKey(ctx, tx)
	if err != nil {
		return managedtest.ReviewListPage{}, err
	}
	scope := detailScope("managed-reviews", owner, q.ProjectID, "", "", "asc", q.Limit)
	last := ""
	if q.Cursor != "" {
		last, err = decodeDetailCursor(key, scope, q.Cursor)
		if err != nil || !lowerHex(last, 32) {
			return managedtest.ReviewListPage{}, task.ErrInvalidArgument
		}
	}
	rows, err := tx.QueryContext(ctx, `SELECT review_id FROM managed_review_manifests WHERE owner_digest=? AND project_id=? AND status='current' AND review_id>? ORDER BY review_id LIMIT ?`, owner, q.ProjectID, last, q.Limit+1)
	if err != nil {
		return managedtest.ReviewListPage{}, task.ErrStorageUnavailable
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if rows.Scan(&id) != nil {
			rows.Close()
			return managedtest.ReviewListPage{}, task.ErrStorageUnavailable
		}
		ids = append(ids, id)
	}
	if rows.Err() != nil {
		rows.Close()
		return managedtest.ReviewListPage{}, task.ErrStorageUnavailable
	}
	rows.Close()
	page := managedtest.ReviewListPage{Items: []managedtest.ReviewSummary{}}
	for i, id := range ids {
		if i == q.Limit {
			page.NextCursor = encodeDetailCursor(key, scope, page.Items[len(page.Items)-1].ReviewID)
			break
		}
		stored, err := loadReview(ctx, tx, key, id)
		if err != nil {
			return managedtest.ReviewListPage{}, err
		}
		if stored.manifest.OwnerDigest != owner || stored.manifest.ProjectID != q.ProjectID {
			return managedtest.ReviewListPage{}, task.ErrStorageUnavailable
		}
		if stored.status != "current" {
			return managedtest.ReviewListPage{}, task.ErrStorageUnavailable
		}
		m := stored.manifest
		if err := validateReviewRun(ctx, tx, m.Binding(), m.SourceDigest); err != nil {
			return managedtest.ReviewListPage{}, err
		}
		page.Items = append(page.Items, managedtest.ReviewSummary{ReviewID: id, ReviewDigest: stored.digest, RunID: m.RunID, ProjectID: m.ProjectID,
			WorkspaceGeneration: m.WorkspaceGeneration, ReportID: m.ReportID, ToolchainID: m.ToolchainID,
			RunRevision: m.RunRevision, CandidateCount: len(stored.candidates), CreatedAt: m.CreatedAt})
		encoded, _ := json.Marshal(page)
		if len(encoded) > managedtest.MaxReviewBytes {
			page.Items = page.Items[:len(page.Items)-1]
			if len(page.Items) == 0 {
				return managedtest.ReviewListPage{}, task.ErrStorageUnavailable
			}
			page.NextCursor = encodeDetailCursor(key, scope, page.Items[len(page.Items)-1].ReviewID)
			break
		}
	}
	if err := tx.Commit(); err != nil {
		return managedtest.ReviewListPage{}, storageError("commit review list", err)
	}
	return page, nil
}
