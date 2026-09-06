//go:build windows

package coverageplatform

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPublishInstrumentationWindowsPinsRootAgainstReplacement(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "root")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	oldHook := instrumentationWindowsRootPinnedForTest
	instrumentationWindowsRootPinnedForTest = func() {
		if err := os.Rename(root, filepath.Join(base, "replacement")); err == nil {
			t.Fatal("retained Windows root allowed replacement")
		}
	}
	t.Cleanup(func() { instrumentationWindowsRootPinnedForTest = oldHook })
	if _, err := PublishInstrumentation(root, "coverage.cmake", "line\n", "v1"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(root, "coverage.cmake")); err != nil {
		t.Fatal(err)
	}
}

func TestPublishInstrumentationWindowsPinsAncestorsAgainstReplacement(t *testing.T) {
	base := t.TempDir()
	ancestor := filepath.Join(base, "ancestor")
	root := filepath.Join(ancestor, "root")
	if err := os.Mkdir(ancestor, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	oldHook := instrumentationWindowsAncestorsPinnedForTest
	instrumentationWindowsAncestorsPinnedForTest = func() {
		if err := os.Rename(ancestor, filepath.Join(base, "replacement")); err == nil {
			t.Fatal("retained ancestor allowed replacement")
		}
	}
	t.Cleanup(func() { instrumentationWindowsAncestorsPinnedForTest = oldHook })
	if _, err := PublishInstrumentation(root, "coverage.cmake", "line\n", "v1"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(root, "coverage.cmake")); err != nil {
		t.Fatal(err)
	}
}

func TestPublishInstrumentationWindowsCleansReadOnlyTemporaryAfterCollision(t *testing.T) {
	root := filepath.Join(t.TempDir(), "root")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	oldHook := instrumentationWindowsBeforeRenameForTest
	instrumentationWindowsBeforeRenameForTest = func() {
		if err := os.WriteFile(filepath.Join(root, "coverage.cmake"), []byte("collision"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { instrumentationWindowsBeforeRenameForTest = oldHook })
	if _, err := PublishInstrumentation(root, "coverage.cmake", "line\n", "v1"); err == nil {
		t.Fatal("rename collision succeeded")
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "coverage.cmake" {
		names := make([]string, len(entries))
		for i := range entries {
			names[i] = entries[i].Name()
		}
		t.Fatalf("entries = %#v, want collision only", names)
	}
	contents, err := os.ReadFile(filepath.Join(root, "coverage.cmake"))
	if err != nil || string(contents) != "collision" {
		t.Fatalf("collision changed: %q, %v", contents, err)
	}
}
