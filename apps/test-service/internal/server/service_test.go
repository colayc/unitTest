package server_test

import (
	"encoding/json"
	"errors"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"unit-test-ide.local/test-service/internal/protocol"
	"unit-test-ide.local/test-service/internal/server"
	"unit-test-ide.local/test-service/internal/session"
)

type readyGenerationHandshake struct{ session.GenerationBackend }

func (readyGenerationHandshake) TestGenerationReady() bool { return true }

type readyManagedGenerationHandshake struct {
	session.ManagedGenerationBackend
	session.ManagedStartResolverBackend
	session.ManagedStartBackend
	session.ManagedRunReadBackend
}

func (readyManagedGenerationHandshake) TestGenerationReady() bool { return true }
func (readyManagedGenerationHandshake) ManagedTestsReady() bool   { return true }

type readyDetailsHandshake struct {
	session.CoverageDetailsProvider
}

func (readyDetailsHandshake) CoverageDetailsReady() bool { return true }

func TestServiceNegotiatesV15OnlyWithGenerationProvider(t *testing.T) {
	for _, tc := range []struct {
		name, want string
		generation session.GenerationBackend
	}{
		{"missing", protocol.Version14, nil},
		{"ready", protocol.Version15, readyGenerationHandshake{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			listener := newQueuedListener()
			service := server.NewServiceWithGeneration(listener, "0123456789abcdef", "linux", "unix-socket", nil, projectionCoverageBackend{}, tc.generation, server.ServiceConfig{})
			done := make(chan error, 1)
			go func() { done <- service.Serve() }()
			defer func() { service.Shutdown(); <-done }()
			client, accepted := net.Pipe()
			defer client.Close()
			listener.connections <- accepted
			payload, _ := json.Marshal(map[string]any{"token": "0123456789abcdef", "clientName": "test", "clientVersion": "0.6.0", "supportedProtocolVersions": []string{protocol.Version15, protocol.Version14}})
			response := exchange(t, client, protocol.Request{ProtocolVersion: protocol.Version15, Kind: "request", MessageID: strings.Repeat("a", 32), Method: "handshake", SentAt: sentAt, Payload: payload})
			if response.Error != nil {
				t.Fatalf("handshake = %+v", response.Error)
			}
			value, ok := response.Payload.(map[string]any)
			if !ok || value["negotiatedProtocolVersion"] != tc.want {
				t.Fatalf("negotiated = %+v, want %s", response.Payload, tc.want)
			}
		})
	}
}

func TestServiceNegotiatesV16OnlyThroughManagedDetailsConstructor(t *testing.T) {
	listener := newQueuedListener()
	generation := readyManagedGenerationHandshake{}
	service := server.NewServiceWithManagedDetails(
		listener, "0123456789abcdef", "linux", "unix-socket", nil,
		projectionCoverageBackend{}, generation, readyDetailsHandshake{}, generation,
		server.ServiceConfig{},
	)
	done := make(chan error, 1)
	go func() { done <- service.Serve() }()
	defer func() { service.Shutdown(); <-done }()
	client, accepted := net.Pipe()
	defer client.Close()
	listener.connections <- accepted
	payload, _ := json.Marshal(map[string]any{
		"token": "0123456789abcdef", "clientName": "test", "clientVersion": "0.6.0",
		"supportedProtocolVersions": []string{protocol.Version16, protocol.Version15, protocol.Version14},
	})
	response := exchange(t, client, protocol.Request{ProtocolVersion: protocol.Version16, Kind: "request", MessageID: strings.Repeat("b", 32), Method: "handshake", SentAt: sentAt, Payload: payload})
	if response.Error != nil {
		t.Fatalf("handshake = %+v", response.Error)
	}
	value, ok := response.Payload.(map[string]any)
	if !ok || value["negotiatedProtocolVersion"] != protocol.Version16 {
		t.Fatalf("negotiated = %+v, want %s", response.Payload, protocol.Version16)
	}
}

