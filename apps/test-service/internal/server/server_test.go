package server_test

import (
	"encoding/json"
	"net"
	"strings"
	"testing"

	"unit-test-ide.local/test-service/internal/protocol"
	"unit-test-ide.local/test-service/internal/server"
	"unit-test-ide.local/test-service/internal/session"
)

const sentAt = "2026-07-21T00:00:00Z"

func exchange(t *testing.T, connection net.Conn, request protocol.Request) protocol.Response {
	t.Helper()
	if err := json.NewEncoder(connection).Encode(request); err != nil {
		t.Fatal(err)
	}
	var response protocol.Response
	if err := json.NewDecoder(connection).Decode(&response); err != nil {
		t.Fatal(err)
	}
	return response
}

func TestServeConnectionHandlesHandshakeAndShutdown(t *testing.T) {
	client, service := net.Pipe()
	active := session.New("0123456789abcdef", "linux", "unix-socket")
	go server.ServeConnection(service, active)
	defer client.Close()
	handshake, _ := json.Marshal(map[string]string{"token": "0123456789abcdef", "clientName": "test", "clientVersion": "0.1.0"})
	response := exchange(t, client, protocol.Request{ProtocolVersion: "1.0", Kind: "request", MessageID: "0123456789abcdef0123456789abcdef", Method: "handshake", SentAt: sentAt, Payload: handshake})
	if response.Kind != "response" {
		t.Fatalf("handshake failed: %#v", response)
	}
	shutdown := exchange(t, client, protocol.Request{ProtocolVersion: "1.0", Kind: "request", MessageID: "fedcba9876543210fedcba9876543210", Method: "shutdown", SentAt: sentAt, Payload: json.RawMessage(`{}`)})
	if shutdown.Kind != "response" {
		t.Fatalf("shutdown failed: %#v", shutdown)
	}
}

func TestServeConnectionRejectsOversizedLine(t *testing.T) {
	client, service := net.Pipe()
	go server.ServeConnection(service, session.New("0123456789abcdef", "linux", "unix-socket"))
	defer client.Close()
	line := requestLineOfSize(t, server.MaxMessageBytes+1)
	go func() { _, _ = client.Write(append(line, '\n')) }()
	var response protocol.Response
	if err := json.NewDecoder(client).Decode(&response); err != nil {
		t.Fatal(err)
	}
	if response.Error == nil || response.Error.Code != "INVALID_MESSAGE" {
		t.Fatalf("unexpected response: %#v", response)
	}
}

func TestServeConnectionAcceptsLineAtMaximumSize(t *testing.T) {
	client, service := net.Pipe()
	go server.ServeConnection(service, session.New("0123456789abcdef", "linux", "unix-socket"))
	defer client.Close()
	line := requestLineOfSize(t, server.MaxMessageBytes)
	go func() { _, _ = client.Write(append(line, '\n')) }()
	var response protocol.Response
	if err := json.NewDecoder(client).Decode(&response); err != nil {
		t.Fatal(err)
	}
	if response.Error == nil || response.Error.Code != "AUTH_REQUIRED" {
		t.Fatalf("expected decoded request to reach the session, got %#v", response)
	}
}

func requestLineOfSize(t *testing.T, size int) []byte {
	t.Helper()
	request := protocol.Request{
		ProtocolVersion: protocol.Version,
		Kind:            "request",
		MessageID:       "0123456789abcdef0123456789abcdef",
		Method:          "capabilities/get",
		SentAt:          sentAt,
		Payload:         json.RawMessage(`{"padding":""}`),
	}
	encoded, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	paddingSize := size - len(encoded)
	if paddingSize < 0 {
		t.Fatalf("requested line size %d is smaller than envelope size %d", size, len(encoded))
	}
	request.Payload = json.RawMessage(`{"padding":"` + strings.Repeat("x", paddingSize) + `"}`)
	encoded, err = json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	if len(encoded) != size {
		t.Fatalf("line size = %d, want %d", len(encoded), size)
	}
	return encoded
}
