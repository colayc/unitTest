//go:build !windows

package coverageplatform

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPublishInstrumentationRejectsRootReplacementBeforePublication(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "root")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	replacement := filepath.Join(base, "replacement")
	if err := os.Mkdir(replacement, 0o700); err != nil {
		t.Fatal(err)
	}
	oldHook := instrumentationRootPinnedForTest
	instrumentationRootPinnedForTest = func() {
		if err := os.Rename(root, filepath.Join(base, "old")); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(replacement, root); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { instrumentationRootPinnedForTest = oldHook })
	if _, err := PublishInstrumentation(root, "coverage.cmake", "line\n", "v1"); err == nil {
		t.Fatal("replaced root published")
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 0 {
		t.Fatalf("replacement root changed: %#v, %v", entries, err)
	}
}
