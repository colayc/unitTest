//go:build linux

package cmake

import (
	"fmt"

	"golang.org/x/sys/unix"
)

func inspectPresetPathComponent(path string) (string, bool, error) {
	var status unix.Stat_t
	if err := unix.Lstat(path, &status); err != nil {
		return "", false, err
	}
	isLink := status.Mode&unix.S_IFMT == unix.S_IFLNK
	return fmt.Sprintf(
		"linux:%x:%x:%x:%d:%d:%d:%d:%d",
		uint64(status.Dev),
		status.Ino,
		status.Mode,
		status.Ctim.Sec,
		status.Ctim.Nsec,
		status.Mtim.Sec,
		status.Mtim.Nsec,
		status.Size,
	), isLink, nil
}
