//go:build linux

package probe

import (
	"errors"
	"io"
	"os"
	"reflect"
	"testing"

	"golang.org/x/sys/unix"
)

func TestMain(m *testing.M) {
	if len(os.Args) == 2 && os.Args[1] == probeSupervisorArgument {
		status := os.NewFile(supervisorStatusFD, "probe-supervisor-status")
		if status == nil {
			os.Exit(2)
		}
		os.Exit(RunSupervisor(os.Stdin, status, os.Stdout, os.Stderr))
	}
	os.Exit(m.Run())
}

func TestLinuxSupervisorPinsPidfdBeforeNegativeProcessGroupSignal(t *testing.T) {
	var events []string
	operations := linuxSupervisorOperations{
		pidfdSendSignal: func(pidfd int, signal unix.Signal, info *unix.Siginfo, flags int) error {
			events = append(events, "pidfd-stop")
			if pidfd != 17 || signal != unix.SIGSTOP || info != nil || flags != 0 {
				t.Fatalf("pidfd signal = (%d, %v, %#v, %d)", pidfd, signal, info, flags)
			}
			return nil
		},
		kill: func(pid int, signal unix.Signal) error {
			events = append(events, "group-kill")
			if pid != -23 || signal != unix.SIGKILL {
				t.Fatalf("group signal = (%d, %v)", pid, signal)
			}
			return nil
		},
	}

	if err := terminateLinuxSupervisorGroup(17, 23, operations); err != nil {
		t.Fatalf("terminate group: %v", err)
	}
	if !reflect.DeepEqual(events, []string{"pidfd-stop", "group-kill"}) {
		t.Fatalf("events = %#v", events)
	}
}

func TestLinuxSupervisorPinFailureNeverSignalsNumericProcessGroup(t *testing.T) {
	pinFailure := errors.New("pidfd no longer names a live supervisor")
	groupSignals := 0
	operations := linuxSupervisorOperations{
		pidfdSendSignal: func(int, unix.Signal, *unix.Siginfo, int) error {
			return pinFailure
		},
		kill: func(int, unix.Signal) error {
			groupSignals++
			return nil
		},
	}

	err := terminateLinuxSupervisorGroup(17, 23, operations)
	if !errors.Is(err, pinFailure) {
		t.Fatalf("error = %v, want pin failure", err)
	}
	if groupSignals != 0 {
		t.Fatalf("numeric group signals = %d, want 0", groupSignals)
	}
}

func TestLinuxSupervisorEntryRejectsNonLeaderInvocation(t *testing.T) {
	if os.Getpid() == unix.Getpgrp() {
		t.Skip("test process unexpectedly owns its process group")
	}
	if code := RunSupervisor(
		io.NopCloser(nilReader{}),
		io.Discard,
		io.Discard,
		io.Discard,
	); code != 2 {
		t.Fatalf("code = %d, want 2", code)
	}
}

type nilReader struct{}

func (nilReader) Read([]byte) (int, error) { return 0, io.EOF }
