package server

import (
	"bufio"
	"encoding/json"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"unit-test-ide.local/test-service/internal/protocol"
)

func TestConnectionWriterEnforcesEncodedLineLimitForResponsesAndEvents(t *testing.T) {
	tests := []struct {
		name     string
		kind     string
		size     int
		oversize bool
	}{
		{"response at limit", "response", MaxMessageBytes, false},
		{"response over limit", "response", MaxMessageBytes + 1, true},
		{"event at limit", "event", MaxMessageBytes, false},
		{"event over limit", "event", MaxMessageBytes + 1, true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			value := sizedOutboundEnvelope(t, test.kind, test.size)
			client, service := net.Pipe()
			defer client.Close()
			outbound := make(chan outboundMessage, 1)
			writerDone := make(chan struct{})
			var closeOnce sync.Once
			closeConnection := func() { closeOnce.Do(func() { _ = service.Close() }) }
			go connectionWriter(service, time.Second, outbound, writerDone, closeConnection)
			ack := make(chan error, 1)
			outbound <- outboundMessage{value: value, done: ack}

			line, err := bufio.NewReader(client).ReadBytes('\n')
			if err != nil {
				t.Fatal(err)
			}
			writeErr := <-ack
			if !test.oversize {
				if writeErr != nil {
					t.Fatal(writeErr)
				}
				if len(line) != MaxMessageBytes+1 || line[len(line)-1] != '\n' {
					t.Fatalf("encoded line length=%d", len(line))
				}
				var envelope struct {
					Kind string `json:"kind"`
				}
				if err := json.Unmarshal(line, &envelope); err != nil || envelope.Kind != test.kind {
					t.Fatalf("envelope=%#v err=%v", envelope, err)
				}
				close(outbound)
				<-writerDone
				_ = service.Close()
				return
			}

			if writeErr == nil {
				t.Fatal("oversized outbound message reported a successful write")
			}
			if len(line)-1 > MaxMessageBytes {
				t.Fatalf("writer emitted oversized fallback line length=%d", len(line)-1)
			}
			var failure protocol.Response
			if err := json.Unmarshal(line, &failure); err != nil {
				t.Fatal(err)
			}
			if failure.Error == nil || failure.Error.Code != "SERVICE_UNHEALTHY" || !failure.Error.Retryable {
				t.Fatalf("fallback=%#v", failure)
			}
			<-writerDone
		})
	}
}

func sizedOutboundEnvelope(t *testing.T, kind string, size int) any {
	t.Helper()
	const messageID = "11111111111111111111111111111111"
	const requestID = "22222222222222222222222222222222"
	const sentAt = "2026-07-22T00:00:00Z"
	if kind == "response" {
		value := protocol.Response{
			ProtocolVersion: protocol.Version11, Kind: "response", MessageID: messageID,
			RequestID: requestID, Method: "tasks/get", SentAt: sentAt, Payload: map[string]string{"data": ""},
		}
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		value.Payload = map[string]string{"data": strings.Repeat("x", size-len(raw))}
		assertEncodedSize(t, value, size)
		return value
	}
	value := protocol.Event{
		ProtocolVersion: protocol.Version11, Kind: "event", MessageID: messageID, SentAt: sentAt,
		Sequence: 1, Event: "task.output", TaskID: requestID, PayloadVersion: 1, Payload: json.RawMessage(`{"data":""}`),
	}
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	value.Payload = json.RawMessage(`{"data":"` + strings.Repeat("x", size-len(raw)) + `"}`)
	assertEncodedSize(t, value, size)
	return value
}

func assertEncodedSize(t *testing.T, value any, size int) {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) != size {
		t.Fatalf("fixture encoded size=%d, want %d", len(raw), size)
	}
}
