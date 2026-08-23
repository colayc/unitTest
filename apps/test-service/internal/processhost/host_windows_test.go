//go:build windows

package processhost

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/sys/windows"

	"unit-test-ide.local/test-service/internal/cmake"
	"unit-test-ide.local/test-service/internal/processcontrol"
	"unit-test-ide.local/test-service/internal/winprocess"
)

func TestTargetWindowsEnvironmentAppliesOverlayAndUnset(t *testing.T) {
	t.Setenv("UT_PROCESSHOST_KEEP", "inherited")
	t.Setenv("UT_PROCESSHOST_REMOVE", "private")
	t.Setenv("UT_PROCESSHOST_REPLACE", "old")
	t.Setenv("UNIT_TEST_SERVICE_TOKEN", "service-secret")
	t.Setenv("UTIDE_PRIVATE_VALUE", "service-private")
	environment := targetWindowsEnvironment(
		[]string{
			"UT_PROCESSHOST_REPLACE=new",
			"unit_test_service_token=override-attempt",
		},
		[]string{"ut_processhost_remove"},
	)
	values := make(map[string]string)
	for _, entry := range environment {
		name, value, found := strings.Cut(entry, "=")
		if found {
			values[strings.ToUpper(name)] = value
		}
	}
	if values["UT_PROCESSHOST_KEEP"] != "inherited" ||
		values["UT_PROCESSHOST_REPLACE"] != "new" {
		t.Fatalf("target environment = %#v", values)
	}
	if _, exists := values["UT_PROCESSHOST_REMOVE"]; exists {
		t.Fatalf("unset environment remained = %#v", values)
	}
	if _, exists := values["UNIT_TEST_SERVICE_TOKEN"]; exists {
		t.Fatalf("service token reached target = %#v", values)
	}
	if _, exists := values["UTIDE_PRIVATE_VALUE"]; exists {
		t.Fatalf("service-owned value reached target = %#v", values)
	}
}

func TestWindowsTargetWaitConfirmsInnerJobEmptyBeforeClosingAndReturning(t *testing.T) {
	queries := 0
	closedAfterZero := false
	operations := defaultWindowsTargetOperations()
	operations.waitProcess = func(windows.Handle, uint32) (uint32, error) { return windows.WAIT_OBJECT_0, nil }
	operations.exitCode = func(windows.Handle) (uint32, error) { return 17, nil }
	operations.terminateJob = func(windows.Handle, uint32) error { return nil }
	operations.queryActiveProcesses = func(windows.Handle) (uint32, error) {
		queries++
		if queries == 1 {
			return 1, nil
		}
		return 0, nil
	}
	operations.closeHandle = func(handle windows.Handle) error {
		if handle == 502 {
			closedAfterZero = queries >= 2
		}
		return nil
	}
	target := &windowsTarget{processOwner: winprocess.NewHandleOwner(501, operations.closeHandle), jobOwner: winprocess.NewHandleOwner(502, operations.closeHandle), pid: 503, ops: operations, waitDone: make(chan struct{}), cleanupWait: 50 * time.Millisecond}
	code, err := target.Wait()
	if code != 17 || err != nil {
		t.Fatalf("Wait = (%d, %v)", code, err)
	}
	if !closedAfterZero {
		t.Fatalf("job closed before zero active processes; queries=%d", queries)
	}
}

