//go:build windows

package coveragebundle

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFreshPinnedDirectoryCleanupRejectsReplacement(t *testing.T) {
	root := filepath.Join(strictTestTempDir(t), "coverage")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	parent, err := pinDirectObject(root, true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = parent.Close() })
	const name = "fresh"
	path := filepath.Join(root, name)
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	expected, err := pinChildObject(parent, name, true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = expected.Close() })
	original := filepath.Join(root, "original")
	if err := os.Rename(path, original); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := removeFreshPinnedDirectory(parent, name, expected); err == nil {
		t.Fatal("fresh cleanup deleted a replacement directory")
	}
	if info, err := os.Stat(path); err != nil || !info.IsDir() {
		t.Fatalf("replacement after rejected cleanup = %v, %v", info, err)
	}
	if info, err := os.Stat(original); err != nil || !info.IsDir() {
		t.Fatalf("original after rejected cleanup = %v, %v", info, err)
	}
}
