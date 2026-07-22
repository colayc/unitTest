//go:build windows

package processcontrol

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	"unit-test-ide.local/test-service/internal/task"
	"unit-test-ide.local/test-service/internal/winprocess"
)

const windowsHelperModeEnvironment = "UNIT_TEST_PROCESSCONTROL_WINDOWS_HELPER"

func TestWindowsHelperProcess(t *testing.T) {
	switch os.Getenv(windowsHelperModeEnvironment) {
	case "":
		return
	case "child":
		select {}
	case "exit-with-child":
		child := exec.Command(os.Args[0], "-test.run=^TestWindowsHelperProcess$")
		child.Env = append(os.Environ(), windowsHelperModeEnvironment+"=child")
		child.Stdout, child.Stderr = os.Stdout, os.Stderr
		if err := child.Start(); err != nil {
			t.Fatal(err)
		}
		fmt.Printf("CHILD_PID=%d\n", child.Process.Pid)
		os.Exit(0)
	case "noisy":
		chunk := strings.Repeat("x", 4096)
		for index := 0; index < 1024; index++ {
			fmt.Fprint(os.Stdout, chunk)
		}
	case "report-inheritance":
		_, present := os.LookupEnv("UNIT_TEST_IDE_STATUS_HANDLE")
		fmt.Printf("STATUS_ENV_PRESENT=%t\n", present)
	case "report-environment":
		fmt.Printf("WINDOWS_ENV_VALUE=%s\n", os.Getenv("UNIT_TEST_WINDOWS_ENV_VALUE"))
	default:
		t.Fatal("unknown helper mode")
	}
}

func TestJobObjectTerminatesHostAndGrandchild(t *testing.T) {
	binary := buildWindowsService(t)
	process, err := NewRunner(binary).Prepare(context.Background(), Spec{
		Executable: binary,
		Args:       []string{"--task-fixture", "spawn-child"},
	}, windowsTestID(1), windowsTestID(2))
	if err != nil {
		t.Fatal(err)
	}
	defer process.Close(context.Background())
	hostPID := process.Lease().HostPID
	if err := process.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	childPID := readWindowsChildPID(t, process.Output())
	if err := process.Terminate(context.Background(), 250*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if result := receiveWindowsResult(t, process.Done()); result.Err != nil {
		t.Fatal(result.Err)
	}
	assertWindowsProcessGone(t, hostPID)
	assertWindowsProcessGone(t, childPID)
}

func TestWindowsNaturalMainExitKillsDescendantsBeforeDone(t *testing.T) {
	binary := buildWindowsService(t)
	process, err := NewRunner(binary).Prepare(context.Background(), Spec{
		Executable: os.Args[0],
		Args:       []string{"-test.run=^TestWindowsHelperProcess$"},
		Env:        []string{windowsHelperModeEnvironment + "=exit-with-child"},
	}, windowsTestID(3), windowsTestID(4))
	if err != nil {
		t.Fatal(err)
	}
	defer process.Close(context.Background())
	if err := process.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	childPID := readWindowsChildPID(t, process.Output())
	if result := receiveWindowsResult(t, process.Done()); result.ExitCode != 0 || result.Err != nil {
		t.Fatalf("result = %#v", result)
	}
	assertWindowsProcessGone(t, childPID)
}

func TestWindowsPrepareBlocksTargetUntilStart(t *testing.T) {
	binary := buildWindowsService(t)
	process, err := NewRunner(binary).Prepare(context.Background(), Spec{
		Executable: binary,
		Args:       []string{"--task-fixture", "spawn-child"},
	}, windowsTestID(5), windowsTestID(6))
	if err != nil {
		t.Fatal(err)
	}
	defer process.Close(context.Background())
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
}

func TestWindowsPreparedParentPipeHandlesAreNotInheritable(t *testing.T) {
	binary := buildWindowsService(t)
	value, err := NewRunner(binary).Prepare(context.Background(), Spec{Executable: binary, Args: []string{"--task-fixture", "success"}}, windowsTestID(51), windowsTestID(52))
	if err != nil {
		t.Fatal(err)
	}
	defer value.Close(context.Background())
	process := value.(*windowsProcess)
	for name, file := range map[string]*os.File{
		"control": process.control,
		"status":  process.status,
		"stdout":  process.stdout,
		"stderr":  process.stderr,
	} {
		flags, err := windowsHandleFlags(windows.Handle(file.Fd()))
		if err != nil {
			t.Fatalf("%s handle: %v", name, err)
		}
		if flags&windows.HANDLE_FLAG_INHERIT != 0 {
			t.Fatalf("%s parent handle is inheritable", name)
		}
	}
}

func TestWindowsPrepareAssignsOuterJobBeforeResumeAndFailsClosed(t *testing.T) {
	events := []string{}
	operations := defaultWindowsRunnerOperations()
	operations.createProtectedJob = func(flags uint32) (windows.Handle, error) {
		events = append(events, fmt.Sprintf("job:%x", flags))
		return windows.Handle(101), nil
	}
	operations.createHost = func(string, windows.Handle, windows.Handle, windows.Handle, windows.Handle) (windows.ProcessInformation, error) {
		events = append(events, "create-suspended")
		return windows.ProcessInformation{Process: 102, Thread: 103, ProcessId: 104}, nil
	}
	operations.assignProcess = func(windows.Handle, windows.Handle) error {
		events = append(events, "assign")
		return nil
	}
	operations.startIdentity = func(windows.Handle) (string, error) { return "identity", nil }
	operations.duplicateObserver = func(windows.Handle) (windows.Handle, error) { return 204, nil }
	operations.resumeThread = func(windows.Handle) error {
		events = append(events, "resume")
		return errors.New("injected resume failure")
	}
	operations.terminateJob = func(windows.Handle, uint32) error {
		events = append(events, "terminate-job")
		return nil
	}
	operations.terminateProcess = func(windows.Handle, uint32) error {
		events = append(events, "terminate-process")
		return nil
	}
	operations.waitProcess = func(windows.Handle, uint32) (uint32, error) { return windows.WAIT_OBJECT_0, nil }
	operations.closeHandle = func(windows.Handle) error { return nil }
	runner := &windowsRunner{executable: os.Args[0], operations: operations}
	if process, err := runner.Prepare(context.Background(), Spec{Executable: os.Args[0]}, windowsTestID(7), windowsTestID(8)); err == nil || process != nil {
		t.Fatalf("Prepare = (%#v, %v), want fail closed", process, err)
	}
	want := []string{"job:2000", "create-suspended", "assign", "resume", "terminate-job", "terminate-process"}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("events = %#v, want %#v", events, want)
	}
}

