package session_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"

	"unit-test-ide.local/test-service/internal/artifactstore"
	"unit-test-ide.local/test-service/internal/eventbroker"
	"unit-test-ide.local/test-service/internal/protocol"
	"unit-test-ide.local/test-service/internal/protocolmodel"
	"unit-test-ide.local/test-service/internal/session"
	"unit-test-ide.local/test-service/internal/task"
)

var fixedTime = time.Date(2026, 7, 22, 3, 4, 5, 0, time.UTC)

func id(digit byte) string {
	return string(make([]byte, 0)) + string(bytesOf(digit, 32))
}

func bytesOf(value byte, count int) []byte {
	result := make([]byte, count)
	for index := range result {
		result[index] = value
	}
	return result
}

type fakeBackend struct {
	startKey      string
	startScenario task.Scenario
	startTimeout  time.Duration
	getID         string
	listCursor    string
	listLimit     int
	cancelID      string
	after         int64
	artifactTask  string
	artifactCur   string
	artifactLim   int
	artifactID    string
	offset        int64
	length        int

	startResult  task.Task
	getResult    task.Task
	listResult   task.Page[task.Task]
	cancelResult task.Task
	subscription *eventbroker.Subscription
	subscribe    func(context.Context, int64) (*eventbroker.Subscription, error)
	artifacts    task.Page[task.Artifact]
	chunk        session.ArtifactChunk
	err          error
}

func (b *fakeBackend) StartSimulation(_ context.Context, idempotencyKey string, scenario task.Scenario, timeout time.Duration) (task.Task, error) {
	b.startKey, b.startScenario, b.startTimeout = idempotencyKey, scenario, timeout
	return b.startResult, b.err
}

func (b *fakeBackend) Get(_ context.Context, taskID string) (task.Task, error) {
	b.getID = taskID
	return b.getResult, b.err
}

func (b *fakeBackend) List(_ context.Context, cursor string, limit int) (task.Page[task.Task], error) {
	b.listCursor, b.listLimit = cursor, limit
	return b.listResult, b.err
}

func (b *fakeBackend) Cancel(_ context.Context, taskID string) (task.Task, error) {
	b.cancelID = taskID
	return b.cancelResult, b.err
}

func (b *fakeBackend) Subscribe(ctx context.Context, after int64) (*eventbroker.Subscription, error) {
	b.after = after
	if b.subscribe != nil {
		return b.subscribe(ctx, after)
	}
	return b.subscription, b.err
}

func (b *fakeBackend) ListArtifacts(_ context.Context, taskID, cursor string, limit int) (task.Page[task.Artifact], error) {
	b.artifactTask, b.artifactCur, b.artifactLim = taskID, cursor, limit
	return b.artifacts, b.err
}

func (b *fakeBackend) ReadArtifact(_ context.Context, artifactID string, offset int64, length int) (session.ArtifactChunk, error) {
	b.artifactID, b.offset, b.length = artifactID, offset, length
	return b.chunk, b.err
}

func authenticatedV11(t *testing.T, backend session.Backend) *session.Session {
	t.Helper()
	s := session.New("0123456789abcdef", "linux", "unix-socket", backend)
	result := s.Handle(context.Background(), requestVersion(t, protocol.Version11, "handshake", map[string]any{
		"token": "0123456789abcdef", "clientName": "test", "clientVersion": "0.2.0",
		"supportedProtocolVersions": []string{protocol.Version11},
	}))
	if result.Response.Kind != "response" {
		t.Fatalf("handshake failed: %#v", result)
	}
	return s
}

func TestSessionRoutesControlledTaskStart(t *testing.T) {
	backend := &fakeBackend{startResult: task.Task{ID: id('1'), Scenario: task.ScenarioHang, Status: task.StatusRunning, CreatedAt: fixedTime, LastSequence: 2}}
	s := authenticatedV11(t, backend)
	result := s.Handle(context.Background(), requestVersion(t, "1.1", "tasks/start", map[string]any{
		"idempotencyKey": id('2'), "scenario": "hang", "timeoutMs": 30000,
	}))
	if result.Response.Kind != "response" || backend.startKey != id('2') ||
		backend.startScenario != task.ScenarioHang || backend.startTimeout != 30*time.Second {
		t.Fatalf("result=%#v start=(%q, %q, %s)", result, backend.startKey, backend.startScenario, backend.startTimeout)
	}
	snapshot, ok := result.Response.Payload.(protocolmodel.TaskSnapshot)
	if !ok || snapshot.TaskID != id('1') || snapshot.Outcome != nil {
		t.Fatalf("payload=%#v", result.Response.Payload)
	}
}

