package ctest

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"unit-test-ide.local/test-service/internal/cmake"
	"unit-test-ide.local/test-service/internal/task"
)

func TestRunnerBuildsServiceOwnedShowOnlyPlan(t *testing.T) {
	root := t.TempDir()
	runner, err := NewRunner(cmake.Installation{
		Executable:      filepath.Join(root, "bin", "cmake.exe"),
		CTestExecutable: filepath.Join(root, "bin", "ctest.exe"),
		Version:         "4.3.4",
		Identity:        strings.Repeat("a", 64),
	})
	if err != nil {
		t.Fatal(err)
	}
	profile := cmake.BuildProfile{
		ID:            strings.Repeat("b", 64),
		ProjectID:     "core",
		BinaryDir:     filepath.Join(t.TempDir(), "build"),
		Configuration: "Debug",
	}
	step, err := runner.ShowOnlyPlan(profile)
	if err != nil {
		t.Fatal(err)
	}
	wantArgs := []string{
		"--test-dir", profile.BinaryDir,
		"-C", "Debug",
		"--show-only=json-v1",
	}
	if step.Kind != task.StepTestDiscovery ||
		step.Process.Executable != runner.installation.CTestExecutable ||
		step.Process.Dir != profile.BinaryDir ||
		!reflect.DeepEqual(step.Process.Args, wantArgs) ||
		!reflect.DeepEqual(step.Process.Env, []string{}) ||
		!reflect.DeepEqual(step.Public.Args, wantArgs) {
		t.Fatalf("show-only step = %#v", step)
	}
}

func TestRunnerOpaquePlanUsesExactCTestSelection(t *testing.T) {
	runner := mustRunner(t)
	metacharacters := `core.[a-z](fast)+?*^$|{}\name`
	descriptor := ExecutionDescriptor{
		LogicalName:   metacharacters,
		TestDirectory: filepath.Join(t.TempDir(), "build"),
		Configuration: "RelWithDebInfo",
		Arguments: []string{
			"--client-controlled", "must-not-appear",
		},
		Executable: cmake.FingerprintFile{Path: filepath.Join(t.TempDir(), "direct.exe")},
	}
	step, err := runner.OpaqueRunPlan(descriptor, 1500*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if step.Kind != task.StepTestRun ||
		step.Process.Executable != runner.installation.CTestExecutable ||
		step.Process.Executable == descriptor.Executable.Path ||
		step.Process.Dir != descriptor.TestDirectory {
		t.Fatalf("opaque step = %#v", step)
	}
	wantPattern := `^core\.\[a-z\]\(fast\)\+\?\*\^\$\|\{\}\\name$`
	wantArgs := []string{
		"--test-dir", descriptor.TestDirectory,
		"-C", descriptor.Configuration,
		"--output-on-failure",
		"--no-tests=error",
		"--timeout", "2",
		"-R", wantPattern,
	}
	if !reflect.DeepEqual(step.Process.Args, wantArgs) {
		t.Fatalf("args = %#v, want %#v", step.Process.Args, wantArgs)
	}
	for _, argument := range step.Process.Args {
		if argument == "--client-controlled" || argument == "must-not-appear" {
			t.Fatalf("descriptor command argument leaked into CTest plan: %#v", step.Process.Args)
		}
	}
}

func TestRunnerRejectsBlockedOrIncompleteOpaqueDescriptor(t *testing.T) {
	runner := mustRunner(t)
	valid := ExecutionDescriptor{
		LogicalName:   "core.tests",
		TestDirectory: filepath.Join(t.TempDir(), "build"),
	}
	cases := map[string]ExecutionDescriptor{
		"blocked": func() ExecutionDescriptor {
			value := valid
			value.Blocked = true
			value.BlockedReason = ReasonExternalCommand
			return value
		}(),
		"missing logical name": func() ExecutionDescriptor {
			value := valid
			value.LogicalName = ""
			return value
		}(),
		"missing test directory": func() ExecutionDescriptor {
			value := valid
			value.TestDirectory = ""
			return value
		}(),
	}
	for name, descriptor := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := runner.OpaqueRunPlan(descriptor, time.Second); err == nil {
				t.Fatal("OpaqueRunPlan() error = nil")
			}
		})
	}
}

func mustRunner(t *testing.T) *Runner {
	t.Helper()
	root := t.TempDir()
	runner, err := NewRunner(cmake.Installation{
		Executable:      filepath.Join(root, "bin", "cmake"),
		CTestExecutable: filepath.Join(root, "bin", "ctest"),
		Version:         "4.3.4",
		Identity:        strings.Repeat("a", 64),
	})
	if err != nil {
		t.Fatal(err)
	}
	return runner
}
