package session_test

import (
	"context"
	"encoding/json"
	"testing"

	"unit-test-ide.local/test-service/internal/protocol"
	"unit-test-ide.local/test-service/internal/session"
)

func request(t *testing.T, method string, payload any) protocol.Request {
	return requestVersion(t, protocol.Version10, method, payload)
}

func requestVersion(t *testing.T, version, method string, payload any) protocol.Request {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	return protocol.Request{ProtocolVersion: version, Kind: "request", MessageID: "0123456789abcdef0123456789abcdef", Method: method, Payload: raw}
}

func TestSessionNegotiatesV11AndKeepsV10Shape(t *testing.T) {
	v11 := session.New("0123456789abcdef", "windows", "named-pipe", nil)
	accepted := v11.Handle(context.Background(), requestVersion(t, "1.1", "handshake", map[string]any{
		"token": "0123456789abcdef", "clientName": "test", "clientVersion": "0.2.0",
		"supportedProtocolVersions": []string{"1.1", "1.0"},
	}))
	if accepted.ProtocolVersion != "1.1" || v11.NegotiatedVersion() != "1.1" {
		t.Fatalf("v1.1 negotiation failed: %#v", accepted)
	}

	v10 := session.New("0123456789abcdef", "linux", "unix-socket", nil)
	legacy := v10.Handle(context.Background(), requestVersion(t, "1.0", "handshake", map[string]string{
		"token": "0123456789abcdef", "clientName": "legacy", "clientVersion": "0.1.0",
	}))
	if legacy.ProtocolVersion != "1.0" || v10.NegotiatedVersion() != "1.0" {
		t.Fatalf("v1.0 negotiation failed: %#v", legacy)
	}

	capabilities := v10.Handle(context.Background(), requestVersion(t, "1.0", "capabilities/get", map[string]any{}))
	raw, err := json.Marshal(capabilities.Payload)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"taskExecution", "eventReplay", "sqliteHistory", "artifactRead", "processTreeControl"} {
		if _, exists := fields[forbidden]; exists {
			t.Fatalf("v1.0 capabilities unexpectedly contain %q: %s", forbidden, raw)
		}
	}
	if len(fields) != 5 {
		t.Fatalf("v1.0 capabilities shape changed: %s", raw)
	}
}

func TestSessionRejectsUnsupportedAndMismatchedVersions(t *testing.T) {
	unknown := session.New("0123456789abcdef", "linux", "unix-socket", nil)
	result := unknown.Handle(context.Background(), requestVersion(t, "2.0", "handshake", map[string]string{
		"token": "0123456789abcdef", "clientName": "test", "clientVersion": "0.2.0",
	}))
	if result.Error == nil || result.Error.Code != "UNSUPPORTED_PROTOCOL" || result.ProtocolVersion != protocol.Version10 {
		t.Fatalf("unexpected unknown-version response: %#v", result)
	}

	negotiated := session.New("0123456789abcdef", "linux", "unix-socket", nil)
	_ = negotiated.Handle(context.Background(), requestVersion(t, "1.1", "handshake", map[string]any{
		"token": "0123456789abcdef", "clientName": "test", "clientVersion": "0.2.0",
		"supportedProtocolVersions": []string{"1.1", "1.0"},
	}))
	result = negotiated.Handle(context.Background(), requestVersion(t, "1.0", "capabilities/get", map[string]any{}))
	if result.Error == nil || result.Error.Code != "UNSUPPORTED_PROTOCOL" || result.ProtocolVersion != protocol.Version11 {
		t.Fatalf("unexpected mismatched-version response: %#v", result)
	}
}

func TestSessionRequiresValidV11VersionOffer(t *testing.T) {
	tests := map[string]struct {
		payload any
		code    string
	}{
		"missing offer": {
			payload: map[string]string{"token": "0123456789abcdef", "clientName": "test", "clientVersion": "0.2.0"},
			code:    "INVALID_MESSAGE",
		},
		"mismatched offer": {
			payload: map[string]any{"token": "0123456789abcdef", "clientName": "test", "clientVersion": "0.2.0", "supportedProtocolVersions": []string{"1.0"}},
			code:    "UNSUPPORTED_PROTOCOL",
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			s := session.New("0123456789abcdef", "linux", "unix-socket", nil)
			result := s.Handle(context.Background(), requestVersion(t, protocol.Version11, "handshake", test.payload))
			if result.Error == nil || result.Error.Code != test.code {
				t.Fatalf("unexpected response: %#v", result)
			}
			if s.Authenticated() || s.NegotiatedVersion() != "" {
				t.Fatal("failed negotiation changed session state")
			}
		})
	}
}

func TestSessionKeepsV10HandshakePayloadStrict(t *testing.T) {
	s := session.New("0123456789abcdef", "linux", "unix-socket", nil)
	result := s.Handle(context.Background(), requestVersion(t, protocol.Version10, "handshake", map[string]any{
		"token": "0123456789abcdef", "clientName": "legacy", "clientVersion": "0.1.0",
		"supportedProtocolVersions": []string{protocol.Version10},
	}))
	if result.Error == nil || result.Error.Code != "INVALID_MESSAGE" {
		t.Fatalf("unexpected response: %#v", result)
	}
}

