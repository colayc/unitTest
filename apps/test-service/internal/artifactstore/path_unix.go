//go:build !windows

package artifactstore

import (
	"errors"
	"os"
	"syscall"
)

func pathEntryIsLink(_ string, info os.FileInfo) (bool, error) {
	return isLinkInfo(info), nil
}

func isLinkInfo(info os.FileInfo) bool {
	return info.Mode()&os.ModeSymlink != 0
}

func syncDirectoryHandle(directory *os.File) error {
	if err := directory.Sync(); err != nil {
		return ErrStoreUnavailable
	}
	return nil
}

func isDirectoryNotEmpty(err error) bool {
	return errors.Is(err, syscall.ENOTEMPTY) || errors.Is(err, syscall.EEXIST)
}
