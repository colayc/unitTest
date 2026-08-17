//go:build linux

package probe

import (
	"errors"
	"io"
	"os"
	"os/exec"
	"syscall"

	"golang.org/x/sys/unix"

	"unit-test-ide.local/test-service/internal/linuxprocess"
)

const (
	probeSupervisorArgument = "--probe-supervisor"
	supervisorStatusFD      = 3
)

type linuxSupervisedTarget struct {
	command      *exec.Cmd
	parentThread *linuxprocess.ParentThread
}

func RunSupervisor(control io.Reader, status, stdout, stderr io.Writer) int {
	pid := os.Getpid()
	if pid <= 1 || unix.Getpgrp() != pid {
		return 2
	}
	unix.CloseOnExec(supervisorStatusFD)
	code := runSupervisorProtocol(control, status, stdout, stderr, startLinuxSupervisedTarget)
	if code == 2 {
		return code
	}
	// This process is the still-live process-group leader. Signaling its own
	// group has no numeric identity check-to-signal gap.
	_ = unix.Kill(0, unix.SIGKILL)
	return code
}

func startLinuxSupervisedTarget(spec Spec, stdout, stderr io.Writer) (supervisedTarget, error) {
	spec, err := normalizeSpec(spec)
	if err != nil {
		return nil, err
	}
	command := exec.Command(spec.Executable, spec.Args...)
	command.Env = append([]string{}, spec.Env...)
	command.Dir = spec.Dir
	command.Stdout = stdout
	command.Stderr = stderr
	command.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGKILL}
	parentThread, err := linuxprocess.Start(command)
	if err != nil {
		return nil, err
	}
	if command.Process == nil || command.Process.Pid <= 1 {
		killAndReapSupervisedTarget(command, parentThread)
		return nil, errors.New("supervised target process unavailable")
	}
	group, err := unix.Getpgid(command.Process.Pid)
	if err != nil || group != unix.Getpgrp() {
		killAndReapSupervisedTarget(command, parentThread)
		return nil, errors.New("supervised target did not inherit supervisor group")
	}
	return &linuxSupervisedTarget{command: command, parentThread: parentThread}, nil
}

func killAndReapSupervisedTarget(command *exec.Cmd, parentThread *linuxprocess.ParentThread) {
	if command != nil && command.Process != nil {
		_ = command.Process.Kill()
		_ = command.Wait()
	}
	parentThread.Release()
}

func (target *linuxSupervisedTarget) PID() int {
	return target.command.Process.Pid
}

func (target *linuxSupervisedTarget) Wait() (int, error) {
	err := target.command.Wait()
	target.parentThread.Release()
	if err == nil {
		return 0, nil
	}
	var exitError *exec.ExitError
	if errors.As(err, &exitError) {
		return exitError.ExitCode(), nil
	}
	return -1, err
}

type linuxSupervisorOperations struct {
	pidfdSendSignal func(int, unix.Signal, *unix.Siginfo, int) error
	kill            func(int, unix.Signal) error
}

func defaultLinuxSupervisorOperations() linuxSupervisorOperations {
	return linuxSupervisorOperations{
		pidfdSendSignal: unix.PidfdSendSignal,
		kill:            unix.Kill,
	}
}

func terminateLinuxSupervisorGroup(pidfd, pgid int, operations linuxSupervisorOperations) error {
	if pidfd < 0 || pgid <= 1 || operations.pidfdSendSignal == nil || operations.kill == nil {
		return errors.New("invalid linux supervisor boundary")
	}
	return terminatePinnedSupervisorBoundary(
		func() error {
			return operations.pidfdSendSignal(pidfd, unix.SIGSTOP, nil, 0)
		},
		func() error {
			return operations.kill(-pgid, unix.SIGKILL)
		},
	)
}
