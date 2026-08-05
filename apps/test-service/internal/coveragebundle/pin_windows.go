//go:build windows

package coveragebundle

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

func openPinnedRegular(path string) (*os.File, error) {
	return openPinnedWindowsObject(path, false)
}

func openPinnedDirectory(path string) (*os.File, error) {
	return openPinnedWindowsObject(path, true)
}

func openPinnedWindowsObject(path string, directory bool) (*os.File, error) {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	access := uint32(windows.GENERIC_READ)
	flags := uint32(windows.FILE_ATTRIBUTE_NORMAL | windows.FILE_FLAG_OPEN_REPARSE_POINT)
	share := uint32(windows.FILE_SHARE_READ)
	if directory {
		access = windows.FILE_READ_ATTRIBUTES
		flags = windows.FILE_FLAG_BACKUP_SEMANTICS | windows.FILE_FLAG_OPEN_REPARSE_POINT
		share |= windows.FILE_SHARE_WRITE
	}
	handle, err := windows.CreateFile(name, access, share, nil, windows.OPEN_EXISTING, flags, 0)
	if err != nil {
		return nil, err
	}
	var information windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &information); err != nil {
		_ = windows.CloseHandle(handle)
		return nil, err
	}
	wantDirectory := information.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY != 0
	if wantDirectory != directory || information.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		_ = windows.CloseHandle(handle)
		return nil, errors.New("opened bundle object has unsafe attributes")
	}
	file := os.NewFile(uintptr(handle), path)
	if file == nil {
		_ = windows.CloseHandle(handle)
		return nil, errors.New("construct bundle pin")
	}
	return file, nil
}
