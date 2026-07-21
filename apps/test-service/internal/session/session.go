package session

import (
	"crypto/subtle"
	"encoding/json"
	"sync"

	"unit-test-ide.local/test-service/internal/protocol"
	"unit-test-ide.local/test-service/internal/protocolmodel"
)

type Session struct {
	token, platform, transport string
	mu                         sync.Mutex
	authenticated              bool
	shutdown                   chan struct{}
	shutdownOnce               sync.Once
}

type handshake struct {
	Token         string `json:"token"`
	ClientName    string `json:"clientName"`
	ClientVersion string `json:"clientVersion"`
}

func New(token, platform, transport string) *Session {
	return &Session{token: token, platform: platform, transport: transport, shutdown: make(chan struct{})}
}

func (s *Session) ShutdownRequested() <-chan struct{} { return s.shutdown }

func (s *Session) Handle(request protocol.Request) protocol.Response {
	s.mu.Lock()
	defer s.mu.Unlock()

	if request.ProtocolVersion != protocol.Version {
		return protocol.Failure(request, "UNSUPPORTED_PROTOCOL", "protocol version is not supported", false)
	}
	if !s.authenticated && request.Method != "handshake" {
		return protocol.Failure(request, "AUTH_REQUIRED", "handshake must be completed first", false)
	}

	switch request.Method {
	case "handshake":
		var payload handshake
		if err := json.Unmarshal(request.Payload, &payload); err != nil {
			return protocol.Failure(request, "INVALID_MESSAGE", "invalid handshake payload", false)
		}
		if subtle.ConstantTimeCompare([]byte(payload.Token), []byte(s.token)) != 1 {
			return protocol.Failure(request, "AUTH_FAILED", "authentication failed", false)
		}
		s.authenticated = true
		return protocol.Success(request, map[string]string{"negotiatedProtocolVersion": protocol.Version, "serviceVersion": "0.1.0"})
	case "capabilities/get":
		return protocol.Success(request, protocolmodel.Capabilities{Platform: s.platform, Transports: []string{s.transport}, Toolchains: []string{}, Frameworks: []string{}, CoverageTools: []string{}})
	case "shutdown":
		s.shutdownOnce.Do(func() { close(s.shutdown) })
		return protocol.Success(request, map[string]bool{"accepted": true})
	default:
		return protocol.Failure(request, "METHOD_NOT_FOUND", "method is not supported", false)
	}
}
