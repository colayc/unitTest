package coveragebundle

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
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
	base := t.TempDir()
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
