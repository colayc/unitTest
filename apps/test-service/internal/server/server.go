package server

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"sync"
	"time"

	"unit-test-ide.local/test-service/internal/eventbroker"
	"unit-test-ide.local/test-service/internal/protocol"
	"unit-test-ide.local/test-service/internal/session"
	"unit-test-ide.local/test-service/internal/task"
)

const MaxMessageBytes = 1024 * 1024

var errOutboundMessageTooLarge = errors.New("outbound message exceeds the 1 MiB limit")

type ConnectionConfig struct {
	HandshakeTimeout time.Duration
	IdleTimeout      time.Duration
	WriteTimeout     time.Duration
}

var DefaultConnectionConfig = ConnectionConfig{
	HandshakeTimeout: 10 * time.Second,
	IdleTimeout:      2 * time.Minute,
	WriteTimeout:     10 * time.Second,
}

type outboundMessage struct {
	value any
	done  chan error
}

type runningSubscription struct {
	subscription *eventbroker.Subscription
	cancel       context.CancelFunc
	done         <-chan struct{}
}

func ServeConnection(connection net.Conn, active *session.Session) {
	ServeConnectionWithConfig(connection, active, DefaultConnectionConfig)
}

func ServeConnectionWithConfig(connection net.Conn, active *session.Session, config ConnectionConfig) {
	connectionContext, cancelConnection := context.WithCancel(context.Background())
	var closeOnce sync.Once
	closeConnection := func() {
		closeOnce.Do(func() {
			cancelConnection()
			_ = connection.Close()
		})
	}

	outbound := make(chan outboundMessage, 32)
	writerDone := make(chan struct{})
	go connectionWriter(connection, config.WriteTimeout, outbound, writerDone, closeConnection)

	var forwarders sync.WaitGroup
	var subscriptionState sync.Mutex
	var subscriptionGeneration uint64
	var activeSubscription *runningSubscription
	retireActiveSubscription := func() {
		if activeSubscription == nil {
			return
		}
		subscriptionState.Lock()
		subscriptionGeneration++
		subscriptionState.Unlock()
		activeSubscription.cancel()
		activeSubscription.subscription.Close()
		<-activeSubscription.done
		activeSubscription = nil
	}
	defer func() {
		cancelConnection()
		retireActiveSubscription()
		closeConnection()
		forwarders.Wait()
		close(outbound)
		<-writerDone
	}()

	scanner := bufio.NewScanner(connection)
	scanner.Buffer(make([]byte, 4*1024), MaxMessageBytes+2)
	var handshakeDeadline time.Time
	if config.HandshakeTimeout > 0 {
		handshakeDeadline = time.Now().Add(config.HandshakeTimeout)
	}

	for {
		if active.Authenticated() {
			if activeSubscription != nil {
				if err := connection.SetReadDeadline(time.Time{}); err != nil {
					return
				}
			} else if config.IdleTimeout > 0 {
				if err := connection.SetReadDeadline(time.Now().Add(config.IdleTimeout)); err != nil {
					return
				}
			}
		} else if !handshakeDeadline.IsZero() {
			if err := connection.SetReadDeadline(handshakeDeadline); err != nil {
				return
			}
		}
		if !scanner.Scan() {
			break
		}
		if len(scanner.Bytes()) > MaxMessageBytes {
			request := protocol.Request{MessageID: "00000000000000000000000000000000"}
			_ = sendAndWait(connectionContext, outbound, writerDone, protocol.Failure(protocol.Version10, request, "INVALID_MESSAGE", "message exceeds the 1 MiB limit", false))
			return
		}
		request, err := protocol.DecodeRequest(scanner.Bytes())
		if err != nil {
			invalid := protocol.Request{MessageID: "00000000000000000000000000000000"}
			_ = sendAndWait(connectionContext, outbound, writerDone, protocol.Failure(protocol.Version10, invalid, "INVALID_MESSAGE", "message is invalid", false))
			return
		}
		if request.Method == "events/subscribe" {
			retireActiveSubscription()
		}
		result := active.Handle(connectionContext, request)
		responseWritten, err := enqueueOutbound(connectionContext, outbound, writerDone, result.Response)
		if err != nil {
			if result.Subscription != nil {
				result.Subscription.Close()
			}
			return
		}
		if result.Subscription != nil {
			if err := connection.SetReadDeadline(time.Time{}); err != nil {
				result.Subscription.Close()
				return
			}
			forwarderContext, cancelForwarder := context.WithCancel(connectionContext)
			forwarderDone := make(chan struct{})
			subscriptionState.Lock()
			subscriptionGeneration++
			generation := subscriptionGeneration
			subscriptionState.Unlock()
			activeSubscription = &runningSubscription{subscription: result.Subscription, cancel: cancelForwarder, done: forwarderDone}
			forwarders.Add(1)
			go func(subscription *eventbroker.Subscription, subscribeRequest protocol.Request) {
				defer forwarders.Done()
				defer close(forwarderDone)
				forwardSubscription(forwarderContext, subscription, subscribeRequest, outbound, writerDone, func() {
					subscriptionState.Lock()
					defer subscriptionState.Unlock()
					if subscriptionGeneration == generation {
						closeConnection()
					}
				})
			}(result.Subscription, request)
			result.Subscription.Activate()
		}
		if err := waitOutbound(connectionContext, writerDone, responseWritten); err != nil {
			return
		}
		select {
		case <-active.ShutdownRequested():
			return
		default:
		}
	}
	if scanErr := scanner.Err(); scanErr != nil {
		var networkError net.Error
		if errors.As(scanErr, &networkError) && networkError.Timeout() {
			return
		}
		request := protocol.Request{MessageID: "00000000000000000000000000000000"}
		_ = sendAndWait(connectionContext, outbound, writerDone, protocol.Failure(protocol.Version10, request, "INVALID_MESSAGE", "message exceeds the 1 MiB limit", false))
	}
}

