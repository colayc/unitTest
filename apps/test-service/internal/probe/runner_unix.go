//go:build linux

package probe

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.org/x/sys/unix"

	"unit-test-ide.local/test-service/internal/linuxprocess"
)

type unixProcessTree struct {
	command       *exec.Cmd
	parentThread  *linuxprocess.ParentThread
	pgid          int
	session       int
	startIdentity string

	terminateOnce sync.Once
	terminateErr  error
}

func startProcessTree(ctx context.Context, spec Spec, stdout, stderr io.Writer) (processTree, error) {
	session, err := unix.Getsid(0)
	if err != nil || session <= 1 {
		return nil, errors.New("probe process identity unavailable")
	}
	command := exec.CommandContext(ctx, spec.Executable, spec.Args...)
	command.Env = append([]string{}, spec.Env...)
	command.Dir = spec.Dir
	command.Stdout = stdout
	command.Stderr = stderr
	command.WaitDelay = waitDelay
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGKILL}
	parentThread, err := linuxprocess.Start(command)
	if err != nil {
		return nil, err
	}
	if command.Process == nil || command.Process.Pid <= 1 {
		if command.Process != nil {
			_ = command.Process.Kill()
		}
		_ = command.Wait()
		parentThread.Release()
		return nil, errors.New("probe process group unavailable")
	}
	identity, err := probeProcessStartIdentity(command.Process.Pid)
	if err != nil || identity == "" {
		_ = unix.Kill(-command.Process.Pid, unix.SIGKILL)
		_ = command.Process.Kill()
		_ = command.Wait()
		parentThread.Release()
		return nil, errors.New("probe process identity unavailable")
	}
	return &unixProcessTree{
		command:       command,
		parentThread:  parentThread,
		pgid:          command.Process.Pid,
		session:       session,
		startIdentity: identity,
	}, nil
}

func (tree *unixProcessTree) Terminate() error {
	tree.terminateOnce.Do(func() {
		err := signalProbeProcessGroup(tree, unix.SIGKILL)
		if err != nil && tree.command.Process != nil {
			err = errors.Join(err, tree.command.Process.Kill())
		}
		tree.terminateErr = err
	})
	return tree.terminateErr
}

func (tree *unixProcessTree) Wait() (int, error) {
	waitErr := tree.command.Wait()
	tree.parentThread.Release()
	cleanupErr := tree.Terminate()
	if !waitUnixProcessGroupGone(tree.pgid, time.Second) {
		cleanupErr = errors.Join(cleanupErr, errors.New("probe process group remained alive"))
	}
	exitCode := -1
	if tree.command.ProcessState != nil {
		exitCode = tree.command.ProcessState.ExitCode()
	}
	if cleanupErr != nil {
		cleanupErr = fmt.Errorf("terminate probe process group: %w", cleanupErr)
	}
	return exitCode, errors.Join(waitErr, cleanupErr)
}

func signalProbeProcessGroup(tree *unixProcessTree, signal unix.Signal) error {
	if tree == nil || tree.pgid <= 1 || tree.session <= 1 || tree.startIdentity == "" {
		return errors.New("invalid probe process group")
	}
	exists, owned, err := probeProcessGroupOwnedBySession(tree.pgid, tree.session, tree.startIdentity)
	if err != nil {
		return err
	}
	if !exists {
		if probeProcessGroupExists(tree.pgid) {
			return errors.New("probe process group identity mismatch")
		}
		return nil
	}
	if !owned {
		return errors.New("probe process group identity mismatch")
	}
	if err := validateProbeLeaderIdentity(tree.pgid, tree.startIdentity); err != nil {
		return err
	}
	err = unix.Kill(-tree.pgid, signal)
	if errors.Is(err, unix.ESRCH) {
		return nil
	}
	return err
}

func validateProbeLeaderIdentity(pgid int, expected string) error {
	contents, err := os.ReadFile("/proc/" + strconv.Itoa(pgid) + "/stat")
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return errors.New("probe process group identity unavailable")
	}
	_, _, identity, err := parseProbeProcessStat(contents)
	if err != nil {
		return errors.New("probe process group identity unavailable")
	}
	if identity != expected {
		return errors.New("probe process group identity mismatch")
	}
	return nil
}

func probeProcessGroupOwnedBySession(pgid, session int, leaderIdentity string) (bool, bool, error) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return false, false, errors.New("probe process group identity unavailable")
	}
	found := false
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil || pid <= 0 {
			continue
		}
		contents, err := os.ReadFile("/proc/" + entry.Name() + "/stat")
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return false, false, errors.New("probe process group identity unavailable")
		}
		group, gotSession, identity, err := parseProbeProcessStat(contents)
		if err != nil {
			return false, false, errors.New("probe process group identity unavailable")
		}
		if group != pgid {
			continue
		}
		found = true
		if gotSession != session {
			return true, false, nil
		}
		if pid == pgid && identity != leaderIdentity {
			return true, false, nil
		}
	}
	return found, found, nil
}

func probeProcessStartIdentity(pid int) (string, error) {
	contents, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return "", err
	}
	_, _, identity, err := parseProbeProcessStat(contents)
	return identity, err
}

func parseProbeProcessStat(contents []byte) (int, int, string, error) {
	closing := strings.LastIndexByte(string(contents), ')')
	if closing < 0 || closing+1 >= len(contents) {
		return 0, 0, "", errors.New("invalid process identity")
	}
	fields := strings.Fields(string(contents[closing+1:]))
	if len(fields) <= 19 {
		return 0, 0, "", errors.New("invalid process identity")
	}
	group, err := strconv.Atoi(fields[2])
	if err != nil || group < 0 {
		return 0, 0, "", errors.New("invalid process identity")
	}
	session, err := strconv.Atoi(fields[3])
	if err != nil || session < 0 {
		return 0, 0, "", errors.New("invalid process identity")
	}
	if _, err := strconv.ParseUint(fields[19], 10, 64); err != nil {
		return 0, 0, "", errors.New("invalid process identity")
	}
	return group, session, fields[19], nil
}

func probeProcessGroupExists(pgid int) bool {
	if pgid <= 1 {
		return false
	}
	err := unix.Kill(-pgid, 0)
	return err == nil || errors.Is(err, unix.EPERM)
}

func waitUnixProcessGroupGone(pgid int, maximum time.Duration) bool {
	deadline := time.Now().Add(maximum)
	for {
		if !probeProcessGroupExists(pgid) {
			return true
		}
		if !time.Now().Before(deadline) {
			return false
		}
		time.Sleep(10 * time.Millisecond)
	}
}
