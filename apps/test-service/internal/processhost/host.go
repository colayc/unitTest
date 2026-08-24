package processhost

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"unit-test-ide.local/test-service/internal/cmake"
	"unit-test-ide.local/test-service/internal/processcontrol"
)

func processHostDebugf(format string, args ...any) {
	if os.Getenv("UNIT_TEST_IDE_DEBUG_PROCESSHOST") != "1" {
		return
	}
	line := fmt.Sprintf("processhost-debug "+format+"\n", args...)
	_, _ = fmt.Fprint(os.Stderr, line)
	if path := os.Getenv("UNIT_TEST_IDE_PROCESSHOST_DEBUG_FILE"); path != "" {
		if file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600); err == nil {
			_, _ = file.WriteString(line)
			_ = file.Close()
		}
	}
}

const maxHostFrameBytes = 4 * 1024 * 1024

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

type synchronizedStatusWriter struct {
	mu     sync.Mutex
	writer io.Writer
	err    error
}

func (writer *synchronizedStatusWriter) Write(
	value processcontrol.HostStatus,
) error {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	if writer.err != nil {
		return writer.err
	}
	writer.err = writeStatus(writer.writer, value)
	return writer.err
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
	processHostDebugf("run-start")
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
	if len(start.Spec.Batch) != 0 {
		processHostDebugf("run-mode=batch count=%d", len(start.Spec.Batch))
		return runBatch(
			ctx,
			platform,
			owner,
			frames,
			status,
			*start.Spec,
		)
	}
	processHostDebugf("run-mode=single")
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

type batchTarget struct {
	item         processcontrol.BatchItem
	target       Target
	startedAt    time.Time
	stdoutReader *os.File
	stderrReader *os.File
}

type batchWaitResult struct {
	result processcontrol.HostChildResult
}

func runBatch(
	ctx context.Context,
	platform Platform,
	owner *controlOwner,
	frames *frameReader,
	status io.Writer,
	spec processcontrol.Spec,
) int {
	if !validBatchSpec(spec) {
		_ = writeStatus(status, processcontrol.HostStatus{
			Kind: "error", ErrorCode: "INVALID_HOST_COMMAND",
			Message: "invalid batch start command",
		})
		return 2
	}
	writer := &synchronizedStatusWriter{writer: status}
	targets := make([]batchTarget, 0, len(spec.Batch))
	for _, item := range spec.Batch {
		processHostDebugf("batch-start id=%s args=%d timeout-ms=%d", item.ID, len(item.Args), item.TimeoutMS)
		stdoutReader, stdoutWriter, err := os.Pipe()
		if err != nil {
			cleanupBatchTargets(platform, targets)
			_ = writer.Write(processcontrol.HostStatus{
				Kind: "error", ErrorCode: "PROCESS_START_FAILED",
				Message: "target process could not start",
			})
			return 1
		}
		stderrReader, stderrWriter, err := os.Pipe()
		if err != nil {
			_ = stdoutReader.Close()
			_ = stdoutWriter.Close()
			cleanupBatchTargets(platform, targets)
			_ = writer.Write(processcontrol.HostStatus{
				Kind: "error", ErrorCode: "PROCESS_START_FAILED",
				Message: "target process could not start",
			})
			return 1
		}
		target, startErr := platform.Start(
			processcontrol.Spec{
				Executable:   item.Executable,
				LaunchPlan:   append([]string(nil), item.LaunchPlan...),
				LaunchInputs: append([]cmake.FingerprintFile(nil), item.LaunchInputs...),
				Args:         append([]string(nil), item.Args...),
				Dir:          item.Dir,
				Env:          append([]string(nil), item.Env...),
				EnvUnset: append(
					[]string(nil),
					item.EnvUnset...,
				),
			},
			stdoutWriter,
			stderrWriter,
		)
		startedAt := time.Now()
		_ = stdoutWriter.Close()
		_ = stderrWriter.Close()
		if startErr != nil || target == nil {
			processHostDebugf("batch-start-failed id=%s", item.ID)
			_ = stdoutReader.Close()
			_ = stderrReader.Close()
			cleanupBatchTargets(platform, targets)
			_ = writer.Write(processcontrol.HostStatus{
				Kind: "error", ErrorCode: "PROCESS_START_FAILED",
				Message: "target process could not start",
			})
			return 1
		}
		processHostDebugf("batch-started id=%s pid=%d group=%d", item.ID, target.PID(), target.ProcessGroup())
		targets = append(targets, batchTarget{
			item: item, target: target,
			startedAt:    startedAt,
			stdoutReader: stdoutReader,
			stderrReader: stderrReader,
		})
	}
	targetProcessGroups := make([]int, len(targets))
	for index, target := range targets {
		targetProcessGroups[index] = target.target.ProcessGroup()
		if targetProcessGroups[index] <= 1 {
			cleanupBatchTargets(platform, targets)
			_ = writer.Write(processcontrol.HostStatus{
				Kind: "error", ErrorCode: "PROCESS_START_FAILED",
				Message: "target process identity unavailable",
			})
			return 1
		}
	}
	if err := writer.Write(processcontrol.HostStatus{
		Kind:                "started",
		PID:                 targets[0].target.PID(),
		TargetProcessGroups: targetProcessGroups,
	}); err != nil {
		cleanupBatchTargets(platform, targets)
		return 1
	}

	stop := make(chan struct{})
	controlDone := readControl(owner, frames, stop)
	var outputCopies sync.WaitGroup
	outputCopies.Add(len(targets) * 2)
	for _, target := range targets {
		go copyBatchOutput(
			&outputCopies,
			writer,
			target.item.ID,
			processcontrol.StreamStdout,
			target.stdoutReader,
		)
		go copyBatchOutput(
			&outputCopies,
			writer,
			target.item.ID,
			processcontrol.StreamStderr,
			target.stderrReader,
		)
	}

	results := make(chan batchWaitResult, len(targets))
	for _, candidate := range targets {
		go func(target batchTarget) {
			timeout := time.Duration(target.item.TimeoutMS) *
				time.Millisecond
			remaining := timeout - time.Since(target.startedAt)
			if remaining <= 0 {
				remaining = time.Nanosecond
			}
			waitContext, cancel := context.WithTimeout(
				ctx,
				remaining,
			)
			result := waitOrStop(
				waitContext,
				platform,
				target.target,
				stop,
			)
			timedOut := errors.Is(
				waitContext.Err(),
				context.DeadlineExceeded,
			) && ctx.Err() == nil
			cancel()
			processHostDebugf("batch-waited id=%s exit=%d timed-out=%t err=%t", target.item.ID, result.exitCode, timedOut, result.err != nil)
			child := processcontrol.HostChildResult{
				ID:       target.item.ID,
				ExitCode: result.exitCode,
				TimedOut: timedOut,
			}
			if result.err != nil {
				child.ErrorCode = "PROCESS_WAIT_FAILED"
			}
			results <- batchWaitResult{result: child}
		}(candidate)
	}
	children := make(
		[]processcontrol.HostChildResult,
		0,
		len(targets),
	)
	for range targets {
		children = append(children, (<-results).result)
	}
	sort.Slice(children, func(left, right int) bool {
		return children[left].ID < children[right].ID
	})
	outputCopies.Wait()
	owner.Close()
	commandResult := <-controlDone
	if commandResult.invalid {
		_ = writer.Write(processcontrol.HostStatus{
			Kind: "error", ErrorCode: "INVALID_HOST_COMMAND",
			Message: "invalid stop command",
		})
		return 2
	}
	errorCode := ""
	for _, child := range children {
		if child.ErrorCode != "" {
			errorCode = "PROCESS_WAIT_FAILED"
			break
		}
	}
	if err := writer.Write(processcontrol.HostStatus{
		Kind: "exit", ErrorCode: errorCode,
		Children: children,
	}); err != nil {
		return 1
	}
	if errorCode != "" {
		return 1
	}
	return 0
}

func validBatchSpec(spec processcontrol.Spec) bool {
	if spec.Executable != "" || len(spec.Args) != 0 ||
		spec.Dir != "" || len(spec.Env) != 0 ||
		len(spec.EnvUnset) != 0 ||
		len(spec.Batch) < 1 || len(spec.Batch) > 256 {
		return false
	}
	seen := make(map[string]struct{}, len(spec.Batch))
	for _, item := range spec.Batch {
		if !validBatchID(item.ID) ||
			item.Executable == "" || item.Dir == "" ||
			item.TimeoutMS < 1 ||
			item.TimeoutMS > (24*time.Hour).Milliseconds() {
			return false
		}
		if _, duplicate := seen[item.ID]; duplicate {
			return false
		}
		seen[item.ID] = struct{}{}
	}
	return true
}

func validBatchID(value string) bool {
	if len(value) < 1 || len(value) > 64 {
		return false
	}
	for index := range len(value) {
		character := value[index]
		if character >= 'a' && character <= 'z' ||
			index > 0 && character >= '0' &&
				character <= '9' ||
			index > 0 && (character == '-' ||
				character == '_') {
			continue
		}
		return false
	}
	return true
}

func cleanupBatchTargets(
	platform Platform,
	targets []batchTarget,
) {
	for _, target := range targets {
		_ = platform.Terminate(target.target, 0)
		_, _ = target.target.Wait()
		_ = target.stdoutReader.Close()
		_ = target.stderrReader.Close()
	}
}

func copyBatchOutput(
	wait *sync.WaitGroup,
	writer *synchronizedStatusWriter,
	source string,
	stream processcontrol.Stream,
	reader *os.File,
) {
	defer wait.Done()
	defer reader.Close()
	buffer := make([]byte, 16*1024)
	for {
		count, err := reader.Read(buffer)
		if count != 0 {
			_ = writer.Write(processcontrol.HostStatus{
				Kind: "output", Source: source,
				Stream: stream,
				Data: append(
					[]byte(nil),
					buffer[:count]...,
				),
			})
		}
		if err != nil {
			return
		}
	}
}
