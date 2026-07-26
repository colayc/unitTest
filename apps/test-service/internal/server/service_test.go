package server_test

import (
	"errors"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"unit-test-ide.local/test-service/internal/protocol"
	"unit-test-ide.local/test-service/internal/server"
)

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
