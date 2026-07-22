//go:build windows

package artifactstore

import (
	"errors"
	"fmt"
	"os"
	"syscall"
	"unsafe"

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
	original := windows.Handle(directory.Fd())
	if err := windows.GetFileInformationByHandle(original, &information); err != nil {
		return fmt.Errorf("%w: inspect pinned directory: %v", ErrStoreUnavailable, err)
	}
	if information.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return ErrUnsafePath
	}
	reopened, err := openPinnedDirectoryForSync(original)
	if err != nil {
		return fmt.Errorf("%w: NtCreateFile relative to pinned directory: %v", ErrStoreUnavailable, err)
	}
	defer windows.CloseHandle(reopened)
	if err := windows.FlushFileBuffers(reopened); err != nil {
		return fmt.Errorf("%w: FlushFileBuffers pinned directory: %v", ErrStoreUnavailable, err)
	}
	return nil
}

func openPinnedDirectoryForSync(original windows.Handle) (windows.Handle, error) {
	// An empty NT object name reopens RootDirectory itself. This preserves the
	// pinned object identity while requesting the write access FlushFileBuffers
	// requires, without reconstructing or following a filesystem path.
	name, err := windows.NewNTUnicodeString("")
	if err != nil {
		return windows.InvalidHandle, err
	}
	attributes := &windows.OBJECT_ATTRIBUTES{
		Length:        uint32(unsafe.Sizeof(windows.OBJECT_ATTRIBUTES{})),
		RootDirectory: original,
		ObjectName:    name,
		Attributes:    windows.OBJ_CASE_INSENSITIVE | windows.OBJ_DONT_REPARSE,
	}
	var handle windows.Handle
	var status windows.IO_STATUS_BLOCK
	var allocationSize int64
	err = windows.NtCreateFile(
		&handle,
		windows.FILE_GENERIC_WRITE,
		attributes,
		&status,
		&allocationSize,
		0,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		windows.FILE_OPEN,
		windows.FILE_DIRECTORY_FILE|windows.FILE_OPEN_FOR_BACKUP_INTENT|windows.FILE_SYNCHRONOUS_IO_NONALERT|windows.FILE_OPEN_REPARSE_POINT,
		0,
		0,
	)
	if err != nil {
		return windows.InvalidHandle, err
	}
	return handle, nil
}

func isDirectoryNotEmpty(err error) bool {
	return errors.Is(err, windows.ERROR_DIR_NOT_EMPTY)
}
