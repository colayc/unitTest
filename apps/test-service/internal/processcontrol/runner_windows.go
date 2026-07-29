//go:build windows

package processcontrol

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"

	"unit-test-ide.local/test-service/internal/task"
	"unit-test-ide.local/test-service/internal/winprocess"
)

var (
	ErrLeaseIdentityMismatch  = errors.New("process lease identity mismatch")
	ErrProcessOutputOverflow  = errors.New("process output overflow")
	errProcessHostUnavailable = errors.New("process host is unavailable")
	errProcessStartFailed     = errors.New("target process could not start")
	errProcessHostFailed      = errors.New("process host failed")
)

const windowsHostShutdownWait = time.Second

type windowsRunner struct {
	executable string
	operations windowsRunnerOperations
}

var _ Runner = (*windowsRunner)(nil)

type windowsRunnerOperations struct {
	createProtectedJob     func(uint32) (windows.Handle, error)
	createHost             func(string, windows.Handle, windows.Handle, windows.Handle, windows.Handle) (windows.ProcessInformation, error)
	assignProcess          func(windows.Handle, windows.Handle) error
	duplicateObserver      func(windows.Handle) (windows.Handle, error)
	resumeThread           func(windows.Handle) error
	terminateJob           func(windows.Handle, uint32) error
	terminateProcess       func(windows.Handle, uint32) error
	nativeTerminateProcess func(windows.Handle, uint32) error
	waitProcess            func(windows.Handle, uint32) (uint32, error)
	startIdentity          func(windows.Handle) (string, error)
	openProcess            func(uint32) (windows.Handle, error)
	closeHandle            func(windows.Handle) error
}

type windowsCloseAttempt struct {
	done chan struct{}
	err  error
}

type windowsProcess struct {
	specValue     Spec
	control       *os.File
	status        *os.File
	stdout        *os.File
	stderr        *os.File
	frames        *json.Decoder
	jobOwner      *winprocess.HandleOwner
	hostOwner     *winprocess.HandleOwner
	observerOwner *winprocess.HandleOwner
	ops           windowsRunnerOperations

	mu             sync.Mutex
	lease          task.ProcessLease
	startAttempted bool
	started        bool
	stopSent       bool
	writeMu        sync.Mutex

	hostExited       chan struct{}
	output           chan Output
	outputDone       chan struct{}
	outputDiscard    chan struct{}
	done             chan Result
	finished         chan struct{}
	contextStop      chan struct{}
	doneOnce         sync.Once
	closeInitOnce    sync.Once
	shutdownOnce     sync.Once
	hostExitOnce     sync.Once
	controlCloseOnce sync.Once
	statusCloseOnce  sync.Once
	streamCloseOnce  sync.Once
	outputCloseOnce  sync.Once
	discardOnce      sync.Once
	contextStopOnce  sync.Once
	outputOverflow   atomic.Bool
	outputMu         sync.Mutex
	outputClosed     bool
	jobErrMu         sync.Mutex
	jobCloseErr      error
	closeMu          sync.Mutex
	closeRunning     bool
	closeComplete    bool
	closeAttempt     *windowsCloseAttempt
	shutdownErr      error
}

var _ Process = (*windowsProcess)(nil)

func NewRunner(serviceExecutable string) Runner {
	return &windowsRunner{executable: serviceExecutable, operations: defaultWindowsRunnerOperations()}
}

func defaultWindowsRunnerOperations() windowsRunnerOperations {
	return windowsRunnerOperations{
		createProtectedJob: createRunnerProtectedJob,
		createHost:         createSuspendedWindowsHost,
		assignProcess:      windows.AssignProcessToJobObject,
		duplicateObserver:  duplicateWindowsProcessObserver,
		resumeThread: func(thread windows.Handle) error {
			_, err := windows.ResumeThread(thread)
			return err
		},
		terminateJob:           windows.TerminateJobObject,
		terminateProcess:       windows.TerminateProcess,
		nativeTerminateProcess: winprocess.NativeTerminateProcess,
		waitProcess:            windows.WaitForSingleObject,
		startIdentity:          windowsStartIdentityFromHandle,
		openProcess: func(pid uint32) (windows.Handle, error) {
			return windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.PROCESS_TERMINATE|windows.SYNCHRONIZE, false, pid)
		},
		closeHandle: windows.CloseHandle,
	}
}

