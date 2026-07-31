package ctest

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"unit-test-ide.local/test-service/internal/cmake"
)

func TestBuildDescriptorMapsDirectFileAPITarget(t *testing.T) {
	fixture := newDescriptorFixture(t)
	descriptor, err := BuildDescriptor(fixture.test, fixture.profile, []cmake.Target{fixture.target})
	if err != nil {
		t.Fatal(err)
	}
	if descriptor.LogicalName != fixture.test.Name ||
		descriptor.Configuration != "Debug" ||
		descriptor.TargetID != fixture.target.ID ||
		descriptor.Executable.Path != fixture.executable ||
		len(descriptor.Executable.SHA256) != 64 ||
		!slices.Equal(descriptor.Arguments, []string{"--fixture", "value"}) ||
		descriptor.WorkingDirectory != fixture.buildDir ||
		!descriptor.Compatibility.CaseLevel ||
		descriptor.Blocked {
		t.Fatalf("descriptor = %#v", descriptor)
	}
	if err := descriptor.ValidateExecutable(); err != nil {
		t.Fatalf("ValidateExecutable() error = %v", err)
	}
}

func TestBuildDescriptorDegradesWrapperConfigurationAndReservedArguments(t *testing.T) {
	t.Run("wrapper inside project", func(t *testing.T) {
		fixture := newDescriptorFixture(t)
		wrapper := filepath.Join(fixture.sourceDir, "tools", executableName("wrapper"))
		if err := os.MkdirAll(filepath.Dir(wrapper), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(wrapper, []byte("wrapper"), 0o700); err != nil {
			t.Fatal(err)
		}
		fixture.test.Command = []string{wrapper, fixture.executable}
		descriptor, err := BuildDescriptor(fixture.test, fixture.profile, []cmake.Target{fixture.target})
		if err != nil {
			t.Fatal(err)
		}
		if descriptor.Blocked || descriptor.Compatibility.CaseLevel ||
			!slices.Contains(descriptor.Compatibility.Reasons, ReasonCommandNotTarget) {
			t.Fatalf("wrapper descriptor = %#v", descriptor)
		}
	})

	t.Run("multi-config mismatch", func(t *testing.T) {
		fixture := newDescriptorFixture(t)
		fixture.test.Config = "Release"
		descriptor, err := BuildDescriptor(fixture.test, fixture.profile, []cmake.Target{fixture.target})
		if err != nil {
			t.Fatal(err)
		}
		if descriptor.Compatibility.CaseLevel ||
			!slices.Contains(descriptor.Compatibility.Reasons, ReasonConfigurationMismatch) {
			t.Fatalf("configuration descriptor = %#v", descriptor)
		}
	})

	t.Run("reserved adapter argument hook", func(t *testing.T) {
		fixture := newDescriptorFixture(t)
		fixture.test.Command = append(fixture.test.Command, "-ln")
		descriptor, err := BuildDescriptor(fixture.test, fixture.profile, []cmake.Target{fixture.target})
		if err != nil {
			t.Fatal(err)
		}
		compatibility := descriptor.CheckReservedArguments(func(arguments []string) bool {
			return slices.Contains(arguments, "-ln")
		})
		if compatibility.CaseLevel ||
			!slices.Contains(compatibility.Reasons, ReasonReservedArgument) {
			t.Fatalf("reserved compatibility = %#v", compatibility)
		}
	})
}

func TestBuildDescriptorBlocksExternalCommandAndWorkingDirectory(t *testing.T) {
	t.Run("external command", func(t *testing.T) {
		fixture := newDescriptorFixture(t)
		external := filepath.Join(t.TempDir(), executableName("external"))
		if err := os.WriteFile(external, []byte("external"), 0o700); err != nil {
			t.Fatal(err)
		}
		fixture.test.Command[0] = external
		descriptor, err := BuildDescriptor(fixture.test, fixture.profile, []cmake.Target{fixture.target})
		if err != nil {
			t.Fatal(err)
		}
		if !descriptor.Blocked || descriptor.BlockedReason != ReasonExternalCommand ||
			descriptor.Compatibility.CaseLevel {
			t.Fatalf("external command descriptor = %#v", descriptor)
		}
	})

	t.Run("external working directory", func(t *testing.T) {
		fixture := newDescriptorFixture(t)
		fixture.test.Properties[0].Value.String = t.TempDir()
		descriptor, err := BuildDescriptor(fixture.test, fixture.profile, []cmake.Target{fixture.target})
		if err != nil {
			t.Fatal(err)
		}
		if !descriptor.Blocked || descriptor.BlockedReason != ReasonExternalWorkingDirectory ||
			descriptor.Compatibility.CaseLevel {
			t.Fatalf("external working directory descriptor = %#v", descriptor)
		}
	})
}

func TestExecutionDescriptorRejectsExecutableMutationAndIdentityReplacement(t *testing.T) {
	for _, mode := range []string{"content mutation", "identity replacement"} {
		t.Run(mode, func(t *testing.T) {
			fixture := newDescriptorFixture(t)
			descriptor, err := BuildDescriptor(fixture.test, fixture.profile, []cmake.Target{fixture.target})
			if err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "content mutation":
				if err := os.WriteFile(fixture.executable, []byte("changed executable"), 0o700); err != nil {
					t.Fatal(err)
				}
			case "identity replacement":
				replacement := fixture.executable + ".replacement"
				if err := os.WriteFile(replacement, []byte("fixture executable"), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.Remove(fixture.executable); err != nil {
					t.Fatal(err)
				}
				if err := os.Rename(replacement, fixture.executable); err != nil {
					t.Fatal(err)
				}
			}
			if err := descriptor.ValidateExecutable(); !errors.Is(err, cmake.ErrTargetArtifactChanged) {
				t.Fatalf("ValidateExecutable() error = %v", err)
			}
		})
	}
}

func TestBuildDescriptorRejectsInvalidArguments(t *testing.T) {
	fixture := newDescriptorFixture(t)
	cases := map[string]func(){
		"profile project": func() { fixture.profile.ProjectID = "" },
		"profile ID":      func() { fixture.profile.ID = "" },
		"profile root":    func() { fixture.profile.BinaryDir = "" },
		"logical name":    func() { fixture.test.Name = "" },
		"command":         func() { fixture.test.Command = nil },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			fixture = newDescriptorFixture(t)
			mutate()
			if _, err := BuildDescriptor(fixture.test, fixture.profile, []cmake.Target{fixture.target}); !errors.Is(err, ErrInvalidDescriptor) {
				t.Fatalf("BuildDescriptor() error = %v", err)
			}
		})
	}
}

