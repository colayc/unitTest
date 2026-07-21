//go:build !windows

package transport

import (
	"errors"
	"fmt"
	"net"
	"os"
	"syscall"
	"time"
)

func listen(endpoint string) (net.Listener, error) {
	info, err := os.Lstat(endpoint)
	if err == nil {
		if info.Mode()&os.ModeSocket == 0 {
			return nil, fmt.Errorf("IPC endpoint exists and is not a Unix socket: %s", endpoint)
		}
		connection, dialErr := net.DialTimeout("unix", endpoint, 250*time.Millisecond)
		if dialErr == nil {
			_ = connection.Close()
			return nil, fmt.Errorf("IPC endpoint is already active: %s", endpoint)
		}
		if !errors.Is(dialErr, syscall.ECONNREFUSED) {
			return nil, fmt.Errorf("cannot confirm Unix socket is stale: %w", dialErr)
		}
		if err := os.Remove(endpoint); err != nil {
			return nil, err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	listener, err := net.Listen("unix", endpoint)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(endpoint, 0o600); err != nil {
		_ = listener.Close()
		return nil, err
	}
	return listener, nil
}

func PlatformName() string  { return "linux" }
func TransportName() string { return "unix-socket" }
