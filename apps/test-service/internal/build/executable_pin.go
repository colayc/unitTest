package build

import (
	"errors"
	"os"
)

func pinExecutable(path string) (*os.File, os.FileInfo, error) {
	before, err := directExecutableInfo(path)
	if err != nil {
		return nil, nil, err
	}
	file, err := openPinnedExecutable(path)
	if err != nil {
		return nil, nil, err
	}
	fail := func(cause error) (*os.File, os.FileInfo, error) {
		_ = file.Close()
		return nil, nil, cause
	}
	info, err := file.Stat()
	if err != nil {
		return fail(err)
	}
	if !info.Mode().IsRegular() || !os.SameFile(before, info) {
		return fail(errors.New("executable identity changed while pinning"))
	}
	after, err := directExecutableInfo(path)
	if err != nil || !os.SameFile(info, after) {
		return fail(errors.New("executable path changed while pinning"))
	}
	return file, info, nil
}

func validatePinnedExecutable(
	file *os.File,
	expected os.FileInfo,
	path string,
) error {
	if file == nil || expected == nil {
		return errors.New("executable pin is closed")
	}
	before, err := directExecutableInfo(path)
	if err != nil || !os.SameFile(expected, before) {
		return errors.New("executable path identity changed")
	}
	current, err := file.Stat()
	if err != nil || !current.Mode().IsRegular() ||
		!os.SameFile(expected, current) {
		return errors.New("executable handle identity changed")
	}
	after, err := directExecutableInfo(path)
	if err != nil || !os.SameFile(expected, after) {
		return errors.New("executable path changed while validating")
	}
	return nil
}

func directExecutableInfo(path string) (os.FileInfo, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("executable is not a direct regular file")
	}
	return info, nil
}
