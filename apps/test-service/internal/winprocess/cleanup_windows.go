//go:build windows

package winprocess

import (
	"errors"
	"time"

	"golang.org/x/sys/windows"
)

var ErrCleanupFailed = errors.New("created process cleanup failed")

type Operations struct {
	Terminate       func(windows.Handle, uint32) error
	NativeTerminate func(windows.Handle, uint32) error
	Wait            func(windows.Handle, uint32) (uint32, error)
	Close           func(windows.Handle) error
}

func DefaultOperations() Operations {
	return Operations{
		Terminate:       windows.TerminateProcess,
		NativeTerminate: NativeTerminateProcess,
		Wait:            windows.WaitForSingleObject,
		Close:           windows.CloseHandle,
	}
}

// FailCreatedProcess takes sole ownership of the process and thread handles.
// It closes the process handle only after a successful signaled wait. If the
// synchronous deadline cannot prove that state, an owned background reaper
// retains the process handle and retries until it can close it safely.
func FailCreatedProcess(process, thread windows.Handle, operations Operations, wait time.Duration) error {
	if process == 0 || process == windows.InvalidHandle || thread == 0 || thread == windows.InvalidHandle || !validOperations(operations) {
		return ErrCleanupFailed
	}
	threadErr := operations.Close(thread)
	if threadErr != nil {
		go retryCloseHandle(thread, operations.Close)
	}
	cleanupErr := CleanupProcess(process, operations, wait)
	if cleanupErr != nil || threadErr != nil {
		return ErrCleanupFailed
	}
	return nil
}

// CleanupProcess takes sole ownership of a process handle. The handle is
// closed synchronously only after a signaled wait, or retained by one reaper.
func CleanupProcess(process windows.Handle, operations Operations, wait time.Duration) error {
	if process == 0 || process == windows.InvalidHandle || !validOperations(operations) {
		return ErrCleanupFailed
	}
	if cleanupCreatedProcess(process, operations, wait) {
		if err := operations.Close(process); err != nil {
			go retryCloseHandle(process, operations.Close)
			return ErrCleanupFailed
		}
		return nil
	}
	go reapCreatedProcess(process, operations)
	return ErrCleanupFailed
}

func retryCloseHandle(handle windows.Handle, closeHandle func(windows.Handle) error) {
	for {
		if err := closeHandle(handle); err == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func validOperations(operations Operations) bool {
	return operations.Terminate != nil && operations.NativeTerminate != nil && operations.Wait != nil && operations.Close != nil
}

func cleanupCreatedProcess(process windows.Handle, operations Operations, wait time.Duration) bool {
	if wait < 0 {
		wait = 0
	}
	if err := operations.Terminate(process, 1); err == nil && waitCreatedProcess(process, operations.Wait, wait) {
		return true
	}
	_ = operations.NativeTerminate(process, 1)
	return waitCreatedProcess(process, operations.Wait, wait)
}

func waitCreatedProcess(process windows.Handle, wait func(windows.Handle, uint32) (uint32, error), duration time.Duration) bool {
	milliseconds := duration.Milliseconds()
	if duration > 0 && milliseconds == 0 {
		milliseconds = 1
	}
	if milliseconds > int64(windows.INFINITE-1) {
		milliseconds = int64(windows.INFINITE - 1)
	}
	result, err := wait(process, uint32(milliseconds))
	return err == nil && result == windows.WAIT_OBJECT_0
}

func reapCreatedProcess(process windows.Handle, operations Operations) {
	for {
		_ = operations.Terminate(process, 1)
		_ = operations.NativeTerminate(process, 1)
		if waitCreatedProcess(process, operations.Wait, 100*time.Millisecond) {
			retryCloseHandle(process, operations.Close)
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
}

var ntdll = windows.NewLazySystemDLL("ntdll.dll")
var ntTerminateProcess = ntdll.NewProc("NtTerminateProcess")

func NativeTerminateProcess(process windows.Handle, exitCode uint32) error {
	status, _, _ := ntTerminateProcess.Call(uintptr(process), uintptr(exitCode))
	if status != 0 {
		return errors.New("native process termination failed")
	}
	return nil
}
