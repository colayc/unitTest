package coveragebundle

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
)

func TestBundleManifestRejectsUnknownSchemaPlatformAndArchitecture(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(map[string]any)
	}{
		{"unknown schema", func(manifest map[string]any) { manifest["schemaVersion"] = float64(2) }},
		{"unknown platform", func(manifest map[string]any) { manifest["platform"] = "plan9-x64" }},
		{"wrong architecture", func(manifest map[string]any) {
			if runtime.GOOS == "windows" {
				manifest["platform"] = "linux-x64"
			} else {
				manifest["platform"] = "windows-x64"
			}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			productRoot, bundleRoot := createBundleFixture(t)
			mutateBundleManifest(t, bundleRoot, test.mutate)
			if pin, err := Resolve(productRoot); err == nil {
				_ = pin.Close()
				t.Fatalf("Resolve accepted %s", test.name)
			}
		})
	}
}

func TestBundleManifestRejectsDuplicateCaseAliasAndNonCanonicalPaths(t *testing.T) {
	for _, candidate := range []string{
		"app/gcovr-runner.pyz/",
		"app//gcovr-runner.pyz",
		"app/./gcovr-runner.pyz",
	} {
		t.Run(strings.ReplaceAll(candidate, "/", "_"), func(t *testing.T) {
			productRoot, bundleRoot := createBundleFixture(t)
			mutateBundleManifest(t, bundleRoot, func(manifest map[string]any) {
				outputs := manifest["outputs"].([]any)
				outputs[0].(map[string]any)["path"] = candidate
			})
			if pin, err := Resolve(productRoot); err == nil {
				_ = pin.Close()
				t.Fatalf("Resolve accepted non-canonical output %q", candidate)
			}
		})
	}

	t.Run("duplicate", func(t *testing.T) {
		productRoot, bundleRoot := createBundleFixture(t)
		mutateBundleManifest(t, bundleRoot, func(manifest map[string]any) {
			outputs := manifest["outputs"].([]any)
			manifest["outputs"] = append(outputs, cloneJSONMap(outputs[0].(map[string]any)))
		})
		if pin, err := Resolve(productRoot); err == nil {
			_ = pin.Close()
			t.Fatal("Resolve accepted a duplicate output")
		}
	})

	t.Run("case alias", func(t *testing.T) {
		productRoot, bundleRoot := createBundleFixture(t)
		mutateBundleManifest(t, bundleRoot, func(manifest map[string]any) {
			outputs := manifest["outputs"].([]any)
			alias := cloneJSONMap(outputs[0].(map[string]any))
			alias["path"] = strings.ToUpper(alias["path"].(string))
			manifest["outputs"] = append([]any{alias}, outputs...)
		})
		if pin, err := Resolve(productRoot); err == nil {
			_ = pin.Close()
			t.Fatal("Resolve accepted a case-alias output")
		}
	})
}

func TestBundleManifestRequiresPlatformExecutionClosure(t *testing.T) {
	productRoot, bundleRoot := createBundleFixture(t)
	mutateBundleManifest(t, bundleRoot, func(manifest map[string]any) {
		outputs := manifest["outputs"].([]any)
		filtered := make([]any, 0, len(outputs)-1)
		for _, value := range outputs {
			record := value.(map[string]any)
			if record["path"] != "app/gcovr-runner.pyz" {
				filtered = append(filtered, value)
			}
		}
		manifest["outputs"] = filtered
	})
	if pin, err := Resolve(productRoot); err == nil {
		_ = pin.Close()
		t.Fatal("Resolve accepted a manifest without the runner archive")
	}
}

