//go:build !windows

package coveragegcc

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
	"unit-test-ide.local/test-service/internal/testrun"
)

func TestSealEvidenceRejectsUnknownAndSpecialEntries(t *testing.T) {
	for _, name := range []string{"foreign.txt", "a.GCNO"} {
		t.Run(name, func(t *testing.T) {
			root := evidenceRoot(t)
			writeEvidence(t, root, name, "x")
			if _, err := SealEvidence(context.Background(), root, nil); err == nil {
				t.Fatal("invalid entry succeeded")
			}
		})
	}
	t.Run("symlink", func(t *testing.T) {
		root := evidenceRoot(t)
		target := filepath.Join(root, "target")
		writeEvidence(t, root, "target", "x")
		if err := os.Symlink(target, filepath.Join(root, "a.gcno")); err != nil {
			t.Skipf("symlink unavailable: %v", err)
		}
		if _, err := SealEvidence(context.Background(), root, nil); err == nil {
			t.Fatal("symlink succeeded")
		}
	})
}

func TestSealEvidenceAcceptsNestedObjectScopedPairs(t *testing.T) {
	root := evidenceRoot(t)
	if err := os.Mkdir(filepath.Join(root, "nested"), 0o700); err != nil {
		t.Fatal(err)
	}
	writeEvidence(t, filepath.Join(root, "nested"), "a.gcno", "n")
	writeEvidence(t, filepath.Join(root, "nested"), "a.gcda", "d")
	manifest, err := SealEvidence(context.Background(), root, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer manifest.Close()
	if got := manifest.Notes[0].RelativePath; got != "nested/a.gcno" {
		t.Fatalf("note path = %q", got)
	}
}

func TestManifestRejectsRootReplacement(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "objects")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	writeEvidence(t, root, "a.gcno", "n")
	writeEvidence(t, root, "a.gcda", "d")
	manifest, err := SealEvidence(context.Background(), root, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer manifest.Close()
	if err := os.Rename(root, filepath.Join(base, "old")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := manifest.Verify(); err == nil {
		t.Fatal("replaced root verified")
	}
}

func TestManifestRejectsPublicMutationAndFileReplacement(t *testing.T) {
	root := evidenceRoot(t)
	writeEvidence(t, root, "a.gcno", "n")
	writeEvidence(t, root, "a.gcda", "d")
	manifest, err := SealEvidence(context.Background(), root, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer manifest.Close()
	manifest.Notes[0].SHA256 = "tampered"
	if err := manifest.Verify(); err == nil {
		t.Fatal("public manifest mutation verified")
	}

	manifest, err = SealEvidence(context.Background(), root, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer manifest.Close()
	manifest.PartialReasons = append(manifest.PartialReasons, "test_crashed")
	if err := manifest.Verify(); err == nil {
		t.Fatal("public partial reason mutation verified")
	}

	manifest, err = SealEvidence(context.Background(), root, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer manifest.Close()
	// Keep the replacement inode allocated before removing the original so the
	// ABA check is deterministic even when the filesystem would reuse inodes.
	writeEvidence(t, root, "replacement.gcda", "d")
	if err := os.Remove(filepath.Join(root, "a.gcda")); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(root, "replacement.gcda"), filepath.Join(root, "a.gcda")); err != nil {
		t.Fatal(err)
	}
	if err := manifest.Verify(); err == nil {
		t.Fatal("ABA replacement verified")
	}
}

func TestEvidenceRejectsUnsafeFilesystemShapes(t *testing.T) {
	for _, fixture := range []struct {
		name  string
		setup func(*testing.T, string)
	}{
		{"hard link", func(t *testing.T, root string) {
			writeEvidence(t, root, "a.gcno", "n")
			if err := os.Link(filepath.Join(root, "a.gcno"), filepath.Join(root, "b.gcno")); err != nil {
				t.Fatal(err)
			}
		}},
		{"case collision", func(t *testing.T, root string) {
			writeEvidence(t, root, "a.gcno", "n")
			writeEvidence(t, root, "A.gcno", "n")
		}},
		{"fifo", func(t *testing.T, root string) {
			if err := unix.Mkfifo(filepath.Join(root, "a.gcno"), 0o600); err != nil {
				t.Skipf("fifo unavailable: %v", err)
			}
		}},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			root := evidenceRoot(t)
			fixture.setup(t, root)
			if _, err := SealEvidence(context.Background(), root, []testrun.InvocationOutcome{{Crashed: true}}); err == nil {
				t.Fatal("unsafe evidence sealed")
			}
		})
	}
}

func TestPrepareEvidenceRemovesOnlySealedStaleDataAndManifestCloseRemovesOnlyListedData(t *testing.T) {
	root := evidenceRoot(t)
	writeEvidence(t, root, "a.gcno", "n")
	writeEvidence(t, root, "a.gcda", "stale")
	prepared, err := PrepareEvidence(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(root, "a.gcda")); !os.IsNotExist(err) {
		t.Fatalf("stale data still exists: %v", err)
	}
	writeEvidence(t, root, "a.gcda", "current")
	manifest, err := prepared.Seal(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	writeEvidence(t, root, "unlisted.gcda", "racer")
	if err := manifest.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(root, "a.gcda")); !os.IsNotExist(err) {
		t.Fatalf("listed data not removed: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(root, "unlisted.gcda")); err != nil {
		t.Fatalf("unlisted data removed: %v", err)
	}
}

func TestEvidenceRejectsDepthCountAndByteBounds(t *testing.T) {
	t.Run("depth", func(t *testing.T) {
		root := evidenceRoot(t)
		current := root
		for i := 0; i <= maxEvidenceDepth; i++ {
			current = filepath.Join(current, "d")
			if err := os.Mkdir(current, 0o700); err != nil {
				t.Fatal(err)
			}
		}
		writeEvidence(t, current, "a.gcno", "n")
		if _, err := SealEvidence(context.Background(), root, []testrun.InvocationOutcome{{Crashed: true}}); err == nil {
			t.Fatal("deep evidence sealed")
		}
	})
	t.Run("count", func(t *testing.T) {
		root := evidenceRoot(t)
		for i := 0; i <= maxEvidenceEntries; i++ {
			writeEvidence(t, root, fmt.Sprintf("%05d.gcno", i), "n")
		}
		if _, err := SealEvidence(context.Background(), root, []testrun.InvocationOutcome{{Crashed: true}}); err == nil {
			t.Fatal("too many entries sealed")
		}
	})
	t.Run("bytes", func(t *testing.T) {
		root := evidenceRoot(t)
		path := filepath.Join(root, "a.gcno")
		if err := os.WriteFile(path, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Truncate(path, maxEvidenceBytes+1); err != nil {
			t.Fatal(err)
		}
		if _, err := SealEvidence(context.Background(), root, []testrun.InvocationOutcome{{Crashed: true}}); err == nil {
			t.Fatal("oversized evidence sealed")
		}
	})
}

func TestSealEvidenceHonorsCancellationDuringTraversalAndHash(t *testing.T) {
	t.Run("traversal", func(t *testing.T) {
		root := evidenceRoot(t)
		writeEvidence(t, root, "a.gcno", "a")
		writeEvidence(t, root, "b.gcno", "b")
		ctx, cancel := context.WithCancel(context.Background())
		oldHook := evidenceScannedEntryForTest
		called := false
		evidenceScannedEntryForTest = func() {
			if !called {
				called = true
				cancel()
			}
		}
		t.Cleanup(func() { evidenceScannedEntryForTest = oldHook; cancel() })
		if _, err := SealEvidence(ctx, root, []testrun.InvocationOutcome{{Crashed: true}}); err == nil {
			t.Fatal("cancelled traversal sealed")
		}
		if !called {
			t.Fatal("traversal cancellation hook was not reached")
		}
	})
	t.Run("hash", func(t *testing.T) {
		root := evidenceRoot(t)
		writeEvidence(t, root, "a.gcno", string(make([]byte, 128*1024)))
		ctx, cancel := context.WithCancel(context.Background())
		oldHook := evidenceHashedChunkForTest
		called := false
		evidenceHashedChunkForTest = func() {
			if !called {
				called = true
				cancel()
			}
		}
		t.Cleanup(func() { evidenceHashedChunkForTest = oldHook; cancel() })
		if _, err := SealEvidence(ctx, root, []testrun.InvocationOutcome{{Crashed: true}}); err == nil {
			t.Fatal("cancelled hash sealed")
		}
		if !called {
			t.Fatal("hash cancellation hook was not reached")
		}
	})
}

func TestManifestCloseIsIdempotentAndRejectsReplacementWithoutDeletion(t *testing.T) {
	t.Run("idempotent", func(t *testing.T) {
		root := evidenceRoot(t)
		writeEvidence(t, root, "a.gcno", "n")
		writeEvidence(t, root, "a.gcda", "d")
		manifest, err := SealEvidence(context.Background(), root, nil)
		if err != nil {
			t.Fatal(err)
		}
		if err := manifest.Close(); err != nil {
			t.Fatal(err)
		}
		if err := manifest.Close(); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("replacement", func(t *testing.T) {
		root := evidenceRoot(t)
		writeEvidence(t, root, "a.gcno", "n")
		writeEvidence(t, root, "a.gcda", "d")
		manifest, err := SealEvidence(context.Background(), root, nil)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(filepath.Join(root, "a.gcda")); err != nil {
			t.Fatal(err)
		}
		writeEvidence(t, root, "a.gcda", "replacement")
		if err := manifest.Close(); err == nil {
			t.Fatal("replacement cleanup succeeded")
		}
		contents, err := os.ReadFile(filepath.Join(root, "a.gcda"))
		if err != nil || string(contents) != "replacement" {
			t.Fatalf("replacement deleted or changed: %q, %v", contents, err)
		}
	})
}
