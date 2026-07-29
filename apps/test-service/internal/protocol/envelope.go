package protocol

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"time"
)

const (
	Version10 = "1.0"
	Version11 = "1.1"
	Version12 = "1.2"
	Version   = Version10
)

type Request struct {
	ProtocolVersion string          `json:"protocolVersion"`
	Kind            string          `json:"kind"`
	MessageID       string          `json:"messageId"`
	Method          string          `json:"method"`
	SentAt          string          `json:"sentAt"`
	Payload         json.RawMessage `json:"payload"`
}

type requestEnvelope struct {
	ProtocolVersion *string         `json:"protocolVersion"`
	Kind            *string         `json:"kind"`
	MessageID       *string         `json:"messageId"`
	Method          *string         `json:"method"`
	SentAt          *string         `json:"sentAt"`
	Payload         json.RawMessage `json:"payload"`
}

type ErrorBody struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Retryable bool   `json:"retryable"`
}

type Response struct {
	ProtocolVersion string     `json:"protocolVersion"`
	Kind            string     `json:"kind"`
	MessageID       string     `json:"messageId"`
	RequestID       string     `json:"requestId"`
	Method          string     `json:"method,omitempty"`
	SentAt          string     `json:"sentAt"`
	Payload         any        `json:"payload,omitempty"`
	Error           *ErrorBody `json:"error,omitempty"`
}

type Event struct {
	ProtocolVersion string          `json:"protocolVersion"`
	Kind            string          `json:"kind"`
	MessageID       string          `json:"messageId"`
	SentAt          string          `json:"sentAt"`
	Sequence        int64           `json:"sequence"`
	Event           string          `json:"event"`
	TaskID          string          `json:"taskId"`
	PayloadVersion  int             `json:"payloadVersion"`
	Payload         json.RawMessage `json:"payload"`
}

func SupportedVersion(version string) bool {
	return version == Version10 || version == Version11 || version == Version12
}

func DecodeRequest(line []byte) (Request, error) {
	var envelope requestEnvelope
	decoder := json.NewDecoder(bytes.NewReader(line))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&envelope); err != nil {
		return Request{}, err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return Request{}, errors.New("multiple JSON values")
	}
	if envelope.ProtocolVersion == nil || envelope.Kind == nil || *envelope.Kind != "request" || envelope.MessageID == nil || !validMessageID(*envelope.MessageID) || envelope.Method == nil || *envelope.Method == "" || envelope.SentAt == nil {
		return Request{}, errors.New("missing request fields")
	}
	if _, err := time.Parse(time.RFC3339, *envelope.SentAt); err != nil {
		return Request{}, errors.New("invalid sentAt")
	}
	var payload map[string]json.RawMessage
	if len(envelope.Payload) == 0 || json.Unmarshal(envelope.Payload, &payload) != nil || payload == nil {
		return Request{}, errors.New("payload must be an object")
	}
	return Request{
		ProtocolVersion: *envelope.ProtocolVersion,
		Kind:            *envelope.Kind,
		MessageID:       *envelope.MessageID,
		Method:          *envelope.Method,
		SentAt:          *envelope.SentAt,
		Payload:         envelope.Payload,
	}, nil
}

func validMessageID(value string) bool {
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

func Success(version string, request Request, payload any) Response {
	return Response{ProtocolVersion: version, Kind: "response", MessageID: newID(), RequestID: request.MessageID, Method: request.Method, SentAt: time.Now().UTC().Format(time.RFC3339Nano), Payload: payload}
}

func Failure(version string, request Request, code, message string, retryable bool) Response {
	if !SupportedVersion(version) {
		version = Version10
	}
	return Response{ProtocolVersion: version, Kind: "error", MessageID: newID(), RequestID: request.MessageID, SentAt: time.Now().UTC().Format(time.RFC3339Nano), Error: &ErrorBody{Code: code, Message: message, Retryable: retryable}}
}

func NewEvent(version string, sequence int64, event, taskID string, at time.Time, payload json.RawMessage) Event {
	return Event{ProtocolVersion: version, Kind: "event", MessageID: newID(), SentAt: at.UTC().Format(time.RFC3339Nano), Sequence: sequence, Event: event, TaskID: taskID, PayloadVersion: 1, Payload: payload}
}

func newID() string {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(value[:])
}
