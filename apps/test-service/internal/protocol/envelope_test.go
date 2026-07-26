package protocol_test

import (
	"encoding/json"
	"testing"
	"time"

	"unit-test-ide.local/test-service/internal/protocol"
)

func TestSupportedVersionRecognizesV10AndV11(t *testing.T) {
	if !protocol.SupportedVersion(protocol.Version10) || !protocol.SupportedVersion(protocol.Version11) {
		t.Fatal("expected protocol 1.0 and 1.1 to be supported")
	}
	if protocol.SupportedVersion("2.0") {
		t.Fatal("expected unknown protocol version to be rejected")
	}
}

func TestResponseConstructorsUseExplicitSupportedVersion(t *testing.T) {
	request := protocol.Request{MessageID: "0123456789abcdef0123456789abcdef", Method: "capabilities/get"}
	success := protocol.Success(protocol.Version11, request, map[string]bool{"accepted": true})
	if success.ProtocolVersion != protocol.Version11 || success.Kind != "response" || success.Method != request.Method {
		t.Fatalf("unexpected success response: %#v", success)
	}
	failure := protocol.Failure(protocol.Version11, request, "SERVICE_UNHEALTHY", "service unavailable", true)
	if failure.ProtocolVersion != protocol.Version11 || failure.Kind != "error" || failure.Error == nil || failure.Error.Code != "SERVICE_UNHEALTHY" {
		t.Fatalf("unexpected failure response: %#v", failure)
	}
}

func TestFailureFallsBackToV10ForUnknownVersion(t *testing.T) {
	response := protocol.Failure("2.0", protocol.Request{MessageID: "0123456789abcdef0123456789abcdef"}, "UNSUPPORTED_PROTOCOL", "protocol version is not supported", false)
	if response.ProtocolVersion != protocol.Version10 {
		t.Fatalf("unexpected fallback version: %q", response.ProtocolVersion)
	}
}

func TestNewEventBuildsV11Envelope(t *testing.T) {
	at := time.Date(2026, 7, 22, 1, 2, 3, 4, time.FixedZone("test", 8*60*60))
	payload := json.RawMessage(`{"state":"running"}`)
	event := protocol.NewEvent(42, "task.updated", "task-1", at, payload)
	if event.ProtocolVersion != protocol.Version11 || event.Kind != "event" || event.Sequence != 42 || event.Event != "task.updated" || event.TaskID != "task-1" || event.PayloadVersion != 1 {
		t.Fatalf("unexpected event: %#v", event)
	}
	if event.SentAt != "2026-07-21T17:02:03.000000004Z" || string(event.Payload) != string(payload) {
		t.Fatalf("unexpected event timing or payload: %#v", event)
	}
}

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
