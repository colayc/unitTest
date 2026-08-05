package coveragebundle

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestPinInstallationReturnsImmutableCloneAndCloseIsIdempotent(t *testing.T) {
	productRoot, _ := createBundleFixture(t)
	pin, err := Resolve(productRoot)
	if err != nil {
		t.Fatal(err)
	}
	first := pin.Installation()
	first.Root = "mutated"
	if second := pin.Installation(); second.Root == first.Root || second.Root == "mutated" {
		t.Fatalf("Installation mutation leaked: %#v", second)
	}
	if err := pin.Close(); err != nil {
		t.Fatal(err)
	}
	if err := pin.Close(); err != nil {
		t.Fatalf("second Close() = %v", err)
	}
	if err := pin.Verify(); !errors.Is(err, ErrBundleIntegrity) {
		t.Fatalf("Verify after Close error = %v, want ErrBundleIntegrity", err)
	}
}

func TestPinDetectsOrPreventsFileReplacement(t *testing.T) {
	productRoot, bundleRoot := createBundleFixture(t)
	pin, err := Resolve(productRoot)
	if err != nil {
		t.Fatal(err)
	}
	defer pin.Close()
	runner := filepath.Join(bundleRoot, "app", "gcovr-runner.pyz")
	replacement := filepath.Join(bundleRoot, "app", "replacement.pyz")
	if err := os.WriteFile(replacement, []byte("replacement"), 0o600); err != nil {
		t.Fatal(err)
	}
	replaceErr := os.Rename(replacement, runner)
	if runtime.GOOS == "windows" {
		if replaceErr == nil {
			t.Fatal("Windows file replacement succeeded while the bundle was pinned")
		}
		if err := os.Remove(replacement); err != nil {
			t.Fatal(err)
		}
		if err := pin.Verify(); err != nil {
			t.Fatalf("Verify after blocked replacement: %v", err)
		}
		return
	}
	if replaceErr != nil {
		t.Fatal(replaceErr)
	}
	if err := pin.Verify(); !errors.Is(err, ErrBundleIntegrity) {
		t.Fatalf("Verify after replacement = %v, want ErrBundleIntegrity", err)
	}
}

func TestPinDetectsOrPreventsDirectoryReplacement(t *testing.T) {
	productRoot, bundleRoot := createBundleFixture(t)
	pin, err := Resolve(productRoot)
	if err != nil {
		t.Fatal(err)
	}
	defer pin.Close()
	app := filepath.Join(bundleRoot, "app")
	moved := filepath.Join(bundleRoot, "app-moved")
	renameErr := os.Rename(app, moved)
	if runtime.GOOS == "windows" {
		if renameErr == nil {
			t.Fatal("Windows directory replacement succeeded while the bundle was pinned")
		}
		if err := pin.Verify(); err != nil {
			t.Fatalf("Verify after blocked replacement: %v", err)
		}
		return
	}
	if renameErr != nil {
		t.Fatal(renameErr)
	}
	if err := os.Mkdir(app, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := pin.Verify(); !errors.Is(err, ErrBundleIntegrity) {
		t.Fatalf("Verify after directory replacement = %v, want ErrBundleIntegrity", err)
	}
}

func TestPinVerifyRechecksDigestsBeforeAndAfter(t *testing.T) {
	productRoot, bundleRoot := createBundleFixture(t)
	resolved, err := resolveBundle(productRoot, resolveHooks{})
	if err != nil {
		t.Fatal(err)
	}
	defer resolved.Close()
	resolved.verifyHook = func() {
		_ = os.WriteFile(filepath.Join(bundleRoot, "licenses", "NOTICE.txt"), []byte("changed between verify passes"), 0o600)
	}
	err = resolved.Verify()
	if runtime.GOOS == "windows" {
		if err != nil {
			t.Fatalf("Windows mutation should be blocked by the opened identity: %v", err)
		}
		return
	}
	if !errors.Is(err, ErrBundleIntegrity) {
		t.Fatalf("Verify() = %v, want ErrBundleIntegrity", err)
	}
}
