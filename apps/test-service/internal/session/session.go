package session

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"sync"
	"unicode/utf8"

	"unit-test-ide.local/test-service/internal/protocol"
	"unit-test-ide.local/test-service/internal/protocolmodel"
)

type TaskAPI interface{}

type Session struct {
	token, platform, transport string
	mu                         sync.Mutex
	authenticated              bool
	negotiatedVersion          string
	taskAPI                    TaskAPI
	shutdown                   chan struct{}
	shutdownOnce               sync.Once
}

type handshake struct {
	Token                     string   `json:"token"`
	ClientName                string   `json:"clientName"`
	ClientVersion             string   `json:"clientVersion"`
	SupportedProtocolVersions []string `json:"supportedProtocolVersions,omitempty"`
}

func New(token, platform, transport string, taskAPI TaskAPI) *Session {
	return &Session{token: token, platform: platform, transport: transport, taskAPI: taskAPI, shutdown: make(chan struct{})}
}

func (s *Session) ShutdownRequested() <-chan struct{} { return s.shutdown }

func (s *Session) Authenticated() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.authenticated
}

func (s *Session) NegotiatedVersion() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.negotiatedVersion
}

func (s *Session) Handle(_ context.Context, request protocol.Request) protocol.Response {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !protocol.SupportedVersion(request.ProtocolVersion) {
		return protocol.Failure(request.ProtocolVersion, request, "UNSUPPORTED_PROTOCOL", "protocol version is not supported", false)
	}
	responseVersion := request.ProtocolVersion
	if s.authenticated {
		responseVersion = s.negotiatedVersion
		if request.ProtocolVersion != s.negotiatedVersion {
			return protocol.Failure(responseVersion, request, "UNSUPPORTED_PROTOCOL", "protocol version does not match the negotiated version", false)
		}
	}
	if !s.authenticated && request.Method != "handshake" {
		return protocol.Failure(responseVersion, request, "AUTH_REQUIRED", "handshake must be completed first", false)
	}

	switch request.Method {
	case "handshake":
		payload, err := decodeHandshake(request.Payload, request.ProtocolVersion)
		if err != nil {
			return protocol.Failure(responseVersion, request, "INVALID_MESSAGE", "invalid handshake payload", false)
		}
		negotiatedVersion, ok := negotiate(request.ProtocolVersion, payload.SupportedProtocolVersions)
		if !ok {
			return protocol.Failure(responseVersion, request, "UNSUPPORTED_PROTOCOL", "protocol version is not supported", false)
		}
		if subtle.ConstantTimeCompare([]byte(payload.Token), []byte(s.token)) != 1 {
			return protocol.Failure(negotiatedVersion, request, "AUTH_FAILED", "authentication failed", false)
		}
		s.authenticated = true
		s.negotiatedVersion = negotiatedVersion
		return protocol.Success(negotiatedVersion, request, map[string]string{"negotiatedProtocolVersion": negotiatedVersion, "serviceVersion": "0.1.0"})
	case "capabilities/get":
		if err := decodeEmpty(request.Payload); err != nil {
			return protocol.Failure(responseVersion, request, "INVALID_MESSAGE", "payload must be an empty object", false)
		}
		if s.negotiatedVersion == protocol.Version11 {
			return protocol.Success(responseVersion, request, protocolmodel.CapabilitiesV11{
				Platform:           protocolmodel.Platform(s.platform),
				Transports:         []protocolmodel.Transport{protocolmodel.Transport(s.transport)},
				Toolchains:         []string{},
				Frameworks:         []string{},
				CoverageTools:      []string{},
				TaskExecution:      true,
				EventReplay:        true,
				SqliteHistory:      true,
				ArtifactRead:       true,
				ProcessTreeControl: map[string]protocolmodel.ProcessTreeControl{"windows": protocolmodel.JobObject, "linux": protocolmodel.ProcessGroup}[s.platform],
			})
		}
		return protocol.Success(responseVersion, request, protocolmodel.Capabilities{Platform: s.platform, Transports: []string{s.transport}, Toolchains: []string{}, Frameworks: []string{}, CoverageTools: []string{}})
	case "shutdown":
		if err := decodeEmpty(request.Payload); err != nil {
			return protocol.Failure(responseVersion, request, "INVALID_MESSAGE", "payload must be an empty object", false)
		}
		s.shutdownOnce.Do(func() { close(s.shutdown) })
		return protocol.Success(responseVersion, request, map[string]bool{"accepted": true})
	default:
		if phase2Method(request.Method) {
			if s.negotiatedVersion == protocol.Version10 {
				return protocol.Failure(responseVersion, request, "PROTOCOL_FEATURE_UNAVAILABLE", "method requires protocol 1.1", false)
			}
			if s.taskAPI == nil {
				return protocol.Failure(responseVersion, request, "SERVICE_UNHEALTHY", "task service is unavailable", true)
			}
		}
		return protocol.Failure(responseVersion, request, "METHOD_NOT_FOUND", "method is not supported", false)
	}
}

func negotiate(envelopeVersion string, supported []string) (string, bool) {
	if envelopeVersion == protocol.Version10 && len(supported) == 0 {
		return protocol.Version10, true
	}
	for _, candidate := range []string{protocol.Version11, protocol.Version10} {
		if candidate != envelopeVersion {
			continue
		}
		for _, offered := range supported {
			if offered == candidate {
				return candidate, true
			}
		}
	}
	return "", false
}

func phase2Method(method string) bool {
	switch method {
	case "tasks/start", "tasks/get", "tasks/list", "tasks/cancel", "events/subscribe", "artifacts/list", "artifacts/read":
		return true
	default:
		return false
	}
}

func decodeEmpty(raw json.RawMessage) error {
	var payload map[string]json.RawMessage
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if err := decoder.Decode(&payload); err != nil || payload == nil || len(payload) != 0 {
		return errors.New("payload is not an empty object")
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return errors.New("multiple JSON values")
	}
	return nil
}

func decodeHandshake(raw json.RawMessage, version string) (handshake, error) {
	var payload handshake
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&payload); err != nil {
		return handshake{}, err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return handshake{}, errors.New("multiple JSON values")
	}
	if utf8.RuneCountInString(payload.Token) < 16 || payload.ClientName == "" || payload.ClientVersion == "" {
		return handshake{}, errors.New("missing handshake fields")
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil || fields == nil {
		return handshake{}, errors.New("handshake payload must be an object")
	}
	if _, offered := fields["supportedProtocolVersions"]; version == protocol.Version10 && offered {
		return handshake{}, errors.New("protocol 1.0 handshake contains a version offer")
	}
	if version == protocol.Version11 && len(payload.SupportedProtocolVersions) == 0 {
		return handshake{}, errors.New("protocol 1.1 handshake requires a version offer")
	}
	return payload, nil
}