func TestBundleManifestRejectsOutputResourceBudgetOverflow(t *testing.T) {
	t.Run("output count before output handles", func(t *testing.T) {
		productRoot, bundleRoot := createBundleFixture(t)
		mutateBundleManifest(t, bundleRoot, func(manifest map[string]any) {
			outputs := manifest["outputs"].([]any)
			for index := len(outputs); index <= maximumManifestOutputs; index++ {
				outputs = append(outputs, map[string]any{
					"path":   fmt.Sprintf("licenses/overflow-%04d.txt", index),
					"sha256": digestBytes([]byte(fmt.Sprintf("overflow-%d", index))),
					"kind":   "regular-file",
				})
			}
			sort.Slice(outputs, func(left, right int) bool {
				return outputs[left].(map[string]any)["path"].(string) < outputs[right].(map[string]any)["path"].(string)
			})
			manifest["outputs"] = outputs
		})
		afterManifest := false
		if pin, err := resolveBundle(productRoot, resolveHooks{afterManifest: func() { afterManifest = true }}); err == nil {
			_ = pin.Close()
			t.Fatal("Resolve accepted output count above the manifest budget")
		}
		if afterManifest {
			t.Fatal("output-count overflow reached the output-handle phase")
		}
	})

	for name, candidate := range map[string]string{
		"depth":  strings.Repeat("a/", maximumBundleDepth) + "file.txt",
		"length": "licenses/" + strings.Repeat("a", maximumPortablePathBytes) + ".txt",
	} {
		t.Run(name, func(t *testing.T) {
			productRoot, bundleRoot := createBundleFixture(t)
			mutateBundleManifest(t, bundleRoot, func(manifest map[string]any) {
				manifest["outputs"].([]any)[0].(map[string]any)["path"] = candidate
			})
			if pin, err := Resolve(productRoot); err == nil {
				_ = pin.Close()
				t.Fatalf("Resolve accepted output path above the %s budget", name)
			}
		})
	}
}

func TestBundleManifestRejectsUnknownFieldsAndTrailingJSON(t *testing.T) {
	productRoot, bundleRoot := createBundleFixture(t)
	mutateBundleManifest(t, bundleRoot, func(manifest map[string]any) {
		manifest["unreviewed"] = true
	})
	if pin, err := Resolve(productRoot); err == nil {
		_ = pin.Close()
		t.Fatal("Resolve accepted an unknown manifest field")
	}

	productRoot, bundleRoot = createBundleFixture(t)
	manifestPath := filepath.Join(bundleRoot, manifestName)
	contents, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	duplicated := strings.Replace(string(contents), `"schemaVersion": 1`, `"schemaVersion": 1, "schemaVersion": 1`, 1)
	if duplicated == string(contents) {
		t.Fatal("fixture manifest did not contain schemaVersion")
	}
	if err := os.WriteFile(manifestPath, []byte(duplicated), 0o600); err != nil {
		t.Fatal(err)
	}
	if pin, err := Resolve(productRoot); err == nil {
		_ = pin.Close()
		t.Fatal("Resolve accepted a duplicate JSON object key")
	}

	productRoot, bundleRoot = createBundleFixture(t)
	manifestPath = filepath.Join(bundleRoot, manifestName)
	contents, err = os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifestPath, append(contents, []byte("{}\n")...), 0o600); err != nil {
		t.Fatal(err)
	}
	if pin, err := Resolve(productRoot); err == nil {
		_ = pin.Close()
		t.Fatal("Resolve accepted trailing JSON")
	}
}

