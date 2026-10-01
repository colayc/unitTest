//go:build windows

package testgenpublish

import (
	"os"
	"syscall"

	"golang.org/x/sys/windows"
)

func linked(info os.FileInfo) bool {
	if info == nil || info.Mode()&os.ModeSymlink != 0 {
		return true
	}
	data, ok := info.Sys().(*syscall.Win32FileAttributeData)
	return ok && data.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0
}
func sameMode(actual, want os.FileMode) bool { return (actual&0222 == 0) == (want&0222 == 0) }
