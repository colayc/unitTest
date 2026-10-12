//go:build !windows

package runtime

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"unit-test-ide.local/test-service/internal/toolhash"
)

func TestRuntimeToolDigestScopeNonWindowsHasNoAuthority(t *testing.T) {
	ctx := context.Background()
	scoped, scope, err := beginCoverageToolScope(ctx)
	if err != nil || scoped != ctx || scope != nil {
		t.Fatalf("unexpected scope: %v %v", scope, err)
	}
	path := filepath.Join(t.TempDir(), "tool")
	if err := os.WriteFile(path, []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	for i := 0; i < 2; i++ {
		result, protected, err := toolhash.Digest(scoped, path, f, 1024)
		if err != nil || protected || result.Evidence != nil || result.Reused {
			t.Fatalf("non-Windows acquired authority: %+v %v %v", result, protected, err)
		}
		if err := os.WriteFile(path, []byte("changed"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	_, scope, err = beginCoverageToolScope(cancelled)
	if !errors.Is(err, context.Canceled) || scope != nil {
		t.Fatalf("cancelled begin: %v %v", scope, err)
	}
}
