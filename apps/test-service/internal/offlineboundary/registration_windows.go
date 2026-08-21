//go:build windows

package offlineboundary

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"io"
	"net"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/Microsoft/go-winio"
)

const (
	registrationPipeEnvironment  = "UNIT_TEST_IDE_WFP_REGISTRATION_PIPE"
	registrationNonceEnvironment = "UNIT_TEST_IDE_WFP_REGISTRATION_NONCE"
	registrationTimeout          = 5 * time.Second
	maxRegisteredPathBytes       = 32 * 1024
)

type executableRegistrationRequest struct {
	path   string
	result chan error
}

type executableRegistrationServer struct {
	listener net.Listener
	nonce    []byte
	requests chan executableRegistrationRequest
	done     chan struct{}
	close    sync.Once
	mu       sync.Mutex
	active   net.Conn
	closeErr error
}

func registrationPipeName(id []byte) string {
	return `\\.\pipe\offlineboundary-register-` + hex.EncodeToString(id)
}

func newExecutableRegistrationServer(pipeID, nonce []byte) (*executableRegistrationServer, error) {
	if len(pipeID) != 16 || len(nonce) != 32 {
		return nil, GuardianStartFailed
	}
	listener, err := winio.ListenPipe(registrationPipeName(pipeID), &winio.PipeConfig{
		MessageMode: true, InputBufferSize: maxRegisteredPathBytes + 64, OutputBufferSize: 64,
	})
	if err != nil {
		return nil, err
	}
	server := &executableRegistrationServer{
		listener: listener, nonce: append([]byte(nil), nonce...),
		requests: make(chan executableRegistrationRequest),
		done:     make(chan struct{}),
	}
	go server.serve()
	return server, nil
}

func (server *executableRegistrationServer) serve() {
	for {
		conn, err := server.listener.Accept()
		if err != nil {
			return
		}
		server.mu.Lock()
		server.active = conn
		server.mu.Unlock()
		_ = conn.SetDeadline(time.Now().Add(registrationTimeout))
		path, requestErr := readRegistrationRequest(conn, server.nonce)
		if requestErr == nil {
			request := executableRegistrationRequest{path: path, result: make(chan error, 1)}
			select {
			case server.requests <- request:
				select {
				case requestErr = <-request.result:
				case <-server.done:
					requestErr = GuardianStartFailed
				}
			case <-server.done:
				requestErr = GuardianStartFailed
			}
		}
		ack := byte(1)
		if requestErr != nil {
			ack = 0
		}
		_, _ = conn.Write([]byte{ack})
		_ = conn.Close()
		server.mu.Lock()
		if server.active == conn {
			server.active = nil
		}
		server.mu.Unlock()
	}
}

func (server *executableRegistrationServer) Close() error {
	server.close.Do(func() {
		close(server.done)
		result := server.listener.Close()
		server.mu.Lock()
		if server.active != nil {
			result = errors.Join(result, server.active.Close())
		}
		server.closeErr = result
		server.mu.Unlock()
	})
	server.mu.Lock()
	defer server.mu.Unlock()
	return server.closeErr
}

// RegisterExecutableForActiveBoundary is the processhost launch gate. When a
// WFP boundary advertises its private capability in the inherited environment,
// registration and the guardian acknowledgement must complete before launch.
func RegisterExecutableForActiveBoundary(path string) error {
	pipeName := os.Getenv(registrationPipeEnvironment)
	nonceHex := os.Getenv(registrationNonceEnvironment)
	if pipeName == "" && nonceHex == "" {
		return nil
	}
	if path == "" || pipeName == "" || nonceHex == "" ||
		!strings.HasPrefix(pipeName, `\\.\pipe\offlineboundary-register-`) {
		return GuardianStartFailed
	}
	nonce, err := hex.DecodeString(nonceHex)
	if err != nil || len(nonce) != 32 {
		return GuardianStartFailed
	}
	ctx, cancel := context.WithTimeout(context.Background(), registrationTimeout)
	defer cancel()
	conn, err := winio.DialPipeContext(ctx, pipeName)
	if err != nil {
		return GuardianStartFailed
	}
	defer conn.Close() //nolint:errcheck
	_ = conn.SetDeadline(time.Now().Add(registrationTimeout))
	if err := writeRegistrationRequest(conn, nonce, path); err != nil {
		return GuardianStartFailed
	}
	var ack [1]byte
	if _, err := io.ReadFull(conn, ack[:]); err != nil || ack[0] != 1 {
		return GuardianStartFailed
	}
	return nil
}

// ExecutableRegistrationActive reports whether this Service is inside the
// private WFP registration boundary. A partial capability still counts as
// active so callers validate and fail closed before attempting a launch.
func ExecutableRegistrationActive() bool {
	return os.Getenv(registrationPipeEnvironment) != "" ||
		os.Getenv(registrationNonceEnvironment) != ""
}

func writeRegistrationRequest(writer io.Writer, nonce []byte, path string) error {
	pathBytes := []byte(path)
	if len(nonce) != 32 || len(pathBytes) == 0 || len(pathBytes) > maxRegisteredPathBytes || strings.ContainsRune(path, '\x00') {
		return GuardianStartFailed
	}
	proof := registrationProof(nonce, pathBytes)
	payloadSize := 1 + len(proof) + len(pathBytes)
	header := make([]byte, 4)
	binary.LittleEndian.PutUint32(header, uint32(payloadSize))
	if _, err := writer.Write(header); err != nil {
		return err
	}
	payload := make([]byte, payloadSize)
	payload[0] = 1
	copy(payload[1:33], proof)
	copy(payload[33:], pathBytes)
	_, err := writer.Write(payload)
	return err
}

func readRegistrationRequest(reader io.Reader, nonce []byte) (string, error) {
	var header [4]byte
	if _, err := io.ReadFull(reader, header[:]); err != nil {
		return "", err
	}
	size := binary.LittleEndian.Uint32(header[:])
	if size < 34 || size > maxRegisteredPathBytes+33 {
		return "", GuardianStartFailed
	}
	payload := make([]byte, size)
	if _, err := io.ReadFull(reader, payload); err != nil {
		return "", err
	}
	pathBytes := payload[33:]
	if payload[0] != 1 || len(pathBytes) == 0 || !hmac.Equal(payload[1:33], registrationProof(nonce, pathBytes)) ||
		strings.ContainsRune(string(pathBytes), '\x00') {
		return "", GuardianStartFailed
	}
	return string(pathBytes), nil
}

func registrationProof(nonce, path []byte) []byte {
	mac := hmac.New(sha256.New, nonce)
	_, _ = mac.Write([]byte("unit-test-ide/wfp-register-app-id/v1"))
	_, _ = mac.Write(path)
	return mac.Sum(nil)
}
