//go:build !windows

package runtime

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

func prepareOwnerOnlyDirectory(absolute string) error {
	start := string(filepath.Separator)
	fd, err := unix.Open(start, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return ErrUnsafeDataDir
	}
	defer func() { _ = unix.Close(fd) }()
	segments := strings.FieldsFunc(strings.TrimPrefix(absolute, start), func(value rune) bool { return value == '/' })
	for _, segment := range segments {
		next, openErr := unix.Openat(fd, segment, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
		if errors.Is(openErr, unix.ENOENT) {
			if err := unix.Mkdirat(fd, segment, 0o700); err != nil {
				return ErrUnsafeDataDir
			}
			next, openErr = unix.Openat(fd, segment, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
		}
		if openErr != nil {
			return ErrUnsafeDataDir
		}
		if err := unix.Close(fd); err != nil {
			_ = unix.Close(next)
			return ErrUnsafeDataDir
		}
		fd = next
	}
	var status unix.Stat_t
	if err := unix.Fstat(fd, &status); err != nil || status.Mode&unix.S_IFMT != unix.S_IFDIR ||
		status.Uid != uint32(os.Geteuid()) || status.Mode&0o077 != 0 {
		return ErrUnsafeDataDir
	}
	return nil
}
