//go:build !windows

package coveragebundle

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"

	"golang.org/x/sys/unix"
)

var descriptorTempSequence uint64
var cleanupPreflightSequence uint64

// unixBeforePinnedUnlink exists only to make the identity recheck race
// deterministic in the Unix regression test. Production leaves it nil.
var unixBeforePinnedUnlink func()

// cleanupAuthorityAvailable is safe only under the private directory model
// enforced by preflightPinnedCleanupAuthority and removePinnedChild. POSIX
// unlinkat names an entry, so no untrusted actor may be able to replace a
// child between the retained-identity check and relative unlinkat.
func cleanupAuthorityAvailable() bool { return true }

func preflightPinnedCleanupAuthority(directory *VerifiedDirectory, parent *pinnedObject) error {
	if directory == nil || parent == nil || parent.path != directory.Path() {
		return errors.New("invalid private cleanup collector")
	}
	if err := directory.Verify(); err != nil {
		return err
	}
	if err := verifyPrivateUnixDirectory(parent); err != nil {
		return fmt.Errorf("private collector directory: %w", err)
	}
	for _, ancestor := range directory.pins {
		if ancestor.path == directory.path {
			continue
		}
		if err := verifyPrivateUnixAncestor(ancestor); err != nil {
			return fmt.Errorf("private collector ancestor %q: %w", ancestor.path, err)
		}
	}
	return preflightPinnedCleanupDirectory(parent)
}

func verifyPrivateUnixDirectory(directory *pinnedObject) error {
	if directory == nil || directory.file == nil || !directory.directory {
		return errors.New("invalid private cleanup directory")
	}
	var status unix.Stat_t
	if err := unix.Fstat(int(directory.file.Fd()), &status); err != nil {
		return err
	}
	if err := validatePrivateUnixDirectoryStatus(status, uint32(os.Geteuid())); err != nil {
		return err
	}
	return directory.verifyIdentity()
}

func validatePrivateUnixDirectoryStatus(status unix.Stat_t, currentUID uint32) error {
	if status.Mode&unix.S_IFMT != unix.S_IFDIR || status.Mode&0o777 != 0o700 {
		return errors.New("directory is not private mode 0700")
	}
	if status.Uid != currentUID {
		return errors.New("directory is not owned by current service user")
	}
	return nil
}

func verifyPrivateUnixAncestor(directory *pinnedObject) error {
	if directory == nil || directory.file == nil || !directory.directory {
		return errors.New("invalid private cleanup ancestor")
	}
	var status unix.Stat_t
	if err := unix.Fstat(int(directory.file.Fd()), &status); err != nil {
		return err
	}
	if status.Mode&unix.S_IFMT != unix.S_IFDIR || status.Mode&0o022 != 0 {
		return errors.New("ancestor permits group or other writes")
	}
	return directory.verifyIdentity()
}

func preflightPinnedCleanupDirectory(parent *pinnedObject) error {
	name := fmt.Sprintf(".coverage-directory-delete-preflight-%d", atomic.AddUint64(&cleanupPreflightSequence, 1))
	if err := mkdirPinnedChild(parent, name, 0o700); err != nil {
		return err
	}
	creationChild, err := pinChildObject(parent, name, true)
	if err != nil {
		return err
	}
	child, err := acquireCleanupDirectoryPin(parent, name)
	if err != nil {
		return errors.Join(err, removeFreshPinnedDirectory(parent, name, creationChild), creationChild.Close())
	}
	return errors.Join(removePinnedChild(parent, child, name), child.Close(), creationChild.Close(), syncPinnedDirectory(parent))
}

func removeFreshPinnedDirectory(parent *pinnedObject, name string, expected *pinnedObject) error {
	if parent == nil || expected == nil || name == "" || filepath.Base(name) != name {
		return errors.New("invalid fresh directory cleanup")
	}
	if err := verifyPrivateUnixDirectory(parent); err != nil {
		return err
	}
	if err := expected.verifyIdentity(); err != nil {
		return err
	}
	return unix.Unlinkat(int(parent.file.Fd()), name, unix.AT_REMOVEDIR)
}

