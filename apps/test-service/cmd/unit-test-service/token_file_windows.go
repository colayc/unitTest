//go:build windows

package main

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"golang.org/x/sys/windows"
)

func openTokenFile(path string) (*os.File, error) {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	handle, err := windows.CreateFile(
		name,
		windows.GENERIC_READ,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_DELETE,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_ATTRIBUTE_NORMAL|windows.FILE_FLAG_OPEN_REPARSE_POINT,
		0,
	)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(handle), path), nil
}

func validateTokenFile(file *os.File, _ os.FileInfo) error {
	sd, err := windows.GetSecurityInfo(
		windows.Handle(file.Fd()),
		windows.SE_FILE_OBJECT,
		windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION,
	)
	if err != nil {
		return err
	}
	owner, _, err := sd.Owner()
	if err != nil {
		return err
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return err
	}
	if owner == nil || !owner.Equals(user.User.Sid) {
		return errors.New("authentication token file is not owned by the current user")
	}
	dacl, _, err := sd.DACL()
	if err != nil || dacl == nil {
		return errors.New("authentication token file must have a restrictive DACL")
	}
	if err := validateOwnerOnlyDACL(sd.String(), user.User.Sid.String()); err != nil {
		return err
	}
	return nil
}

func validateOwnerOnlyDACL(sddl string, ownerSID string) error {
	start := strings.Index(sddl, "D:")
	if start < 0 {
		return errors.New("authentication token file DACL is missing")
	}
	dacl := sddl[start+2:]
	if sacl := strings.Index(dacl, "S:"); sacl >= 0 {
		dacl = dacl[:sacl]
	}
	firstACE := strings.IndexByte(dacl, '(')
	if firstACE < 0 || !strings.Contains(dacl[:firstACE], "P") {
		return errors.New("authentication token file DACL must disable inheritance")
	}

	allowedOwner := false
	for _, ace := range splitSDDLACEs(dacl[firstACE:]) {
		fields := strings.Split(ace, ";")
		if len(fields) != 6 {
			return errors.New("authentication token file DACL contains an unsupported ACE")
		}
		switch fields[0] {
		case "D", "OD", "XD":
			continue
		case "A", "OA", "XA", "ZA":
			if fields[5] != ownerSID {
				return fmt.Errorf("authentication token file DACL grants access to %s", fields[5])
			}
			allowedOwner = true
		default:
			return fmt.Errorf("authentication token file DACL contains unsupported ACE type %s", fields[0])
		}
	}
	if !allowedOwner {
		return errors.New("authentication token file DACL does not grant access to its owner")
	}
	return nil
}

func splitSDDLACEs(value string) []string {
	var aces []string
	for index := 0; index < len(value); {
		if value[index] != '(' {
			index++
			continue
		}
		start := index + 1
		depth := 1
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
			aces = append(aces, value[start:index-1])
		}
	}
	return aces
}
