//go:build windows

package instance

import (
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

func TestLockFileHasProtectedOwnerOnlyDACL(t *testing.T) {
	path := filepath.Join(t.TempDir(), "service.lock")
	locked, err := Lock(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := locked.Close(); err != nil {
		t.Fatal(err)
	}
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	handle, err := windows.CreateFile(name, windows.GENERIC_READ|windows.READ_CONTROL, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(handle)
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	if err := validateOwnerOnlyFileHandle(handle, user.User.Sid); err != nil {
		t.Fatalf("lock DACL validation failed: %v", err)
	}
}
