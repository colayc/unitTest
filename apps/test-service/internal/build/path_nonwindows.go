//go:build !windows

package build

import "path/filepath"

func canonicalNativePath(path string) string {
	return filepath.Clean(path)
}
