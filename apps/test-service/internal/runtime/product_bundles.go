package runtime

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strings"

	"unit-test-ide.local/test-service/internal/coveragebundle"
	"unit-test-ide.local/test-service/internal/testgenbundle"
)

var ErrInvalidProductBundleRoots = errors.New("invalid product bundle roots")
var ErrProductBundlesUnavailable = errors.New("product bundles are unavailable")

// ProductBundleRoots is the closed product-selected bundle boundary. Workspace
// configuration and PATH lookup cannot supply or replace any of these roots.
type ProductBundleRoots struct {
	CMake    string
	Coverage string
	Testgen  string
}

type verifiedTestgenBundle interface {
	ClangPath() string
	ResourceDir() string
	ManifestSHA256() string
	Verify() error
}

// ProductBundles retains the verified test-generation capability. CMake is
// verified immediately afterwards by the existing resolver, while coverage is
// pinned again for each GCC execution so each run owns its descriptor handles.
type ProductBundles struct {
	roots   ProductBundleRoots
	testgen verifiedTestgenBundle
}

func (bundles *ProductBundles) Testgen() verifiedTestgenBundle {
	if bundles == nil {
		return nil
	}
	return bundles.testgen
}

func (bundles *ProductBundles) Verify() error {
	if bundles == nil || bundles.testgen == nil {
		return ErrProductBundlesUnavailable
	}
	if err := bundles.roots.Validate(); err != nil {
		return ErrProductBundlesUnavailable
	}
	if err := bundles.testgen.Verify(); err != nil {
		return ErrProductBundlesUnavailable
	}
	return nil
}

func (roots ProductBundleRoots) any() bool {
	return roots.CMake != "" || roots.Coverage != "" || roots.Testgen != ""
}

func sameCanonicalPath(left, right string) bool {
	if goruntime.GOOS == "windows" {
		return strings.EqualFold(left, right)
	}
	return left == right
}

func validateDirectDirectory(path string) error {
	if path == "" || !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return ErrInvalidProductBundleRoots
	}
	for current := path; ; current = filepath.Dir(current) {
		info, err := os.Lstat(current)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return ErrInvalidProductBundleRoots
		}
		parent := filepath.Dir(current)
		if parent == current {
			break
		}
	}
	return nil
}

// Validate requires the exact release layout bundles/{cmake,coverage,testgen}
// and intentionally returns no native path in its error.
func (roots ProductBundleRoots) Validate() error {
	entries := []struct {
		path string
		leaf string
	}{
		{roots.CMake, "cmake"},
		{roots.Coverage, "coverage"},
		{roots.Testgen, "testgen"},
	}
	parent := ""
	for _, entry := range entries {
		if err := validateDirectDirectory(entry.path); err != nil || filepath.Base(entry.path) != entry.leaf {
			return fmt.Errorf("%w: unavailable", ErrInvalidProductBundleRoots)
		}
		candidateParent := filepath.Dir(entry.path)
		if parent == "" {
			parent = candidateParent
		} else if !sameCanonicalPath(parent, candidateParent) {
			return fmt.Errorf("%w: unrelated roots", ErrInvalidProductBundleRoots)
		}
	}
	return nil
}

func openProductBundles(roots ProductBundleRoots, platform string) (*ProductBundles, error) {
	if platform != goruntime.GOOS || roots.Validate() != nil {
		return nil, ErrProductBundlesUnavailable
	}
	coverage, err := coveragebundle.ResolveExact(roots.Coverage)
	if err != nil {
		return nil, ErrProductBundlesUnavailable
	}
	if err := coverage.Verify(); err != nil {
		_ = coverage.Close()
		return nil, ErrProductBundlesUnavailable
	}
	if err := coverage.Close(); err != nil {
		return nil, ErrProductBundlesUnavailable
	}
	testgen, err := testgenbundle.Open(roots.Testgen)
	if err != nil {
		return nil, ErrProductBundlesUnavailable
	}
	bundles := &ProductBundles{roots: roots, testgen: testgen}
	if err := bundles.Verify(); err != nil {
		return nil, err
	}
	return bundles, nil
}
