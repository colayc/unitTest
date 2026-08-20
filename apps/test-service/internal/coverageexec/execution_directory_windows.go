//go:build windows

package coverageexec

import (
	"errors"
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

func createOwnerOnlyExecutionDirectory(path string) error {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return err
	}
	if user == nil || user.User.Sid == nil {
		return errors.New("current process owner is unavailable")
	}
	sid := user.User.Sid.String()
	descriptor, err := windows.SecurityDescriptorFromString(
		fmt.Sprintf("O:%sD:P(A;OICI;GA;;;%s)", sid, sid),
	)
	if err != nil {
		return err
	}
	attributes := &windows.SecurityAttributes{
		Length:             uint32(unsafe.Sizeof(windows.SecurityAttributes{})),
		SecurityDescriptor: descriptor,
	}
	pointer, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	return windows.CreateDirectory(pointer, attributes)
}
