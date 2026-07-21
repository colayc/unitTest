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

const Version = "1.0"

type Request struct {
	ProtocolVersion string          `json:"protocolVersion"`
	Kind            string          `json:"kind"`
	MessageID       string          `json:"messageId"`
	Method          string          `json:"method"`
	SentAt          string          `json:"sentAt,omitempty"`
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

func DecodeRequest(line []byte) (Request, error) {
	var value Request
	decoder := json.NewDecoder(bytes.NewReader(line))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return Request{}, err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return Request{}, errors.New("multiple JSON values")
	}
	if value.Kind != "request" || value.MessageID == "" || value.Method == "" {
		return Request{}, errors.New("missing request fields")
	}
	return value, nil
}

func Success(request Request, payload any) Response {
	return Response{ProtocolVersion: Version, Kind: "response", MessageID: newID(), RequestID: request.MessageID, Method: request.Method, SentAt: time.Now().UTC().Format(time.RFC3339Nano), Payload: payload}
}

func Failure(request Request, code, message string, retryable bool) Response {
	return Response{ProtocolVersion: Version, Kind: "error", MessageID: newID(), RequestID: request.MessageID, SentAt: time.Now().UTC().Format(time.RFC3339Nano), Error: &ErrorBody{Code: code, Message: message, Retryable: retryable}}
}

func newID() string {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(value[:])
}
