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
