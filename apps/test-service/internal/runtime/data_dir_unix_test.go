//go:build !windows

package runtime

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPrepareDataDirRejectsPermissiveUnixMode(t *testing.T) {
	root := filepath.Join(t.TempDir(), "permissive")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := PrepareDataDir(root); err == nil {
		t.Fatal("PrepareDataDir accepted mode 0755")
	}
}

func TestPrepareDataDirRejectsUnixSymlinkInEveryPathSegment(t *testing.T) {
	target := filepath.Join(t.TempDir(), "target")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if _, err := PrepareDataDir(filepath.Join(link, "nested")); err == nil {
		t.Fatal("PrepareDataDir accepted a symlink ancestor")
	}
}
