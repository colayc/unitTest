package runtime

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestPrepareDataDirReturnsFixedAbsoluteLayout(t *testing.T) {
	root := filepath.Join(t.TempDir(), "private", "service-data")
	layout, err := PrepareDataDir(root)
	if err != nil {
		t.Fatal(err)
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		t.Fatal(err)
	}
	if layout.Root != absolute || layout.Database != filepath.Join(absolute, "history.sqlite3") ||
		layout.Artifacts != filepath.Join(absolute, "artifacts") || layout.Lock != filepath.Join(absolute, "service.lock") {
		t.Fatalf("layout = %#v", layout)
	}
	info, err := os.Stat(layout.Root)
	if err != nil || !info.IsDir() {
		t.Fatalf("data directory = %#v, %v", info, err)
	}
}

func TestPrepareDataDirRejectsARegularFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := PrepareDataDir(path); !errors.Is(err, ErrUnsafeDataDir) {
		t.Fatalf("PrepareDataDir() error = %v, want ErrUnsafeDataDir", err)
	}
}

func TestPrepareDataDirRejectsNULWithoutPanicking(t *testing.T) {
	if _, err := PrepareDataDir(filepath.Join(t.TempDir(), "bad") + "\x00suffix"); !errors.Is(err, ErrUnsafeDataDir) {
		t.Fatalf("PrepareDataDir() error = %v, want ErrUnsafeDataDir", err)
	}
}
