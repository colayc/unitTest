package testgenbundle

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func fixtureDigest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func writeFixture(t *testing.T, root, path string, bytes []byte, mode os.FileMode) {
	t.Helper()
	absolute := filepath.Join(root, filepath.FromSlash(path))
	if err := os.MkdirAll(filepath.Dir(absolute), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(absolute, bytes, mode); err != nil {
		t.Fatal(err)
	}
}

func bundleFixture(t *testing.T, mutate func(*sourceManifest)) (string, string, string) {
	t.Helper()
	root := t.TempDir()
	key, err := hostPlatform()
	if err != nil {
		t.Skip(err)
	}
	executable := "bin/clang-22"
	if runtime.GOOS == "windows" {
		executable = "bin/clang.exe"
	}
	content := []byte("pinned fixture executable\n")
	writeFixture(t, root, executable, content, 0o755)
	resource := "lib/clang/22/include/stddef.h"
	resourceContent := []byte("resource fixture\n")
	writeFixture(t, root, resource, resourceContent, 0o644)
	license := "licenses/llvm/LICENSE.TXT"
	licenseContent := []byte("upstream license fixture\n")
	writeFixture(t, root, license, licenseContent, 0o644)
	other := "linux-x64"
	if key == other {
		other = "windows-x64"
	}
	manifest := sourceManifest{
		SchemaVersion: 1,
		ClangVersion:  clangVersion,
		SourceCommit:  sourceCommit,
		Platforms: map[string]platformRecord{
			key: {
				Target:      key,
				Executable:  executable,
				ResourceDir: resourceDirectory,
				Files: []fileRecord{
					{Path: executable, SHA256: fixtureDigest(content), Size: int64(len(content))},
					{Path: resource, SHA256: fixtureDigest(resourceContent), Size: int64(len(resourceContent))},
				},
				Licenses: []licenseRecord{{
					Path: license, URL: "https://raw.githubusercontent.com/llvm/llvm-project/" + sourceCommit + "/LICENSE.TXT",
					SHA256: fixtureDigest(licenseContent), Size: int64(len(licenseContent)),
				}},
			},
			other: {Target: other},
		},
	}
	if mutate != nil {
		mutate(&manifest)
	}
	manifestBytes, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	writeFixture(t, root, "manifest.json", manifestBytes, 0o644)
	manifestHash := fixtureDigest(manifestBytes)
	readyBytes, err := json.Marshal(readyRecord{SchemaVersion: 1, Platform: key, ManifestSHA256: manifestHash})
	if err != nil {
		t.Fatal(err)
	}
	writeFixture(t, root, "READY", readyBytes, 0o644)
	return root, manifestHash, executable
}

func TestOpenVerifiesPinnedExecutableResourceAndLicenses(t *testing.T) {
	root, manifestHash, executable := bundleFixture(t, nil)
	bundle, err := openWithExpectedManifest(root, manifestHash)
	if err != nil {
		t.Fatal(err)
	}
	if bundle.ClangPath() != filepath.Join(root, filepath.FromSlash(executable)) ||
		bundle.ResourceDir() != filepath.Join(root, "lib", "clang", "22") ||
		bundle.ManifestSHA256() != manifestHash {
		t.Fatalf("unexpected verified bundle identity: %#v", bundle)
	}
	if err := os.WriteFile(bundle.ClangPath(), []byte("substitute executable\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := bundle.Verify(); err == nil {
		t.Fatal("Verify accepted a substituted executable")
	}
	if _, err := openWithExpectedManifest(root, manifestHash); err == nil {
		t.Fatal("Open accepted a substituted executable")
	}
}

func TestOpenRejectsMissingLicenseAndExtraFile(t *testing.T) {
	root, hash, _ := bundleFixture(t, nil)
	if err := os.Remove(filepath.Join(root, "licenses", "llvm", "LICENSE.TXT")); err != nil {
		t.Fatal(err)
	}
	if _, err := openWithExpectedManifest(root, hash); err == nil {
		t.Fatal("Open accepted a missing upstream license")
	}
	root, hash, _ = bundleFixture(t, nil)
	writeFixture(t, root, "extra.txt", []byte("not inventoried\n"), 0o644)
	if _, err := openWithExpectedManifest(root, hash); err == nil {
		t.Fatal("Open accepted an extra file")
	}
}

func TestOpenRejectsMixedPlatformAndLinkedResource(t *testing.T) {
	root, hash, _ := bundleFixture(t, func(manifest *sourceManifest) {
		key, _ := hostPlatform()
		entry := manifest.Platforms[key]
		entry.Target = "other-platform"
		manifest.Platforms[key] = entry
	})
	if _, err := openWithExpectedManifest(root, hash); err == nil {
		t.Fatal("Open accepted a mixed-platform bundle")
	}
	root, hash, _ = bundleFixture(t, nil)
	resource := filepath.Join(root, "lib", "clang", "22", "include", "stddef.h")
	if err := os.Remove(resource); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "outside.h")
	if err := os.WriteFile(outside, []byte("resource fixture\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, resource); err != nil {
		if os.IsPermission(err) {
			t.Skip("host cannot create symlinks")
		}
		t.Fatal(err)
	}
	if _, err := openWithExpectedManifest(root, hash); err == nil {
		t.Fatal("Open accepted a linked resource file")
	}
}

func TestOpenDoesNotFallBackToPathClang(t *testing.T) {
	pathDir := t.TempDir()
	name := "clang"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	if err := os.WriteFile(filepath.Join(pathDir, name), []byte("substitute clang\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", pathDir)
	if runtime.GOOS == "windows" {
		t.Setenv("PATHEXT", ".EXE")
	}
	if bundle, err := Open(t.TempDir()); err == nil {
		t.Fatalf("Open accepted an absent bundle via PATH: %#v", bundle)
	}
}

func TestOpenRejectsNonexistentRoot(t *testing.T) {
	root := filepath.Join(t.TempDir(), "missing")
	if bundle, err := Open(root); err == nil {
		t.Fatalf("Open accepted missing root: %#v", bundle)
	}
}

func TestOpenPreparedLocalBundleWhenAvailable(t *testing.T) {
	key, err := hostPlatform()
	if err != nil {
		t.Skip(err)
	}
	root, err := filepath.Abs(filepath.Join("..", "..", "..", "..", ".superpowers", "cache", "testgen-bundle", clangVersion, key))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(root); os.IsNotExist(err) {
		t.Skip("local prepared Clang bundle is absent")
	} else if err != nil {
		t.Fatal(err)
	}
	bundle, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := bundle.Verify(); err != nil {
		t.Fatal(err)
	}
}
