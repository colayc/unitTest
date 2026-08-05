//go:build !windows

package coveragebundle

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

func openPinnedRegular(path string) (*os.File, error) {
	return openPinnedUnixObject(path, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW)
}

func openPinnedDirectory(path string) (*os.File, error) {
	return openPinnedUnixObject(path, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW)
}

func openPinnedUnixObject(path string, flags int) (*os.File, error) {
	descriptor, err := unix.Open(path, flags, 0)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(descriptor), path)
	if file == nil {
		_ = unix.Close(descriptor)
		return nil, errors.New("construct bundle pin")
	}
	return file, nil
}
