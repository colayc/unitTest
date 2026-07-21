//go:build !windows

package transport_test

import (
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
