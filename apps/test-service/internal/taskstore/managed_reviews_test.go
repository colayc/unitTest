package taskstore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"unit-test-ide.local/test-service/internal/managedtest"
	"unit-test-ide.local/test-service/internal/task"
	"unit-test-ide.local/test-service/internal/testgendomain"
)

// A 020 database must gain the optional review tables without rebuilding v1 data.
func TestManagedReviewMigrationUpgrades020(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "review-upgrade.sqlite")
	migrations, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	db := openConfiguredDatabase(t, path)
	store := &Store{db: db, newID: task.NewID}
	applyMigrationsThrough(t, ctx, store, migrations[:20])
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	upgraded, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer upgraded.Close()
	for _, name := range []string{"managed_review_meta", "managed_review_manifests", "managed_review_candidates"} {
		var count int
		if err := upgraded.db.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_master WHERE type='table' AND name=?`, name).Scan(&count); err != nil || count != 1 {
			t.Fatalf("missing %s: count=%d err=%v", name, count, err)
		}
	}
}

func reviewHash(b []byte) string { sum := sha256.Sum256(b); return hex.EncodeToString(sum[:]) }

func reviewFixture(t *testing.T, s *Store) managedtest.ReviewDraft {
	t.Helper()
	index := attestedDetailFixture(t, s, 8123)
	run := generationRunFixture()
	run.Request.SessionOwnerDigest = strings.Repeat("6", 64)
	run.Request.ProjectID = index.ProjectID
	run.Request.WorkspaceGeneration = index.WorkspaceGeneration
	if _, err := s.CreateGeneration(context.Background(), run); err != nil {
		t.Fatal(err)
	}
	caseID, err := managedtest.StableCaseID(index.ProjectID, index.Files[0].RelativePath, index.Files[0].Functions[0].ID, "zero")
	if err != nil {
		t.Fatal(err)
	}
	current := []byte("old exact bytes\n")
	generated := []byte("new exact bytes\n")
	candidate := managedtest.ReviewCandidate{CandidateID: caseID, Status: managedtest.StatusCurrent,
		AcceptedDigest: strings.Repeat("7", 64), CurrentDigest: reviewHash(current), GeneratedDigest: reviewHash(generated),
		Diff: "-old\n+new\n", CurrentBytes: current, GeneratedBytes: generated}
	manifest := managedtest.ReviewManifest{ReviewID: strings.Repeat("8", 32), OwnerDigest: run.Request.SessionOwnerDigest,
		RunID: run.ID, RunRevision: run.Revision, ProjectID: run.Request.ProjectID, WorkspaceGeneration: run.Request.WorkspaceGeneration,
		ReportID: index.ReportID, ToolchainID: "workspace-toolchain", SourceDigest: run.Request.SourceDigest,
		CurrentPreimageDigest: reviewHash(current), GeneratedPreimageDigest: reviewHash(generated),
		ArtifactRef: "artifact:review-1", CreatedAt: time.Date(2026, 9, 28, 2, 0, 0, 0, time.UTC)}
	manifest.CandidateSetDigest = managedtest.ReviewCandidateSetDigest([]managedtest.ReviewCandidate{candidate})
	return managedtest.ReviewDraft{Manifest: manifest, Candidates: []managedtest.ReviewCandidate{candidate}}
}

func reviewBinding(d managedtest.ReviewDraft) managedtest.ReviewBinding {
	m := d.Manifest
	return managedtest.ReviewBinding{OwnerDigest: m.OwnerDigest, RunID: m.RunID, RunRevision: m.RunRevision,
		ProjectID: m.ProjectID, WorkspaceGeneration: m.WorkspaceGeneration, ReportID: m.ReportID, ToolchainID: m.ToolchainID}
}

func TestManagedReviewPersistsExactBytesAndIsOwnerBoundAfterRestart(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "reviews.sqlite")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	draft := reviewFixture(t, s)
	if !managedtest.ValidReviewDraft(draft) {
		t.Fatalf("invalid fixture: manifest=%+v candidate=%v set=%s", draft.Manifest, managedtest.ValidReviewCandidate(draft.Candidates[0]), managedtest.ReviewCandidateSetDigest(draft.Candidates))
	}
	if err := s.CommitManagedReview(ctx, draft); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	q := managedtest.ReviewGetQuery{Binding: reviewBinding(draft), ReviewID: draft.Manifest.ReviewID, Limit: 1}
	page, err := s.GetManagedReview(ctx, q)
	if err != nil || len(page.Cases) != 1 || page.ReviewDigest != draft.Manifest.Digest() {
		t.Fatalf("page=%+v err=%v", page, err)
	}
	selected, err := s.LookupManagedReviewSelection(ctx, q.Binding, q.ReviewID, draft.Manifest.Digest(), []string{draft.Candidates[0].CandidateID})
	if err != nil || string(selected[0].GeneratedBytes) != "new exact bytes\n" {
		t.Fatalf("selected=%+v err=%v", selected, err)
	}
	q.Binding.OwnerDigest = strings.Repeat("a", 64)
	if _, err := s.GetManagedReview(ctx, q); !errors.Is(err, task.ErrNotFound) {
		t.Fatalf("cross owner get=%v", err)
	}
	if _, err := s.LookupManagedReviewSelection(ctx, q.Binding, q.ReviewID, draft.Manifest.Digest(), []string{draft.Candidates[0].CandidateID}); !errors.Is(err, task.ErrNotFound) {
		t.Fatalf("cross owner selection=%v", err)
	}
}

func TestManagedReviewRejectsDuplicateAndStaleBindings(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	draft := reviewFixture(t, s)
	duplicate := draft
	duplicate.Candidates = append(append([]managedtest.ReviewCandidate{}, draft.Candidates...), draft.Candidates[0])
	if err := s.CommitManagedReview(ctx, duplicate); !errors.Is(err, task.ErrInvalidArgument) {
		t.Fatalf("duplicate candidate=%v", err)
	}
	stale := draft
	stale.Manifest.RunRevision++
	if err := s.CommitManagedReview(ctx, stale); !errors.Is(err, task.ErrConflict) {
		t.Fatalf("stale revision=%v", err)
	}
	stale = draft
	stale.Manifest.SourceDigest = strings.Repeat("f", 64)
	if err := s.CommitManagedReview(ctx, stale); !errors.Is(err, task.ErrConflict) {
		t.Fatalf("source mismatch=%v", err)
	}
	stale = draft
	stale.Manifest.ReportID = strings.Repeat("f", 32)
	if err := s.CommitManagedReview(ctx, stale); !errors.Is(err, task.ErrConflict) {
		t.Fatalf("unknown report=%v", err)
	}
	stale = draft
	stale.Manifest.ToolchainID = "wrong-toolchain"
	if err := s.CommitManagedReview(ctx, stale); !errors.Is(err, task.ErrConflict) {
		t.Fatalf("toolchain mismatch=%v", err)
	}
	if err := s.CommitManagedReview(ctx, draft); err != nil {
		t.Fatal(err)
	}
	if err := s.CommitManagedReview(ctx, draft); !errors.Is(err, task.ErrConflict) {
		t.Fatalf("duplicate review=%v", err)
	}
	q := managedtest.ReviewGetQuery{Binding: reviewBinding(draft), ReviewID: draft.Manifest.ReviewID, Limit: 1}
	q.Binding.ReportID = strings.Repeat("a", 32)
	if _, err := s.GetManagedReview(ctx, q); !errors.Is(err, task.ErrConflict) {
		t.Fatalf("report mismatch=%v", err)
	}
	q.Binding = reviewBinding(draft)
	q.Binding.ToolchainID = "other"
	if _, err := s.GetManagedReview(ctx, q); !errors.Is(err, task.ErrConflict) {
		t.Fatalf("toolchain mismatch=%v", err)
	}
}

func TestManagedReviewCursorIsOwnerAndQueryBound(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	draft := reviewFixture(t, s)
	second := draft.Candidates[0]
	second.CandidateID = "utc_11111111111111111111111111111111"
	draft.Candidates = append(draft.Candidates, second)
	draft.Manifest.CandidateSetDigest = managedtest.ReviewCandidateSetDigest(draft.Candidates)
	if err := s.CommitManagedReview(ctx, draft); err != nil {
		t.Fatal(err)
	}
	q := managedtest.ReviewGetQuery{Binding: reviewBinding(draft), ReviewID: draft.Manifest.ReviewID, Limit: 1}
	first, err := s.GetManagedReview(ctx, q)
	if err != nil || len(first.Cases) != 1 || first.NextCursor == "" {
		t.Fatalf("first page=%+v err=%v", first, err)
	}
	q.Cursor = first.NextCursor
	last, err := s.GetManagedReview(ctx, q)
	if err != nil || len(last.Cases) != 1 || last.NextCursor != "" || last.Cases[0].CandidateID == first.Cases[0].CandidateID {
		t.Fatalf("last page=%+v err=%v", last, err)
	}
	q.Cursor += "x"
	if _, err := s.GetManagedReview(ctx, q); !errors.Is(err, task.ErrInvalidArgument) {
		t.Fatalf("tampered cursor=%v", err)
	}
	q.Cursor = first.NextCursor
	q.Limit = 2
	if _, err := s.GetManagedReview(ctx, q); !errors.Is(err, task.ErrInvalidArgument) {
		t.Fatalf("limit-swapped cursor=%v", err)
	}
	q.Limit = 1
	q.Binding.OwnerDigest = strings.Repeat("a", 64)
	if _, err := s.GetManagedReview(ctx, q); !errors.Is(err, task.ErrNotFound) {
		t.Fatalf("owner-swapped cursor=%v", err)
	}
}

func TestManagedReviewRejectsOversizeAndTamperedArtifacts(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	draft := reviewFixture(t, s)
	large := draft
	large.Candidates = append([]managedtest.ReviewCandidate(nil), draft.Candidates...)
	large.Candidates[0].GeneratedBytes = []byte(strings.Repeat("x", 400<<10))
	large.Candidates[0].GeneratedDigest = reviewHash(large.Candidates[0].GeneratedBytes)
	large.Manifest.CandidateSetDigest = managedtest.ReviewCandidateSetDigest(large.Candidates)
	if err := s.CommitManagedReview(ctx, large); !errors.Is(err, task.ErrInvalidArgument) {
		t.Fatalf("oversize review=%v", err)
	}
	if err := s.CommitManagedReview(ctx, draft); err != nil {
		t.Fatal(err)
	}
	q := managedtest.ReviewGetQuery{Binding: reviewBinding(draft), ReviewID: draft.Manifest.ReviewID, Limit: 1}
	if _, err := s.db.ExecContext(ctx, `UPDATE managed_review_candidates SET candidate_json=? WHERE review_id=?`, []byte(`{"candidateId":"forged"}`), draft.Manifest.ReviewID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetManagedReview(ctx, q); !errors.Is(err, task.ErrStorageUnavailable) {
		t.Fatalf("tampered candidate=%v", err)
	}
}

func TestManagedReviewStaleCannotBeSelected(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	draft := reviewFixture(t, s)
	if err := s.CommitManagedReview(ctx, draft); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkManagedReviewStale(ctx, draft.Manifest.OwnerDigest, draft.Manifest.ReviewID); err != nil {
		t.Fatal(err)
	}
	q := managedtest.ReviewGetQuery{Binding: reviewBinding(draft), ReviewID: draft.Manifest.ReviewID, Limit: 1}
	if _, err := s.GetManagedReview(ctx, q); !errors.Is(err, task.ErrConflict) {
		t.Fatalf("stale get=%v", err)
	}
	if _, err := s.LookupManagedReviewSelection(ctx, q.Binding, q.ReviewID, draft.Manifest.Digest(), []string{draft.Candidates[0].CandidateID}); !errors.Is(err, task.ErrConflict) {
		t.Fatalf("stale select=%v", err)
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE managed_review_manifests SET status='current' WHERE review_id=?`, draft.Manifest.ReviewID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetManagedReview(ctx, q); !errors.Is(err, task.ErrStorageUnavailable) {
		t.Fatalf("tampered stale status=%v", err)
	}
}

