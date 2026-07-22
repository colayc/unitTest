package server_test

import (
	"encoding/json"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

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
	active := session.New("0123456789abcdef", "linux", "unix-socket", nil)
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

func TestServeConnectionRejectsNonEmptyAuthenticatedPayload(t *testing.T) {
	client, service := net.Pipe()
	active := session.New("0123456789abcdef", "linux", "unix-socket", nil)
	go server.ServeConnection(service, active)
	defer client.Close()
	handshake, _ := json.Marshal(map[string]string{"token": "0123456789abcdef", "clientName": "test", "clientVersion": "0.1.0"})
	_ = exchange(t, client, protocol.Request{ProtocolVersion: "1.0", Kind: "request", MessageID: "0123456789abcdef0123456789abcdef", Method: "handshake", SentAt: sentAt, Payload: handshake})
	response := exchange(t, client, protocol.Request{ProtocolVersion: "1.0", Kind: "request", MessageID: "fedcba9876543210fedcba9876543210", Method: "capabilities/get", SentAt: sentAt, Payload: json.RawMessage(`{"unexpected":true}`)})
	if response.Error == nil || response.Error.Code != "INVALID_MESSAGE" {
		t.Fatalf("unexpected response: %#v", response)
	}
}

type deadlineConn struct {
	net.Conn
	mu     sync.Mutex
	reads  []time.Time
	writes []time.Time
}

func (c *deadlineConn) SetReadDeadline(value time.Time) error {
	c.mu.Lock()
	c.reads = append(c.reads, value)
	c.mu.Unlock()
	return c.Conn.SetReadDeadline(value)
}

func (c *deadlineConn) SetWriteDeadline(value time.Time) error {
	c.mu.Lock()
	c.writes = append(c.writes, value)
	c.mu.Unlock()
	return c.Conn.SetWriteDeadline(value)
}

func TestServeConnectionAppliesHandshakeIdleAndWriteDeadlines(t *testing.T) {
	client, rawService := net.Pipe()
	service := &deadlineConn{Conn: rawService}
	active := session.New("0123456789abcdef", "linux", "unix-socket", nil)
	done := make(chan struct{})
	go func() {
		server.ServeConnectionWithConfig(service, active, server.ConnectionConfig{
			HandshakeTimeout: time.Second,
			IdleTimeout:      2 * time.Second,
			WriteTimeout:     3 * time.Second,
		})
		close(done)
	}()
	handshake, _ := json.Marshal(map[string]string{"token": "0123456789abcdef", "clientName": "test", "clientVersion": "0.1.0"})
	_ = exchange(t, client, protocol.Request{ProtocolVersion: "1.0", Kind: "request", MessageID: "0123456789abcdef0123456789abcdef", Method: "handshake", SentAt: sentAt, Payload: handshake})
	_ = exchange(t, client, protocol.Request{ProtocolVersion: "1.0", Kind: "request", MessageID: "fedcba9876543210fedcba9876543210", Method: "shutdown", SentAt: sentAt, Payload: json.RawMessage(`{}`)})
	_ = client.Close()
	<-done
	service.mu.Lock()
	defer service.mu.Unlock()
	if len(service.reads) < 2 {
		t.Fatalf("read deadlines = %d, want handshake and authenticated idle deadlines", len(service.reads))
	}
	if len(service.writes) < 2 {
		t.Fatalf("write deadlines = %d, want one per response", len(service.writes))
	}
	firstReadWindow := service.reads[0].Sub(time.Now())
	secondReadWindow := service.reads[1].Sub(time.Now())
	if firstReadWindow <= 0 || firstReadWindow > time.Second {
		t.Fatalf("handshake deadline window = %v", firstReadWindow)
	}
	if secondReadWindow <= time.Second || secondReadWindow > 2*time.Second {
		t.Fatalf("idle deadline window = %v", secondReadWindow)
	}
}

func TestServeConnectionDoesNotExtendHandshakeDeadline(t *testing.T) {
	client, rawService := net.Pipe()
	service := &deadlineConn{Conn: rawService}
	active := session.New("0123456789abcdef", "linux", "unix-socket", nil)
	done := make(chan struct{})
	go func() {
		server.ServeConnectionWithConfig(service, active, server.ConnectionConfig{
			HandshakeTimeout: time.Second,
			IdleTimeout:      2 * time.Second,
			WriteTimeout:     time.Second,
		})
		close(done)
	}()

	unauthenticated := protocol.Request{ProtocolVersion: "1.0", Kind: "request", MessageID: "0123456789abcdef0123456789abcdef", Method: "capabilities/get", SentAt: sentAt, Payload: json.RawMessage(`{}`)}
	_ = exchange(t, client, unauthenticated)
	handshake, _ := json.Marshal(map[string]string{"token": "0123456789abcdef", "clientName": "test", "clientVersion": "0.1.0"})
	_ = exchange(t, client, protocol.Request{ProtocolVersion: "1.0", Kind: "request", MessageID: "fedcba9876543210fedcba9876543210", Method: "handshake", SentAt: sentAt, Payload: handshake})
	_ = client.Close()
	<-done

	service.mu.Lock()
	defer service.mu.Unlock()
	if len(service.reads) < 3 {
		t.Fatalf("read deadlines = %d, want two handshake reads and one authenticated idle read", len(service.reads))
	}
	if !service.reads[0].Equal(service.reads[1]) {
		t.Fatalf("handshake deadline was extended from %v to %v", service.reads[0], service.reads[1])
	}
	if !service.reads[2].After(service.reads[1]) {
		t.Fatalf("authenticated idle deadline %v does not follow handshake deadline %v", service.reads[2], service.reads[1])
	}
}

func TestServeConnectionRejectsOversizedLine(t *testing.T) {
	client, service := net.Pipe()
	go server.ServeConnection(service, session.New("0123456789abcdef", "linux", "unix-socket", nil))
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
	go server.ServeConnection(service, session.New("0123456789abcdef", "linux", "unix-socket", nil))
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
