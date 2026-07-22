//go:build linux

package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strconv"
	"syscall"

	"golang.org/x/sys/unix"

	"unit-test-ide.local/test-service/internal/processhost"
)

func init() {
	processHostEntry = func(stdin io.Reader, stdout, stderr io.Writer) int {
		statusFD, err := strconv.Atoi(os.Getenv(statusHandleEnvironment))
		if err != nil || statusFD != 3 {
			fmt.Fprintln(stderr, "invalid process host status handle")
			return 2
		}
		status := os.NewFile(uintptr(statusFD), "process-host-status")
		if status == nil {
			fmt.Fprintln(stderr, "invalid process host status handle")
			return 2
		}
		unix.CloseOnExec(statusFD)
		defer status.Close()
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		return processhost.Run(ctx, processhost.NewPlatform(), stdin, status, stdout, stderr)
	}
}