func (runner *windowsRunner) Prepare(ctx context.Context, spec Spec, taskID, serviceInstanceID string) (Process, error) {
	job, err := runner.operations.createProtectedJob(windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE)
	if err != nil {
		return nil, errProcessHostUnavailable
	}
	jobOwner := winprocess.NewHandleOwner(job, runner.operations.closeHandle)
	controlReader, controlWriter, err := os.Pipe()
	if err != nil {
		_ = jobOwner.CloseEventually()
		return nil, errProcessHostUnavailable
	}
	statusReader, statusWriter, err := os.Pipe()
	if err != nil {
		closeWindowsFiles(controlReader, controlWriter)
		_ = jobOwner.CloseEventually()
		return nil, errProcessHostUnavailable
	}
	stdoutReader, stdoutWriter, err := os.Pipe()
	if err != nil {
		closeWindowsFiles(controlReader, controlWriter, statusReader, statusWriter)
		_ = jobOwner.CloseEventually()
		return nil, errProcessHostUnavailable
	}
	stderrReader, stderrWriter, err := os.Pipe()
	if err != nil {
		closeWindowsFiles(controlReader, controlWriter, statusReader, statusWriter, stdoutReader, stdoutWriter)
		_ = jobOwner.CloseEventually()
		return nil, errProcessHostUnavailable
	}
	closeAll := func() {
		closeWindowsFiles(controlReader, controlWriter, statusReader, statusWriter, stdoutReader, stdoutWriter, stderrReader, stderrWriter)
		_ = jobOwner.CloseEventually()
	}
	if err := clearWindowsFileInheritance(controlReader, controlWriter, statusReader, statusWriter, stdoutReader, stdoutWriter, stderrReader, stderrWriter); err != nil {
		closeAll()
		return nil, errProcessHostUnavailable
	}
	info, err := runner.operations.createHost(runner.executable, windows.Handle(controlReader.Fd()), windows.Handle(statusWriter.Fd()), windows.Handle(stdoutWriter.Fd()), windows.Handle(stderrWriter.Fd()))
	if err != nil {
		closeAll()
		return nil, errProcessHostUnavailable
	}
	closeWindowsFiles(controlReader, statusWriter, stdoutWriter, stderrWriter)
	var observerOwner *winprocess.HandleOwner
	failCreatedHost := func(terminateJob bool) error {
		if terminateJob {
			_, _ = jobOwner.UseExclusive(func(handle windows.Handle) error {
				return runner.operations.terminateJob(handle, 1)
			})
		}
		_ = observerOwner.CloseEventually()
		cleanupErr := winprocess.FailCreatedProcess(info.Process, info.Thread, runner.createdProcessOperations(), 250*time.Millisecond)
		closeWindowsFiles(controlWriter, statusReader, stdoutReader, stderrReader)
		_ = jobOwner.CloseEventually()
		return cleanupErr
	}
	if err := runner.operations.assignProcess(job, info.Process); err != nil {
		if cleanupErr := failCreatedHost(false); cleanupErr != nil {
			return nil, errProcessHostFailed
		}
		return nil, errProcessHostUnavailable
	}
	identity, err := runner.operations.startIdentity(info.Process)
	if err != nil || identity == "" {
		if cleanupErr := failCreatedHost(true); cleanupErr != nil {
			return nil, errProcessHostFailed
		}
		return nil, errProcessHostUnavailable
	}
	observer, err := runner.operations.duplicateObserver(info.Process)
	if err != nil || observer == 0 || observer == windows.InvalidHandle {
		if cleanupErr := failCreatedHost(true); cleanupErr != nil {
			return nil, errProcessHostFailed
		}
		return nil, errProcessHostUnavailable
	}
	observerOwner = winprocess.NewHandleOwner(observer, runner.operations.closeHandle)
	if err := runner.operations.resumeThread(info.Thread); err != nil {
		if cleanupErr := failCreatedHost(true); cleanupErr != nil {
			return nil, errProcessHostFailed
		}
		return nil, errProcessHostUnavailable
	}
	_ = winprocess.NewHandleOwner(info.Thread, runner.operations.closeHandle).CloseEventually()
	process := &windowsProcess{
		specValue:     spec,
		control:       controlWriter,
		status:        statusReader,
		stdout:        stdoutReader,
		stderr:        stderrReader,
		frames:        json.NewDecoder(bufio.NewReader(statusReader)),
		jobOwner:      jobOwner,
		hostOwner:     winprocess.NewHandleOwner(info.Process, runner.operations.closeHandle),
		observerOwner: observerOwner,
		ops:           runner.operations,
		lease: task.ProcessLease{
			TaskID:            taskID,
			HostPID:           int(info.ProcessId),
			HostStartIdentity: identity,
			ServiceInstanceID: serviceInstanceID,
		},
		hostExited:    make(chan struct{}),
		output:        make(chan Output, 64),
		outputDone:    make(chan struct{}),
		outputDiscard: make(chan struct{}),
		done:          make(chan Result, 1),
		finished:      make(chan struct{}),
		contextStop:   make(chan struct{}),
	}
	go process.waitHost()
	go process.copyOutput()
	go process.watchContext(ctx)
	return process, nil
}

