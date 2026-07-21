package protocol_test

import (
	"testing"

	"unit-test-ide.local/test-service/internal/protocol"
)

func TestDecodeRequestRejectsUnknownField(t *testing.T) {
	_, err := protocol.DecodeRequest([]byte(`{"protocolVersion":"1.0","kind":"request","messageId":"0123456789abcdef0123456789abcdef","method":"shutdown","payload":{},"unknown":true}`))
	if err == nil {
		t.Fatal("expected unknown field to fail")
	}
}

func TestDecodeRequestRejectsMissingMessageID(t *testing.T) {
	_, err := protocol.DecodeRequest([]byte(`{"protocolVersion":"1.0","kind":"request","method":"shutdown","payload":{}}`))
	if err == nil {
		t.Fatal("expected missing messageId to fail")
	}
}

func TestDecodeRequestRejectsTrailingJSON(t *testing.T) {
	_, err := protocol.DecodeRequest([]byte(`{"protocolVersion":"1.0","kind":"request","messageId":"0123456789abcdef0123456789abcdef","method":"shutdown","payload":{}} {}`))
	if err == nil {
		t.Fatal("expected trailing JSON to fail")
	}
}
