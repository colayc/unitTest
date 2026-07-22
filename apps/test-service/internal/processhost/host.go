package processhost

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"time"
	"unicode/utf8"

	"unit-test-ide.local/test-service/internal/processcontrol"
)

const maxHostFrameBytes = 64 * 1024

type Target interface {
	PID() int
	ProcessGroup() int
	Wait() (int, error)
}

type Platform interface {
	Start(processcontrol.Spec, io.Writer, io.Writer) (Target, error)
	Terminate(Target, time.Duration) error
}

type waitResult struct {
	exitCode int
	err      error
	stopped  bool
}

type controlResult struct {
	invalid bool
}

type frameReader struct {
	reader *bufio.Reader
}

func newFrameReader(reader io.Reader) *frameReader {
	return &frameReader{reader: bufio.NewReaderSize(reader, maxHostFrameBytes+3)}
}

func (reader *frameReader) command() (processcontrol.HostCommand, error) {
	line, err := reader.reader.ReadSlice('\n')
	if errors.Is(err, bufio.ErrBufferFull) {
		return processcontrol.HostCommand{}, errors.New("host frame exceeds limit")
	}
	if err != nil && !errors.Is(err, io.EOF) {
		return processcontrol.HostCommand{}, err
	}
	if errors.Is(err, io.EOF) && len(line) == 0 {
		return processcontrol.HostCommand{}, io.EOF
	}
	line = bytes.TrimSuffix(line, []byte{'\n'})
	line = bytes.TrimSuffix(line, []byte{'\r'})
	if len(line) > maxHostFrameBytes {
		return processcontrol.HostCommand{}, errors.New("host frame exceeds limit")
	}
	if len(line) == 0 || !utf8.Valid(line) {
		return processcontrol.HostCommand{}, errors.New("invalid host frame")
	}
	decoder := json.NewDecoder(bytes.NewReader(line))
	decoder.DisallowUnknownFields()
	var command processcontrol.HostCommand
	if err := decoder.Decode(&command); err != nil {
		return processcontrol.HostCommand{}, err
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return processcontrol.HostCommand{}, errors.New("host frame has trailing data")
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
		result.stopped = true
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

func Run(ctx context.Context, platform Platform, control io.Reader, status io.Writer, stdout, stderr io.Writer) int {
	defer closeControl(control)
	frames := newFrameReader(control)
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
	controlDone := make(chan controlResult, 1)
	go func() {
		command, err := frames.command()
		invalid := err != nil && !errors.Is(err, io.EOF)
		if err == nil && (command.Kind != "stop" || command.Spec != nil) {
			invalid = true
		}
		controlDone <- controlResult{invalid: invalid}
		close(stop)
	}()

	result := waitOrStop(ctx, platform, target, stop)
	var commandResult controlResult
	select {
	case commandResult = <-controlDone:
	default:
		closeControl(control)
		commandResult = <-controlDone
	}
	closeControl(control)
	if result.stopped && commandResult.invalid {
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

func closeControl(control io.Reader) {
	if closer, ok := control.(io.Closer); ok {
		_ = closer.Close()
	}
}
