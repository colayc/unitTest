//go:build windows

package runtime

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

func prepareOwnerOnlyDirectory(absolute string) error {
	user, descriptor, err := runtimeOwnerDescriptor()
	if err != nil {
		return err
	}
	volume := filepath.VolumeName(absolute)
	remainder := strings.TrimLeft(strings.TrimPrefix(absolute, volume), `\/`)
	current := volume + string(filepath.Separator)
	for _, segment := range strings.FieldsFunc(remainder, func(value rune) bool { return value == '\\' || value == '/' }) {
		current = filepath.Join(current, segment)
		attributes, attributeErr := windows.GetFileAttributes(mustWindowsPath(current))
		switch {
		case attributeErr == nil:
			if attributes&windows.FILE_ATTRIBUTE_DIRECTORY == 0 || attributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
				return ErrUnsafeDataDir
			}
		case errors.Is(attributeErr, windows.ERROR_FILE_NOT_FOUND), errors.Is(attributeErr, windows.ERROR_PATH_NOT_FOUND):
			security := &windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), SecurityDescriptor: descriptor}
			if err := windows.CreateDirectory(mustWindowsPath(current), security); err != nil && !errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
				return ErrUnsafeDataDir
			}
		default:
			return ErrUnsafeDataDir
		}
	}
	return validateOwnerOnlyDirectoryWithSID(absolute, user.User.Sid)
}

func validateOwnerOnlyDirectory(path string) error {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return ErrUnsafeDataDir
	}
	return validateOwnerOnlyDirectoryWithSID(path, user.User.Sid)
}

func validateOwnerOnlyDirectoryWithSID(path string, expected *windows.SID) error {
	handle, err := windows.CreateFile(
		mustWindowsPath(path),
		windows.FILE_LIST_DIRECTORY|windows.READ_CONTROL,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT,
		0,
	)
	if err != nil {
		return ErrUnsafeDataDir
	}
	defer windows.CloseHandle(handle)
	var information windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &information); err != nil ||
		information.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY == 0 ||
		information.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
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
	if err != nil || control&windows.SE_DACL_PROTECTED == 0 || !runtimeOwnerOnlyAllows(descriptor.String(), expected) {
		return ErrUnsafeDataDir
	}
	return nil
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
