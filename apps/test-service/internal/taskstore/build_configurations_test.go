package taskstore

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"unit-test-ide.local/test-service/internal/task"
)

func TestBuildConfigurationRoundTripAndAtomicReplacement(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "history.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	ctx := context.Background()
	first := BuildConfiguration{
		WorkspaceID: strings.Repeat("9", 64), ProjectID: "core", ProfileID: strings.Repeat("a", 64),
		Fingerprint: strings.Repeat("b", 64), BuildDirectory: "build/profile",
		CMakeIdentity: strings.Repeat("c", 64), FileAPIIdentity: strings.Repeat("d", 64),
		ConfiguredAt: time.Date(2026, 7, 29, 1, 2, 3, 0, time.UTC),
	}
	if err := store.PutBuildConfiguration(ctx, first); err != nil {
		t.Fatal(err)
	}
	got, err := store.GetBuildConfiguration(ctx, first.WorkspaceID, first.ProjectID, first.ProfileID)
	if err != nil || got != first {
		t.Fatalf("GetBuildConfiguration() = %#v, %v", got, err)
	}
	second := first
	second.Fingerprint = strings.Repeat("e", 64)
	second.ConfiguredAt = second.ConfiguredAt.Add(time.Minute)
	if err := store.PutBuildConfiguration(ctx, second); err != nil {
		t.Fatal(err)
	}
	got, err = store.GetBuildConfiguration(ctx, first.WorkspaceID, first.ProjectID, first.ProfileID)
	if err != nil || got != second {
		t.Fatalf("replacement = %#v, %v", got, err)
	}
}

func TestBuildConfigurationUnknownKeyAndInvalidRecordFailClosed(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "history.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	ctx := context.Background()
	if _, err := store.GetBuildConfiguration(ctx, strings.Repeat("8", 64), "core", strings.Repeat("a", 64)); !errors.Is(err, task.ErrNotFound) {
		t.Fatalf("GetBuildConfiguration() error = %v, want ErrNotFound", err)
	}
	if err := store.PutBuildConfiguration(ctx, BuildConfiguration{}); !errors.Is(err, task.ErrInvalidArgument) {
		t.Fatalf("PutBuildConfiguration(empty) error = %v, want ErrInvalidArgument", err)
	}
}
