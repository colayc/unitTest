//go:build windows

package runtime

import (
	"fmt"
	"path/filepath"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

func TestPrepareDataDirCreatesProtectedOwnerOnlyWindowsDirectory(t *testing.T) {
	root := filepath.Join(t.TempDir(), "owner-only")
	if _, err := PrepareDataDir(root); err != nil {
		t.Fatal(err)
	}
	if err := validateOwnerOnlyDirectory(root); err != nil {
		t.Fatalf("created directory validation failed: %v", err)
	}
}

func TestPrepareDataDirRejectsWindowsDirectoryWithNonOwnerAllowACE(t *testing.T) {
	root := filepath.Join(t.TempDir(), "permissive")
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	descriptor, err := windows.SecurityDescriptorFromString(fmt.Sprintf("O:%sD:P(A;OICI;GA;;;%s)(A;OICI;GR;;;WD)", user.User.Sid.String(), user.User.Sid.String()))
	if err != nil {
		t.Fatal(err)
	}
	name, err := windows.UTF16PtrFromString(root)
	if err != nil {
		t.Fatal(err)
	}
	attributes := &windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), SecurityDescriptor: descriptor}
	if err := windows.CreateDirectory(name, attributes); err != nil {
		t.Fatal(err)
	}
	if _, err := PrepareDataDir(root); err == nil {
		t.Fatal("PrepareDataDir accepted a DACL granting access to Everyone")
	}
}
