package taskstore

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"unit-test-ide.local/test-service/internal/managedtest"
	"unit-test-ide.local/test-service/internal/task"
)

func TestManagedRetirementIsDurableAndIdempotent(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "managed-retire.sqlite")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	r := store.ManagedTestRegistry()
	a, _ := managedFixture(t, "1")
	if err := r.BeginManagedAcceptance(ctx, a); err != nil {
		t.Fatal(err)
	}
	if err := r.MarkManagedFileWritten(ctx, a.AcceptanceID, a.PublishedFileDigest); err != nil {
		t.Fatal(err)
	}
	if err := r.CommitAccepted(ctx, a); err != nil {
		t.Fatal(err)
	}
	retirement := managedtest.Retirement{CaseID: a.Record.CaseID, ReviewDigest: strings.Repeat("2", 64), PublishedFileDigest: strings.Repeat("3", 64), ConfirmationDigest: strings.Repeat("4", 64), TestRelativePath: a.Record.TestRelativePath, At: a.At.Add(time.Minute)}
	if err := r.RetireAccepted(ctx, retirement); err != nil {
		t.Fatal(err)
	}
	if err := r.RetireAccepted(ctx, retirement); err != nil {
		t.Fatalf("idempotent retirement: %v", err)
	}
	other := retirement
	other.ConfirmationDigest = strings.Repeat("5", 64)
	if err := r.RetireAccepted(ctx, other); !errors.Is(err, task.ErrConflict) {
		t.Fatalf("different decision accepted: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if !store.ManagedTestsReady() {
		t.Fatal("retirement invalidated registry")
	}
	record, err := store.ManagedTestRegistry().Get(ctx, a.Record.CaseID)
	if err != nil || record.Status != managedtest.StatusInvalid {
		t.Fatalf("retirement lost on restart: %+v %v", record, err)
	}
}
