//go:build windows

package artifactstore

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

func pathEntryIsLink(path string, info os.FileInfo) (bool, error) {
	if info.Mode()&os.ModeSymlink != 0 {
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

func openNoFollow(path string) (*os.File, error) {
	pointer, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, ErrUnsafePath
	}
	handle, err := windows.CreateFile(
		pointer,
		windows.GENERIC_READ,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_ATTRIBUTE_NORMAL|windows.FILE_FLAG_OPEN_REPARSE_POINT,
		0,
	)
	if err != nil {
		return nil, ErrStoreUnavailable
	}
	closeHandle := true
	defer func() {
		if closeHandle {
			_ = windows.CloseHandle(handle)
		}
	}()
	var information windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &information); err != nil {
		return nil, ErrStoreUnavailable
	}
	if information.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return nil, ErrUnsafePath
	}
	openedPath, err := finalPath(handle)
	if err != nil {
		return nil, ErrStoreUnavailable
	}
	if !strings.EqualFold(filepath.Clean(openedPath), filepath.Clean(path)) {
		return nil, ErrUnsafePath
	}
	file := os.NewFile(uintptr(handle), "artifact")
	if file == nil {
		return nil, ErrStoreUnavailable
	}
	closeHandle = false
	return file, nil
}

func finalPath(handle windows.Handle) (string, error) {
	buffer := make([]uint16, windows.MAX_PATH)
	for {
		length, err := windows.GetFinalPathNameByHandle(handle, &buffer[0], uint32(len(buffer)), 0)
		if err != nil {
			return "", err
		}
		if int(length) < len(buffer) {
			path := windows.UTF16ToString(buffer[:length])
			if strings.HasPrefix(path, `\\?\UNC\`) {
				return `\\` + strings.TrimPrefix(path, `\\?\UNC\`), nil
			}
			return strings.TrimPrefix(path, `\\?\`), nil
		}
		buffer = make([]uint16, length+1)
	}
}

func syncDirectory(path string) error {
	pointer, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return ErrUnsafePath
	}
	handle, err := windows.CreateFile(
		pointer,
		windows.GENERIC_READ,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT,
		0,
	)
	if err != nil {
		return ErrStoreUnavailable
	}
	defer windows.CloseHandle(handle)
	var information windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &information); err != nil {
		return ErrStoreUnavailable
	}
	if information.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return ErrUnsafePath
	}
	// Windows does not support fsync on directory handles. The artifact file is
	// flushed before the same-volume rename, and this handle pins the directory
	// while its post-rename identity is checked.
	return nil
}

func renameAtomic(source, target string) error {
	sourcePointer, err := windows.UTF16PtrFromString(source)
	if err != nil {
		return ErrUnsafePath
	}
	targetPointer, err := windows.UTF16PtrFromString(target)
	if err != nil {
		return ErrUnsafePath
	}
	if err := windows.MoveFileEx(sourcePointer, targetPointer, windows.MOVEFILE_WRITE_THROUGH); err != nil {
		return ErrStoreUnavailable
	}
	return nil
}

func isDirectoryNotEmpty(err error) bool {
	return errors.Is(err, windows.ERROR_DIR_NOT_EMPTY)
}
