package probe

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestRunnerCapturesSeparateStreamsAndExitCode(t *testing.T) {
	result, err := NewRunner().Run(context.Background(), helperSpec(t, "success"))
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0", result.ExitCode)
	}
	if got := string(result.Stdout); got != "stdout-marker" {
		t.Fatalf("Stdout = %q", got)
	}
	if got := string(result.Stderr); got != "stderr-marker" {
		t.Fatalf("Stderr = %q", got)
	}
}

func TestRunnerRejectsExecutableThatCouldSearchPath(t *testing.T) {
	_, err := NewRunner().Run(context.Background(), Spec{Executable: "cmake"})
	if !errors.Is(err, ErrInvalidSpec) {
		t.Fatalf("error = %v, want ErrInvalidSpec", err)
	}
}

func TestRunnerDoesNotInheritEnvironment(t *testing.T) {
	t.Setenv("UNIT_TEST_IDE_PROBE_INHERITED", "secret")
	spec := helperSpec(t, "environment", "UNIT_TEST_IDE_PROBE_INHERITED")
	spec.Env = []string{}

	result, err := NewRunner().Run(context.Background(), spec)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got := string(result.Stdout); got != "" {
		t.Fatalf("inherited environment value = %q, want empty", got)
	}
}

func TestRunnerUsesOnlyExplicitEnvironment(t *testing.T) {
	spec := helperSpec(t, "environment", "UNIT_TEST_IDE_PROBE_VISIBLE")
	spec.Env = []string{"UNIT_TEST_IDE_PROBE_VISIBLE=explicit"}

	result, err := NewRunner().Run(context.Background(), spec)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got := string(result.Stdout); got != "explicit" {
		t.Fatalf("explicit environment value = %q", got)
	}
}

func TestRunnerRejectsMalformedEnvironment(t *testing.T) {
	spec := helperSpec(t, "success")
	spec.Env = []string{"MISSING_SEPARATOR"}

	_, err := NewRunner().Run(context.Background(), spec)
	if !errors.Is(err, ErrInvalidSpec) {
		t.Fatalf("error = %v, want ErrInvalidSpec", err)
	}
}

func TestRunnerTimesOutAndWaitsForProcessExit(t *testing.T) {
	spec := helperSpec(t, "hang")
	spec.Timeout = 75 * time.Millisecond
	started := time.Now()

	result, err := NewRunner().Run(context.Background(), spec)
	elapsed := time.Since(started)

	if !errors.Is(err, ErrTimeout) {
		t.Fatalf("error = %v, want ErrTimeout", err)
	}
	if result.ExitCode != -1 {
		t.Fatalf("ExitCode = %d, want -1", result.ExitCode)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("timeout cleanup took %v", elapsed)
	}
}

func TestRunnerUsesFiveSecondDefaultTimeout(t *testing.T) {
	spec := helperSpec(t, "sleep", "5500")
	started := time.Now()

	_, err := NewRunner().Run(context.Background(), spec)
	elapsed := time.Since(started)

	if !errors.Is(err, ErrTimeout) {
		t.Fatalf("error = %v, want ErrTimeout", err)
	}
	if elapsed < 4500*time.Millisecond || elapsed > 7*time.Second {
		t.Fatalf("default timeout elapsed = %v, want approximately 5s", elapsed)
	}
}

func TestRunnerHonorsCallerCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := NewRunner().Run(ctx, helperSpec(t, "hang"))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
}

func TestRunnerStopsOnStdoutLimit(t *testing.T) {
	spec := helperSpec(t, "large-stdout")
	spec.MaxOutput = 64
	started := time.Now()

	result, err := NewRunner().Run(context.Background(), spec)
	elapsed := time.Since(started)

	if !errors.Is(err, ErrOutputLimit) {
		t.Fatalf("error = %v, want ErrOutputLimit", err)
	}
	if len(result.Stdout) != 64 {
		t.Fatalf("stdout length = %d, want 64", len(result.Stdout))
	}
	if len(result.Stderr) != 0 {
		t.Fatalf("stderr length = %d, want 0", len(result.Stderr))
	}
	if elapsed > 2*time.Second {
		t.Fatalf("stdout limit cleanup took %v", elapsed)
	}
}

func TestRunnerStopsOnStderrLimit(t *testing.T) {
	spec := helperSpec(t, "large-stderr")
	spec.MaxOutput = 64

	result, err := NewRunner().Run(context.Background(), spec)

	if !errors.Is(err, ErrOutputLimit) {
		t.Fatalf("error = %v, want ErrOutputLimit", err)
	}
	if len(result.Stderr) != 64 {
		t.Fatalf("stderr length = %d, want 64", len(result.Stderr))
	}
	if len(result.Stdout) != 0 {
		t.Fatalf("stdout length = %d, want 0", len(result.Stdout))
	}
}

func TestRunnerTimeoutTerminatesDescendant(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "descendant.pid")
	spec := helperSpec(t, "spawn-descendant-hang", marker)
	spec.Timeout = 500 * time.Millisecond

	_, err := NewRunner().Run(context.Background(), spec)
	if !errors.Is(err, ErrTimeout) {
		t.Fatalf("error = %v, want ErrTimeout", err)
	}
	assertRecordedDescendantGone(t, marker)
}

func TestRunnerOutputLimitTerminatesDescendantAndConvergesPipes(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "descendant.pid")
	spec := helperSpec(t, "spawn-descendant-output", marker)
	spec.MaxOutput = 64

	_, err := NewRunner().Run(context.Background(), spec)
	if !errors.Is(err, ErrOutputLimit) {
		t.Fatalf("error = %v, want ErrOutputLimit", err)
	}
	assertRecordedDescendantGone(t, marker)
}