func (runner *windowsRunner) createdProcessOperations() winprocess.Operations {
	return winprocess.Operations{
		Terminate:       runner.operations.terminateProcess,
		NativeTerminate: runner.operations.nativeTerminateProcess,
		Wait:            runner.operations.waitProcess,
		Close:           runner.operations.closeHandle,
	}
}

func (runner *windowsRunner) Cleanup(ctx context.Context, lease task.ProcessLease, grace time.Duration) error {
	if lease.HostPID <= 0 || lease.HostStartIdentity == "" {
		return ErrLeaseIdentityMismatch
	}
	handle, err := runner.operations.openProcess(uint32(lease.HostPID))
	if errors.Is(err, windows.ERROR_INVALID_PARAMETER) {
		return nil
	}
	if err != nil {
		return errProcessHostUnavailable
	}
	observerOwner := winprocess.NewHandleOwner(handle, runner.operations.closeHandle)
	defer observerOwner.CloseEventually() //nolint:errcheck
	identity, err := runner.operations.startIdentity(handle)
	if err != nil {
		return errProcessHostUnavailable
	}
	if identity != lease.HostStartIdentity {
		return ErrLeaseIdentityMismatch
	}
	waitResult, waitErr := runner.operations.waitProcess(handle, 0)
	if waitErr != nil {
		return errProcessHostUnavailable
	}
	if waitResult == windows.WAIT_OBJECT_0 {
		return nil
	}
	terminateErr := runner.operations.terminateProcess(handle, 1)
	if grace < 0 {
		grace = 0
	}
	if !waitWindowsHandle(ctx, runner.operations.waitProcess, handle, grace+windowsHostShutdownWait) {
		return errors.Join(redactWindowsOperationError(terminateErr), errProcessHostFailed)
	}
	return nil
}

func (process *windowsProcess) Lease() task.ProcessLease {
	process.mu.Lock()
	defer process.mu.Unlock()
	return process.lease
}

