//go:build windows

package runtime

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"

	"unit-test-ide.local/test-service/internal/instance"
)

func assertOwnerOnlyDirectoryForTest(t *testing.T, path string) {
	t.Helper()
	if err := validateOwnerOnlyDirectory(path); err != nil {
		t.Fatalf("owner-only directory %q validation failed: %v", path, err)
	}
}

func TestPrepareDataDirCreatesProtectedOwnerOnlyWindowsDirectory(t *testing.T) {
	root := filepath.Join(t.TempDir(), "owner-only")
	if _, err := PrepareDataDir(root); err != nil {
		_, internalErr := pinOwnerOnlyDirectory(root)
		t.Fatalf("PrepareDataDir: %v; internal pin: %v", err, internalErr)
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

func TestPrepareDataDirRejectsIntermediateWindowsJunction(t *testing.T) {
	base := t.TempDir()
	target := filepath.Join(base, "target")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "junction")
	if err := exec.Command("cmd.exe", "/c", "mklink", "/J", link, target).Run(); err != nil {
		t.Skipf("directory junctions are unavailable: %v", err)
	}
	if _, err := PrepareDataDir(filepath.Join(link, "nested")); !errors.Is(err, ErrUnsafeDataDir) {
		t.Fatalf("PrepareDataDir through junction error = %v", err)
	}
}

func TestPinOwnerOnlyDirectoryFailsClosedOnAccessDeniedAncestor(t *testing.T) {
	root := filepath.Join(t.TempDir(), "data")
	blocked := filepath.Dir(root)
	guard, err := pinOwnerOnlyDirectoryWithOpen(root, func(path string, share uint32, readControl bool) (windows.Handle, error) {
		if filepath.Clean(path) == filepath.Clean(blocked) {
			return windows.InvalidHandle, windows.ERROR_ACCESS_DENIED
		}
		return openAbsoluteWindowsDirectory(path, share, readControl)
	})
	if guard != nil {
		_ = guard.Close()
	}
	if err == nil {
		t.Fatal("access-denied ancestor was accepted without a pinned handle or trust proof")
	}
}

func TestWindowsSystemAncestorCanBePinnedWithoutAccessDeniedException(t *testing.T) {
	handle, err := openAbsoluteWindowsDirectory(`C:\Users`, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, false)
	if err != nil {
		t.Fatalf("pin system-owned Users directory: %v", err)
	}
	defer windows.CloseHandle(handle)
	if err := validateWindowsDirectoryObject(handle); err != nil {
		t.Fatal(err)
	}
}

func TestRuntimePinsWindowsAncestorsUntilClose(t *testing.T) {
	base := t.TempDir()
	ancestor := filepath.Join(base, "shared-parent")
	root := filepath.Join(ancestor, "data")
	active, err := Open(Config{
		DataDir: root, ServiceExecutable: os.Args[0], WorkspaceRoot: filepath.Dir(root), Platform: "windows",
		dependencies: testDependencies(&recordingRunner{}, nil),
	})
	if err != nil {
		t.Fatal(err)
	}
	moved := filepath.Join(base, "moved-parent")
	if err := os.Rename(ancestor, moved); err == nil {
		_ = active.Close()
		t.Fatal("ancestor rename succeeded while Runtime owned the pinned path")
	}
	layout, err := PrepareDataDir(root)
	if err != nil {
		_ = active.Close()
		t.Fatal(err)
	}
	if _, err := os.Stat(layout.Database); err != nil {
		_ = active.Close()
		t.Fatalf("database is not under the pinned root: %v", err)
	}
	if err := active.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(ancestor, moved); err != nil {
		t.Fatalf("ancestor remained pinned after Close: %v", err)
	}
}

func TestRuntimePinsWindowsAncestorsBeforeInstanceLock(t *testing.T) {
	base := t.TempDir()
	ancestor := filepath.Join(base, "shared-parent")
	root := filepath.Join(ancestor, "data")
	deps := testDependencies(&recordingRunner{}, nil)
	lockInstance := deps.lockInstance
	moved := filepath.Join(base, "moved-parent")
	var renameErr error
	deps.lockInstance = func(path string) (io.Closer, error) {
		renameErr = os.Rename(ancestor, moved)
		if renameErr == nil {
			if _, err := PrepareDataDir(root); err != nil {
				return nil, err
			}
		}
		return lockInstance(path)
	}
	active, err := Open(Config{DataDir: root, ServiceExecutable: os.Args[0], WorkspaceRoot: filepath.Dir(root), Platform: "windows", dependencies: deps})
	if err != nil {
		t.Fatal(err)
	}
	defer active.Close()
	if renameErr == nil {
		t.Fatal("ancestor replacement succeeded between validation and instance lock")
	}
	if !errors.Is(func() error {
		_, err := instance.Lock(filepath.Join(root, "service.lock"))
		return err
	}(), instance.ErrAlreadyRunning) {
		t.Fatal("runtime lock is not held under the original pinned root")
	}
}

func TestOwnerOnlyACLValidationRejectsInheritedOwnerACE(t *testing.T) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	descriptor, err := windows.SecurityDescriptorFromString(fmt.Sprintf("O:%sD:P(A;OICIID;GA;;;%s)", user.User.Sid.String(), user.User.Sid.String()))
	if err != nil {
		t.Fatal(err)
	}
	dacl, _, err := descriptor.DACL()
	if err != nil {
		t.Fatal(err)
	}
	if !windowsACLHasInheritedACE(dacl) {
		t.Fatal("owner-only ACL validator accepted an inherited owner ACE")
	}
}
