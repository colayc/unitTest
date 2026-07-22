package task

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestShutdownPublishesOneIntentWithoutBlockingOnFullCommandQueue(t *testing.T) {
	manager := &Manager{commands: make(chan any, 1), shutdownSignal: make(chan struct{}, 1), stopped: make(chan struct{})}
	manager.commands <- struct{}{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	started := time.Now()
	err := manager.Shutdown(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Shutdown error = %v, want cancellation", err)
	}
	if elapsed := time.Since(started); elapsed > 200*time.Millisecond {
		t.Fatalf("Shutdown blocked on full command queue for %s", elapsed)
	}
	if got := len(manager.shutdownSignal); got != 1 {
		t.Fatalf("shutdown intents = %d, want 1", got)
	}
	if err := manager.Shutdown(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("second Shutdown error = %v, want cancellation", err)
	}
	if got := len(manager.shutdownSignal); got != 1 {
		t.Fatalf("shutdown intents after retry = %d, want 1", got)
	}
}
