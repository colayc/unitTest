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
	"sync/atomic"
	"syscall"
	"time"

	"golang.org/x/sys/unix"

	"unit-test-ide.local/test-service/internal/linuxprocess"
	"unit-test-ide.local/test-service/internal/task"
)

var (
	ErrLeaseIdentityMismatch  = errors.New("process lease identity mismatch")
	ErrProcessOutputOverflow  = errors.New("process output overflow")
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
	operations linuxOperations
}

var _ Runner = (*unixRunner)(nil)

type unixProcess struct {
	host         *exec.Cmd
	parentThread *linuxprocess.ParentThread
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
	outputCloseOnce  sync.Once
	discardOnce      sync.Once
	outputOverflow   atomic.Bool
	operations       linuxOperations
}

var _ Process = (*unixProcess)(nil)

func NewRunner(serviceExecutable string) Runner {
	return &unixRunner{executable: serviceExecutable, operations: defaultLinuxOperations()}
}

type linuxOperations struct {
	startIdentity func(int) (string, error)
	ownedGroup    func(int, int) (bool, error)
	signalPID     func(int, unix.Signal) error
	signalGroup   func(int, unix.Signal) error
	pidExists     func(int) bool
	groupExists   func(int) bool
}

func defaultLinuxOperations() linuxOperations {
	operations := linuxOperations{
		startIdentity: linuxStartIdentity,
		signalPID:     signalLinuxPID,
		signalGroup:   signalLinuxGroup,
		pidExists:     linuxPIDExists,
		groupExists:   linuxGroupExists,
	}
	operations.ownedGroup = linuxOwnedGroupExists
	return operations
}

func (operations linuxOperations) signalHost(pid int, expected string, signal unix.Signal, group bool) error {
	present, err := operations.validateHostForSignal(pid, expected)
	if err != nil || !present {
		return err
	}
	if group {
		return operations.signalGroup(pid, signal)
	}
	return operations.signalPID(pid, signal)
}

func (operations linuxOperations) validateHost(pid int, expected string) error {
	present, err := operations.validateHostForSignal(pid, expected)
	if err != nil {
		return err
	}
	if !present {
		return errProcessHostUnavailable
	}
	return nil
}

func (operations linuxOperations) validateHostForSignal(pid int, expected string) (bool, error) {
	if pid <= 1 || expected == "" || operations.startIdentity == nil {
		return false, ErrLeaseIdentityMismatch
	}
	identity, err := operations.startIdentity(pid)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, errProcessHostUnavailable
	}
	if identity != expected {
		return false, ErrLeaseIdentityMismatch
	}
	return true, nil
}

func (operations linuxOperations) signalTargetGroup(lease task.ProcessLease, hostExists bool, signal unix.Signal) error {
	// Recovery deliberately uses the persisted tuple as one fixed ownership proof:
	// HostPID is also the session ID, HostStartIdentity identifies that Host
	// incarnation, and TargetProcessGroup must still belong to that session.
	exists, err := operations.ownedGroup(lease.TargetProcessGroup, lease.HostPID)
	if err != nil {
		return err
	}
	if !exists {
		if operations.groupExists(lease.TargetProcessGroup) {
			return ErrLeaseIdentityMismatch
		}
		return nil
	}
	if hostExists {
		if _, err := operations.validateHostForSignal(lease.HostPID, lease.HostStartIdentity); err != nil {
			return err
		}
	}
	return operations.signalGroup(lease.TargetProcessGroup, signal)
}

