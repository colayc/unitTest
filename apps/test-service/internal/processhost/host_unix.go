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

	"unit-test-ide.local/test-service/internal/linuxprocess"
	"unit-test-ide.local/test-service/internal/processcontrol"
)

const postKillWait = time.Second

type unixPlatform struct {
	startIdentity func(int) (string, error)
	sessionID     func() (int, error)
}

var _ Platform = (*unixPlatform)(nil)

type unixTarget struct {
	cmd           *exec.Cmd
	parentThread  *linuxprocess.ParentThread
	pgid          int
	session       int
	startIdentity string

	waitOnce sync.Once
	waitDone chan struct{}
	waitCode int
	waitErr  error
}

var _ Target = (*unixTarget)(nil)

func NewPlatform() Platform { return newUnixPlatform(processStartIdentity) }

func newUnixPlatform(startIdentity func(int) (string, error)) *unixPlatform {
	return &unixPlatform{
		startIdentity: startIdentity,
		sessionID: func() (int, error) {
			return unix.Getsid(0)
		},
	}
}

func (platform *unixPlatform) Start(spec processcontrol.Spec, stdout, stderr io.Writer) (Target, error) {
	session, err := platform.sessionID()
	if err != nil || session <= 1 {
		return nil, errors.New("target identity unavailable")
	}
	cmd := exec.Command(spec.Executable, spec.Args...)
	cmd.Dir = spec.Dir
	cmd.Env = targetEnvironment(spec.Env, spec.EnvUnset)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGKILL}
	parentThread, err := linuxprocess.Start(cmd)
	if err != nil {
		return nil, err
	}
	identity, err := platform.startIdentity(cmd.Process.Pid)
	if err != nil || identity == "" {
		killAndReapFailedTarget(cmd)
		parentThread.Release()
		return nil, errors.New("target identity unavailable")
	}
	return &unixTarget{
		cmd: cmd, parentThread: parentThread, pgid: cmd.Process.Pid,
		session: session, startIdentity: identity, waitDone: make(chan struct{}),
	}, nil
}

func killAndReapFailedTarget(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	if cmd.Process.Pid > 1 {
		_ = unix.Kill(-cmd.Process.Pid, unix.SIGKILL)
	}
	_ = cmd.Process.Kill()
	_ = cmd.Wait()
}

func targetEnvironment(
	extra []string,
	unsetValues ...[]string,
) []string {
	var unset []string
	if len(unsetValues) != 0 {
		unset = unsetValues[0]
	}
	removed := make(map[string]struct{}, len(unset))
	for _, key := range unset {
		removed[key] = struct{}{}
	}
	overridden := make(map[string]struct{}, len(extra))
	for _, entry := range extra {
		key, _, found := strings.Cut(entry, "=")
		if found && !serviceOwnedTargetEnvironmentKey(key) {
			overridden[key] = struct{}{}
		}
	}
	inherited := os.Environ()
	result := make([]string, 0, len(inherited)+len(extra))
	for _, entry := range inherited {
		key, _, found := strings.Cut(entry, "=")
		if !found ||
			serviceOwnedTargetEnvironmentKey(key) {
			continue
		}
		if _, exists := removed[key]; exists {
			continue
		}
		if _, exists := overridden[key]; exists {
			continue
		}
		result = append(result, entry)
	}
	for _, entry := range extra {
		key, _, found := strings.Cut(entry, "=")
		if !found || serviceOwnedTargetEnvironmentKey(key) {
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
		defer target.parentThread.Release()
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
	if err != nil {
		return err
	}
	if !exists {
		if processGroupExists(target.pgid) {
			return errors.New("process group identity mismatch")
		}
		return nil
	}
	if !owned {
		return errors.New("process group identity mismatch")
	}
	if err := validateTargetLeaderIdentity(target.pgid, target.startIdentity, os.ReadFile); err != nil {
		return err
	}
	err = unix.Kill(-target.pgid, signal)
	if errors.Is(err, unix.ESRCH) {
		return nil
	}
	return err
}

func validateTargetLeaderIdentity(pgid int, expected string, readFile func(string) ([]byte, error)) error {
	if pgid <= 1 || expected == "" {
		return errors.New("process group identity mismatch")
	}
	contents, err := readFile("/proc/" + strconv.Itoa(pgid) + "/stat")
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return errors.New("process group identity unavailable")
	}
	_, _, identity, err := parseProcessStat(contents)
	if err != nil {
		return errors.New("process group identity unavailable")
	}
	if identity != expected {
		return errors.New("process group identity mismatch")
	}
	return nil
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
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil || pid <= 0 {
			continue
		}
		names = append(names, entry.Name())
	}
	return scanProcessGroupOwned(names, os.ReadFile, pgid, session, leaderIdentity)
}

func scanProcessGroupOwned(names []string, readFile func(string) ([]byte, error), pgid, session int, leaderIdentity string) (bool, bool, error) {
	// Linux retains the session's PID identity while any session member exists,
	// so the numeric session ID cannot be reused underneath an extant member.
	found := false
	for _, name := range names {
		pid, err := strconv.Atoi(name)
		if err != nil || pid <= 0 {
			return false, false, errors.New("process group identity unavailable")
		}
		contents, err := readFile("/proc/" + name + "/stat")
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return false, false, errors.New("process group identity unavailable")
		}
		group, gotSession, identity, err := parseProcessStat(contents)
		if err != nil {
			return false, false, errors.New("process group identity unavailable")
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
