//go:build linux

package processcontrol

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.org/x/sys/unix"

	"unit-test-ide.local/test-service/internal/task"
)

var (
	ErrLeaseIdentityMismatch  = errors.New("process lease identity mismatch")
	errProcessHostUnavailable = errors.New("process host is unavailable")
	errProcessStartFailed     = errors.New("target process could not start")
	errProcessHostFailed      = errors.New("process host failed")
)

const (
	statusHandleNumber = 3
	hostShutdownWait   = time.Second
)

type unixRunner struct {
	executable string
}

var _ Runner = (*unixRunner)(nil)

type unixProcess struct {
	host         *exec.Cmd
	specValue    Spec
	control      *os.File
	status       *os.File
	statusFrames *json.Decoder
	stdout       *os.File
	stderr       *os.File

	mu             sync.Mutex
	lease          task.ProcessLease
	startAttempted bool
	started        bool
	stopSent       bool

	writeMu sync.Mutex

	hostExited       chan struct{}
	output           chan Output
	outputDone       chan struct{}
	outputDiscard    chan struct{}
	done             chan Result
	finished         chan struct{}
	doneOnce         sync.Once
	closeOnce        sync.Once
	controlCloseOnce sync.Once
	statusCloseOnce  sync.Once
	discardOnce      sync.Once
}

var _ Process = (*unixProcess)(nil)

func NewRunner(serviceExecutable string) Runner {
	return &unixRunner{executable: serviceExecutable}
}

func (runner *unixRunner) Prepare(ctx context.Context, spec Spec, taskID, serviceInstanceID string) (Process, error) {
	controlReader, controlWriter, err := os.Pipe()
	if err != nil {
		return nil, errProcessHostUnavailable
	}
	statusReader, statusWriter, err := os.Pipe()
	if err != nil {
		closeFiles(controlReader, controlWriter)
		return nil, errProcessHostUnavailable
	}
	stdoutReader, stdoutWriter, err := os.Pipe()
	if err != nil {
		closeFiles(controlReader, controlWriter, statusReader, statusWriter)
		return nil, errProcessHostUnavailable
	}
	stderrReader, stderrWriter, err := os.Pipe()
	if err != nil {
		closeFiles(controlReader, controlWriter, statusReader, statusWriter, stdoutReader, stdoutWriter)
		return nil, errProcessHostUnavailable
	}

	host := exec.CommandContext(ctx, runner.executable, "--process-host")
	host.Stdin = controlReader
	host.Stdout = stdoutWriter
	host.Stderr = stderrWriter
	host.ExtraFiles = []*os.File{statusWriter}
	host.Env = append(os.Environ(), "UNIT_TEST_IDE_STATUS_HANDLE="+strconv.Itoa(statusHandleNumber))
	host.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Pdeathsig: syscall.SIGTERM}
	host.Cancel = func() error {
		if host.Process == nil {
			return os.ErrProcessDone
		}
		err := host.Process.Signal(syscall.SIGTERM)
		if errors.Is(err, os.ErrProcessDone) {
			return nil
		}
		return err
	}
	host.WaitDelay = 3 * time.Second
	if err := host.Start(); err != nil {
		closeFiles(controlReader, controlWriter, statusReader, statusWriter, stdoutReader, stdoutWriter, stderrReader, stderrWriter)
		return nil, errProcessHostUnavailable
	}
	closeFiles(controlReader, statusWriter, stdoutWriter, stderrWriter)

	identity, err := linuxStartIdentity(host.Process.Pid)
	if err != nil {
		_ = host.Process.Kill()
		_ = host.Wait()
		closeFiles(controlWriter, statusReader, stdoutReader, stderrReader)
		return nil, errProcessHostUnavailable
	}

	process := &unixProcess{
		host:         host,
		specValue:    spec,
		control:      controlWriter,
		status:       statusReader,
		statusFrames: json.NewDecoder(bufio.NewReader(statusReader)),
		stdout:       stdoutReader,
		stderr:       stderrReader,
		lease: task.ProcessLease{
			TaskID:            taskID,
			HostPID:           host.Process.Pid,
			HostStartIdentity: identity,
			ServiceInstanceID: serviceInstanceID,
		},
		hostExited:    make(chan struct{}),
		output:        make(chan Output, 64),
		outputDone:    make(chan struct{}),
		outputDiscard: make(chan struct{}),
		done:          make(chan Result, 1),
		finished:      make(chan struct{}),
	}
	go process.waitHost()
	go process.copyOutput()
	return process, nil
}

