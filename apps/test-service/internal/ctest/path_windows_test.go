//go:build windows

package ctest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"unit-test-ide.local/test-service/internal/cmake"
)

func TestBuildDescriptorWindowsPathsUseFilesystemIdentity(t *testing.T) {
	fixture := newDescriptorFixture(t)
	fixture.test.Command[0] = strings.ToUpper(fixture.executable)
	descriptor, err := BuildDescriptor(fixture.test, fixture.profile, []cmake.Target{fixture.target})
	if err != nil {
		t.Fatal(err)
	}
	if !descriptor.Compatibility.CaseLevel || descriptor.Executable.Identity == "" {
		t.Fatalf("case-variant Windows descriptor = %#v", descriptor)
	}
}

func TestLaunchNativePathShortensOversizedExistingDirectory(t *testing.T) {
	root := t.TempDir()
	longPath := root
	for range 18 {
		longPath = filepath.Join(longPath, "long-directory")
	}
	longPath = filepath.Join(longPath, "framework-matrix", "f1")
	if err := os.MkdirAll(longPath, 0o700); err != nil {
		t.Fatal(err)
	}
	if len(longPath) < 248 {
		t.Skip("test directory is not beyond the Windows CreateProcess limit")
	}
	shortPath := launchNativePath(longPath)
	if len(shortPath) >= len(longPath) {
		t.Fatalf("launchNativePath() = %q, want a shorter spelling than %q", shortPath, longPath)
	}
	if _, err := os.Stat(shortPath); err != nil {
		t.Fatalf("short launch path is not usable: %v", err)
	}
}
