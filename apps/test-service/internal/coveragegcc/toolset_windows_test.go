//go:build windows

package coveragegcc

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"unit-test-ide.local/test-service/internal/toolchain"
)

func TestGCCPinToolsetWindowsHasNoFilesystemSideEffects(t *testing.T) {
	root := t.TempDir()
	before, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	instance := toolchain.Instance{Family: toolchain.FamilyGCC, CCompiler: filepath.Join(root, "missing-gcc"), CXXCompiler: filepath.Join(root, "missing-g++"), Coverage: toolchain.CoverageCapability{GCov: filepath.Join(root, "missing-gcov")}}
	toolset, err := PinToolset(instance)
	if toolset != nil || !errors.Is(err, ErrUnsupportedPlatform) {
		t.Fatalf("PinToolset() = %#v, %v; want nil ErrUnsupportedPlatform", toolset, err)
	}
	after, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("Windows stub changed filesystem entries: before=%#v after=%#v", before, after)
	}
}
