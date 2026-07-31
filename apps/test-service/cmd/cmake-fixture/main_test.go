package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"unit-test-ide.local/test-service/internal/cmake"
	"unit-test-ide.local/test-service/internal/ctest"
)

func TestFixtureSupportsOnlyDeterministicProbeCommands(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if code := run([]string{"--version=json-v1"}, &stdout, &stderr); code != 0 {
		t.Fatalf("version exit = %d, stderr = %q", code, stderr.String())
	}
	var version struct {
		Program struct {
			Name    string `json:"name"`
			Version struct {
				String string `json:"string"`
			} `json:"version"`
		} `json:"program"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &version); err != nil {
		t.Fatalf("decode version: %v", err)
	}
	if version.Program.Name != "cmake" || version.Program.Version.String != fixtureVersion {
		t.Fatalf("version = %#v", version)
	}

	for _, test := range []struct {
		args []string
		want string
	}{
		{[]string{"--list-presets=configure"}, "Available configure presets:\n"},
		{[]string{"--build", "--list-presets"}, "Available build presets:\n"},
	} {
		stdout.Reset()
		stderr.Reset()
		if code := run(test.args, &stdout, &stderr); code != 0 || stdout.String() != test.want {
			t.Fatalf("run(%q) = %d, %q, %q", test.args, code, stdout.String(), stderr.String())
		}
	}

	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"--install", "forbidden"}, &stdout, &stderr); code != 2 {
		t.Fatalf("unknown command exit = %d, want 2", code)
	}
}

func TestFixtureSupportsPairedCTestVersionProbe(t *testing.T) {
	for _, test := range []struct {
		program string
		want    bool
	}{
		{program: "ctest", want: true},
		{program: filepath.Join("tools", "ctest"+filepath.Ext(os.Args[0])), want: true},
		{program: "cmake", want: false},
		{program: "ctest-helper", want: false},
	} {
		if got := isCTestProgram(test.program); got != test.want {
			t.Fatalf("isCTestProgram(%q) = %t, want %t", test.program, got, test.want)
		}
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if code := runCTest([]string{"--version"}, &stdout, &stderr); code != 0 {
		t.Fatalf("CTest version exit = %d, stderr = %q", code, stderr.String())
	}
	if want := "ctest version " + fixtureVersion + "\n"; stdout.String() != want {
		t.Fatalf("CTest version = %q, want %q", stdout.String(), want)
	}

	stdout.Reset()
	stderr.Reset()
	if code := runCTest([]string{"--show-only=json-v1"}, &stdout, &stderr); code != 2 {
		t.Fatalf("unsupported CTest command exit = %d, want 2", code)
	}
}

func TestFixtureCTestShowsDeterministicFrameworkDescriptor(
	t *testing.T,
) {
	buildDir := t.TempDir()
	sourceDir := t.TempDir()
	if err := writeFixtureState(buildDir, fixtureState{
		SourceDir:      sourceDir,
		ConfigureCount: 1,
	}); err != nil {
		t.Fatal(err)
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := runCTest(
		[]string{
			"--test-dir",
			buildDir,
			"-C",
			"Debug",
			"--show-only=json-v1",
		},
		&stdout,
		&stderr,
	)
	if code != 0 || stderr.Len() != 0 {
		t.Fatalf(
			"runCTest() = %d, stdout=%q, stderr=%q",
			code,
			stdout.String(),
			stderr.String(),
		)
	}
	var document struct {
		Tests []struct {
			Name    string   `json:"name"`
			Command []string `json:"command"`
		} `json:"tests"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &document); err != nil {
		t.Fatal(err)
	}
	if len(document.Tests) != 1 ||
		document.Tests[0].Name != "framework-tests" ||
		len(document.Tests[0].Command) != 3 ||
		document.Tests[0].Command[1] !=
			"--fixture-scenario" ||
		document.Tests[0].Command[2] != "normal" ||
		!filepath.IsAbs(document.Tests[0].Command[0]) {
		t.Fatalf("show-only document = %#v", document)
	}
}

