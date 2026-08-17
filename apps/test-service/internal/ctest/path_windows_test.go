//go:build windows

package ctest

import (
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
