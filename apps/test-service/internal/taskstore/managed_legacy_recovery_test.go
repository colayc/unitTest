package taskstore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"unit-test-ide.local/test-service/internal/managedtest"
	"unit-test-ide.local/test-service/internal/task"
)

func legacyHash(value []byte) string {
	sum := sha256.Sum256(value)
	return hex.EncodeToString(sum[:])
}

func seedAccepted017(t *testing.T, path string, tamperBlock bool) (managedtest.Acceptance, []byte, []byte, []byte) {
	t.Helper()
	ctx := context.Background()
	migrations, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	db := openConfiguredDatabase(t, path)
	s := &Store{db: db, newID: task.NewID}
	applyMigrationsThrough(t, ctx, s, migrations[:17])
	a, document := managedFixture(t, "1")
	source, receipt := []byte("int zero() { return 0; }\n"), []byte("validated-cpputest-run-1")
	a.PreimageDigest, a.PublishedFileDigest = legacyHash(document), legacyHash(document)
	a.Record.SourceDigest, a.Record.ValidationReceiptDigest = legacyHash(source), legacyHash(receipt)
	encoded, err := json.Marshal(a)
	if err != nil {
		t.Fatal(err)
	}
	digest := managedAcceptanceDigest(encoded)
	blockDigest := a.Record.AcceptedBlockDigest
	if tamperBlock {
		blockDigest = strings.Repeat("0", 64)
	}
	_, err = db.ExecContext(ctx, `INSERT INTO managed_test_records(case_id,project_id,source_file_id,function_id,source_relative_path,scenario_id,test_relative_path,accepted_block_digest,generator_version,framework,toolchain_id,source_digest,validation_receipt_digest,status,last_verified_at,revision) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,1)`, a.Record.CaseID, a.Record.ProjectID, a.Record.SourceFileID, a.Record.FunctionID, a.Record.SourceRelativePath, a.Record.ScenarioID, a.Record.TestRelativePath, blockDigest, a.Record.GeneratorVersion, a.Record.Framework, a.Record.ToolchainID, a.Record.SourceDigest, a.Record.ValidationReceiptDigest, a.Record.Status, formatTime(a.Record.LastVerifiedAt))
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.ExecContext(ctx, `INSERT INTO managed_test_transitions(case_id,revision,from_status,to_status,reason,receipt_digest,acceptance_id,record_digest,occurred_at) VALUES(?,1,'none','current','accepted_verified',?,?,?,?)`, a.Record.CaseID, a.Record.ValidationReceiptDigest, a.AcceptanceID, digest, formatTime(a.At))
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.ExecContext(ctx, `INSERT INTO managed_test_commits(acceptance_id,case_id,acceptance_digest) VALUES(?,?,?)`, a.AcceptanceID, a.Record.CaseID, digest)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	return a, document, source, receipt
}

func TestLegacy017AcceptedRecordRequiresExplicitVerifiedRecovery(t *testing.T) {
	for _, tc := range []struct {
		name         string
		tamper       bool
		wantRecovery bool
	}{
		{"intact", false, true}, {"tampered-block", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			path := filepath.Join(t.TempDir(), "legacy.sqlite")
			old, document, source, receipt := seedAccepted017(t, path, tc.tamper)
			s, err := Open(path)
			if err != nil {
				t.Fatal(err)
			}
			if !s.CoverageDetailReady() {
				t.Fatal("v1.5 detail unavailable")
			}
			if s.ManagedTestsReady() {
				t.Fatal("legacy acceptance advertised")
			}
			if _, err := s.ManagedTestRegistry().Get(ctx, old.Record.CaseID); !errors.Is(err, task.ErrStorageUnavailable) {
				t.Fatalf("legacy Get = %v", err)
			}
			fresh := old
			fresh.AcceptanceID = strings.Repeat("2", 32)
			fresh.Record.LastVerifiedAt = old.Record.LastVerifiedAt.Add(time.Minute)
			fresh.At = fresh.Record.LastVerifiedAt
			r := s.ManagedTestRegistry()
			if err := r.BeginManagedAcceptance(ctx, fresh); err != nil {
				t.Fatal(err)
			}
			if err := r.MarkManagedFileWritten(ctx, fresh.AcceptanceID, fresh.PublishedFileDigest); err != nil {
				t.Fatal(err)
			}
			if err := r.CommitAccepted(ctx, fresh); !errors.Is(err, task.ErrStorageUnavailable) {
				t.Fatalf("unverified commit = %v", err)
			}
			evidence := managedtest.LegacyRecoveryEvidence{AcceptanceID: fresh.AcceptanceID, PreimageDocument: document, PublishedDocument: document, SourceBytes: source, ValidationReceipt: receipt}
			err = r.AuthorizeLegacyManagedRecovery(ctx, evidence)
			if !tc.wantRecovery {
				if !errors.Is(err, task.ErrStorageUnavailable) {
					t.Fatalf("tampered recovery = %v", err)
				}
				if s.ManagedTestsReady() {
					t.Fatal("tampered legacy became ready")
				}
				s.Close()
				return
			}
			if err != nil {
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
			if err := s.ManagedTestRegistry().CommitAccepted(ctx, fresh); err != nil {
				t.Fatalf("durable recovery commit: %v", err)
			}
			if !s.ManagedTestsReady() {
				t.Fatal("recovered registry not ready")
			}
			got, err := s.ManagedTestRegistry().Get(ctx, fresh.Record.CaseID)
			if err != nil || got != fresh.Record {
				t.Fatalf("recovered Get = %+v, %v", got, err)
			}
		})
	}
}

