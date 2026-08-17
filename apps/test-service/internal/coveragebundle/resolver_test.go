package coveragebundle

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func createWindowsJunction(link, target string) error {
	return exec.Command("cmd.exe", "/c", "mklink", "/J", link, target).Run()
}

func TestBundleResolverRejectsMissingREADYAndDigestMismatch(t *testing.T) {
	t.Run("missing READY", func(t *testing.T) {
		productRoot, bundleRoot := createBundleFixture(t)
		if err := os.Remove(filepath.Join(bundleRoot, readyName)); err != nil {
			t.Fatal(err)
		}
		if pin, err := Resolve(productRoot); err == nil {
			_ = pin.Close()
			t.Fatal("Resolve accepted a bundle without READY")
		}
	})

	t.Run("invalid READY", func(t *testing.T) {
		productRoot, bundleRoot := createBundleFixture(t)
		if err := os.WriteFile(filepath.Join(bundleRoot, readyName), []byte("not-ready\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if pin, err := Resolve(productRoot); err == nil {
			_ = pin.Close()
			t.Fatal("Resolve accepted invalid READY contents")
		}
	})

	t.Run("digest mismatch", func(t *testing.T) {
		productRoot, bundleRoot := createBundleFixture(t)
		if err := os.WriteFile(filepath.Join(bundleRoot, "app", "gcovr-runner.pyz"), []byte("tampered"), 0o600); err != nil {
			t.Fatal(err)
		}
		if pin, err := Resolve(productRoot); err == nil {
			_ = pin.Close()
			t.Fatal("Resolve accepted a digest mismatch")
		}
	})
}

func TestBundleResolverUsesOnlyFixedProductInstallationPath(t *testing.T) {
	productRoot, bundleRoot := createBundleFixture(t)
	t.Setenv("PATH", filepath.Dir(bundleRoot))
	if pin, err := Resolve(bundleRoot); err == nil {
		_ = pin.Close()
		t.Fatal("Resolve treated a caller-selected bundle directory as the product root")
	}
	pin, err := Resolve(productRoot)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pin.Close() })
	installation := pin.Installation()
	if installation.Root != bundleRoot || installation.Runner != filepath.Join(bundleRoot, "app", "gcovr-runner.pyz") {
		t.Fatalf("Installation() = %#v", installation)
	}
	wantPython := filepath.Join(bundleRoot, "python", "bin", "python3")
	if runtime.GOOS == "windows" {
		wantPython = filepath.Join(bundleRoot, "python", "python.exe")
	}
	if installation.Python != wantPython || installation.PythonVersion != "3.14.6" || installation.GcovrVersion != "8.6" || len(installation.ManifestSHA256) != 64 {
		t.Fatalf("Installation() = %#v", installation)
	}
}

func TestBundleResolverRejectsClosedSetMismatch(t *testing.T) {
	productRoot, bundleRoot := createBundleFixture(t)
	if err := os.WriteFile(filepath.Join(bundleRoot, "python", "unexpected.dll"), []byte("unlisted"), 0o600); err != nil {
		t.Fatal(err)
	}
	if pin, err := Resolve(productRoot); err == nil {
		_ = pin.Close()
		t.Fatal("Resolve accepted an unlisted output")
	}
}

