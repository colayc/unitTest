//go:build !windows

package main

import (
	"errors"
	"fmt"
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

func openTokenFile(path string) (*os.File, error) {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), path), nil
}

func inspectTokenPath(path string) (os.FileInfo, error) {
	return os.Lstat(path)
}

func validateTokenFile(_ *os.File, info os.FileInfo) error {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return errors.New("cannot determine authentication token file ownership")
	}
	if stat.Uid != uint32(os.Geteuid()) {
		return fmt.Errorf("authentication token file owner %d is not current user %d", stat.Uid, os.Geteuid())
	}
	if info.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("authentication token file mode %o permits group or other access", info.Mode().Perm())
	}
	return nil
}
