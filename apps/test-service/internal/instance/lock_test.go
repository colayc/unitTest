package instance

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

const lockHelperEnvironment = "UNIT_TEST_INSTANCE_LOCK_HELPER"

func TestLockHelperProcess(t *testing.T) {
	path := os.Getenv(lockHelperEnvironment)
	if path == "" {
		return
	}
	locked, err := Lock(path)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Println("LOCKED")
	_, _ = io.Copy(io.Discard, os.Stdin)
	if err := locked.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestLockRejectsSecondProcessAndCanBeReacquired(t *testing.T) {
	path := filepath.Join(t.TempDir(), "service.lock")
	command := exec.Command(os.Args[0], "-test.run=^TestLockHelperProcess$")
	command.Env = append(os.Environ(), lockHelperEnvironment+"="+path)
	stdin, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	waited := false
	defer func() {
		_ = stdin.Close()
		if !waited {
			_ = command.Wait()
		}
	}()
	line, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil || line != "LOCKED\n" {
		t.Fatalf("helper readiness = %q, %v", line, err)
	}

	second, err := Lock(path)
	if second != nil {
		_ = second.Close()
	}
	if !errors.Is(err, ErrAlreadyRunning) {
		t.Fatalf("second Lock() error = %v, want ErrAlreadyRunning", err)
	}
	if err := stdin.Close(); err != nil {
		t.Fatal(err)
	}
	if err := command.Wait(); err != nil {
		t.Fatal(err)
	}
	waited = true

	reacquired, err := Lock(path)
	if err != nil {
		t.Fatalf("reacquire Lock() error = %v", err)
	}
	if err := reacquired.Close(); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(path); err != nil || !info.Mode().IsRegular() {
		t.Fatalf("lock file after Close = %#v, %v", info, err)
	}
}