func (process *windowsProcess) Start(ctx context.Context) error {
	process.mu.Lock()
	if process.startAttempted {
		process.mu.Unlock()
		return errProcessStartFailed
	}
	process.startAttempted = true
	process.mu.Unlock()
	process.writeMu.Lock()
	err := json.NewEncoder(process.control).Encode(StartCommand(process.specValue))
	process.writeMu.Unlock()
	if err != nil {
		process.closeControl()
		process.finishAfterHost(Result{Err: errProcessHostFailed})
		return errProcessHostFailed
	}
	status, err := process.readStatus(ctx)
	if err != nil || status.Kind == "error" {
		process.closeControl()
		process.finishAfterHost(Result{Err: errProcessStartFailed})
		return errProcessStartFailed
	}
	if validateWindowsStartedStatus(status) != nil {
		process.closeControl()
		process.finishAfterHost(Result{Err: errProcessHostFailed})
		return errProcessHostFailed
	}
	process.mu.Lock()
	process.started = true
	process.lease.TargetProcessGroup = status.ProcessGroup
	process.mu.Unlock()
	go process.watchExit()
	return nil
}

func (process *windowsProcess) Output() <-chan Output { return process.output }
func (process *windowsProcess) Done() <-chan Result   { return process.done }

func (process *windowsProcess) Terminate(ctx context.Context, grace time.Duration) error {
	select {
	case <-process.finished:
		process.retryRetainedHandles()
		return redactWindowsOperationError(process.outerJobError())
	default:
	}
	process.mu.Lock()
	started := process.started
	if started && !process.stopSent {
		process.stopSent = true
		process.writeMu.Lock()
		err := json.NewEncoder(process.control).Encode(StopCommand())
		process.writeMu.Unlock()
		if err != nil {
			process.mu.Unlock()
			return process.forceTerminate(ctx, errProcessHostFailed)
		}
	}
	process.mu.Unlock()
	if !started {
		process.closeControl()
		_ = process.shutdownHostBounded(ctx)
		process.publish(Result{Err: errProcessHostFailed})
		return nil
	}
	if grace < 0 {
		grace = 0
	}
	if waitWindowsClosedContext(ctx, process.finished, grace) {
		process.closeOuterJob()
		return redactWindowsOperationError(process.outerJobError())
	}
	err := process.forceTerminate(ctx, nil)
	process.closeOuterJob()
	return errors.Join(err, redactWindowsOperationError(process.outerJobError()))
}

