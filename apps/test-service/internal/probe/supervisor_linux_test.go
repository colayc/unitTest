//go:build linux

package probe

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"reflect"
	"testing"

	"golang.org/x/sys/unix"
)

const probeExactEnvironmentHelperArgument = "--probe-exact-environment-helper"

func TestMain(m *testing.M) {
	if len(os.Args) == 2 && os.Args[1] == probeSupervisorArgument {
		status := os.NewFile(supervisorStatusFD, "probe-supervisor-status")
		if status == nil {
			os.Exit(2)
		}
		os.Exit(RunSupervisor(os.Stdin, status, os.Stdout, os.Stderr))
	}
	if len(os.Args) == 2 && os.Args[1] == probeExactEnvironmentHelperArgument {
		directory, err := os.Getwd()
		if err != nil {
			os.Exit(2)
		}
		if err := json.NewEncoder(os.Stdout).Encode(struct {
			Environment []string `json:"environment"`
			Directory   string   `json:"directory"`
		}{
			Environment: os.Environ(),
			Directory:   directory,
		}); err != nil {
			os.Exit(2)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestLinuxSupervisedTargetPreservesExactEnvironmentWithWorkingDirectory(t *testing.T) {
	tests := []struct {
		name        string
		environment []string
	}{
		{name: "empty", environment: []string{}},
		{
			name:        "explicit PWD",
			environment: []string{"PWD=/must-not-be-rewritten", "UNIT_TEST_IDE_PROBE_VISIBLE=explicit"},
		},
		{
			name: "ordinary variables preserve order",
			environment: []string{
				"UNIT_TEST_IDE_PROBE_SECOND=two",
				"UNIT_TEST_IDE_PROBE_FIRST=one",
			},
		},
	}

	executable, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable() error = %v", err)
	}
	directory := t.TempDir()
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			stdoutReader, stdoutWriter, err := os.Pipe()
			if err != nil {
				t.Fatalf("stdout pipe: %v", err)
			}
			stderrReader, stderrWriter, err := os.Pipe()
			if err != nil {
				_ = stdoutReader.Close()
				_ = stdoutWriter.Close()
				t.Fatalf("stderr pipe: %v", err)
			}
			t.Cleanup(func() {
				_ = stdoutReader.Close()
				_ = stdoutWriter.Close()
				_ = stderrReader.Close()
				_ = stderrWriter.Close()
			})

			target, err := startLinuxSupervisedTarget(Spec{
				Executable: executable,
				Args:       []string{probeExactEnvironmentHelperArgument},
				Env:        append([]string{}, test.environment...),
				Dir:        directory,
			}, stdoutWriter, stderrWriter)
			if err != nil {
				t.Fatalf("start target: %v", err)
			}
			exitCode, err := target.Wait()
			if err != nil || exitCode != 0 {
				t.Fatalf("wait target = (%d, %v)", exitCode, err)
			}
			if err := stdoutWriter.Close(); err != nil {
				t.Fatalf("close stdout writer: %v", err)
			}
			if err := stderrWriter.Close(); err != nil {
				t.Fatalf("close stderr writer: %v", err)
			}

			var result struct {
				Environment []string `json:"environment"`
				Directory   string   `json:"directory"`
			}
			if err := json.NewDecoder(stdoutReader).Decode(&result); err != nil {
				t.Fatalf("decode target output: %v", err)
			}
			if !reflect.DeepEqual(result.Environment, test.environment) {
				t.Fatalf("environment = %#v, want %#v", result.Environment, test.environment)
			}
			if result.Directory != directory {
				t.Fatalf("directory = %q, want %q", result.Directory, directory)
			}
			stderr, err := io.ReadAll(stderrReader)
			if err != nil {
				t.Fatalf("read stderr: %v", err)
			}
			if len(stderr) != 0 {
				t.Fatalf("stderr = %q, want empty", stderr)
			}
		})
	}
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
