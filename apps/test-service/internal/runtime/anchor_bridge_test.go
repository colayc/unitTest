package runtime

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestAnchorLinknameIsRestrictedToRuntimeProductionBridge(t *testing.T) {
	_, sourceFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	root := filepath.Dir(sourceFile)
	assertNoProductionLinkname := func(directory string) {
		t.Helper()
		entries, err := os.ReadDir(directory)
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
				continue
			}
			contents, err := os.ReadFile(filepath.Join(directory, entry.Name()))
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(contents), "//go:linkname") {
				t.Fatalf("production linkname outside runtime/anchor_bridge.go: %s", filepath.Join(directory, entry.Name()))
			}
		}
	}
	for _, directory := range []string{
		filepath.Join(root, "..", "build"),
		filepath.Join(root, "..", "coveragebundle"),
	} {
		assertNoProductionLinkname(directory)
	}
}
