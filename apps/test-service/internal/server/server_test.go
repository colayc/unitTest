package server_test

import (
	"context"
	"encoding/json"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"unit-test-ide.local/test-service/internal/eventbroker"
	"unit-test-ide.local/test-service/internal/protocol"
	"unit-test-ide.local/test-service/internal/server"
	"unit-test-ide.local/test-service/internal/session"
	"unit-test-ide.local/test-service/internal/task"
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

type streamSource struct{}

func (streamSource) Watermark(context.Context) (int64, error) { return 0, nil }
func (streamSource) EventsAfter(context.Context, int64, int64, int) ([]task.Event, error) {
	return nil, nil
}

type replaySource struct {
	events    []task.Event
	mu        sync.Mutex
	subscribe int
	requested []chan struct{}
	gates     []chan struct{}
}

func (s *replaySource) Watermark(context.Context) (int64, error) {
	return int64(len(s.events)), nil
}

func (s *replaySource) EventsAfter(ctx context.Context, after, through int64, limit int) ([]task.Event, error) {
	if after == 0 {
		s.mu.Lock()
		subscribe := s.subscribe
		s.subscribe++
		close(s.requested[subscribe])
		gate := s.gates[subscribe]
		s.mu.Unlock()
		select {
		case <-gate:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	time.Sleep(5 * time.Millisecond)
	result := make([]task.Event, 0, limit)
	for _, event := range s.events {
		if event.Sequence > after && event.Sequence <= through {
			result = append(result, event)
			if len(result) == limit {
				break
			}
		}
	}
	return result, nil
}

type streamBackend struct {
	broker           *eventbroker.Broker
	subscription     *eventbroker.Subscription
	get              task.Task
	getCalls         atomic.Int32
	subscribeContext chan struct{}
	replayRequested  []chan struct{}
	replayGates      []chan struct{}
}

func (b *streamBackend) Start(context.Context, task.StartRequest) (task.Task, error) {
	return task.Task{}, nil
}
func (b *streamBackend) Get(context.Context, string) (task.Task, error) {
	b.getCalls.Add(1)
	return b.get, nil
}
func (b *streamBackend) List(context.Context, string, int) (task.Page[task.Task], error) {
	return task.Page[task.Task]{}, nil
}
func (b *streamBackend) Cancel(context.Context, string) (task.Task, error) { return task.Task{}, nil }
func (b *streamBackend) Subscribe(ctx context.Context, after int64) (*eventbroker.Subscription, error) {
	if b.subscription != nil {
		return b.subscription, nil
	}
	subscription, err := b.broker.Subscribe(ctx, after)
	if err != nil {
		return nil, err
	}
	if b.subscribeContext != nil {
		go func() { <-ctx.Done(); close(b.subscribeContext) }()
	}
	return subscription, nil
}
func (b *streamBackend) ListArtifacts(context.Context, string, string, int) (task.Page[task.Artifact], error) {
	return task.Page[task.Artifact]{}, nil
}
func (b *streamBackend) ReadArtifact(context.Context, string, int64, int) (session.ArtifactChunk, error) {
	return session.ArtifactChunk{}, nil
}

func testID(value byte) string { return strings.Repeat(string(value), 32) }

func newStreamBackend(t *testing.T, queueSize int) *streamBackend {
	t.Helper()
	broker, err := eventbroker.New(streamSource{}, queueSize, 10)
	if err != nil {
		t.Fatal(err)
	}
	return &streamBackend{broker: broker, get: task.Task{ID: testID('1'), Scenario: task.ScenarioHang, Timeout: time.Second, Status: task.StatusRunning, CreatedAt: time.Now().UTC(), LastSequence: 2}}
}

func newReplayBackend(t *testing.T, eventCount, queueSize int) *streamBackend {
	t.Helper()
	events := make([]task.Event, eventCount)
	for index := range events {
		sequence := int64(index + 1)
		events[index] = task.Event{Sequence: sequence, ID: testID('e'), EventDraft: task.EventDraft{
			TaskID: testID('1'), Type: task.EventTaskOutput, At: time.Unix(sequence, 0).UTC(), Payload: json.RawMessage(`{"stream":"stdout"}`),
		}}
	}
	requested := []chan struct{}{make(chan struct{}), make(chan struct{})}
	gates := []chan struct{}{make(chan struct{}), make(chan struct{})}
	source := &replaySource{events: events, requested: requested, gates: gates}
	broker, err := eventbroker.New(source, queueSize, queueSize)
	if err != nil {
		t.Fatal(err)
	}
	return &streamBackend{
		broker: broker, replayRequested: requested, replayGates: gates,
		get: task.Task{ID: testID('1'), Scenario: task.ScenarioHang, Timeout: time.Second, Status: task.StatusRunning, CreatedAt: time.Now().UTC(), LastSequence: int64(eventCount)},
	}
}

func authenticateV11Connection(t *testing.T, connection net.Conn) {
	t.Helper()
	payload, _ := json.Marshal(map[string]any{
		"token": "0123456789abcdef", "clientName": "test", "clientVersion": "0.2.0",
		"supportedProtocolVersions": []string{protocol.Version11},
	})
	response := exchange(t, connection, protocol.Request{ProtocolVersion: protocol.Version11, Kind: "request", MessageID: testID('a'), Method: "handshake", SentAt: sentAt, Payload: payload})
	if response.Kind != "response" {
		t.Fatalf("handshake failed: %#v", response)
	}
}

type serialWriteConn struct {
	net.Conn
	writing    atomic.Int32
	concurrent atomic.Bool
}

func (c *serialWriteConn) Write(value []byte) (int, error) {
	if c.writing.Add(1) != 1 {
		c.concurrent.Store(true)
	}
	defer c.writing.Add(-1)
	return c.Conn.Write(value)
}

func TestServeConnectionSendsSubscribeResponseBeforeEventsAndSerializesWrites(t *testing.T) {
	client, rawService := net.Pipe()
	probe := &serialWriteConn{Conn: rawService}
	backend := newStreamBackend(t, 8)
	active := session.New("0123456789abcdef", "linux", "unix-socket", backend)
	go server.ServeConnection(probe, active)
	defer client.Close()
	authenticateV11Connection(t, client)

	subscribePayload, _ := json.Marshal(map[string]any{"afterSequence": 0})
	if err := json.NewEncoder(client).Encode(protocol.Request{ProtocolVersion: protocol.Version11, Kind: "request", MessageID: testID('b'), Method: "events/subscribe", SentAt: sentAt, Payload: subscribePayload}); err != nil {
		t.Fatal(err)
	}
	var first protocol.Response
	if err := json.NewDecoder(client).Decode(&first); err != nil {
		t.Fatal(err)
	}
	if first.Kind != "response" || first.Method != "events/subscribe" {
		t.Fatalf("first envelope=%#v", first)
	}

	backend.broker.Publish(task.Event{Sequence: 3, ID: testID('e'), EventDraft: task.EventDraft{TaskID: testID('1'), Type: task.EventTaskStarted, At: time.Now().UTC(), Payload: json.RawMessage(`{"status":"running"}`)}})
	getPayload, _ := json.Marshal(map[string]any{"taskId": testID('1')})
	go func() {
		_ = json.NewEncoder(client).Encode(protocol.Request{ProtocolVersion: protocol.Version11, Kind: "request", MessageID: testID('c'), Method: "tasks/get", SentAt: sentAt, Payload: getPayload})
	}()

	decoder := json.NewDecoder(client)
	kinds := map[string]bool{}
	for range 2 {
		var envelope struct {
			Kind     string `json:"kind"`
			Sequence int64  `json:"sequence"`
			Method   string `json:"method"`
		}
		if err := decoder.Decode(&envelope); err != nil {
			t.Fatal(err)
		}
		kinds[envelope.Kind] = true
		if envelope.Kind == "event" && envelope.Sequence != 3 {
			t.Fatalf("event=%#v", envelope)
		}
		if envelope.Kind == "response" && envelope.Method != "tasks/get" {
			t.Fatalf("response=%#v", envelope)
		}
	}
	if !kinds["event"] || !kinds["response"] || probe.concurrent.Load() {
		t.Fatalf("kinds=%v concurrent=%v", kinds, probe.concurrent.Load())
	}
}

func TestServeConnectionDrainsReplayLargerThanBrokerQueueForDuplicateSubscriptions(t *testing.T) {
	client, serviceConn := net.Pipe()
	backend := newReplayBackend(t, 10, 2)
	go server.ServeConnection(serviceConn, session.New("0123456789abcdef", "linux", "unix-socket", backend))
	defer client.Close()
	_ = client.SetDeadline(time.Now().Add(5 * time.Second))
	authenticateV11Connection(t, client)
	encoder, decoder := json.NewEncoder(client), json.NewDecoder(client)

	for attempt := range 2 {
		payload, _ := json.Marshal(map[string]any{"afterSequence": 0})
		requestID := strings.Repeat(string(rune('b'+attempt)), 32)
		if err := encoder.Encode(protocol.Request{ProtocolVersion: protocol.Version11, Kind: "request", MessageID: requestID, Method: "events/subscribe", SentAt: sentAt, Payload: payload}); err != nil {
			t.Fatal(err)
		}
		select {
		case <-backend.replayRequested[attempt]:
		case <-time.After(time.Second):
			t.Fatalf("attempt %d replay did not start", attempt)
		}
		time.Sleep(25 * time.Millisecond)
		close(backend.replayGates[attempt])
		time.Sleep(50 * time.Millisecond)
		var response protocol.Response
		if err := decoder.Decode(&response); err != nil {
			t.Fatal(err)
		}
		if response.Kind != "response" || response.Method != "events/subscribe" {
			t.Fatalf("attempt %d first envelope=%#v", attempt, response)
		}
		for want := int64(1); want <= 10; want++ {
			var envelope struct {
				Kind     string              `json:"kind"`
				Sequence int64               `json:"sequence"`
				Error    *protocol.ErrorBody `json:"error"`
			}
			if err := decoder.Decode(&envelope); err != nil {
				t.Fatalf("attempt %d sequence %d: %v", attempt, want, err)
			}
			if envelope.Error != nil || envelope.Kind != "event" || envelope.Sequence != want {
				t.Fatalf("attempt %d want sequence %d, envelope=%#v", attempt, want, envelope)
			}
		}
	}
}

func TestServeConnectionMapsSubscriptionErrorAndCloses(t *testing.T) {
	client, serviceConn := net.Pipe()
	events := make(chan task.Event)
	errorsChannel := make(chan error, 1)
	backend := &streamBackend{subscription: &eventbroker.Subscription{Events: events, Errors: errorsChannel}}
	go server.ServeConnection(serviceConn, session.New("0123456789abcdef", "linux", "unix-socket", backend))
	defer client.Close()
	authenticateV11Connection(t, client)
	subscribePayload, _ := json.Marshal(map[string]any{"afterSequence": 0})
	if err := json.NewEncoder(client).Encode(protocol.Request{ProtocolVersion: protocol.Version11, Kind: "request", MessageID: testID('b'), Method: "events/subscribe", SentAt: sentAt, Payload: subscribePayload}); err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(client)
	var response protocol.Response
	if err := decoder.Decode(&response); err != nil || response.Kind != "response" {
		t.Fatalf("response=%#v err=%v", response, err)
	}
	errorsChannel <- eventbroker.ErrSubscriberTooSlow
	close(errorsChannel)
	close(events)
	sawError := false
	for range 3 {
		var envelope protocol.Response
		err := decoder.Decode(&envelope)
		if err != nil {
			break
		}
		if envelope.Error != nil {
			if envelope.Error.Code != "SUBSCRIBER_TOO_SLOW" || !envelope.Error.Retryable {
				t.Fatalf("error envelope=%#v", envelope)
			}
			sawError = true
			break
		}
	}
	if !sawError {
		t.Fatal("connection closed without reporting subscriber-too-slow")
	}
	var afterClose json.RawMessage
	if err := decoder.Decode(&afterClose); err == nil {
		t.Fatalf("connection remained open with envelope %s", afterClose)
	}
}

func TestServeConnectionCancelsSubscriptionOnClientDisconnect(t *testing.T) {
	client, serviceConn := net.Pipe()
	backend := newStreamBackend(t, 8)
	backend.subscribeContext = make(chan struct{})
	go server.ServeConnection(serviceConn, session.New("0123456789abcdef", "linux", "unix-socket", backend))
	authenticateV11Connection(t, client)
	subscribePayload, _ := json.Marshal(map[string]any{"afterSequence": 0})
	_ = exchange(t, client, protocol.Request{ProtocolVersion: protocol.Version11, Kind: "request", MessageID: testID('b'), Method: "events/subscribe", SentAt: sentAt, Payload: subscribePayload})
	_ = client.Close()
	select {
	case <-backend.subscribeContext:
	case <-time.After(time.Second):
		t.Fatal("subscription context was not cancelled")
	}
}

func TestServeConnectionClearsIdleDeadlineWhileSubscribed(t *testing.T) {
	client, rawService := net.Pipe()
	tracked := &deadlineConn{Conn: rawService}
	backend := newStreamBackend(t, 8)
	go server.ServeConnectionWithConfig(tracked, session.New("0123456789abcdef", "linux", "unix-socket", backend), server.ConnectionConfig{HandshakeTimeout: time.Second, IdleTimeout: time.Second, WriteTimeout: time.Second})
	defer client.Close()
	authenticateV11Connection(t, client)
	subscribePayload, _ := json.Marshal(map[string]any{"afterSequence": 0})
	_ = exchange(t, client, protocol.Request{ProtocolVersion: protocol.Version11, Kind: "request", MessageID: testID('b'), Method: "events/subscribe", SentAt: sentAt, Payload: subscribePayload})
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		tracked.mu.Lock()
		cleared := false
		for _, value := range tracked.reads {
			cleared = cleared || value.IsZero()
		}
		tracked.mu.Unlock()
		if cleared {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("read idle deadline was not cleared for active subscription")
}
