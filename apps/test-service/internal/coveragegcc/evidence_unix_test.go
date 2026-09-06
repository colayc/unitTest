//go:build !windows

package coveragegcc

import (
	"context"
	"os"
	"path/filepath"
	"testing"
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
