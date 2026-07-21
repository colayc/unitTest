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

func TestSessionRejectsUnknownMethodAfterAuthentication(t *testing.T) {
	s := session.New("0123456789abcdef", "linux", "unix-socket")
	_ = s.Handle(request(t, "handshake", map[string]string{"token": "0123456789abcdef", "clientName": "test", "clientVersion": "0.1.0"}))
	result := s.Handle(request(t, "unknown", map[string]any{}))
	if result.Error.Code != "METHOD_NOT_FOUND" {
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
