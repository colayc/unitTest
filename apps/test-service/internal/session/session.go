package session

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"sync"
	"time"
	"unicode/utf8"

	"unit-test-ide.local/test-service/internal/artifactstore"
	"unit-test-ide.local/test-service/internal/eventbroker"
	"unit-test-ide.local/test-service/internal/protocol"
	"unit-test-ide.local/test-service/internal/protocolmodel"
	"unit-test-ide.local/test-service/internal/task"
)

const (
	defaultPageLimit = 100
	maxPageLimit     = 200
	maxTimeoutMS     = int64((24 * time.Hour) / time.Millisecond)
)

type ArtifactChunk struct {
	Data       []byte
	NextOffset int64
	EOF        bool
	Metadata   task.Artifact
}

type Backend interface {
	Start(context.Context, task.StartRequest) (task.Task, error)
	Get(context.Context, string) (task.Task, error)
	List(context.Context, string, int) (task.Page[task.Task], error)
	Cancel(context.Context, string) (task.Task, error)
	Subscribe(context.Context, int64) (*eventbroker.Subscription, error)
	ListArtifacts(context.Context, string, string, int) (task.Page[task.Artifact], error)
	ReadArtifact(context.Context, string, int64, int) (ArtifactChunk, error)
}

type HandleResult struct {
	protocol.Response
	Subscription *eventbroker.Subscription
}

type Session struct {
	token, platform, transport string
	mu                         sync.Mutex
	authenticated              bool
	negotiatedVersion          string
	backend                    Backend
	shutdown                   chan struct{}
	shutdownOnce               sync.Once
}

type handshake struct {
	Token                     string   `json:"token"`
	ClientName                string   `json:"clientName"`
	ClientVersion             string   `json:"clientVersion"`
	SupportedProtocolVersions []string `json:"supportedProtocolVersions,omitempty"`
}

type startPayload struct {
	IdempotencyKey string        `json:"idempotencyKey"`
	Scenario       task.Scenario `json:"scenario"`
	TimeoutMS      int64         `json:"timeoutMs"`
}

type taskIDPayload struct {
	TaskID string `json:"taskId"`
}

type listPayload struct {
	Cursor *string `json:"cursor"`
	Limit  *int    `json:"limit"`
}

type subscribePayload struct {
	AfterSequence *int64 `json:"afterSequence"`
}

type artifactListPayload struct {
	TaskID string  `json:"taskId"`
	Cursor *string `json:"cursor"`
	Limit  *int    `json:"limit"`
}

type artifactReadPayload struct {
	ArtifactID string `json:"artifactId"`
	Offset     *int64 `json:"offset"`
	Length     int    `json:"length"`
}

type taskPagePayload struct {
	Items      []protocolmodel.TaskSnapshot `json:"items"`
	NextCursor string                       `json:"nextCursor,omitempty"`
}

type artifactPagePayload struct {
	Items      []protocolmodel.ArtifactMetadata `json:"items"`
	NextCursor string                           `json:"nextCursor,omitempty"`
}

type artifactChunkPayload struct {
	Data       string `json:"data"`
	NextOffset int64  `json:"nextOffset"`
	EOF        bool   `json:"eof"`
	SizeBytes  int64  `json:"sizeBytes"`
	SHA256     string `json:"sha256"`
}

func New(token, platform, transport string, backend Backend) *Session {
	return &Session{token: token, platform: platform, transport: transport, backend: backend, shutdown: make(chan struct{})}
}

func (s *Session) ShutdownRequested() <-chan struct{} { return s.shutdown }

func (s *Session) Authenticated() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.authenticated
}

func (s *Session) NegotiatedVersion() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.negotiatedVersion
}

