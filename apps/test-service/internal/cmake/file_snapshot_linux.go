//go:build linux

package cmake

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

func openStableFile(path string) (*os.File, error) {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fd), path)
	if file == nil {
		_ = unix.Close(fd)
		return nil, errors.New("construct file from descriptor")
	}
	return file, nil
}

func stableFileOSIdentity(file *os.File) (string, error) {
	var stat unix.Stat_t
	if err := unix.Fstat(int(file.Fd()), &stat); err != nil {
		return "", err
	}
	if stat.Dev == 0 || stat.Ino == 0 {
		return "", errors.New("filesystem did not provide device/inode identity")
	}
	return fmt.Sprintf("linux:%x:%x", uint64(stat.Dev), stat.Ino), nil
}
