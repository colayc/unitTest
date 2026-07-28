//go:build windows

package probe

import (
	"errors"
	"time"

	"golang.org/x/sys/windows"
)

func waitTestProcessGone(pid int, maximum time.Duration) bool {
	deadline := time.Now().Add(maximum)
	for {
		handle, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(pid))
		if errors.Is(err, windows.ERROR_INVALID_PARAMETER) {
			return true
		}
		if err == nil {
			result, waitErr := windows.WaitForSingleObject(handle, 0)
			_ = windows.CloseHandle(handle)
			if waitErr == nil && result == windows.WAIT_OBJECT_0 {
				return true
			}
		}
		if !time.Now().Before(deadline) {
			return false
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func terminateTestProcess(pid int) {
	handle, err := windows.OpenProcess(windows.PROCESS_TERMINATE, false, uint32(pid))
	if err != nil {
		return
	}
	_ = windows.TerminateProcess(handle, 1)
	_ = windows.CloseHandle(handle)
}