func TestWindowsPrepareJobOrAssignmentFailureNeverResumes(t *testing.T) {
	for _, test := range []struct {
		name       string
		failJob    bool
		failAssign bool
	}{
		{name: "job", failJob: true},
		{name: "assignment", failAssign: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			resumed := false
			operations := defaultWindowsRunnerOperations()
			operations.createProtectedJob = func(uint32) (windows.Handle, error) {
				if test.failJob {
					return 0, errors.New("injected job failure")
				}
				return 201, nil
			}
			operations.createHost = func(string, windows.Handle, windows.Handle, windows.Handle, windows.Handle) (windows.ProcessInformation, error) {
				return windows.ProcessInformation{Process: 202, Thread: 203, ProcessId: 204}, nil
			}
			operations.assignProcess = func(windows.Handle, windows.Handle) error {
				if test.failAssign {
					return errors.New("injected assignment failure")
				}
				return nil
			}
			operations.resumeThread = func(windows.Handle) error { resumed = true; return nil }
			operations.terminateProcess = func(windows.Handle, uint32) error { return nil }
			operations.waitProcess = func(windows.Handle, uint32) (uint32, error) { return windows.WAIT_OBJECT_0, nil }
			operations.closeHandle = func(windows.Handle) error { return nil }
			runner := &windowsRunner{executable: os.Args[0], operations: operations}
			if process, err := runner.Prepare(context.Background(), Spec{}, windowsTestID(9), windowsTestID(10)); err == nil || process != nil {
				t.Fatalf("Prepare = (%#v, %v)", process, err)
			}
			if resumed {
				t.Fatal("Host resumed after protection failure")
			}
		})
	}
}

func TestWindowsPrepareAssignmentCleanupUsesFallbackAndRetainsOwnershipOnFailure(t *testing.T) {
	for _, test := range []struct {
		name           string
		allowInitially bool
	}{
		{name: "native fallback", allowInitially: true},
		{name: "background retry", allowInitially: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			var allowCleanup atomic.Bool
			allowCleanup.Store(test.allowInitially)
			var nativeCalled atomic.Bool
			var processClosed atomic.Bool
			var closedBeforeSignal atomic.Bool
			operations := defaultWindowsRunnerOperations()
			operations.createProtectedJob = func(uint32) (windows.Handle, error) { return 701, nil }
			operations.createHost = func(string, windows.Handle, windows.Handle, windows.Handle, windows.Handle) (windows.ProcessInformation, error) {
				return windows.ProcessInformation{Process: 702, Thread: 703, ProcessId: 704}, nil
			}
			operations.assignProcess = func(windows.Handle, windows.Handle) error { return errors.New("injected assignment failure") }
			operations.terminateProcess = func(windows.Handle, uint32) error { return errors.New("injected primary failure") }
			operations.nativeTerminateProcess = func(windows.Handle, uint32) error {
				nativeCalled.Store(true)
				if allowCleanup.Load() {
					return nil
				}
				return errors.New("injected native failure")
			}
			operations.waitProcess = func(windows.Handle, uint32) (uint32, error) {
				if allowCleanup.Load() {
					return windows.WAIT_OBJECT_0, nil
				}
				return uint32(windows.WAIT_TIMEOUT), nil
			}
			operations.closeHandle = func(handle windows.Handle) error {
				if handle == 702 {
					if !allowCleanup.Load() {
						closedBeforeSignal.Store(true)
					}
					processClosed.Store(true)
				}
				return nil
			}
			runner := &windowsRunner{executable: os.Args[0], operations: operations}
			started := time.Now()
			process, err := runner.Prepare(context.Background(), Spec{}, windowsTestID(59), windowsTestID(60))
			if process != nil {
				t.Fatalf("Prepare returned process %#v", process)
			}
			if test.allowInitially {
				if !errors.Is(err, errProcessHostUnavailable) || !processClosed.Load() {
					t.Fatalf("error=%v processClosed=%t", err, processClosed.Load())
				}
			} else {
				if !errors.Is(err, errProcessHostFailed) || processClosed.Load() {
					t.Fatalf("error=%v processClosed=%t", err, processClosed.Load())
				}
				if time.Since(started) > 500*time.Millisecond {
					t.Fatalf("Prepare cleanup failure was not bounded: %s", time.Since(started))
				}
				allowCleanup.Store(true)
				deadline := time.Now().Add(time.Second)
				for !processClosed.Load() && time.Now().Before(deadline) {
					time.Sleep(time.Millisecond)
				}
			}
			if !nativeCalled.Load() || !processClosed.Load() || closedBeforeSignal.Load() {
				t.Fatalf("native=%t processClosed=%t closedBeforeSignal=%t", nativeCalled.Load(), processClosed.Load(), closedBeforeSignal.Load())
			}
		})
	}
}

