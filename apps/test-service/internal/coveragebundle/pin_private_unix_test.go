//go:build !windows

package coveragebundle

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestPrivateUnixDirectoryStatusRequiresCurrentOwnerAnd0700(t *testing.T) {
	owner := uint32(os.Geteuid())
	if err := validatePrivateUnixDirectoryStatus(unix.Stat_t{Mode: unix.S_IFDIR | 0o700, Uid: owner}, owner); err != nil {
		t.Fatalf("private current-owner status rejected: %v", err)
	}
	if err := validatePrivateUnixDirectoryStatus(unix.Stat_t{Mode: unix.S_IFDIR | 0o700, Uid: owner + 1}, owner); err == nil {
		t.Fatal("private status accepted another owner")
	}
	if err := validatePrivateUnixDirectoryStatus(unix.Stat_t{Mode: unix.S_IFDIR | 0o770, Uid: owner}, owner); err == nil {
		t.Fatal("private status accepted group-writable mode")
	}
}

func TestPrivateUnixCleanupAuthorityRejectsNonPrivateCollector(t *testing.T) {
	root := filepath.Join(strictTestTempDir(t), "coverage")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(root, 0o770); err != nil {
		t.Fatal(err)
	}
	directory, err := newVerifiedDirectory(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = directory.Close() })
	if err := preflightPinnedCleanupAuthority(directory, directoryFinalPin(directory)); err == nil {
		t.Fatal("private cleanup preflight accepted group-writable collector")
	}
}

func TestPrivateUnixCleanupRemovesRetainedChild(t *testing.T) {
	root := filepath.Join(strictTestTempDir(t), "coverage")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	parent, err := pinDirectObject(root, true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = parent.Close() })
	name := "owned.json"
	path := filepath.Join(root, name)
	if err := os.WriteFile(path, []byte("owned"), 0o600); err != nil {
		t.Fatal(err)
	}
	child, err := pinChildObjectWithDelete(parent, name, false, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := removePinnedChild(parent, child, name); err != nil {
		_ = child.Close()
		t.Fatalf("remove retained child: %v", err)
	}
	if err := child.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatalf("retained child remains after cleanup: %v", err)
	}
}

func TestPrivateUnixCleanupRejectsReplacementWithoutDeletingIt(t *testing.T) {
	root := filepath.Join(strictTestTempDir(t), "coverage")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	parent, err := pinDirectObject(root, true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = parent.Close() })
	name := "owned.json"
	path := filepath.Join(root, name)
	if err := os.WriteFile(path, []byte("owned"), 0o600); err != nil {
		t.Fatal(err)
	}
	child, err := pinChildObjectWithDelete(parent, name, false, true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = child.Close() })
	replacement := filepath.Join(root, "replacement.json")
	originalHook := unixBeforePinnedUnlink
	unixBeforePinnedUnlink = func() {
		if err := os.Rename(path, replacement); err != nil {
			t.Fatalf("rename original: %v", err)
		}
		if err := os.WriteFile(path, []byte("replacement"), 0o600); err != nil {
			t.Fatalf("create replacement: %v", err)
		}
	}
	t.Cleanup(func() { unixBeforePinnedUnlink = originalHook })
	if err := removePinnedChild(parent, child, name); err == nil {
		t.Fatal("cleanup removed a replacement after identity changed")
	}
	if contents, err := os.ReadFile(path); err != nil || string(contents) != "replacement" {
		t.Fatalf("replacement after rejected cleanup = %q, %v", contents, err)
	}
	if contents, err := os.ReadFile(replacement); err != nil || string(contents) != "owned" {
		t.Fatalf("original after rejected cleanup = %q, %v", contents, err)
	}
	if err := child.verifyIdentity(); err == nil {
		t.Fatal("replacement did not invalidate retained identity")
	}
}