func (runner *unixRunner) Cleanup(ctx context.Context, lease task.ProcessLease, grace time.Duration) error {
	if lease.HostPID <= 1 || lease.HostStartIdentity == "" {
		return ErrLeaseIdentityMismatch
	}
	if lease.TargetProcessGroup != 0 {
		if err := validateLinuxSignalTarget(lease.TargetProcessGroup); err != nil {
			return err
		}
	}
	identity, err := linuxStartIdentity(lease.HostPID)
	hostExists := err == nil
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return errProcessHostUnavailable
	}
	if hostExists && identity != lease.HostStartIdentity {
		return ErrLeaseIdentityMismatch
	}
	if !hostExists && lease.TargetProcessGroup == 0 {
		return nil
	}
	if lease.TargetProcessGroup > 1 {
		exists, err := linuxOwnedGroupExists(lease.TargetProcessGroup, lease.HostPID)
		if err != nil {
			return err
		}
		if !exists && !hostExists {
			return nil
		}
	}
	if grace < 0 {
		grace = 0
	}

	var cleanupErr error
	if lease.TargetProcessGroup > 1 {
		if err := signalOwnedLinuxGroup(lease.TargetProcessGroup, lease.HostPID, unix.SIGTERM); err != nil {
			cleanupErr = errProcessHostFailed
		}
	}
	if hostExists {
		if err := signalLinuxPID(lease.HostPID, unix.SIGTERM); err != nil {
			cleanupErr = errors.Join(cleanupErr, errProcessHostFailed)
		}
	}
	if waitLeaseGone(ctx, lease, grace) {
		return cleanupErr
	}

	identity, err = linuxStartIdentity(lease.HostPID)
	hostExists = err == nil
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return errors.Join(cleanupErr, errProcessHostUnavailable)
	}
	if hostExists && identity != lease.HostStartIdentity {
		return errors.Join(cleanupErr, ErrLeaseIdentityMismatch)
	}
	if lease.TargetProcessGroup > 1 {
		if err := signalOwnedLinuxGroup(lease.TargetProcessGroup, lease.HostPID, unix.SIGKILL); err != nil {
			cleanupErr = errors.Join(cleanupErr, errProcessHostFailed)
		}
	}
	if hostExists {
		if err := signalLinuxPID(lease.HostPID, unix.SIGKILL); err != nil {
			cleanupErr = errors.Join(cleanupErr, errProcessHostFailed)
		}
	}
	if !waitLeaseGone(ctx, lease, hostShutdownWait) {
		cleanupErr = errors.Join(cleanupErr, errProcessHostFailed)
	}
	return cleanupErr
}

func (process *unixProcess) Lease() task.ProcessLease {
	process.mu.Lock()
	defer process.mu.Unlock()
	return process.lease
}