func TestWindowsProtectedJobHasKillOnCloseLimit(t *testing.T) {
	job, err := createRunnerProtectedJob(windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE)
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(job) //nolint:errcheck
	limits := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	if err := windows.QueryInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&limits)), uint32(unsafe.Sizeof(limits)), nil); err != nil {
		t.Fatal(err)
	}
	if limits.BasicLimitInformation.LimitFlags&windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE == 0 {
		t.Fatalf("job flags = %#x", limits.BasicLimitInformation.LimitFlags)
	}
}

func TestWindowsCleanupRejectsStaleIdentityWithoutTermination(t *testing.T) {
	terminated := false
	operations := defaultWindowsRunnerOperations()
	operations.openProcess = func(uint32) (windows.Handle, error) { return 301, nil }
	operations.startIdentity = func(windows.Handle) (string, error) { return "current", nil }
	operations.terminateProcess = func(windows.Handle, uint32) error { terminated = true; return nil }
	operations.closeHandle = func(windows.Handle) error { return nil }
	runner := &windowsRunner{operations: operations}
	err := runner.Cleanup(context.Background(), task.ProcessLease{HostPID: 1234, HostStartIdentity: "stale"}, 0)
	if !errors.Is(err, ErrLeaseIdentityMismatch) || terminated {
		t.Fatalf("Cleanup error = %v, terminated = %t", err, terminated)
	}
}

func TestWindowsCleanupIsIdempotentForExitedMatchingHost(t *testing.T) {
	terminated := false
	operations := defaultWindowsRunnerOperations()
	operations.openProcess = func(uint32) (windows.Handle, error) { return 311, nil }
	operations.startIdentity = func(windows.Handle) (string, error) { return "matching", nil }
	operations.waitProcess = func(windows.Handle, uint32) (uint32, error) { return windows.WAIT_OBJECT_0, nil }
	operations.terminateProcess = func(windows.Handle, uint32) error { terminated = true; return windows.ERROR_ACCESS_DENIED }
	operations.closeHandle = func(windows.Handle) error { return nil }
	runner := &windowsRunner{operations: operations}
	if err := runner.Cleanup(context.Background(), task.ProcessLease{HostPID: 1234, HostStartIdentity: "matching"}, 0); err != nil {
		t.Fatal(err)
	}
	if terminated {
		t.Fatal("Cleanup attempted to terminate an already exited Host")
	}
}

