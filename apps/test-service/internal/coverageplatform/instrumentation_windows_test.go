//go:build windows

package coverageplatform

import (
	"errors"
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

func TestPublishInstrumentationWindowsFailsClosedWhenAncestorBindingFails(t *testing.T) {
	for _, injected := range []error{os.ErrPermission, errors.New("metadata unavailable")} {
		t.Run(injected.Error(), func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "root")
			if err := os.Mkdir(root, 0o700); err != nil {
				t.Fatal(err)
			}
			oldHook := instrumentationWindowsAncestorBindingFailureForTest
			instrumentationWindowsAncestorBindingFailureForTest = func(path string) error {
				if path == root {
					return injected
				}
				return nil
			}
			t.Cleanup(func() { instrumentationWindowsAncestorBindingFailureForTest = oldHook })
			if _, err := PublishInstrumentation(root, "coverage.cmake", "line\n", "v1"); err == nil {
				t.Fatal("unbound ancestor published")
			}
			entries, err := os.ReadDir(root)
			if err != nil || len(entries) != 0 {
				t.Fatalf("failed binding wrote root: %#v, %v", entries, err)
			}
		})
	}
}

func TestPublishInstrumentationWindowsRejectsSymlinkAncestorBeforeWriting(t *testing.T) {
	base := t.TempDir()
	target := filepath.Join(base, "target")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err := PublishInstrumentation(link, "coverage.cmake", "line\n", "v1"); err == nil {
		t.Fatal("symlink ancestor published")
	}
	entries, err := os.ReadDir(target)
	if err != nil || len(entries) != 0 {
		t.Fatalf("symlink target changed: %#v, %v", entries, err)
	}
}