func connectionWriter(connection net.Conn, timeout time.Duration, outbound <-chan outboundMessage, done chan<- struct{}, closeConnection func()) {
	defer close(done)
	for message := range outbound {
		line, encodeErr := encodeOutboundLine(message.value)
		terminal := encodeErr != nil
		if encodeErr != nil {
			line, _ = encodeOutboundLine(outboundLimitFailure(message.value))
		}
		var writeErr error
		if timeout > 0 {
			writeErr = connection.SetWriteDeadline(time.Now().Add(timeout))
		}
		if writeErr == nil {
			if len(line) == 0 {
				writeErr = encodeErr
			} else {
				writeErr = writeAll(connection, line)
			}
		}
		resultErr := writeErr
		if resultErr == nil {
			resultErr = encodeErr
		}
		if message.done != nil {
			message.done <- resultErr
			close(message.done)
		}
		if terminal || writeErr != nil {
			closeConnection()
			return
		}
	}
}

func encodeOutboundLine(value any) ([]byte, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	if len(encoded) > MaxMessageBytes {
		return nil, errOutboundMessageTooLarge
	}
	return append(encoded, '\n'), nil
}

func outboundLimitFailure(value any) protocol.Response {
	version := protocol.Version10
	requestID := "00000000000000000000000000000000"
	switch envelope := value.(type) {
	case protocol.Response:
		version = envelope.ProtocolVersion
		if envelope.RequestID != "" {
			requestID = envelope.RequestID
		}
	case protocol.Event:
		version = envelope.ProtocolVersion
	}
	return protocol.Failure(version, protocol.Request{MessageID: requestID}, "SERVICE_UNHEALTHY", "outbound message exceeds the 1 MiB limit", true)
}

func writeAll(connection net.Conn, value []byte) error {
	for len(value) > 0 {
		written, err := connection.Write(value)
		if err != nil {
			return err
		}
		if written == 0 {
			return net.ErrClosed
		}
		value = value[written:]
	}
	return nil
}

func sendAndWait(ctx context.Context, outbound chan<- outboundMessage, writerDone <-chan struct{}, value any) error {
	written, err := enqueueOutbound(ctx, outbound, writerDone, value)
	if err != nil {
		return err
	}
	return waitOutbound(ctx, writerDone, written)
}

func enqueueOutbound(ctx context.Context, outbound chan<- outboundMessage, writerDone <-chan struct{}, value any) (<-chan error, error) {
	written := make(chan error, 1)
	select {
	case outbound <- outboundMessage{value: value, done: written}:
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-writerDone:
		return nil, net.ErrClosed
	}
	return written, nil
}

func waitOutbound(ctx context.Context, writerDone <-chan struct{}, written <-chan error) error {
	select {
	case err := <-written:
		return err
	case <-ctx.Done():
		return ctx.Err()
	case <-writerDone:
		select {
		case err := <-written:
			return err
		default:
			return net.ErrClosed
		}
	}
}

func sendOutbound(ctx context.Context, outbound chan<- outboundMessage, writerDone <-chan struct{}, value any) bool {
	select {
	case outbound <- outboundMessage{value: value}:
		return true
	case <-ctx.Done():
		return false
	case <-writerDone:
		return false
	}
}

