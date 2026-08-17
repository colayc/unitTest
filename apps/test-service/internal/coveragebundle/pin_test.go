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

func TestPinResourceBudgetsRejectEntryDirectoryAndHandleOverflow(t *testing.T) {
	handles := resourceBudget{handles: maximumPersistentHandles}
	if err := handles.reserveHandle(); err == nil {
		t.Fatal("persistent handle budget accepted an overflow")
	}
	entries := resourceBudget{entries: maximumActualEntries}
	if err := entries.recordEntry(false); err == nil {
		t.Fatal("actual entry budget accepted an overflow")
	}
	directories := resourceBudget{directories: maximumBundleDirectories}
	if err := directories.recordEntry(true); err == nil {
		t.Fatal("directory budget accepted an overflow")
	}
	hashBytes := resourceBudget{hashBytes: maximumTotalHashBytes}
	if err := hashBytes.addHashBytes(1); err == nil {
		t.Fatal("total hash byte budget accepted an overflow")
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

func TestPinPinsAndDetectsOrPreventsProductAncestorReplacement(t *testing.T) {
	sourceProduct, _ := createBundleFixture(t)
	base := testScratchDir(t)
	ancestor := filepath.Join(base, "stable-ancestor")
	if err := os.Mkdir(ancestor, 0o700); err != nil {
		t.Fatal(err)
	}
	productRoot := filepath.Join(ancestor, "product")
	if err := os.Rename(sourceProduct, productRoot); err != nil {
		t.Fatal(err)
	}
	resolved, err := resolveBundle(productRoot, resolveHooks{})
	if err != nil {
		t.Fatal(err)
	}
	defer resolved.Close()
	ancestorPinned := false
	for _, directory := range resolved.directories {
		if filepath.Clean(directory.path) == filepath.Clean(ancestor) {
			ancestorPinned = true
			break
		}
	}
	if !ancestorPinned {
		t.Fatal("intermediate product ancestor did not retain an opened identity")
	}
	moved := filepath.Join(base, "moved-ancestor")
	renameErr := os.Rename(ancestor, moved)
	if runtime.GOOS == "windows" {
		if renameErr == nil {
			t.Fatal("Windows product ancestor replacement succeeded while pinned")
		}
		if err := resolved.Verify(); err != nil {
			t.Fatalf("Verify after blocked ancestor replacement: %v", err)
		}
		return
	}
	if renameErr != nil {
		t.Fatal(renameErr)
	}
	if err := resolved.Verify(); !errors.Is(err, ErrBundleIntegrity) {
		t.Fatalf("Verify after product ancestor replacement = %v, want ErrBundleIntegrity", err)
	}
}

func TestPinLinuxPythonChmodFailsClosed(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux executable-mode runtime evidence requires Linux")
	}
	productRoot, _ := createBundleFixture(t)
	resolved, err := Resolve(productRoot)
	if err != nil {
		t.Fatal(err)
	}
	defer resolved.Close()
	python := resolved.Installation().Python
	if err := os.Chmod(python, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := resolved.Verify(); !errors.Is(err, ErrBundleIntegrity) {
		t.Fatalf("Verify after chmod = %v, want ErrBundleIntegrity", err)
	}
}
