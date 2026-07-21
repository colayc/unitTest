package protocol_test

import (
	"testing"

	"unit-test-ide.local/test-service/internal/protocol"
)

const validRequest = `{"protocolVersion":"1.0","kind":"request","messageId":"0123456789abcdef0123456789abcdef","method":"shutdown","sentAt":"2026-07-21T00:00:00Z","payload":{}}`

func TestDecodeRequestAcceptsValidEnvelope(t *testing.T) {
	request, err := protocol.DecodeRequest([]byte(validRequest))
	if err != nil {
		t.Fatalf("expected valid request: %v", err)
	}
	if request.ProtocolVersion != "1.0" || request.Method != "shutdown" {
		t.Fatalf("unexpected request: %#v", request)
	}
}

func TestDecodeRequestAllowsUnsupportedProtocolVersionForDispatch(t *testing.T) {
	request, err := protocol.DecodeRequest([]byte(`{"protocolVersion":"2.0","kind":"request","messageId":"0123456789abcdef0123456789abcdef","method":"shutdown","sentAt":"2026-07-21T00:00:00Z","payload":{}}`))
	if err != nil {
		t.Fatalf("expected request to reach session dispatch: %v", err)
	}
	if request.ProtocolVersion != "2.0" {
		t.Fatalf("unexpected protocol version: %q", request.ProtocolVersion)
	}
}

func TestDecodeRequestRejectsInvalidEnvelopeFields(t *testing.T) {
	tests := map[string]string{
		"missing protocolVersion": `{"kind":"request","messageId":"0123456789abcdef0123456789abcdef","method":"shutdown","sentAt":"2026-07-21T00:00:00Z","payload":{}}`,
		"wrong kind":              `{"protocolVersion":"1.0","kind":"response","messageId":"0123456789abcdef0123456789abcdef","method":"shutdown","sentAt":"2026-07-21T00:00:00Z","payload":{}}`,
		"missing method":          `{"protocolVersion":"1.0","kind":"request","messageId":"0123456789abcdef0123456789abcdef","sentAt":"2026-07-21T00:00:00Z","payload":{}}`,
		"empty method":            `{"protocolVersion":"1.0","kind":"request","messageId":"0123456789abcdef0123456789abcdef","method":"","sentAt":"2026-07-21T00:00:00Z","payload":{}}`,
		"missing sentAt":          `{"protocolVersion":"1.0","kind":"request","messageId":"0123456789abcdef0123456789abcdef","method":"shutdown","payload":{}}`,
		"invalid sentAt":          `{"protocolVersion":"1.0","kind":"request","messageId":"0123456789abcdef0123456789abcdef","method":"shutdown","sentAt":"not-a-date","payload":{}}`,
		"missing messageId":       `{"protocolVersion":"1.0","kind":"request","method":"shutdown","sentAt":"2026-07-21T00:00:00Z","payload":{}}`,
		"short messageId":         `{"protocolVersion":"1.0","kind":"request","messageId":"0123456789abcdef","method":"shutdown","sentAt":"2026-07-21T00:00:00Z","payload":{}}`,
		"uppercase messageId":     `{"protocolVersion":"1.0","kind":"request","messageId":"0123456789ABCDEF0123456789abcdef","method":"shutdown","sentAt":"2026-07-21T00:00:00Z","payload":{}}`,
		"non-hex messageId":       `{"protocolVersion":"1.0","kind":"request","messageId":"0123456789abcdef0123456789abcdeg","method":"shutdown","sentAt":"2026-07-21T00:00:00Z","payload":{}}`,
		"missing payload":         `{"protocolVersion":"1.0","kind":"request","messageId":"0123456789abcdef0123456789abcdef","method":"shutdown","sentAt":"2026-07-21T00:00:00Z"}`,
		"null payload":            `{"protocolVersion":"1.0","kind":"request","messageId":"0123456789abcdef0123456789abcdef","method":"shutdown","sentAt":"2026-07-21T00:00:00Z","payload":null}`,
		"array payload":           `{"protocolVersion":"1.0","kind":"request","messageId":"0123456789abcdef0123456789abcdef","method":"shutdown","sentAt":"2026-07-21T00:00:00Z","payload":[]}`,
		"string payload":          `{"protocolVersion":"1.0","kind":"request","messageId":"0123456789abcdef0123456789abcdef","method":"shutdown","sentAt":"2026-07-21T00:00:00Z","payload":"value"}`,
	}
	for name, input := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := protocol.DecodeRequest([]byte(input)); err == nil {
				t.Fatal("expected invalid envelope to fail")
			}
		})
	}
}

func TestDecodeRequestRejectsUnknownField(t *testing.T) {
	_, err := protocol.DecodeRequest([]byte(`{"protocolVersion":"1.0","kind":"request","messageId":"0123456789abcdef0123456789abcdef","method":"shutdown","sentAt":"2026-07-21T00:00:00Z","payload":{},"unknown":true}`))
	if err == nil {
		t.Fatal("expected unknown field to fail")
	}
}

func TestDecodeRequestRejectsTrailingJSON(t *testing.T) {
	_, err := protocol.DecodeRequest([]byte(validRequest + ` {}`))
	if err == nil {
		t.Fatal("expected trailing JSON to fail")
	}
}
