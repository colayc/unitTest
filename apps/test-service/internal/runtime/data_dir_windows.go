//go:build windows

package runtime

import (
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

type windowsDirectoryGuard struct {
	handles []windows.Handle
	once    sync.Once
	err     error
}

func pinOwnerOnlyDirectory(absolute string) (io.Closer, error) {
	return pinOwnerOnlyDirectoryWithOpen(absolute, openAbsoluteWindowsDirectory)
}

func pinOwnerOnlyDirectoryWithOpen(absolute string, openDirectory func(string, uint32, bool) (windows.Handle, error)) (io.Closer, error) {
	user, descriptor, err := runtimeOwnerDescriptor()
	if err != nil {
		return nil, err
	}
	volume := filepath.VolumeName(absolute)
	remainder := strings.TrimLeft(strings.TrimPrefix(absolute, volume), `\/`)
	current := volume + string(filepath.Separator)
	segments := strings.FieldsFunc(remainder, func(value rune) bool { return value == '\\' || value == '/' })
	guard := &windowsDirectoryGuard{}
	fail := func(cause error) (io.Closer, error) {
		_ = guard.Close()
		return nil, fmt.Errorf("pin data directory: %w", cause)
	}
	for segmentIndex, segment := range segments {
		current = filepath.Join(current, segment)
		final := segmentIndex == len(segments)-1
		handle, openErr := openDirectory(current, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, final)
		if errors.Is(openErr, windows.ERROR_FILE_NOT_FOUND) || errors.Is(openErr, windows.ERROR_PATH_NOT_FOUND) {
			security := &windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), SecurityDescriptor: descriptor}
			if err := windows.CreateDirectory(mustWindowsPath(current), security); err != nil && !errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
				return fail(fmt.Errorf("create segment: %w", err))
			}
			handle, openErr = openDirectory(current, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, final)
		}
		if openErr != nil {
			return fail(fmt.Errorf("open segment %d: %w", segmentIndex, openErr))
		}
		if validateErr := validateWindowsDirectoryObject(handle); validateErr != nil {
			_ = windows.CloseHandle(handle)
			return fail(fmt.Errorf("validate segment: %w", validateErr))
		}
		guard.handles = append(guard.handles, handle)
	}
	if len(guard.handles) == 0 {
		return fail(errors.New("missing data directory components"))
	}
	root := guard.handles[len(guard.handles)-1]
	if validateOwnerOnlyDirectoryHandle(root, user.User.Sid) != nil {
		return fail(errors.New("validate final directory"))
	}
	return guard, nil
}

func (g *windowsDirectoryGuard) Close() error {
	if g == nil {
		return nil
	}
	g.once.Do(func() {
		for index := len(g.handles) - 1; index >= 0; index-- {
			if err := windows.CloseHandle(g.handles[index]); err != nil {
				g.err = ErrUnsafeDataDir
			}
		}
		g.handles = nil
	})
	return g.err
}

func openAbsoluteWindowsDirectory(path string, share uint32, readControl bool) (windows.Handle, error) {
	access := uint32(0)
	if readControl {
		access = windows.FILE_READ_ATTRIBUTES | windows.READ_CONTROL
	}
	return windows.CreateFile(
		mustWindowsPath(path),
		access,
		share,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT,
		0,
	)
}

func validateWindowsDirectoryObject(handle windows.Handle) error {
	if handle == 0 || handle == windows.InvalidHandle {
		return ErrUnsafeDataDir
	}
	var information windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &information); err != nil {
		return fmt.Errorf("inspect directory handle: %w", err)
	}
	if information.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY == 0 || information.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return fmt.Errorf("unexpected directory attributes 0x%x", information.FileAttributes)
	}
	return nil
}

func validateOwnerOnlyDirectory(path string) error {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return ErrUnsafeDataDir
	}
	return validateOwnerOnlyDirectoryWithSID(path, user.User.Sid)
}

