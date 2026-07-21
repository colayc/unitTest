package server

import (
	"bufio"
	"encoding/json"
	"net"

	"unit-test-ide.local/test-service/internal/protocol"
	"unit-test-ide.local/test-service/internal/session"
)

const MaxMessageBytes = 1024 * 1024

func ServeConnection(connection net.Conn, active *session.Session) {
	defer connection.Close()
	scanner := bufio.NewScanner(connection)
	scanner.Buffer(make([]byte, 64*1024), MaxMessageBytes)
	encoder := json.NewEncoder(connection)
	for scanner.Scan() {
		request, err := protocol.DecodeRequest(scanner.Bytes())
		if err != nil {
			_ = encoder.Encode(protocol.Failure(protocol.Request{MessageID: "00000000000000000000000000000000"}, "INVALID_MESSAGE", "message is invalid", false))
			return
		}
		if err := encoder.Encode(active.Handle(request)); err != nil {
			return
		}
		select {
		case <-active.ShutdownRequested():
			return
		default:
		}
	}
	if scanner.Err() != nil {
		_ = encoder.Encode(protocol.Failure(protocol.Request{MessageID: "00000000000000000000000000000000"}, "INVALID_MESSAGE", "message exceeds the 1 MiB limit", false))
	}
}