type descriptorFixture struct {
	sourceDir  string
	buildDir   string
	executable string
	profile    cmake.BuildProfile
	target     cmake.Target
	test       RawTest
}

func newDescriptorFixture(t *testing.T) descriptorFixture {
	t.Helper()
	root := t.TempDir()
	sourceDir := filepath.Join(root, "source")
	buildDir := filepath.Join(root, "build")
	executable := filepath.Join(buildDir, "bin", executableName("unit-tests"))
	for _, directory := range []string{sourceDir, buildDir, filepath.Dir(executable)} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(executable, []byte("fixture executable"), 0o700); err != nil {
		t.Fatal(err)
	}
	profile := cmake.BuildProfile{
		ID: strings.Repeat("a", 64), ProjectID: "fixture", Generator: "Ninja",
		Configuration: "Debug", BinaryDir: buildDir,
	}
	target := cmake.Target{
		ID: strings.Repeat("b", 64), Name: "unit-tests", Type: "EXECUTABLE",
		ProjectID: profile.ProjectID, ProfileID: profile.ID,
		Configuration: "Debug", SourceDir: sourceDir, BuildDir: buildDir,
		ProjectSourceDir: sourceDir, ProjectBuildDir: buildDir,
		Artifacts: []string{executable},
	}
	test := RawTest{
		Name: "unit.tests", Config: "Debug",
		Command: []string{executable, "--fixture", "value"},
		Properties: []Property{
			{Name: "WORKING_DIRECTORY", Value: PropertyValue{Kind: PropertyString, String: buildDir}},
			{Name: "LABELS", Value: PropertyValue{Kind: PropertyStrings, Strings: []string{"fast"}}},
		},
	}
	return descriptorFixture{
		sourceDir: sourceDir, buildDir: buildDir, executable: executable,
		profile: profile, target: target, test: test,
	}
}

func executableName(name string) string {
	if filepath.Separator == '\\' {
		return name + ".exe"
	}
	return name
}
