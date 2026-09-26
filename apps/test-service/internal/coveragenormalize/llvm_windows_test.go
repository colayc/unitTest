//go:build windows

package coveragenormalize

import (
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

func TestNormalizeLLVMAcceptsShortSpellingOfWorkspaceSource(t *testing.T) {
	input := llvmNormalizationFixture(t)
	longPath := input.Export.Files[0].NativePath
	shortPath := shortPathForNormalizationTest(t, longPath)
	if shortPath == longPath {
		t.Skipf("filesystem did not provide a distinct short spelling for %q", longPath)
	}
	input.Export.Files[0].NativePath = shortPath
	if _, _, err := NormalizeLLVM(input); err != nil {
		t.Fatalf("NormalizeLLVM rejected short spelling %q for %q: %v", shortPath, longPath, err)
	}
}

func shortPathForNormalizationTest(t *testing.T, path string) string {
	t.Helper()
	value, err := windows.UTF16PtrFromString(filepath.Clean(path))
	if err != nil {
		t.Fatal(err)
	}
	buffer := make([]uint16, 32768)
	length, err := windows.GetShortPathName(value, &buffer[0], uint32(len(buffer)))
	if err != nil {
		t.Fatal(err)
	}
	if length == 0 || length >= uint32(len(buffer)) {
		t.Fatalf("GetShortPathName returned invalid length %d", length)
	}
	return filepath.Clean(windows.UTF16ToString(buffer[:length]))
}
