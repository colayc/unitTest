package testcontrol

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestAllocatorReadsPinnedControlFileOnceAndRemovesIt(
	t *testing.T,
) {
	root := t.TempDir()
	allocator, err := NewAllocator(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = allocator.Close() })
	control, err := allocator.Allocate(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	path := control.Path()
	if !filepath.IsAbs(path) {
		t.Fatalf("control path = %q", path)
	}
	want := []byte("{\"type\":\"case\"}\n")
	if err := os.WriteFile(path, want, 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := control.Read(context.Background(), int64(len(want)))
	if err != nil || string(got) != string(want) {
		t.Fatalf("Read() = %q, %v", got, err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("control file remains after Read: %v", err)
	}
	if _, err := control.Read(
		context.Background(),
		int64(len(want)),
	); !errors.Is(err, ErrControlUnavailable) {
		t.Fatalf("second Read() error = %v", err)
	}
}

func TestAllocatorCleansStaleFilesAndCloseReleasesLiveFiles(
	t *testing.T,
) {
	root := t.TempDir()
	stale := filepath.Join(root, "stale.jsonl")
	if err := os.WriteFile(stale, []byte("stale"), 0o600); err != nil {
		t.Fatal(err)
	}
	allocator, err := NewAllocator(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(stale); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stale file remains: %v", err)
	}
	control, err := allocator.Allocate(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	path := control.Path()
	if err := allocator.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("live file remains after Close: %v", err)
	}
	if _, err := allocator.Allocate(
		context.Background(),
	); !errors.Is(err, ErrControlUnavailable) {
		t.Fatalf("Allocate after Close error = %v", err)
	}
}

func TestAllocatorRejectsOversizedControlOutput(t *testing.T) {
	allocator, err := NewAllocator(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = allocator.Close() })
	control, err := allocator.Allocate(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		control.Path(),
		[]byte("too-large"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := control.Read(
		context.Background(),
		3,
	); !errors.Is(err, ErrInvalidControlFile) {
		t.Fatalf("oversized Read() error = %v", err)
	}
}

func TestAllocatorRejectsUnsafeReadLimitWithoutConsumingFile(
	t *testing.T,
) {
	allocator, err := NewAllocator(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = allocator.Close() })
	control, err := allocator.Allocate(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := control.Read(
		context.Background(),
		maximumControlFileBytes+1,
	); !errors.Is(err, ErrInvalidControlFile) {
		t.Fatalf("unsafe Read() error = %v", err)
	}
	if control.Path() == "" {
		t.Fatal("invalid limit consumed the control file")
	}
}
