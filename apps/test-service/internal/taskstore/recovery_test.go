package taskstore

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"unit-test-ide.local/test-service/internal/task"
)

func TestManagedPendingRecoveryRequiresObservedFileDigest(t *testing.T) {
	for _, written := range []bool{false, true} {
		t.Run(map[bool]string{false: "preimage", true: "published"}[written], func(t *testing.T) {
			ctx := context.Background()
			path := filepath.Join(t.TempDir(), "restart.sqlite")
			s, err := Open(path)
			if err != nil {
				t.Fatal(err)
			}
			a, _ := managedFixture(t, "1")
			if err := s.ManagedTestRegistry().BeginManagedAcceptance(ctx, a); err != nil {
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
			if err := s.RecoverPendingManagedAcceptance(ctx, a.AcceptanceID, strings.Repeat("9", 64)); !errors.Is(err, task.ErrConflict) {
				t.Fatalf("unknown digest = %v", err)
			}
			observed := a.PreimageDigest
			if written {
				observed = a.PublishedFileDigest
			}
			if err := s.RecoverPendingManagedAcceptance(ctx, a.AcceptanceID, observed); err != nil {
				t.Fatal(err)
			}
			pending, err := s.ManagedTestRegistry().ListPendingManagedAcceptances(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if written {
				if len(pending) != 1 || pending[0].Phase != "file_written" {
					t.Fatalf("written pending = %+v", pending)
				}
				if err := s.ManagedTestRegistry().CommitAccepted(ctx, a); err != nil {
					t.Fatal(err)
				}
			} else if len(pending) != 0 {
				t.Fatalf("rolled-back pending = %+v", pending)
			}
		})
	}
}

func TestManagedPendingRecoveryRejectsPayloadColumnMismatch(t *testing.T) {
	ctx := context.Background()
	s, err := Open(filepath.Join(t.TempDir(), "pending-tamper.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	a, _ := managedFixture(t, "1")
	if err := s.ManagedTestRegistry().BeginManagedAcceptance(ctx, a); err != nil {
		t.Fatal(err)
	}
	tampered := a
	tampered.PublishedFileDigest = strings.Repeat("8", 64)
	encoded, err := json.Marshal(tampered)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE managed_test_pending_acceptances SET record_json=? WHERE acceptance_id=?`, string(encoded), a.AcceptanceID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ManagedTestRegistry().ListPendingManagedAcceptances(ctx); !errors.Is(err, task.ErrStorageUnavailable) {
		t.Fatalf("tampered recovery payload = %v", err)
	}
}