func TestWindowsTargetActiveCountTimeoutOrErrorReturnsStableWaitFailure(t *testing.T) {
	for _, test := range []struct {
		name  string
		query func(windows.Handle) (uint32, error)
	}{
		{name: "timeout", query: func(windows.Handle) (uint32, error) { return 1, nil }},
		{name: "error", query: func(windows.Handle) (uint32, error) { return 0, errors.New("private native query detail") }},
	} {
		t.Run(test.name, func(t *testing.T) {
			operations := defaultWindowsTargetOperations()
			operations.waitProcess = func(windows.Handle, uint32) (uint32, error) { return windows.WAIT_OBJECT_0, nil }
			operations.exitCode = func(windows.Handle) (uint32, error) { return 0, nil }
			operations.terminateJob = func(windows.Handle, uint32) error { return nil }
			operations.queryActiveProcesses = test.query
			operations.closeHandle = func(windows.Handle) error { return nil }
			target := &windowsTarget{processOwner: winprocess.NewHandleOwner(511, operations.closeHandle), jobOwner: winprocess.NewHandleOwner(512, operations.closeHandle), pid: 513, ops: operations, waitDone: make(chan struct{}), cleanupWait: 20 * time.Millisecond}
			started := time.Now()
			_, err := target.Wait()
			if err == nil || err.Error() != "target job cleanup failed" {
				t.Fatalf("Wait error = %v", err)
			}
			if time.Since(started) > 200*time.Millisecond {
				t.Fatalf("Wait did not release promptly: %s", time.Since(started))
			}
		})
	}
}

func TestWindowsTargetTerminateActiveCountFailureStillReleasesWait(t *testing.T) {
	operations := defaultWindowsTargetOperations()
	operations.waitProcess = func(windows.Handle, uint32) (uint32, error) { return windows.WAIT_OBJECT_0, nil }
	operations.exitCode = func(windows.Handle) (uint32, error) { return 1, nil }
	operations.terminateJob = func(windows.Handle, uint32) error { return errors.New("private terminate detail") }
	operations.queryActiveProcesses = func(windows.Handle) (uint32, error) { return 1, nil }
	operations.closeHandle = func(windows.Handle) error { return nil }
	target := &windowsTarget{processOwner: winprocess.NewHandleOwner(521, operations.closeHandle), jobOwner: winprocess.NewHandleOwner(522, operations.closeHandle), pid: 523, ops: operations, waitDone: make(chan struct{}), cleanupWait: 20 * time.Millisecond}
	platform := newWindowsPlatform(operations)
	done := make(chan error, 1)
	go func() { done <- platform.Terminate(target, 0) }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("Terminate returned nil")
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("Terminate did not release Wait")
	}
}

func TestWindowsTargetAssignmentFailureUsesSharedFallbackBeforeProcessClose(t *testing.T) {
	var mu sync.Mutex
	events := []string{}
	appendEvent := func(value string) {
		mu.Lock()
		defer mu.Unlock()
		events = append(events, value)
	}
	operations := defaultWindowsTargetOperations()
	operations.createProtectedJob = func(uint32) (windows.Handle, error) { return 601, nil }
	operations.createSuspended = func(processcontrol.Spec, windows.Handle, windows.Handle, windows.Handle) (windows.ProcessInformation, error) {
		return windows.ProcessInformation{Process: 602, Thread: 603, ProcessId: 604}, nil
	}
	operations.assignProcess = func(windows.Handle, windows.Handle) error { return errors.New("injected assign failure") }
	operations.terminateProcess = func(windows.Handle, uint32) error {
		appendEvent("terminate")
		return errors.New("injected primary failure")
	}
	operations.nativeTerminateProcess = func(windows.Handle, uint32) error {
		appendEvent("native-terminate")
		return nil
	}
	operations.waitProcess = func(windows.Handle, uint32) (uint32, error) {
		appendEvent("signaled")
		return windows.WAIT_OBJECT_0, nil
	}
	operations.closeHandle = func(handle windows.Handle) error {
		switch handle {
		case 602:
			appendEvent("close-process")
		case 603:
			appendEvent("close-thread")
		}
		return nil
	}
	platform := newWindowsPlatform(operations)
	target, err := platform.Start(processcontrol.Spec{Executable: os.Args[0]}, os.Stdout, os.Stderr)
	if err == nil || target != nil {
		t.Fatalf("Start = (%#v, %v)", target, err)
	}
	mu.Lock()
	defer mu.Unlock()
	want := []string{"close-thread", "terminate", "native-terminate", "signaled", "close-process"}
	if len(events) != len(want) {
		t.Fatalf("events = %#v", events)
	}
	for index := range want {
		if events[index] != want[index] {
			t.Fatalf("events = %#v, want %#v", events, want)
		}
	}
}

