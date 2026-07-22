//go:build linux

package processcontrol

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"unit-test-ide.local/test-service/internal/task"
)

const helperModeEnvironment = "UNIT_TEST_PROCESSCONTROL_HELPER"

func TestLinuxHelperProcess(t *testing.T) {
	mode := os.Getenv(helperModeEnvironment)
	if mode == "" {
		return
	}
	switch mode {
	case "child":
		waitForHelperSignal(false)
	case "ignore-term-child":
		waitForHelperSignal(true)
	case "exit-with-child", "ignore-term":
		childMode := "child"
		if mode == "ignore-term" {
			signal.Ignore(unix.SIGTERM)
			childMode = "ignore-term-child"
		}
		child := exec.Command(os.Args[0], "-test.run=^TestLinuxHelperProcess$")
		child.Env = append(os.Environ(), helperModeEnvironment+"="+childMode)
		child.Stdout, child.Stderr = os.Stdout, os.Stderr
		if err := child.Start(); err != nil {
			t.Fatal(err)
		}
		fmt.Printf("CHILD_PID=%d\n", child.Process.Pid)
		if mode == "exit-with-child" {
			return
		}
		waitForHelperSignal(true)
	case "noisy":
		chunk := strings.Repeat("x", 4096)
		for index := 0; index < 1024; index++ {
			fmt.Fprint(os.Stdout, chunk)
		}
	case "report-inheritance":
		_, present := os.LookupEnv("UNIT_TEST_IDE_STATUS_HANDLE")
		fmt.Printf("STATUS_ENV_PRESENT=%t\n", present)
		fmt.Printf("CONTROL_PIPE_INHERITED=%t\n", descriptorIsFIFO(0))
		fmt.Printf("STDOUT_PIPE_INHERITED=%t\n", descriptorIsFIFO(1))
		fmt.Printf("STDERR_PIPE_INHERITED=%t\n", descriptorIsFIFO(2))
		fmt.Printf("STATUS_PIPE_INHERITED=%t\n", descriptorIsFIFO(3))
	default:
		t.Fatalf("unknown helper mode %q", mode)
	}
}

func waitForHelperSignal(ignoreTERM bool) {
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, unix.SIGTERM, unix.SIGINT)
	defer signal.Stop(signals)
	if !ignoreTERM {
		<-signals
		return
	}
	for range signals {
	}
}

func descriptorIsFIFO(fd int) bool {
	var stat unix.Stat_t
	return unix.Fstat(fd, &stat) == nil && stat.Mode&unix.S_IFMT == unix.S_IFIFO
}

