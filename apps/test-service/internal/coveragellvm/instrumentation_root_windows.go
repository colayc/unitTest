//go:build windows

package coveragellvm

import (
	"errors"
	"os"
	"strings"

	"golang.org/x/sys/windows"
)

func pinInstrumentationRoot(path string) (*instrumentationRootPin, error) {
	if err := rejectReparseAncestors(path); err != nil {
		return nil, err
	}
	file, err := openWindowsObject(path, true, windows.FILE_READ_ATTRIBUTES|windows.READ_CONTROL)
	if err != nil {
		return nil, err
	}
	info, err := file.Stat()
	if err != nil || !info.IsDir() {
		_ = file.Close()
		return nil, errors.New("instrumentation root is not a directory")
	}
	native, err := identityFromHandle(windows.Handle(file.Fd()))
	if err != nil || native.attrs&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 ||
		validateOwnerOnlyInstrumentationRoot(windows.Handle(file.Fd())) != nil {
		_ = file.Close()
		return nil, errors.New("instrumentation root is not owner-only")
	}
	pin := &instrumentationRootPin{path: path, file: file, info: info, native: native}
	if err := verifyInstrumentationRoot(pin); err != nil {
		_ = file.Close()
		return nil, err
	}
	return pin, nil
}

func verifyInstrumentationRoot(pin *instrumentationRootPin) error {
	if pin == nil || pin.file == nil || pin.info == nil {
		return errors.New("instrumentation root pin is closed")
	}
	if err := rejectReparseAncestors(pin.path); err != nil {
		return err
	}
	before, err := windowsPathIdentity(pin.path, true)
	if err != nil || before != pin.native {
		return errors.New("instrumentation root path changed")
	}
	current, err := identityFromHandle(windows.Handle(pin.file.Fd()))
	if err != nil || current != pin.native || validateOwnerOnlyInstrumentationRoot(windows.Handle(pin.file.Fd())) != nil {
		return errors.New("instrumentation root handle changed")
	}
	info, err := pin.file.Stat()
	if err != nil || !info.IsDir() || !os.SameFile(pin.info, info) {
		return errors.New("instrumentation root information changed")
	}
	after, err := windowsPathIdentity(pin.path, true)
	if err != nil || after != current {
		return errors.New("instrumentation root changed while validating")
	}
	return rejectReparseAncestors(pin.path)
}

func validateOwnerOnlyInstrumentationRoot(handle windows.Handle) error {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil || user == nil || user.User.Sid == nil {
		return ErrInvalidToolset
	}
	descriptor, err := windows.GetSecurityInfo(
		handle, windows.SE_FILE_OBJECT,
		windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION,
	)
	if err != nil {
		return ErrInvalidToolset
	}
	owner, _, err := descriptor.Owner()
	if err != nil || owner == nil || !owner.Equals(user.User.Sid) {
		return ErrInvalidToolset
	}
	dacl, _, err := descriptor.DACL()
	if err != nil || dacl == nil {
		return ErrInvalidToolset
	}
	control, _, err := descriptor.Control()
	if err != nil || control&windows.SE_DACL_PROTECTED == 0 || instrumentationACLHasInheritedACE(dacl) ||
		!instrumentationOwnerOnlyAllows(descriptor.String(), user.User.Sid) {
		return ErrInvalidToolset
	}
	return nil
}

func instrumentationACLHasInheritedACE(acl *windows.ACL) bool {
	for index := uint32(0); index < uint32(acl.AceCount); index++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(acl, index, &ace); err != nil || ace == nil || ace.Header.AceFlags&windows.INHERITED_ACE != 0 {
			return true
		}
	}
	return false
}

func instrumentationOwnerOnlyAllows(sddl string, expected *windows.SID) bool {
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
	for _, ace := range splitInstrumentationACEs(dacl[first:]) {
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

func splitInstrumentationACEs(value string) []string {
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
