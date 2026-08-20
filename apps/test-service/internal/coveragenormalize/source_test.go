package coveragenormalize

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestDigestSourceBindsIdentityAndComputesSHA256(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "src", "file.cpp")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	contents := []byte("int main() { return 0; }\n")
	if err := os.WriteFile(path, contents, 0o600); err != nil {
		t.Fatal(err)
	}
	binding, err := DigestSource(root, path, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	expected := sha256.Sum256(contents)
	if binding.SHA256 != hex.EncodeToString(expected[:]) || binding.NativePath != path {
		t.Fatalf("binding = %#v", binding)
	}
}

func TestDigestSourceRejectsOversizedAndNonRegularFiles(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "src.cpp")
	if err := os.WriteFile(path, []byte("0123456789"), 0o600); err != nil {
		t.Fatal(err)
	}
	limits := DefaultLimits()
	limits.MaxInputBytes = 5
	if _, err := DigestSource(root, path, limits); !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("oversized error = %v", err)
	}
	directory := filepath.Join(root, "directory")
	if err := os.Mkdir(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := DigestSource(root, directory, DefaultLimits()); !errors.Is(err, ErrSourceIdentity) {
		t.Fatalf("directory error = %v", err)
	}
}

func TestDigestSourceRejectsInvalidLimitsAndEscapedPath(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "src.cpp")
	if err := os.WriteFile(path, []byte("ok"), 0o600); err != nil {
		t.Fatal(err)
	}
	invalid := DefaultLimits()
	invalid.MaxFiles = 0
	if _, err := DigestSource(root, path, invalid); !errors.Is(err, ErrInvalidLimits) {
		t.Fatalf("invalid limits error = %v", err)
	}
	if _, err := DigestSource(root, filepath.Join(filepath.Dir(root), "outside.cpp"), DefaultLimits()); !errors.Is(err, ErrInvalidSourcePath) {
		t.Fatalf("outside error = %v", err)
	}
}

func TestSourceSnapshotRejectsReplacementBetweenIdentityAndDigest(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "src.cpp")
	if err := os.WriteFile(path, []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	snapshot, err := openSourceSnapshot(root, path, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	defer snapshot.close()

	original := filepath.Join(root, "original.cpp")
	if err := os.Rename(path, original); err != nil {
		if runtime.GOOS == "windows" {
			// The retained Windows handle denies rename/delete sharing, so the
			// replacement is prevented before a digest can observe another file.
			return
		}
		t.Fatalf("cannot exercise retained-handle replacement: %v", err)
	}
	if err := os.WriteFile(path, []byte("replacement"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := snapshot.digest(); !errors.Is(err, ErrSourceIdentity) {
		t.Fatalf("digest after path replacement error = %v, want ErrSourceIdentity", err)
	}
}