func validateOwnerOnlyDirectoryWithSID(path string, expected *windows.SID) error {
	handle, err := openAbsoluteWindowsDirectory(path, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, true)
	if err != nil {
		return ErrUnsafeDataDir
	}
	defer windows.CloseHandle(handle)
	return validateOwnerOnlyDirectoryHandle(handle, expected)
}

func validateOwnerOnlyDirectoryHandle(handle windows.Handle, expected *windows.SID) error {
	if err := validateWindowsDirectoryObject(handle); err != nil {
		return ErrUnsafeDataDir
	}
	descriptor, err := windows.GetSecurityInfo(handle, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return ErrUnsafeDataDir
	}
	owner, _, err := descriptor.Owner()
	if err != nil || owner == nil || expected == nil || !owner.Equals(expected) {
		return ErrUnsafeDataDir
	}
	dacl, _, err := descriptor.DACL()
	if err != nil || dacl == nil {
		return ErrUnsafeDataDir
	}
	control, _, err := descriptor.Control()
	if err != nil || control&windows.SE_DACL_PROTECTED == 0 || windowsACLHasInheritedACE(dacl) || !runtimeOwnerOnlyAllows(descriptor.String(), expected) {
		return ErrUnsafeDataDir
	}
	return nil
}

func windowsACLHasInheritedACE(acl *windows.ACL) bool {
	for index := uint32(0); index < uint32(acl.AceCount); index++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(acl, index, &ace); err != nil || ace == nil || ace.Header.AceFlags&windows.INHERITED_ACE != 0 {
			return true
		}
	}
	return false
}

func runtimeOwnerDescriptor() (*windows.Tokenuser, *windows.SECURITY_DESCRIPTOR, error) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil || user == nil || user.User.Sid == nil {
		return nil, nil, ErrUnsafeDataDir
	}
	sid := user.User.Sid.String()
	descriptor, err := windows.SecurityDescriptorFromString(fmt.Sprintf("O:%sD:P(A;OICI;GA;;;%s)", sid, sid))
	if err != nil {
		return nil, nil, ErrUnsafeDataDir
	}
	return user, descriptor, nil
}

func runtimeOwnerOnlyAllows(sddl string, expected *windows.SID) bool {
	start := strings.Index(sddl, "D:")
	if start < 0 {
		return false
	}
	dacl := sddl[start+2:]
	if sacl := strings.Index(dacl, "S:"); sacl >= 0 {
		dacl = dacl[:sacl]
	}
	first := strings.IndexByte(dacl, '(')
	if first < 0 {
		return false
	}
	allowedOwner := false
	for _, ace := range runtimeSplitACEs(dacl[first:]) {
		fields := strings.Split(ace, ";")
		if len(fields) != 6 {
			return false
		}
		switch fields[0] {
		case "D", "OD", "XD":
			continue
		case "A", "OA", "XA", "ZA":
			if strings.Contains(fields[1], "ID") {
				return false
			}
			descriptor, err := windows.SecurityDescriptorFromString("O:" + fields[5])
			if err != nil {
				return false
			}
			trustee, _, err := descriptor.Owner()
			if err != nil || trustee == nil || !trustee.Equals(expected) {
				return false
			}
			allowedOwner = true
		default:
			return false
		}
	}
	return allowedOwner
}

func runtimeSplitACEs(value string) []string {
	var result []string
	for index := 0; index < len(value); {
		if value[index] != '(' {
			index++
			continue
		}
		start, depth := index+1, 1
		index++
		for index < len(value) && depth > 0 {
			if value[index] == '(' {
				depth++
			} else if value[index] == ')' {
				depth--
			}
			index++
		}
		if depth == 0 {
			result = append(result, value[start:index-1])
		}
	}
	return result
}

func mustWindowsPath(path string) *uint16 {
	value, err := windows.UTF16PtrFromString(path)
	if err != nil {
		panic(err)
	}
	return value
}
