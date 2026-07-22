//go:build windows

package artifactstore

import (
	"errors"
	"os"
	"syscall"

	"golang.org/x/sys/windows"
)

func pathEntryIsLink(path string, info os.FileInfo) (bool, error) {
	if isLinkInfo(info) {
		return true, nil
	}
	pointer, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return false, err
	}
	attributes, err := windows.GetFileAttributes(pointer)
	if err != nil {
		return false, err
	}
	return attributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0, nil
}

func isLinkInfo(info os.FileInfo) bool {
	if info.Mode()&os.ModeSymlink != 0 {
		return true
	}
	data, ok := info.Sys().(*syscall.Win32FileAttributeData)
	return ok && data.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0
}

func syncDirectoryHandle(directory *os.File) error {
	var information windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(windows.Handle(directory.Fd()), &information); err != nil {
		return ErrStoreUnavailable
	}
	if information.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return ErrUnsafePath
	}
	// Windows has no portable directory fsync. Publication is handle-relative,
	// and the artifact file itself was flushed before its final hard link appears.
	return nil
}

func isDirectoryNotEmpty(err error) bool {
	return errors.Is(err, windows.ERROR_DIR_NOT_EMPTY)
}
