package taskfixture_test

import (
	"bytes"
	"context"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"unit-test-ide.local/test-service/internal/task"
	"unit-test-ide.local/test-service/internal/taskfixture"
)

type lockedBuffer struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

func (buffer *lockedBuffer) Write(value []byte) (int, error) {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.buffer.Write(value)
}

func (buffer *lockedBuffer) String() string {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.buffer.String()
}

func (buffer *lockedBuffer) Len() int {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.buffer.Len()
}

func (buffer *lockedBuffer) Reset() {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	buffer.buffer.Reset()
}

func TestMain(m *testing.M) {
	if len(os.Args) == 2 && os.Args[1] == "--task-fixture-child" {
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		<-ctx.Done()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestFixtureScenarios(t *testing.T) {
	var stdout, stderr lockedBuffer
	if code := taskfixture.Run(context.Background(), task.ScenarioEmitOutput, &stdout, &stderr); code != 0 {
		t.Fatalf("code = %d", code)
	}
	if stdout.String() != "fixture stdout\n" || stderr.String() != "fixture stderr\n" {
		t.Fatalf("stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	if code := taskfixture.Run(context.Background(), task.ScenarioExitNonzero, &stdout, &stderr); code != 17 {
		t.Fatalf("code = %d", code)
	}
	if stdout.Len() != 0 || stderr.String() != "fixture exits with code 17\n" {
		t.Fatalf("stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
}

func TestFixtureSuccessHasNoOutput(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := taskfixture.Run(context.Background(), task.ScenarioSuccess, &stdout, &stderr); code != 0 {
		t.Fatalf("code = %d", code)
	}
	if stdout.Len() != 0 || stderr.Len() != 0 {
		t.Fatalf("stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
}

func TestFixtureRejectsUnknownScenarioWithoutReflectingIt(t *testing.T) {
	var stdout, stderr bytes.Buffer
	secret := task.Scenario(`unknown-C:\private\token`)
	if code := taskfixture.Run(context.Background(), secret, &stdout, &stderr); code != 2 {
		t.Fatalf("code = %d", code)
	}
	if stdout.Len() != 0 || stderr.String() != "unknown fixture scenario\n" {
		t.Fatalf("stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
	if strings.Contains(stderr.String(), string(secret)) {
		t.Fatalf("stderr reflected scenario: %q", stderr.String())
	}
}

func TestFixtureHangStopsWhenContextIsCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan int, 1)
	go func() { done <- taskfixture.Run(ctx, task.ScenarioHang, &bytes.Buffer{}, &bytes.Buffer{}) }()
	cancel()
	select {
	case code := <-done:
		if code != 0 {
			t.Fatalf("code = %d", code)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("hang fixture did not stop after cancellation")
	}
}

func TestSpawnChildReportsOnlyPIDAndStopsWithContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var stdout, stderr lockedBuffer
	done := make(chan int, 1)
	go func() { done <- taskfixture.Run(ctx, task.ScenarioSpawnChild, &stdout, &stderr) }()

	deadline := time.Now().Add(2 * time.Second)
	for !strings.HasPrefix(stdout.String(), "CHILD_PID=") && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	cancel()
	select {
	case code := <-done:
		if code != 0 {
			t.Fatalf("code = %d, stderr = %q", code, stderr.String())
		}
	case <-time.After(2 * time.Second):
		t.Fatal("spawn-child fixture did not stop after cancellation")
	}
	if !strings.HasPrefix(stdout.String(), "CHILD_PID=") || !strings.HasSuffix(stdout.String(), "\n") {
		t.Fatalf("stdout = %q", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q", stderr.String())
	}
}
