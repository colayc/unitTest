package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"unit-test-ide.local/test-service/internal/unityrunner"
	"unit-test-ide.local/test-service/internal/workspace"
)

func TestUnityRunnerVersionJSON(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"--version=json-v1"}, &stdout, &stderr); code != 0 {
		t.Fatalf("run() code = %d, stderr = %s", code, stderr.String())
	}
	var version struct {
		SchemaVersion  int    `json:"schemaVersion"`
		Name           string `json:"name"`
		Version        string `json:"version"`
		RunnerProtocol string `json:"runnerProtocol"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &version); err != nil {
		t.Fatal(err)
	}
	if version.SchemaVersion != 1 || version.Name != "unity-runner-generator" ||
		version.Version != unityrunner.CurrentGeneratorVersion ||
		version.RunnerProtocol != "utide.runner.v1" || stderr.Len() != 0 {
		t.Fatalf("version = %#v, stderr = %q", version, stderr.String())
	}
}

func TestUnityRunnerGeneratePublishesDeterministicOutputs(t *testing.T) {
	fixture := newCLIFixture(t)
	var stdout, stderr bytes.Buffer
	if code := run(fixture.args(), &stdout, &stderr); code != 0 {
		t.Fatalf("run() code = %d, stdout = %q, stderr = %q", code, stdout.String(), stderr.String())
	}
	runnerBefore, err := os.ReadFile(fixture.runnerPath)
	if err != nil {
		t.Fatal(err)
	}
	manifestBefore, err := os.ReadFile(fixture.manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	runnerInfoBefore, err := os.Stat(fixture.runnerPath)
	if err != nil {
		t.Fatal(err)
	}
	manifestInfoBefore, err := os.Stat(fixture.manifestPath)
	if err != nil {
		t.Fatal(err)
	}

	stdout.Reset()
	stderr.Reset()
	if code := run(fixture.args(), &stdout, &stderr); code != 0 {
		t.Fatalf("second run() code = %d, stderr = %q", code, stderr.String())
	}
	runnerAfter, _ := os.ReadFile(fixture.runnerPath)
	manifestAfter, _ := os.ReadFile(fixture.manifestPath)
	runnerInfoAfter, _ := os.Stat(fixture.runnerPath)
	manifestInfoAfter, _ := os.Stat(fixture.manifestPath)
	if !bytes.Equal(runnerBefore, runnerAfter) || !bytes.Equal(manifestBefore, manifestAfter) {
		t.Fatal("second generation changed output bytes")
	}
	if !os.SameFile(runnerInfoBefore, runnerInfoAfter) ||
		!os.SameFile(manifestInfoBefore, manifestInfoAfter) {
		t.Fatal("identical generation unnecessarily replaced an output file")
	}
	var manifest unityrunner.Manifest
	if err := json.Unmarshal(manifestAfter, &manifest); err != nil {
		t.Fatal(err)
	}
	if err := manifest.Verify(); err != nil {
		t.Fatal(err)
	}
}

func TestUnityRunnerRejectsUnknownFlagsAndPathEscapes(t *testing.T) {
	fixture := newCLIFixture(t)
	tests := map[string][]string{
		"unknown flag": append(fixture.args(), "--command", "cmd.exe"),
		"source escape": replaceFlagValue(
			fixture.args(), "--source", "../outside.c",
		),
		"absolute source": replaceFlagValue(
			fixture.args(), "--source", filepath.Join(fixture.workspaceRoot, "test.c"),
		),
		"runner escape": replaceFlagValue(
			fixture.args(), "--runner", "../outside-runner.c",
		),
		"manifest escape": replaceFlagValue(
			fixture.args(), "--manifest", "../outside-manifest.json",
		),
		"same output": replaceFlagValue(
			fixture.args(), "--manifest", "generated/runner.c",
		),
	}
	for name, args := range tests {
		t.Run(name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if code := run(args, &stdout, &stderr); code == 0 {
				t.Fatalf("run() succeeded, stdout = %q", stdout.String())
			}
			if stdout.Len() != 0 || stderr.Len() == 0 {
				t.Fatalf("stdout = %q, stderr = %q", stdout.String(), stderr.String())
			}
		})
	}
	if _, err := os.Stat(filepath.Join(fixture.buildRoot, "..", "outside-runner.c")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("escaped runner was created: %v", err)
	}
}

func TestUnityRunnerRejectsSourceAndOutputLinks(t *testing.T) {
	fixture := newCLIFixture(t)
	sourceLink := filepath.Join(fixture.workspaceRoot, "linked.c")
	if err := os.Symlink(filepath.Join(fixture.workspaceRoot, "test.c"), sourceLink); err != nil {
		if runtime.GOOS == "windows" {
			t.Skipf("creating symlink requires an unavailable Windows privilege: %v", err)
		}
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	args := replaceFlagValue(fixture.args(), "--source", "linked.c")
	if code := run(args, &stdout, &stderr); code == 0 {
		t.Fatal("run() accepted a source symlink")
	}

	outside := filepath.Join(t.TempDir(), "outside.c")
	if err := os.WriteFile(outside, []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, fixture.runnerPath); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	stderr.Reset()
	if code := run(fixture.args(), &stdout, &stderr); code == 0 {
		t.Fatal("run() accepted an output symlink")
	}
	contents, err := os.ReadFile(outside)
	if err != nil {
		t.Fatal(err)
	}
	if string(contents) != "outside" {
		t.Fatalf("outside output was overwritten: %q", contents)
	}
}

func TestUnityRunnerRejectsOutputParentLinkSwap(t *testing.T) {
	fixture := newCLIFixture(t)
	root, err := workspace.OpenRoot(fixture.buildRoot)
	if err != nil {
		t.Fatal(err)
	}
	output, err := resolveOutput(root, "generated/runner.c")
	if err != nil {
		t.Fatal(err)
	}
	originalParent := filepath.Join(fixture.buildRoot, "generated")
	movedParent := filepath.Join(fixture.buildRoot, "generated-original")
	if err := os.Rename(originalParent, movedParent); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	if err := os.Symlink(outside, originalParent); err != nil {
		if runtime.GOOS == "windows" {
			t.Skipf("creating a directory symlink requires an unavailable Windows privilege: %v", err)
		}
		t.Fatal(err)
	}
	if err := revalidateOutput(root, output); err == nil {
		t.Fatal("revalidateOutput() accepted a replaced output parent")
	}
	if _, err := os.Stat(filepath.Join(outside, "runner.c")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("outside output was created: %v", err)
	}
}

func TestUnityRunnerAtomicWritePreservesDestinationOnPublishFailure(t *testing.T) {
	directory := t.TempDir()
	destination := filepath.Join(directory, "runner.c")
	if err := os.WriteFile(destination, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	originalReplace := replacePublishedFile
	replacePublishedFile = func(_, _ string) error {
		return errors.New("injected replace failure")
	}
	t.Cleanup(func() { replacePublishedFile = originalReplace })

	if err := atomicWriteFile(destination, []byte("new"), 0o600); err == nil {
		t.Fatal("atomicWriteFile() succeeded")
	}
	contents, err := os.ReadFile(destination)
	if err != nil {
		t.Fatal(err)
	}
	if string(contents) != "old" {
		t.Fatalf("destination = %q, want old bytes", contents)
	}
	matches, err := filepath.Glob(filepath.Join(directory, ".utide-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 0 {
		t.Fatalf("temporary outputs remain: %#v", matches)
	}
}

type cliFixture struct {
	workspaceRoot string
	buildRoot     string
	runnerPath    string
	manifestPath  string
}

func newCLIFixture(t *testing.T) cliFixture {
	t.Helper()
	workspaceRoot := filepath.Join(t.TempDir(), "workspace")
	buildRoot := filepath.Join(t.TempDir(), "build")
	outputDirectory := filepath.Join(buildRoot, "generated")
	for _, directory := range []string{workspaceRoot, outputDirectory} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	source := `
#include "unity.h"
void setUp(void) {}
void tearDown(void) {}
void test_cli(void) {}
`
	if err := os.WriteFile(filepath.Join(workspaceRoot, "test.c"), []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	return cliFixture{
		workspaceRoot: workspaceRoot,
		buildRoot:     buildRoot,
		runnerPath:    filepath.Join(outputDirectory, "runner.c"),
		manifestPath:  filepath.Join(outputDirectory, "manifest.json"),
	}
}

func (fixture cliFixture) args() []string {
	return []string{
		"generate",
		"--workspace-root", fixture.workspaceRoot,
		"--build-root", fixture.buildRoot,
		"--runner", "generated/runner.c",
		"--manifest", "generated/manifest.json",
		"--source", "test.c",
	}
}

func replaceFlagValue(args []string, flag, value string) []string {
	result := append([]string(nil), args...)
	for index := 0; index+1 < len(result); index++ {
		if result[index] == flag {
			result[index+1] = value
			return result
		}
	}
	panic("missing flag " + flag + " in " + strings.Join(args, " "))
}
