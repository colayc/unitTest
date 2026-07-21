package session_test

import (
	"encoding/json"
	"testing"

	"unit-test-ide.local/test-service/internal/protocol"
	"unit-test-ide.local/test-service/internal/session"
)

func request(t *testing.T, method string, payload any) protocol.Request {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	return protocol.Request{ProtocolVersion: "1.0", Kind: "request", MessageID: "0123456789abcdef0123456789abcdef", Method: method, Payload: raw}
}

func TestSessionRequiresHandshakeThenReturnsCapabilities(t *testing.T) {
	s := session.New("0123456789abcdef", "windows", "named-pipe")
	before := s.Handle(request(t, "capabilities/get", map[string]any{}))
	if before.Kind != "error" || before.Error.Code != "AUTH_REQUIRED" {
		t.Fatalf("unexpected response: %#v", before)
	}
	accepted := s.Handle(request(t, "handshake", map[string]string{"token": "0123456789abcdef", "clientName": "test", "clientVersion": "0.1.0"}))
	if accepted.Kind != "response" {
		t.Fatalf("handshake failed: %#v", accepted)
	}
	capabilities := s.Handle(request(t, "capabilities/get", map[string]any{}))
	if capabilities.Kind != "response" {
		t.Fatalf("capabilities failed: %#v", capabilities)
	}
}

func TestSessionRejectsWrongToken(t *testing.T) {
	s := session.New("0123456789abcdef", "linux", "unix-socket")
	result := s.Handle(request(t, "handshake", map[string]string{"token": "wrong-token-value", "clientName": "test", "clientVersion": "0.1.0"}))
	if result.Error.Code != "AUTH_FAILED" {
		t.Fatalf("unexpected response: %#v", result)
	}
}

func TestSessionRejectsInvalidHandshakePayload(t *testing.T) {
	tests := map[string]string{
		"unknown property":      `{"token":"0123456789abcdef","clientName":"test","clientVersion":"0.1.0","unknown":true}`,
		"missing token":         `{"clientName":"test","clientVersion":"0.1.0"}`,
		"short token":           `{"token":"too-short","clientName":"test","clientVersion":"0.1.0"}`,
		"missing clientName":    `{"token":"0123456789abcdef","clientVersion":"0.1.0"}`,
		"empty clientName":      `{"token":"0123456789abcdef","clientName":"","clientVersion":"0.1.0"}`,
		"missing clientVersion": `{"token":"0123456789abcdef","clientName":"test"}`,
		"empty clientVersion":   `{"token":"0123456789abcdef","clientName":"test","clientVersion":""}`,
		"null":                  `null`,
		"array":                 `[]`,
		"string":                `"payload"`,
		"trailing JSON":         `{"token":"0123456789abcdef","clientName":"test","clientVersion":"0.1.0"} {}`,
	}
	for name, payload := range tests {
		t.Run(name, func(t *testing.T) {
			s := session.New("0123456789abcdef", "linux", "unix-socket")
			result := s.Handle(protocol.Request{
				ProtocolVersion: protocol.Version,
				Kind:            "request",
				MessageID:       "0123456789abcdef0123456789abcdef",
				Method:          "handshake",
				Payload:         json.RawMessage(payload),
			})
			if result.Kind != "error" || result.Error == nil || result.Error.Code != "INVALID_MESSAGE" {
				t.Fatalf("unexpected response: %#v", result)
			}
		})
	}
}

func TestSessionRejectsUnknownMethodAfterAuthentication(t *testing.T) {
	s := session.New("0123456789abcdef", "linux", "unix-socket")
	_ = s.Handle(request(t, "handshake", map[string]string{"token": "0123456789abcdef", "clientName": "test", "clientVersion": "0.1.0"}))
	result := s.Handle(request(t, "unknown", map[string]any{}))
	if result.Error.Code != "METHOD_NOT_FOUND" {
		t.Fatalf("unexpected response: %#v", result)
	}
}

func TestSessionRejectsNonEmptyPayloadForKnownEmptyMethods(t *testing.T) {
	for _, method := range []string{"capabilities/get", "shutdown"} {
		t.Run(method, func(t *testing.T) {
			s := session.New("0123456789abcdef", "linux", "unix-socket")
			_ = s.Handle(request(t, "handshake", map[string]string{"token": "0123456789abcdef", "clientName": "test", "clientVersion": "0.1.0"}))
			result := s.Handle(request(t, method, map[string]any{"unexpected": true}))
			if result.Error == nil || result.Error.Code != "INVALID_MESSAGE" {
				t.Fatalf("unexpected response: %#v", result)
			}
			if method == "shutdown" {
				select {
				case <-s.ShutdownRequested():
					t.Fatal("invalid shutdown payload requested shutdown")
				default:
				}
			}
		})
	}
}

func TestSessionPreservesUnknownMethodHandlingForNonEmptyPayload(t *testing.T) {
	s := session.New("0123456789abcdef", "linux", "unix-socket")
	_ = s.Handle(request(t, "handshake", map[string]string{"token": "0123456789abcdef", "clientName": "test", "clientVersion": "0.1.0"}))
	result := s.Handle(request(t, "unknown", map[string]any{"allowed": "for method routing"}))
	if result.Error == nil || result.Error.Code != "METHOD_NOT_FOUND" {
		t.Fatalf("unexpected response: %#v", result)
	}
}

func TestShutdownClosesSignalOnce(t *testing.T) {
	s := session.New("0123456789abcdef", "linux", "unix-socket")
	_ = s.Handle(request(t, "handshake", map[string]string{"token": "0123456789abcdef", "clientName": "test", "clientVersion": "0.1.0"}))
	_ = s.Handle(request(t, "shutdown", map[string]any{}))
	_ = s.Handle(request(t, "shutdown", map[string]any{}))
	select {
	case <-s.ShutdownRequested():
	default:
		t.Fatal("shutdown signal was not closed")
	}
}