func (runner *unixRunner) Prepare(ctx context.Context, spec Spec, taskID, serviceInstanceID string) (Process, error) {
	operations := runner.operations
	if operations.startIdentity == nil {
		operations = defaultLinuxOperations()
	}
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
	identityReady := make(chan struct{})
	var hostIdentity string
	host.Cancel = func() error {
		<-identityReady
		if host.Process == nil {
			return os.ErrProcessDone
		}
		if hostIdentity == "" {
			return os.ErrProcessDone
		}
		err := operations.signalHost(host.Process.Pid, hostIdentity, unix.SIGTERM, false)
		if errors.Is(err, os.ErrProcessDone) || errors.Is(err, unix.ESRCH) {
			return nil
		}
		if err == nil {
			pid, identity := host.Process.Pid, hostIdentity
			go func() {
				timer := time.NewTimer(3 * time.Second)
				defer timer.Stop()
				<-timer.C
				_ = operations.signalHost(pid, identity, unix.SIGKILL, true)
			}()
		}
		return err
	}
	parentThread, err := linuxprocess.Start(host)
	if err != nil {
		close(identityReady)
		closeFiles(controlReader, controlWriter, statusReader, statusWriter, stdoutReader, stdoutWriter, stderrReader, stderrWriter)
		return nil, errProcessHostUnavailable
	}
	closeFiles(controlReader, statusWriter, stdoutWriter, stderrWriter)

	identity, err := operations.startIdentity(host.Process.Pid)
	if err != nil || identity == "" {
		finishPrepareIdentityFailure(controlWriter, identityReady, host.Wait, parentThread.Release, statusReader, stdoutReader, stderrReader)
		return nil, errProcessHostUnavailable
	}
	hostIdentity = identity
	close(identityReady)

	process := &unixProcess{
		host:         host,
		parentThread: parentThread,
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
		operations:    operations,
	}
	go process.waitHost()
	go process.copyOutput()
	return process, nil
}

func (runner *unixRunner) Cleanup(ctx context.Context, lease task.ProcessLease, grace time.Duration) error {
	operations := runner.operations
	if operations.startIdentity == nil {
		operations = defaultLinuxOperations()
	}
	if lease.HostPID <= 1 || lease.HostStartIdentity == "" {
		return ErrLeaseIdentityMismatch
	}
	groups, err := linuxLeaseTargetGroups(lease)
	if err != nil {
		return err
	}
	identity, err := operations.startIdentity(lease.HostPID)
	hostExists := err == nil
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return errProcessHostUnavailable
	}
	if hostExists && identity != lease.HostStartIdentity {
		return ErrLeaseIdentityMismatch
	}
	if !hostExists && len(groups) == 0 {
		return nil
	}
	for _, group := range groups {
		exists, err := operations.ownedGroup(
			group,
			lease.HostPID,
		)
		if err != nil {
			return err
		}
		if !exists && operations.groupExists(group) {
			return ErrLeaseIdentityMismatch
		}
		if hostExists {
			present, err := operations.validateHostForSignal(lease.HostPID, lease.HostStartIdentity)
			if err != nil {
				return err
			}
			hostExists = present
		}
	}
	if grace < 0 {
		grace = 0
	}

	var cleanupErr error
	for _, group := range groups {
		groupLease := lease
		groupLease.TargetProcessGroup = group
		if err := operations.signalTargetGroup(
			groupLease,
			hostExists,
			unix.SIGTERM,
		); err != nil {
			return redactLinuxOperationError(err)
		}
	}
	if hostExists {
		if err := operations.signalHost(lease.HostPID, lease.HostStartIdentity, unix.SIGTERM, false); err != nil {
			cleanupErr = errors.Join(cleanupErr, redactLinuxOperationError(err))
		}
	}
	if waitLeaseGone(ctx, lease, grace, operations) {
		return cleanupErr
	}

	identity, err = operations.startIdentity(lease.HostPID)
	hostExists = err == nil
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return errors.Join(cleanupErr, errProcessHostUnavailable)
	}
	if hostExists && identity != lease.HostStartIdentity {
		return errors.Join(cleanupErr, ErrLeaseIdentityMismatch)
	}
	for _, group := range groups {
		groupLease := lease
		groupLease.TargetProcessGroup = group
		if err := operations.signalTargetGroup(
			groupLease,
			hostExists,
			unix.SIGKILL,
		); err != nil {
			return errors.Join(cleanupErr, redactLinuxOperationError(err))
		}
	}
	if hostExists {
		if err := operations.signalHost(lease.HostPID, lease.HostStartIdentity, unix.SIGKILL, false); err != nil {
			cleanupErr = errors.Join(cleanupErr, redactLinuxOperationError(err))
		}
	}
	if !waitLeaseGone(ctx, lease, hostShutdownWait, operations) {
		cleanupErr = errors.Join(cleanupErr, errProcessHostFailed)
	}
	return cleanupErr
}

