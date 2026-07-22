//go:build !windows

package artifactstore

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"syscall"

	"golang.org/x/sys/unix"
)

func pathEntryIsLink(_ string, info os.FileInfo) (bool, error) {
	return info.Mode()&os.ModeSymlink != 0, nil
}

func openNoFollow(path string) (*os.File, error) {
	descriptor, err := unix.Open(path, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		if errors.Is(err, unix.ELOOP) {
			return nil, ErrUnsafePath
		}
		return nil, ErrStoreUnavailable
	}
	file := os.NewFile(uintptr(descriptor), "artifact")
	if file == nil {
		_ = unix.Close(descriptor)
		return nil, ErrStoreUnavailable
	}
	if runtime.GOOS == "linux" {
		openedPath, err := os.Readlink(fmt.Sprintf("/proc/self/fd/%d", descriptor))
		if err != nil || filepath.Clean(openedPath) != filepath.Clean(path) {
			_ = file.Close()
			return nil, ErrUnsafePath
		}
	}
	return file, nil
}

func syncDirectory(path string) error {
	descriptor, err := unix.Open(path, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return ErrStoreUnavailable
	}
	defer unix.Close(descriptor)
	if err := unix.Fsync(descriptor); err != nil {
		return ErrStoreUnavailable
	}
	return nil
}

func renameAtomic(source, target string) error {
	if err := os.Rename(source, target); err != nil {
		return ErrStoreUnavailable
	}
	return nil
}

func isDirectoryNotEmpty(err error) bool {
	return errors.Is(err, syscall.ENOTEMPTY) || errors.Is(err, syscall.EEXIST)
}
