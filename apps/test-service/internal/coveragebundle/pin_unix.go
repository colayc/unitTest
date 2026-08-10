//go:build !windows

package coveragebundle

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

func openPinnedRegular(path string) (*os.File, error) {
	return openPinnedUnixObject(path, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW)
}

func openDescriptorOutput(path string) (*os.File, error) {
	return openPinnedRegular(path)
}

func openPinnedDirectory(path string) (*os.File, error) {
	return openPinnedUnixObject(path, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW)
}

func openPinnedChild(parent *pinnedObject, name string, directory bool) (*os.File, error) {
	flags := unix.O_RDONLY | unix.O_CLOEXEC | unix.O_NOFOLLOW
	if directory {
		flags |= unix.O_DIRECTORY
	}
	return openPinnedUnixObjectAt(int(parent.file.Fd()), name, flags, filepath.Join(parent.path, name))
}

func openPinnedDirectoryReader(parent *pinnedObject) (*os.File, error) {
	return openPinnedUnixObjectAt(
		int(parent.file.Fd()), ".",
		unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW,
		parent.path,
	)
}

func pinnedChildDirectory(parent *pinnedObject, name string) (bool, error) {
	var status unix.Stat_t
	if err := unix.Fstatat(int(parent.file.Fd()), name, &status, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return false, err
	}
	switch status.Mode & unix.S_IFMT {
	case unix.S_IFDIR:
		return true, nil
	case unix.S_IFREG:
		return false, nil
	default:
		return false, errors.New("bundle child is not a direct regular object")
	}
}

func pinProductRootAncestors(absolute string) ([]*pinnedObject, error) {
	root := string(filepath.Separator)
	segments := strings.FieldsFunc(strings.TrimPrefix(absolute, root), func(value rune) bool { return value == '/' })
	if len(segments) > maximumProductRootComponents {
		return nil, errors.New("product root component budget exceeded")
	}
	rootPin, err := pinDirectObject(root, true)
	if err != nil {
		return nil, err
	}
	pins := []*pinnedObject{rootPin}
	fail := func(cause error) ([]*pinnedObject, error) {
		for index := len(pins) - 1; index >= 0; index-- {
			_ = pins[index].Close()
		}
		return nil, cause
	}
	parent := rootPin
	for _, segment := range segments {
		child, err := pinChildObject(parent, segment, true)
		if err != nil {
			return fail(err)
		}
		pins = append(pins, child)
		parent = child
	}
	return pins, nil
}

func openPinnedUnixObject(path string, flags int) (*os.File, error) {
	descriptor, err := unix.Open(path, flags, 0)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(descriptor), path)
	if file == nil {
		_ = unix.Close(descriptor)
		return nil, errors.New("construct bundle pin")
	}
	return file, nil
}

func openPinnedUnixObjectAt(parent int, name string, flags int, displayPath string) (*os.File, error) {
	descriptor, err := unix.Openat(parent, name, flags, 0)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(descriptor), displayPath)
	if file == nil {
		_ = unix.Close(descriptor)
		return nil, errors.New("construct relative bundle pin")
	}
	return file, nil
}
