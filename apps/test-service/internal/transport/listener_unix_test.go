//go:build !windows

package transport_test

import (
	"net"
	"os"
	"path/filepath"
	"testing"

	"unit-test-ide.local/test-service/internal/transport"
)

func TestUnixSocketIsOwnerOnly(t *testing.T) {
	endpoint := filepath.Join(t.TempDir(), "service.sock")
	listener, err := transport.Listen(endpoint)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	info, err := os.Stat(endpoint)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %o", info.Mode().Perm())
	}
}

func TestListenPreservesRegularFileEndpoint(t *testing.T) {
	endpoint := filepath.Join(t.TempDir(), "service.sock")
	contents := []byte("do not remove")
	if err := os.WriteFile(endpoint, contents, 0o600); err != nil {
		t.Fatal(err)
	}
	assertListenRejected(t, endpoint)
	actual, err := os.ReadFile(endpoint)
	if err != nil {
		t.Fatalf("regular file was not preserved: %v", err)
	}
	if string(actual) != string(contents) {
		t.Fatalf("regular file contents = %q, want %q", actual, contents)
	}
}

func TestListenPreservesDirectoryEndpoint(t *testing.T) {
	endpoint := filepath.Join(t.TempDir(), "service.sock")
	if err := os.Mkdir(endpoint, 0o700); err != nil {
		t.Fatal(err)
	}
	assertListenRejected(t, endpoint)
	info, err := os.Lstat(endpoint)
	if err != nil {
		t.Fatalf("directory was not preserved: %v", err)
	}
	if !info.IsDir() {
		t.Fatalf("endpoint mode = %v, want directory", info.Mode())
	}
}

func TestListenPreservesSymlinkEndpoint(t *testing.T) {
	directory := t.TempDir()
	target := filepath.Join(directory, "target")
	endpoint := filepath.Join(directory, "service.sock")
	if err := os.WriteFile(target, []byte("target"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, endpoint); err != nil {
		t.Fatal(err)
	}
	assertListenRejected(t, endpoint)
	actualTarget, err := os.Readlink(endpoint)
	if err != nil {
		t.Fatalf("symlink was not preserved: %v", err)
	}
	if actualTarget != target {
		t.Fatalf("symlink target = %q, want %q", actualTarget, target)
	}
}

func TestListenRemovesStaleUnixSocket(t *testing.T) {
	endpoint := filepath.Join(t.TempDir(), "service.sock")
	stale, err := net.Listen("unix", endpoint)
	if err != nil {
		t.Fatal(err)
	}
	unixListener, ok := stale.(*net.UnixListener)
	if !ok {
		_ = stale.Close()
		t.Fatalf("listener type = %T, want *net.UnixListener", stale)
	}
	unixListener.SetUnlinkOnClose(false)
	if err := stale.Close(); err != nil {
		t.Fatal(err)
	}

	listener, err := transport.Listen(endpoint)
	if err != nil {
		t.Fatalf("listen with stale socket: %v", err)
	}
	defer listener.Close()
}

func assertListenRejected(t *testing.T, endpoint string) {
	t.Helper()
	listener, err := transport.Listen(endpoint)
	if listener != nil {
		_ = listener.Close()
	}
	if err == nil {
		t.Fatal("expected existing non-socket endpoint to be rejected")
	}
}