func TestWindowsStartIdentityUsesCreationTime(t *testing.T) {
	identity, err := windowsStartIdentity(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	if value, err := strconv.ParseUint(identity, 10, 64); err != nil || value == 0 {
		t.Fatalf("identity = %q, err = %v", identity, err)
	}
}

func TestWindowsStartIdentityRetriesObserverClose(t *testing.T) {
	var attempts atomic.Int32
	identity, err := windowsStartIdentityWithOperations(
		123,
		func(uint32) (windows.Handle, error) { return 731, nil },
		func(handle windows.Handle) (string, error) {
			if handle != 731 {
				t.Fatalf("identity handle = %d", handle)
			}
			return "identity", nil
		},
		func(handle windows.Handle) error {
			if handle != 731 {
				t.Fatalf("close handle = %d", handle)
			}
			if attempts.Add(1) == 1 {
				return errors.New("injected observer close failure")
			}
			return nil
		},
	)
	if err != nil || identity != "identity" {
		t.Fatalf("identity = (%q, %v)", identity, err)
	}
	deadline := time.Now().Add(time.Second)
	for attempts.Load() < 2 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if got := attempts.Load(); got < 2 {
		t.Fatalf("close attempts = %d", got)
	}
}

func TestWindowsTerminateErrorStillReleasesDoneAndKillsTree(t *testing.T) {
	binary := buildWindowsService(t)
	operations := defaultWindowsRunnerOperations()
	operations.terminateJob = func(windows.Handle, uint32) error { return errors.New("injected terminate failure") }
	runner := &windowsRunner{executable: binary, operations: operations}
	process, err := runner.Prepare(context.Background(), Spec{Executable: binary, Args: []string{"--task-fixture", "spawn-child"}}, windowsTestID(11), windowsTestID(12))
	if err != nil {
		t.Fatal(err)
	}
	defer process.Close(context.Background())
	if err := process.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	childPID := readWindowsChildPID(t, process.Output())
	if err := process.Terminate(context.Background(), 0); err == nil {
		t.Fatal("Terminate returned nil after injected native failure")
	}
	_ = receiveWindowsResult(t, process.Done())
	assertWindowsProcessGone(t, childPID)
}

func TestWindowsContextCancellationAndCloseKillTargetTrees(t *testing.T) {
	for _, test := range []struct {
		name string
		stop func(context.CancelFunc, Process) error
	}{
		{name: "context cancellation", stop: func(cancel context.CancelFunc, _ Process) error { cancel(); return nil }},
		{name: "close", stop: func(_ context.CancelFunc, process Process) error { return process.Close(context.Background()) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			binary := buildWindowsService(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			process, err := NewRunner(binary).Prepare(ctx, Spec{Executable: binary, Args: []string{"--task-fixture", "spawn-child"}}, windowsTestID(13), windowsTestID(14))
			if err != nil {
				t.Fatal(err)
			}
			defer process.Close(context.Background())
			if err := process.Start(context.Background()); err != nil {
				t.Fatal(err)
			}
			childPID := readWindowsChildPID(t, process.Output())
			if err := test.stop(cancel, process); err != nil {
				t.Fatal(err)
			}
			assertWindowsProcessGone(t, childPID)
		})
	}
}

func TestWindowsControlEOFAndUnexpectedHostExitCleanTargetTrees(t *testing.T) {
	for _, test := range []struct {
		name string
		stop func(*windowsProcess) error
	}{
		{name: "control EOF", stop: func(process *windowsProcess) error { process.closeControl(); return nil }},
		{name: "Host exit", stop: func(process *windowsProcess) error {
			_, err := process.hostOwner.Use(func(handle windows.Handle) error { return windows.TerminateProcess(handle, 1) })
			return err
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			binary := buildWindowsService(t)
			value, err := NewRunner(binary).Prepare(context.Background(), Spec{Executable: binary, Args: []string{"--task-fixture", "spawn-child"}}, windowsTestID(55), windowsTestID(56))
			if err != nil {
				t.Fatal(err)
			}
			defer value.Close(context.Background())
			if err := value.Start(context.Background()); err != nil {
				t.Fatal(err)
			}
			childPID := readWindowsChildPID(t, value.Output())
			if err := test.stop(value.(*windowsProcess)); err != nil {
				t.Fatal(err)
			}
			_ = receiveWindowsResult(t, value.Done())
			assertWindowsProcessGone(t, childPID)
		})
	}
}

func TestWindowsOrdinaryNonzeroExitIsAResult(t *testing.T) {
	binary := buildWindowsService(t)
	process, err := NewRunner(binary).Prepare(context.Background(), Spec{Executable: binary, Args: []string{"--task-fixture", "exit-nonzero"}}, windowsTestID(57), windowsTestID(58))
	if err != nil {
		t.Fatal(err)
	}
	defer process.Close(context.Background())
	if err := process.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	result := receiveWindowsResult(t, process.Done())
	if result.ExitCode != 17 || result.Err != nil {
		t.Fatalf("result = %#v", result)
	}
}

func TestWindowsNoisyTargetDoesNotBlockDoneOrCloseWithoutOutputConsumer(t *testing.T) {
	binary := buildWindowsService(t)
	process, err := NewRunner(binary).Prepare(context.Background(), windowsHelperSpec("noisy"), windowsTestID(15), windowsTestID(16))
	if err != nil {
		t.Fatal(err)
	}
	if err := process.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	result := receiveWindowsResult(t, process.Done())
	if !errors.Is(result.Err, ErrProcessOutputOverflow) {
		t.Fatalf("result = %#v", result)
	}
	if err := process.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestWindowsTargetDoesNotReceiveStatusHandleEnvironment(t *testing.T) {
	binary := buildWindowsService(t)
	process, err := NewRunner(binary).Prepare(context.Background(), windowsHelperSpec("report-inheritance"), windowsTestID(17), windowsTestID(18))
	if err != nil {
		t.Fatal(err)
	}
	defer process.Close(context.Background())
	if err := process.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	var output strings.Builder
	for item := range process.Output() {
		if item.Stream == StreamStdout {
			output.Write(item.Data)
		}
	}
	if result := receiveWindowsResult(t, process.Done()); result.Err != nil {
		t.Fatal(result.Err)
	}
	if !strings.Contains(output.String(), "STATUS_ENV_PRESENT=false") {
		t.Fatalf("output = %q", output.String())
	}
}

func TestWindowsTargetEnvironmentPrefersSpecValues(t *testing.T) {
	t.Setenv("UNIT_TEST_WINDOWS_ENV_VALUE", "parent")
	binary := buildWindowsService(t)
	spec := windowsHelperSpec("report-environment")
	spec.Env = append(spec.Env, "unit_test_windows_env_value=spec")
	process, err := NewRunner(binary).Prepare(context.Background(), spec, windowsTestID(53), windowsTestID(54))
	if err != nil {
		t.Fatal(err)
	}
	defer process.Close(context.Background())
	if err := process.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	var output strings.Builder
	for item := range process.Output() {
		if item.Stream == StreamStdout {
			output.Write(item.Data)
		}
	}
	if result := receiveWindowsResult(t, process.Done()); result.Err != nil {
		t.Fatal(result.Err)
	}
	if !strings.Contains(output.String(), "WINDOWS_ENV_VALUE=spec") {
		t.Fatalf("output = %q", output.String())
	}
}

func TestWindowsCloseIsIdempotentWithoutHandleOrGoroutineLeak(t *testing.T) {
	binary := buildWindowsService(t)
	beforeHandleSnapshot := windowsHandleSnapshot(t)
	beforeHandles := windowsHandleCount(t)
	beforeGoroutines := runtime.NumGoroutine()
	handleCounts := []uint32{beforeHandles}
	for index := 0; index < 4; index++ {
		process, err := NewRunner(binary).Prepare(context.Background(), Spec{Executable: binary, Args: []string{"--task-fixture", "success"}}, windowsTestID(20+index), windowsTestID(30))
		if err != nil {
			t.Fatal(err)
		}
		if err := process.Start(context.Background()); err != nil {
			t.Fatal(err)
		}
		_ = receiveWindowsResult(t, process.Done())
		if err := process.Close(context.Background()); err != nil {
			t.Fatal(err)
		}
		if err := process.Close(context.Background()); err != nil {
			t.Fatal(err)
		}
		handleCounts = append(handleCounts, windowsHandleCount(t))
	}
	runtime.GC()
	after := windowsHandleCount(t)
	newTypes := windowsNewHandleTypes(t, beforeHandleSnapshot)
	for _, handleType := range newTypes {
		if handleType == "Job" || handleType == "Process" || handleType == "File" {
			t.Fatalf("owned handle leaked: counts = %v, final %d, new handle types = %v", handleCounts, after, newTypes)
		}
	}
	if after > beforeHandles+16 {
		t.Fatalf("handle counts = %v, final %d, new handle types = %v", handleCounts, after, newTypes)
	}
	if after := runtime.NumGoroutine(); after > beforeGoroutines+4 {
		t.Fatalf("goroutine count grew from %d to %d", beforeGoroutines, after)
	}
}

func TestWindowsProcessCloseCanRetryAfterContextCancellation(t *testing.T) {
	binary := buildWindowsService(t)
	process, err := NewRunner(binary).Prepare(context.Background(), Spec{
		Executable: binary,
		Args:       []string{"--task-fixture", "success"},
	}, windowsTestID(69), windowsTestID(70))
	if err != nil {
		t.Fatal(err)
	}

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := process.Close(canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("Close(canceled) = %v, want context.Canceled", err)
	}

	retry, retryCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer retryCancel()
	if err := process.Close(retry); err != nil {
		t.Fatalf("Close(retry) = %v", err)
	}
}

func TestWindowsCloseAndDoneAreBoundedWhenEveryNativeCleanupStageFails(t *testing.T) {
	controlReader, controlWriter, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer controlReader.Close()
	statusReader, statusWriter, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer statusWriter.Close()
	stdoutReader, stdoutWriter, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer stdoutWriter.Close()
	stderrReader, stderrWriter, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer stderrWriter.Close()

	var allowCleanup atomic.Bool
	var hostClosed atomic.Bool
	operations := defaultWindowsRunnerOperations()
	operations.terminateJob = func(windows.Handle, uint32) error { return errors.New("injected job termination failure") }
	operations.terminateProcess = func(windows.Handle, uint32) error {
		if allowCleanup.Load() {
			return nil
		}
		return errors.New("injected process termination failure")
	}
	operations.nativeTerminateProcess = func(windows.Handle, uint32) error {
		if allowCleanup.Load() {
			return nil
		}
		return errors.New("injected native termination failure")
	}
	operations.waitProcess = func(windows.Handle, uint32) (uint32, error) {
		if allowCleanup.Load() {
			return windows.WAIT_OBJECT_0, nil
		}
		return windows.WAIT_FAILED, errors.New("injected wait failure")
	}
	operations.closeHandle = func(handle windows.Handle) error {
		switch handle {
		case 401:
			return errors.New("injected job close failure")
		case 402:
			hostClosed.Store(true)
		}
		return nil
	}
	process := &windowsProcess{
		control:       controlWriter,
		status:        statusReader,
		stdout:        stdoutReader,
		stderr:        stderrReader,
		jobOwner:      winprocess.NewHandleOwner(401, operations.closeHandle),
		hostOwner:     winprocess.NewHandleOwner(402, operations.closeHandle),
		observerOwner: winprocess.NewHandleOwner(403, operations.closeHandle),
		ops:           operations,
		started:       true,
		hostExited:    make(chan struct{}),
		output:        make(chan Output, 1),
		outputDone:    make(chan struct{}),
		outputDiscard: make(chan struct{}),
		done:          make(chan Result, 1),
		finished:      make(chan struct{}),
		contextStop:   make(chan struct{}),
	}
	go process.copyOutput()

	closed := make(chan error, 1)
	go func() { closed <- process.Close(context.Background()) }()
	select {
	case err := <-closed:
		if !errors.Is(err, errProcessHostFailed) {
			t.Fatalf("Close error = %v", err)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Close blocked after native cleanup failures")
	}
	select {
	case result, ok := <-process.Done():
		if !ok || !errors.Is(result.Err, errProcessHostFailed) {
			t.Fatalf("Done result = %#v, open=%t", result, ok)
		}
		if _, open := <-process.Done(); open {
			t.Fatal("Done published more than one result")
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Done remained blocked")
	}
	select {
	case _, open := <-process.Output():
		if open {
			t.Fatal("Output remained open")
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Output remained blocked")
	}
	if hostClosed.Load() {
		t.Fatal("Host process handle was discarded before signaled")
	}
	allowCleanup.Store(true)
	deadline := time.Now().Add(time.Second)
	for !hostClosed.Load() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if !hostClosed.Load() {
		t.Fatal("background cleanup did not close the signaled Host handle")
	}
	started := time.Now()
	if err := process.Close(context.Background()); !errors.Is(err, errProcessHostFailed) || time.Since(started) > 100*time.Millisecond {
		t.Fatalf("second Close = %v after %s", err, time.Since(started))
	}
}

func TestWindowsHostHandleCloseCanRetryAfterNativeFailure(t *testing.T) {
	var attempts atomic.Int32
	operations := defaultWindowsRunnerOperations()
	operations.closeHandle = func(windows.Handle) error {
		if attempts.Add(1) == 1 {
			return errors.New("injected close failure")
		}
		return nil
	}
	process := &windowsProcess{hostOwner: winprocess.NewHandleOwner(501, operations.closeHandle), ops: operations}
	if err := process.closeHostHandle(); err == nil {
		t.Fatal("first close unexpectedly succeeded")
	}
	if err := process.closeHostHandle(); err != nil {
		t.Fatalf("second close = %v", err)
	}
	if got := attempts.Load(); got != 2 {
		t.Fatalf("close attempts = %d", got)
	}
}

func TestWindowsOuterJobCloseRetriesAfterFailure(t *testing.T) {
	var attempts atomic.Int32
	var successes atomic.Int32
	operations := defaultWindowsRunnerOperations()
	operations.closeHandle = func(handle windows.Handle) error {
		if handle != 601 {
			return nil
		}
		if attempts.Add(1) == 1 {
			return errors.New("injected job close failure")
		}
		successes.Add(1)
		return nil
	}
	process := &windowsProcess{jobOwner: winprocess.NewHandleOwner(601, operations.closeHandle), ops: operations}
	process.closeOuterJob()
	process.closeOuterJob()
	if got := attempts.Load(); got != 2 {
		t.Fatalf("close attempts = %d", got)
	}
	if got := successes.Load(); got != 1 {
		t.Fatalf("successful closes = %d", got)
	}
	if process.jobOwner.Open() {
		t.Fatal("outer Job handle ownership was not released")
	}
	if err := process.outerJobError(); err != nil {
		t.Fatalf("retained close error = %v", err)
	}
}

func TestWindowsNaturalHostExitRetriesProcessClose(t *testing.T) {
	var attempts atomic.Int32
	var successes atomic.Int32
	operations := defaultWindowsRunnerOperations()
	operations.waitProcess = func(handle windows.Handle, timeout uint32) (uint32, error) {
		if handle != 611 || timeout != windows.INFINITE {
			t.Fatalf("wait = (%d, %d)", handle, timeout)
		}
		return windows.WAIT_OBJECT_0, nil
	}
	operations.closeHandle = func(handle windows.Handle) error {
		if handle != 612 {
			return nil
		}
		if attempts.Add(1) == 1 {
			return errors.New("injected process close failure")
		}
		successes.Add(1)
		return nil
	}
	process := &windowsProcess{
		hostOwner:     winprocess.NewHandleOwner(612, operations.closeHandle),
		observerOwner: winprocess.NewHandleOwner(611, operations.closeHandle),
		ops:           operations,
		hostExited:    make(chan struct{}),
	}
	process.waitHost()
	_ = process.closeHostHandle()
	deadline := time.Now().Add(time.Second)
	for successes.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if got := attempts.Load(); got < 2 {
		t.Fatalf("close attempts = %d", got)
	}
	if got := successes.Load(); got != 1 {
		t.Fatalf("successful closes = %d", got)
	}
	if process.hostOwner.Open() {
		t.Fatal("Host process handle ownership was not released")
	}
}

func TestWindowsPrepareRetriesResumedThreadClose(t *testing.T) {
	var threadAttempts atomic.Int32
	var threadSuccesses atomic.Int32
	operations := defaultWindowsRunnerOperations()
	operations.createProtectedJob = func(uint32) (windows.Handle, error) { return 621, nil }
	operations.createHost = func(string, windows.Handle, windows.Handle, windows.Handle, windows.Handle) (windows.ProcessInformation, error) {
		return windows.ProcessInformation{Process: 622, Thread: 623, ProcessId: 624}, nil
	}
	operations.assignProcess = func(windows.Handle, windows.Handle) error { return nil }
	operations.startIdentity = func(windows.Handle) (string, error) { return "identity", nil }
	operations.duplicateObserver = func(windows.Handle) (windows.Handle, error) { return 625, nil }
	operations.resumeThread = func(windows.Handle) error { return nil }
	operations.waitProcess = func(windows.Handle, uint32) (uint32, error) { return windows.WAIT_OBJECT_0, nil }
	operations.closeHandle = func(handle windows.Handle) error {
		if handle != 623 {
			return nil
		}
		if threadAttempts.Add(1) == 1 {
			return errors.New("injected thread close failure")
		}
		threadSuccesses.Add(1)
		return nil
	}
	runner := &windowsRunner{executable: os.Args[0], operations: operations}
	value, err := runner.Prepare(context.Background(), Spec{}, windowsTestID(63), windowsTestID(64))
	if err != nil {
		t.Fatal(err)
	}
	process := value.(*windowsProcess)
	defer process.Close(context.Background())
	deadline := time.Now().Add(time.Second)
	for threadSuccesses.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if got := threadAttempts.Load(); got < 2 {
		t.Fatalf("thread close attempts = %d", got)
	}
	if got := threadSuccesses.Load(); got != 1 {
		t.Fatalf("successful thread closes = %d", got)
	}
}

func TestWindowsPrepareObserverFailureCleansUpBeforeResume(t *testing.T) {
	var resumed atomic.Bool
	var processClosed atomic.Bool
	operations := defaultWindowsRunnerOperations()
	operations.createProtectedJob = func(uint32) (windows.Handle, error) { return 631, nil }
	operations.createHost = func(string, windows.Handle, windows.Handle, windows.Handle, windows.Handle) (windows.ProcessInformation, error) {
		return windows.ProcessInformation{Process: 632, Thread: 633, ProcessId: 634}, nil
	}
	operations.assignProcess = func(windows.Handle, windows.Handle) error { return nil }
	operations.startIdentity = func(windows.Handle) (string, error) { return "identity", nil }
	operations.duplicateObserver = func(windows.Handle) (windows.Handle, error) {
		return 0, errors.New("injected duplicate failure")
	}
	operations.resumeThread = func(windows.Handle) error {
		resumed.Store(true)
		return nil
	}
	operations.terminateProcess = func(windows.Handle, uint32) error { return nil }
	operations.waitProcess = func(windows.Handle, uint32) (uint32, error) { return windows.WAIT_OBJECT_0, nil }
	operations.closeHandle = func(handle windows.Handle) error {
		if handle == 632 {
			processClosed.Store(true)
		}
		return nil
	}
	runner := &windowsRunner{executable: os.Args[0], operations: operations}
	process, err := runner.Prepare(context.Background(), Spec{}, windowsTestID(65), windowsTestID(66))
	if process != nil || !errors.Is(err, errProcessHostUnavailable) {
		t.Fatalf("Prepare = (%#v, %v)", process, err)
	}
	if resumed.Load() || !processClosed.Load() {
		t.Fatalf("resumed=%t processClosed=%t", resumed.Load(), processClosed.Load())
	}
}

func TestWindowsObserverWaitAndCleanupHaveExclusiveHandles(t *testing.T) {
	observerStarted := make(chan struct{})
	allowObserverExit := make(chan struct{})
	var observerActive atomic.Bool
	var allowOriginalCleanup atomic.Bool
	var rawHandleCollision atomic.Bool
	var originalClosed atomic.Bool
	var observerClosed atomic.Bool
	var observerCloseAttempts atomic.Int32
	operations := defaultWindowsRunnerOperations()
	operations.createProtectedJob = func(uint32) (windows.Handle, error) { return 641, nil }
	operations.createHost = func(string, windows.Handle, windows.Handle, windows.Handle, windows.Handle) (windows.ProcessInformation, error) {
		return windows.ProcessInformation{Process: 642, Thread: 643, ProcessId: 644}, nil
	}
	operations.assignProcess = func(windows.Handle, windows.Handle) error { return nil }
	operations.startIdentity = func(windows.Handle) (string, error) { return "identity", nil }
	operations.duplicateObserver = func(handle windows.Handle) (windows.Handle, error) {
		if handle != 642 {
			t.Fatalf("duplicated handle = %d", handle)
		}
		return 645, nil
	}
	operations.resumeThread = func(windows.Handle) error { return nil }
	operations.terminateJob = func(windows.Handle, uint32) error { return errors.New("injected job termination failure") }
	operations.terminateProcess = func(windows.Handle, uint32) error {
		if allowOriginalCleanup.Load() {
			return nil
		}
		return errors.New("injected process termination failure")
	}
	operations.nativeTerminateProcess = operations.terminateProcess
	operations.waitProcess = func(handle windows.Handle, timeout uint32) (uint32, error) {
		switch handle {
		case 645:
			if timeout != windows.INFINITE {
				t.Fatalf("observer timeout = %d", timeout)
			}
			observerActive.Store(true)
			close(observerStarted)
			<-allowObserverExit
			observerActive.Store(false)
			return windows.WAIT_OBJECT_0, nil
		case 642:
			if !observerActive.Load() {
				rawHandleCollision.Store(true)
			}
			if allowOriginalCleanup.Load() {
				return windows.WAIT_OBJECT_0, nil
			}
			return windows.WAIT_FAILED, errors.New("injected original wait failure")
		default:
			return windows.WAIT_OBJECT_0, nil
		}
	}
	operations.closeHandle = func(handle windows.Handle) error {
		switch handle {
		case 642:
			originalClosed.Store(true)
		case 645:
			if observerCloseAttempts.Add(1) == 1 {
				return errors.New("injected observer close failure")
			}
			observerClosed.Store(true)
		}
		return nil
	}
	runner := &windowsRunner{executable: os.Args[0], operations: operations}
	value, err := runner.Prepare(context.Background(), Spec{}, windowsTestID(67), windowsTestID(68))
	if err != nil {
		t.Fatal(err)
	}
	process := value.(*windowsProcess)
	select {
	case <-observerStarted:
	case <-time.After(time.Second):
		t.Fatal("observer wait did not start")
	}
	started := time.Now()
	if err := process.Close(context.Background()); !errors.Is(err, errProcessHostFailed) {
		t.Fatalf("Close = %v", err)
	}
	if time.Since(started) > 500*time.Millisecond {
		t.Fatalf("Close blocked for %s", time.Since(started))
	}
	select {
	case <-process.Done():
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Done remained blocked")
	}
	allowOriginalCleanup.Store(true)
	deadline := time.Now().Add(time.Second)
	for !originalClosed.Load() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	close(allowObserverExit)
	deadline = time.Now().Add(time.Second)
	for !observerClosed.Load() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if rawHandleCollision.Load() || !originalClosed.Load() || !observerClosed.Load() {
		t.Fatalf("collision=%t originalClosed=%t observerClosed=%t", rawHandleCollision.Load(), originalClosed.Load(), observerClosed.Load())
	}
}

func TestWindowsStartedStatusValidation(t *testing.T) {
	for _, status := range []HostStatus{
		{},
		{Kind: "started", PID: 0, ProcessGroup: 0},
		{Kind: "started", PID: 42, ProcessGroup: 0},
		{Kind: "started", PID: 42, ProcessGroup: 41},
	} {
		if validateWindowsStartedStatus(status) == nil {
			t.Fatalf("accepted status %#v", status)
		}
	}
	if err := validateWindowsStartedStatus(HostStatus{Kind: "started", PID: 42, ProcessGroup: 42}); err != nil {
		t.Fatal(err)
	}
}

func TestWindowsRunnerRedactsExecutableAndNativeErrors(t *testing.T) {
	binary := buildWindowsService(t)
	secret := `C:\private\secret-program.exe`
	process, err := NewRunner(binary).Prepare(context.Background(), Spec{Executable: secret}, windowsTestID(40), windowsTestID(41))
	if err != nil {
		t.Fatal(err)
	}
	defer process.Close(context.Background())
	err = process.Start(context.Background())
	if err == nil || strings.Contains(err.Error(), secret) {
		t.Fatalf("Start error = %v", err)
	}
	operations := defaultWindowsRunnerOperations()
	operations.createProtectedJob = func(uint32) (windows.Handle, error) { return 0, errors.New(secret) }
	runner := &windowsRunner{executable: secret, operations: operations}
	if _, err := runner.Prepare(context.Background(), Spec{}, windowsTestID(42), windowsTestID(43)); err == nil || strings.Contains(err.Error(), secret) {
		t.Fatalf("Prepare error = %v", err)
	}
}

func assertWindowsProcessGone(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	var lastErr error
	var lastExitCode uint32
	for time.Now().Before(deadline) {
		handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
		if errors.Is(err, windows.ERROR_INVALID_PARAMETER) {
			return
		}
		if err == nil {
			_ = windows.GetExitCodeProcess(handle, &lastExitCode)
			_ = windows.CloseHandle(handle)
			if lastExitCode != 259 { // STILL_ACTIVE
				return
			}
		}
		lastErr = err
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("process %d is still present (open error %v, exit code %d)", pid, lastErr, lastExitCode)
}

func windowsHelperSpec(mode string) Spec {
	return Spec{
		Executable: os.Args[0],
		Args:       []string{"-test.run=^TestWindowsHelperProcess$"},
		Env:        []string{windowsHelperModeEnvironment + "=" + mode},
	}
}

func windowsHandleCount(t *testing.T) uint32 {
	t.Helper()
	var count uint32
	procedure := windows.NewLazySystemDLL("kernel32.dll").NewProc("GetProcessHandleCount")
	result, _, callErr := procedure.Call(uintptr(windows.CurrentProcess()), uintptr(unsafe.Pointer(&count)))
	if result == 0 {
		t.Fatal(callErr)
	}
	return count
}

func windowsHandleFlags(handle windows.Handle) (uint32, error) {
	var flags uint32
	procedure := windows.NewLazySystemDLL("kernel32.dll").NewProc("GetHandleInformation")
	result, _, callErr := procedure.Call(uintptr(handle), uintptr(unsafe.Pointer(&flags)))
	if result == 0 {
		return 0, callErr
	}
	return flags, nil
}

type windowsSystemHandleEntry struct {
	Object                uintptr
	ProcessID             uintptr
	Handle                uintptr
	GrantedAccess         uint32
	CreatorBackTraceIndex uint16
	ObjectTypeIndex       uint16
	HandleAttributes      uint32
	Reserved              uint32
}

func windowsHandleSnapshot(t *testing.T) map[uintptr]struct{} {
	t.Helper()
	buffer := make([]byte, 1<<16)
	for {
		var needed uint32
		err := windows.NtQuerySystemInformation(64, unsafe.Pointer(&buffer[0]), uint32(len(buffer)), &needed)
		if err == nil {
			break
		}
		if needed <= uint32(len(buffer)) {
			t.Fatal(err)
		}
		buffer = make([]byte, needed+4096)
	}
	count := *(*uintptr)(unsafe.Pointer(&buffer[0]))
	base := unsafe.Pointer(uintptr(unsafe.Pointer(&buffer[0])) + 2*unsafe.Sizeof(uintptr(0)))
	entries := unsafe.Slice((*windowsSystemHandleEntry)(base), count)
	result := map[uintptr]struct{}{}
	pid := uintptr(os.Getpid())
	for _, entry := range entries {
		if entry.ProcessID == pid {
			result[entry.Handle] = struct{}{}
		}
	}
	return result
}

func windowsNewHandleTypes(t *testing.T, before map[uintptr]struct{}) []string {
	t.Helper()
	after := windowsHandleSnapshot(t)
	result := []string{}
	for handle := range after {
		if _, existed := before[handle]; existed {
			continue
		}
		result = append(result, windowsObjectType(windows.Handle(handle)))
	}
	return result
}

type windowsUnicodeString struct {
	Length        uint16
	MaximumLength uint16
	Buffer        *uint16
}

func windowsObjectType(handle windows.Handle) string {
	procedure := windows.NewLazySystemDLL("ntdll.dll").NewProc("NtQueryObject")
	buffer := make([]byte, 4096)
	result, _, _ := procedure.Call(uintptr(handle), 2, uintptr(unsafe.Pointer(&buffer[0])), uintptr(len(buffer)), 0)
	if result != 0 {
		return fmt.Sprintf("unknown(%#x)", result)
	}
	name := (*windowsUnicodeString)(unsafe.Pointer(&buffer[0]))
	if name.Buffer == nil || name.Length == 0 {
		return "unnamed"
	}
	return windows.UTF16ToString(unsafe.Slice(name.Buffer, int(name.Length/2)))
}

func buildWindowsService(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "unit-test-service.exe")
	command := exec.Command(pinnedGoExecutable(t), "build", "-o", path, "../../cmd/unit-test-service")
	command.Env = append(os.Environ(), "GOWORK="+workspaceFile(t))
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build service: %v\n%s", err, output)
	}
	return path
}

func pinnedGoExecutable(t *testing.T) string {
	t.Helper()
	path, err := filepath.Abs(filepath.Join("..", "..", "..", "..", ".superpowers", "runtime", "go1.26.5", "go", "bin", "go.exe"))
	if err != nil {
		t.Fatal(err)
	}
	return path
}

func workspaceFile(t *testing.T) string {
	t.Helper()
	path, err := filepath.Abs(filepath.Join("..", "..", "..", "..", "go.work"))
	if err != nil {
		t.Fatal(err)
	}
	return path
}

func windowsTestID(index int) string { return fmt.Sprintf("%032x", index) }

func readWindowsChildPID(t *testing.T, output <-chan Output) int {
	t.Helper()
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	var stdout strings.Builder
	for {
		select {
		case item, ok := <-output:
			if !ok {
				t.Fatalf("output closed before child PID: %q", stdout.String())
			}
			if item.Stream != StreamStdout {
				continue
			}
			stdout.Write(item.Data)
			for _, line := range strings.Split(stdout.String(), "\n") {
				if !strings.HasPrefix(line, "CHILD_PID=") {
					continue
				}
				pid, err := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(line, "CHILD_PID=")))
				if err == nil && pid > 0 {
					return pid
				}
			}
		case <-deadline.C:
			t.Fatalf("timed out waiting for child PID: %q", stdout.String())
		}
	}
}

func receiveWindowsResult(t *testing.T, done <-chan Result) Result {
	t.Helper()
	select {
	case result := <-done:
		return result
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for process result")
		return Result{}
	}
}