type queuedListener struct {
	connections  chan net.Conn
	accepted     chan struct{}
	closed       chan struct{}
	once         sync.Once
	beforeReturn func()
}

func newQueuedListener() *queuedListener {
	return &queuedListener{connections: make(chan net.Conn), accepted: make(chan struct{}, 8), closed: make(chan struct{})}
}

func (l *queuedListener) Accept() (net.Conn, error) {
	select {
	case connection := <-l.connections:
		l.accepted <- struct{}{}
		if l.beforeReturn != nil {
			l.beforeReturn()
		}
		return connection, nil
	case <-l.closed:
		return nil, net.ErrClosed
	}
}

func TestServiceClosesConnectionAcceptedDuringShutdown(t *testing.T) {
	listener := newQueuedListener()
	service := server.NewService(listener, "0123456789abcdef", "linux", "unix-socket", nil, server.ServiceConfig{
		MaxConnections: 1,
		Connection: server.ConnectionConfig{
			HandshakeTimeout: time.Minute,
			IdleTimeout:      time.Minute,
			WriteTimeout:     time.Second,
		},
	})
	listener.beforeReturn = service.Shutdown
	done := make(chan error, 1)
	go func() { done <- service.Serve() }()

	client, accepted := net.Pipe()
	defer client.Close()
	listener.connections <- accepted
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("service did not close a connection returned by Accept during shutdown")
	}
}
func (l *queuedListener) Close() error   { l.once.Do(func() { close(l.closed) }); return nil }
func (l *queuedListener) Addr() net.Addr { return stubAddr("test") }

type stubAddr string

func (a stubAddr) Network() string { return string(a) }
func (a stubAddr) String() string  { return string(a) }

func TestServiceConnectionLimitExhaustionAndRecovery(t *testing.T) {
	listener := newQueuedListener()
	service := server.NewService(listener, "0123456789abcdef", "linux", "unix-socket", nil, server.ServiceConfig{MaxConnections: 1})
	done := make(chan error, 1)
	go func() { done <- service.Serve() }()

	client1, server1 := net.Pipe()
	listener.connections <- server1
	<-listener.accepted
	client2, server2 := net.Pipe()
	defer client2.Close()
	sentSecond := make(chan struct{})
	go func() { listener.connections <- server2; close(sentSecond) }()
	select {
	case <-listener.accepted:
		t.Fatal("second connection accepted while limit was exhausted")
	case <-time.After(25 * time.Millisecond):
	}
	_ = client1.Close()
	select {
	case <-listener.accepted:
	case <-time.After(time.Second):
		t.Fatal("connection capacity was not recovered")
	}
	<-sentSecond
	service.Shutdown()
	select {
	case err := <-done:
		if err != nil && !errors.Is(err, net.ErrClosed) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("service did not close active connections and wait for handlers")
	}
}

func TestServiceSharesBackendAcrossIndependentSessions(t *testing.T) {
	listener := newQueuedListener()
	backend := newStreamBackend(t, 8)
	service := server.NewService(listener, "0123456789abcdef", "linux", "unix-socket", backend, server.ServiceConfig{MaxConnections: 2})
	done := make(chan error, 1)
	go func() { done <- service.Serve() }()

	for index := range 2 {
		client, accepted := net.Pipe()
		listener.connections <- accepted
		<-listener.accepted
		authenticateV11Connection(t, client)
		payload := []byte(`{"taskId":"11111111111111111111111111111111"}`)
		response := exchange(t, client, protocol.Request{ProtocolVersion: protocol.Version11, Kind: "request", MessageID: strings.Repeat(string(rune('c'+index)), 32), Method: "tasks/get", SentAt: sentAt, Payload: payload})
		if response.Kind != "response" {
			t.Fatalf("response=%#v", response)
		}
		_ = client.Close()
	}
	if backend.getCalls.Load() != 2 {
		t.Fatalf("backend get calls=%d", backend.getCalls.Load())
	}
	service.Shutdown()
	select {
	case err := <-done:
		if err != nil && !errors.Is(err, net.ErrClosed) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("service did not stop")
	}
}
