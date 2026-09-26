//go:build !windows

package coveragenormalize

import "path/filepath"

func canonicalNativePath(path string) string {
	return filepath.Clean(path)
}
