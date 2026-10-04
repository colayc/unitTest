package taskstore

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"unit-test-ide.local/test-service/internal/coveragedetail"
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
	candidate := managedtest.ReviewCandidate{CandidateID: caseID, TestRelativePath: "tests/generated/src/a_test.cpp", Status: managedtest.StatusCurrent,
		AcceptedDigest: strings.Repeat("7", 64), CurrentDigest: reviewHash(current), GeneratedDigest: reviewHash(generated),
		CurrentBytes: current, GeneratedBytes: generated}
	manifest := managedtest.ReviewManifest{ReviewID: strings.Repeat("8", 32), OwnerDigest: run.Request.SessionOwnerDigest,
		RunID: run.ID, RunRevision: run.Revision, ProjectID: run.Request.ProjectID, WorkspaceGeneration: run.Request.WorkspaceGeneration,
		ReportID: index.ReportID, ToolchainID: "workspace-toolchain", SourceDigest: run.Request.SourceDigest,
		ArtifactRef: "artifact:review-1", CreatedAt: time.Date(2026, 9, 28, 2, 0, 0, 0, time.UTC)}
	manifest.CandidateSetDigest = managedtest.ReviewCandidateSetDigest([]managedtest.ReviewCandidate{candidate})
	manifest.CurrentPreimageDigest, manifest.GeneratedPreimageDigest = managedtest.ReviewPreimageSetDigests([]managedtest.ReviewCandidate{candidate})
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
	serialized, err := json.Marshal(page.Cases[0])
	if err != nil || strings.Contains(strings.ToLower(string(serialized)), "diff") {
		t.Fatalf("unverified diff was displayed: %s, %v", serialized, err)
	}
	selected, err := s.LookupManagedReviewSelection(ctx, q.Binding, q.ReviewID, draft.Manifest.Digest(), []string{draft.Candidates[0].CandidateID})
	if err != nil || string(selected[0].GeneratedBytes) != "new exact bytes\n" {
		t.Fatalf("selected=%+v err=%v", selected, err)
	}
	whole, err := s.ReadManagedReviewDraft(ctx, q.Binding, q.ReviewID, draft.Manifest.Digest())
	if err != nil || whole.Manifest != draft.Manifest || len(whole.Candidates) != 1 || string(whole.Candidates[0].CurrentBytes) != "old exact bytes\n" {
		t.Fatalf("whole review=%+v err=%v", whole, err)
	}
	if _, err := s.ReadManagedReviewDraft(ctx, q.Binding, q.ReviewID, strings.Repeat("f", 64)); !errors.Is(err, task.ErrConflict) {
		t.Fatalf("changed review digest=%v", err)
	}
	q.Binding.OwnerDigest = strings.Repeat("a", 64)
	if _, err := s.GetManagedReview(ctx, q); !errors.Is(err, task.ErrNotFound) {
		t.Fatalf("cross owner get=%v", err)
	}
	if _, err := s.LookupManagedReviewSelection(ctx, q.Binding, q.ReviewID, draft.Manifest.Digest(), []string{draft.Candidates[0].CandidateID}); !errors.Is(err, task.ErrNotFound) {
		t.Fatalf("cross owner selection=%v", err)
	}
	if _, err := s.ReadManagedReviewDraft(ctx, q.Binding, q.ReviewID, draft.Manifest.Digest()); !errors.Is(err, task.ErrNotFound) {
		t.Fatalf("cross owner whole review=%v", err)
	}
}