func TestRunnerNaturalTargetExitTerminatesLingeringDescendant(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "descendant.pid")
	result, err := NewRunner().Run(
		context.Background(),
		helperSpec(t, "spawn-descendant-exit", marker),
	)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0", result.ExitCode)
	}
	assertRecordedDescendantGone(t, marker)
}

func TestRunnerAppliesIndependentDefaultOutputLimits(t *testing.T) {
	spec := helperSpec(t, "exact-streams", strconv.Itoa(defaultMaxOutput))

	result, err := NewRunner().Run(context.Background(), spec)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(result.Stdout) != defaultMaxOutput || len(result.Stderr) != defaultMaxOutput {
		t.Fatalf("stream lengths = (%d, %d), want (%d, %d)",
			len(result.Stdout), len(result.Stderr), defaultMaxOutput, defaultMaxOutput)
	}

	spec = helperSpec(t, "large-stdout")
	result, err = NewRunner().Run(context.Background(), spec)
	if !errors.Is(err, ErrOutputLimit) {
		t.Fatalf("large stdout error = %v, want ErrOutputLimit", err)
	}
	if len(result.Stdout) != defaultMaxOutput {
		t.Fatalf("large stdout length = %d, want %d", len(result.Stdout), defaultMaxOutput)
	}
}

func helperSpec(t *testing.T, mode string, values ...string) Spec {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable() error = %v", err)
	}
	args := []string{"-test.run=^TestProbeHelperProcess$", "--", mode}
	args = append(args, values...)
	return Spec{
		Executable: executable,
		Args:       args,
		Env:        []string{},
		Dir:        filepath.Dir(executable),
	}
}

func TestProbeHelperProcess(t *testing.T) {
	separator := -1
	for index, argument := range os.Args {
		if argument == "--" {
			separator = index
			break
		}
	}
	if separator < 0 || separator+1 >= len(os.Args) {
		return
	}

	mode := os.Args[separator+1]
	values := os.Args[separator+2:]
	switch mode {
	case "success":
		_, _ = fmt.Fprint(os.Stdout, "stdout-marker")
		_, _ = fmt.Fprint(os.Stderr, "stderr-marker")
	case "environment":
		if len(values) != 1 {
			os.Exit(2)
		}
		_, _ = fmt.Fprint(os.Stdout, os.Getenv(values[0]))
	case "hang":
		time.Sleep(time.Hour)
	case "sleep":
		if len(values) != 1 {
			os.Exit(2)
		}
		milliseconds, err := strconv.Atoi(values[0])
		if err != nil {
			os.Exit(2)
		}
		time.Sleep(time.Duration(milliseconds) * time.Millisecond)
	case "large-stdout":
		writeUntilClosed(os.Stdout)
	case "large-stderr":
		writeUntilClosed(os.Stderr)
	case "exact-streams":
		if len(values) != 1 {
			os.Exit(2)
		}
		size, err := strconv.Atoi(values[0])
		if err != nil {
			os.Exit(2)
		}
		chunk := bytes.Repeat([]byte("x"), size)
		_, _ = os.Stdout.Write(chunk)
		_, _ = os.Stderr.Write(chunk)
	case "spawn-descendant-hang":
		spawnProbeDescendant(values, "descendant-hang", true)
	case "spawn-descendant-output":
		spawnProbeDescendant(values, "descendant-output", true)
	case "spawn-descendant-exit":
		spawnProbeDescendant(values, "descendant-hang", false)
	case "descendant-hang":
		time.Sleep(time.Hour)
	case "descendant-output":
		if len(values) != 1 {
			os.Exit(2)
		}
		waitForMarker(values[0])
		writeUntilClosed(os.Stdout)
		time.Sleep(time.Hour)
	default:
		_, _ = fmt.Fprintf(os.Stderr, "unknown helper mode %q", mode)
		os.Exit(2)
	}
	os.Exit(0)
}

func spawnProbeDescendant(values []string, mode string, keepParentAlive bool) {
	if len(values) != 1 {
		os.Exit(2)
	}
	executable, err := os.Executable()
	if err != nil {
		os.Exit(2)
	}
	marker := values[0]
	child := exec.Command(executable, "-test.run=^TestProbeHelperProcess$", "--", mode, marker)
	child.Env = []string{}
	child.Stdout = os.Stdout
	child.Stderr = os.Stderr
	if err := child.Start(); err != nil {
		os.Exit(2)
	}
	if err := os.WriteFile(marker, []byte(strconv.Itoa(child.Process.Pid)), 0o600); err != nil {
		_ = child.Process.Kill()
		os.Exit(2)
	}
	if keepParentAlive {
		time.Sleep(time.Hour)
	}
}

func waitForMarker(marker string) {
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(marker); err == nil {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	os.Exit(2)
}

func assertRecordedDescendantGone(t *testing.T, marker string) {
	t.Helper()
	data, err := os.ReadFile(marker)
	if err != nil {
		t.Fatalf("read descendant marker: %v", err)
	}
	pid, err := strconv.Atoi(string(data))
	if err != nil || pid <= 0 {
		t.Fatalf("descendant PID = %q, error = %v", data, err)
	}
	t.Cleanup(func() {
		terminateTestProcess(pid)
	})
	if !waitTestProcessGone(pid, 2*time.Second) {
		t.Fatalf("descendant PID %d remained alive", pid)
	}
}

func writeUntilClosed(file *os.File) {
	chunk := strings.Repeat("x", 32*1024)
	for {
		if _, err := file.WriteString(chunk); err != nil {
			return
		}
	}
}
