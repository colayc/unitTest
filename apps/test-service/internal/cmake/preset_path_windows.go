//go:build windows

package cmake

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/windows"
)

func inspectPresetPathComponent(path string) (string, bool, error) {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return "", false, err
	}
	handle, err := windows.CreateFile(
		name,
		windows.FILE_READ_ATTRIBUTES,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT,
		0,
	)
	if err != nil {
		if errors.Is(err, windows.ERROR_FILE_NOT_FOUND) ||
			errors.Is(err, windows.ERROR_PATH_NOT_FOUND) {
			return "", false, os.ErrNotExist
		}
		return "", false, err
	}
	defer windows.CloseHandle(handle)

	var information windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &information); err != nil {
		return "", false, err
	}
	isLink := information.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0
	return fmt.Sprintf(
		"windows:%08x:%08x%08x:%08x:%08x%08x:%08x%08x",
		information.VolumeSerialNumber,
		information.FileIndexHigh,
		information.FileIndexLow,
		information.FileAttributes,
		information.LastWriteTime.HighDateTime,
		information.LastWriteTime.LowDateTime,
		information.FileSizeHigh,
		information.FileSizeLow,
	), isLink, nil
}