func TestBundleResolverRejectsActualEntryBudgetOverflowAndRollsBackPins(t *testing.T) {
	productRoot, bundleRoot := createBundleFixture(t)
	overflowRoot := filepath.Join(bundleRoot, "python")
	for index := 0; index < maximumActualEntries; index++ {
		name := filepath.Join(overflowRoot, fmt.Sprintf("overflow-%04d.dat", index))
		if err := os.WriteFile(name, nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if pin, err := Resolve(productRoot); err == nil {
		_ = pin.Close()
		t.Fatal("Resolve accepted an actual tree above the entry budget")
	}
	moved := productRoot + "-moved"
	if err := os.Rename(productRoot, moved); err != nil {
		t.Fatalf("failed Resolve leaked a persistent handle: %v", err)
	}
}

func TestBundleResolverRejectsDirectoryBudgetOverflowAndRollsBackPins(t *testing.T) {
	productRoot, bundleRoot := createBundleFixture(t)
	overflowRoot := filepath.Join(bundleRoot, "python")
	for index := 0; index < maximumBundleDirectories; index++ {
		name := filepath.Join(overflowRoot, fmt.Sprintf("directory-overflow-%04d", index))
		if err := os.Mkdir(name, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if pin, err := Resolve(productRoot); err == nil {
		_ = pin.Close()
		t.Fatal("Resolve accepted an actual tree above the directory budget")
	} else if !strings.Contains(err.Error(), "bundle directory budget exceeded") {
		t.Fatalf("Resolve error = %v, want directory budget", err)
	}
	moved := productRoot + "-moved"
	if err := os.Rename(productRoot, moved); err != nil {
		t.Fatalf("directory-overflow rollback leaked a persistent handle: %v", err)
	}
}

func TestBundleResolverRejectsPersistentHandleBudgetOverflowAndRollsBack(t *testing.T) {
	sourceProduct, _ := createBundleFixture(t)
	deepRoot := testScratchDir(t)
	for index := 0; index < 40; index++ {
		deepRoot = filepath.Join(deepRoot, fmt.Sprintf("d%02d", index))
	}
	if err := os.MkdirAll(deepRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	productRoot := filepath.Join(deepRoot, "product")
	if err := os.Rename(sourceProduct, productRoot); err != nil {
		t.Fatal(err)
	}
	key, err := currentPlatformKey()
	if err != nil {
		t.Fatal(err)
	}
	bundleRoot := filepath.Join(productRoot, "coverage-bundle", key)
	existingEntries := countFixtureEntries(t, bundleRoot)
	productComponents := len(strings.FieldsFunc(strings.TrimPrefix(productRoot, filepath.VolumeName(productRoot)), func(value rune) bool {
		return value == '/' || value == '\\'
	}))
	ancestorHandles := 1 + productComponents
	extraFiles := maximumPersistentHandles - ancestorHandles - 2 - existingEntries + 1
	if extraFiles <= 0 || existingEntries+extraFiles > maximumActualEntries {
		t.Fatalf("invalid handle-overflow fixture: ancestors=%d entries=%d extras=%d", ancestorHandles, existingEntries, extraFiles)
	}
	overflowRoot := filepath.Join(bundleRoot, "python")
	for index := 0; index < extraFiles; index++ {
		name := filepath.Join(overflowRoot, fmt.Sprintf("handle-overflow-%04d.dat", index))
		if err := os.WriteFile(name, nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if pin, err := Resolve(productRoot); err == nil {
		_ = pin.Close()
		t.Fatal("Resolve accepted a tree above the persistent handle budget")
	} else if !strings.Contains(err.Error(), "persistent handle budget exceeded") {
		t.Fatalf("Resolve error = %v, want persistent handle budget", err)
	}
	moved := productRoot + "-moved"
	if err := os.Rename(productRoot, moved); err != nil {
		t.Fatalf("handle-overflow rollback leaked a persistent handle: %v", err)
	}
}

func countFixtureEntries(t *testing.T, root string) int {
	t.Helper()
	count := 0
	if err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path != root {
			count++
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return count
}

func TestBundleResolverRejectsOversizedSparseOutputBeforeHashing(t *testing.T) {
	productRoot, bundleRoot := createBundleFixture(t)
	runner := filepath.Join(bundleRoot, "app", "gcovr-runner.pyz")
	if err := os.Truncate(runner, maximumRegularFileBytes+1); err != nil {
		t.Fatal(err)
	}
	if pin, err := Resolve(productRoot); err == nil {
		_ = pin.Close()
		t.Fatal("Resolve accepted an output above the single-file budget")
	}
}

func TestBundleResolverFailsClosedOnTOCTOU(t *testing.T) {
	productRoot, bundleRoot := createBundleFixture(t)
	_, err := resolveBundle(productRoot, resolveHooks{afterManifest: func() {
		_ = os.WriteFile(filepath.Join(bundleRoot, "app", "gcovr-runner.pyz"), []byte("changed during resolve"), 0o600)
	}})
	if err == nil {
		t.Fatal("Resolve accepted a file changed after manifest verification")
	}
}

func TestBundleResolverRejectsSymlinkOrJunctionEscape(t *testing.T) {
	key, err := currentPlatformKey()
	if err != nil {
		t.Skip(err)
	}
	base := testScratchDir(t)
	targetProduct, _ := createBundleFixture(t)
	productRoot := filepath.Join(base, "product")
	if err := os.Mkdir(productRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(targetProduct, "coverage-bundle")
	link := filepath.Join(productRoot, "coverage-bundle")
	if runtime.GOOS == "windows" {
		if err := createWindowsJunction(link, target); err != nil {
			t.Skipf("directory junctions are unavailable: %v", err)
		}
	} else if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if pin, err := Resolve(productRoot); err == nil {
		_ = pin.Close()
		t.Fatalf("Resolve accepted %s bundle escape through %s", key, link)
	}
}

func TestBundleResolverRejectsIntermediateProductAncestorSymlinkOrJunction(t *testing.T) {
	targetProduct, _ := createBundleFixture(t)
	targetParent := filepath.Dir(targetProduct)
	base := testScratchDir(t)
	alias := filepath.Join(base, "intermediate-alias")
	if runtime.GOOS == "windows" {
		if err := createWindowsJunction(alias, targetParent); err != nil {
			t.Skipf("directory junctions are unavailable: %v", err)
		}
	} else if err := os.Symlink(targetParent, alias); err != nil {
		t.Fatal(err)
	}
	productRoot := filepath.Join(alias, filepath.Base(targetProduct))
	if pin, err := Resolve(productRoot); err == nil {
		_ = pin.Close()
		t.Fatalf("Resolve accepted intermediate product ancestor alias %q", alias)
	}
}