func (process *unixProcess) Start(ctx context.Context) error {
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
	if status.Kind != "started" || status.PID <= 0 || status.ProcessGroup <= 0 || status.PID != status.ProcessGroup {
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

func (process *unixProcess) Output() <-chan Output { return process.output }
func (process *unixProcess) Done() <-chan Result   { return process.done }

func (process *unixProcess) Terminate(ctx context.Context, grace time.Duration) error {
	select {
	case <-process.finished:
		return nil
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
			return process.forceTerminate(ctx, grace, errProcessHostFailed)
		}
	}
	process.mu.Unlock()
	if !started {
		process.closeControl()
		process.finishAfterHost(Result{Err: errProcessHostFailed})
		return nil
	}
	if grace < 0 {
		grace = 0
	}
	if waitClosed(ctx, process.finished, grace) {
		return nil
	}
	return process.forceTerminate(ctx, hostShutdownWait, nil)
}

func (process *unixProcess) Close() error {
	process.closeOnce.Do(func() {
		process.closeControl()
		process.discardOnce.Do(func() { close(process.outputDiscard) })
		process.mu.Lock()
		started := process.started
		process.mu.Unlock()
		if !started {
			process.finishAfterHost(Result{Err: errProcessHostFailed})
		}
	})
	<-process.hostExited
	<-process.outputDone
	process.closeStatus()
	return nil
}

func (process *unixProcess) readStatus(ctx context.Context) (HostStatus, error) {
	type response struct {
		status HostStatus
		err    error
	}
	result := make(chan response, 1)
	go func() {
		var status HostStatus
		err := process.statusFrames.Decode(&status)
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

func (process *unixProcess) watchExit() {
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

func (process *unixProcess) finishAfterHost(result Result) {
	<-process.hostExited
	process.publish(result)
}

func (process *unixProcess) publish(result Result) {
	process.doneOnce.Do(func() {
		process.done <- result
		close(process.done)
		close(process.finished)
	})
}

func (process *unixProcess) waitHost() {
	_ = process.host.Wait()
	close(process.hostExited)
}

func (process *unixProcess) copyOutput() {
	var readers sync.WaitGroup
	readers.Add(2)
	go process.copyStream(&readers, process.stdout, StreamStdout)
	go process.copyStream(&readers, process.stderr, StreamStderr)
	readers.Wait()
	close(process.output)
	close(process.outputDone)
}

func (process *unixProcess) copyStream(readers *sync.WaitGroup, reader *os.File, stream Stream) {
	defer readers.Done()
	defer reader.Close()
	buffer := make([]byte, 32*1024)
	for {
		count, err := reader.Read(buffer)
		if count > 0 {
			data := append([]byte(nil), buffer[:count]...)
			select {
			case process.output <- Output{Stream: stream, Data: data}:
			case <-process.outputDiscard:
			}
		}
		if err != nil {
			return
		}
	}
}

func (process *unixProcess) closeControl() {
	process.controlCloseOnce.Do(func() {
		process.writeMu.Lock()
		defer process.writeMu.Unlock()
		_ = process.control.Close()
	})
}

func (process *unixProcess) closeStatus() {
	process.statusCloseOnce.Do(func() { _ = process.status.Close() })
}

func (process *unixProcess) forceTerminate(ctx context.Context, wait time.Duration, prior error) error {
	prior = errors.Join(prior, ctx.Err())
	lease := process.Lease()
	identity, err := linuxStartIdentity(lease.HostPID)
	if err == nil && identity == lease.HostStartIdentity {
		if lease.TargetProcessGroup > 1 {
			if err := signalOwnedLinuxGroup(lease.TargetProcessGroup, lease.HostPID, unix.SIGKILL); err != nil {
				prior = errors.Join(prior, errProcessHostFailed)
			}
		}
		if !waitClosed(ctx, process.hostExited, 100*time.Millisecond) {
			if err := signalLinuxGroup(lease.HostPID, unix.SIGTERM); err != nil {
				prior = errors.Join(prior, errProcessHostFailed)
			}
		}
		if !waitClosed(ctx, process.hostExited, wait) {
			identity, err = linuxStartIdentity(lease.HostPID)
			if err == nil && identity == lease.HostStartIdentity {
				if err := signalLinuxGroup(lease.HostPID, unix.SIGKILL); err != nil {
					prior = errors.Join(prior, errProcessHostFailed)
				}
			} else if err == nil {
				prior = errors.Join(prior, ErrLeaseIdentityMismatch)
			}
		}
	} else if err == nil {
		prior = errors.Join(prior, ErrLeaseIdentityMismatch)
	}
	process.closeControl()
	if !waitClosed(ctx, process.hostExited, hostShutdownWait) {
		prior = errors.Join(prior, errProcessHostFailed)
	}
	return prior
}

func waitClosed(ctx context.Context, done <-chan struct{}, duration time.Duration) bool {
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

func waitLeaseGone(ctx context.Context, lease task.ProcessLease, duration time.Duration) bool {
	deadline := time.Now().Add(duration)
	for {
		hostGone := !linuxPIDExists(lease.HostPID)
		groupGone := lease.TargetProcessGroup <= 0 || !linuxGroupExists(lease.TargetProcessGroup)
		if hostGone && groupGone {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func linuxStartIdentity(pid int) (string, error) {
	contents, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return "", err
	}
	return parseLinuxStartIdentity(contents)
}

func parseLinuxStartIdentity(contents []byte) (string, error) {
	stat, err := parseLinuxProcessStat(contents)
	if err != nil {
		return "", err
	}
	return stat.startIdentity, nil
}

type linuxProcessStat struct {
	processGroup  int
	session       int
	startIdentity string
}

func parseLinuxProcessStat(contents []byte) (linuxProcessStat, error) {
	closing := strings.LastIndexByte(string(contents), ')')
	if closing < 0 || closing+1 >= len(contents) {
		return linuxProcessStat{}, errors.New("invalid process identity")
	}
	fields := strings.Fields(string(contents[closing+1:]))
	if len(fields) <= 19 {
		return linuxProcessStat{}, errors.New("invalid process identity")
	}
	processGroup, err := strconv.Atoi(fields[2])
	if err != nil || processGroup <= 0 {
		return linuxProcessStat{}, errors.New("invalid process identity")
	}
	session, err := strconv.Atoi(fields[3])
	if err != nil || session <= 0 {
		return linuxProcessStat{}, errors.New("invalid process identity")
	}
	if _, err := strconv.ParseUint(fields[19], 10, 64); err != nil {
		return linuxProcessStat{}, errors.New("invalid process identity")
	}
	return linuxProcessStat{processGroup: processGroup, session: session, startIdentity: fields[19]}, nil
}

func linuxPIDExists(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := unix.Kill(pid, 0)
	return err == nil || errors.Is(err, unix.EPERM)
}

func linuxGroupExists(pgid int) bool {
	if validateLinuxSignalTarget(pgid) != nil {
		return false
	}
	err := unix.Kill(-pgid, 0)
	return err == nil || errors.Is(err, unix.EPERM)
}

func signalLinuxPID(pid int, signal unix.Signal) error {
	if pid <= 1 {
		return ErrLeaseIdentityMismatch
	}
	err := unix.Kill(pid, signal)
	if errors.Is(err, unix.ESRCH) {
		return nil
	}
	return err
}

func signalLinuxGroup(pgid int, signal unix.Signal) error {
	if err := validateLinuxSignalTarget(pgid); err != nil {
		return err
	}
	err := unix.Kill(-pgid, signal)
	if errors.Is(err, unix.ESRCH) {
		return nil
	}
	return err
}

func validateLinuxSignalTarget(value int) error {
	if value <= 1 {
		return ErrLeaseIdentityMismatch
	}
	return nil
}

func signalOwnedLinuxGroup(pgid, session int, signal unix.Signal) error {
	exists, err := linuxOwnedGroupExists(pgid, session)
	if err != nil || !exists {
		return err
	}
	return signalLinuxGroup(pgid, signal)
}

func linuxOwnedGroupExists(pgid, session int) (bool, error) {
	if err := validateLinuxSignalTarget(pgid); err != nil {
		return false, err
	}
	if session <= 1 {
		return false, ErrLeaseIdentityMismatch
	}
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return false, errProcessHostUnavailable
	}
	found := false
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil || pid <= 0 {
			continue
		}
		contents, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			continue
		}
		stat, err := parseLinuxProcessStat(contents)
		if err != nil {
			continue
		}
		if stat.processGroup != pgid {
			continue
		}
		found = true
		if stat.session != session {
			return false, ErrLeaseIdentityMismatch
		}
	}
	return found, nil
}

func closeFiles(files ...io.Closer) {
	for _, file := range files {
		if file != nil {
			_ = file.Close()
		}
	}
}
