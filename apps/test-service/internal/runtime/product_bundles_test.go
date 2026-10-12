package runtime

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestProductBundleRootsRequireExactSiblingDirectories(t *testing.T) {
	parent := filepath.Join(t.TempDir(), "bundles")
	roots := ProductBundleRoots{
		CMake:    filepath.Join(parent, "cmake"),
		Coverage: filepath.Join(parent, "coverage"),
		Testgen:  filepath.Join(parent, "testgen"),
	}
	for _, root := range []string{roots.CMake, roots.Coverage, roots.Testgen} {
		if err := os.MkdirAll(root, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := roots.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}

	cases := map[string]ProductBundleRoots{
		"missing":  {CMake: roots.CMake, Coverage: roots.Coverage},
		"relative": {CMake: "cmake", Coverage: roots.Coverage, Testgen: roots.Testgen},
		"alias":    {CMake: roots.CMake + string(filepath.Separator) + ".." + string(filepath.Separator) + "cmake", Coverage: roots.Coverage, Testgen: roots.Testgen},
		"escaped":  {CMake: roots.CMake, Coverage: filepath.Join(t.TempDir(), "coverage"), Testgen: roots.Testgen},
		"renamed":  {CMake: roots.CMake, Coverage: roots.Coverage, Testgen: filepath.Join(parent, "clang")},
	}
	for name, candidate := range cases {
		t.Run(name, func(t *testing.T) {
			if err := candidate.Validate(); !errors.Is(err, ErrInvalidProductBundleRoots) {
				t.Fatalf("Validate() error = %v", err)
			}
		})
	}
}

func TestProductBundleRootsRejectLinkedComponents(t *testing.T) {
	parent := filepath.Join(t.TempDir(), "bundles")
	realCoverage := filepath.Join(t.TempDir(), "coverage")
	for _, root := range []string{filepath.Join(parent, "cmake"), filepath.Join(parent, "testgen"), realCoverage} {
		if err := os.MkdirAll(root, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	linkedCoverage := filepath.Join(parent, "coverage")
	if err := os.Symlink(realCoverage, linkedCoverage); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	roots := ProductBundleRoots{CMake: filepath.Join(parent, "cmake"), Coverage: linkedCoverage, Testgen: filepath.Join(parent, "testgen")}
	if err := roots.Validate(); !errors.Is(err, ErrInvalidProductBundleRoots) {
		t.Fatalf("Validate() error = %v", err)
	}
}

type replaceableTestgenBundle struct{ verifyErr error }

func (*replaceableTestgenBundle) ClangPath() string      { return "clang" }
func (*replaceableTestgenBundle) ResourceDir() string    { return "resource" }
func (*replaceableTestgenBundle) ManifestSHA256() string { return "identity" }
func (bundle *replaceableTestgenBundle) Verify() error   { return bundle.verifyErr }

func TestProductBundlesReverifyTheRetainedTestgenIdentity(t *testing.T) {
	parent := filepath.Join(t.TempDir(), "bundles")
	roots := ProductBundleRoots{
		CMake: filepath.Join(parent, "cmake"), Coverage: filepath.Join(parent, "coverage"),
		Testgen: filepath.Join(parent, "testgen"),
	}
	for _, root := range []string{roots.CMake, roots.Coverage, roots.Testgen} {
		if err := os.MkdirAll(root, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	retained := &replaceableTestgenBundle{}
	bundles := &ProductBundles{roots: roots, testgen: retained}
	if err := bundles.Verify(); err != nil {
		t.Fatal(err)
	}
	retained.verifyErr = errors.New("replaced")
	if err := bundles.Verify(); !errors.Is(err, ErrProductBundlesUnavailable) {
		t.Fatalf("Verify() error = %v", err)
	}
}
