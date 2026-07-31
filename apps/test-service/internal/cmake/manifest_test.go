package cmake

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"unit-test-ide.local/test-service/internal/unityrunner"
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
		windows.CTestExecutable != "bin/ctest.exe" ||
		windows.LicensePath != "doc/cmake/LICENSE.rst" {
		t.Fatalf("win32-x64 archive = %#v", windows)
	}
	linux := manifest.Archives["linux-x64"]
	if linux.URL != "https://cmake.org/files/v4.3/cmake-4.3.4-linux-x86_64.tar.gz" ||
		linux.RootDirectory != "cmake-4.3.4-linux-x86_64" ||
		linux.Executable != "bin/cmake" ||
		linux.CTestExecutable != "bin/ctest" ||
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

func TestManifestRejectsCTestMissingFromInstalledFiles(t *testing.T) {
	path := writeManifestMutation(t, func(source string) string {
		return strings.Replace(
			source,
			`"bin/ctest": "44794330f90c8cc9c1e7cbf9d81ffb97b4d8bc88e347f8cc5a290ff7d38aec21",`,
			`"bin/not-ctest": "44794330f90c8cc9c1e7cbf9d81ffb97b4d8bc88e347f8cc5a290ff7d38aec21",`,
			1,
		)
	})
	_, err := loadManifest(path)
	if !errors.Is(err, ErrInvalidManifest) {
		t.Fatalf("error = %v, want ErrInvalidManifest", err)
	}
}

func TestUnityRunnerGeneratorIdentityComesFromProductManifest(t *testing.T) {
	platform := "linux"
	if runtime.GOOS == "windows" {
		platform = "win32"
	}
	firstRoot := writeProductManifestFixture(t, platform, []byte("fixed generator bytes"))
	secondRoot := writeProductManifestFixture(t, platform, []byte("fixed generator bytes"))
	t.Setenv("PATH", t.TempDir())

	first, err := ResolveUnityRunnerGenerator(firstRoot, platform, "x64")
	if err != nil {
		t.Fatal(err)
	}
	second, err := ResolveUnityRunnerGenerator(secondRoot, platform, "x64")
	if err != nil {
		t.Fatal(err)
	}
	if first.Path == "" || !filepath.IsAbs(first.Path) ||
		first.RelativePath != expectedUnityRunnerGeneratorPath(platform) ||
		first.Version != unityrunner.CurrentGeneratorVersion ||
		len(first.SHA256) != 64 || len(first.Identity) != 64 {
		t.Fatalf("generator = %#v", first)
	}
	if first.Identity != second.Identity {
		t.Fatalf("install root changed product identity: %q != %q", first.Identity, second.Identity)
	}
	if filepath.Dir(first.Path) == os.Getenv("PATH") {
		t.Fatalf("generator was searched from PATH: %#v", first)
	}
}

func TestUnityRunnerGeneratorManifestRejectsUnsafeOrMismatchedEntry(t *testing.T) {
	platform := "linux"
	if runtime.GOOS == "windows" {
		platform = "win32"
	}
	tests := map[string]struct {
		mutate func(*ProductManifest)
		want   error
	}{
		"path": {mutate: func(value *ProductManifest) {
			value.UnityRunnerGenerator.RelativePath = "../generator"
		}, want: ErrInvalidProductManifest},
		"version": {mutate: func(value *ProductManifest) {
			value.UnityRunnerGenerator.Version = "latest"
		}, want: ErrInvalidProductManifest},
		"digest": {mutate: func(value *ProductManifest) {
			value.UnityRunnerGenerator.SHA256 = strings.Repeat("0", 64)
		}, want: ErrProductIntegrity},
		"platform": {mutate: func(value *ProductManifest) {
			value.UnityRunnerGenerator.Platform = "darwin"
		}, want: ErrInvalidProductManifest},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			root := writeProductManifestFixture(t, platform, []byte("fixed generator bytes"))
			manifestPath := filepath.Join(root, productManifestName)
			var manifest ProductManifest
			data, err := os.ReadFile(manifestPath)
			if err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(data, &manifest); err != nil {
				t.Fatal(err)
			}
			test.mutate(&manifest)
			data, err = json.Marshal(manifest)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(manifestPath, data, 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := ResolveUnityRunnerGenerator(root, platform, "x64"); !errors.Is(err, test.want) {
				t.Fatalf("error = %v, want %v", err, test.want)
			}
		})
	}
}

func writeProductManifestFixture(t *testing.T, platform string, executable []byte) string {
	t.Helper()
	root := t.TempDir()
	relative := expectedUnityRunnerGeneratorPath(platform)
	path := filepath.Join(root, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, executable, 0o700); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(executable)
	manifest := ProductManifest{
		SchemaVersion: 1,
		UnityRunnerGenerator: ProductExecutableManifest{
			RelativePath: relative,
			Version:      unityrunner.CurrentGeneratorVersion,
			SHA256:       hex.EncodeToString(sum[:]),
			Platform:     platform,
			Architecture: "x64",
		},
	}
	encoded, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, productManifestName), encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	return root
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
