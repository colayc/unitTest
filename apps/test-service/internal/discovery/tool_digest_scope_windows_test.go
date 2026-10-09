//go:build windows

package discovery

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"unit-test-ide.local/test-service/internal/cmake"
	"unit-test-ide.local/test-service/internal/probe"
	"unit-test-ide.local/test-service/internal/toolchain"
	"unit-test-ide.local/test-service/internal/toolhash"
	"unit-test-ide.local/test-service/internal/workspace"
)

func TestInspectorToolDigestScopePropagation(t *testing.T) {
	paths := []string{filepath.Join(t.TempDir(), "cmake.exe"), filepath.Join(t.TempDir(), "cl.exe")}
	for _, p := range paths {
		if err := os.WriteFile(p, []byte("controlled executable bytes"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	ctx, scope, err := toolhash.Begin(context.Background())
	if err != nil || scope == nil {
		t.Fatalf("begin: %v", err)
	}
	defer scope.Close()
	outcomes := make(chan error, 4)
	digest := func(ctx context.Context, path string) error {
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()
		r, protected, err := toolhash.Digest(ctx, path, f, 1024)
		if err != nil {
			return err
		}
		if !protected || r.Evidence == nil {
			return errors.New("Inspector discarded scoped context")
		}
		return nil
	}
	var loads, resolves, toolchains atomic.Int32
	tools := fakeToolchainDiscovery{discover: func(ctx context.Context) ([]toolchain.Instance, []toolchain.Issue) {
		toolchains.Add(1)
		outcomes <- digest(ctx, paths[1])
		return nil, nil
	}}
	inspector := newTestInspector(t, openProjectRoot(t, "."), tools, inspectorDependencies{
		loadConfig: func(workspace.Root) (workspace.LoadResult, error) {
			loads.Add(1)
			return workspace.LoadResult{Config: workspace.Config{Version: 1}}, nil
		},
		resolve: func(ctx context.Context, _ probe.Runner, _ cmake.ResolverConfig) (cmake.Installation, error) {
			resolves.Add(1)
			err := digest(ctx, paths[0])
			outcomes <- err
			if err != nil {
				return cmake.Installation{}, err
			}
			return testInstallation(), nil
		},
		discoverPresets: noPresetDiscovery,
	})
	for i := 0; i < 2; i++ {
		if _, err := inspector.Inspect(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if len(outcomes) != 4 {
		t.Fatal("lost foundation callback")
	}
	for i := 0; i < 4; i++ {
		if err := <-outcomes; err != nil {
			t.Fatal(err)
		}
	}
	if loads.Load() != 2 || resolves.Load() != 2 || toolchains.Load() != 2 {
		t.Fatal("cached a complete discovery")
	}
	if got := scope.Stats(); got.FullHashCalls != 2 || got.Retained != 2 || got.Reuses != 2 || got.HashedBytes != 54 {
		t.Fatalf("guarded tools: %+v", got)
	}
	if err := scope.Close(); err != nil {
		t.Fatal(err)
	}
	for _, p := range paths {
		if err := os.WriteFile(p, []byte("after close"), 0600); err != nil {
			t.Fatal("guard leaked", err)
		}
	}
}

func TestInspectorToolDigestScopeCancellation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cmake.exe")
	if err := os.WriteFile(path, []byte("controlled executable bytes"), 0600); err != nil {
		t.Fatal(err)
	}
	parent, cancel := context.WithCancel(context.Background())
	ctx, scope, err := toolhash.Begin(parent)
	if err != nil || scope == nil {
		cancel()
		t.Fatalf("begin: %v", err)
	}
	gate := make(chan struct{})
	var release sync.Once
	cmakeStarted, toolchainsStarted := make(chan struct{}), make(chan struct{})
	cmakeDone, toolchainsDone, inspectDone := make(chan struct{}), make(chan struct{}), make(chan struct{})
	digested := make(chan error, 1)
	result := make(chan error, 1)
	// Cleanup always releases and joins both callbacks and Inspect, even after
	// a watchdog fails. Assertions run only in the test goroutine.
	t.Cleanup(func() {
		cancel()
		release.Do(func() { close(gate) })
		cleanupClosed := make(chan struct{})
		go func() { defer close(cleanupClosed); _ = scope.Close() }()
		for _, done := range []chan struct{}{cmakeDone, toolchainsDone, inspectDone} {
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Error("cancellation cleanup did not join worker")
			}
		}
		select {
		case <-cleanupClosed:
		case <-time.After(5 * time.Second):
			t.Error("scope cleanup did not finish")
		}
	})
	tools := fakeToolchainDiscovery{discover: func(ctx context.Context) ([]toolchain.Instance, []toolchain.Issue) {
		defer close(toolchainsDone)
		close(toolchainsStarted)
		<-ctx.Done()
		return nil, nil
	}}
	inspector := newTestInspector(t, openProjectRoot(t, "."), tools, inspectorDependencies{
		loadConfig: func(workspace.Root) (workspace.LoadResult, error) {
			return workspace.LoadResult{Config: workspace.Config{Version: 1}}, nil
		},
		resolve: func(ctx context.Context, _ probe.Runner, _ cmake.ResolverConfig) (cmake.Installation, error) {
			defer close(cmakeDone)
			close(cmakeStarted)
			f, err := os.Open(path)
			if err != nil {
				digested <- err
				return cmake.Installation{}, err
			}
			r, protected, err := toolhash.Digest(ctx, path, f, 1024)
			closeErr := f.Close()
			if err == nil {
				err = closeErr
			}
			if err == nil && (!protected || r.Evidence == nil) {
				err = errors.New("missing guarded evidence")
			}
			digested <- err
			<-gate
			return testInstallation(), err
		}, discoverPresets: noPresetDiscovery,
	})
	go func() { defer close(inspectDone); _, err := inspector.Inspect(ctx); result <- err }()
	for _, started := range []chan struct{}{cmakeStarted, toolchainsStarted} {
		select {
		case <-started:
		case <-time.After(5 * time.Second):
			t.Fatal("foundation did not start")
		}
	}
	select {
	case err := <-digested:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("digest did not finish")
	}
	if err := os.WriteFile(path, []byte("must remain guarded"), 0600); err == nil {
		t.Fatal("guard missing before cancellation")
	}
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Inspect cancellation: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Inspect waited for blocked callback")
	}
	closed := make(chan error, 1)
	go func() { closed <- scope.Close() }()
	select {
	case err := <-closed:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("scope close: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("scope close waited for callback")
	}
	if err := os.WriteFile(path, []byte("after close"), 0600); err != nil {
		t.Fatal("guard leaked", err)
	}
	release.Do(func() { close(gate) })
	select {
	case <-cmakeDone:
	case <-time.After(5 * time.Second):
		t.Fatal("blocked callback did not join")
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, _, err := toolhash.Digest(ctx, path, f, 1024); !errors.Is(err, context.Canceled) {
		t.Fatalf("closed cancelled scope reused: %v", err)
	}
}
