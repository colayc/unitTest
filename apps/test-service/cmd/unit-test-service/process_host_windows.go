//go:build windows

package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strconv"
	"syscall"

	"golang.org/x/sys/windows"

	"unit-test-ide.local/test-service/internal/processhost"
)

func init() {
	processHostEntry = func(stdin io.Reader, stdout, stderr io.Writer) int {
		value, err := strconv.ParseUint(os.Getenv(statusHandleEnvironment), 10, 64)
		if err != nil || value == 0 || uintptr(value) != uintptr(windows.Handle(value)) {
			fmt.Fprintln(stderr, "invalid process host status handle")
			return 2
		}
		statusHandle := windows.Handle(value)
		if err := windows.SetHandleInformation(statusHandle, windows.HANDLE_FLAG_INHERIT, 0); err != nil {
			fmt.Fprintln(stderr, "invalid process host status handle")
			return 2
		}
		status := os.NewFile(uintptr(statusHandle), "process-host-status")
		if status == nil {
			fmt.Fprintln(stderr, "invalid process host status handle")
			return 2
		}
		defer status.Close()
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		return processhost.Run(ctx, processhost.NewPlatform(), stdin, status, stdout, stderr)
	}
}