func (process *windowsProcess) Close(ctx context.Context) error {
	process.closeMu.Lock()
	if process.closeComplete {
		process.closeMu.Unlock()
		process.retryRetainedHandles()
		return nil
	}
	if process.closeRunning {
		attempt := process.closeAttempt
		process.closeMu.Unlock()
		select {
		case <-attempt.done:
			process.retryRetainedHandles()
			return attempt.err
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	attempt := &windowsCloseAttempt{done: make(chan struct{})}
	process.closeRunning = true
	process.closeAttempt = attempt
	process.closeMu.Unlock()

	err := process.closeWindowsProcess(ctx)
	process.closeMu.Lock()
	attempt.err = err
	process.closeRunning = false
	if err == nil {
		process.closeComplete = true
	}
	close(attempt.done)
	process.closeMu.Unlock()
	process.retryRetainedHandles()
	return err
}

func (process *windowsProcess) closeWindowsProcess(ctx context.Context) error {
	process.closeInitOnce.Do(func() {
		process.contextStopOnce.Do(func() { close(process.contextStop) })
		process.closeControl()
		process.discardOnce.Do(func() { close(process.outputDiscard) })
	})
	var closeErr error
	if !waitWindowsClosedContext(ctx, process.finished, 100*time.Millisecond) {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		shutdownErr := process.shutdownHostBounded(ctx)
		process.publish(Result{Err: errProcessHostFailed})
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if shutdownErr != nil {
			closeErr = errProcessHostFailed
		}
	} else {
		process.closeOuterJob()
		if process.currentCleanupError() != nil {
			closeErr = errProcessHostFailed
		}
	}
	process.closeStatus()
	process.closeOutputReaders()
	process.closeOutput()
	return closeErr
}

func (process *windowsProcess) readStatus(ctx context.Context) (HostStatus, error) {
	type response struct {
		status HostStatus
		err    error
	}
	result := make(chan response, 1)
	go func() {
		var status HostStatus
		err := process.frames.Decode(&status)
		result <- response{status: status, err: err}
	}()
	select {
	case value := <-result:
		return value.status, value.err
	case <-ctx.Done():
		process.closeControl()
		process.closeStatus()
		return HostStatus{}, ctx.Err()
	}
}

func (process *windowsProcess) watchExit() {
	status, err := process.readStatus(context.Background())
	result := Result{}
	if err != nil || status.Kind != "exit" {
		result.Err = errProcessHostFailed
	} else {
		result.ExitCode = status.ExitCode
		if status.ErrorCode != "" {
			result.Err = errProcessHostFailed
		}
	}
	process.finishAfterHost(result)
}

func (process *windowsProcess) watchContext(ctx context.Context) {
	select {
	case <-ctx.Done():
		_ = process.Terminate(context.Background(), 0)
	case <-process.contextStop:
	case <-process.finished:
	}
}

func (process *windowsProcess) waitHost() {
	var result uint32
	used, err := process.observerOwner.Use(func(handle windows.Handle) error {
		var waitErr error
		result, waitErr = process.ops.waitProcess(handle, windows.INFINITE)
		return waitErr
	})
	if used && err == nil && result == windows.WAIT_OBJECT_0 {
		process.markHostExited()
	}
	_ = process.observerOwner.CloseEventually()
}

func (process *windowsProcess) copyOutput() {
	var readers sync.WaitGroup
	readers.Add(2)
	go process.copyStream(&readers, process.stdout, StreamStdout)
	go process.copyStream(&readers, process.stderr, StreamStderr)
	readers.Wait()
	process.closeOutput()
	close(process.outputDone)
}

func (process *windowsProcess) copyStream(readers *sync.WaitGroup, reader *os.File, stream Stream) {
	defer readers.Done()
	defer reader.Close()
	buffer := make([]byte, 32*1024)
	for {
		count, err := reader.Read(buffer)
		if count > 0 {
			data := append([]byte(nil), buffer[:count]...)
			process.sendOutput(Output{Stream: stream, Data: data})
		}
		if err != nil {
			return
		}
	}
}

func (process *windowsProcess) finishAfterHost(result Result) {
	if !waitWindowsClosed(process.hostExited, windowsHostShutdownWait) {
		result.Err = errors.Join(result.Err, errProcessHostFailed)
		_ = process.shutdownHostBounded(context.Background())
	}
	if !waitWindowsClosed(process.outputDone, windowsHostShutdownWait) {
		result.Err = errors.Join(result.Err, errProcessHostFailed)
		process.closeOutputReaders()
	}
	_ = process.closeHostHandle()
	process.closeOutput()
	if result.Err == nil && process.outputOverflow.Load() {
		result.Err = ErrProcessOutputOverflow
	}
	process.publish(result)
}

func (process *windowsProcess) publish(result Result) {
	process.doneOnce.Do(func() {
		// Make completion visible to Terminate before the public Done result can
		// be observed, so Terminate-after-Done is always idempotent.
		close(process.finished)
		process.done <- result
		close(process.done)
	})
}

func (process *windowsProcess) forceTerminate(ctx context.Context, prior error) error {
	prior = errors.Join(prior, ctx.Err())
	if err := process.shutdownHostBounded(ctx); err != nil {
		prior = errors.Join(prior, errProcessHostFailed)
	}
	process.publish(Result{Err: errProcessHostFailed})
	return prior
}

func (process *windowsProcess) shutdownHostBounded(ctx context.Context) error {
	process.shutdownOnce.Do(func() {
		process.closeControl()
		jobOperationErr := process.terminateOuterJob()
		process.closeOuterJob()
		process.closeStatus()
		process.closeOutputReaders()

		var cleanupErr error
		var hostCloseErr error
		select {
		case <-process.hostExited:
			hostCloseErr = process.closeHostHandle()
		default:
			host := process.hostOwner.Take()
			if host != 0 && host != windows.InvalidHandle {
				cleanupErr = winprocess.CleanupProcess(host, process.hostCleanupOperations(), boundedWindowsWait(ctx, 100*time.Millisecond))
				if cleanupErr == nil {
					// CleanupProcess only succeeds after a signaled wait. Publish
					// that proof synchronously so an immediate Manager Close
					// cannot mistake observer scheduling latency for a live Host.
					process.markHostExited()
				}
			}
		}
		if !waitWindowsClosedContext(ctx, process.outputDone, 100*time.Millisecond) {
			process.closeOutput()
		}
		if cleanupErr != nil || jobOperationErr != nil || process.outerJobError() != nil || hostCloseErr != nil {
			process.shutdownErr = errProcessHostFailed
		}
	})
	return process.shutdownErr
}

func (process *windowsProcess) hostCleanupOperations() winprocess.Operations {
	return winprocess.Operations{
		Terminate:       process.ops.terminateProcess,
		NativeTerminate: process.ops.nativeTerminateProcess,
		Wait: func(handle windows.Handle, timeout uint32) (uint32, error) {
			return process.ops.waitProcess(handle, timeout)
		},
		Close: process.ops.closeHandle,
	}
}

func (process *windowsProcess) closeOuterJob() {
	err := process.jobOwner.CloseEventually()
	process.jobErrMu.Lock()
	process.jobCloseErr = err
	process.jobErrMu.Unlock()
}

func (process *windowsProcess) terminateOuterJob() error {
	_, err := process.jobOwner.UseExclusive(func(handle windows.Handle) error {
		return process.ops.terminateJob(handle, 1)
	})
	return err
}

func (process *windowsProcess) outerJobError() error {
	process.jobErrMu.Lock()
	defer process.jobErrMu.Unlock()
	return process.jobCloseErr
}

func (process *windowsProcess) currentCleanupError() error {
	if process.outerJobError() != nil || process.jobOwner.Open() {
		return errProcessHostFailed
	}
	select {
	case <-process.hostExited:
		if process.closeHostHandle() != nil || process.hostOwner.Open() {
			return errProcessHostFailed
		}
		return nil
	default:
		return errProcessHostFailed
	}
}

func (process *windowsProcess) markHostExited() {
	process.hostExitOnce.Do(func() { close(process.hostExited) })
}

func (process *windowsProcess) closeHostHandle() error {
	return process.hostOwner.CloseEventually()
}

func (process *windowsProcess) retryRetainedHandles() {
	process.closeOuterJob()
	select {
	case <-process.hostExited:
		_ = process.closeHostHandle()
	default:
	}
}

func (process *windowsProcess) closeControl() {
	process.controlCloseOnce.Do(func() {
		process.writeMu.Lock()
		defer process.writeMu.Unlock()
		_ = process.control.Close()
	})
}

func (process *windowsProcess) closeStatus() {
	process.statusCloseOnce.Do(func() { _ = process.status.Close() })
}

func (process *windowsProcess) closeOutputReaders() {
	process.streamCloseOnce.Do(func() {
		if process.stdout != nil {
			_ = process.stdout.Close()
		}
		if process.stderr != nil {
			_ = process.stderr.Close()
		}
	})
}

func (process *windowsProcess) sendOutput(value Output) {
	process.outputMu.Lock()
	defer process.outputMu.Unlock()
	if process.outputClosed {
		return
	}
	select {
	case process.output <- value:
	case <-process.outputDiscard:
	default:
		process.outputOverflow.Store(true)
	}
}

func (process *windowsProcess) closeOutput() {
	process.outputCloseOnce.Do(func() {
		process.outputMu.Lock()
		defer process.outputMu.Unlock()
		process.outputClosed = true
		close(process.output)
	})
}

func validateWindowsStartedStatus(status HostStatus) error {
	if status.Kind != "started" || status.PID <= 0 || status.ProcessGroup != status.PID {
		return errProcessHostFailed
	}
	return nil
}

func redactWindowsOperationError(err error) error {
	if err == nil || errors.Is(err, ErrLeaseIdentityMismatch) || errors.Is(err, errProcessHostUnavailable) || errors.Is(err, errProcessHostFailed) {
		return err
	}
	return errProcessHostFailed
}

func waitWindowsClosed(done <-chan struct{}, duration time.Duration) bool {
	if duration < 0 {
		duration = 0
	}
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-done:
		return true
	case <-timer.C:
		return false
	}
}

func waitWindowsClosedContext(ctx context.Context, done <-chan struct{}, duration time.Duration) bool {
	if duration < 0 {
		duration = 0
	}
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-done:
		return true
	case <-ctx.Done():
		return false
	case <-timer.C:
		return false
	}
}