func (s *Session) Handle(ctx context.Context, request protocol.Request) HandleResult {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !protocol.SupportedVersion(request.ProtocolVersion) {
		return handled(protocol.Failure(request.ProtocolVersion, request, "UNSUPPORTED_PROTOCOL", "protocol version is not supported", false))
	}
	responseVersion := request.ProtocolVersion
	if s.authenticated {
		responseVersion = s.negotiatedVersion
		if request.ProtocolVersion != s.negotiatedVersion {
			return handled(protocol.Failure(responseVersion, request, "UNSUPPORTED_PROTOCOL", "protocol version does not match the negotiated version", false))
		}
	}
	if !s.authenticated && request.Method != "handshake" {
		return handled(protocol.Failure(responseVersion, request, "AUTH_REQUIRED", "handshake must be completed first", false))
	}

	switch request.Method {
	case "handshake":
		payload, err := decodeHandshake(request.Payload, request.ProtocolVersion)
		if err != nil {
			return handled(protocol.Failure(responseVersion, request, "INVALID_MESSAGE", "invalid handshake payload", false))
		}
		negotiatedVersion, ok := negotiate(request.ProtocolVersion, payload.SupportedProtocolVersions)
		if !ok {
			return handled(protocol.Failure(responseVersion, request, "UNSUPPORTED_PROTOCOL", "protocol version is not supported", false))
		}
		if subtle.ConstantTimeCompare([]byte(payload.Token), []byte(s.token)) != 1 {
			return handled(protocol.Failure(negotiatedVersion, request, "AUTH_FAILED", "authentication failed", false))
		}
		s.authenticated = true
		s.negotiatedVersion = negotiatedVersion
		return handled(protocol.Success(negotiatedVersion, request, map[string]string{"negotiatedProtocolVersion": negotiatedVersion, "serviceVersion": "0.1.0"}))
	case "capabilities/get":
		if err := decodeEmpty(request.Payload); err != nil {
			return handled(protocol.Failure(responseVersion, request, "INVALID_MESSAGE", "payload must be an empty object", false))
		}
		if s.negotiatedVersion == protocol.Version11 {
			return handled(protocol.Success(responseVersion, request, protocolmodel.CapabilitiesV11{
				Platform:           protocolmodel.Platform(s.platform),
				Transports:         []protocolmodel.Transport{protocolmodel.Transport(s.transport)},
				Toolchains:         []string{},
				Frameworks:         []string{},
				CoverageTools:      []string{},
				TaskExecution:      true,
				EventReplay:        true,
				SqliteHistory:      true,
				ArtifactRead:       true,
				ProcessTreeControl: map[string]protocolmodel.ProcessTreeControl{"windows": protocolmodel.JobObject, "linux": protocolmodel.ProcessGroup}[s.platform],
			}))
		}
		return handled(protocol.Success(responseVersion, request, protocolmodel.Capabilities{Platform: s.platform, Transports: []string{s.transport}, Toolchains: []string{}, Frameworks: []string{}, CoverageTools: []string{}}))
	case "shutdown":
		if err := decodeEmpty(request.Payload); err != nil {
			return handled(protocol.Failure(responseVersion, request, "INVALID_MESSAGE", "payload must be an empty object", false))
		}
		s.shutdownOnce.Do(func() { close(s.shutdown) })
		return handled(protocol.Success(responseVersion, request, map[string]bool{"accepted": true}))
	}

	if phase2Method(request.Method) {
		if s.negotiatedVersion == protocol.Version10 {
			return handled(protocol.Failure(responseVersion, request, "PROTOCOL_FEATURE_UNAVAILABLE", "method requires protocol 1.1", false))
		}
		if s.backend == nil {
			return handled(protocol.Failure(responseVersion, request, "SERVICE_UNHEALTHY", "task service is unavailable", true))
		}
		return s.handlePhase2(ctx, responseVersion, request)
	}
	return handled(protocol.Failure(responseVersion, request, "METHOD_NOT_FOUND", "method is not supported", false))
}

