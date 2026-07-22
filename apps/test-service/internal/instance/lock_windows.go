//go:build windows

package instance

import (
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

type windowsLock struct {
	handle windows.Handle
	once   sync.Once
	err    error
}

func lock(path string) (io.Closer, error) {
	owner, descriptor, err := ownerOnlyDescriptor(false)
	if err != nil {
		return nil, ErrLockUnavailable
	}
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, ErrLockUnavailable
	}
	attributes := &windows.SecurityAttributes{
		Length:             uint32(unsafe.Sizeof(windows.SecurityAttributes{})),
		SecurityDescriptor: descriptor,
	}
	handle, err := windows.CreateFile(
		name,
		windows.GENERIC_READ|windows.GENERIC_WRITE|windows.READ_CONTROL,
		0,
		attributes,
		windows.OPEN_ALWAYS,
		windows.FILE_ATTRIBUTE_NORMAL|windows.FILE_FLAG_OPEN_REPARSE_POINT,
		0,
	)
	if err != nil {
		if errors.Is(err, windows.ERROR_SHARING_VIOLATION) || errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
			return nil, ErrAlreadyRunning
		}
		return nil, ErrLockUnavailable
	}
	fail := func(result error) (io.Closer, error) {
		_ = windows.CloseHandle(handle)
		return nil, result
	}
	if err := validateOwnerOnlyFileHandle(handle, owner); err != nil {
		return fail(err)
	}
	var overlapped windows.Overlapped
	if err := windows.LockFileEx(handle, windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &overlapped); err != nil {
		if errors.Is(err, windows.ERROR_LOCK_VIOLATION) || errors.Is(err, windows.ERROR_SHARING_VIOLATION) {
			return fail(ErrAlreadyRunning)
		}
		return fail(ErrLockUnavailable)
	}
	return &windowsLock{handle: handle}, nil
}

func (l *windowsLock) Close() error {
	if l == nil {
		return nil
	}
	l.once.Do(func() {
		var overlapped windows.Overlapped
		unlockErr := windows.UnlockFileEx(l.handle, 0, 1, 0, &overlapped)
		closeErr := windows.CloseHandle(l.handle)
		if unlockErr != nil || closeErr != nil {
			l.err = ErrLockUnavailable
		}
	})
	return l.err
}

func ownerOnlyDescriptor(directory bool) (*windows.SID, *windows.SECURITY_DESCRIPTOR, error) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil || user == nil || user.User.Sid == nil {
		return nil, nil, ErrLockUnavailable
	}
	sid := user.User.Sid
	flags := ""
	if directory {
		flags = "OICI"
	}
	descriptor, err := windows.SecurityDescriptorFromString(fmt.Sprintf("O:%sD:P(A;%s;GA;;;%s)", sid.String(), flags, sid.String()))
	if err != nil {
		return nil, nil, ErrLockUnavailable
	}
	return sid, descriptor, nil
}

func validateOwnerOnlyFileHandle(handle windows.Handle, expectedOwner *windows.SID) error {
	var information windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &information); err != nil ||
		information.FileAttributes&(windows.FILE_ATTRIBUTE_DIRECTORY|windows.FILE_ATTRIBUTE_REPARSE_POINT) != 0 {
		return ErrLockUnavailable
	}
	return validateOwnerOnlySecurity(handle, expectedOwner)
}

func validateOwnerOnlySecurity(handle windows.Handle, expectedOwner *windows.SID) error {
	descriptor, err := windows.GetSecurityInfo(handle, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return ErrLockUnavailable
	}
	owner, _, err := descriptor.Owner()
	if err != nil || owner == nil || expectedOwner == nil || !owner.Equals(expectedOwner) {
		return ErrLockUnavailable
	}
	dacl, _, err := descriptor.DACL()
	if err != nil || dacl == nil {
		return ErrLockUnavailable
	}
	control, _, err := descriptor.Control()
	if err != nil || control&windows.SE_DACL_PROTECTED == 0 {
		return ErrLockUnavailable
	}
	if !ownerOnlyAllows(descriptor.String(), expectedOwner) {
		return ErrLockUnavailable
	}
	return nil
}

func ownerOnlyAllows(sddl string, expectedOwner *windows.SID) bool {
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
	for _, ace := range splitACEs(dacl[first:]) {
		fields := strings.Split(ace, ";")
		if len(fields) != 6 {
			return false
		}
		switch fields[0] {
		case "D", "OD", "XD":
			continue
		case "A", "OA", "XA", "ZA":
			if !trusteeMatches(fields[5], expectedOwner) {
				return false
			}
			allowedOwner = true
		default:
			return false
		}
	}
	return allowedOwner
}

func trusteeMatches(value string, expected *windows.SID) bool {
	descriptor, err := windows.SecurityDescriptorFromString("O:" + value)
	if err != nil {
		return false
	}
	actual, _, err := descriptor.Owner()
	return err == nil && actual != nil && expected != nil && actual.Equals(expected)
}

func splitACEs(value string) []string {
	var result []string
	for index := 0; index < len(value); {
		if value[index] != '(' {
			index++
			continue
		}
		start, depth := index+1, 1
		index++
		for index < len(value) && depth > 0 {
			switch value[index] {
			case '(':
				depth++
			case ')':
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
