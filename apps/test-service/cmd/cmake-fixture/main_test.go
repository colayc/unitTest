package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"unit-test-ide.local/test-service/internal/cmake"
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

func TestFixtureConfigureWritesReadableFileAPIAndBuildWarning(t *testing.T) {
	root := t.TempDir()
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
