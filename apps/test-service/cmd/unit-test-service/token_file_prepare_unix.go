//go:build !windows

package main

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

func createTokenFile(path string) (*os.File, os.FileInfo, error) {
	fd, err := unix.Open(
		path,
		unix.O_RDWR|unix.O_CREAT|unix.O_EXCL|unix.O_CLOEXEC|unix.O_NOFOLLOW,
		0o600,
	)
	if err != nil {
		return nil, nil, err
	}
	file := os.NewFile(uintptr(fd), path)
	info, statErr := file.Stat()
	if statErr != nil {
		return nil, nil, errors.Join(statErr, file.Close())
	}
	if err := file.Chmod(0o600); err != nil {
		closeErr := file.Close()
		cleanupErr := removeSameTokenFile(path, info)
		return nil, nil, errors.Join(err, closeErr, cleanupErr)
	}
	return file, info, nil
}