func (s *Session) handlePhase2(ctx context.Context, version string, request protocol.Request) HandleResult {
	switch request.Method {
	case "tasks/start":
		payload, err := decodeStrict[startPayload](request.Payload)
		if err != nil || !validID(payload.IdempotencyKey) || !task.ValidScenario(payload.Scenario) || payload.TimeoutMS < 1 || payload.TimeoutMS > maxTimeoutMS {
			return invalidPayload(version, request)
		}
		value, err := s.backend.Start(ctx, task.StartRequest{IdempotencyKey: payload.IdempotencyKey, Scenario: payload.Scenario, Timeout: time.Duration(payload.TimeoutMS) * time.Millisecond})
		if err != nil {
			return backendFailure(version, request, err)
		}
		return handled(protocol.Success(version, request, toProtocolTask(value)))
	case "tasks/get", "tasks/cancel":
		payload, err := decodeStrict[taskIDPayload](request.Payload)
		if err != nil || !validID(payload.TaskID) {
			return invalidPayload(version, request)
		}
		var value task.Task
		if request.Method == "tasks/get" {
			value, err = s.backend.Get(ctx, payload.TaskID)
		} else {
			value, err = s.backend.Cancel(ctx, payload.TaskID)
		}
		if err != nil {
			return backendFailure(version, request, err)
		}
		return handled(protocol.Success(version, request, toProtocolTask(value)))
	case "tasks/list":
		payload, cursor, limit, err := decodeList(request.Payload)
		_ = payload
		if err != nil {
			return invalidPayload(version, request)
		}
		page, err := s.backend.List(ctx, cursor, limit)
		if err != nil {
			return backendFailure(version, request, err)
		}
		items := make([]protocolmodel.TaskSnapshot, len(page.Items))
		for index := range page.Items {
			items[index] = toProtocolTask(page.Items[index])
		}
		return handled(protocol.Success(version, request, taskPagePayload{Items: items, NextCursor: page.NextCursor}))
	case "events/subscribe":
		payload, err := decodeStrict[subscribePayload](request.Payload)
		if err != nil || payload.AfterSequence == nil || *payload.AfterSequence < 0 {
			return invalidPayload(version, request)
		}
		subscription, err := s.backend.Subscribe(ctx, *payload.AfterSequence)
		if err != nil {
			return backendFailure(version, request, err)
		}
		return HandleResult{Response: protocol.Success(version, request, map[string]int64{"afterSequence": *payload.AfterSequence}), Subscription: subscription}
	case "artifacts/list":
		payload, err := decodeStrict[artifactListPayload](request.Payload)
		if err != nil || !validID(payload.TaskID) {
			return invalidPayload(version, request)
		}
		cursor, limit, err := normalizedPage(payload.Cursor, payload.Limit)
		if err != nil {
			return invalidPayload(version, request)
		}
		page, err := s.backend.ListArtifacts(ctx, payload.TaskID, cursor, limit)
		if err != nil {
			return backendFailure(version, request, err)
		}
		items := make([]protocolmodel.ArtifactMetadata, len(page.Items))
		for index := range page.Items {
			items[index] = toProtocolArtifact(page.Items[index])
		}
		return handled(protocol.Success(version, request, artifactPagePayload{Items: items, NextCursor: page.NextCursor}))
	case "artifacts/read":
		payload, err := decodeStrict[artifactReadPayload](request.Payload)
		if err != nil || !validID(payload.ArtifactID) || payload.Offset == nil || *payload.Offset < 0 || payload.Length < 1 || payload.Length > artifactstore.MaxReadChunk {
			return invalidPayload(version, request)
		}
		chunk, err := s.backend.ReadArtifact(ctx, payload.ArtifactID, *payload.Offset, payload.Length)
		if err != nil {
			return backendFailure(version, request, err)
		}
		return handled(protocol.Success(version, request, artifactChunkPayload{
			Data: base64.RawURLEncoding.EncodeToString(chunk.Data), NextOffset: chunk.NextOffset, EOF: chunk.EOF,
			SizeBytes: chunk.Metadata.Size, SHA256: chunk.Metadata.SHA256,
		}))
	default:
		return handled(protocol.Failure(version, request, "METHOD_NOT_FOUND", "method is not supported", false))
	}
}

func handled(response protocol.Response) HandleResult { return HandleResult{Response: response} }

func invalidPayload(version string, request protocol.Request) HandleResult {
	return handled(protocol.Failure(version, request, "INVALID_MESSAGE", "invalid method payload", false))
}

func backendFailure(version string, request protocol.Request, err error) HandleResult {
	code, message, retryable := "SERVICE_UNHEALTHY", "task service is unavailable", true
	switch {
	case errors.Is(err, task.ErrIdempotencyConflict):
		code, message, retryable = "IDEMPOTENCY_CONFLICT", "idempotency key conflicts with an existing task", false
	case errors.Is(err, eventbroker.ErrInvalidCursor):
		code, message, retryable = "EVENT_CURSOR_INVALID", "event cursor is invalid", false
	case errors.Is(err, eventbroker.ErrSubscriberTooSlow):
		code, message, retryable = "SUBSCRIBER_TOO_SLOW", "event subscriber is too slow", true
	case errors.Is(err, task.ErrNotFound):
		if request.Method == "artifacts/list" || request.Method == "artifacts/read" {
			code, message = "ARTIFACT_NOT_FOUND", "artifact was not found"
		} else {
			code, message = "TASK_NOT_FOUND", "task was not found"
		}
		retryable = false
	case errors.Is(err, task.ErrInvalidArgument):
		code, message, retryable = "INVALID_TASK_SPEC", "task specification is invalid", false
	case errors.Is(err, artifactstore.ErrInvalidRange):
		code, message, retryable = "INVALID_MESSAGE", "artifact range is invalid", false
	case errors.Is(err, artifactstore.ErrArtifactChanged):
		code, message, retryable = "ARTIFACT_NOT_READY", "artifact is not ready", true
	case errors.Is(err, artifactstore.ErrInvalidArtifact), errors.Is(err, artifactstore.ErrUnsafePath):
		code, message, retryable = "ARTIFACT_NOT_FOUND", "artifact was not found", false
	case errors.Is(err, task.ErrStorageUnavailable), errors.Is(err, artifactstore.ErrStoreUnavailable):
		code, message, retryable = "STORAGE_UNAVAILABLE", "storage is unavailable", true
	}
	return handled(protocol.Failure(version, request, code, message, retryable))
}

