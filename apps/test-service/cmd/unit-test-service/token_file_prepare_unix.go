//go:build !windows

package main

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

func createTokenFile(path string) (*os.File, error) {
	fd, err := unix.Open(
		path,
		unix.O_RDWR|unix.O_CREAT|unix.O_EXCL|unix.O_CLOEXEC|unix.O_NOFOLLOW,
		0o600,
	)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fd), path)
	if err := file.Chmod(0o600); err != nil {
		info, statErr := file.Stat()
		closeErr := file.Close()
		var cleanupErr error
		if statErr == nil {
			cleanupErr = removeSameTokenFile(path, info)
		}
		return nil, errors.Join(err, statErr, closeErr, cleanupErr)
	}
	return file, nil
}
