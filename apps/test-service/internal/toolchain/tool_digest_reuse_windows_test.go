//go:build windows

package toolchain

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"unit-test-ide.local/test-service/internal/toolhash"
)

func TestToolchainToolDigestScopedBinary(t *testing.T) {
	p := filepath.Join(t.TempDir(), "cl.exe")
	data := []byte("complete compiler fixture")
	if err := os.WriteFile(p, data, 0600); err != nil {
		t.Fatal(err)
	}
	ctx, s, err := toolhash.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	full, before := 0, 0
	open := func() *executableSnapshot {
		t.Helper()
		snap, err := openExecutableSnapshotWithDigestObserver(ctx, p, 4096, func() { before++ }, func() { full++ })
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = snap.Close() })
		return snap
	}
	first, second := open(), open()
	if first.file == second.file {
		t.Fatal("snapshots do not own independent handles")
	}
	for _, snap := range []*executableSnapshot{first, second} {
		for i := 0; i < 3; i++ {
			if err := snap.Verify(ctx); err != nil {
				t.Fatal(err)
			}
		}
	}
	if got := s.Stats(); got.FullHashCalls != 1 || got.HashedBytes != int64(len(data)) || full != 0 {
		t.Fatalf("protected digest counts: core=%+v fallback=%d, want one complete core hash and zero fallback", got, full)
	}
	if before != 2 || first.toolEvidence == nil || second.toolEvidence == nil {
		t.Fatalf("initial hooks=%d, protected evidence=%v/%v", before, first.toolEvidence != nil, second.toolEvidence != nil)
	}
	sha := sha256.Sum256(data)
	wantSHA := hex.EncodeToString(sha[:])
	encoded, err := json.Marshal(struct {
		Path   string `json:"path"`
		SHA256 string `json:"sha256"`
	}{strings.ToLower(filepath.ToSlash(filepath.Clean(p))), wantSHA})
	if err != nil {
		t.Fatal(err)
	}
	identity := sha256.Sum256(encoded)
	for _, snap := range []*executableSnapshot{first, second} {
		if snap.digest != wantSHA || snap.identity != hex.EncodeToString(identity[:]) {
			t.Fatal("Path/SHA identity encoding changed")
		}
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	if err := second.Verify(ctx); err != nil {
		t.Fatal("closing one consumer invalidated another", err)
	}
	if err := os.WriteFile(p, data, 0600); err == nil {
		t.Fatal("write allowed while scope guarded")
	}
	if err := os.Remove(p); err == nil {
		t.Fatal("delete allowed while scope guarded")
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if err := second.Verify(ctx); err == nil {
		t.Fatal("closed scope silently rehashed bound snapshot")
	}
	if full != 0 || s.Stats().FullHashCalls != 1 {
		t.Fatal("closed evidence fell back to hashing")
	}
	// The independent consumer remains open: writes become possible when the
	// scope guard closes, but Go's consumer may still deny deletion itself.
	if err := os.WriteFile(p, data, 0600); err != nil {
		t.Fatal("scope guard not released", err)
	}
	if err := second.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(p); err != nil {
		t.Fatal("consumer close did not release delete sharing", err)
	}
}

func TestToolchainToolDigestScriptsRemainFullHash(t *testing.T) {
	p := filepath.Join(t.TempDir(), "VsDevCmd.bat")
	if err := os.WriteFile(p, []byte("@echo off\r\n"), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, s, err := toolhash.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	full := 0
	snap, err := openExecutableSnapshotWithDigestObserver(ctx, p, 4096, nil, func() { full++ })
	if err != nil {
		t.Fatal(err)
	}
	defer snap.Close()
	initial := full
	if initial < 1 || snap.toolEvidence != nil {
		t.Fatal("script gained protected digest authority")
	}
	if err := snap.Verify(ctx); err != nil {
		t.Fatal(err)
	}
	if full != initial+1 || s.Stats().FullHashCalls != 0 {
		t.Fatal("script did not rehash")
	}
}

func TestToolchainToolDigestSmallerLimitAndCancelled(t *testing.T) {
	t.Run("smaller open limit", func(t *testing.T) {
		p := filepath.Join(t.TempDir(), "tool.exe")
		if err := os.WriteFile(p, []byte("12345678"), 0600); err != nil {
			t.Fatal(err)
		}
		ctx, s, err := toolhash.Begin(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		full := 0
		snap, err := openExecutableSnapshotWithDigestObserver(ctx, p, 8, nil, func() { full++ })
		if err != nil {
			t.Fatal(err)
		}
		defer snap.Close()
		if _, err := openExecutableSnapshotWithDigestObserver(ctx, p, 7, nil, func() { full++ }); !errors.Is(err, errExecutableTooLarge) {
			t.Fatalf("smaller limit accepted cached file: %v", err)
		}
		if full != 0 || s.Stats().FullHashCalls != 1 {
			t.Fatalf("smaller limit rehashed: fallback=%d core=%+v", full, s.Stats())
		}
		if s.Err() == nil || snap.Verify(ctx) == nil || s.Close() == nil {
			t.Fatal("retained capture limit rejection was not sticky")
		}
	})
	t.Run("child verify cancellation only", func(t *testing.T) {
		p := filepath.Join(t.TempDir(), "tool.exe")
		if err := os.WriteFile(p, []byte("tool"), 0600); err != nil {
			t.Fatal(err)
		}
		ctx, s, err := toolhash.Begin(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		snap, err := openExecutableSnapshotWithLimit(ctx, p, 4096)
		if err != nil {
			t.Fatal(err)
		}
		defer snap.Close()
		child, cancel := context.WithCancel(ctx)
		cancel()
		if err := snap.Verify(child); !errors.Is(err, context.Canceled) {
			t.Fatalf("Verify: %v", err)
		}
		if ctx.Err() != nil {
			t.Fatal("parent cancelled")
		}
		if !errors.Is(s.Err(), context.Canceled) || snap.Verify(ctx) == nil || !errors.Is(s.Close(), context.Canceled) {
			t.Fatal("bound child cancellation was not sticky")
		}
	})
	t.Run("cancelled open and verify", func(t *testing.T) {
		p := filepath.Join(t.TempDir(), "tool.exe")
		if err := os.WriteFile(p, []byte("tool"), 0600); err != nil {
			t.Fatal(err)
		}
		ctx, s, err := toolhash.Begin(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		full := 0
		snap, err := openExecutableSnapshotWithDigestObserver(ctx, p, 4096, nil, func() { full++ })
		if err != nil {
			t.Fatal(err)
		}
		defer snap.Close()
		cancelled, cancel := context.WithCancel(ctx)
		cancel()
		if _, err := openExecutableSnapshotWithLimit(cancelled, p, 4096); !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled open: %v", err)
		}
		if err := snap.Verify(cancelled); !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled verify: %v", err)
		}
		if full != 0 || s.Stats().FullHashCalls != 1 {
			t.Fatal("cancellation invoked another hash")
		}
	})
	t.Run("cancelled before digest hook", func(t *testing.T) {
		p := filepath.Join(t.TempDir(), "tool.exe")
		if err := os.WriteFile(p, []byte("tool"), 0600); err != nil {
			t.Fatal(err)
		}
		parent, cancel := context.WithCancel(context.Background())
		ctx, s, err := toolhash.Begin(parent)
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		hooks, full := 0, 0
		_, err = openExecutableSnapshotWithDigestObserver(ctx, p, 4096, func() { hooks++; cancel() }, func() { full++ })
		if !errors.Is(err, context.Canceled) || hooks != 1 || full != 0 || s.Stats().FullHashCalls != 0 {
			t.Fatalf("hook cancellation: err=%v hooks=%d fallback=%d core=%+v", err, hooks, full, s.Stats())
		}
	})
}

func TestToolchainToolDigestIdentityChangesNextScope(t *testing.T) {
	p := filepath.Join(t.TempDir(), "tool.EXE")
	var identities [2]string
	for i, data := range []string{"first", "other"} {
		if err := os.WriteFile(p, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
		ctx, s, err := toolhash.Begin(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = s.Close() })
		full := 0
		snap, err := openExecutableSnapshotWithDigestObserver(ctx, p, 4096, nil, func() { full++ })
		if err != nil {
			_ = s.Close()
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = snap.Close() })
		identities[i] = snap.identity
		stats := s.Stats()
		if err := snap.Close(); err != nil {
			t.Fatal(err)
		}
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
		if stats.FullHashCalls != 1 || full != 0 {
			t.Fatalf("scope %d did not hash first use: %+v fallback=%d", i, stats, full)
		}
	}
	if identities[0] == identities[1] {
		t.Fatal("changed bytes retained prior scope identity")
	}
}

func TestToolchainToolDigestDifferentConsumerRejected(t *testing.T) {
	root := t.TempDir()
	p, other := filepath.Join(root, "tool.exe"), filepath.Join(root, "other.exe")
	for _, path := range []string{p, other} {
		if err := os.WriteFile(path, []byte("same bytes"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	ctx, s, err := toolhash.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	full := 0
	snap, err := openExecutableSnapshotWithDigestObserver(ctx, p, 4096, nil, func() { full++ })
	if err != nil {
		t.Fatal(err)
	}
	defer snap.Close()
	foreign, err := os.Open(other)
	if err != nil {
		t.Fatal(err)
	}
	defer foreign.Close()
	original := snap.file
	snap.file = foreign
	verifyErr := snap.Verify(ctx)
	snap.file = original
	if verifyErr == nil {
		t.Fatal("same bytes from different consumer accepted")
	}
	if full != 0 || s.Stats().FullHashCalls != 1 {
		t.Fatal("different consumer triggered fallback hashing")
	}
	if s.Err() == nil {
		t.Error("bound consumer rejection did not poison scope")
	}
	if err := snap.Verify(ctx); err == nil {
		t.Error("restored consumer revived evidence")
	}
	if _, _, err := toolhash.Digest(ctx, p, original, 4096); err == nil {
		t.Error("restored consumer revived Digest")
	}
	if err := s.Close(); err == nil {
		t.Error("bound consumer rejection lost at Close")
	}
}

func TestToolchainToolDigestSelectedSDKExcluded(t *testing.T) {
	for _, name := range []string{"winmm.lib", "runtime.dll", "setup.cmd", "setup.ps1"} {
		t.Run(name, func(t *testing.T) {
			p := filepath.Join(t.TempDir(), name)
			if err := os.WriteFile(p, []byte("excluded bytes"), 0600); err != nil {
				t.Fatal(err)
			}
			ctx, s, err := toolhash.Begin(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			f, err := os.Open(p)
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			result, ok, err := guardedExecutableDigest(ctx, p, f, 4096)
			if err != nil || ok || result.Evidence != nil {
				t.Fatalf("excluded extension gained evidence: %+v %v %v", result, ok, err)
			}
			full := 0
			snap, err := openExecutableSnapshotWithDigestObserver(ctx, p, 4096, nil, func() { full++ })
			if err != nil {
				t.Fatal(err)
			}
			defer snap.Close()
			initial := full
			if err := snap.Verify(ctx); err != nil {
				t.Fatal(err)
			}
			if snap.toolEvidence != nil || initial < 1 || full != initial+1 || s.Stats().FullHashCalls != 0 {
				t.Fatal("excluded file stopped full hashing")
			}
		})
	}
}

func TestToolchainToolDigestUnboundPrechecksStayHealthy(t *testing.T) {
	root := t.TempDir()
	p := filepath.Join(root, "tool.exe")
	if err := os.WriteFile(p, []byte("real tool"), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, s, err := toolhash.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	snap, err := openExecutableSnapshotWithLimit(ctx, p, 4096)
	if err != nil {
		t.Fatal(err)
	}
	defer snap.Close()
	for _, name := range []string{"missing.exe", "setup.cmd", "sdk.lib"} {
		if _, err := openExecutableSnapshotWithLimit(ctx, filepath.Join(root, name), 4096); err == nil {
			t.Fatal("missing optional file accepted")
		}
	}
	if s.Err() != nil || snap.Verify(ctx) != nil || s.Close() != nil {
		t.Fatal("unbound optional failure poisoned scope")
	}
}

func TestToolchainToolDigestSpellingBudgetFallback(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "real")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "tool.exe")
	if err := os.WriteFile(p, []byte("tool bytes"), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, s, err := toolhash.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	snap, err := openExecutableSnapshotWithLimit(ctx, p, 4096)
	if err != nil {
		t.Fatal(err)
	}
	defer snap.Close()
	for i := 0; i < 129; i++ {
		alias := filepath.Join(root, "alias-"+strconv.Itoa(i))
		if err := os.Symlink(dir, alias); err != nil {
			t.Fatal(err)
		}
		full := 0
		other, err := openExecutableSnapshotWithDigestObserver(ctx, filepath.Join(alias, "tool.exe"), 4096, nil, func() { full++ })
		if err != nil {
			t.Fatal(err)
		}
		if i < 128 && (other.toolEvidence == nil || full != 0) {
			t.Error("admitted spelling did not reuse")
		}
		if i == 128 {
			if other.toolEvidence != nil || full != 2 || other.digest != snap.digest {
				t.Error("overflow did not fully hash capture and Verify")
			}
			if err := other.Verify(ctx); err != nil || full != 3 {
				t.Error("overflow Verify did not fully hash", err)
			}
		}
		if err := other.Close(); err != nil {
			t.Fatal(err)
		}
	}
	if s.Err() != nil || snap.Verify(ctx) != nil || s.Close() != nil {
		t.Fatal("optional spelling overflow poisoned scope")
	}
}