func boundedWindowsWait(ctx context.Context, maximum time.Duration) time.Duration {
	if deadline, ok := ctx.Deadline(); ok {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return 0
		}
		if remaining < maximum {
			return remaining
		}
	}
	return maximum
}

func waitWindowsHandle(ctx context.Context, wait func(windows.Handle, uint32) (uint32, error), handle windows.Handle, duration time.Duration) bool {
	deadline := time.Now().Add(duration)
	for {
		result, err := wait(handle, 0)
		if err == nil && result == windows.WAIT_OBJECT_0 {
			return true
		}
		if err != nil || time.Now().After(deadline) {
			return false
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func windowsStartIdentity(pid int) (string, error) {
	return windowsStartIdentityWithOperations(
		pid,
		func(value uint32) (windows.Handle, error) {
			return windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, value)
		},
		windowsStartIdentityFromHandle,
		windows.CloseHandle,
	)
}

func windowsStartIdentityWithOperations(pid int, openProcess func(uint32) (windows.Handle, error), identity func(windows.Handle) (string, error), closeHandle func(windows.Handle) error) (string, error) {
	if pid <= 0 {
		return "", ErrLeaseIdentityMismatch
	}
	handle, err := openProcess(uint32(pid))
	if err != nil {
		return "", err
	}
	observerOwner := winprocess.NewHandleOwner(handle, closeHandle)
	defer observerOwner.CloseEventually() //nolint:errcheck
	return identity(handle)
}

func duplicateWindowsProcessObserver(process windows.Handle) (windows.Handle, error) {
	var observer windows.Handle
	current := windows.CurrentProcess()
	if err := windows.DuplicateHandle(current, process, current, &observer, windows.SYNCHRONIZE, false, 0); err != nil {
		return 0, err
	}
	return observer, nil
}

func windowsStartIdentityFromHandle(handle windows.Handle) (string, error) {
	if handle == 0 || handle == windows.InvalidHandle {
		return "", ErrLeaseIdentityMismatch
	}
	var creation, exit, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(handle, &creation, &exit, &kernel, &user); err != nil {
		return "", err
	}
	value := uint64(creation.HighDateTime)<<32 | uint64(creation.LowDateTime)
	if value == 0 {
		return "", ErrLeaseIdentityMismatch
	}
	return strconv.FormatUint(value, 10), nil
}

func createRunnerProtectedJob(limitFlags uint32) (windows.Handle, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return 0, err
	}
	limits := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	limits.BasicLimitInformation.LimitFlags = limitFlags
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&limits)), uint32(unsafe.Sizeof(limits))); err != nil {
		_ = winprocess.NewHandleOwner(job, windows.CloseHandle).CloseEventually()
		return 0, err
	}
	return job, nil
}

