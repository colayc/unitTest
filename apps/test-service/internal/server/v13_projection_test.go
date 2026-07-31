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

func TestV12ProjectsTestEventsWithoutChangingJournalIdentity(t *testing.T) {
	taskID := testID('4')
	at := time.Date(2026, 7, 31, 4, 0, 0, 0, time.UTC)
	persisted := eventForProjection(
		12,
		taskID,
		task.EventTestRunFinished,
		at,
		`{"runId":"55555555555555555555555555555555","outcome":"passed","summary":{"total":1},"resultRevision":"6666666666666666666666666666666666666666666666666666666666666666","incomplete":false}`,
	)
	event := subscribeSingleProjectedEvent(
		t,
		protocol.Version12,
		persisted,
	)
	if event.MessageID != persisted.ID ||
		event.Sequence != persisted.Sequence ||
		event.SentAt != persisted.At.Format(time.RFC3339Nano) ||
		event.Event != string(task.EventTaskOutput) ||
		string(event.Payload) !=
			`{"stepId":"test-compatibility","stream":"combined","text":"","truncated":false}` {
		t.Fatalf("projected event = %#v", event)
	}
}

func TestV13PreservesTestEventPayloadAndJournalIdentity(t *testing.T) {
	taskID := testID('7')
	at := time.Date(2026, 7, 31, 4, 30, 0, 0, time.UTC)
	payload :=
		`{"runId":"88888888888888888888888888888888","catalogRevision":"9999999999999999999999999999999999999999999999999999999999999999","total":3}`
	persisted := eventForProjection(
		13,
		taskID,
		task.EventTestRunStarted,
		at,
		payload,
	)
	event := subscribeSingleProjectedEvent(
		t,
		protocol.Version13,
		persisted,
	)
	if event.MessageID != persisted.ID ||
		event.Sequence != persisted.Sequence ||
		event.Event != string(persisted.Type) ||
		string(event.Payload) != payload {
		t.Fatalf("projected event = %#v", event)
	}
}

func TestV13AddsBuildCategoryToLegacyDiagnosticJournalEntry(
	t *testing.T,
) {
	persisted := eventForProjection(
		14,
		testID('a'),
		task.EventTaskDiagnostic,
		time.Date(2026, 7, 31, 5, 0, 0, 0, time.UTC),
		`{"diagnostic":{"severity":"warning","code":"C4996","message":"warning"}}`,
	)
	event := subscribeSingleProjectedEvent(
		t,
		protocol.Version13,
		persisted,
	)
	var payload struct {
		Diagnostic struct {
			Category string `json:"category"`
		} `json:"diagnostic"`
	}
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Diagnostic.Category != "build_error" {
		t.Fatalf("projected diagnostic = %#v", payload)
	}
}

func subscribeSingleProjectedEvent(
	t *testing.T,
	version string,
	persisted task.Event,
) protocol.Event {
	t.Helper()
	broker, err := eventbroker.New(
		projectionSource{events: []task.Event{persisted}},
		4,
		4,
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = broker.Close() })
	backend := &streamBackend{
		broker: broker,
		get: task.Task{
			ID:           persisted.TaskID,
			Kind:         task.KindTestRun,
			Status:       task.StatusRunning,
			CreatedAt:    persisted.At,
			LastSequence: persisted.Sequence,
		},
	}
	client, serviceConnection := net.Pipe()
	go server.ServeConnection(
		serviceConnection,
		session.New(
			"0123456789abcdef",
			"linux",
			"unix-socket",
			backend,
		),
	)
	t.Cleanup(func() { _ = client.Close() })
	if err := client.SetDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}
	authenticateVersionConnection(t, client, version)
	subscribePayload, _ := json.Marshal(
		map[string]any{"afterSequence": 0},
	)
	if err := json.NewEncoder(client).Encode(protocol.Request{
		ProtocolVersion: version,
		Kind:            "request",
		MessageID:       testID('d'),
		Method:          "events/subscribe",
		SentAt:          sentAt,
		Payload:         subscribePayload,
	}); err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(client)
	var response protocol.Response
	if err := decoder.Decode(&response); err != nil ||
		response.Kind != "response" {
		t.Fatalf("subscribe response = %#v, %v", response, err)
	}
	var event protocol.Event
	if err := decoder.Decode(&event); err != nil {
		t.Fatal(err)
	}
	return event
}

func authenticateVersionConnection(
	t *testing.T,
	connection net.Conn,
	version string,
) {
	t.Helper()
	payload, _ := json.Marshal(map[string]any{
		"token":                     "0123456789abcdef",
		"clientName":                "test",
		"clientVersion":             "0.4.0",
		"supportedProtocolVersions": []string{version},
	})
	response := exchange(t, connection, protocol.Request{
		ProtocolVersion: version,
		Kind:            "request",
		MessageID:       testID('c'),
		Method:          "handshake",
		SentAt:          sentAt,
		Payload:         payload,
	})
	if response.Kind != "response" ||
		response.ProtocolVersion != version {
		t.Fatalf("handshake = %#v", response)
	}
}