func TestLinuxPrepareBlocksTargetUntilStart(t *testing.T) {
	binary := buildService(t)
	process, err := NewRunner(binary).Prepare(context.Background(), Spec{
		Executable: binary,
		Args:       []string{"--task-fixture", "spawn-child"},
	}, testID(1), testID(2))
	if err != nil {
		t.Fatal(err)
	}
	defer process.Close()
	lease := process.Lease()
	if lease.HostPID <= 0 || lease.HostStartIdentity == "" || lease.TargetProcessGroup != 0 {
		t.Fatalf("prepared lease = %#v", lease)
	}
	select {
	case output := <-process.Output():
		t.Fatalf("target emitted before Start: %#v", output)
	case result := <-process.Done():
		t.Fatalf("process completed before Start: %#v", result)
	case <-time.After(100 * time.Millisecond):
	}
	if err := process.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	childPID := readChildPID(t, process.Output())
	if err := process.Terminate(context.Background(), 250*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	result := receiveResult(t, process.Done())
	if result.Err != nil {
		t.Fatal(result.Err)
	}
	assertProcessGone(t, childPID)
}

func TestLinuxRunnerUsesSeparateHostSessionAndTargetProcessGroup(t *testing.T) {
	binary := buildService(t)
	process, err := NewRunner(binary).Prepare(context.Background(), Spec{
		Executable: binary,
		Args:       []string{"--task-fixture", "hang"},
	}, testID(3), testID(4))
	if err != nil {
		t.Fatal(err)
	}
	defer process.Close()
	if err := process.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	lease := process.Lease()
	if lease.TargetProcessGroup <= 0 || lease.TargetProcessGroup == lease.HostPID {
		t.Fatalf("started lease = %#v", lease)
	}
	if sid, err := unix.Getsid(lease.HostPID); err != nil || sid != lease.HostPID {
		t.Fatalf("host sid = %d, err = %v, want %d", sid, err, lease.HostPID)
	}
	if group, err := unix.Getpgid(lease.HostPID); err != nil || group != lease.HostPID {
		t.Fatalf("host pgid = %d, err = %v, want %d", group, err, lease.HostPID)
	}
	if group, err := unix.Getpgid(lease.TargetProcessGroup); err != nil || group != lease.TargetProcessGroup {
		t.Fatalf("target pgid = %d, err = %v, want %d", group, err, lease.TargetProcessGroup)
	}
	if err := process.Terminate(context.Background(), 100*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if result := receiveResult(t, process.Done()); result.Err != nil {
		t.Fatal(result.Err)
	}
}

func TestLinuxRunnerPreservesStdoutAndStderr(t *testing.T) {
	binary := buildService(t)
	process, err := NewRunner(binary).Prepare(context.Background(), Spec{
		Executable: binary,
		Args:       []string{"--task-fixture", "emit-output"},
	}, testID(5), testID(6))
	if err != nil {
		t.Fatal(err)
	}
	defer process.Close()
	if err := process.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	streams := collectOutput(t, process.Output())
	if streams[StreamStdout] != "fixture stdout\n" || streams[StreamStderr] != "fixture stderr\n" {
		t.Fatalf("streams = %#v", streams)
	}
	if result := receiveResult(t, process.Done()); result.ExitCode != 0 || result.Err != nil {
		t.Fatalf("result = %#v", result)
	}
}

func TestLinuxCleanupRejectsMismatchedIdentityWithoutSignalling(t *testing.T) {
	runner := NewRunner(os.Args[0])
	lease := task.ProcessLease{HostPID: os.Getpid(), HostStartIdentity: "stale-or-reused", TargetProcessGroup: unix.Getpgrp()}
	err := runner.Cleanup(context.Background(), lease, time.Millisecond)
	if !errors.Is(err, ErrLeaseIdentityMismatch) {
		t.Fatalf("Cleanup error = %v, want ErrLeaseIdentityMismatch", err)
	}
	if err := unix.Kill(os.Getpid(), 0); err != nil {
		t.Fatalf("Cleanup signalled current process: %v", err)
	}
}

func TestLinuxSignalTargetsRejectBroadcastValues(t *testing.T) {
	for _, value := range []int{-1, 0, 1} {
		if err := validateLinuxSignalTarget(value); !errors.Is(err, ErrLeaseIdentityMismatch) {
			t.Fatalf("validateLinuxSignalTarget(%d) = %v", value, err)
		}
	}
	if err := validateLinuxSignalTarget(2); err != nil {
		t.Fatalf("validateLinuxSignalTarget(2) = %v", err)
	}
}

func TestLinuxControlEOFAndHostSignalCleanTargetTree(t *testing.T) {
	for _, test := range []struct {
		name string
		stop func(t *testing.T, process Process)
	}{
		{name: "control EOF", stop: func(t *testing.T, process Process) {
			if err := process.Close(); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "host SIGTERM", stop: func(t *testing.T, process Process) {
			if err := unix.Kill(process.Lease().HostPID, unix.SIGTERM); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			binary := buildService(t)
			process, err := NewRunner(binary).Prepare(context.Background(), Spec{Executable: binary, Args: []string{"--task-fixture", "spawn-child"}}, testID(7), testID(8))
			if err != nil {
				t.Fatal(err)
			}
			defer process.Close()
			if err := process.Start(context.Background()); err != nil {
				t.Fatal(err)
			}
			childPID := readChildPID(t, process.Output())
			test.stop(t, process)
			_ = receiveResult(t, process.Done())
			assertProcessGone(t, childPID)
		})
	}
}

func TestLinuxPlatformCleansDescendantsAfterNaturalMainExit(t *testing.T) {
	binary := buildService(t)
	process, err := NewRunner(binary).Prepare(context.Background(), helperSpec("exit-with-child"), testID(40), testID(41))
	if err != nil {
		t.Fatal(err)
	}
	defer process.Close()
	if err := process.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	childPID := readChildPID(t, process.Output())
	if result := receiveResult(t, process.Done()); result.ExitCode != 0 || result.Err != nil {
		t.Fatalf("result = %#v", result)
	}
	assertProcessGone(t, childPID)
}

func TestLinuxPlatformEscalatesAndTerminateAlwaysReleasesWait(t *testing.T) {
	binary := buildService(t)
	process, err := NewRunner(binary).Prepare(context.Background(), helperSpec("ignore-term"), testID(42), testID(43))
	if err != nil {
		t.Fatal(err)
	}
	defer process.Close()
	if err := process.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	childPID := readChildPID(t, process.Output())
	started := time.Now()
	err = process.Terminate(context.Background(), 75*time.Millisecond)
	if time.Since(started) < 70*time.Millisecond {
		t.Fatal("Terminate did not honor the TERM grace period")
	}
	if err != nil {
		t.Fatal(err)
	}
	if result := receiveResult(t, process.Done()); result.Err != nil {
		t.Fatal(result.Err)
	}
	assertProcessGone(t, childPID)
}

func TestLinuxTerminateContextErrorStillReleasesWait(t *testing.T) {
	binary := buildService(t)
	process, err := NewRunner(binary).Prepare(context.Background(), helperSpec("ignore-term"), testID(44), testID(45))
	if err != nil {
		t.Fatal(err)
	}
	defer process.Close()
	if err := process.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	childPID := readChildPID(t, process.Output())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := process.Terminate(ctx, time.Second); !errors.Is(err, context.Canceled) {
		t.Fatalf("Terminate error = %v, want context.Canceled", err)
	}
	_ = receiveResult(t, process.Done())
	assertProcessGone(t, childPID)
}

func TestLinuxRunnerDoesNotInheritInternalStatusHandleIntoTarget(t *testing.T) {
	binary := buildService(t)
	process, err := NewRunner(binary).Prepare(context.Background(), helperSpec("report-inheritance"), testID(46), testID(47))
	if err != nil {
		t.Fatal(err)
	}
	defer process.Close()
	if err := process.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	streams := collectOutput(t, process.Output())
	if strings.Contains(streams[StreamStdout], "STATUS_ENV_PRESENT=true") || !strings.Contains(streams[StreamStdout], "STATUS_ENV_PRESENT=false") {
		t.Fatalf("target inherited internal status handle environment: %q", streams[StreamStdout])
	}
	for _, marker := range []string{
		"CONTROL_PIPE_INHERITED=false",
		"STDOUT_PIPE_INHERITED=true",
		"STDERR_PIPE_INHERITED=true",
		"STATUS_PIPE_INHERITED=false",
	} {
		if !strings.Contains(streams[StreamStdout], marker) {
			t.Fatalf("target descriptor inheritance missing %q: %q", marker, streams[StreamStdout])
		}
	}
	if result := receiveResult(t, process.Done()); result.Err != nil {
		t.Fatal(result.Err)
	}
}

func TestLinuxTerminateAfterNaturalCompletionIsIdempotent(t *testing.T) {
	binary := buildService(t)
	process, err := NewRunner(binary).Prepare(context.Background(), Spec{Executable: binary, Args: []string{"--task-fixture", "success"}}, testID(52), testID(53))
	if err != nil {
		t.Fatal(err)
	}
	defer process.Close()
	if err := process.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if result := receiveResult(t, process.Done()); result.Err != nil {
		t.Fatal(result.Err)
	}
	if err := process.Terminate(context.Background(), time.Millisecond); err != nil {
		t.Fatalf("Terminate after completion = %v", err)
	}
}

func TestLinuxRunnerContextCancellationCleansTargetTree(t *testing.T) {
	binary := buildService(t)
	ctx, cancel := context.WithCancel(context.Background())
	process, err := NewRunner(binary).Prepare(ctx, Spec{Executable: binary, Args: []string{"--task-fixture", "spawn-child"}}, testID(48), testID(49))
	if err != nil {
		t.Fatal(err)
	}
	defer process.Close()
	if err := process.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	childPID := readChildPID(t, process.Output())
	cancel()
	_ = receiveResult(t, process.Done())
	assertProcessGone(t, childPID)
}

func TestLinuxCloseBeforeStartReapsHostAndCleanupIsIdempotent(t *testing.T) {
	binary := buildService(t)
	runner := NewRunner(binary)
	process, err := runner.Prepare(context.Background(), Spec{Executable: binary, Args: []string{"--task-fixture", "hang"}}, testID(50), testID(51))
	if err != nil {
		t.Fatal(err)
	}
	lease := process.Lease()
	if err := process.Close(); err != nil {
		t.Fatal(err)
	}
	if err := process.Close(); err != nil {
		t.Fatal(err)
	}
	assertProcessGone(t, lease.HostPID)
	if err := runner.Cleanup(context.Background(), lease, time.Millisecond); err != nil {
		t.Fatalf("idempotent Cleanup error = %v", err)
	}
}

func TestLinuxNoisyTargetDoesNotBlockDoneOrCloseWithoutOutputConsumer(t *testing.T) {
	binary := buildService(t)
	process, err := NewRunner(binary).Prepare(context.Background(), helperSpec("noisy"), testID(54), testID(55))
	if err != nil {
		t.Fatal(err)
	}
	if err := process.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if result := receiveResult(t, process.Done()); !errors.Is(result.Err, ErrProcessOutputOverflow) {
		t.Fatalf("result = %#v, want stable output overflow", result)
	}
	closed := make(chan error, 1)
	go func() { closed <- process.Close() }()
	select {
	case err := <-closed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Close blocked behind unconsumed output")
	}
}

func TestLinuxOutputOverflowDoesNotReplaceStrongerProcessError(t *testing.T) {
	stronger := errors.New("stronger process failure")
	process := &unixProcess{}
	process.outputOverflow.Store(true)
	result := process.applyOutputOverflow(Result{Err: stronger})
	if !errors.Is(result.Err, stronger) || errors.Is(result.Err, ErrProcessOutputOverflow) {
		t.Fatalf("result error = %v, want stronger error only", result.Err)
	}
}

func TestLinuxCleanupRevalidatesHostIdentityAfterGroupScanBeforeSignals(t *testing.T) {
	currentIdentity := "expected"
	signals := 0
	operations := linuxOperations{
		startIdentity: func(int) (string, error) { return currentIdentity, nil },
		ownedGroup: func(int, int) (bool, error) {
			currentIdentity = "reused"
			return true, nil
		},
		signalPID:   func(int, unix.Signal) error { signals++; return nil },
		signalGroup: func(int, unix.Signal) error { signals++; return nil },
		pidExists:   func(int) bool { return true },
		groupExists: func(int) bool { return true },
	}
	runner := &unixRunner{executable: os.Args[0], operations: operations}
	err := runner.Cleanup(context.Background(), task.ProcessLease{
		HostPID: 200, HostStartIdentity: "expected", TargetProcessGroup: 300,
	}, time.Millisecond)
	if !errors.Is(err, ErrLeaseIdentityMismatch) {
		t.Fatalf("Cleanup error = %v, want identity mismatch", err)
	}
	if signals != 0 {
		t.Fatalf("signals = %d, want 0", signals)
	}
}

func TestLinuxHostSignalRevalidatesIdentityAfterWait(t *testing.T) {
	signals := 0
	currentIdentity := "expected"
	operations := linuxOperations{
		startIdentity: func(int) (string, error) { return currentIdentity, nil },
		signalPID:     func(int, unix.Signal) error { signals++; return nil },
		signalGroup:   func(int, unix.Signal) error { signals++; return nil },
	}
	// Simulate the leased Host exiting and its PID being reused while a grace
	// wait is in progress. The next signal must re-read rather than trust the
	// identity observed before that wait.
	currentIdentity = "reused"
	for _, group := range []bool{false, true} {
		err := operations.signalHost(200, "expected", unix.SIGKILL, group)
		if !errors.Is(err, ErrLeaseIdentityMismatch) {
			t.Fatalf("signalHost(group=%t) = %v", group, err)
		}
	}
	if signals != 0 {
		t.Fatalf("signals = %d, want 0", signals)
	}
}

func TestLinuxValidateHostForSignalTreatsDisappearedHostAsGone(t *testing.T) {
	operations := linuxOperations{
		startIdentity: func(int) (string, error) { return "", os.ErrNotExist },
	}
	present, err := operations.validateHostForSignal(200, "expected")
	if err != nil || present {
		t.Fatalf("validateHostForSignal = (%t, %v), want (false, nil)", present, err)
	}
}

func TestLinuxHostSignalSkipsPIDAndGroupWhenHostDisappears(t *testing.T) {
	for _, group := range []bool{false, true} {
		t.Run(fmt.Sprintf("group=%t", group), func(t *testing.T) {
			identityReads := 0
			signals := 0
			operations := linuxOperations{
				startIdentity: func(int) (string, error) {
					identityReads++
					if identityReads == 1 {
						return "expected", nil
					}
					return "", os.ErrNotExist
				},
				signalPID:   func(int, unix.Signal) error { signals++; return nil },
				signalGroup: func(int, unix.Signal) error { signals++; return nil },
			}
			if err := operations.validateHost(200, "expected"); err != nil {
				t.Fatalf("initial validation = %v", err)
			}
			if err := operations.signalHost(200, "expected", unix.SIGKILL, group); err != nil {
				t.Fatalf("signalHost(group=%t) = %v, want already-gone success", group, err)
			}
			if signals != 0 {
				t.Fatalf("signals = %d, want 0", signals)
			}
		})
	}
}

func TestLinuxCleanupContinuesOwnedGroupCleanupWhenHostDisappearsAfterScan(t *testing.T) {
	identityReads := 0
	pidSignals := 0
	var groupSignals []int
	operations := linuxOperations{
		startIdentity: func(int) (string, error) {
			identityReads++
			if identityReads == 1 {
				return "expected", nil
			}
			return "", os.ErrNotExist
		},
		ownedGroup: func(int, int) (bool, error) { return true, nil },
		signalPID: func(int, unix.Signal) error {
			pidSignals++
			return nil
		},
		signalGroup: func(group int, _ unix.Signal) error {
			groupSignals = append(groupSignals, group)
			return nil
		},
		pidExists:   func(int) bool { return false },
		groupExists: func(int) bool { return false },
	}
	runner := &unixRunner{operations: operations}
	err := runner.Cleanup(context.Background(), task.ProcessLease{
		HostPID: 200, HostStartIdentity: "expected", TargetProcessGroup: 300,
	}, 0)
	if err != nil {
		t.Fatalf("Cleanup = %v, want idempotent success", err)
	}
	if pidSignals != 0 {
		t.Fatalf("Host PID signals = %d, want 0", pidSignals)
	}
	if len(groupSignals) != 1 || groupSignals[0] != 300 {
		t.Fatalf("group signals = %v, want only owned target group 300", groupSignals)
	}
}

func TestLinuxForceTerminateSkipsHostGroupEscalationWhenHostDisappears(t *testing.T) {
	controlReader, controlWriter, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer controlReader.Close()

	identityReads := 0
	groupSignals := 0
	hostExited := make(chan struct{})
	operations := linuxOperations{
		startIdentity: func(int) (string, error) {
			identityReads++
			if identityReads == 1 {
				return "expected", nil
			}
			if identityReads == 3 {
				close(hostExited)
			}
			return "", os.ErrNotExist
		},
		signalGroup: func(int, unix.Signal) error { groupSignals++; return nil },
	}
	process := &unixProcess{
		control:    controlWriter,
		hostExited: hostExited,
		lease: task.ProcessLease{
			HostPID: 200, HostStartIdentity: "expected",
		},
		operations: operations,
	}
	if err := process.forceTerminate(context.Background(), 0, nil); err != nil {
		t.Fatalf("forceTerminate = %v, want already-gone success", err)
	}
	if groupSignals != 0 {
		t.Fatalf("Host group signals = %d, want 0", groupSignals)
	}
}

func TestLinuxPrepareIdentityFailureClosesEachOwnedFileOnce(t *testing.T) {
	control := &linuxCountingCloser{}
	status := &linuxCountingCloser{}
	stdout := &linuxCountingCloser{}
	stderr := &linuxCountingCloser{}
	identityReady := make(chan struct{})
	waited := false

	finishPrepareIdentityFailure(control, identityReady, func() error {
		waited = true
		if control.closes != 1 {
			t.Fatalf("control closes before wait = %d, want 1", control.closes)
		}
		if status.closes != 0 || stdout.closes != 0 || stderr.closes != 0 {
			t.Fatal("remaining pipe readers closed before Host wait")
		}
		select {
		case <-identityReady:
		default:
			t.Fatal("identity readiness not released before Host wait")
		}
		return nil
	}, status, stdout, stderr)

	if !waited {
		t.Fatal("Host wait was not called")
	}
	for name, closer := range map[string]*linuxCountingCloser{
		"control": control, "status": status, "stdout": stdout, "stderr": stderr,
	} {
		if closer.closes != 1 {
			t.Fatalf("%s closes = %d, want 1", name, closer.closes)
		}
	}
}

type linuxCountingCloser struct {
	closes int
}

func (closer *linuxCountingCloser) Close() error {
	closer.closes++
	return nil
}

func TestLinuxOwnedGroupScanFailsClosedOnUnreadableOrMalformedStat(t *testing.T) {
	tests := []struct {
		name string
		read func(string) ([]byte, error)
	}{
		{name: "unreadable", read: func(string) ([]byte, error) { return nil, fs.ErrPermission }},
		{name: "malformed", read: func(string) ([]byte, error) { return []byte("malformed"), nil }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := scanLinuxOwnedGroup([]string{"123"}, test.read, 123, 456)
			if !errors.Is(err, errProcessHostUnavailable) {
				t.Fatalf("scan error = %v, want fail-closed unavailable", err)
			}
		})
	}
}

func TestLinuxOwnedGroupScanIgnoresOnlyDisappearedEntries(t *testing.T) {
	exists, err := scanLinuxOwnedGroup([]string{"123"}, func(string) ([]byte, error) {
		return nil, os.ErrNotExist
	}, 123, 456)
	if err != nil || exists {
		t.Fatalf("scan = (%t, %v), want (false, nil)", exists, err)
	}
}

func TestLinuxOwnedGroupScanAcceptsUnrelatedKernelThreadStat(t *testing.T) {
	records := map[string][]byte{
		"2":   linuxStatForTest(2, 0, 0, "10"),
		"300": linuxStatForTest(300, 300, 200, "20"),
	}
	exists, err := scanLinuxOwnedGroup([]string{"2", "300"}, func(path string) ([]byte, error) {
		return records[filepath.Base(filepath.Dir(path))], nil
	}, 300, 200)
	if err != nil || !exists {
		t.Fatalf("scan = (%t, %v), want owned group", exists, err)
	}
}

func TestLinuxStartedStatusRejectsPIDOrGroupOne(t *testing.T) {
	for _, status := range []HostStatus{
		{Kind: "started", PID: 1, ProcessGroup: 2},
		{Kind: "started", PID: 2, ProcessGroup: 1},
	} {
		if err := validateLinuxStartedStatus(status); err == nil {
			t.Fatalf("status accepted: %#v", status)
		}
	}
}

func linuxStatForTest(pid, group, session int, identity string) []byte {
	fields := []string{"S", "1", strconv.Itoa(group), strconv.Itoa(session)}
	for len(fields) < 19 {
		fields = append(fields, "0")
	}
	fields = append(fields, identity)
	return []byte(fmt.Sprintf("%d (process) %s\n", pid, strings.Join(fields, " ")))
}

func TestLinuxCleanupRemovesOwnedGroupAfterHostIsGone(t *testing.T) {
	binary := buildService(t)
	runner := NewRunner(binary)
	process, err := runner.Prepare(context.Background(), Spec{Executable: binary, Args: []string{"--task-fixture", "spawn-child"}}, testID(56), testID(57))
	if err != nil {
		t.Fatal(err)
	}
	defer process.Close()
	if err := process.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	childPID := readChildPID(t, process.Output())
	lease := process.Lease()
	if err := unix.Kill(lease.HostPID, unix.SIGKILL); err != nil {
		t.Fatal(err)
	}
	assertProcessGone(t, lease.HostPID)
	if err := runner.Cleanup(context.Background(), lease, 50*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	assertProcessGone(t, childPID)
}

func TestLinuxCloseIsIdempotentAndDoesNotLeakDescriptorsOrGoroutines(t *testing.T) {
	binary := buildService(t)
	baselineFDs := countFDs(t)
	baselineGoroutines := runtime.NumGoroutine()
	for index := 0; index < 4; index++ {
		process, err := NewRunner(binary).Prepare(context.Background(), Spec{Executable: binary, Args: []string{"--task-fixture", "success"}}, testID(9+index), testID(20))
		if err != nil {
			t.Fatal(err)
		}
		if err := process.Start(context.Background()); err != nil {
			t.Fatal(err)
		}
		_ = receiveResult(t, process.Done())
		if err := process.Close(); err != nil {
			t.Fatal(err)
		}
		if err := process.Close(); err != nil {
			t.Fatal(err)
		}
	}
	deadline := time.Now().Add(2 * time.Second)
	for (countFDs(t) > baselineFDs || runtime.NumGoroutine() > baselineGoroutines+2) && time.Now().Before(deadline) {
		runtime.Gosched()
		time.Sleep(10 * time.Millisecond)
	}
	if got := countFDs(t); got > baselineFDs {
		t.Fatalf("open descriptors = %d, baseline = %d", got, baselineFDs)
	}
	if got := runtime.NumGoroutine(); got > baselineGoroutines+2 {
		t.Fatalf("goroutines = %d, baseline = %d", got, baselineGoroutines)
	}
}

func TestLinuxStartIdentityParsesStatAfterLastClosingParenthesis(t *testing.T) {
	stat := []byte("123 (name with ) and ( parentheses) S 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15 16 17 18 424242 20\n")
	identity, err := parseLinuxStartIdentity(stat)
	if err != nil {
		t.Fatal(err)
	}
	if identity != "424242" {
		t.Fatalf("identity = %q, want 424242", identity)
	}
}

func TestLinuxRunnerRedactsExecutableAndHostErrors(t *testing.T) {
	secret := filepath.Join(t.TempDir(), "secret-process-host")
	_, err := NewRunner(secret).Prepare(context.Background(), Spec{}, testID(30), testID(31))
	if err == nil {
		t.Fatal("Prepare succeeded")
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("Prepare leaked executable path: %v", err)
	}

	binary := buildService(t)
	process, err := NewRunner(binary).Prepare(context.Background(), Spec{Executable: secret}, testID(32), testID(33))
	if err != nil {
		t.Fatal(err)
	}
	defer process.Close()
	err = process.Start(context.Background())
	if err == nil || strings.Contains(err.Error(), secret) {
		t.Fatalf("Start error = %v", err)
	}
}

func buildService(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "unit-test-service")
	command := exec.Command("go", "build", "-o", path, "../../cmd/unit-test-service")
	command.Env = os.Environ()
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build service: %v\n%s", err, output)
	}
	return path
}

func testID(index int) string { return fmt.Sprintf("%032x", index) }

func readChildPID(t *testing.T, output <-chan Output) int {
	t.Helper()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	var stdout strings.Builder
	for {
		select {
		case item, ok := <-output:
			if !ok {
				t.Fatalf("output closed before child PID: %q", stdout.String())
			}
			if item.Stream == StreamStdout {
				stdout.Write(item.Data)
				if strings.Contains(stdout.String(), "\n") {
					return childPIDFromString(t, stdout.String())
				}
			}
		case <-deadline.C:
			t.Fatalf("timed out waiting for child PID: %q", stdout.String())
		}
	}
}

func childPIDFromString(t *testing.T, value string) int {
	t.Helper()
	for _, line := range strings.Split(value, "\n") {
		if strings.HasPrefix(line, "CHILD_PID=") {
			pid, err := strconv.Atoi(strings.TrimPrefix(line, "CHILD_PID="))
			if err == nil && pid > 0 {
				return pid
			}
		}
	}
	t.Fatalf("missing child PID in %q", value)
	return 0
}

func collectOutput(t *testing.T, output <-chan Output) map[Stream]string {
	t.Helper()
	streams := map[Stream]string{}
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	for {
		select {
		case item, ok := <-output:
			if !ok {
				return streams
			}
			streams[item.Stream] += string(item.Data)
		case <-deadline.C:
			t.Fatal("timed out collecting output")
		}
	}
}

func receiveResult(t *testing.T, done <-chan Result) Result {
	t.Helper()
	select {
	case result := <-done:
		return result
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for result")
		return Result{}
	}
}

func assertProcessGone(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if err := unix.Kill(pid, 0); errors.Is(err, unix.ESRCH) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("process %d still exists", pid)
}

func helperSpec(mode string) Spec {
	return Spec{
		Executable: os.Args[0],
		Args:       []string{"-test.run=^TestLinuxHelperProcess$"},
		Env:        []string{helperModeEnvironment + "=" + mode},
	}
}

func countFDs(t *testing.T) int {
	t.Helper()
	entries, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		t.Fatal(err)
	}
	return len(entries)
}