func TestSessionRoutesRemainingPhase2Methods(t *testing.T) {
	finished := fixedTime.Add(time.Minute)
	artifact := task.Artifact{ID: id('a'), TaskID: id('1'), Kind: "task-summary", MIMEType: "application/json", Size: 3, SHA256: string(bytesOf('b', 64)), CreatedAt: fixedTime}
	taskValue := task.Task{ID: id('1'), Scenario: task.ScenarioSuccess, Timeout: time.Second, Status: task.StatusFinished, Outcome: task.OutcomeSucceeded, CreatedAt: fixedTime, FinishedAt: &finished, LastSequence: 4}
	backend := &fakeBackend{
		getResult: taskValue, cancelResult: taskValue,
		listResult: task.Page[task.Task]{Items: []task.Task{taskValue}, NextCursor: "next-task"},
		artifacts:  task.Page[task.Artifact]{Items: []task.Artifact{artifact}, NextCursor: "next-artifact"},
		chunk:      session.ArtifactChunk{Data: []byte{0xfb, 0xff}, NextOffset: 2, EOF: true, Metadata: artifact},
	}
	s := authenticatedV11(t, backend)

	tests := []struct {
		method  string
		payload map[string]any
		check   func(*testing.T, session.HandleResult)
	}{
		{"tasks/get", map[string]any{"taskId": id('1')}, func(t *testing.T, result session.HandleResult) {
			if backend.getID != id('1') {
				t.Fatalf("id=%q", backend.getID)
			}
		}},
		{"tasks/list", map[string]any{"cursor": "cursor", "limit": 7}, func(t *testing.T, result session.HandleResult) {
			if backend.listCursor != "cursor" || backend.listLimit != 7 {
				t.Fatalf("cursor=%q limit=%d", backend.listCursor, backend.listLimit)
			}
		}},
		{"tasks/cancel", map[string]any{"taskId": id('1')}, func(t *testing.T, result session.HandleResult) {
			if backend.cancelID != id('1') {
				t.Fatalf("id=%q", backend.cancelID)
			}
		}},
		{"events/subscribe", map[string]any{"afterSequence": 3}, func(t *testing.T, result session.HandleResult) {
			if backend.after != 3 || result.Subscription != backend.subscription {
				t.Fatalf("after=%d subscription=%p", backend.after, result.Subscription)
			}
		}},
		{"artifacts/list", map[string]any{"taskId": id('1'), "cursor": "artifact-cursor", "limit": 9}, func(t *testing.T, result session.HandleResult) {
			if backend.artifactTask != id('1') || backend.artifactCur != "artifact-cursor" || backend.artifactLim != 9 {
				t.Fatalf("task=%q cursor=%q limit=%d", backend.artifactTask, backend.artifactCur, backend.artifactLim)
			}
		}},
		{"artifacts/read", map[string]any{"artifactId": id('a'), "offset": 0, "length": 64}, func(t *testing.T, result session.HandleResult) {
			if backend.artifactID != id('a') || backend.offset != 0 || backend.length != 64 {
				t.Fatalf("id=%q offset=%d length=%d", backend.artifactID, backend.offset, backend.length)
			}
			raw, _ := json.Marshal(result.Response.Payload)
			var payload struct {
				Data       string `json:"data"`
				NextOffset int64  `json:"nextOffset"`
				EOF        bool   `json:"eof"`
				SizeBytes  int64  `json:"sizeBytes"`
				SHA256     string `json:"sha256"`
			}
			if err := json.Unmarshal(raw, &payload); err != nil {
				t.Fatal(err)
			}
			if payload.Data != base64.RawURLEncoding.EncodeToString([]byte{0xfb, 0xff}) || payload.NextOffset != 2 || !payload.EOF || payload.SizeBytes != 3 || payload.SHA256 != artifact.SHA256 {
				t.Fatalf("payload=%s", raw)
			}
		}},
	}
	for _, test := range tests {
		t.Run(test.method, func(t *testing.T) {
			result := s.Handle(context.Background(), requestVersion(t, protocol.Version11, test.method, test.payload))
			if result.Response.Kind != "response" {
				t.Fatalf("result=%#v", result)
			}
			test.check(t, result)
		})
	}
}