func mkdirPinnedChild(parent *pinnedObject, name string, mode uint32) error {
	if err := verifyPrivateUnixDirectory(parent); err != nil {
		return err
	}
	return unix.Mkdirat(int(parent.file.Fd()), name, mode)
}

func createPinnedTemp(parent *pinnedObject, prefix string) (*os.File, string, error) {
	if err := verifyPrivateUnixDirectory(parent); err != nil {
		return nil, "", err
	}
	for attempt := 0; attempt < 32; attempt++ {
		name := fmt.Sprintf("%s-%d.tmp", prefix, atomic.AddUint64(&descriptorTempSequence, 1))
		fd, err := unix.Openat(int(parent.file.Fd()), name, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_CLOEXEC, 0o600)
		if err == unix.EEXIST {
			continue
		}
		if err != nil {
			return nil, "", err
		}
		return os.NewFile(uintptr(fd), filepath.Join(parent.path, name)), name, nil
	}
	return nil, "", errors.New("unable to allocate descriptor temporary")
}

func duplicatePinnedTemporary(parent *pinnedObject, original *os.File, name string) (*pinnedObject, error) {
	if parent == nil || original == nil || name == "" || filepath.Base(name) != name {
		return nil, errors.New("invalid temporary pin")
	}
	fd, err := unix.Dup(int(original.Fd()))
	if err != nil {
		return nil, err
	}
	duplicate := os.NewFile(uintptr(fd), filepath.Join(parent.path, name))
	if duplicate == nil {
		_ = unix.Close(fd)
		return nil, errors.New("duplicate temporary pin")
	}
	info, err := duplicate.Stat()
	if err != nil {
		_ = duplicate.Close()
		return nil, err
	}
	return pinOpenedObject(filepath.Join(parent.path, name), false, info, duplicate)
}

func removeCreatedTemporary(parent *pinnedObject, original *os.File, name string) error {
	if parent == nil || original == nil || name == "" || filepath.Base(name) != name {
		return errors.New("invalid created temporary cleanup")
	}
	if err := verifyPrivateUnixDirectory(parent); err != nil {
		return err
	}
	return unix.Unlinkat(int(parent.file.Fd()), name, 0)
}

func renamePinnedChild(parent *pinnedObject, oldName, newName string) error {
	if err := verifyPrivateUnixDirectory(parent); err != nil {
		return err
	}
	return unix.Renameat(int(parent.file.Fd()), oldName, int(parent.file.Fd()), newName)
}

func removePinnedChild(parent, child *pinnedObject, name string) error {
	if parent == nil || child == nil || name == "" || filepath.Base(name) != name {
		return errors.New("invalid pinned child removal")
	}
	if err := verifyPrivateUnixDirectory(parent); err != nil {
		return fmt.Errorf("private cleanup parent: %w", err)
	}
	if err := child.verifyIdentity(); err != nil {
		return fmt.Errorf("verify cleanup child: %w", err)
	}
	if unixBeforePinnedUnlink != nil {
		unixBeforePinnedUnlink()
	}
	// The private 0700 parent precondition means only this service can mutate
	// direct children. Rechecking after the testable interleave rejects any
	// observed replacement before unlinkat names an entry.
	if err := child.verifyIdentity(); err != nil {
		return fmt.Errorf("verify cleanup child before unlink: %w", err)
	}
	flags := 0
	if child.directory {
		flags = unix.AT_REMOVEDIR
	}
	if err := unix.Unlinkat(int(parent.file.Fd()), name, flags); err != nil {
		return err
	}
	return parent.verifyIdentity()
}

func syncPinnedDirectory(parent *pinnedObject) error {
	return unix.Fsync(int(parent.file.Fd()))
}

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

func openPinnedChildForDelete(parent *pinnedObject, name string, directory bool) (*os.File, error) {
	return openPinnedChild(parent, name, directory)
}

func openPinnedOutputChild(parent *pinnedObject, name string) (*os.File, error) {
	return openPinnedChild(parent, name, false)
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