func forwardSubscription(ctx context.Context, subscription *eventbroker.Subscription, subscribeRequest protocol.Request, outbound chan<- outboundMessage, writerDone <-chan struct{}, closeConnection func()) {
	defer subscription.Close()
	events, subscriptionErrors := subscription.Events, subscription.Errors
	for events != nil || subscriptionErrors != nil {
		select {
		case <-ctx.Done():
			return
		case event, ok := <-events:
			if !ok {
				events = nil
				continue
			}
			projected, err := toProtocolEvent(event, subscribeRequest.ProtocolVersion)
			if err != nil {
				response := subscriptionFailure(subscribeRequest, err)
				_ = sendAndWait(ctx, outbound, writerDone, response)
				closeConnection()
				return
			}
			if !sendOutbound(ctx, outbound, writerDone, projected) {
				return
			}
		case err, ok := <-subscriptionErrors:
			if !ok {
				subscriptionErrors = nil
				continue
			}
			response := subscriptionFailure(subscribeRequest, err)
			_ = sendAndWait(ctx, outbound, writerDone, response)
			closeConnection()
			return
		}
	}
}

func toProtocolEvent(event task.Event, version string) (protocol.Event, error) {
	eventType := event.Type
	payload := event.Payload
	if version == protocol.Version11 {
		switch event.Type {
		case task.EventTaskStepStarted, task.EventTaskStepFinished, task.EventTaskDiagnostic:
			eventType = task.EventTaskOutput
			payload = json.RawMessage(`{"stream":"service","text":"","truncated":false}`)
		case task.EventTaskOutput:
			var err error
			payload, err = projectV11Output(event.Payload)
			if err != nil {
				return protocol.Event{}, err
			}
		}
	}
	return protocol.NewEvent(version, event.Sequence, string(eventType), event.TaskID, event.At, payload), nil
}

func projectV11Output(payload json.RawMessage) (json.RawMessage, error) {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	root, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	delimiter, ok := root.(json.Delim)
	if !ok || delimiter != '{' {
		return nil, errors.New("task output payload must be an object")
	}

	seen := make(map[string]bool, 4)
	var stream, text string
	var truncated bool
	for decoder.More() {
		nameToken, err := decoder.Token()
		if err != nil {
			return nil, err
		}
		name, ok := nameToken.(string)
		if !ok {
			return nil, errors.New("task output payload field name must be a string")
		}
		switch name {
		case "stepId", "stream", "text", "truncated":
		default:
			return nil, errors.New("task output payload has an unknown field")
		}
		if seen[name] {
			return nil, errors.New("task output payload has a duplicate field")
		}
		seen[name] = true

		value, err := decoder.Token()
		if err != nil {
			return nil, err
		}
		switch name {
		case "stepId":
			if _, ok := value.(string); !ok {
				return nil, errors.New("task output payload stepId must be a string")
			}
		case "stream":
			stream, ok = value.(string)
			if !ok {
				return nil, errors.New("task output payload stream must be a string")
			}
		case "text":
			text, ok = value.(string)
			if !ok {
				return nil, errors.New("task output payload text must be a string")
			}
		case "truncated":
			truncated, ok = value.(bool)
			if !ok {
				return nil, errors.New("task output payload truncated must be a boolean")
			}
		}
	}
	end, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	delimiter, ok = end.(json.Delim)
	if !ok || delimiter != '}' {
		return nil, errors.New("task output payload object is not closed")
	}
	if !seen["stream"] || !seen["text"] || !seen["truncated"] {
		return nil, errors.New("task output payload is missing required fields")
	}
	if _, err := decoder.Token(); err != io.EOF {
		return nil, errors.New("task output payload has trailing data")
	}
	return json.Marshal(struct {
		Stream    string `json:"stream"`
		Text      string `json:"text"`
		Truncated bool   `json:"truncated"`
	}{
		Stream:    stream,
		Text:      text,
		Truncated: truncated,
	})
}

func subscriptionFailure(request protocol.Request, err error) protocol.Response {
	if errors.Is(err, eventbroker.ErrSubscriberTooSlow) {
		return protocol.Failure(request.ProtocolVersion, request, "SUBSCRIBER_TOO_SLOW", "event subscriber is too slow", true)
	}
	return protocol.Failure(request.ProtocolVersion, request, "STORAGE_UNAVAILABLE", "event subscription failed", true)
}
