package processhost

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"unit-test-ide.local/test-service/internal/processcontrol"
)

const maxHostFrameBytes = 64 * 1024

func serviceOwnedTargetEnvironmentKey(value string) bool {
	upper := strings.ToUpper(value)
	return strings.HasPrefix(upper, "UTIDE_") ||
		strings.HasPrefix(upper, "UNIT_TEST_IDE_") ||
		upper == "UNIT_TEST_SERVICE_TOKEN"
}

type Target interface {
	PID() int
	ProcessGroup() int
	// Wait must complete after every Platform.Terminate invocation, including
	// when Terminate returns an error.
	Wait() (int, error)
}

type Platform interface {
	Start(processcontrol.Spec, io.Writer, io.Writer) (Target, error)
	// Terminate must initiate target-tree cleanup and cause Target.Wait to
	// complete, even when cleanup itself returns an error. Run waits for that
	// completion rather than timing out and abandoning a live target tree.
	Terminate(Target, time.Duration) error
}

type waitResult struct {
	exitCode int
	err      error
}

type controlResult struct {
	invalid bool
}

type frameReader struct {
	reader *bufio.Reader
}

var errInvalidHostFrame = errors.New("invalid host frame")

type controlOwner struct {
	// control is the production inherited pipe. Requiring io.ReadCloser is
	// what lets Run join a decoder blocked in Read without leaking it.
	control io.ReadCloser
	closed  chan struct{}
	once    sync.Once
}

func newControlOwner(reader io.Reader) (*controlOwner, bool) {
	control, ok := reader.(io.ReadCloser)
	if !ok {
		return nil, false
	}
	return &controlOwner{control: control, closed: make(chan struct{})}, true
}

func (owner *controlOwner) Close() {
	owner.once.Do(func() {
		close(owner.closed)
		_ = owner.control.Close()
	})
}

func (owner *controlOwner) Closed() bool {
	select {
	case <-owner.closed:
		return true
	default:
		return false
	}
}

func newFrameReader(reader io.Reader) *frameReader {
	return &frameReader{reader: bufio.NewReaderSize(reader, maxHostFrameBytes+3)}
}

func (reader *frameReader) command() (processcontrol.HostCommand, error) {
	line, err := reader.reader.ReadSlice('\n')
	if errors.Is(err, bufio.ErrBufferFull) {
		return processcontrol.HostCommand{}, errInvalidHostFrame
	}
	if err != nil && !errors.Is(err, io.EOF) {
		if len(line) != 0 {
			return processcontrol.HostCommand{}, errInvalidHostFrame
		}
		return processcontrol.HostCommand{}, err
	}
	if errors.Is(err, io.EOF) && len(line) == 0 {
		return processcontrol.HostCommand{}, io.EOF
	}
	line = bytes.TrimSuffix(line, []byte{'\n'})
	line = bytes.TrimSuffix(line, []byte{'\r'})
	if len(line) > maxHostFrameBytes {
		return processcontrol.HostCommand{}, errInvalidHostFrame
	}
	if len(line) == 0 || !utf8.Valid(line) {
		return processcontrol.HostCommand{}, errInvalidHostFrame
	}
	decoder := json.NewDecoder(bytes.NewReader(line))
	decoder.DisallowUnknownFields()
	var command processcontrol.HostCommand
	if err := decoder.Decode(&command); err != nil {
		return processcontrol.HostCommand{}, errInvalidHostFrame
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return processcontrol.HostCommand{}, errInvalidHostFrame
	}
	return command, nil
}

func writeStatus(writer io.Writer, value processcontrol.HostStatus) error {
	return json.NewEncoder(writer).Encode(value)
}

func waitOrStop(ctx context.Context, platform Platform, target Target, stop <-chan struct{}) waitResult {
	done := make(chan waitResult, 1)
	go func() {
		code, err := target.Wait()
		done <- waitResult{exitCode: code, err: err}
	}()
	select {
	case result := <-done:
		if err := platform.Terminate(target, 2*time.Second); err != nil && result.err == nil {
			result.err = err
		}
		return result
	case <-stop:
		err := platform.Terminate(target, 2*time.Second)
		result := <-done
		if result.err == nil {
			result.err = err
		}
		return result
	case <-ctx.Done():
		err := platform.Terminate(target, 2*time.Second)
		result := <-done
		if result.err == nil {
			result.err = err
		}
		return result
	}
}

func readControl(owner *controlOwner, frames *frameReader, stop chan<- struct{}) <-chan controlResult {
	done := make(chan controlResult, 1)
	go func() {
		defer close(done)
		stopped := false
		signalStop := sync.OnceFunc(func() { close(stop) })
		for {
			command, err := frames.command()
			if errors.Is(err, io.EOF) {
				signalStop()
				done <- controlResult{}
				return
			}
			if err != nil {
				if owner.Closed() && !errors.Is(err, errInvalidHostFrame) {
					done <- controlResult{}
					return
				}
				signalStop()
				done <- controlResult{invalid: true}
				return
			}
			if stopped || command.Kind != "stop" || command.Spec != nil {
				signalStop()
				done <- controlResult{invalid: true}
				return
			}
			stopped = true
			signalStop()
		}
	}()
	return done
}

// Run owns control for the duration of the Host protocol. Control must
// implement io.ReadCloser so target completion or context cancellation can
// interrupt and join a blocked command read. Run closes it exactly once.
func Run(ctx context.Context, platform Platform, control io.Reader, status io.Writer, stdout, stderr io.Writer) int {
	owner, ok := newControlOwner(control)
	if !ok {
		_ = writeStatus(status, processcontrol.HostStatus{Kind: "error", ErrorCode: "INVALID_HOST_CONTROL", Message: "invalid host control"})
		return 2
	}
	defer owner.Close()
	frames := newFrameReader(owner.control)
	start, err := frames.command()
	if err != nil || start.Kind != "start" || start.Spec == nil {
		_ = writeStatus(status, processcontrol.HostStatus{Kind: "error", ErrorCode: "INVALID_HOST_COMMAND", Message: "invalid start command"})
		return 2
	}
	target, err := platform.Start(*start.Spec, stdout, stderr)
	if err != nil || target == nil {
		_ = writeStatus(status, processcontrol.HostStatus{Kind: "error", ErrorCode: "PROCESS_START_FAILED", Message: "target process could not start"})
		return 1
	}
	if err := writeStatus(status, processcontrol.HostStatus{Kind: "started", PID: target.PID(), ProcessGroup: target.ProcessGroup()}); err != nil {
		_ = platform.Terminate(target, 2*time.Second)
		_, _ = target.Wait()
		return 1
	}

	stop := make(chan struct{})
	controlDone := readControl(owner, frames, stop)
	result := waitOrStop(ctx, platform, target, stop)
	owner.Close()
	commandResult := <-controlDone
	if commandResult.invalid {
		_ = writeStatus(status, processcontrol.HostStatus{Kind: "error", ErrorCode: "INVALID_HOST_COMMAND", Message: "invalid stop command"})
		return 2
	}
	errorCode := ""
	if result.err != nil {
		errorCode = "PROCESS_WAIT_FAILED"
	}
	if err := writeStatus(status, processcontrol.HostStatus{Kind: "exit", ExitCode: result.exitCode, ErrorCode: errorCode}); err != nil {
		return 1
	}
	if result.err != nil {
		return 1
	}
	return 0
}
