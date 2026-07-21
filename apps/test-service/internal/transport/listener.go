package transport

import "net"

func Listen(endpoint string) (net.Listener, error) { return listen(endpoint) }
