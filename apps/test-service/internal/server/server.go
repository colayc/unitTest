package server

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net"
	"time"

	"unit-test-ide.local/test-service/internal/protocol"
	"unit-test-ide.local/test-service/internal/session"
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

func ServeConnection(connection net.Conn, active *session.Session) {
	ServeConnectionWithConfig(connection, active, DefaultConnectionConfig)
}

func ServeConnectionWithConfig(connection net.Conn, active *session.Session, config ConnectionConfig) {
	defer connection.Close()
	scanner := bufio.NewScanner(connection)
	scanner.Buffer(make([]byte, 4*1024), MaxMessageBytes+2)
	encoder := json.NewEncoder(connection)
	var handshakeDeadline time.Time
	if config.HandshakeTimeout > 0 {
		handshakeDeadline = time.Now().Add(config.HandshakeTimeout)
	}
	for {
		if active.Authenticated() {
			if config.IdleTimeout > 0 {
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
			writeResponse(connection, encoder, config.WriteTimeout, protocol.Failure(protocol.Version10, protocol.Request{MessageID: "00000000000000000000000000000000"}, "INVALID_MESSAGE", "message exceeds the 1 MiB limit", false))
			return
		}
		request, err := protocol.DecodeRequest(scanner.Bytes())
		if err != nil {
			writeResponse(connection, encoder, config.WriteTimeout, protocol.Failure(protocol.Version10, protocol.Request{MessageID: "00000000000000000000000000000000"}, "INVALID_MESSAGE", "message is invalid", false))
			return
		}
		if err := writeResponse(connection, encoder, config.WriteTimeout, active.Handle(context.Background(), request)); err != nil {
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
		_ = writeResponse(connection, encoder, config.WriteTimeout, protocol.Failure(protocol.Version10, protocol.Request{MessageID: "00000000000000000000000000000000"}, "INVALID_MESSAGE", "message exceeds the 1 MiB limit", false))
	}
}

func writeResponse(connection net.Conn, encoder *json.Encoder, timeout time.Duration, response protocol.Response) error {
	if timeout > 0 {
		if err := connection.SetWriteDeadline(time.Now().Add(timeout)); err != nil {
			return err
		}
	}
	return encoder.Encode(response)
}
