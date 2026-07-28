//go:build linux

package probe

import (
	"errors"
	"syscall"
	"time"
)

func waitTestProcessGone(pid int, maximum time.Duration) bool {
	deadline := time.Now().Add(maximum)
	for {
		err := syscall.Kill(pid, 0)
		if errors.Is(err, syscall.ESRCH) {
			return true
		}
		if !time.Now().Before(deadline) {
			return false
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func terminateTestProcess(pid int) {
	_ = syscall.Kill(pid, syscall.SIGKILL)
}
