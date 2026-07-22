//go:build windows

package winprocess

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func TestFailCreatedProcessUsesNativeFallbackAndClosesAfterSignal(t *testing.T) {
	var eventsMu sync.Mutex
	events := []string{}
	appendEvent := func(value string) {
		eventsMu.Lock()
		defer eventsMu.Unlock()
		events = append(events, value)
	}
	operations := Operations{
		Terminate: func(windows.Handle, uint32) error {
			appendEvent("terminate")
			return errors.New("injected primary failure")
		},
		NativeTerminate: func(windows.Handle, uint32) error {
			appendEvent("native-terminate")
			return nil
		},
		Wait: func(windows.Handle, uint32) (uint32, error) {
			appendEvent("signaled")
			return windows.WAIT_OBJECT_0, nil
		},
		Close: func(handle windows.Handle) error {
			if handle == 11 {
				appendEvent("close-thread")
			} else {
				appendEvent("close-process")
			}
			return nil
		},
	}
	if err := FailCreatedProcess(10, 11, operations, 20*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	eventsMu.Lock()
	defer eventsMu.Unlock()
	want := []string{"close-thread", "terminate", "native-terminate", "signaled", "close-process"}
	if len(events) != len(want) {
		t.Fatalf("events = %#v, want %#v", events, want)
	}
	for index := range want {
		if events[index] != want[index] {
			t.Fatalf("events = %#v, want %#v", events, want)
		}
	}
}

func TestFailCreatedProcessWaitTimeoutOrErrorEscalatesToNativeFallback(t *testing.T) {
	for _, test := range []struct {
		name      string
		firstWait func(windows.Handle, uint32) (uint32, error)
	}{
		{name: "timeout", firstWait: func(windows.Handle, uint32) (uint32, error) { return uint32(windows.WAIT_TIMEOUT), nil }},
		{name: "error", firstWait: func(windows.Handle, uint32) (uint32, error) {
			return windows.WAIT_FAILED, errors.New("injected wait failure")
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			waits := 0
			fallbacks := 0
			closed := false
			operations := Operations{
				Terminate:       func(windows.Handle, uint32) error { return nil },
				NativeTerminate: func(windows.Handle, uint32) error { fallbacks++; return nil },
				Wait: func(handle windows.Handle, timeout uint32) (uint32, error) {
					waits++
					if waits == 1 {
						return test.firstWait(handle, timeout)
					}
					return windows.WAIT_OBJECT_0, nil
				},
				Close: func(handle windows.Handle) error {
					if handle == 20 {
						closed = true
					}
					return nil
				},
			}
			if err := FailCreatedProcess(20, 21, operations, 20*time.Millisecond); err != nil {
				t.Fatal(err)
			}
			if fallbacks != 1 || !closed || waits != 2 {
				t.Fatalf("fallbacks=%d waits=%d closed=%t", fallbacks, waits, closed)
			}
		})
	}
}

func TestFailCreatedProcessRetainsOwnershipUntilBackgroundRetrySignals(t *testing.T) {
	var allowCleanup atomic.Bool
	var processClosed atomic.Bool
	var closeBeforeSignal atomic.Bool
	operations := Operations{
		Terminate: func(windows.Handle, uint32) error {
			if allowCleanup.Load() {
				return nil
			}
			return errors.New("injected primary failure")
		},
		NativeTerminate: func(windows.Handle, uint32) error {
			if allowCleanup.Load() {
				return nil
			}
			return errors.New("injected fallback failure")
		},
		Wait: func(windows.Handle, uint32) (uint32, error) {
			if allowCleanup.Load() {
				return windows.WAIT_OBJECT_0, nil
			}
			return uint32(windows.WAIT_TIMEOUT), nil
		},
		Close: func(handle windows.Handle) error {
			if handle == 30 {
				if !allowCleanup.Load() {
					closeBeforeSignal.Store(true)
				}
				processClosed.Store(true)
			}
			return nil
		},
	}
	started := time.Now()
	err := FailCreatedProcess(30, 31, operations, 20*time.Millisecond)
	if !errors.Is(err, ErrCleanupFailed) {
		t.Fatalf("error = %v", err)
	}
	if elapsed := time.Since(started); elapsed > 250*time.Millisecond {
		t.Fatalf("cleanup did not fail promptly: %s", elapsed)
	}
	if processClosed.Load() {
		t.Fatal("process handle closed before a signaled wait")
	}
	allowCleanup.Store(true)
	deadline := time.Now().Add(time.Second)
	for !processClosed.Load() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if !processClosed.Load() || closeBeforeSignal.Load() {
		t.Fatalf("processClosed=%t closeBeforeSignal=%t", processClosed.Load(), closeBeforeSignal.Load())
	}
}

func TestFailCreatedProcessRetainsHandleWhenCloseFailsAfterSignal(t *testing.T) {
	var closeAttempts atomic.Int32
	var closed atomic.Bool
	operations := Operations{
		Terminate:       func(windows.Handle, uint32) error { return nil },
		NativeTerminate: func(windows.Handle, uint32) error { return nil },
		Wait:            func(windows.Handle, uint32) (uint32, error) { return windows.WAIT_OBJECT_0, nil },
		Close: func(handle windows.Handle) error {
			if handle != 40 {
				return nil
			}
			if closeAttempts.Add(1) == 1 {
				return errors.New("injected close failure")
			}
			closed.Store(true)
			return nil
		},
	}
	if err := FailCreatedProcess(40, 41, operations, 20*time.Millisecond); !errors.Is(err, ErrCleanupFailed) {
		t.Fatalf("error = %v", err)
	}
	deadline := time.Now().Add(time.Second)
	for !closed.Load() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if !closed.Load() {
		t.Fatal("failed process-handle close was not retried by an owner")
	}
}

func TestFailCreatedProcessBackgroundReaperRetriesCloseAfterSignal(t *testing.T) {
	var allowSignal atomic.Bool
	var closeAttempts atomic.Int32
	var closed atomic.Bool
	operations := Operations{
		Terminate:       func(windows.Handle, uint32) error { return errors.New("injected termination failure") },
		NativeTerminate: func(windows.Handle, uint32) error { return errors.New("injected native termination failure") },
		Wait: func(windows.Handle, uint32) (uint32, error) {
			if allowSignal.Load() {
				return windows.WAIT_OBJECT_0, nil
			}
			return windows.WAIT_FAILED, errors.New("injected wait failure")
		},
		Close: func(handle windows.Handle) error {
			if handle != 50 {
				return nil
			}
			if closeAttempts.Add(1) == 1 {
				return errors.New("injected close failure")
			}
			closed.Store(true)
			return nil
		},
	}
	if err := FailCreatedProcess(50, 51, operations, 0); !errors.Is(err, ErrCleanupFailed) {
		t.Fatalf("error = %v", err)
	}
	allowSignal.Store(true)
	deadline := time.Now().Add(time.Second)
	for !closed.Load() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if !closed.Load() || closeAttempts.Load() < 2 {
		t.Fatalf("closed=%t close attempts=%d", closed.Load(), closeAttempts.Load())
	}
}
