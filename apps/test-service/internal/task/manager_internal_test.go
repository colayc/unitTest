package task

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestShutdownHonorsContextWhileCommandQueueIsFull(t *testing.T) {
	manager := &Manager{commands: make(chan any, 1), stopped: make(chan struct{})}
	manager.commands <- struct{}{}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	started := time.Now()
	err := manager.Shutdown(ctx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Shutdown error = %v, want deadline", err)
	}
	if elapsed := time.Since(started); elapsed > 200*time.Millisecond {
		t.Fatalf("Shutdown ignored enqueue deadline for %s", elapsed)
	}
}
