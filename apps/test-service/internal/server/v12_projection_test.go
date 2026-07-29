package server_test

import (
	"encoding/json"
	"net"
	"testing"
	"time"

	"unit-test-ide.local/test-service/internal/eventbroker"
	"unit-test-ide.local/test-service/internal/protocol"
	"unit-test-ide.local/test-service/internal/server"
	"unit-test-ide.local/test-service/internal/session"
	"unit-test-ide.local/test-service/internal/task"
)

func TestServeConnectionV11ProjectsDiagnosticWithoutChangingSequence(t *testing.T) {
	taskID := testID('1')
	at := time.Date(2026, 7, 29, 1, 0, 0, 0, time.UTC)
	broker, err := eventbroker.New(projectionSource{events: []task.Event{
		eventForProjection(7, taskID, task.EventTaskDiagnostic, at, `{"diagnostic":{"severity":"warning","code":"C123","message":"warning"}}`),
	}}, 4, 4)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = broker.Close() })
	backend := &streamBackend{broker: broker, get: task.Task{
		ID: taskID, Kind: task.KindSimulation, Scenario: task.ScenarioHang,
		Timeout: time.Second, Status: task.StatusRunning, CreatedAt: at, LastSequence: 7,
	}}
	_, decoder := openV11Subscription(t, backend)
	assertProjectedOutputEvent(t, decoder, 7, taskID, "service", "", false)
}

func TestServeConnectionV12PreservesDiagnosticEvent(t *testing.T) {
	taskID := testID('1')
	at := time.Date(2026, 7, 29, 2, 0, 0, 0, time.UTC)
	payload := `{"diagnostic":{"severity":"warning","code":"C123","message":"warning"}}`
	broker, err := eventbroker.New(projectionSource{events: []task.Event{
		eventForProjection(9, taskID, task.EventTaskDiagnostic, at, payload),
	}}, 4, 4)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = broker.Close() })
	backend := &streamBackend{broker: broker, get: task.Task{
		ID: taskID, Kind: task.KindCMakeBuild, Timeout: time.Second,
		Status: task.StatusRunning, CreatedAt: at, LastSequence: 9,
	}}

	client, serviceConn := net.Pipe()
	go server.ServeConnection(serviceConn, session.New("0123456789abcdef", "linux", "unix-socket", backend))
	t.Cleanup(func() { _ = client.Close() })
	if err := client.SetDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}
	authenticateV12Connection(t, client)

	subscribePayload, _ := json.Marshal(map[string]any{"afterSequence": 0})
	if err := json.NewEncoder(client).Encode(protocol.Request{
		ProtocolVersion: protocol.Version12, Kind: "request", MessageID: testID('b'),
		Method: "events/subscribe", SentAt: sentAt, Payload: subscribePayload,
	}); err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(client)
	var response protocol.Response
	if err := decoder.Decode(&response); err != nil || response.Kind != "response" {
		t.Fatalf("subscribe response = %#v, %v", response, err)
	}
	var event protocol.Event
	if err := decoder.Decode(&event); err != nil {
		t.Fatal(err)
	}
	if event.ProtocolVersion != protocol.Version12 || event.Event != string(task.EventTaskDiagnostic) ||
		event.Sequence != 9 || event.TaskID != taskID || string(event.Payload) != payload {
		t.Fatalf("event = %#v", event)
	}
}

func authenticateV12Connection(t *testing.T, connection net.Conn) {
	t.Helper()
	payload, _ := json.Marshal(map[string]any{
		"token": "0123456789abcdef", "clientName": "test", "clientVersion": "0.3.0",
		"supportedProtocolVersions": []string{protocol.Version12, protocol.Version11, protocol.Version10},
	})
	response := exchange(t, connection, protocol.Request{
		ProtocolVersion: protocol.Version12, Kind: "request", MessageID: testID('a'),
		Method: "handshake", SentAt: sentAt, Payload: payload,
	})
	if response.Kind != "response" || response.ProtocolVersion != protocol.Version12 {
		t.Fatalf("handshake = %#v", response)
	}
}
