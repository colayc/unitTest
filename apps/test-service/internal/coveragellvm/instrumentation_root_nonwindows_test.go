//go:build !windows

package coveragellvm

import (
	"os"
	"testing"
)

func makeOwnerOnlyInstrumentationRoot(t *testing.T, path string) {
	t.Helper()
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
}