func redactLinuxOperationError(err error) error {
	if errors.Is(err, ErrLeaseIdentityMismatch) || errors.Is(err, errProcessHostUnavailable) {
		return err
	}
	return errProcessHostFailed
}

func linuxLeaseTargetGroups(
	lease task.ProcessLease,
) ([]int, error) {
	values := make(
		[]int,
		0,
		1+len(lease.TargetProcessGroups),
	)
	if lease.TargetProcessGroup != 0 {
		values = append(values, lease.TargetProcessGroup)
	}
	values = append(values, lease.TargetProcessGroups...)
	seen := make(map[int]struct{}, len(values))
	result := make([]int, 0, len(values))
	for _, group := range values {
		if err := validateLinuxSignalTarget(group); err != nil {
			return nil, err
		}
		if _, duplicate := seen[group]; duplicate {
			continue
		}
		seen[group] = struct{}{}
		result = append(result, group)
	}
	return result, nil
}

func (process *unixProcess) Lease() task.ProcessLease {
	process.mu.Lock()
	defer process.mu.Unlock()
	result := process.lease
	result.TargetProcessGroups = append(
		[]int(nil),
		process.lease.TargetProcessGroups...,
	)
	return result
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
	if len(process.specValue.Batch) == 0 &&
		validateLinuxStartedStatus(status) != nil ||
		len(process.specValue.Batch) != 0 &&
			validateBatchStartedStatus(
				status,
				process.specValue.Batch,
			) != nil {
		process.closeControl()
		process.finishAfterHost(Result{Err: errProcessHostFailed})
		return errProcessHostFailed
	}
	process.mu.Lock()
	process.started = true
	process.lease.TargetProcessGroup = status.ProcessGroup
	process.lease.TargetProcessGroups = append(
		[]int(nil),
		status.TargetProcessGroups...,
	)
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

func (process *unixProcess) Close(ctx context.Context) error {
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
	select {
	case <-process.hostExited:
	case <-ctx.Done():
		process.closeStatus()
		return ctx.Err()
	}
	select {
	case <-process.outputDone:
	case <-ctx.Done():
		process.closeStatus()
		return ctx.Err()
	}
	select {
	case <-process.finished:
	case <-ctx.Done():
		process.closeStatus()
		return ctx.Err()
	}
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
	result := Result{}
	for {
		status, err := process.readStatus(context.Background())
		if err != nil {
			result.Err = errProcessHostFailed
			break
		}
		if status.Kind == "output" {
			if !validBatchOutputStatus(
				status,
				len(process.specValue.Batch) != 0,
			) {
				result.Err = errProcessHostFailed
				break
			}
			process.sendOutput(Output{
				Source: status.Source,
				Stream: status.Stream,
				Data: append(
					[]byte(nil),
					status.Data...,
				),
			})
			continue
		}
		if status.Kind != "exit" {
			result.Err = errProcessHostFailed
			break
		}
		children, valid := hostChildResults(
			status.Children,
			process.specValue.Batch,
		)
		if !valid ||
			len(process.specValue.Batch) == 0 &&
				len(children) != 0 {
			result.Err = errProcessHostFailed
			break
		}
		result.ExitCode = status.ExitCode
		result.Children = children
		if status.ErrorCode != "" {
			result.Err = errProcessHostFailed
		}
		break
	}
	process.finishAfterHost(result)
}

func (process *unixProcess) finishAfterHost(result Result) {
	<-process.hostExited
	<-process.outputDone
	process.closeOutput()
	process.publish(process.applyOutputOverflow(result))
}

func (process *unixProcess) applyOutputOverflow(result Result) Result {
	if result.Err == nil && process.outputOverflow.Load() {
		result.Err = ErrProcessOutputOverflow
	}
	return result
}

func (process *unixProcess) publish(result Result) {
	process.doneOnce.Do(func() {
		// Make completion visible to Terminate before the public Done result can
		// be observed, so Terminate-after-Done is always idempotent.
		close(process.finished)
		process.done <- result
		close(process.done)
	})
}

func (process *unixProcess) waitHost() {
	_ = process.host.Wait()
	process.parentThread.Release()
	close(process.hostExited)
}

func (process *unixProcess) copyOutput() {
	var readers sync.WaitGroup
	readers.Add(2)
	go process.copyStream(&readers, process.stdout, StreamStdout)
	go process.copyStream(&readers, process.stderr, StreamStderr)
	readers.Wait()
	close(process.outputDone)
}

func (process *unixProcess) closeOutput() {
	process.outputCloseOnce.Do(func() {
		close(process.output)
	})
}

func (process *unixProcess) copyStream(readers *sync.WaitGroup, reader *os.File, stream Stream) {
	defer readers.Done()
	defer reader.Close()
	buffer := make([]byte, 32*1024)
	for {
		count, err := reader.Read(buffer)
		if count > 0 {
			data := append([]byte(nil), buffer[:count]...)
			process.sendOutput(Output{
				Stream: stream,
				Data:   data,
			})
		}
		if err != nil {
			return
		}
	}
}

func (process *unixProcess) sendOutput(value Output) {
	select {
	case process.output <- value:
	case <-process.outputDiscard:
	default:
		process.outputOverflow.Store(true)
	}
}

func validateLinuxStartedStatus(status HostStatus) error {
	if status.Kind != "started" || status.PID <= 1 ||
		status.ProcessGroup <= 1 ||
		status.PID != status.ProcessGroup ||
		len(status.TargetProcessGroups) != 0 {
		return errProcessHostFailed
	}
	return nil
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
	if err := process.operations.validateHost(lease.HostPID, lease.HostStartIdentity); err == nil {
		groups, groupErr := linuxLeaseTargetGroups(lease)
		if groupErr != nil {
			prior = errors.Join(
				prior,
				redactLinuxOperationError(groupErr),
			)
		}
		for _, group := range groups {
			groupLease := lease
			groupLease.TargetProcessGroup = group
			if err := process.operations.signalTargetGroup(
				groupLease,
				true,
				unix.SIGKILL,
			); err != nil {
				prior = errors.Join(prior, redactLinuxOperationError(err))
			}
		}
		if !waitClosed(ctx, process.hostExited, 100*time.Millisecond) {
			if err := process.operations.signalHost(lease.HostPID, lease.HostStartIdentity, unix.SIGTERM, true); err != nil {
				prior = errors.Join(prior, redactLinuxOperationError(err))
			}
		}
		if !waitClosed(ctx, process.hostExited, wait) {
			if err := process.operations.signalHost(lease.HostPID, lease.HostStartIdentity, unix.SIGKILL, true); err != nil {
				prior = errors.Join(prior, redactLinuxOperationError(err))
			}
		}
	} else {
		prior = errors.Join(prior, redactLinuxOperationError(err))
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

func waitLeaseGone(ctx context.Context, lease task.ProcessLease, duration time.Duration, operations linuxOperations) bool {
	groups, err := linuxLeaseTargetGroups(lease)
	if err != nil {
		return false
	}
	deadline := time.Now().Add(duration)
	for {
		hostGone := !operations.pidExists(lease.HostPID)
		groupsGone := true
		for _, group := range groups {
			if operations.groupExists(group) {
				groupsGone = false
				break
			}
		}
		if hostGone && groupsGone {
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
	if err != nil || processGroup < 0 {
		return linuxProcessStat{}, errors.New("invalid process identity")
	}
	session, err := strconv.Atoi(fields[3])
	if err != nil || session < 0 {
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
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil || pid <= 0 {
			continue
		}
		names = append(names, entry.Name())
	}
	return scanLinuxOwnedGroup(names, os.ReadFile, pgid, session)
}

func scanLinuxOwnedGroup(names []string, readFile func(string) ([]byte, error), pgid, session int) (bool, error) {
	// Linux retains the session's PID identity while any session member exists,
	// so the numeric session ID cannot be reused underneath an extant member.
	found := false
	for _, name := range names {
		contents, err := readFile("/proc/" + name + "/stat")
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return false, errProcessHostUnavailable
		}
		stat, err := parseLinuxProcessStat(contents)
		if err != nil {
			return false, errProcessHostUnavailable
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

func finishPrepareIdentityFailure(control io.Closer, identityReady chan struct{}, wait func() error, release func(), remaining ...io.Closer) {
	closeFiles(control)
	close(identityReady)
	_ = wait()
	release()
	closeFiles(remaining...)
}
