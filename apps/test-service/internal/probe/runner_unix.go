//go:build linux

package probe

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"syscall"

	"golang.org/x/sys/unix"

	"unit-test-ide.local/test-service/internal/linuxprocess"
)

type unixProcessTree struct {
	command       *exec.Cmd
	parentThread  *linuxprocess.ParentThread
	controlWriter *os.File
	statusReader  *os.File
	pidfd         int
	pgid          int
	operations    linuxSupervisorOperations

	closeControlOnce sync.Once
	closeStatusOnce  sync.Once
	closePidfdOnce   sync.Once
	terminateOnce    sync.Once
	terminateErr     error
	waitOnce         sync.Once
	waitDone         chan struct{}
	waitErr          error
}

func startProcessTree(ctx context.Context, spec Spec, stdout, stderr io.Writer) (processTree, error) {
	supervisorExecutable, err := os.Executable()
	if err != nil {
		return nil, errors.New("probe supervisor executable unavailable")
	}
	controlReader, controlWriter, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	statusReader, statusWriter, err := os.Pipe()
	if err != nil {
		closeProbeUnixFiles(controlReader, controlWriter)
		return nil, err
	}

	command := exec.Command(supervisorExecutable, probeSupervisorArgument)
	command.Env = []string{}
	command.Stdin = controlReader
	command.Stdout = stdout
	command.Stderr = stderr
	command.ExtraFiles = []*os.File{statusWriter}
	command.WaitDelay = waitDelay
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGKILL}
	parentThread, err := linuxprocess.Start(command)
	if err != nil {
		closeProbeUnixFiles(controlReader, controlWriter, statusReader, statusWriter)
		return nil, err
	}
	closeProbeUnixFiles(controlReader, statusWriter)
	if command.Process == nil || command.Process.Pid <= 1 {
		closeProbeUnixFiles(controlWriter, statusReader)
		killAndReapProbeSupervisor(command, parentThread)
		return nil, errors.New("probe supervisor process unavailable")
	}
	pgid := command.Process.Pid
	actualGroup, err := unix.Getpgid(pgid)
	if err != nil || actualGroup != pgid {
		closeProbeUnixFiles(controlWriter, statusReader)
		killAndReapProbeSupervisor(command, parentThread)
		return nil, errors.New("probe supervisor did not become process-group leader")
	}
	pidfd, err := unix.PidfdOpen(pgid, 0)
	if err != nil {
		closeProbeUnixFiles(controlWriter, statusReader)
		killAndReapProbeSupervisor(command, parentThread)
		return nil, fmt.Errorf("open probe supervisor pidfd: %w", err)
	}
	unix.CloseOnExec(pidfd)
	tree := &unixProcessTree{
		command:       command,
		parentThread:  parentThread,
		controlWriter: controlWriter,
		statusReader:  statusReader,
		pidfd:         pidfd,
		pgid:          pgid,
		operations:    defaultLinuxSupervisorOperations(),
		waitDone:      make(chan struct{}),
	}
	if err := writeSupervisorFrame(controlWriter, supervisorRequest{
		Version: supervisorProtocolVersion,
		Spec:    spec,
	}); err != nil {
		cleanupErr := tree.stopAndReap()
		return nil, errors.Join(fmt.Errorf("write probe supervisor request: %w", err), cleanupErr)
	}
	type statusResult struct {
		status supervisorStatus
		err    error
	}
	started := make(chan statusResult, 1)
	go func() {
		var status supervisorStatus
		err := readSupervisorFrame(statusReader, &status)
		started <- statusResult{status: status, err: err}
	}()
	select {
	case <-ctx.Done():
		cleanupErr := tree.stopAndReap()
		return nil, errors.Join(ctx.Err(), cleanupErr)
	case result := <-started:
		if result.err != nil {
			cleanupErr := tree.stopAndReap()
			return nil, errors.Join(fmt.Errorf("read probe supervisor start status: %w", result.err), cleanupErr)
		}
		if result.status.Version != supervisorProtocolVersion ||
			result.status.Kind != supervisorStatusStarted ||
			result.status.PID <= 1 {
			cleanupErr := tree.stopAndReap()
			return nil, errors.Join(errors.New("probe supervisor rejected target start"), cleanupErr)
		}
	}
	return tree, nil
}

func killAndReapProbeSupervisor(command *exec.Cmd, parentThread *linuxprocess.ParentThread) {
	if command != nil && command.Process != nil {
		_ = command.Process.Kill()
		_ = command.Wait()
	}
	parentThread.Release()
}

func (tree *unixProcessTree) Terminate() error {
	tree.terminateOnce.Do(func() {
		tree.terminateErr = terminateLinuxSupervisorGroup(
			tree.pidfd,
			tree.pgid,
			tree.operations,
		)
		if tree.terminateErr != nil {
			// If the pidfd pin or the pinned group signal fails, never fall
			// back to a parent-issued numeric group signal. Wake a possibly
			// stopped supervisor, then release its control pipe so the live
			// group leader safely kills its own group.
			_ = tree.operations.pidfdSendSignal(tree.pidfd, unix.SIGCONT, nil, 0)
		}
		tree.closeControl()
	})
	return tree.terminateErr
}

func (tree *unixProcessTree) Wait() (int, error) {
	var targetStatus supervisorStatus
	statusErr := readSupervisorFrame(tree.statusReader, &targetStatus)
	cleanupErr := tree.Terminate()
	supervisorWaitErr := tree.reapSupervisor()

	exitCode := -1
	var targetErr error
	if statusErr != nil {
		targetErr = fmt.Errorf("read probe supervisor exit status: %w", statusErr)
	} else if targetStatus.Version != supervisorProtocolVersion ||
		targetStatus.Kind != supervisorStatusExited {
		targetErr = errors.New("probe supervisor returned invalid exit status")
	} else {
		exitCode = targetStatus.ExitCode
		switch {
		case targetStatus.ErrorCode != "":
			targetErr = errors.New("probe supervisor could not wait for target")
		case exitCode != 0:
			targetErr = fmt.Errorf("probe target exited with %d", exitCode)
		}
	}
	if cleanupErr == nil {
		// The supervisor is intentionally killed with its group after the
		// target status is captured.
		supervisorWaitErr = nil
	}
	if cleanupErr != nil {
		cleanupErr = fmt.Errorf("terminate pinned probe supervisor group: %w", cleanupErr)
	}
	return exitCode, errors.Join(targetErr, cleanupErr, supervisorWaitErr)
}

func (tree *unixProcessTree) stopAndReap() error {
	return errors.Join(tree.Terminate(), tree.reapSupervisor())
}

func (tree *unixProcessTree) reapSupervisor() error {
	tree.waitOnce.Do(func() {
		tree.waitErr = tree.command.Wait()
		tree.parentThread.Release()
		tree.closeControl()
		tree.closeStatus()
		tree.closePidfd()
		close(tree.waitDone)
	})
	<-tree.waitDone
	return tree.waitErr
}

func (tree *unixProcessTree) closeControl() {
	tree.closeControlOnce.Do(func() {
		_ = tree.controlWriter.Close()
	})
}

func (tree *unixProcessTree) closeStatus() {
	tree.closeStatusOnce.Do(func() {
		_ = tree.statusReader.Close()
	})
}

func (tree *unixProcessTree) closePidfd() {
	tree.closePidfdOnce.Do(func() {
		_ = unix.Close(tree.pidfd)
	})
}

func closeProbeUnixFiles(files ...*os.File) {
	for _, file := range files {
		if file != nil {
			_ = file.Close()
		}
	}
}
