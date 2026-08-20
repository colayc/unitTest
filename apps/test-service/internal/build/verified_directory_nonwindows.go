//go:build !windows

package build

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
)

type directoryNativeIdentity struct{}

func pinVerifiedDirectory(path string) (*verifiedDirectory, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("invalid directory")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	handleInfo, err := file.Stat()
	if err != nil || !os.SameFile(info, handleInfo) {
		_ = file.Close()
		return nil, errors.New("directory identity changed")
	}
	sum := sha256.Sum256([]byte("coverage-directory-v1\x00" + filepath.Clean(path)))
	return &verifiedDirectory{path: path, identity: hex.EncodeToString(sum[:]), file: file, info: info}, nil
}

func verifyDirectory(directory *verifiedDirectory) error {
	if directory.file == nil {
		return errors.New("directory pin is closed")
	}
	pathInfo, err := os.Lstat(directory.path)
	if err != nil || !pathInfo.IsDir() || pathInfo.Mode()&os.ModeSymlink != 0 {
		return errors.New("invalid directory path")
	}
	handleInfo, err := directory.file.Stat()
	if err != nil || !os.SameFile(directory.info, pathInfo) || !os.SameFile(directory.info, handleInfo) {
		return errors.New("directory identity changed")
	}
	return nil
}

func rejectDirectoryReparseAncestors(path string) error {
	for current := filepath.Clean(path); ; current = filepath.Dir(current) {
		info, err := os.Lstat(current)
		if err != nil || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("directory ancestry is invalid")
		}
		next := filepath.Dir(current)
		if next == current {
			return nil
		}
	}
}
