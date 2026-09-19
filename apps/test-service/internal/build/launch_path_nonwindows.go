//go:build !windows

package build

import "path/filepath"

func cmakeLaunchPath(path string) (string, error) { return filepath.Clean(path), nil }

func cmakeLaunchExecutablePath(path string) (string, error) {
	return cmakeLaunchPath(path)
}