func TestWindowsTargetFailsClosedBeforeCreateProcessWhenLaunchRegistrationFails(t *testing.T) {
	registrationCalls := []string{}
	createJobCalled := false
	operations := defaultWindowsTargetOperations()
	operations.registerExecutable = func(path string) error {
		registrationCalls = append(registrationCalls, path)
		if path == `C:\fixture\child.exe` {
			return errors.New("injected registration failure")
		}
		return nil
	}
	operations.createProtectedJob = func(uint32) (windows.Handle, error) {
		createJobCalled = true
		return 0, errors.New("must not create job")
	}
	platform := newWindowsPlatform(operations)
	target, err := platform.Start(processcontrol.Spec{
		Executable: `C:\fixture\cmake.exe`,
		LaunchPlan: []string{`C:\fixture\ninja.exe`, `C:\fixture\child.exe`},
	}, os.Stdout, os.Stderr)
	if err == nil || target != nil {
		t.Fatalf("Start() = (%#v, %v), want fail-closed registration error", target, err)
	}
	if createJobCalled {
		t.Fatal("native job/CreateProcess path ran after registration failure")
	}
	want := []string{`C:\fixture\cmake.exe`, `C:\fixture\ninja.exe`, `C:\fixture\child.exe`}
	if !reflect.DeepEqual(registrationCalls, want) {
		t.Fatalf("registration calls = %#v, want %#v", registrationCalls, want)
	}
}

func TestWindowsTargetDefersMissingDerivedArtifactRegistration(t *testing.T) {
	registrationCalls := []string{}
	operations := defaultWindowsTargetOperations()
	operations.registerExecutable = func(path string) error {
		registrationCalls = append(registrationCalls, path)
		return nil
	}
	operations.createProtectedJob = func(uint32) (windows.Handle, error) {
		return 0, errors.New("stop after registration")
	}
	_, err := newWindowsPlatform(operations).Start(processcontrol.Spec{
		Executable: `C:\fixture\cmake.exe`,
		LaunchPlan: []string{`C:\fixture\build\coverage-tests.exe`},
		Args:       []string{"--build", `C:\fixture\build`},
		Dir:        `C:\fixture`,
	}, os.Stdout, os.Stderr)
	if err == nil {
		t.Fatal("Start() succeeded, want injected job failure")
	}
	want := []string{`C:\fixture\cmake.exe`}
	if !reflect.DeepEqual(registrationCalls, want) {
		t.Fatalf("registrationCalls = %#v, want %#v", registrationCalls, want)
	}
}