func TestFixtureConfigureWritesReadableFileAPIAndBuildWarning(t *testing.T) {
	root := t.TempDir()
	originalLocate := locateFixtureExecutable
	locateFixtureExecutable = func() (string, error) {
		return filepath.Join(
			root,
			fixtureExecutableName("cmake-fixture"),
		), nil
	}
	t.Cleanup(func() {
		locateFixtureExecutable = originalLocate
	})
	if err := os.WriteFile(
		filepath.Join(
			root,
			fixtureExecutableName("test-framework-fixture"),
		),
		[]byte("fixture executable"),
		0o700,
	); err != nil {
		t.Fatal(err)
	}
	sourceDir := filepath.Join(root, "source")
	buildDir := filepath.Join(root, "build")
	if err := os.MkdirAll(sourceDir, 0o700); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{
		"CMakeLists.txt": "cmake_minimum_required(VERSION 3.25)\nproject(fixture LANGUAGES CXX)\n",
		"main.cpp":       "int main() { return 0; }\n",
	} {
		if err := os.WriteFile(filepath.Join(sourceDir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	args := []string{
		"-S", sourceDir, "-B", buildDir, "-G", "Ninja",
		"-DCMAKE_C_COMPILER=fixture-cc", "-DCMAKE_CXX_COMPILER=fixture-cxx",
	}
	if code := run(args, &stdout, &stderr); code != 0 {
		t.Fatalf("configure exit = %d, stderr = %q", code, stderr.String())
	}
	profile := cmake.BuildProfile{
		ID:        "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		ProjectID: "root", Configuration: "Debug", BinaryDir: buildDir,
	}
	reply, err := cmake.ReadReply(buildDir, []string{root}, profile)
	if err != nil {
		t.Fatalf("ReadReply() error = %v", err)
	}
	if len(reply.Targets) != 1 || reply.Targets[0].Name != "fixture-app" {
		t.Fatalf("Targets = %#v", reply.Targets)
	}

	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"--build", buildDir, "--parallel", "2"}, &stdout, &stderr); code != 0 {
		t.Fatalf("build exit = %d, stderr = %q", code, stderr.String())
	}
	materialized, err := os.ReadFile(filepath.Join(
		buildDir,
		"bin",
		fixtureExecutableName("fixture-app"),
	))
	if err != nil || string(materialized) != "fixture executable" {
		t.Fatalf(
			"materialized fixture = %q, %v",
			materialized,
			err,
		)
	}
	var ctestOutput bytes.Buffer
	if err := writeCTestShowOnly(buildDir, &ctestOutput); err != nil {
		t.Fatal(err)
	}
	ctestSnapshot, err := ctest.ParseShowOnlyJSON(
		ctestOutput.Bytes(),
		ctest.DefaultLimits(),
	)
	if err != nil || len(ctestSnapshot.Tests) != 1 {
		t.Fatalf(
			"CTest snapshot = %#v, %v",
			ctestSnapshot,
			err,
		)
	}
	descriptor, err := ctest.BuildDescriptor(
		ctestSnapshot.Tests[0],
		profile,
		reply.Targets,
	)
	if err != nil ||
		descriptor.Blocked ||
		!descriptor.Compatibility.CaseLevel {
		t.Fatalf(
			"CTest descriptor = %#v, %v",
			descriptor,
			err,
		)
	}
	for _, warning := range []string{":7:3: warning:", "(8,3): warning C4996:"} {
		if !strings.Contains(stdout.String(), warning) {
			t.Fatalf("build output %q does not contain %q", stdout.String(), warning)
		}
	}
	state, err := readFixtureState(buildDir)
	if err != nil {
		t.Fatal(err)
	}
	if state.ConfigureCount != 1 || state.BuildCount != 1 {
		t.Fatalf("fixture state = %#v", state)
	}
}

func TestFixtureListsAndConfiguresNamedPreset(t *testing.T) {
	root := t.TempDir()
	for name, content := range map[string]string{
		"CMakeLists.txt": "cmake_minimum_required(VERSION 3.25)\nproject(fixture LANGUAGES CXX)\n",
		"main.cpp":       "int main() { return 0; }\n",
		"CMakePresets.json": `{
			"version": 6,
			"configurePresets": [{
				"name": "fixture",
				"generator": "Ninja",
				"binaryDir": "${sourceDir}/build-fixture"
			}]
		}`,
	} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	previous, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(previous); err != nil {
			t.Errorf("restore working directory: %v", err)
		}
	})

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if code := run([]string{"--list-presets=configure"}, &stdout, &stderr); code != 0 {
		t.Fatalf("list exit = %d, stderr = %q", code, stderr.String())
	}
	if stdout.String() != "Available configure presets:\n  \"fixture\"\n" {
		t.Fatalf("list output = %q", stdout.String())
	}
	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"--preset", "fixture"}, &stdout, &stderr); code != 0 {
		t.Fatalf("configure exit = %d, stderr = %q", code, stderr.String())
	}
	state, err := readFixtureState(filepath.Join(root, "build-fixture"))
	if err != nil {
		t.Fatal(err)
	}
	if state.ConfigureCount != 1 || state.SourceDir != root {
		t.Fatalf("fixture state = %#v", state)
	}
}