func createSuspendedWindowsHost(executable string, control, status, stdout, stderr windows.Handle) (windows.ProcessInformation, error) {
	environment := hostWindowsEnvironment(os.Environ(), status)
	return createRunnerSuspendedProcess(executable, []string{"--process-host"}, environment, control, stdout, stderr, []windows.Handle{control, status, stdout, stderr})
}

func createRunnerSuspendedProcess(executable string, args, environment []string, stdin, stdout, stderr windows.Handle, inherited []windows.Handle) (windows.ProcessInformation, error) {
	var info windows.ProcessInformation
	for _, handle := range inherited {
		if handle == 0 || handle == windows.InvalidHandle {
			return info, errors.New("invalid inherited handle")
		}
		if err := windows.SetHandleInformation(handle, windows.HANDLE_FLAG_INHERIT, windows.HANDLE_FLAG_INHERIT); err != nil {
			return info, err
		}
		defer windows.SetHandleInformation(handle, windows.HANDLE_FLAG_INHERIT, 0) //nolint:errcheck
	}
	attributes, err := windows.NewProcThreadAttributeList(1)
	if err != nil {
		return info, err
	}
	defer attributes.Delete()
	if err := attributes.Update(windows.PROC_THREAD_ATTRIBUTE_HANDLE_LIST, unsafe.Pointer(&inherited[0]), uintptr(len(inherited))*unsafe.Sizeof(inherited[0])); err != nil {
		return info, err
	}
	startup := windows.StartupInfoEx{}
	startup.StartupInfo.Cb = uint32(unsafe.Sizeof(startup))
	startup.StartupInfo.Flags = windows.STARTF_USESTDHANDLES
	startup.StartupInfo.StdInput = stdin
	startup.StartupInfo.StdOutput = stdout
	startup.StartupInfo.StdErr = stderr
	startup.ProcThreadAttributeList = attributes.List()
	application, err := windows.UTF16PtrFromString(executable)
	if err != nil {
		return info, err
	}
	environmentBlock, err := runnerWindowsEnvironmentBlock(environment)
	if err != nil {
		return info, err
	}
	flags := uint32(windows.CREATE_SUSPENDED | windows.CREATE_NO_WINDOW | windows.CREATE_UNICODE_ENVIRONMENT | windows.EXTENDED_STARTUPINFO_PRESENT)
	create := func(extraFlags uint32) error {
		commandLine, commandErr := windows.UTF16FromString(windows.ComposeCommandLine(append([]string{executable}, args...)))
		if commandErr != nil {
			return commandErr
		}
		return windows.CreateProcess(application, &commandLine[0], nil, nil, true, flags|extraFlags, &environmentBlock[0], nil, &startup.StartupInfo, &info)
	}
	err = create(windows.CREATE_BREAKAWAY_FROM_JOB)
	if errors.Is(err, windows.ERROR_ACCESS_DENIED) {
		err = create(0)
	}
	return info, err
}