func TestWindowsTargetRechecksPinnedCMakeGraphBeforeCreateProcess(t *testing.T) {
	path := filepath.Join(t.TempDir(), "CMakeLists.txt")
	if err := os.WriteFile(path, []byte("project(before)\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	state, _, err := cmake.SnapshotLaunchInput(path, 512*1024)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("execute_process(COMMAND unknown.exe)\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	createJobCalled := false
	operations := defaultWindowsTargetOperations()
	operations.registerExecutable = func(string) error { return nil }
	operations.createProtectedJob = func(uint32) (windows.Handle, error) {
		createJobCalled = true
		return 0, errors.New("must not create job")
	}
	target, err := newWindowsPlatform(operations).Start(processcontrol.Spec{
		Executable: os.Args[0], LaunchInputs: []cmake.FingerprintFile{state},
	}, os.Stdout, os.Stderr)
	if err == nil || target != nil {
		t.Fatalf("Start() = (%#v, %v), want changed source graph rejection", target, err)
	}
	if createJobCalled {
		t.Fatal("CreateProcess path ran after CMake graph changed")
	}
}

func TestWindowsTargetRetainsCMakeGraphAcrossVerifyToCreateProcessWindow(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "CMakeLists.txt")
	replacement := filepath.Join(directory, "replacement.cmake")
	original := []byte("project(original)\n")
	malicious := []byte("execute_process(COMMAND unknown.exe)\n")
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(replacement, malicious, 0o600); err != nil {
		t.Fatal(err)
	}
	state, _, err := cmake.SnapshotLaunchInput(path, 512*1024)
	if err != nil {
		t.Fatal(err)
	}
	createJobCalled := false
	writeBlocked := false
	replaceBlocked := false
	operations := defaultWindowsTargetOperations()
	operations.registerExecutable = func(string) error { return nil }
	operations.afterLaunchInputs = func() error {
		writeBlocked = os.WriteFile(path, malicious, 0o600) != nil
		replaceBlocked = os.Rename(replacement, path) != nil
		return errors.New("injected mutation attempt")
	}
	operations.createProtectedJob = func(uint32) (windows.Handle, error) {
		createJobCalled = true
		return 0, errors.New("must not create job")
	}
	target, err := newWindowsPlatform(operations).Start(processcontrol.Spec{
		Executable: os.Args[0], LaunchInputs: []cmake.FingerprintFile{state},
	}, os.Stdout, os.Stderr)
	if err == nil || target != nil {
		t.Fatalf("Start() = (%#v, %v), want mutation-window failure", target, err)
	}
	if !writeBlocked || !replaceBlocked || createJobCalled {
		t.Fatalf("writeBlocked=%v replaceBlocked=%v createJobCalled=%v", writeBlocked, replaceBlocked, createJobCalled)
	}
	got, readErr := os.ReadFile(path)
	if readErr != nil || !bytes.Equal(got, original) {
		t.Fatalf("original input changed: data=%q err=%v", got, readErr)
	}
	gotReplacement, readErr := os.ReadFile(replacement)
	if readErr != nil || !bytes.Equal(gotReplacement, malicious) {
		t.Fatalf("replacement changed: data=%q err=%v", gotReplacement, readErr)
	}
}

func TestWindowsInnerJobCloseRetriesAfterFailure(t *testing.T) {
	var attempts atomic.Int32
	var successes atomic.Int32
	operations := defaultWindowsTargetOperations()
	operations.terminateJob = func(windows.Handle, uint32) error { return nil }
	operations.queryActiveProcesses = func(windows.Handle) (uint32, error) { return 0, nil }
	operations.closeHandle = func(handle windows.Handle) error {
		if handle != 701 {
			return nil
		}
		if attempts.Add(1) == 1 {
			return errors.New("injected job close failure")
		}
		successes.Add(1)
		return nil
	}
	target := &windowsTarget{processOwner: winprocess.NewHandleOwner(702, operations.closeHandle), jobOwner: winprocess.NewHandleOwner(701, operations.closeHandle), pid: 703, ops: operations, waitDone: make(chan struct{}), cleanupWait: time.Millisecond}
	if err := target.closeJob(); err == nil {
		t.Fatal("first close unexpectedly succeeded")
	}
	if err := target.closeJob(); err != nil {
		t.Fatalf("second close = %v", err)
	}
	if got := attempts.Load(); got != 2 {
		t.Fatalf("close attempts = %d", got)
	}
	if got := successes.Load(); got != 1 {
		t.Fatalf("successful closes = %d", got)
	}
	if target.jobOwner.Open() {
		t.Fatal("inner Job handle ownership was not released")
	}
}

func TestWindowsTargetWaitRetriesProcessClose(t *testing.T) {
	var attempts atomic.Int32
	var successes atomic.Int32
	operations := defaultWindowsTargetOperations()
	operations.waitProcess = func(windows.Handle, uint32) (uint32, error) { return windows.WAIT_OBJECT_0, nil }
	operations.exitCode = func(windows.Handle) (uint32, error) { return 0, nil }
	operations.terminateJob = func(windows.Handle, uint32) error { return nil }
	operations.queryActiveProcesses = func(windows.Handle) (uint32, error) { return 0, nil }
	operations.closeHandle = func(handle windows.Handle) error {
		if handle != 712 {
			return nil
		}
		if attempts.Add(1) == 1 {
			return errors.New("injected process close failure")
		}
		successes.Add(1)
		return nil
	}
	target := &windowsTarget{processOwner: winprocess.NewHandleOwner(712, operations.closeHandle), jobOwner: winprocess.NewHandleOwner(711, operations.closeHandle), pid: 713, ops: operations, waitDone: make(chan struct{}), cleanupWait: time.Millisecond}
	if _, err := target.Wait(); err != nil {
		t.Fatal(err)
	}
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
	if target.processOwner.Open() {
		t.Fatal("target process handle ownership was not released")
	}
}

func TestWindowsRealTargetOwnerCleanup(t *testing.T) {
	operations := defaultWindowsTargetOperations()
	originalCreateJob := operations.createProtectedJob
	originalCreate := operations.createSuspended
	originalTerminateJob := operations.terminateJob
	originalQuery := operations.queryActiveProcesses
	originalClose := operations.closeHandle
	var mu sync.Mutex
	events := []string{}
	appendEvent := func(format string, values ...any) {
		mu.Lock()
		defer mu.Unlock()
		events = append(events, fmt.Sprintf(format, values...))
	}
	var job windows.Handle
	var process windows.Handle
	operations.createProtectedJob = func(flags uint32) (windows.Handle, error) {
		value, err := originalCreateJob(flags)
		job = value
		return value, err
	}
	operations.createSuspended = func(spec processcontrol.Spec, stdin, stdout, stderr windows.Handle) (windows.ProcessInformation, error) {
		info, err := originalCreate(spec, stdin, stdout, stderr)
		process = info.Process
		return info, err
	}
	operations.terminateJob = func(handle windows.Handle, code uint32) error {
		err := originalTerminateJob(handle, code)
		appendEvent("terminate(%d)=%v", handle, err)
		return err
	}
	operations.queryActiveProcesses = func(handle windows.Handle) (uint32, error) {
		active, err := originalQuery(handle)
		appendEvent("query(%d)=(%d,%v)", handle, active, err)
		return active, err
	}
	operations.closeHandle = func(handle windows.Handle) error {
		err := originalClose(handle)
		if handle == job || handle == process {
			appendEvent("close(%d)=%v", handle, err)
		}
		return err
	}
	platform := newWindowsPlatform(operations)
	platform.cleanupWait = 100 * time.Millisecond
	target, err := platform.Start(processcontrol.Spec{Executable: os.Getenv("ComSpec"), Args: []string{"/c", "exit", "17"}}, os.Stdout, os.Stderr)
	if err != nil {
		t.Fatal(err)
	}
	code, err := target.Wait()
	if err != nil || code != 17 {
		mu.Lock()
		defer mu.Unlock()
		t.Fatalf("Wait=(%d,%v), events=%v", code, err, events)
	}
}

func TestWindowsTargetTerminateAfterNaturalWaitIsIdempotent(t *testing.T) {
	operations := defaultWindowsTargetOperations()
	operations.waitProcess = func(windows.Handle, uint32) (uint32, error) { return windows.WAIT_OBJECT_0, nil }
	operations.exitCode = func(windows.Handle) (uint32, error) { return 17, nil }
	operations.terminateJob = func(windows.Handle, uint32) error { return nil }
	operations.queryActiveProcesses = func(windows.Handle) (uint32, error) { return 0, nil }
	operations.closeHandle = func(windows.Handle) error { return nil }
	target := &windowsTarget{
		processOwner: winprocess.NewHandleOwner(721, operations.closeHandle),
		jobOwner:     winprocess.NewHandleOwner(722, operations.closeHandle),
		pid:          723,
		ops:          operations,
		waitDone:     make(chan struct{}),
		cleanupWait:  time.Millisecond,
	}
	if code, err := target.Wait(); code != 17 || err != nil {
		t.Fatalf("Wait = (%d, %v)", code, err)
	}
	if err := newWindowsPlatform(operations).Terminate(target, 0); err != nil {
		t.Fatalf("Terminate after Wait = %v", err)
	}
}
