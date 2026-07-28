//go:build windows

package cmake

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/windows"
)

func openStableFile(path string) (*os.File, error) {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	handle, err := windows.CreateFile(
		name,
		windows.GENERIC_READ,
		windows.FILE_SHARE_READ,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_ATTRIBUTE_NORMAL|windows.FILE_FLAG_OPEN_REPARSE_POINT|windows.FILE_FLAG_SEQUENTIAL_SCAN,
		0,
	)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(handle), path)
	if file == nil {
		_ = windows.CloseHandle(handle)
		return nil, errors.New("construct file from handle")
	}
	return file, nil
}

func stableFileOSIdentity(file *os.File) (string, error) {
	var information windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(windows.Handle(file.Fd()), &information); err != nil {
		return "", err
	}
	if information.FileAttributes&(windows.FILE_ATTRIBUTE_DIRECTORY|windows.FILE_ATTRIBUTE_REPARSE_POINT) != 0 {
		return "", errors.New("handle is not a direct regular file")
	}
	if information.FileIndexHigh == 0 && information.FileIndexLow == 0 {
		return "", errors.New("filesystem did not provide a file index")
	}
	return fmt.Sprintf(
		"windows:%08x:%08x%08x",
		information.VolumeSerialNumber,
		information.FileIndexHigh,
		information.FileIndexLow,
	), nil
}
