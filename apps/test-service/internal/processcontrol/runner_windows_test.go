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
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	"unit-test-ide.local/test-service/internal/task"
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
	defer process.Close()
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
	defer process.Close()
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
}

func TestWindowsPreparedParentPipeHandlesAreNotInheritable(t *testing.T) {
	binary := buildWindowsService(t)
	value, err := NewRunner(binary).Prepare(context.Background(), Spec{Executable: binary, Args: []string{"--task-fixture", "success"}}, windowsTestID(51), windowsTestID(52))
	if err != nil {
		t.Fatal(err)
	}
	defer value.Close()
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

func TestWindowsTerminateErrorStillReleasesDoneAndKillsTree(t *testing.T) {
	binary := buildWindowsService(t)
	operations := defaultWindowsRunnerOperations()
	operations.terminateJob = func(windows.Handle, uint32) error { return errors.New("injected terminate failure") }
	runner := &windowsRunner{executable: binary, operations: operations}
	process, err := runner.Prepare(context.Background(), Spec{Executable: binary, Args: []string{"--task-fixture", "spawn-child"}}, windowsTestID(11), windowsTestID(12))
	if err != nil {
		t.Fatal(err)
	}
	defer process.Close()
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
		{name: "close", stop: func(_ context.CancelFunc, process Process) error { return process.Close() }},
	} {
		t.Run(test.name, func(t *testing.T) {
			binary := buildWindowsService(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			process, err := NewRunner(binary).Prepare(ctx, Spec{Executable: binary, Args: []string{"--task-fixture", "spawn-child"}}, windowsTestID(13), windowsTestID(14))
			if err != nil {
				t.Fatal(err)
			}
			defer process.Close()
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
		{name: "Host exit", stop: func(process *windowsProcess) error { return windows.TerminateProcess(process.host, 1) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			binary := buildWindowsService(t)
			value, err := NewRunner(binary).Prepare(context.Background(), Spec{Executable: binary, Args: []string{"--task-fixture", "spawn-child"}}, windowsTestID(55), windowsTestID(56))
			if err != nil {
				t.Fatal(err)
			}
			defer value.Close()
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
	defer process.Close()
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
	if err := process.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestWindowsTargetDoesNotReceiveStatusHandleEnvironment(t *testing.T) {
	binary := buildWindowsService(t)
	process, err := NewRunner(binary).Prepare(context.Background(), windowsHelperSpec("report-inheritance"), windowsTestID(17), windowsTestID(18))
	if err != nil {
		t.Fatal(err)
	}
	defer process.Close()
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
	defer process.Close()
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
		if err := process.Close(); err != nil {
			t.Fatal(err)
		}
		if err := process.Close(); err != nil {
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
	defer process.Close()
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
