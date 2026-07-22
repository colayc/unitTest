package server

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net"
	"sync"
	"time"

	"unit-test-ide.local/test-service/internal/eventbroker"
	"unit-test-ide.local/test-service/internal/protocol"
	"unit-test-ide.local/test-service/internal/session"
	"unit-test-ide.local/test-service/internal/task"
)

const MaxMessageBytes = 1024 * 1024

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
	var activeSubscription *eventbroker.Subscription
	defer func() {
		cancelConnection()
		if activeSubscription != nil {
			activeSubscription.Close()
		}
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
		result := active.Handle(connectionContext, request)
		if err := sendAndWait(connectionContext, outbound, writerDone, result.Response); err != nil {
			return
		}
		if result.Subscription != nil {
			if activeSubscription != nil {
				activeSubscription.Close()
			}
			activeSubscription = result.Subscription
			if err := connection.SetReadDeadline(time.Time{}); err != nil {
				return
			}
			forwarders.Add(1)
			go func(subscription *eventbroker.Subscription, subscribeRequest protocol.Request) {
				defer forwarders.Done()
				forwardSubscription(connectionContext, subscription, subscribeRequest, outbound, writerDone, closeConnection)
			}(result.Subscription, request)
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
	encoder := json.NewEncoder(connection)
	for message := range outbound {
		var err error
		if timeout > 0 {
			err = connection.SetWriteDeadline(time.Now().Add(timeout))
		}
		if err == nil {
			err = encoder.Encode(message.value)
		}
		if message.done != nil {
			message.done <- err
			close(message.done)
		}
		if err != nil {
			closeConnection()
			return
		}
	}
}

func sendAndWait(ctx context.Context, outbound chan<- outboundMessage, writerDone <-chan struct{}, value any) error {
	written := make(chan error, 1)
	select {
	case outbound <- outboundMessage{value: value, done: written}:
	case <-ctx.Done():
		return ctx.Err()
	case <-writerDone:
		return net.ErrClosed
	}
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
			if !sendOutbound(ctx, outbound, writerDone, toProtocolEvent(event)) {
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

func toProtocolEvent(event task.Event) protocol.Event {
	return protocol.NewEvent(event.Sequence, string(event.Type), event.TaskID, event.At, event.Payload)
}

func subscriptionFailure(request protocol.Request, err error) protocol.Response {
	if errors.Is(err, eventbroker.ErrSubscriberTooSlow) {
		return protocol.Failure(protocol.Version11, request, "SUBSCRIBER_TOO_SLOW", "event subscriber is too slow", true)
	}
	return protocol.Failure(protocol.Version11, request, "STORAGE_UNAVAILABLE", "event subscription failed", true)
}
