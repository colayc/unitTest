//go:build windows

package build

import (
	"errors"
	"os"
	"path/filepath"

	"golang.org/x/sys/windows"
)

// cmakeLaunchPath returns a stable short spelling for an existing path. CMake
// may reduce long components to their 8.3 aliases while creating try-compile
// directories and launching child tools. All path-valued launch inputs must
// therefore use one spelling throughout a launch plan.
func cmakeLaunchPath(path string) (string, error) {
	path = filepath.Clean(path)
	if path == "" {
		return "", errors.New("empty launch executable")
	}
	if _, err := os.Stat(path); err != nil {
		// Deterministic planner fixtures may describe a generated executable
		// before it exists. Keep that declaration unchanged; CMake cannot
		// resolve a short spelling until the file is created.
		if errors.Is(err, os.ErrNotExist) {
			return path, nil
		}
		return "", err
	}
	longPath, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return "", err
	}
	buffer := make([]uint16, 32768)
	length, err := windows.GetShortPathName(longPath, &buffer[0], uint32(len(buffer)))
	if err != nil {
		return "", err
	}
	if length == 0 || length >= uint32(len(buffer)) {
		return "", errors.New("short launch executable path unavailable")
	}
	return filepath.Clean(windows.UTF16ToString(buffer[:length])), nil
}

func cmakeLaunchExecutablePath(path string) (string, error) {
	return cmakeLaunchPath(path)
}
