package cmake

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestManifestMatchesProductionBundleShape(t *testing.T) {
	manifest, err := loadManifest(filepath.Join("testdata", "bundle-manifest.valid.json"))
	if err != nil {
		t.Fatalf("loadManifest() error = %v", err)
	}
	if manifest.SchemaVersion != 1 || manifest.CMakeVersion != "4.3.4" || manifest.License != "BSD-3-Clause" {
		t.Fatalf("manifest header = %#v", manifest)
	}

	windows := manifest.Archives["win32-x64"]
	if windows.URL != "https://cmake.org/files/v4.3/cmake-4.3.4-windows-x86_64.zip" ||
		windows.RootDirectory != "cmake-4.3.4-windows-x86_64" ||
		windows.Executable != "bin/cmake.exe" ||
		windows.LicensePath != "doc/cmake/LICENSE.rst" {
		t.Fatalf("win32-x64 archive = %#v", windows)
	}
	linux := manifest.Archives["linux-x64"]
	if linux.URL != "https://cmake.org/files/v4.3/cmake-4.3.4-linux-x86_64.tar.gz" ||
		linux.RootDirectory != "cmake-4.3.4-linux-x86_64" ||
		linux.Executable != "bin/cmake" ||
		linux.LicensePath != "doc/cmake/LICENSE.rst" {
		t.Fatalf("linux-x64 archive = %#v", linux)
	}
}

func TestManifestRejectsUnknownFields(t *testing.T) {
	path := writeManifestMutation(t, func(source string) string {
		return strings.Replace(source, `"schemaVersion": 1,`, `"schemaVersion": 1, "command": "sh",`, 1)
	})

	_, err := loadManifest(path)
	if !errors.Is(err, ErrInvalidManifest) {
		t.Fatalf("error = %v, want ErrInvalidManifest", err)
	}
}

func TestManifestRejectsUnsafeInstalledPath(t *testing.T) {
	path := writeManifestMutation(t, func(source string) string {
		return strings.Replace(source, `"bin/cmake": "d323`, `"../bin/cmake": "d323`, 1)
	})

	_, err := loadManifest(path)
	if !errors.Is(err, ErrInvalidManifest) {
		t.Fatalf("error = %v, want ErrInvalidManifest", err)
	}
}

func TestManifestRejectsUnsupportedPlatformKey(t *testing.T) {
	path := writeManifestMutation(t, func(source string) string {
		return strings.Replace(source, `"linux-x64": {`, `"darwin-x64": {`, 1)
	})

	_, err := loadManifest(path)
	if !errors.Is(err, ErrInvalidManifest) {
		t.Fatalf("error = %v, want ErrInvalidManifest", err)
	}
}

func TestManifestRejectsExecutableMissingFromInstalledFiles(t *testing.T) {
	path := writeManifestMutation(t, func(source string) string {
		return strings.Replace(source, `"bin/cmake": "d323cee6566aca642fd337ba819efd61cc3883fac87a29d82dc21bae04169481",`, `"bin/not-cmake": "d323cee6566aca642fd337ba819efd61cc3883fac87a29d82dc21bae04169481",`, 1)
	})

	_, err := loadManifest(path)
	if !errors.Is(err, ErrInvalidManifest) {
		t.Fatalf("error = %v, want ErrInvalidManifest", err)
	}
}

func writeManifestMutation(t *testing.T, mutate func(string) string) string {
	t.Helper()
	source, err := os.ReadFile(filepath.Join("testdata", "bundle-manifest.valid.json"))
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	path := filepath.Join(t.TempDir(), "manifest.json")
	if err := os.WriteFile(path, []byte(mutate(string(source))), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	return path
}