func TestLegacyRecoveryRejectsForgedAuthorizationAndInvalidEvidence(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "legacy-forge.sqlite")
	old, document, source, receipt := seedAccepted017(t, path, false)
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	fresh := old
	fresh.AcceptanceID = strings.Repeat("2", 32)
	fresh.Record.LastVerifiedAt = old.Record.LastVerifiedAt.Add(time.Minute)
	fresh.At = fresh.Record.LastVerifiedAt
	r := s.ManagedTestRegistry()
	if err := r.BeginManagedAcceptance(ctx, fresh); err != nil {
		t.Fatal(err)
	}
	if err := r.MarkManagedFileWritten(ctx, fresh.AcceptanceID, fresh.PublishedFileDigest); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE managed_test_pending_acceptances SET legacy_recovery_revision=1,legacy_recovery_mac=? WHERE acceptance_id=?`, strings.Repeat("0", 64), fresh.AcceptanceID); err != nil {
		t.Fatal(err)
	}
	if err := r.CommitAccepted(ctx, fresh); !errors.Is(err, task.ErrStorageUnavailable) {
		t.Fatalf("forged authorization = %v", err)
	}
	evidence := managedtest.LegacyRecoveryEvidence{AcceptanceID: fresh.AcceptanceID, PreimageDocument: document, PublishedDocument: document, SourceBytes: []byte("wrong"), ValidationReceipt: receipt}
	if err := r.AuthorizeLegacyManagedRecovery(ctx, evidence); !errors.Is(err, task.ErrConflict) {
		t.Fatalf("wrong source = %v", err)
	}
	evidence.SourceBytes = source
	evidence.ValidationReceipt = []byte("wrong")
	if err := r.AuthorizeLegacyManagedRecovery(ctx, evidence); !errors.Is(err, task.ErrConflict) {
		t.Fatalf("wrong receipt = %v", err)
	}
	evidence.ValidationReceipt = receipt
	if err := r.AuthorizeLegacyManagedRecovery(ctx, evidence); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE managed_test_records SET accepted_block_digest=? WHERE case_id=?`, strings.Repeat("0", 64), old.Record.CaseID); err != nil {
		t.Fatal(err)
	}
	if err := r.CommitAccepted(ctx, fresh); !errors.Is(err, task.ErrStorageUnavailable) {
		t.Fatalf("revision-pinned record tamper = %v", err)
	}
}

func TestMigration019FailurePreservesV15(t *testing.T) {
	ctx := context.Background()
	migrations, err := loadMigrations()
	if err != nil || len(migrations) < 19 || migrations[18].version != 19 {
		t.Fatalf("migration 019 = %v", err)
	}
	path := filepath.Join(t.TempDir(), "migration019.sqlite")
	db := openConfiguredDatabase(t, path)
	s := &Store{db: db, newID: task.NewID}
	applyMigrationsThrough(t, ctx, s, migrations[:18])
	if _, err := db.ExecContext(ctx, `ALTER TABLE managed_test_pending_acceptances ADD COLUMN legacy_recovery_revision INTEGER`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatalf("v1.5 unavailable: %v", err)
	}
	defer reopened.Close()
	if !reopened.CoverageDetailReady() || reopened.ManagedTestsReady() {
		t.Fatal("optional migration failure leaked into v1.5 or managed capability")
	}
}
