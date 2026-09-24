//go:build windows

package ctest

import (
	"os"
	"path/filepath"

	"golang.org/x/sys/windows"
)

func canonicalNativePath(path string) string {
	path = filepath.Clean(path)
	value, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return path
	}
	buffer := make([]uint16, 32768)
	length, err := windows.GetLongPathName(value, &buffer[0], uint32(len(buffer)))
	if err != nil || length == 0 || length >= uint32(len(buffer)) {
		return path
	}
	return filepath.Clean(windows.UTF16ToString(buffer[:length]))
}

// launchNativePath returns a process-launch spelling that remains usable by
// CreateProcessW when CTest reports a working directory beyond MAX_PATH.
// CMake already emits short aliases for many executable paths, but its CTest
// JSON keeps the working directory in its long spelling. Shortening only
// oversized existing paths preserves the descriptor's normal identity while
// avoiding ERROR_INVALID_NAME during framework discovery.
func launchNativePath(path string) string {
	path = filepath.Clean(path)
	if path == "" || len(path) < 248 {
		return path
	}
	if _, err := os.Stat(path); err != nil {
		return path
	}
	value, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return path
	}
	buffer := make([]uint16, 32768)
	length, err := windows.GetShortPathName(value, &buffer[0], uint32(len(buffer)))
	if err != nil || length == 0 || length >= uint32(len(buffer)) {
		return path
	}
	return filepath.Clean(windows.UTF16ToString(buffer[:length]))
}
