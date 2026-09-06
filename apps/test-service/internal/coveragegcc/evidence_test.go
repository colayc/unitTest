package coveragegcc

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
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

func evidenceRoot(t *testing.T) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "objects")
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