func decodeList(raw json.RawMessage) (listPayload, string, int, error) {
	payload, err := decodeStrict[listPayload](raw)
	if err != nil {
		return payload, "", 0, err
	}
	cursor, limit, err := normalizedPage(payload.Cursor, payload.Limit)
	return payload, cursor, limit, err
}

func normalizedPage(cursorValue *string, limitValue *int) (string, int, error) {
	cursor := ""
	if cursorValue != nil {
		if *cursorValue == "" {
			return "", 0, errors.New("empty cursor")
		}
		cursor = *cursorValue
	}
	limit := defaultPageLimit
	if limitValue != nil {
		if *limitValue < 1 || *limitValue > maxPageLimit {
			return "", 0, errors.New("invalid limit")
		}
		limit = *limitValue
	}
	return cursor, limit, nil
}

func decodeStrict[T any](raw json.RawMessage) (T, error) {
	var value T
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return value, err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return value, errors.New("multiple JSON values")
	}
	return value, nil
}

func toProtocolTask(value task.Task) protocolmodel.TaskSnapshot {
	result := protocolmodel.TaskSnapshot{
		TaskID: value.ID, Kind: protocolmodel.Simulation, Scenario: protocolmodel.Scenario(value.Scenario),
		Status: protocolmodel.Status(value.Status), CreatedAt: value.CreatedAt, LastSequence: value.LastSequence,
	}
	if value.Timeout > 0 {
		timeout := value.Timeout.Milliseconds()
		result.TimeoutMS = &timeout
	}
	if value.StartedAt != nil {
		started := *value.StartedAt
		result.StartedAt = &started
	}
	if value.FinishedAt != nil {
		finished := *value.FinishedAt
		result.FinishedAt = &finished
	}
	if value.Status == task.StatusFinished {
		outcome := protocolmodel.Outcome(value.Outcome)
		result.Outcome = &outcome
	}
	if value.ErrorCode != "" {
		code := value.ErrorCode
		result.ErrorCode = &code
	}
	if value.ErrorMessage != "" {
		message := value.ErrorMessage
		result.ErrorMessage = &message
	}
	return result
}

func toProtocolArtifact(value task.Artifact) protocolmodel.ArtifactMetadata {
	return protocolmodel.ArtifactMetadata{
		ArtifactID: value.ID, TaskID: value.TaskID, Kind: protocolmodel.ArtifactKind(value.Kind),
		MIMEType: protocolmodel.MIMEType(value.MIMEType), SizeBytes: value.Size, Sha256: value.SHA256, CreatedAt: value.CreatedAt,
	}
}

func validID(value string) bool {
	if len(value) != 32 {
		return false
	}
	for _, character := range value {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}

func negotiate(envelopeVersion string, supported []string) (string, bool) {
	if envelopeVersion == protocol.Version10 && len(supported) == 0 {
		return protocol.Version10, true
	}
	for _, candidate := range []string{protocol.Version11, protocol.Version10} {
		if candidate != envelopeVersion {
			continue
		}
		for _, offered := range supported {
			if offered == candidate {
				return candidate, true
			}
		}
	}
	return "", false
}

func phase2Method(method string) bool {
	switch method {
	case "tasks/start", "tasks/get", "tasks/list", "tasks/cancel", "events/subscribe", "artifacts/list", "artifacts/read":
		return true
	default:
		return false
	}
}

func decodeEmpty(raw json.RawMessage) error {
	var payload map[string]json.RawMessage
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if err := decoder.Decode(&payload); err != nil || payload == nil || len(payload) != 0 {
		return errors.New("payload is not an empty object")
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return errors.New("multiple JSON values")
	}
	return nil
}

func decodeHandshake(raw json.RawMessage, version string) (handshake, error) {
	payload, err := decodeStrict[handshake](raw)
	if err != nil {
		return handshake{}, err
	}
	if utf8.RuneCountInString(payload.Token) < 16 || payload.ClientName == "" || payload.ClientVersion == "" {
		return handshake{}, errors.New("missing handshake fields")
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil || fields == nil {
		return handshake{}, errors.New("handshake payload must be an object")
	}
	if _, offered := fields["supportedProtocolVersions"]; version == protocol.Version10 && offered {
		return handshake{}, errors.New("protocol 1.0 handshake contains a version offer")
	}
	if version == protocol.Version11 && len(payload.SupportedProtocolVersions) == 0 {
		return handshake{}, errors.New("protocol 1.1 handshake requires a version offer")
	}
	return payload, nil
}
