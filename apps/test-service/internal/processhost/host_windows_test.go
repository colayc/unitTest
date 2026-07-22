//go:build windows

package processhost

import (
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"golang.org/x/sys/windows"

	"unit-test-ide.local/test-service/internal/processcontrol"
)

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
	target := &windowsTarget{process: 501, job: 502, pid: 503, ops: operations, waitDone: make(chan struct{}), cleanupWait: 50 * time.Millisecond}
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
			target := &windowsTarget{process: 511, job: 512, pid: 513, ops: operations, waitDone: make(chan struct{}), cleanupWait: 20 * time.Millisecond}
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
	target := &windowsTarget{process: 521, job: 522, pid: 523, ops: operations, waitDone: make(chan struct{}), cleanupWait: 20 * time.Millisecond}
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
