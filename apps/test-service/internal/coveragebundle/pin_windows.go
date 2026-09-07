//go:build windows

package coveragebundle

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"unsafe"

	"golang.org/x/sys/windows"
)

func cleanupAuthorityAvailable() bool { return true }

var cleanupPreflightSequence uint64
var descriptorTempSequence uint64

// preflightPinnedCleanupAuthority proves that the service can obtain DELETE
// authority in the retained collector before any task child is created. The
// probe is DELETE_ON_CLOSE, so success leaves no entry and failure occurs
// before gcovr or a descriptor temporary exists.
func preflightPinnedCleanupAuthority(directory *VerifiedDirectory, parent *pinnedObject) error {
	if directory == nil || parent == nil || parent.path != directory.Path() {
		return errors.New("invalid cleanup collector")
	}
	if err := directory.Verify(); err != nil {
		return err
	}
	if err := parent.verifyIdentity(); err != nil {
		return err
	}
	return preflightPinnedCleanupDirectory(parent)
}

func preflightPinnedCleanupDirectory(parent *pinnedObject) error {
	name := fmt.Sprintf(".coverage-directory-delete-preflight-%d", atomic.AddUint64(&cleanupPreflightSequence, 1))
	if err := mkdirPinnedChild(parent, name, 0o700); err != nil {
		return err
	}
	child, err := acquireCleanupDirectoryPin(parent, name)
	if err != nil {
		return errors.Join(err, removeFreshPinnedDirectory(parent, name))
	}
	return errors.Join(removePinnedChild(parent, child, name), child.Close(), syncPinnedDirectory(parent))
}

func removeFreshPinnedDirectory(parent *pinnedObject, name string) error {
	if parent == nil || name == "" || filepath.Base(name) != name {
		return errors.New("invalid fresh directory cleanup")
	}
	if err := parent.verifyIdentity(); err != nil {
		return err
	}
	return os.Remove(filepath.Join(parent.path, name))
}

func mkdirPinnedChild(parent *pinnedObject, name string, mode uint32) error {
	if err := parent.verifyIdentity(); err != nil {
		return err
	}
	if err := os.Mkdir(filepath.Join(parent.path, name), os.FileMode(mode)); err != nil {
		return err
	}
	return parent.verifyIdentity()
}

func createPinnedTemp(parent *pinnedObject, prefix string) (*os.File, string, error) {
	if err := parent.verifyIdentity(); err != nil {
		return nil, "", err
	}
	for attempt := 0; attempt < 32; attempt++ {
		name := fmt.Sprintf("%s-%d.tmp", prefix, atomic.AddUint64(&descriptorTempSequence, 1))
		path := filepath.Join(parent.path, name)
		utf16, err := windows.UTF16PtrFromString(path)
		if err != nil {
			return nil, "", err
		}
		handle, err := windows.CreateFile(utf16, windows.GENERIC_READ|windows.GENERIC_WRITE|windows.DELETE,
			windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
			nil, windows.CREATE_NEW, windows.FILE_ATTRIBUTE_NORMAL, 0)
		if err == windows.ERROR_FILE_EXISTS {
			continue
		}
		if err != nil {
			return nil, "", err
		}
		file := os.NewFile(uintptr(handle), path)
		if file == nil {
			_ = windows.CloseHandle(handle)
			return nil, "", errors.New("construct descriptor temporary")
		}
		if err := parent.verifyIdentity(); err != nil {
			_ = file.Close()
			return nil, "", err
		}
		return file, name, nil
	}
	return nil, "", errors.New("unable to allocate descriptor temporary")
}

func duplicatePinnedTemporary(parent *pinnedObject, original *os.File, name string) (*pinnedObject, error) {
	if parent == nil || original == nil || name == "" || filepath.Base(name) != name {
		return nil, errors.New("invalid temporary pin")
	}
	var duplicate windows.Handle
	if err := windows.DuplicateHandle(windows.CurrentProcess(), windows.Handle(original.Fd()), windows.CurrentProcess(), &duplicate, 0, false, windows.DUPLICATE_SAME_ACCESS); err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(duplicate), filepath.Join(parent.path, name))
	if file == nil {
		_ = windows.CloseHandle(duplicate)
		return nil, errors.New("duplicate descriptor temporary")
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, err
	}
	return pinOpenedObject(filepath.Join(parent.path, name), false, info, file)
}

func removeCreatedTemporary(parent *pinnedObject, original *os.File, name string) error {
	if parent == nil || original == nil || name == "" || filepath.Base(name) != name {
		return errors.New("invalid created temporary cleanup")
	}
	if err := parent.verifyIdentity(); err != nil {
		return err
	}
	info := fileDispositionInfo{DeleteFile: 1}
	return windows.SetFileInformationByHandle(windows.Handle(original.Fd()), windows.FileDispositionInfo, (*byte)(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info)))
}

func renamePinnedChild(parent *pinnedObject, oldName, newName string) error {
	if err := parent.verifyIdentity(); err != nil {
		return err
	}
	if err := os.Rename(filepath.Join(parent.path, oldName), filepath.Join(parent.path, newName)); err != nil {
		return err
	}
	return parent.verifyIdentity()
}

