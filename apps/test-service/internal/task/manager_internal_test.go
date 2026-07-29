package task

import (
	"context"
	"errors"
	"sync"
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

func TestShutdownCancellationWinsWhenStopCompletesDuringWait(t *testing.T) {
	for range 64 {
		stopped := make(chan struct{})
		manager := &Manager{
			shutdownSignal: make(chan struct{}, 1),
			stopped:        stopped,
		}
		base, cancel := context.WithCancel(context.Background())
		cancel()
		ctx := &stopWhenDoneContext{Context: base, stopped: stopped}

		if err := manager.Shutdown(ctx); !errors.Is(err, context.Canceled) {
			t.Fatalf("Shutdown error = %v, want cancellation", err)
		}
	}
}

type stopWhenDoneContext struct {
	context.Context
	stopped chan struct{}
	once    sync.Once
}

func (c *stopWhenDoneContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.stopped) })
	return c.Context.Done()
}
