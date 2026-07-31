//go:build linux

package ctest

import (
	"os"
	"path/filepath"
	"testing"

	"unit-test-ide.local/test-service/internal/cmake"
)

func TestBuildDescriptorLinuxPathsAreCaseSensitive(t *testing.T) {
	fixture := newDescriptorFixture(t)
	fixture.test.Command[0] = filepath.Join(fixture.buildDir, "bin", "UNIT-TESTS")
	if err := os.WriteFile(fixture.test.Command[0], []byte("different executable"), 0o700); err != nil {
		t.Fatal(err)
	}
	descriptor, err := BuildDescriptor(fixture.test, fixture.profile, []cmake.Target{fixture.target})
	if err != nil {
		t.Fatal(err)
	}
	if descriptor.Compatibility.CaseLevel {
		t.Fatalf("case-variant path mapped on Linux: %#v", descriptor)
	}
}