func TestManagedRegistryRecoversExactAcceptedBlockFromDurableReview(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	draft := reviewFixture(t, s)
	fileID, err := coveragedetail.StableFileID(draft.Manifest.ProjectID, "src/a.cpp")
	if err != nil {
		t.Fatal(err)
	}
	functionID, err := coveragedetail.StableFunctionID(fileID, "linkage:7:_Z3foov:signature:")
	if err != nil {
		t.Fatal(err)
	}
	block, err := managedtest.RenderMarkers(draft.Candidates[0].CandidateID, functionID, "foo", []byte("TEST(Generated, Zero) {}\n"), "\n")
	if err != nil {
		t.Fatal(err)
	}
	document, err := managedtest.ParseDocument(block, int64(len(block)), 1)
	if err != nil {
		t.Fatal(err)
	}
	draft.Candidates[0].AcceptedDigest = ""
	draft.Candidates[0].CurrentBytes = []byte{}
	draft.Candidates[0].GeneratedBytes = block
	draft.Candidates[0].CurrentDigest = reviewHash(draft.Candidates[0].CurrentBytes)
	draft.Candidates[0].GeneratedDigest = reviewHash(block)
	draft.Manifest.CandidateSetDigest = managedtest.ReviewCandidateSetDigest(draft.Candidates)
	draft.Manifest.CurrentPreimageDigest, draft.Manifest.GeneratedPreimageDigest = managedtest.ReviewPreimageSetDigests(draft.Candidates)
	if err := s.CommitManagedReview(ctx, draft); err != nil {
		t.Fatal(err)
	}
	acceptedAt := draft.Manifest.CreatedAt.Add(time.Minute)
	acceptance := managedtest.Acceptance{
		AcceptanceID: strings.Repeat("1", 32), ReviewDigest: draft.Manifest.Digest(),
		PreimageDigest: reviewHash(nil), PublishedFileDigest: reviewHash(block), At: acceptedAt,
		Record: managedtest.Record{
			CaseID: draft.Candidates[0].CandidateID, ProjectID: draft.Manifest.ProjectID, SourceFileID: fileID, FunctionID: functionID,
			SourceRelativePath: "src/a.cpp", ScenarioID: "zero", TestRelativePath: draft.Candidates[0].TestRelativePath,
			AcceptedBlockDigest: document.Blocks[0].Digest, GeneratorVersion: "unit-test-service-v1", Framework: "cpputest",
			ToolchainID: draft.Manifest.ToolchainID, SourceDigest: draft.Manifest.SourceDigest,
			ValidationReceiptDigest: strings.Repeat("f", 64), Status: managedtest.StatusCurrent, LastVerifiedAt: acceptedAt,
		},
	}
	commitManagedFixture(t, s.ManagedTestRegistry(), acceptance)
	got, err := s.ManagedTestRegistry().ReadAcceptedBlock(ctx, acceptance.Record.CaseID)
	if err != nil || !bytes.Equal(got, block) {
		t.Fatalf("accepted block=%q err=%v", got, err)
	}
	if _, err := s.db.Exec(`UPDATE managed_review_candidates SET candidate_json=? WHERE review_id=? AND candidate_id=?`, []byte(`{}`), draft.Manifest.ReviewID, acceptance.Record.CaseID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ManagedTestRegistry().ReadAcceptedBlock(ctx, acceptance.Record.CaseID); !errors.Is(err, task.ErrStorageUnavailable) {
		t.Fatalf("tampered review block accepted: %v", err)
	}
}

func TestManagedReviewBindingLookupRejectsOtherOwnerAndStaleReview(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	draft := reviewFixture(t, s)
	if err := s.CommitManagedReview(ctx, draft); err != nil {
		t.Fatal(err)
	}
	got, err := s.LookupManagedReviewBinding(ctx, draft.Manifest.OwnerDigest, draft.Manifest.ReviewID)
	if err != nil || got != draft.Manifest.Binding() {
		t.Fatalf("binding=%+v err=%v", got, err)
	}
	if _, err := s.LookupManagedReviewBinding(ctx, strings.Repeat("a", 64), draft.Manifest.ReviewID); !errors.Is(err, task.ErrNotFound) {
		t.Fatalf("cross-owner lookup=%v", err)
	}
	if err := s.MarkManagedReviewStale(ctx, draft.Manifest.OwnerDigest, draft.Manifest.ReviewID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.LookupManagedReviewBinding(ctx, draft.Manifest.OwnerDigest, draft.Manifest.ReviewID); !errors.Is(err, task.ErrConflict) {
		t.Fatalf("stale lookup=%v", err)
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

func TestManagedReviewRejectsPlausibleButUnboundPreimageDigests(t *testing.T) {
	for _, field := range []string{"current", "generated"} {
		t.Run(field, func(t *testing.T) {
			s := openTestStore(t)
			draft := reviewFixture(t, s)
			if field == "current" {
				draft.Manifest.CurrentPreimageDigest = strings.Repeat("f", 64)
			} else {
				draft.Manifest.GeneratedPreimageDigest = strings.Repeat("f", 64)
			}
			if err := s.CommitManagedReview(context.Background(), draft); !errors.Is(err, task.ErrInvalidArgument) {
				t.Fatalf("unbound %s preimage digest=%v", field, err)
			}
		})
	}
}

func TestManagedReviewMultiCandidateBytesRemainBoundAfterStorageTamper(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	draft := reviewFixture(t, s)
	second := draft.Candidates[0]
	second.CandidateID = "utc_22222222222222222222222222222222"
	second.TestRelativePath = "tests/generated/src/b_test.cpp"
	second.CurrentBytes = []byte("old b\n")
	second.GeneratedBytes = []byte("new b\n")
	second.CurrentDigest, second.GeneratedDigest = reviewHash(second.CurrentBytes), reviewHash(second.GeneratedBytes)
	draft.Candidates = append(draft.Candidates, second)
	draft.Manifest.CandidateSetDigest = managedtest.ReviewCandidateSetDigest(draft.Candidates)
	draft.Manifest.CurrentPreimageDigest, draft.Manifest.GeneratedPreimageDigest = managedtest.ReviewPreimageSetDigests(draft.Candidates)
	if err := s.CommitManagedReview(ctx, draft); err != nil {
		t.Fatal(err)
	}
	reversed := draft
	reversed.Candidates = []managedtest.ReviewCandidate{second, draft.Candidates[0]}
	if !managedtest.ValidReviewDraft(reversed) {
		t.Fatal("canonical multi-candidate order was not stable")
	}
	q := managedtest.ReviewGetQuery{Binding: reviewBinding(draft), ReviewID: draft.Manifest.ReviewID, Limit: 2}
	if page, err := s.GetManagedReview(ctx, q); err != nil || len(page.Cases) != 2 {
		t.Fatalf("genuine page=%+v err=%v", page, err)
	}
	key, err := reviewKey(ctx, s.db)
	if err != nil {
		t.Fatal(err)
	}
	changed := draft.Candidates[0]
	changed.CurrentBytes = []byte("newly edited old bytes\n")
	changed.CurrentDigest = reviewHash(changed.CurrentBytes)
	encodedCandidate, err := json.Marshal(changed)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE managed_review_candidates SET candidate_json=?,candidate_mac=? WHERE review_id=? AND candidate_id=?`, encodedCandidate, reviewMAC(key, "managed-review-candidate-v1", encodedCandidate), draft.Manifest.ReviewID, changed.CandidateID); err != nil {
		t.Fatal(err)
	}
	forged := draft.Manifest
	forged.CandidateSetDigest = managedtest.ReviewCandidateSetDigest([]managedtest.ReviewCandidate{changed, second})
	encodedManifest, err := json.Marshal(forged)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE managed_review_manifests SET candidate_set_digest=?,review_digest=?,manifest_json=?,manifest_mac=?,status_mac=? WHERE review_id=?`, forged.CandidateSetDigest, forged.Digest(), encodedManifest, reviewMAC(key, "managed-review-manifest-v1", encodedManifest), reviewStatusMAC(key, forged.ReviewID, forged.Digest(), "current"), forged.ReviewID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetManagedReview(ctx, q); !errors.Is(err, task.ErrStorageUnavailable) {
		t.Fatalf("forged internally consistent MACs but unbound preimage=%v", err)
	}
	if _, err := s.LookupManagedReviewSelection(ctx, q.Binding, q.ReviewID, forged.Digest(), []string{changed.CandidateID}); !errors.Is(err, task.ErrStorageUnavailable) {
		t.Fatalf("forged exact-byte lookup=%v", err)
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
	draft.Manifest.CurrentPreimageDigest, draft.Manifest.GeneratedPreimageDigest = managedtest.ReviewPreimageSetDigests(draft.Candidates)
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
	large.Manifest.CurrentPreimageDigest, large.Manifest.GeneratedPreimageDigest = managedtest.ReviewPreimageSetDigests(large.Candidates)
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
