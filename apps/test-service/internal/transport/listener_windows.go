//go:build windows

package transport

import (
	"fmt"
	"net"
	"os/user"

	"github.com/Microsoft/go-winio"
)

func listen(endpoint string) (net.Listener, error) {
	current, err := user.Current()
	if err != nil {
		return nil, err
	}
	sddl := fmt.Sprintf("D:P(A;;GA;;;%s)", current.Uid)
	return winio.ListenPipe(endpoint, &winio.PipeConfig{SecurityDescriptor: sddl})
}

func PlatformName() string  { return "windows" }
func TransportName() string { return "named-pipe" }