func TestSessionReturnsVersionedCapabilities(t *testing.T) {
	s := session.New("0123456789abcdef", "linux", "unix-socket", nil)
	_ = s.Handle(context.Background(), requestVersion(t, protocol.Version11, "handshake", map[string]any{
		"token": "0123456789abcdef", "clientName": "test", "clientVersion": "0.2.0",
		"supportedProtocolVersions": []string{"1.1"},
	}))
	result := s.Handle(context.Background(), requestVersion(t, protocol.Version11, "capabilities/get", map[string]any{}))
	raw, err := json.Marshal(result.Payload)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{"taskExecution", "eventReplay", "sqliteHistory", "artifactRead", "processTreeControl"} {
		if _, exists := fields[required]; !exists {
			t.Fatalf("v1.1 capabilities missing %q: %s", required, raw)
		}
	}
}

func TestSessionGatesPhase2MethodsByProtocolAndDependency(t *testing.T) {
	methods := []string{"tasks/start", "tasks/get", "tasks/list", "tasks/cancel", "events/subscribe", "artifacts/list", "artifacts/read"}
	for _, version := range []string{protocol.Version10, protocol.Version11} {
		t.Run(version, func(t *testing.T) {
			s := session.New("0123456789abcdef", "linux", "unix-socket", nil)
			handshakePayload := map[string]any{"token": "0123456789abcdef", "clientName": "test", "clientVersion": "0.2.0"}
			if version == protocol.Version11 {
				handshakePayload["supportedProtocolVersions"] = []string{protocol.Version11}
			}
			_ = s.Handle(context.Background(), requestVersion(t, version, "handshake", handshakePayload))
			for _, method := range methods {
				result := s.Handle(context.Background(), requestVersion(t, version, method, map[string]any{}))
				want := "PROTOCOL_FEATURE_UNAVAILABLE"
				if version == protocol.Version11 {
					want = "SERVICE_UNHEALTHY"
				}
				if result.Error == nil || result.Error.Code != want {
					t.Fatalf("%s: got %#v, want %s", method, result, want)
				}
			}
		})
	}
}

func TestSessionRequiresHandshakeThenReturnsCapabilities(t *testing.T) {
	s := session.New("0123456789abcdef", "windows", "named-pipe", nil)
	before := s.Handle(context.Background(), request(t, "capabilities/get", map[string]any{}))
	if before.Kind != "error" || before.Error.Code != "AUTH_REQUIRED" {
		t.Fatalf("unexpected response: %#v", before)
	}
	accepted := s.Handle(context.Background(), request(t, "handshake", map[string]string{"token": "0123456789abcdef", "clientName": "test", "clientVersion": "0.1.0"}))
	if accepted.Kind != "response" {
		t.Fatalf("handshake failed: %#v", accepted)
	}
	capabilities := s.Handle(context.Background(), request(t, "capabilities/get", map[string]any{}))
	if capabilities.Kind != "response" {
		t.Fatalf("capabilities failed: %#v", capabilities)
	}
}

func TestSessionRejectsWrongToken(t *testing.T) {
	s := session.New("0123456789abcdef", "linux", "unix-socket", nil)
	result := s.Handle(context.Background(), request(t, "handshake", map[string]string{"token": "wrong-token-value", "clientName": "test", "clientVersion": "0.1.0"}))
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
			s := session.New("0123456789abcdef", "linux", "unix-socket", nil)
			result := s.Handle(context.Background(), protocol.Request{
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
	s := session.New("0123456789abcdef", "linux", "unix-socket", nil)
	_ = s.Handle(context.Background(), request(t, "handshake", map[string]string{"token": "0123456789abcdef", "clientName": "test", "clientVersion": "0.1.0"}))
	result := s.Handle(context.Background(), request(t, "unknown", map[string]any{}))
	if result.Error.Code != "METHOD_NOT_FOUND" {
		t.Fatalf("unexpected response: %#v", result)
	}
}

func TestSessionRejectsNonEmptyPayloadForKnownEmptyMethods(t *testing.T) {
	for _, method := range []string{"capabilities/get", "shutdown"} {
		t.Run(method, func(t *testing.T) {
			s := session.New("0123456789abcdef", "linux", "unix-socket", nil)
			_ = s.Handle(context.Background(), request(t, "handshake", map[string]string{"token": "0123456789abcdef", "clientName": "test", "clientVersion": "0.1.0"}))
			result := s.Handle(context.Background(), request(t, method, map[string]any{"unexpected": true}))
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
	s := session.New("0123456789abcdef", "linux", "unix-socket", nil)
	_ = s.Handle(context.Background(), request(t, "handshake", map[string]string{"token": "0123456789abcdef", "clientName": "test", "clientVersion": "0.1.0"}))
	result := s.Handle(context.Background(), request(t, "unknown", map[string]any{"allowed": "for method routing"}))
	if result.Error == nil || result.Error.Code != "METHOD_NOT_FOUND" {
		t.Fatalf("unexpected response: %#v", result)
	}
}

func TestShutdownClosesSignalOnce(t *testing.T) {
	s := session.New("0123456789abcdef", "linux", "unix-socket", nil)
	_ = s.Handle(context.Background(), request(t, "handshake", map[string]string{"token": "0123456789abcdef", "clientName": "test", "clientVersion": "0.1.0"}))
	_ = s.Handle(context.Background(), request(t, "shutdown", map[string]any{}))
	_ = s.Handle(context.Background(), request(t, "shutdown", map[string]any{}))
	select {
	case <-s.ShutdownRequested():
	default:
		t.Fatal("shutdown signal was not closed")
	}
}