func createBundleFixture(t *testing.T) (string, string) {
	t.Helper()
	key, err := currentPlatformKey()
	if err != nil {
		t.Skipf("unsupported test platform: %v", err)
	}
	productRoot := filepath.Join(testScratchDir(t), "product")
	bundleRoot := filepath.Join(productRoot, "coverage-bundle", key)
	files := map[string][]byte{
		"app/gcovr-runner.pyz": []byte("runner and locked dependencies"),
		"licenses/NOTICE.txt":  []byte("fixture license"),
	}
	if key == "windows-x64" {
		files["python/python.exe"] = []byte("python executable")
		files["python/python314.zip"] = []byte("standard library")
		files["python/python314.dll"] = []byte("native dependency")
	} else {
		files["python/bin/python3"] = []byte("python executable")
		files["python/lib/python3.14/os.py"] = []byte("standard library")
		files["python/lib/libpython3.14.so"] = []byte("native dependency")
	}

	paths := make([]string, 0, len(files))
	for relative, contents := range files {
		absolute := filepath.Join(bundleRoot, filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(absolute), 0o700); err != nil {
			t.Fatal(err)
		}
		mode := os.FileMode(0o600)
		if relative == "python/bin/python3" {
			mode = 0o700
		}
		if err := os.WriteFile(absolute, contents, mode); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, relative)
	}
	sort.Strings(paths)
	outputs := make([]map[string]any, 0, len(paths))
	for _, relative := range paths {
		outputs = append(outputs, map[string]any{
			"path": relative, "sha256": digestBytes(files[relative]), "kind": "regular-file",
		})
	}
	artifactKind := "embedded-archive"
	artifactFilename := "python.zip"
	artifactURL := "https://www.python.org/python.zip"
	if key == "linux-x64" {
		artifactKind = "source-archive"
		artifactFilename = "python.tgz"
		artifactURL = "https://www.python.org/python.tgz"
	}
	manifest := map[string]any{
		"schemaVersion": float64(1),
		"platform":      key,
		"pythonVersion": "3.14.6",
		"gcovrVersion":  "8.6",
		"inputs": map[string]any{
			"pythonArtifact": map[string]any{
				"kind": artifactKind, "filename": artifactFilename, "url": artifactURL, "sha256": digestBytes([]byte("python source")),
			},
			"wheels": []any{map[string]any{
				"project": "gcovr", "version": "8.6", "kind": "wheel", "filename": "gcovr.whl", "url": "https://files.pythonhosted.org/gcovr.whl", "sha256": digestBytes([]byte("gcovr wheel")),
			}},
			"provenance": map[string]any{
				"recipe": map[string]any{"name": "coverage-bundle-recipe-v2", "sha256": digestBytes([]byte("recipe"))},
				"builderImage": func() any {
					if key == "linux-x64" {
						return "quay.io/pypa/manylinux@sha256:" + digestBytes([]byte("image"))
					}
					return nil
				}(),
				"glibcBaseline": func() any {
					if key == "linux-x64" {
						return "2.28"
					}
					return nil
				}(),
			},
		},
		"outputs": outputs,
	}
	writeBundleManifest(t, bundleRoot, manifest)
	if err := os.WriteFile(filepath.Join(bundleRoot, readyName), []byte("ready\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return productRoot, bundleRoot
}

func testScratchDir(t *testing.T) string {
	t.Helper()
	workingDirectory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	repositoryRoot := filepath.Clean(filepath.Join(workingDirectory, "..", "..", "..", ".."))
	base := filepath.Join(repositoryRoot, ".superpowers", "cache", "coveragebundle-tests")
	if err := os.MkdirAll(base, 0o700); err != nil {
		t.Fatal(err)
	}
	root, err := os.MkdirTemp(base, "case-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	return root
}

func mutateBundleManifest(t *testing.T, bundleRoot string, mutate func(map[string]any)) {
	t.Helper()
	manifestPath := filepath.Join(bundleRoot, manifestName)
	contents, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	var manifest map[string]any
	if err := json.Unmarshal(contents, &manifest); err != nil {
		t.Fatal(err)
	}
	mutate(manifest)
	writeBundleManifest(t, bundleRoot, manifest)
}

func writeBundleManifest(t *testing.T, bundleRoot string, manifest map[string]any) {
	t.Helper()
	contents, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(bundleRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bundleRoot, manifestName), append(contents, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
}

func cloneJSONMap(value map[string]any) map[string]any {
	clone := make(map[string]any, len(value))
	for key, item := range value {
		clone[key] = item
	}
	return clone
}

func digestBytes(contents []byte) string {
	digest := sha256.Sum256(contents)
	return hex.EncodeToString(digest[:])
}
