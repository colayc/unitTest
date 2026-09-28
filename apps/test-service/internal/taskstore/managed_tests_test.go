package taskstore

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"unit-test-ide.local/test-service/internal/coveragedetail"
	"unit-test-ide.local/test-service/internal/managedtest"
	"unit-test-ide.local/test-service/internal/task"
)

// Without migration 017, an existing 016 database cannot store managed cases.
func TestMigration017UpgradesExistingStoreWithoutRebuildingV1(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "managed-upgrade.sqlite")
	migrations, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	db := openConfiguredDatabase(t, path)
	store := &Store{db: db, newID: task.NewID}
	applyMigrationsThrough(t, ctx, store, migrations[:16])
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	upgraded, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer upgraded.Close()
	for _, name := range []string{"managed_test_records", "managed_test_transitions", "managed_test_pending_acceptances"} {
		var count int
		if err := upgraded.db.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_master WHERE type='table' AND name=?`, name).Scan(&count); err != nil || count != 1 {
			t.Fatalf("migration 017 table %s = %d, %v", name, count, err)
		}
	}
}

func managedFixture(t *testing.T, letter string) (managedtest.Acceptance, []byte) {
	t.Helper()
	fileID, err := coveragedetail.StableFileID("project", "src/example.cpp")
	if err != nil {
		t.Fatal(err)
	}
	functionID := strings.Repeat("a", 32)
	caseID, err := managedtest.StableCaseID("project", "src/example.cpp", functionID, "zero")
	if err != nil {
		t.Fatal(err)
	}
	block := []byte("// unit-test-ide:managed-begin case=" + caseID + " function=" + functionID + " symbol=ExampleZero\n" +
		"TEST(Example, Zero) { CHECK_EQUAL(0, zero()); }\n" +
		"// unit-test-ide:managed-end case=" + caseID + "\n")
	doc, err := managedtest.ParseDocument(block, 1<<20, 10)
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 9, 28, 1, 2, 3, 0, time.UTC)
	return managedtest.Acceptance{
		AcceptanceID: strings.Repeat(letter, 32), ReviewDigest: strings.Repeat("b", 64),
		PreimageDigest: strings.Repeat("c", 64), PublishedFileDigest: strings.Repeat("d", 64), At: at,
		Record: managedtest.Record{CaseID: caseID, ProjectID: "project", SourceFileID: fileID,
			SourceRelativePath: "src/example.cpp", ScenarioID: "zero", FunctionID: functionID,
			TestRelativePath: "tests/generated/src/example_test.cpp", AcceptedBlockDigest: doc.Blocks[0].Digest,
			GeneratorVersion: "1", Framework: "cpputest", ToolchainID: "toolchain-1",
			SourceDigest: strings.Repeat("e", 64), ValidationReceiptDigest: strings.Repeat("f", 64),
			Status: managedtest.StatusCurrent, LastVerifiedAt: at},
	}, block
}

func TestManagedAcceptanceSurvivesRestartAtEveryBoundary(t *testing.T) {
	for _, phase := range []string{"before-file-write", "after-file-write", "after-registry-commit"} {
		t.Run(phase, func(t *testing.T) {
			ctx := context.Background()
			path := filepath.Join(t.TempDir(), "pending.sqlite")
			s, err := Open(path)
			if err != nil {
				t.Fatal(err)
			}
			registry := s.ManagedTestRegistry()
			acceptance, _ := managedFixture(t, "1")
			if err := registry.BeginManagedAcceptance(ctx, acceptance); err != nil {
				t.Fatal(err)
			}
			if phase != "before-file-write" {
				if err := registry.MarkManagedFileWritten(ctx, acceptance.AcceptanceID, acceptance.PublishedFileDigest); err != nil {
					t.Fatal(err)
				}
			}
			if phase == "after-registry-commit" {
				if err := registry.CommitAccepted(ctx, acceptance); err != nil {
					t.Fatal(err)
				}
			}
			if err := s.Close(); err != nil {
				t.Fatal(err)
			}
			s, err = Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			registry = s.ManagedTestRegistry()
			if phase != "after-registry-commit" {
				if s.ManagedTestsReady() {
					t.Fatal("pending acceptance advertised as ready")
				}
				pending, err := registry.ListPendingManagedAcceptances(ctx)
				if err != nil || len(pending) != 1 || pending[0].Acceptance.AcceptanceID != acceptance.AcceptanceID {
					t.Fatalf("pending after restart = %+v, %v", pending, err)
				}
				if _, err := registry.Get(ctx, acceptance.Record.CaseID); !errors.Is(err, task.ErrStorageUnavailable) {
					t.Fatalf("uncommitted record = %v", err)
				}
			} else {
				if !s.ManagedTestsReady() {
					t.Fatal("committed acceptance not ready")
				}
				if err := registry.CommitAccepted(ctx, acceptance); err != nil {
					t.Fatalf("idempotent replay = %v", err)
				}
				record, err := registry.Get(ctx, acceptance.Record.CaseID)
				if err != nil || record.AcceptedBlockDigest != acceptance.Record.AcceptedBlockDigest {
					t.Fatalf("committed record = %+v, %v", record, err)
				}
			}
		})
	}
}

func TestManagedReconcileDoesNotReverifyChangedRecords(t *testing.T) {
	ctx := context.Background()
	s, err := Open(filepath.Join(t.TempDir(), "managed.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	registry := s.ManagedTestRegistry()
	a, block := managedFixture(t, "1")
	if err := registry.BeginManagedAcceptance(ctx, a); err != nil {
		t.Fatal(err)
	}
	if err := registry.MarkManagedFileWritten(ctx, a.AcceptanceID, a.PublishedFileDigest); err != nil {
		t.Fatal(err)
	}
	if err := registry.CommitAccepted(ctx, a); err != nil {
		t.Fatal(err)
	}
	snapshot := managedtest.WorkspaceSnapshot{ProjectID: "project", ToolchainID: a.Record.ToolchainID,
		ReceiptDigest: strings.Repeat("2", 64), At: a.At.Add(time.Minute),
		Sources: []managedtest.SourceSnapshot{{FileID: a.Record.SourceFileID, RelativePath: a.Record.SourceRelativePath,
			Digest: strings.Repeat("3", 64), FunctionIDs: []string{a.Record.FunctionID}}},
		Tests: []managedtest.TestFileSnapshot{{RelativePath: a.Record.TestRelativePath, Bytes: block}}}
	if err := registry.ReconcileWorkspace(ctx, snapshot); err != nil {
		t.Fatal(err)
	}
	record, err := registry.Get(ctx, a.Record.CaseID)
	if err != nil || record.Status != managedtest.StatusStale {
		t.Fatalf("source change = %+v, %v", record, err)
	}
	snapshot.Sources[0].Digest = a.Record.SourceDigest
	snapshot.ReceiptDigest = strings.Repeat("4", 64)
	if err := registry.ReconcileWorkspace(ctx, snapshot); err != nil {
		t.Fatal(err)
	}
	record, err = registry.Get(ctx, a.Record.CaseID)
	if err != nil || record.Status != managedtest.StatusStale {
		t.Fatalf("unverified return to current = %+v, %v", record, err)
	}
	snapshot.Sources[0].FunctionIDs = nil
	snapshot.ReceiptDigest = strings.Repeat("5", 64)
	if err := registry.ReconcileWorkspace(ctx, snapshot); err != nil {
		t.Fatal(err)
	}
	record, err = registry.Get(ctx, a.Record.CaseID)
	if err != nil || record.Status != managedtest.StatusOrphaned {
		t.Fatalf("function disappearance = %+v, %v", record, err)
	}
}

func commitManagedFixture(t *testing.T, registry *ManagedRegistry, a managedtest.Acceptance) {
	t.Helper()
	ctx := context.Background()
	if err := registry.BeginManagedAcceptance(ctx, a); err != nil {
		t.Fatal(err)
	}
	if err := registry.MarkManagedFileWritten(ctx, a.AcceptanceID, a.PublishedFileDigest); err != nil {
		t.Fatal(err)
	}
	if err := registry.CommitAccepted(ctx, a); err != nil {
		t.Fatal(err)
	}
}

func TestManagedRecordPagingAndCursorBinding(t *testing.T) {
	ctx := context.Background()
	s, err := Open(filepath.Join(t.TempDir(), "managed-pages.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	r := s.ManagedTestRegistry()
	for i, scenario := range []string{"alpha", "beta", "gamma"} {
		a, _ := managedFixture(t, string(rune('1'+i)))
		a.Record.ScenarioID = scenario
		a.Record.CaseID, err = managedtest.StableCaseID(a.Record.ProjectID, a.Record.SourceRelativePath, a.Record.FunctionID, scenario)
		if err != nil {
			t.Fatal(err)
		}
		commitManagedFixture(t, r, a)
	}
	page, err := r.List(ctx, managedtest.Query{ProjectID: "project", Limit: 2})
	if err != nil || len(page.Items) != 2 || page.NextCursor == "" {
		t.Fatalf("first page = %+v, %v", page, err)
	}
	second, err := r.List(ctx, managedtest.Query{ProjectID: "project", Limit: 2, Cursor: page.NextCursor})
	if err != nil || len(second.Items) != 1 || second.NextCursor != "" || second.Items[0].CaseID == page.Items[0].CaseID || second.Items[0].CaseID == page.Items[1].CaseID {
		t.Fatalf("second page = %+v, %v", second, err)
	}
	if _, err := r.List(ctx, managedtest.Query{ProjectID: "other", Limit: 2, Cursor: page.NextCursor}); !errors.Is(err, task.ErrInvalidArgument) {
		t.Fatalf("cross-project cursor = %v", err)
	}
	if _, err := r.List(ctx, managedtest.Query{ProjectID: "project", Limit: 1, Cursor: page.NextCursor}); !errors.Is(err, task.ErrInvalidArgument) {
		t.Fatalf("cross-limit cursor = %v", err)
	}
	if _, err := r.List(ctx, managedtest.Query{ProjectID: "project", Limit: 2, Cursor: page.NextCursor + "x"}); !errors.Is(err, task.ErrInvalidArgument) {
		t.Fatalf("tampered cursor = %v", err)
	}
}

func TestManagedRegistryRejectsDuplicateIdentityAndPathBinding(t *testing.T) {
	ctx := context.Background()
	s, err := Open(filepath.Join(t.TempDir(), "managed-duplicates.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	r := s.ManagedTestRegistry()
	a, _ := managedFixture(t, "1")
	commitManagedFixture(t, r, a)
	changed := a
	changed.AcceptanceID = strings.Repeat("2", 32)
	changed.Record.TestRelativePath = "tests/generated/other_test.cpp"
	changed.Record.LastVerifiedAt = changed.Record.LastVerifiedAt.Add(time.Minute)
	changed.At = changed.Record.LastVerifiedAt
	if err := r.BeginManagedAcceptance(ctx, changed); err != nil {
		t.Fatal(err)
	}
	if err := r.MarkManagedFileWritten(ctx, changed.AcceptanceID, changed.PublishedFileDigest); err != nil {
		t.Fatal(err)
	}
	if err := r.CommitAccepted(ctx, changed); !errors.Is(err, task.ErrConflict) {
		t.Fatalf("case ID path move = %v", err)
	}
	if err := r.ResolvePendingManagedAcceptance(ctx, changed.AcceptanceID, changed.PreimageDigest); err != nil {
		t.Fatal(err)
	}
	other := a
	other.AcceptanceID = strings.Repeat("3", 32)
	other.Record.SourceRelativePath = "src/other.cpp"
	other.Record.SourceFileID, err = coveragedetail.StableFileID("project", "src/other.cpp")
	if err != nil {
		t.Fatal(err)
	}
	other.Record.CaseID, err = managedtest.StableCaseID("project", "src/other.cpp", other.Record.FunctionID, other.Record.ScenarioID)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.BeginManagedAcceptance(ctx, other); err != nil {
		t.Fatal(err)
	}
	if err := r.MarkManagedFileWritten(ctx, other.AcceptanceID, other.PublishedFileDigest); err != nil {
		t.Fatal(err)
	}
	if err := r.CommitAccepted(ctx, other); !errors.Is(err, task.ErrConflict) {
		t.Fatalf("two source files share test path = %v", err)
	}
	if err := r.ResolvePendingManagedAcceptance(ctx, other.AcceptanceID, other.PreimageDigest); err != nil {
		t.Fatal(err)
	}
	alias := a
	alias.AcceptanceID = strings.Repeat("4", 32)
	alias.Record.SourceRelativePath = "src/Example.cpp"
	alias.Record.SourceFileID, err = coveragedetail.StableFileID("project", alias.Record.SourceRelativePath)
	if err != nil {
		t.Fatal(err)
	}
	alias.Record.CaseID, err = managedtest.StableCaseID("project", alias.Record.SourceRelativePath, alias.Record.FunctionID, alias.Record.ScenarioID)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.BeginManagedAcceptance(ctx, alias); err != nil {
		t.Fatal(err)
	}
	if err := r.MarkManagedFileWritten(ctx, alias.AcceptanceID, alias.PublishedFileDigest); err != nil {
		t.Fatal(err)
	}
	if err := r.CommitAccepted(ctx, alias); !errors.Is(err, task.ErrConflict) {
		t.Fatalf("source path alias = %v", err)
	}
}

func TestManagedReconcileToolchainAndCorruptMarkerFailClosed(t *testing.T) {
	ctx := context.Background()
	s, err := Open(filepath.Join(t.TempDir(), "managed-reconcile.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	r := s.ManagedTestRegistry()
	a, block := managedFixture(t, "1")
	commitManagedFixture(t, r, a)
	snapshot := managedtest.WorkspaceSnapshot{ProjectID: "project", ToolchainID: "toolchain-2", ReceiptDigest: strings.Repeat("2", 64), At: a.At.Add(time.Minute),
		Sources: []managedtest.SourceSnapshot{{FileID: a.Record.SourceFileID, RelativePath: a.Record.SourceRelativePath, Digest: a.Record.SourceDigest, FunctionIDs: []string{a.Record.FunctionID}}},
		Tests:   []managedtest.TestFileSnapshot{{RelativePath: a.Record.TestRelativePath, Bytes: block}}}
	if err := r.ReconcileWorkspace(ctx, snapshot); err != nil {
		t.Fatal(err)
	}
	v, err := r.Get(ctx, a.Record.CaseID)
	if err != nil || v.Status != managedtest.StatusStale {
		t.Fatalf("toolchain change = %+v, %v", v, err)
	}
	snapshot.Tests[0].Bytes = []byte("// unit-test-ide:managed-begin bad\n")
	snapshot.ReceiptDigest = strings.Repeat("3", 64)
	if err := r.ReconcileWorkspace(ctx, snapshot); err != nil {
		t.Fatal(err)
	}
	v, err = r.Get(ctx, a.Record.CaseID)
	if err != nil || v.Status != managedtest.StatusInvalid {
		t.Fatalf("marker corruption = %+v, %v", v, err)
	}
	var reason string
	if err := s.db.QueryRow(`SELECT reason FROM managed_test_transitions WHERE case_id=? ORDER BY revision DESC LIMIT 1`, a.Record.CaseID).Scan(&reason); err != nil || reason != "marker_corrupt_or_missing" {
		t.Fatalf("durable reason = %q, %v", reason, err)
	}
}

func TestManagedReconcileRejectsDuplicateAndEscapingTestPaths(t *testing.T) {
	ctx := context.Background()
	s, err := Open(filepath.Join(t.TempDir(), "snapshot.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	base := managedtest.WorkspaceSnapshot{ProjectID: "project", ToolchainID: "toolchain-1", ReceiptDigest: strings.Repeat("a", 64), At: time.Now()}
	base.Tests = []managedtest.TestFileSnapshot{{RelativePath: "tests/generated/empty_test.cpp", Bytes: []byte{}}}
	base.Tests = append(base.Tests, base.Tests[0])
	if err := s.ManagedTestRegistry().ReconcileWorkspace(ctx, base); !errors.Is(err, task.ErrInvalidArgument) {
		t.Fatalf("duplicate empty test path = %v", err)
	}
	base.Tests = []managedtest.TestFileSnapshot{{RelativePath: "tests/generated/../escape_test.cpp", Bytes: []byte{}}}
	if err := s.ManagedTestRegistry().ReconcileWorkspace(ctx, base); !errors.Is(err, task.ErrInvalidArgument) {
		t.Fatalf("escaping test path = %v", err)
	}
}

func TestManagedRegistryIntegrityRejectsMissingTransitionHistory(t *testing.T) {
	ctx := context.Background()
	s, err := Open(filepath.Join(t.TempDir(), "integrity.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	a, block := managedFixture(t, "1")
	r := s.ManagedTestRegistry()
	commitManagedFixture(t, r, a)
	snapshot := managedtest.WorkspaceSnapshot{ProjectID: "project", ToolchainID: a.Record.ToolchainID, ReceiptDigest: strings.Repeat("2", 64), At: a.At.Add(time.Minute),
		Sources: []managedtest.SourceSnapshot{{FileID: a.Record.SourceFileID, RelativePath: a.Record.SourceRelativePath, Digest: strings.Repeat("3", 64), FunctionIDs: []string{a.Record.FunctionID}}},
		Tests:   []managedtest.TestFileSnapshot{{RelativePath: a.Record.TestRelativePath, Bytes: block}}}
	if err := r.ReconcileWorkspace(ctx, snapshot); err != nil {
		t.Fatal(err)
	}
	if !s.ManagedTestsReady() {
		t.Fatal("valid transition chain not ready")
	}
	if _, err := s.db.Exec(`DELETE FROM managed_test_transitions WHERE case_id=? AND revision=1`, a.Record.CaseID); err != nil {
		t.Fatal(err)
	}
	if s.ManagedTestsReady() {
		t.Fatal("missing first transition advertised")
	}
}

func TestManagedRegistryHidesCurrentRecordsDuringPendingPublish(t *testing.T) {
	ctx := context.Background()
	s, err := Open(filepath.Join(t.TempDir(), "pending-read.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	r := s.ManagedTestRegistry()
	a, _ := managedFixture(t, "1")
	commitManagedFixture(t, r, a)
	updated := a
	updated.AcceptanceID = strings.Repeat("2", 32)
	updated.Record.AcceptedBlockDigest = strings.Repeat("6", 64)
	updated.Record.LastVerifiedAt = a.At.Add(time.Minute)
	updated.At = updated.Record.LastVerifiedAt
	if err := r.BeginManagedAcceptance(ctx, updated); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Get(ctx, a.Record.CaseID); !errors.Is(err, task.ErrStorageUnavailable) {
		t.Fatalf("current read while file may have changed = %v", err)
	}
	if _, err := r.List(ctx, managedtest.Query{ProjectID: "project", Limit: 10}); !errors.Is(err, task.ErrStorageUnavailable) {
		t.Fatalf("current list while file may have changed = %v", err)
	}
	if err := r.ResolvePendingManagedAcceptance(ctx, updated.AcceptanceID, updated.PreimageDigest); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Get(ctx, a.Record.CaseID); err != nil {
		t.Fatalf("restored read = %v", err)
	}
}

func TestManagedRegistryIntegrityBindsValidationReceiptAndCommit(t *testing.T) {
	for _, mutation := range []string{"receipt", "commit"} {
		t.Run(mutation, func(t *testing.T) {
			s, err := Open(filepath.Join(t.TempDir(), "binding.sqlite"))
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			a, _ := managedFixture(t, "1")
			commitManagedFixture(t, s.ManagedTestRegistry(), a)
			if !s.ManagedTestsReady() {
				t.Fatal("valid registry not ready")
			}
			if mutation == "receipt" {
				_, err = s.db.Exec(`UPDATE managed_test_transitions SET receipt_digest=? WHERE case_id=? AND revision=1`, strings.Repeat("9", 64), a.Record.CaseID)
			} else {
				_, err = s.db.Exec(`UPDATE managed_test_commits SET acceptance_digest=? WHERE acceptance_id=?`, strings.Repeat("9", 64), a.AcceptanceID)
			}
			if err != nil {
				t.Fatal(err)
			}
			if s.ManagedTestsReady() {
				t.Fatal("forged evidence advertised")
			}
		})
	}
}

func TestManagedStaleReturnsCurrentOnlyAfterFreshAcceptance(t *testing.T) {
	ctx := context.Background()
	s, err := Open(filepath.Join(t.TempDir(), "reverify.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	r := s.ManagedTestRegistry()
	a, block := managedFixture(t, "1")
	commitManagedFixture(t, r, a)
	changed := strings.Repeat("8", 64)
	snapshot := managedtest.WorkspaceSnapshot{ProjectID: "project", ToolchainID: a.Record.ToolchainID, ReceiptDigest: strings.Repeat("2", 64), At: a.At.Add(time.Minute),
		Sources: []managedtest.SourceSnapshot{{FileID: a.Record.SourceFileID, RelativePath: a.Record.SourceRelativePath, Digest: changed, FunctionIDs: []string{a.Record.FunctionID}}},
		Tests:   []managedtest.TestFileSnapshot{{RelativePath: a.Record.TestRelativePath, Bytes: block}}}
	if err := r.ReconcileWorkspace(ctx, snapshot); err != nil {
		t.Fatal(err)
	}
	updated := a
	updated.AcceptanceID = strings.Repeat("3", 32)
	updated.Record.SourceDigest = changed
	updated.Record.ValidationReceiptDigest = strings.Repeat("4", 64)
	updated.Record.LastVerifiedAt = a.At.Add(2 * time.Minute)
	updated.At = updated.Record.LastVerifiedAt
	commitManagedFixture(t, r, updated)
	v, err := r.Get(ctx, a.Record.CaseID)
	if err != nil || v.Status != managedtest.StatusCurrent || v.ValidationReceiptDigest != updated.Record.ValidationReceiptDigest {
		t.Fatalf("reverified record = %+v, %v", v, err)
	}
	var from, reason string
	if err := s.db.QueryRow(`SELECT from_status,reason FROM managed_test_transitions WHERE case_id=? ORDER BY revision DESC LIMIT 1`, a.Record.CaseID).Scan(&from, &reason); err != nil || from != "stale" || reason != "accepted_verified" {
		t.Fatalf("reverify transition = %q %q %v", from, reason, err)
	}
}
