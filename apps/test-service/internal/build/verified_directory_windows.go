//go:build windows

package build

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

type directoryNativeIdentity struct {
	volume uint32
	high   uint32
	low    uint32
	attrs  uint32
}

func pinVerifiedDirectory(path string) (*verifiedDirectory, error) {
	absolute, err := filepath.Abs(path)
	if err != nil || filepath.Clean(path) != path || filepath.Clean(absolute) != path {
		return nil, errors.New("directory path is not canonical")
	}
	if err := rejectDirectoryReparseAncestors(path); err != nil {
		return nil, err
	}
	before, err := directoryPathIdentity(path)
	if err != nil {
		return nil, err
	}
	file, err := openDirectoryHandle(path)
	if err != nil {
		return nil, err
	}
	fail := func(cause error) (*verifiedDirectory, error) { _ = file.Close(); return nil, cause }
	info, err := file.Stat()
	if err != nil || !info.IsDir() {
		return fail(errors.New("path is not a directory"))
	}
	current, err := directoryHandleIdentity(windows.Handle(file.Fd()))
	if err != nil || current != before {
		return fail(errors.New("directory identity changed while pinning"))
	}
	after, err := directoryPathIdentity(path)
	if err != nil || after != current {
		return fail(errors.New("directory path changed while pinning"))
	}
	nativeText := fmt.Sprintf("windows:%08x:%08x%08x", current.volume, current.high, current.low)
	sum := sha256.Sum256([]byte("coverage-directory-v1\x00" + strings.ToLower(path) + "\x00" + nativeText))
	return &verifiedDirectory{path: path, identity: hex.EncodeToString(sum[:]), file: file, info: info, native: current}, nil
}

func verifyDirectory(directory *verifiedDirectory) error {
	if directory.file == nil || directory.info == nil {
		return errors.New("directory pin is closed")
	}
	if err := rejectDirectoryReparseAncestors(directory.path); err != nil {
		return err
	}
	before, err := directoryPathIdentity(directory.path)
	if err != nil || before != directory.native {
		return errors.New("directory path identity changed")
	}
	current, err := directoryHandleIdentity(windows.Handle(directory.file.Fd()))
	if err != nil || current != directory.native {
		return errors.New("directory handle identity changed")
	}
	info, err := directory.file.Stat()
	if err != nil || !info.IsDir() || !os.SameFile(directory.info, info) {
		return errors.New("directory information changed")
	}
	after, err := directoryPathIdentity(directory.path)
	if err != nil || after != current {
		return errors.New("directory changed while validating")
	}
	return rejectDirectoryReparseAncestors(directory.path)
}

func directoryPathIdentity(path string) (directoryNativeIdentity, error) {
	file, err := openDirectoryHandle(path)
	if err != nil {
		return directoryNativeIdentity{}, err
	}
	defer file.Close()
	return directoryHandleIdentity(windows.Handle(file.Fd()))
}

func directoryHandleIdentity(handle windows.Handle) (directoryNativeIdentity, error) {
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &info); err != nil {
		return directoryNativeIdentity{}, err
	}
	if info.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY == 0 || info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 || info.FileIndexHigh == 0 && info.FileIndexLow == 0 {
		return directoryNativeIdentity{}, errors.New("directory is indirect or has no identity")
	}
	return directoryNativeIdentity{volume: info.VolumeSerialNumber, high: info.FileIndexHigh, low: info.FileIndexLow, attrs: info.FileAttributes}, nil
}

func openDirectoryHandle(path string) (*os.File, error) {
	pointer, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	handle, err := windows.CreateFile(pointer, windows.FILE_READ_ATTRIBUTES, windows.FILE_SHARE_READ, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(handle), path)
	if file == nil {
		_ = windows.CloseHandle(handle)
		return nil, errors.New("construct directory handle")
	}
	return file, nil
}

func rejectDirectoryReparseAncestors(path string) error {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	root := filepath.VolumeName(absolute) + string(filepath.Separator)
	current := root
	for _, component := range strings.Split(strings.TrimPrefix(absolute, root), string(filepath.Separator)) {
		if component == "" {
			continue
		}
		current = filepath.Join(current, component)
		pointer, err := windows.UTF16PtrFromString(current)
		if err != nil {
			return err
		}
		attributes, err := windows.GetFileAttributes(pointer)
		if err != nil || attributes&windows.FILE_ATTRIBUTE_DIRECTORY == 0 || attributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
			return errors.New("directory ancestry crosses a reparse point")
		}
	}
	return nil
}
