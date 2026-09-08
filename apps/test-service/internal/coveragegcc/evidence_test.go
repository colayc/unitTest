package coveragegcc

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"unit-test-ide.local/test-service/internal/testrun"
)

func TestSealEvidenceDerivesDataOnlyFromSealedNotes(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("GCC evidence is intentionally unsupported on Windows")
	}
	root := evidenceRoot(t)
	writeEvidence(t, root, "a.gcno", "note")
	writeEvidence(t, root, "a.gcda", "data")
	manifest, err := SealEvidence(context.Background(), root, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer manifest.Close()
	if len(manifest.Notes) != 1 || len(manifest.Data) != 1 {
		t.Fatalf("manifest = %#v", manifest)
	}
	if manifest.Notes[0].RelativePath != "a.gcno" || manifest.Data[0].RelativePath != "a.gcda" {
		t.Fatalf("manifest = %#v", manifest)
	}
}

func TestSealEvidenceRejectsUnexpectedOrMissingData(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("GCC evidence is intentionally unsupported on Windows")
	}
	root := evidenceRoot(t)
	writeEvidence(t, root, "a.gcno", "note")
	writeEvidence(t, root, "b.gcda", "data")
	if _, err := SealEvidence(context.Background(), root, nil); err == nil {
		t.Fatal("unexpected data succeeded")
	}
	root = evidenceRoot(t)
	writeEvidence(t, root, "a.gcno", "note")
	if _, err := SealEvidence(context.Background(), root, nil); err == nil {
		t.Fatal("missing data without partial reason succeeded")
	}
	manifest, err := SealEvidence(context.Background(), root, []testrun.InvocationOutcome{{TimedOut: true}})
	if err != nil {
		t.Fatal(err)
	}
	defer manifest.Close()
	if len(manifest.PartialReasons) != 1 {
		t.Fatalf("partial reasons = %#v", manifest.PartialReasons)
	}
}

func TestSealEvidenceHonorsCancellation(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("GCC evidence is intentionally unsupported on Windows")
	}
	root := evidenceRoot(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := SealEvidence(ctx, root, nil); err == nil {
		t.Fatal("cancelled seal succeeded")
	}
}

func TestPrepareEvidenceRejectsZeroNotesAndNewPreparedEntries(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("GCC evidence is intentionally unsupported on Windows")
	}
	t.Run("zero notes", func(t *testing.T) {
		if _, err := PrepareEvidence(evidenceRoot(t)); err == nil {
			t.Fatal("zero-note preparation succeeded")
		}
	})
	t.Run("new note", func(t *testing.T) {
		root := evidenceRoot(t)
		writeEvidence(t, root, "a.gcno", "note")
		prepared, err := PrepareEvidence(root)
		if err != nil {
			t.Fatal(err)
		}
		defer prepared.Close()
		writeEvidence(t, root, "b.gcno", "new")
		if _, err := prepared.Seal(context.Background(), nil); err == nil {
			t.Fatal("new note sealed")
		}
	})
	t.Run("new data", func(t *testing.T) {
		root := evidenceRoot(t)
		writeEvidence(t, root, "a.gcno", "note")
		prepared, err := PrepareEvidence(root)
		if err != nil {
			t.Fatal(err)
		}
		defer prepared.Close()
		writeEvidence(t, root, "b.gcda", "new")
		if _, err := prepared.Seal(context.Background(), []testrun.InvocationOutcome{{Crashed: true}}); err == nil {
			t.Fatal("unexpected data sealed")
		}
	})
}

func TestEvidenceWindowsHasNoSideEffects(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows-only unsupported contract")
	}
	root := evidenceRoot(t)
	writeEvidence(t, root, "a.gcno", "note")
	if _, err := PrepareEvidence(root); err == nil {
		t.Fatal("Windows evidence preparation succeeded")
	}
	if _, err := os.Lstat(filepath.Join(root, "a.gcno")); err != nil {
		t.Fatalf("unsupported call changed evidence: %v", err)
	}
}

func evidenceRoot(t *testing.T) string {
	t.Helper()
	base, err := filepath.Abs(filepath.Join("..", "..", "..", "..", ".task4-scratch", "coveragegcc"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(base, 0o700); err != nil {
		t.Fatal(err)
	}
	directory, err := os.MkdirTemp(base, "case-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(directory) })
	root := filepath.Join(directory, "objects")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	return root
}
func writeEvidence(t *testing.T, root, name, value string) {
	t.Helper()
	path := filepath.Join(root, name)
	if err := os.WriteFile(path, []byte(value), 0o600); err != nil {
		t.Fatal(err)
	}
}
