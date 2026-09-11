package coverageplatform

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"runtime"
	"sync"
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
	wantMode := os.FileMode(0o400)
	if runtime.GOOS == "windows" {
		wantMode = 0o444
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm() != wantMode {
		t.Fatalf("published mode = %v", info.Mode())
	}
}

func TestPublishInstrumentationAllowsExactlyOneConcurrentPublisher(t *testing.T) {
	root := filepath.Join(t.TempDir(), "root")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	results := make(chan error, 2)
	var group sync.WaitGroup
	for range 2 {
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			_, err := PublishInstrumentation(root, "coverage.cmake", "line\n", "v1")
			results <- err
		}()
	}
	close(start)
	group.Wait()
	close(results)
	succeeded := 0
	for err := range results {
		if err == nil {
			succeeded++
		}
	}
	if succeeded != 1 {
		t.Fatalf("successful publishers = %d, want 1", succeeded)
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

func TestPublishInstrumentationRejectsSymlinkDestinationWithoutWriting(t *testing.T) {
	root := filepath.Join(t.TempDir(), "root")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "target")
	if err := os.WriteFile(target, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(root, "coverage.cmake")
	if err := os.Symlink(target, destination); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err := PublishInstrumentation(root, "coverage.cmake", "line\n", "v1"); err == nil {
		t.Fatal("symlink destination succeeded")
	}
	contents, err := os.ReadFile(target)
	if err != nil || string(contents) != "keep" {
		t.Fatalf("destination target changed: %q, %v", contents, err)
	}
}
