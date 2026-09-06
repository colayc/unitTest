package coverageplatform

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

func TestPublishInstrumentationPublishesExclusiveReadOnlyContract(t *testing.T) {
	root := filepath.Join(t.TempDir(), "root")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	value, err := PublishInstrumentation(root, "coverage.cmake", "line\n", "v1")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := value.IncludePath, filepath.Join(root, "coverage.cmake"); got != want {
		t.Fatalf("path = %q, want %q", got, want)
	}
	sum := sha256.Sum256([]byte("line\n"))
	if got, want := value.SHA256, hex.EncodeToString(sum[:]); got != want {
		t.Fatalf("SHA256 = %q, want %q", got, want)
	}
	if _, err := PublishInstrumentation(root, "coverage.cmake", "line\n", "v1"); err == nil {
		t.Fatal("duplicate publication succeeded")
	}
	info, err := os.Lstat(value.IncludePath)
	if err != nil {
		t.Fatal(err)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("published mode = %v", info.Mode())
	}
}

func TestPublishInstrumentationRejectsAliasedOrReplacedRoot(t *testing.T) {
	target := filepath.Join(t.TempDir(), "target")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(filepath.Dir(target), "link")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err := PublishInstrumentation(link, "coverage.cmake", "x", "v1"); err == nil {
		t.Fatal("symlink root succeeded")
	}
}