func removePinnedChild(parent, child *pinnedObject, name string) error {
	if parent == nil || child == nil || name == "" || filepath.Base(name) != name {
		return errors.New("invalid pinned child removal")
	}
	if err := parent.verifyIdentity(); err != nil {
		return err
	}
	if err := child.verifyIdentity(); err != nil {
		return err
	}
	info := fileDispositionInfo{DeleteFile: 1}
	if err := windows.SetFileInformationByHandle(windows.Handle(child.file.Fd()), windows.FileDispositionInfo, (*byte)(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		return err
	}
	return parent.verifyIdentity()
}

type fileDispositionInfo struct{ DeleteFile byte }

func syncPinnedDirectory(parent *pinnedObject) error {
	if err := parent.file.Sync(); err != nil {
		// Windows does not expose directory metadata flush through an ordinary
		// directory handle; the retained handle and post-operation identity
		// verification remain the durability boundary there.
		if errors.Is(err, windows.ERROR_ACCESS_DENIED) {
			return nil
		}
		return err
	}
	return nil
}

func openPinnedRegular(path string) (*os.File, error) {
	return openPinnedWindowsObject(path, false)
}

func openDescriptorOutput(path string) (*os.File, error) {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	handle, err := windows.CreateFile(name, windows.GENERIC_READ,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return nil, err
	}
	var information windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &information); err != nil {
		_ = windows.CloseHandle(handle)
		return nil, err
	}
	if information.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY != 0 || information.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		_ = windows.CloseHandle(handle)
		return nil, errors.New("opened output has unsafe attributes")
	}
	return os.NewFile(uintptr(handle), path), nil
}

func openPinnedDirectory(path string) (*os.File, error) {
	return openPinnedWindowsObject(path, true)
}

func openPinnedChild(parent *pinnedObject, name string, directory bool) (*os.File, error) {
	return openPinnedWindowsObject(filepath.Join(parent.path, name), directory)
}

func openPinnedChildForDelete(parent *pinnedObject, name string, directory bool) (*os.File, error) {
	return openPinnedWindowsObjectWithDelete(filepath.Join(parent.path, name), directory, true)
}

func openPinnedOutputChild(parent *pinnedObject, name string) (*os.File, error) {
	path := filepath.Join(parent.path, name)
	utf16, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	handle, err := windows.CreateFile(utf16, windows.GENERIC_READ,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(handle), path), nil
}

func openPinnedDirectoryReader(parent *pinnedObject) (*os.File, error) {
	if parent == nil || parent.file == nil || !parent.directory {
		return nil, errors.New("invalid pinned directory reader")
	}
	var duplicate windows.Handle
	if err := windows.DuplicateHandle(
		windows.CurrentProcess(), windows.Handle(parent.file.Fd()),
		windows.CurrentProcess(), &duplicate, 0, false, windows.DUPLICATE_SAME_ACCESS,
	); err != nil {
		return nil, err
	}
	reader := os.NewFile(uintptr(duplicate), parent.path)
	if reader == nil {
		_ = windows.CloseHandle(duplicate)
		return nil, errors.New("duplicate pinned directory reader")
	}
	return reader, nil
}

func pinnedChildDirectory(parent *pinnedObject, name string) (bool, error) {
	info, err := directObjectInfo(filepath.Join(parent.path, name))
	if err != nil {
		return false, err
	}
	return info.IsDir(), nil
}

func pinProductRootAncestors(absolute string) ([]*pinnedObject, error) {
	volume := filepath.VolumeName(absolute)
	if volume == "" {
		return nil, errors.New("product root has no volume")
	}
	root := volume + string(filepath.Separator)
	remainder := strings.TrimLeft(strings.TrimPrefix(absolute, volume), `\/`)
	segments := strings.FieldsFunc(remainder, func(value rune) bool { return value == '\\' || value == '/' })
	if len(segments) > maximumProductRootComponents {
		return nil, errors.New("product root component budget exceeded")
	}
	rootPin, err := pinDirectObject(root, true)
	if err != nil {
		return nil, fmt.Errorf("pin volume root %q: %w", root, err)
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
			return fail(fmt.Errorf("pin product ancestor %q: %w", filepath.Join(parent.path, segment), err))
		}
		pins = append(pins, child)
		parent = child
	}
	return pins, nil
}

func openPinnedWindowsObject(path string, directory bool) (*os.File, error) {
	return openPinnedWindowsObjectWithDelete(path, directory, false)
}

func openPinnedWindowsObjectWithDelete(path string, directory, deleteAccess bool) (*os.File, error) {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	access := uint32(windows.GENERIC_READ)
	flags := uint32(windows.FILE_ATTRIBUTE_NORMAL | windows.FILE_FLAG_OPEN_REPARSE_POINT)
	share := uint32(windows.FILE_SHARE_READ | windows.FILE_SHARE_DELETE)
	if directory {
		access = windows.FILE_READ_ATTRIBUTES | windows.FILE_LIST_DIRECTORY
		flags = windows.FILE_FLAG_BACKUP_SEMANTICS
		share |= windows.FILE_SHARE_WRITE
	}
	if deleteAccess {
		// A child created and later cleaned by the descriptor must carry DELETE
		// access from the time it is pinned; cleanup never reopens by pathname.
		access |= windows.DELETE
	}
	handle, err := windows.CreateFile(name, access, share, nil, windows.OPEN_EXISTING, flags, 0)
	if err != nil {
		return nil, err
	}
	var information windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &information); err != nil {
		_ = windows.CloseHandle(handle)
		return nil, err
	}
	wantDirectory := information.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY != 0
	if wantDirectory != directory || information.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		_ = windows.CloseHandle(handle)
		return nil, errors.New("opened bundle object has unsafe attributes")
	}
	file := os.NewFile(uintptr(handle), path)
	if file == nil {
		_ = windows.CloseHandle(handle)
		return nil, errors.New("construct bundle pin")
	}
	return file, nil
}
