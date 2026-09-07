//go:build linux

package runtime

import (
	"path/filepath"
	"testing"
)

func TestLinuxCoverageBundleRootIsExactCanonicalSeam(t *testing.T) {
	root := filepath.Join(t.TempDir(), "bundles", "coverage")
	adapter := gccCoverageAdapter{bundleRoot: root}
	if adapter.bundleRoot != root || !filepath.IsAbs(adapter.bundleRoot) || filepath.Clean(adapter.bundleRoot) != adapter.bundleRoot {
		t.Fatalf("bundle root seam = %q", adapter.bundleRoot)
	}
}
