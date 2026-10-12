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

// Each alias names the real final regular file through a directory symlink.
// Losing an accepted parent spelling must not lose its bound scope failure.
func newCMakeParentAliasFixture(t *testing.T) (canonical, first, second string) {
	t.Helper()
	root := t.TempDir()
	parent := filepath.Join(root, "real")
	if err := os.Mkdir(parent, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(parent, "cmake.exe")
	if err := os.WriteFile(path, []byte("cmake parent alias fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	canonical = canonicalPath(t, path)
	for _, name := range []string{"first", "second"} {
		if err := os.Symlink(filepath.Dir(canonical), filepath.Join(root, name)); err != nil {
			t.Fatal(err)
		}
	}
	return canonical, filepath.Join(root, "first", "cmake.exe"), filepath.Join(root, "second", "cmake.exe")
}

func TestCMakeToolDigestParentAliases(t *testing.T) {
	for _, mode := range []string{"original", "later", "unbound"} {
		t.Run(mode, func(t *testing.T) {
			canonical, first, second := newCMakeParentAliasFixture(t)
			ctx, scope, err := toolhash.Begin(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			defer scope.Close()
			inputs := []string{first, first}
			removed := first
			if mode == "later" {
				inputs, removed = []string{canonical, second}, second
			} else if mode == "unbound" {
				inputs, removed = []string{canonical, canonical}, second
			}
			var evidence *toolhash.Evidence
			for _, input := range inputs {
				got, snapshot, err := verifyStandaloneExecutableWithContext(ctx, input)
				if err != nil {
					t.Fatal(err)
				}
				defer snapshot.Close()
				if got != canonical || snapshot.path != canonical || snapshot.toolEvidence == nil {
					t.Fatalf("canonical=%q snapshot=%q evidence=%v", got, snapshot.path, snapshot.toolEvidence)
				}
				info, err := os.Stat(input)
				if err != nil || !os.SameFile(snapshot.info, info) {
					t.Fatalf("alias identity: %v", err)
				}
				if err := snapshot.Verify(); err != nil {
					t.Fatal(err)
				}
				evidence = snapshot.toolEvidence
				if err := snapshot.Close(); err != nil {
					t.Fatal(err)
				}
			}
			if stats := scope.Stats(); stats.FullHashCalls != 1 || stats.Retained != 1 || stats.Reuses != 1 {
				t.Fatalf("scope=%+v", stats)
			}
			if file, err := os.OpenFile(canonical, os.O_WRONLY, 0); err == nil {
				file.Close()
				t.Fatal("closing consumers released the scope guard")
			}
			if err := os.Remove(filepath.Dir(removed)); err != nil {
				t.Fatal(err)
			}
			_, _, err = verifyStandaloneExecutableWithContext(ctx, removed)
			if !errors.Is(err, ErrInvalidExecutable) {
				t.Fatalf("mapping: %v", err)
			}
			if mode != "unbound" && scope.Err() == nil {
				t.Error("accepted CMake spelling rejection was not sticky")
			}
			if mode == "unbound" && scope.Err() != nil {
				t.Fatal("unbound missing parent poisoned scope", scope.Err())
			}
			if err := os.Symlink(filepath.Dir(canonical), filepath.Dir(removed)); err != nil {
				t.Fatal(err)
			}
			consumer, err := os.Open(canonical)
			if err != nil {
				t.Fatal(err)
			}
			_, verifyErr := evidence.Verify(ctx, removed, consumer, 0)
			_, protected, digestErr := toolhash.Digest(ctx, removed, consumer, 0)
			if mode == "unbound" {
				if verifyErr != nil || digestErr != nil || !protected || scope.Err() != nil {
					t.Errorf("unbound failure impaired reuse: verify=%v digest=%v protected=%v", verifyErr, digestErr, protected)
				}
			} else if verifyErr == nil || digestErr == nil || protected {
				t.Errorf("restored alias revived reuse: verify=%v digest=%v protected=%v", verifyErr, digestErr, protected)
			}
			if err := consumer.Close(); err != nil {
				t.Fatal(err)
			}
			closeErr := scope.Close()
			if mode == "unbound" && closeErr != nil {
				t.Fatal("unbound failure lost scope health", closeErr)
			} else if mode != "unbound" && closeErr == nil {
				t.Error("bound failure lost at Close")
			}
			if err := os.WriteFile(canonical, []byte("released"), 0600); err != nil {
				t.Fatal("guard leaked", err)
			}
		})
	}
}

func TestCMakeToolDigestInputFallbackBoundaries(t *testing.T) {
	const wantSHA = "ed28abbe35cbfd1f3b382e17bdec5af9b4782a469c950b1b21033c0452a8ff1b"
	for _, mode := range []string{"unscoped", "unsupported-extended", "additional-spelling-budget", "distinct-hard-link"} {
		t.Run(mode, func(t *testing.T) {
			canonical, first, _ := newCMakeParentAliasFixture(t)
			ctx := context.Background()
			var scope *toolhash.Scope
			if mode != "unscoped" {
				var err error
				ctx, scope, err = toolhash.Begin(ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer scope.Close()
			}
			input := first
			if mode == "unsupported-extended" {
				input = `\\?\` + canonical
			}
			if mode == "additional-spelling-budget" {
				_, initial, err := verifyStandaloneExecutableWithContext(ctx, canonical)
				if err != nil {
					t.Fatal(err)
				}
				defer initial.Close()
				if err := initial.Close(); err != nil {
					t.Fatal(err)
				}
				for i := 0; i < 129; i++ {
					parent := filepath.Join(filepath.Dir(first), "..", fmt.Sprintf("budget-%d", i))
					if err := os.Symlink(filepath.Dir(canonical), parent); err != nil {
						t.Fatal(err)
					}
					input = filepath.Join(parent, "cmake.exe")
					got, snapshot, err := verifyStandaloneExecutableWithContext(ctx, input)
					if err != nil {
						t.Fatal(err)
					}
					defer snapshot.Close()
					if got != canonical || snapshot.digest != wantSHA || (snapshot.toolEvidence != nil) != (i < 128) {
						t.Fatalf("spelling %d: canonical=%q evidence=%v SHA=%s", i, got, snapshot.toolEvidence, snapshot.digest)
					}
					if err := snapshot.Close(); err != nil {
						t.Fatal(err)
					}
				}
				if stats := scope.Stats(); stats.FullHashCalls != 1 || stats.Retained != 1 || stats.Reuses != 128 {
					t.Fatalf("budget scope=%+v", stats)
				}
			}
			if mode == "distinct-hard-link" {
				hardLink := filepath.Join(filepath.Dir(canonical), "other.exe")
				if err := os.Link(canonical, hardLink); err != nil {
					t.Fatal(err)
				}
				_, original, err := verifyStandaloneExecutableWithContext(ctx, first)
				if err != nil {
					t.Fatal(err)
				}
				defer original.Close()
				got, distinct, err := verifyStandaloneExecutableWithContext(ctx, hardLink)
				if err != nil {
					t.Fatal(err)
				}
				defer distinct.Close()
				if got != canonicalPath(t, hardLink) || got == canonical || !os.SameFile(original.info, distinct.info) ||
					original.file == distinct.file || original.toolEvidence == distinct.toolEvidence || distinct.digest != wantSHA {
					t.Fatal("hard-link names or independently owned handles collapsed")
				}
				if stats := scope.Stats(); stats.FullHashCalls != 2 || stats.Retained != 2 {
					t.Fatalf("distinct names scope=%+v", stats)
				}
				if err := original.Close(); err != nil {
					t.Fatal(err)
				}
				original.file = nil
				if err := distinct.Verify(); err != nil {
					t.Fatal("one consumer close invalidated another", err)
				}
				if err := scope.Close(); err != nil {
					t.Fatal(err)
				}
				if file, err := os.OpenFile(hardLink, os.O_WRONLY, 0); err == nil {
					file.Close()
					t.Fatal("scope close released independently owned consumer")
				}
				if err := distinct.Close(); err != nil {
					t.Fatal(err)
				}
				distinct.file = nil
				if err := os.WriteFile(canonical, []byte("released"), 0600); err != nil {
					t.Fatal("guard or consumer leaked", err)
				}
				return
			}
			got, resolved, err := verifyStandaloneExecutableWithContext(ctx, input)
			if err != nil {
				t.Fatal(err)
			}
			defer resolved.Close()
			if got != canonical || resolved.toolEvidence != nil || resolved.digest != wantSHA {
				t.Fatalf("fallback canonical=%q evidence=%v SHA=%s", got, resolved.toolEvidence, resolved.digest)
			}
			if err := resolved.Close(); err != nil {
				t.Fatal(err)
			}
			full := 0
			observed, err := captureSnapshot(ctx, input, 0, true, func() { full++ })
			if err != nil {
				t.Fatal(err)
			}
			defer observed.Close()
			if err := observed.Verify(); err != nil {
				t.Fatal(err)
			}
			if observed.toolEvidence != nil || observed.digest != wantSHA || full != 3 {
				t.Fatalf("legacy fallback evidence=%v SHA=%s hashes=%d", observed.toolEvidence, observed.digest, full)
			}
			if err := observed.Close(); err != nil {
				t.Fatal(err)
			}
			if scope != nil {
				if scope.Err() != nil {
					t.Fatal("optional miss poisoned scope", scope.Err())
				}
				if mode == "additional-spelling-budget" {
					_, admitted, err := verifyStandaloneExecutableWithContext(ctx, filepath.Join(filepath.Dir(first), "..", "budget-0", "cmake.exe"))
					if err != nil || admitted.toolEvidence == nil {
						t.Fatalf("budget exhaustion impaired admitted spelling: %v", err)
					}
					if err := admitted.Close(); err != nil {
						t.Fatal(err)
					}
					if err := os.Remove(filepath.Dir(input)); err != nil {
						t.Fatal(err)
					}
					_, _, err = verifyStandaloneExecutableWithContext(ctx, input)
					if !errors.Is(err, ErrInvalidExecutable) || scope.Err() != nil {
						t.Fatalf("unadmitted missing spelling poisoned scope: %v scope=%v", err, scope.Err())
					}
				} else if scope.Stats().FullHashCalls != 0 || scope.Stats().Retained != 0 {
					t.Fatal("unsupported initial input acquired protection")
				}
				if err := scope.Close(); err != nil {
					t.Fatal("optional miss lost scope health", err)
				}
			}
		})
	}
}

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
	if s.Err() != nil {
		t.Fatal("license failure poisoned tool scope")
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
	if s.Err() == nil {
		t.Error("bound path rejection did not poison scope")
	}
	if err := snap.Verify(); err == nil {
		t.Error("restored path revived evidence")
	}
	if _, _, err := toolhash.Digest(ctx, p, snap.file, 0); err == nil {
		t.Error("restored path revived Digest")
	}
	if err := s.Close(); err == nil {
		t.Error("bound path rejection lost at Close")
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

func TestCMakeToolDigestOuterRejections(t *testing.T) {
	for _, mode := range []string{"parent", "containment", "pair", "capture-limit", "unbound-optional", "metadata"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			p := filepath.Join(root, "cmake.exe")
			if err := os.WriteFile(p, []byte("tool"), 0600); err != nil {
				t.Fatal(err)
			}
			ctx, s, err := toolhash.Begin(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			snap, err := captureToolFileSnapshot(ctx, p, 0)
			if err != nil {
				t.Fatal(err)
			}
			defer snap.Close()
			switch mode {
			case "parent":
				err = verifyStandaloneSnapshot(filepath.Join(root, "missing", "cmake.exe"), snap)
			case "containment":
				err = verifyBundleSnapshots(t.TempDir(), []*fileSnapshot{snap})
			case "pair":
				err = verifyStandalonePair(p, snap, filepath.Join(t.TempDir(), "ctest.exe"), snap)
			case "capture-limit":
				_, err = captureToolFileSnapshot(ctx, p, 1)
			case "unbound-optional":
				_, _, err = verifyStandaloneExecutableWithContext(ctx, filepath.Join(root, "missing.exe"))
			case "metadata":
				metadata, e := captureFileSnapshot(p, 0)
				if e != nil {
					t.Fatal(e)
				}
				defer metadata.Close()
				metadata.path = filepath.Join(root, "missing.json")
				err = verifyBundleSnapshots(root, []*fileSnapshot{metadata, snap})
			}
			if err == nil {
				t.Fatal("verification unexpectedly succeeded")
			}
			if mode == "unbound-optional" || mode == "metadata" {
				if s.Err() != nil || snap.Verify() != nil || s.Close() != nil {
					t.Fatal("unbound failure poisoned scope")
				}
			} else if s.Err() == nil || snap.Verify() == nil || s.Close() == nil {
				t.Fatal("outer bound failure was not sticky")
			}
		})
	}
}

func TestCMakeToolDigestLatePathRejection(t *testing.T) {
	root := t.TempDir()
	p, other := filepath.Join(root, "cmake.exe"), filepath.Join(root, "other.exe")
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
	snap, err := captureToolFileSnapshot(ctx, p, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer snap.Close()
	observed := false
	snap.beforePostPathCheck = func() { observed = true; snap.path = other }
	err = snap.Verify()
	snap.path, snap.beforePostPathCheck = p, nil
	if !observed || err == nil {
		t.Fatal("late real path check did not reject other object")
	}
	if s.Err() == nil || snap.Verify() == nil || s.Close() == nil {
		t.Fatal("late rejection was not sticky")
	}
}

func TestCMakeToolDigestBundleToolMismatch(t *testing.T) {
	root := createBundle(t, "win32-x64")
	policy, err := loadManifest(filepath.Join(root, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	archive := policy.Archives["win32-x64"]
	path := filepath.Join(root, policy.CMakeVersion, "win32-x64", filepath.FromSlash(archive.RootDirectory), filepath.FromSlash(archive.Executable))
	if err := os.WriteFile(path, []byte("wrong tool bytes"), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, s, err := toolhash.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	_, err = resolveFixtureBundle(t, ctx, &versionRunner{t: t, version: policy.CMakeVersion}, ResolverConfig{BundleRoot: root, Platform: "win32", Architecture: "x64"})
	if !errors.Is(err, ErrBundleIntegrity) || s.Err() == nil || s.Close() == nil {
		t.Fatalf("bound tool mismatch: %v scope=%v", err, s.Err())
	}
}