func TestManagedReviewRunRevisionDriftInvalidatesSelection(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	draft := reviewFixture(t, s)
	if err := s.CommitManagedReview(ctx, draft); err != nil {
		t.Fatal(err)
	}
	current, err := s.GetGeneration(ctx, draft.Manifest.RunID)
	if err != nil {
		t.Fatal(err)
	}
	next := current
	next.State = testgendomain.StateBaseline
	next.Revision++
	if _, err := s.CheckpointGeneration(ctx, current.Revision, next, nil, nil); err != nil {
		t.Fatal(err)
	}
	q := managedtest.ReviewGetQuery{Binding: reviewBinding(draft), ReviewID: draft.Manifest.ReviewID, Limit: 1}
	if _, err := s.GetManagedReview(ctx, q); !errors.Is(err, task.ErrConflict) {
		t.Fatalf("revision drift get=%v", err)
	}
	if _, err := s.LookupManagedReviewSelection(ctx, q.Binding, q.ReviewID, draft.Manifest.Digest(), []string{draft.Candidates[0].CandidateID}); !errors.Is(err, task.ErrConflict) {
		t.Fatalf("revision drift selection=%v", err)
	}
}

func TestManagedReviewRejectsTamperedManifestMACAndSelectedDigest(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	draft := reviewFixture(t, s)
	if err := s.CommitManagedReview(ctx, draft); err != nil {
		t.Fatal(err)
	}
	b := reviewBinding(draft)
	if _, err := s.LookupManagedReviewSelection(ctx, b, draft.Manifest.ReviewID, strings.Repeat("0", 64), []string{draft.Candidates[0].CandidateID}); !errors.Is(err, task.ErrConflict) {
		t.Fatalf("wrong review digest=%v", err)
	}
	if _, err := s.LookupManagedReviewSelection(ctx, b, draft.Manifest.ReviewID, draft.Manifest.Digest(), []string{draft.Candidates[0].CandidateID, draft.Candidates[0].CandidateID}); !errors.Is(err, task.ErrInvalidArgument) {
		t.Fatalf("duplicate selection=%v", err)
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE managed_review_manifests SET manifest_mac=? WHERE review_id=?`, strings.Repeat("0", 64), draft.Manifest.ReviewID); err != nil {
		t.Fatal(err)
	}
	q := managedtest.ReviewGetQuery{Binding: b, ReviewID: draft.Manifest.ReviewID, Limit: 1}
	if _, err := s.GetManagedReview(ctx, q); !errors.Is(err, task.ErrStorageUnavailable) {
		t.Fatalf("tampered manifest MAC=%v", err)
	}
}

func TestManagedReviewListIsOwnerBoundAndCursorAuthenticated(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	draft := reviewFixture(t, s)
	other := draft
	other.Manifest.ReviewID = strings.Repeat("9", 32)
	for _, d := range []managedtest.ReviewDraft{draft, other} {
		if err := s.CommitManagedReview(ctx, d); err != nil {
			t.Fatal(err)
		}
	}
	q := managedtest.ReviewListQuery{ProjectID: draft.Manifest.ProjectID, Limit: 1}
	first, err := s.ListManagedReviews(ctx, draft.Manifest.OwnerDigest, q)
	if err != nil || len(first.Items) != 1 || first.NextCursor == "" {
		t.Fatalf("list first=%+v err=%v", first, err)
	}
	q.Cursor = first.NextCursor
	last, err := s.ListManagedReviews(ctx, draft.Manifest.OwnerDigest, q)
	if err != nil || len(last.Items) != 1 || last.NextCursor != "" || first.Items[0].ReviewID == last.Items[0].ReviewID {
		t.Fatalf("list last=%+v err=%v", last, err)
	}
	if _, err := s.ListManagedReviews(ctx, strings.Repeat("a", 64), q); !errors.Is(err, task.ErrInvalidArgument) {
		t.Fatalf("owner-swapped list cursor=%v", err)
	}
	if err := s.MarkManagedReviewStale(ctx, draft.Manifest.OwnerDigest, draft.Manifest.ReviewID); err != nil {
		t.Fatal(err)
	}
	fresh, err := s.ListManagedReviews(ctx, draft.Manifest.OwnerDigest, managedtest.ReviewListQuery{ProjectID: draft.Manifest.ProjectID, Limit: 1})
	if err != nil || len(fresh.Items) != 1 || fresh.Items[0].ReviewID != other.Manifest.ReviewID || fresh.NextCursor != "" {
		t.Fatalf("list after first stale=%+v err=%v", fresh, err)
	}
}

func TestManagedReviewMigrationChecksumFailurePreservesV15(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "review-checksum.sqlite")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	run := generationRunFixture()
	if _, err := s.CreateGeneration(ctx, run); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE schema_migrations SET sha256=? WHERE version=21`, strings.Repeat("0", 64)); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if s.ManagedReviewsReady() {
		t.Fatal("tampered optional review migration ready")
	}
	if _, err := s.GetGeneration(ctx, run.ID); err != nil {
		t.Fatalf("v1.5 generation hidden: %v", err)
	}
}