func TestSessionRejectsInvalidPhase2Payloads(t *testing.T) {
	tests := []struct{ name, method, payload string }{
		{"unknown field", "tasks/start", `{"idempotencyKey":"22222222222222222222222222222222","scenario":"hang","timeoutMs":1,"executable":"bad"}`},
		{"multiple values", "tasks/get", `{"taskId":"11111111111111111111111111111111"} {}`},
		{"invalid id", "tasks/get", `{"taskId":"../secret"}`},
		{"empty cursor", "tasks/list", `{"cursor":""}`},
		{"limit zero", "tasks/list", `{"limit":0}`},
		{"limit too large", "tasks/list", `{"limit":201}`},
		{"missing sequence", "events/subscribe", `{}`},
		{"negative sequence", "events/subscribe", `{"afterSequence":-1}`},
		{"missing offset", "artifacts/read", `{"artifactId":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","length":1}`},
		{"negative offset", "artifacts/read", `{"artifactId":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","offset":-1,"length":1}`},
		{"oversized read", "artifacts/read", `{"artifactId":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","offset":0,"length":65537}`},
		{"timeout too large", "tasks/start", `{"idempotencyKey":"22222222222222222222222222222222","scenario":"hang","timeoutMs":86400001}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			s := authenticatedV11(t, &fakeBackend{})
			result := s.Handle(context.Background(), protocol.Request{ProtocolVersion: protocol.Version11, Kind: "request", MessageID: id('3'), Method: test.method, Payload: json.RawMessage(test.payload)})
			if result.Response.Error == nil || result.Response.Error.Code != "INVALID_MESSAGE" || result.Response.Error.Retryable {
				t.Fatalf("result=%#v", result)
			}
		})
	}
}

func TestSessionMapsPhase2Errors(t *testing.T) {
	tests := []struct {
		name, method string
		err          error
		code         string
		retryable    bool
	}{
		{"task missing", "tasks/get", task.ErrNotFound, "TASK_NOT_FOUND", false},
		{"artifact missing", "artifacts/read", task.ErrNotFound, "ARTIFACT_NOT_FOUND", false},
		{"idempotency conflict", "tasks/start", task.ErrIdempotencyConflict, "IDEMPOTENCY_CONFLICT", false},
		{"storage unavailable", "tasks/list", task.ErrStorageUnavailable, "STORAGE_UNAVAILABLE", true},
		{"invalid task", "tasks/start", task.ErrInvalidArgument, "INVALID_TASK_SPEC", false},
		{"invalid task cursor", "tasks/list", task.ErrInvalidArgument, "INVALID_MESSAGE", false},
		{"invalid artifact cursor", "artifacts/list", task.ErrInvalidArgument, "INVALID_MESSAGE", false},
		{"invalid cursor", "events/subscribe", eventbroker.ErrInvalidCursor, "EVENT_CURSOR_INVALID", false},
		{"slow subscriber", "events/subscribe", eventbroker.ErrSubscriberTooSlow, "SUBSCRIBER_TOO_SLOW", true},
		{"invalid artifact range", "artifacts/read", artifactstore.ErrInvalidRange, "INVALID_MESSAGE", false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			backend := &fakeBackend{err: test.err}
			s := authenticatedV11(t, backend)
			payload := map[string]any{}
			switch test.method {
			case "tasks/get":
				payload["taskId"] = id('1')
			case "tasks/start":
				payload = map[string]any{"idempotencyKey": id('2'), "scenario": "hang", "timeoutMs": 1}
			case "events/subscribe":
				payload["afterSequence"] = 0
			case "artifacts/list":
				payload = map[string]any{"taskId": id('1'), "cursor": "invalid", "limit": 10}
			case "artifacts/read":
				payload = map[string]any{"artifactId": id('a'), "offset": 0, "length": 1}
			}
			result := s.Handle(context.Background(), requestVersion(t, protocol.Version11, test.method, payload))
			if result.Response.Error == nil || result.Response.Error.Code != test.code || result.Response.Error.Retryable != test.retryable {
				t.Fatalf("result=%#v", result)
			}
		})
	}
}

type failingEventSource struct{ err error }

func (s failingEventSource) Watermark(context.Context) (int64, error) { return 0, s.err }
func (failingEventSource) EventsAfter(context.Context, int64, int64, int) ([]task.Event, error) {
	return nil, nil
}

func TestSessionMapsSubscribeSetupFailureToStorageUnavailable(t *testing.T) {
	broker, err := eventbroker.New(failingEventSource{err: task.ErrStorageUnavailable}, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	backend := &fakeBackend{subscribe: broker.Subscribe}
	s := authenticatedV11(t, backend)
	result := s.Handle(context.Background(), requestVersion(t, protocol.Version11, "events/subscribe", map[string]any{"afterSequence": 0}))
	if result.Response.Error == nil || result.Response.Error.Code != "STORAGE_UNAVAILABLE" || !result.Response.Error.Retryable {
		t.Fatalf("result=%#v", result)
	}
}

func request(t *testing.T, method string, payload any) protocol.Request {
	return requestVersion(t, protocol.Version10, method, payload)
}

func requestVersion(t *testing.T, version, method string, payload any) protocol.Request {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	return protocol.Request{ProtocolVersion: version, Kind: "request", MessageID: "0123456789abcdef0123456789abcdef", Method: method, Payload: raw}
}

func TestSessionNegotiatesV11AndKeepsV10Shape(t *testing.T) {
	v11 := session.New("0123456789abcdef", "windows", "named-pipe", nil)
	accepted := v11.Handle(context.Background(), requestVersion(t, "1.1", "handshake", map[string]any{
		"token": "0123456789abcdef", "clientName": "test", "clientVersion": "0.2.0",
		"supportedProtocolVersions": []string{"1.1", "1.0"},
	}))
	if accepted.ProtocolVersion != "1.1" || v11.NegotiatedVersion() != "1.1" {
		t.Fatalf("v1.1 negotiation failed: %#v", accepted)
	}

	v10 := session.New("0123456789abcdef", "linux", "unix-socket", nil)
	legacy := v10.Handle(context.Background(), requestVersion(t, "1.0", "handshake", map[string]string{
		"token": "0123456789abcdef", "clientName": "legacy", "clientVersion": "0.1.0",
	}))
	if legacy.ProtocolVersion != "1.0" || v10.NegotiatedVersion() != "1.0" {
		t.Fatalf("v1.0 negotiation failed: %#v", legacy)
	}

	capabilities := v10.Handle(context.Background(), requestVersion(t, "1.0", "capabilities/get", map[string]any{}))
	raw, err := json.Marshal(capabilities.Payload)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"taskExecution", "eventReplay", "sqliteHistory", "artifactRead", "processTreeControl"} {
		if _, exists := fields[forbidden]; exists {
			t.Fatalf("v1.0 capabilities unexpectedly contain %q: %s", forbidden, raw)
		}
	}
	if len(fields) != 5 {
		t.Fatalf("v1.0 capabilities shape changed: %s", raw)
	}
}

func TestSessionRejectsUnsupportedAndMismatchedVersions(t *testing.T) {
	unknown := session.New("0123456789abcdef", "linux", "unix-socket", nil)
	result := unknown.Handle(context.Background(), requestVersion(t, "2.0", "handshake", map[string]string{
		"token": "0123456789abcdef", "clientName": "test", "clientVersion": "0.2.0",
	}))
	if result.Error == nil || result.Error.Code != "UNSUPPORTED_PROTOCOL" || result.ProtocolVersion != protocol.Version10 {
		t.Fatalf("unexpected unknown-version response: %#v", result)
	}

	negotiated := session.New("0123456789abcdef", "linux", "unix-socket", nil)
	_ = negotiated.Handle(context.Background(), requestVersion(t, "1.1", "handshake", map[string]any{
		"token": "0123456789abcdef", "clientName": "test", "clientVersion": "0.2.0",
		"supportedProtocolVersions": []string{"1.1", "1.0"},
	}))
	result = negotiated.Handle(context.Background(), requestVersion(t, "1.0", "capabilities/get", map[string]any{}))
	if result.Error == nil || result.Error.Code != "UNSUPPORTED_PROTOCOL" || result.ProtocolVersion != protocol.Version11 {
		t.Fatalf("unexpected mismatched-version response: %#v", result)
	}
}

func TestSessionRequiresValidV11VersionOffer(t *testing.T) {
	tests := map[string]struct {
		payload any
		code    string
	}{
		"missing offer": {
			payload: map[string]string{"token": "0123456789abcdef", "clientName": "test", "clientVersion": "0.2.0"},
			code:    "INVALID_MESSAGE",
		},
		"mismatched offer": {
			payload: map[string]any{"token": "0123456789abcdef", "clientName": "test", "clientVersion": "0.2.0", "supportedProtocolVersions": []string{"1.0"}},
			code:    "UNSUPPORTED_PROTOCOL",
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			s := session.New("0123456789abcdef", "linux", "unix-socket", nil)
			result := s.Handle(context.Background(), requestVersion(t, protocol.Version11, "handshake", test.payload))
			if result.Error == nil || result.Error.Code != test.code {
				t.Fatalf("unexpected response: %#v", result)
			}
			if s.Authenticated() || s.NegotiatedVersion() != "" {
				t.Fatal("failed negotiation changed session state")
			}
		})
	}
}

func TestSessionKeepsV10HandshakePayloadStrict(t *testing.T) {
	s := session.New("0123456789abcdef", "linux", "unix-socket", nil)
	result := s.Handle(context.Background(), requestVersion(t, protocol.Version10, "handshake", map[string]any{
		"token": "0123456789abcdef", "clientName": "legacy", "clientVersion": "0.1.0",
		"supportedProtocolVersions": []string{protocol.Version10},
	}))
	if result.Error == nil || result.Error.Code != "INVALID_MESSAGE" {
		t.Fatalf("unexpected response: %#v", result)
	}
}

func TestSessionReturnsVersionedCapabilities(t *testing.T) {
	s := session.New("0123456789abcdef", "linux", "unix-socket", nil)
	_ = s.Handle(context.Background(), requestVersion(t, protocol.Version11, "handshake", map[string]any{
		"token": "0123456789abcdef", "clientName": "test", "clientVersion": "0.2.0",
		"supportedProtocolVersions": []string{"1.1"},
	}))
	result := s.Handle(context.Background(), requestVersion(t, protocol.Version11, "capabilities/get", map[string]any{}))
	raw, err := json.Marshal(result.Payload)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{"taskExecution", "eventReplay", "sqliteHistory", "artifactRead", "processTreeControl"} {
		if _, exists := fields[required]; !exists {
			t.Fatalf("v1.1 capabilities missing %q: %s", required, raw)
		}
	}
}

func TestSessionGatesPhase2MethodsByProtocolAndDependency(t *testing.T) {
	methods := []string{"tasks/start", "tasks/get", "tasks/list", "tasks/cancel", "events/subscribe", "artifacts/list", "artifacts/read"}
	for _, version := range []string{protocol.Version10, protocol.Version11} {
		t.Run(version, func(t *testing.T) {
			s := session.New("0123456789abcdef", "linux", "unix-socket", nil)
			handshakePayload := map[string]any{"token": "0123456789abcdef", "clientName": "test", "clientVersion": "0.2.0"}
			if version == protocol.Version11 {
				handshakePayload["supportedProtocolVersions"] = []string{protocol.Version11}
			}
			_ = s.Handle(context.Background(), requestVersion(t, version, "handshake", handshakePayload))
			for _, method := range methods {
				result := s.Handle(context.Background(), requestVersion(t, version, method, map[string]any{}))
				want := "PROTOCOL_FEATURE_UNAVAILABLE"
				if version == protocol.Version11 {
					want = "SERVICE_UNHEALTHY"
				}
				if result.Error == nil || result.Error.Code != want {
					t.Fatalf("%s: got %#v, want %s", method, result, want)
				}
			}
		})
	}
}

func TestSessionRequiresHandshakeThenReturnsCapabilities(t *testing.T) {
	s := session.New("0123456789abcdef", "windows", "named-pipe", nil)
	before := s.Handle(context.Background(), request(t, "capabilities/get", map[string]any{}))
	if before.Kind != "error" || before.Error.Code != "AUTH_REQUIRED" {
		t.Fatalf("unexpected response: %#v", before)
	}
	accepted := s.Handle(context.Background(), request(t, "handshake", map[string]string{"token": "0123456789abcdef", "clientName": "test", "clientVersion": "0.1.0"}))
	if accepted.Kind != "response" {
		t.Fatalf("handshake failed: %#v", accepted)
	}
	capabilities := s.Handle(context.Background(), request(t, "capabilities/get", map[string]any{}))
	if capabilities.Kind != "response" {
		t.Fatalf("capabilities failed: %#v", capabilities)
	}
}

func TestSessionRejectsWrongToken(t *testing.T) {
	s := session.New("0123456789abcdef", "linux", "unix-socket", nil)
	result := s.Handle(context.Background(), request(t, "handshake", map[string]string{"token": "wrong-token-value", "clientName": "test", "clientVersion": "0.1.0"}))
	if result.Error.Code != "AUTH_FAILED" {
		t.Fatalf("unexpected response: %#v", result)
	}
}

func TestSessionRejectsInvalidHandshakePayload(t *testing.T) {
	tests := map[string]string{
		"unknown property":      `{"token":"0123456789abcdef","clientName":"test","clientVersion":"0.1.0","unknown":true}`,
		"missing token":         `{"clientName":"test","clientVersion":"0.1.0"}`,
		"short token":           `{"token":"too-short","clientName":"test","clientVersion":"0.1.0"}`,
		"missing clientName":    `{"token":"0123456789abcdef","clientVersion":"0.1.0"}`,
		"empty clientName":      `{"token":"0123456789abcdef","clientName":"","clientVersion":"0.1.0"}`,
		"missing clientVersion": `{"token":"0123456789abcdef","clientName":"test"}`,
		"empty clientVersion":   `{"token":"0123456789abcdef","clientName":"test","clientVersion":""}`,
		"null":                  `null`,
		"array":                 `[]`,
		"string":                `"payload"`,
		"trailing JSON":         `{"token":"0123456789abcdef","clientName":"test","clientVersion":"0.1.0"} {}`,
	}
	for name, payload := range tests {
		t.Run(name, func(t *testing.T) {
			s := session.New("0123456789abcdef", "linux", "unix-socket", nil)
			result := s.Handle(context.Background(), protocol.Request{
				ProtocolVersion: protocol.Version,
				Kind:            "request",
				MessageID:       "0123456789abcdef0123456789abcdef",
				Method:          "handshake",
				Payload:         json.RawMessage(payload),
			})
			if result.Kind != "error" || result.Error == nil || result.Error.Code != "INVALID_MESSAGE" {
				t.Fatalf("unexpected response: %#v", result)
			}
		})
	}
}

func TestSessionRejectsUnknownMethodAfterAuthentication(t *testing.T) {
	s := session.New("0123456789abcdef", "linux", "unix-socket", nil)
	_ = s.Handle(context.Background(), request(t, "handshake", map[string]string{"token": "0123456789abcdef", "clientName": "test", "clientVersion": "0.1.0"}))
	result := s.Handle(context.Background(), request(t, "unknown", map[string]any{}))
	if result.Error.Code != "METHOD_NOT_FOUND" {
		t.Fatalf("unexpected response: %#v", result)
	}
}

func TestSessionRejectsNonEmptyPayloadForKnownEmptyMethods(t *testing.T) {
	for _, method := range []string{"capabilities/get", "shutdown"} {
		t.Run(method, func(t *testing.T) {
			s := session.New("0123456789abcdef", "linux", "unix-socket", nil)
			_ = s.Handle(context.Background(), request(t, "handshake", map[string]string{"token": "0123456789abcdef", "clientName": "test", "clientVersion": "0.1.0"}))
			result := s.Handle(context.Background(), request(t, method, map[string]any{"unexpected": true}))
			if result.Error == nil || result.Error.Code != "INVALID_MESSAGE" {
				t.Fatalf("unexpected response: %#v", result)
			}
			if method == "shutdown" {
				select {
				case <-s.ShutdownRequested():
					t.Fatal("invalid shutdown payload requested shutdown")
				default:
				}
			}
		})
	}
}

func TestSessionPreservesUnknownMethodHandlingForNonEmptyPayload(t *testing.T) {
	s := session.New("0123456789abcdef", "linux", "unix-socket", nil)
	_ = s.Handle(context.Background(), request(t, "handshake", map[string]string{"token": "0123456789abcdef", "clientName": "test", "clientVersion": "0.1.0"}))
	result := s.Handle(context.Background(), request(t, "unknown", map[string]any{"allowed": "for method routing"}))
	if result.Error == nil || result.Error.Code != "METHOD_NOT_FOUND" {
		t.Fatalf("unexpected response: %#v", result)
	}
}

func TestShutdownClosesSignalOnce(t *testing.T) {
	s := session.New("0123456789abcdef", "linux", "unix-socket", nil)
	_ = s.Handle(context.Background(), request(t, "handshake", map[string]string{"token": "0123456789abcdef", "clientName": "test", "clientVersion": "0.1.0"}))
	_ = s.Handle(context.Background(), request(t, "shutdown", map[string]any{}))
	_ = s.Handle(context.Background(), request(t, "shutdown", map[string]any{}))
	select {
	case <-s.ShutdownRequested():
	default:
		t.Fatal("shutdown signal was not closed")
	}
}
