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
		control, err := newInterruptibleProcessHostControl(stdin)
		if err != nil {
			fmt.Fprintln(stderr, "invalid process host control")
			return 2
		}
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		return processhost.Run(ctx, processhost.NewPlatform(), control, status, stdout, stderr)
	}
}

func newInterruptibleProcessHostControl(reader io.Reader) (*os.File, error) {
	file, ok := reader.(*os.File)
	if !ok {
		return nil, os.ErrInvalid
	}
	fd, err := unix.Dup(int(file.Fd()))
	if err != nil {
		return nil, err
	}
	unix.CloseOnExec(fd)
	if err := unix.SetNonblock(fd, true); err != nil {
		_ = unix.Close(fd)
		return nil, err
	}
	control := os.NewFile(uintptr(fd), "process-host-control")
	if control == nil {
		_ = unix.Close(fd)
		return nil, os.ErrInvalid
	}
	return control, nil
}
