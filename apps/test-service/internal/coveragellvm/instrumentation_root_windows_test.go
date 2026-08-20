//go:build windows

package coveragellvm

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

func TestWriteInstrumentationRejectsInheritedWindowsACL(t *testing.T) {
	root := filepath.Join(t.TempDir(), "inherited-task-root")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := WriteInstrumentation(root); err == nil {
		t.Fatal("WriteInstrumentation accepted a Task root without a protected owner-only DACL")
	}
}

func makeOwnerOnlyInstrumentationRoot(t *testing.T, path string) {
	t.Helper()
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil || user == nil || user.User.Sid == nil {
		t.Fatal("current Windows user SID is unavailable")
	}
	sid := user.User.Sid.String()
	descriptor, err := windows.SecurityDescriptorFromString(
		fmt.Sprintf("O:%sD:P(A;OICI;GA;;;%s)", sid, sid),
	)
	if err != nil {
		t.Fatal(err)
	}
	attributes := &windows.SecurityAttributes{
		Length:             uint32(unsafe.Sizeof(windows.SecurityAttributes{})),
		SecurityDescriptor: descriptor,
	}
	pointer, err := windows.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := windows.CreateDirectory(pointer, attributes); err != nil {
		t.Fatal(err)
	}
}