func hostWindowsEnvironment(base []string, status windows.Handle) []string {
	result := make([]string, 0, len(base)+1)
	for _, entry := range base {
		if strings.HasPrefix(strings.ToUpper(entry), "UNIT_TEST_IDE_STATUS_HANDLE=") {
			continue
		}
		result = append(result, entry)
	}
	return append(result, "UNIT_TEST_IDE_STATUS_HANDLE="+strconv.FormatUint(uint64(status), 10))
}

func runnerWindowsEnvironmentBlock(environment []string) ([]uint16, error) {
	environment = append([]string(nil), environment...)
	sort.SliceStable(environment, func(i, j int) bool { return strings.ToUpper(environment[i]) < strings.ToUpper(environment[j]) })
	block := make([]uint16, 0)
	for _, entry := range environment {
		if strings.IndexByte(entry, 0) >= 0 {
			return nil, errors.New("environment contains NUL")
		}
		for _, value := range entry {
			block = utf16.AppendRune(block, value)
		}
		block = append(block, 0)
	}
	block = append(block, 0)
	if len(environment) == 0 {
		block = append(block, 0)
	}
	return block, nil
}

func closeWindowsFiles(files ...io.Closer) {
	for _, file := range files {
		if file != nil {
			_ = file.Close()
		}
	}
}

func clearWindowsFileInheritance(files ...*os.File) error {
	for _, file := range files {
		if file == nil {
			continue
		}
		if err := windows.SetHandleInformation(windows.Handle(file.Fd()), windows.HANDLE_FLAG_INHERIT, 0); err != nil {
			return err
		}
	}
	return nil
}
