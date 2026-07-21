//go:build !windows

package transport

import (
	"net"
	"os"
)

func listen(endpoint string) (net.Listener, error) {
	_ = os.Remove(endpoint)
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
