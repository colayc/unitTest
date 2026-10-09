//go:build windows

package cmake

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"unit-test-ide.local/test-service/internal/toolhash"
)

func TestCMakeToolDigestScopeReusesOnlyToolBytes(t *testing.T) {
	p := filepath.Join(t.TempDir(), "cmake.exe")
	if err := os.WriteFile(p, []byte("cmake fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, s, err := toolhash.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var full int
	for i := 0; i < 2; i++ {
		snap, err := captureToolFileSnapshotWithHook(ctx, p, 0, func() { full++ })
		if err != nil {
			t.Fatal(err)
		}
		for j := 0; j < 3; j++ {
			if err := snap.Verify(); err != nil {
				t.Fatal(err)
			}
		}
		if err := snap.Close(); err != nil {
			t.Fatal(err)
		}
	}
	if full != 0 || s.Stats().FullHashCalls != 1 {
		t.Fatalf("legacy=%d scope=%+v", full, s.Stats())
	}
	ordinary, err := captureFileSnapshot(p, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer ordinary.Close()
	if ordinary.toolEvidence != nil {
		t.Fatal("ordinary input opted into cache")
	}
	ordinary.onFullHash = func() { full++ }
	if err := ordinary.Verify(); err != nil || full != 1 {
		t.Fatal("ordinary verification skipped", err)
	}
}

func TestCMakeToolDigestResolverStillProbesBothQueries(t *testing.T) {
	executable := currentExecutable(t)
	runner := &versionRunner{t: t, version: "4.3.4"}
	ctx, s, err := toolhash.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var first Installation
	for i := 0; i < 2; i++ {
		got, err := Resolve(ctx, runner, ResolverConfig{Override: executable, Platform: "win32", Architecture: "x64"})
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			first = got
		} else if got != first {
			t.Fatalf("installation changed: %+v != %+v", got, first)
		}
	}
	runner.assertCalls(t,
		expectedVersionSpec(canonicalPath(t, executable), "--version=json-v1"),
		expectedVersionSpec(pairedCTestPath(t, executable), "--version"),
		expectedVersionSpec(canonicalPath(t, executable), "--version=json-v1"),
		expectedVersionSpec(pairedCTestPath(t, executable), "--version"),
	)
	if stats := s.Stats(); stats.FullHashCalls != 2 || stats.Retained != 2 || stats.Reuses != 2 {
		t.Fatalf("scope=%+v", stats)
	}
}

func TestCMakeToolDigestBundleExcludesMetadata(t *testing.T) {
	root := createBundle(t, "win32-x64")
	ctx, s, err := toolhash.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	policy, err := loadManifest(filepath.Join(root, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	config := ResolverConfig{BundleRoot: root, Platform: "win32", Architecture: "x64"}
	runner := &versionRunner{t: t, version: policy.CMakeVersion}
	for i := 0; i < 2; i++ {
		if _, err := resolveFixtureBundle(t, ctx, runner, config); err != nil {
			t.Fatal(err)
		}
	}
	if stats := s.Stats(); stats.FullHashCalls != 2 || stats.Retained != 2 || stats.Reuses != 2 {
		t.Fatalf("metadata included in scope: %+v", stats)
	}
	archive := policy.Archives["win32-x64"]
	platformRoot := filepath.Join(root, policy.CMakeVersion, "win32-x64")
	installRoot := filepath.Join(platformRoot, filepath.FromSlash(archive.RootDirectory))
	for _, relative := range []string{archive.Executable, archive.CTestExecutable} {
		snap, err := captureToolFileSnapshot(ctx, filepath.Join(installRoot, filepath.FromSlash(relative)), 0)
		if err != nil {
			t.Fatal(err)
		}
		if snap.toolEvidence == nil {
			t.Fatal("bundle tool has no evidence")
		}
		if err := snap.Verify(); err != nil {
			t.Fatal(err)
		}
		if err := snap.Close(); err != nil {
			t.Fatal(err)
		}
	}
	_, manifestSnap, err := loadManifestSnapshot(filepath.Join(root, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	defer manifestSnap.Close()
	stateSnap, err := loadBundleStateSnapshot(filepath.Join(platformRoot, "bundle-state.json"), policy, "win32-x64", archive)
	if err != nil {
		t.Fatal(err)
	}
	defer stateSnap.Close()
	licensePath := filepath.Join(installRoot, filepath.FromSlash(archive.LicensePath))
	licenseSnap, err := captureFileSnapshot(licensePath, 0)
	if err != nil {
		t.Fatal(err)
	}
	before := s.Stats()
	for _, snap := range []*fileSnapshot{manifestSnap, stateSnap, licenseSnap} {
		if snap.toolEvidence != nil {
			t.Fatal("metadata has guarded evidence")
		}
		full := 0
		snap.onFullHash = func() { full++ }
		if err := snap.Verify(); err != nil {
			t.Fatal(err)
		}
		if full != 1 {
			t.Fatalf("metadata Verify hashes=%d", full)
		}
	}
	if s.Stats() != before {
		t.Fatal("metadata verification contributed scoped hits")
	}
	if err := licenseSnap.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(licensePath, []byte("changed license"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := resolveFixtureBundle(t, ctx, runner, config); !errors.Is(err, ErrBundleIntegrity) {
		t.Fatalf("license SHA mismatch accepted: %v", err)
	}
}

func TestCMakeToolDigestClosedScopeFails(t *testing.T) {
	for _, mode := range []string{"closed", "cancelled"} {
		t.Run(mode, func(t *testing.T) {
			parent, cancel := context.WithCancel(context.Background())
			defer cancel()
			ctx, s, err := toolhash.Begin(parent)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			p := currentExecutable(t)
			full := 0
			snap, err := captureToolFileSnapshotWithHook(ctx, p, 0, func() { full++ })
			if err != nil {
				t.Fatal(err)
			}
			defer snap.Close()
			if mode == "closed" {
				if err := s.Close(); err != nil {
					t.Fatal(err)
				}
			} else {
				cancel()
			}
			if err := snap.Verify(); err == nil {
				t.Fatal("invalidated evidence accepted")
			}
			if _, err := captureToolFileSnapshotWithHook(ctx, p, 0, func() { full++ }); err == nil {
				t.Fatal("invalidated capture accepted")
			}
			if _, err := Resolve(ctx, &versionRunner{t: t, version: "4.3.4"}, ResolverConfig{Override: p}); !errors.Is(err, ErrInvalidExecutable) {
				t.Fatalf("resolver error mapping=%v", err)
			}
			if full != 0 {
				t.Fatalf("invalidated evidence fell back: hashes=%d", full)
			}
		})
	}
}

func TestCMakeToolDigestFallbackAndOriginalLimits(t *testing.T) {
	t.Run("no scope", func(t *testing.T) {
		p := currentExecutable(t)
		full := 0
		snap, err := captureToolFileSnapshotWithHook(context.Background(), p, 0, func() { full++ })
		if err != nil {
			t.Fatal(err)
		}
		defer snap.Close()
		if err := snap.Verify(); err != nil {
			t.Fatal(err)
		}
		if snap.toolEvidence != nil || full != 3 {
			t.Fatalf("evidence=%v hashes=%d", snap.toolEvidence, full)
		}
		_, ordinary, err := verifyStandaloneExecutable(p)
		if err != nil {
			t.Fatal(err)
		}
		defer ordinary.Close()
		if ordinary.toolEvidence != nil {
			t.Fatal("legacy wrapper has evidence")
		}
	})
	t.Run("consumer limit", func(t *testing.T) {
		p := filepath.Join(t.TempDir(), "cmake.exe")
		if err := os.WriteFile(p, []byte("fixture"), 0600); err != nil {
			t.Fatal(err)
		}
		ctx, s, err := toolhash.Begin(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		full := 0
		if _, err := captureToolFileSnapshotWithHook(ctx, p, 6, func() { full++ }); err == nil {
			t.Fatal("stricter limit bypassed")
		}
		if full != 0 || s.Stats().FullHashCalls != 0 {
			t.Fatal("oversize file was hashed")
		}
		snap, err := captureToolFileSnapshotWithHook(ctx, p, 7, func() { full++ })
		if err != nil {
			t.Fatal(err)
		}
		defer snap.Close()
		if err := snap.Verify(); err != nil {
			t.Fatal(err)
		}
		if snap.maximum != 7 || snap.toolEvidence == nil || full != 0 {
			t.Fatalf("maximum=%d evidence=%v hashes=%d", snap.maximum, snap.toolEvidence, full)
		}
	})
	t.Run("capacity", func(t *testing.T) {
		ctx, s, err := toolhash.Begin(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		root := t.TempDir()
		for i := 0; i < 128; i++ {
			p := filepath.Join(root, fmt.Sprintf("tool-%d.exe", i))
			if err := os.WriteFile(p, []byte("small fixture"), 0600); err != nil {
				t.Fatal(err)
			}
			snap, err := captureToolFileSnapshot(ctx, p, 0)
			if err != nil {
				t.Fatal(err)
			}
			if err := snap.Close(); err != nil {
				t.Fatal(err)
			}
		}
		if stats := s.Stats(); stats.Retained != 128 || stats.FullHashCalls != 128 {
			t.Fatalf("capacity not reached: %+v", stats)
		}
		p := filepath.Join(root, "overflow.exe")
		if err := os.WriteFile(p, []byte("overflow fixture"), 0600); err != nil {
			t.Fatal(err)
		}
		full := 0
		snap, err := captureToolFileSnapshotWithHook(ctx, p, 0, func() { full++ })
		if err != nil {
			t.Fatal(err)
		}
		defer snap.Close()
		if err := snap.Verify(); err != nil {
			t.Fatal(err)
		}
		if snap.toolEvidence != nil || full != 3 || s.Stats().FullHashCalls != 128 {
			t.Fatalf("overflow evidence=%v hashes=%d scope=%+v", snap.toolEvidence, full, s.Stats())
		}
	})
	t.Run("unsupported initial spelling", func(t *testing.T) {
		p := filepath.Join(t.TempDir(), "cmake.exe")
		if err := os.WriteFile(p, []byte("fixture"), 0600); err != nil {
			t.Fatal(err)
		}
		ctx, s, err := toolhash.Begin(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		full := 0
		snap, err := captureToolFileSnapshotWithHook(ctx, `\\?\`+p, 0, func() { full++ })
		if err != nil {
			t.Fatal(err)
		}
		defer snap.Close()
		if err := snap.Verify(); err != nil {
			t.Fatal(err)
		}
		if snap.toolEvidence != nil || full != 3 || s.Stats().FullHashCalls != 0 {
			t.Fatalf("unsupported spelling evidence=%v hashes=%d scope=%+v", snap.toolEvidence, full, s.Stats())
		}
	})
}

func TestCMakeToolDigestPathDriftStillFails(t *testing.T) {
	root := t.TempDir()
	p := filepath.Join(root, "cmake.exe")
	other := filepath.Join(root, "other.exe")
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
	snap, err := captureToolFileSnapshotWithHook(ctx, p, 0, func() { full++ })
	if err != nil {
		t.Fatal(err)
	}
	defer snap.Close()
	snap.path = other
	if err := snap.Verify(); err == nil {
		t.Fatal("different object with identical bytes accepted")
	}
	if full != 0 {
		t.Fatal("path drift entered byte-hash fallback")
	}
	snap.path = p
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if file, err := os.OpenFile(p, os.O_WRONLY, 0); err == nil {
		file.Close()
		t.Fatal("scope close released consumer-owned lock")
	}
	if err := snap.Close(); err != nil {
		t.Fatal(err)
	}
	snap.file = nil
	file, err := os.OpenFile(p, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal("guard or consumer lock leaked", err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}
