//go:build windows

package build

import (
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
