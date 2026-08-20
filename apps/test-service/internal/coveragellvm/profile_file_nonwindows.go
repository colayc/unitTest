//go:build !windows

package coveragellvm

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
)

type sealedFileIdentity struct {
	device uint64
	inode  uint64
}

func openSealedProfile(
	path string,
) (*os.File, os.FileInfo, sealedFileIdentity, error) {
	before, err := os.Lstat(path)
	if err != nil || !before.Mode().IsRegular() ||
		before.Mode()&os.ModeSymlink != 0 || profileLinkCount(before) != 1 {
		return nil, nil, sealedFileIdentity{}, errors.New("profile is indirect or multiply linked")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, nil, sealedFileIdentity{}, err
	}
	fail := func(cause error) (*os.File, os.FileInfo, sealedFileIdentity, error) {
		_ = file.Close()
		return nil, nil, sealedFileIdentity{}, cause
	}
	current, err := file.Stat()
	if err != nil || !current.Mode().IsRegular() ||
		!os.SameFile(before, current) || profileLinkCount(current) != 1 {
		return fail(errors.New("profile identity changed while opening"))
	}
	identity, ok := unixProfileIdentity(current)
	if !ok {
		return fail(errors.New("profile identity unavailable"))
	}
	if err := verifySealedProfile(path, file, current, identity); err != nil {
		return fail(err)
	}
	return file, current, identity, nil
}

func verifySealedProfile(
	path string,
	file *os.File,
	info os.FileInfo,
	identity sealedFileIdentity,
) error {
	if file == nil || info == nil || rejectProfileLinkAncestors(path) != nil {
		return errors.New("profile snapshot is closed")
	}
	before, err := os.Lstat(path)
	beforeIdentity, ok := unixProfileIdentity(before)
	if err != nil || !ok || !before.Mode().IsRegular() ||
		before.Mode()&os.ModeSymlink != 0 || beforeIdentity != identity ||
		profileLinkCount(before) != 1 {
		return errors.New("profile path identity changed")
	}
	current, err := file.Stat()
	currentIdentity, ok := unixProfileIdentity(current)
	if err != nil || !ok || currentIdentity != identity ||
		!os.SameFile(info, current) || current.Size() != info.Size() ||
		profileLinkCount(current) != 1 {
		return errors.New("profile handle identity changed")
	}
	after, err := os.Lstat(path)
	afterIdentity, ok := unixProfileIdentity(after)
	if err != nil || !ok || afterIdentity != identity ||
		!os.SameFile(info, after) || profileLinkCount(after) != 1 {
		return errors.New("profile changed while validating")
	}
	return nil
}

func rejectProfileLinkAncestors(path string) error {
	for current := filepath.Clean(path); ; current = filepath.Dir(current) {
		info, err := os.Lstat(current)
		if err != nil || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("profile ancestry crosses a symbolic link")
		}
		next := filepath.Dir(current)
		if next == current {
			return nil
		}
	}
}

func unixProfileIdentity(info os.FileInfo) (sealedFileIdentity, bool) {
	if info == nil {
		return sealedFileIdentity{}, false
	}
	status, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return sealedFileIdentity{}, false
	}
	return sealedFileIdentity{
		device: uint64(status.Dev),
		inode:  uint64(status.Ino),
	}, true
}

func profileLinkCount(info os.FileInfo) uint64 {
	if info == nil {
		return 0
	}
	status, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0
	}
	return uint64(status.Nlink)
}
