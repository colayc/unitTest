//go:build linux

package processhost

import (
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

	"unit-test-ide.local/test-service/internal/processcontrol"
)

const postKillWait = time.Second

type unixPlatform struct{}

var _ Platform = (*unixPlatform)(nil)

type unixTarget struct {
	cmd           *exec.Cmd
	pgid          int
	session       int
	startIdentity string

	waitOnce sync.Once
	waitDone chan struct{}
	waitCode int
	waitErr  error
}

var _ Target = (*unixTarget)(nil)

func NewPlatform() Platform { return &unixPlatform{} }

func (*unixPlatform) Start(spec processcontrol.Spec, stdout, stderr io.Writer) (Target, error) {
	session, err := unix.Getsid(0)
	if err != nil || session <= 1 {
		return nil, errors.New("target identity unavailable")
	}
	cmd := exec.Command(spec.Executable, spec.Args...)
	cmd.Dir = spec.Dir
	cmd.Env = targetEnvironment(spec.Env)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGKILL}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	identity, _ := processStartIdentity(cmd.Process.Pid)
	return &unixTarget{cmd: cmd, pgid: cmd.Process.Pid, session: session, startIdentity: identity, waitDone: make(chan struct{})}, nil
}

func targetEnvironment(extra []string) []string {
	combined := append(os.Environ(), extra...)
	result := make([]string, 0, len(combined))
	for _, entry := range combined {
		if strings.HasPrefix(entry, "UNIT_TEST_IDE_STATUS_HANDLE=") {
			continue
		}
		result = append(result, entry)
	}
	return result
}

func (target *unixTarget) PID() int          { return target.cmd.Process.Pid }
func (target *unixTarget) ProcessGroup() int { return target.pgid }

func (target *unixTarget) Wait() (int, error) {
	target.waitOnce.Do(func() {
		defer close(target.waitDone)
		err := target.cmd.Wait()
		if err == nil {
			target.waitCode = 0
			return
		}
		var exitError *exec.ExitError
		if errors.As(err, &exitError) {
			target.waitCode = exitError.ExitCode()
			return
		}
		target.waitCode = -1
		target.waitErr = err
	})
	<-target.waitDone
	return target.waitCode, target.waitErr
}

func (*unixPlatform) Terminate(value Target, grace time.Duration) error {
	target, ok := value.(*unixTarget)
	if !ok || target == nil || target.pgid <= 1 {
		return errors.New("invalid linux process target")
	}
	if grace < 0 {
		grace = 0
	}

	var cleanupErr error
	if err := signalProcessGroup(target, unix.SIGTERM); err != nil {
		cleanupErr = errors.New("target process group termination failed")
	}
	if !waitProcessGroupGone(target.pgid, grace) {
		if err := signalProcessGroup(target, unix.SIGKILL); err != nil {
			cleanupErr = errors.Join(cleanupErr, errors.New("target process group kill failed"))
			// Releasing Wait is unconditional, even if the group signal failed.
			_ = target.cmd.Process.Kill()
		}
	}
	_, waitErr := target.Wait()
	if !waitProcessGroupGone(target.pgid, postKillWait) {
		cleanupErr = errors.Join(cleanupErr, errors.New("target process group remained alive"))
	}
	return errors.Join(cleanupErr, waitErr)
}

func signalProcessGroup(target *unixTarget, signal unix.Signal) error {
	if target == nil || target.pgid <= 1 || target.session <= 1 {
		return errors.New("invalid process group")
	}
	exists, owned, err := processGroupOwnedBySession(target.pgid, target.session, target.startIdentity)
	if err != nil || !exists {
		return err
	}
	if !owned {
		return errors.New("process group identity mismatch")
	}
	err = unix.Kill(-target.pgid, signal)
	if errors.Is(err, unix.ESRCH) {
		return nil
	}
	return err
}

func processGroupExists(pgid int) bool {
	if pgid <= 1 {
		return false
	}
	err := unix.Kill(-pgid, 0)
	return err == nil || errors.Is(err, unix.EPERM)
}

func processGroupOwnedBySession(pgid, session int, leaderIdentity string) (bool, bool, error) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return false, false, errors.New("process group identity unavailable")
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
			continue
		}
		group, gotSession, identity, err := parseProcessStat(contents)
		if err != nil {
			continue
		}
		if group != pgid {
			continue
		}
		found = true
		if gotSession != session {
			return true, false, nil
		}
		if pid == pgid && leaderIdentity != "" && identity != leaderIdentity {
			return true, false, nil
		}
	}
	return found, found, nil
}

func processStartIdentity(pid int) (string, error) {
	contents, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return "", err
	}
	_, _, identity, err := parseProcessStat(contents)
	return identity, err
}

func parseProcessStat(contents []byte) (int, int, string, error) {
	closing := strings.LastIndexByte(string(contents), ')')
	if closing < 0 || closing+1 >= len(contents) {
		return 0, 0, "", errors.New("invalid process identity")
	}
	fields := strings.Fields(string(contents[closing+1:]))
	if len(fields) <= 19 {
		return 0, 0, "", errors.New("invalid process identity")
	}
	group, err := strconv.Atoi(fields[2])
	if err != nil || group <= 0 {
		return 0, 0, "", errors.New("invalid process identity")
	}
	session, err := strconv.Atoi(fields[3])
	if err != nil || session <= 0 {
		return 0, 0, "", errors.New("invalid process identity")
	}
	if _, err := strconv.ParseUint(fields[19], 10, 64); err != nil {
		return 0, 0, "", errors.New("invalid process identity")
	}
	return group, session, fields[19], nil
}

func waitProcessGroupGone(pgid int, duration time.Duration) bool {
	if !processGroupExists(pgid) {
		return true
	}
	deadline := time.Now().Add(duration)
	for time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
		if !processGroupExists(pgid) {
			return true
		}
	}
	return !processGroupExists(pgid)
}
