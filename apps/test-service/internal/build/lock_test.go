package build

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestDirectoryLocksRejectConcurrentBinaryDirectoryConflict(t *testing.T) {
	locks := NewDirectoryLocks()
	otherProcessRegistry := NewDirectoryLocks()
	directory := filepath.Join(t.TempDir(), "build")
	if err := os.Mkdir(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	first, err := locks.Acquire(directory)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := locks.Acquire(directory); !errors.Is(err, ErrBuildDirectoryBusy) {
		t.Fatalf("second Acquire() error = %v, want ErrBuildDirectoryBusy", err)
	}
	if _, err := otherProcessRegistry.Acquire(directory); !errors.Is(err, ErrBuildDirectoryBusy) {
		t.Fatalf("cross-registry Acquire() error = %v, want ErrBuildDirectoryBusy", err)
	}
	if err := first.Release(); err != nil {
		t.Fatal(err)
	}
	second, err := locks.Acquire(directory)
	if err != nil {
		t.Fatalf("Acquire() after Release error = %v", err)
	}
	if err := second.Release(); err != nil {
		t.Fatal(err)
	}
}
