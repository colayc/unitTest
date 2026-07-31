package unityrunner

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestGeneratedRunnerCompilesAndExecutesOnCurrentPlatform(t *testing.T) {
	compiler := findCCompiler(t)
	root := t.TempDir()
	sourcePath := filepath.Join(root, "tests.c")
	headerPath := filepath.Join(root, "unity.h")
	stubPath := filepath.Join(root, "unity_stub.c")
	runnerPath := filepath.Join(root, "runner.c")
	executablePath := filepath.Join(root, "unity-runner")
	if runtime.GOOS == "windows" {
		executablePath += ".exe"
	}
	writeCompileFixture(t, sourcePath, compileTestSource)
	writeCompileFixture(t, headerPath, compileTestHeader)
	writeCompileFixture(t, stubPath, compileTestStub)

	manifest, err := ParseSources(root, []string{"tests.c"}, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	runner, manifestJSON, err := Generate(GenerateInput{
		Manifest: manifest, GeneratorVersion: CurrentGeneratorVersion,
	})
	if err != nil {
		t.Fatal(err)
	}
	writeCompileFixture(t, runnerPath, string(runner))
	var generated Manifest
	if err := json.Unmarshal(manifestJSON, &generated); err != nil {
		t.Fatal(err)
	}
	testCase, found := compileTestCase(generated, "test_escape")
	if !found {
		t.Fatalf("generated manifest = %#v", generated)
	}

	compileContext, cancelCompile := context.WithTimeout(
		context.Background(),
		30*time.Second,
	)
	defer cancelCompile()
	command := exec.CommandContext(
		compileContext,
		compiler,
		"-std=c11",
		"-Wall",
		"-Wextra",
		"-Werror",
		"-I", root,
		runnerPath,
		sourcePath,
		stubPath,
		"-o", executablePath,
	)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf(
			"compile generated runner with %s: %v\n%s",
			compiler,
			err,
			output,
		)
	}

	markerPath := filepath.Join(root, "executed.txt")
	listPath := filepath.Join(root, "list.jsonl")
	runRunner(t, executablePath, markerPath,
		"--utide-protocol", "utide.runner.v1",
		"--utide-mode", "list",
		"--utide-result", listPath,
	)
	if _, err := os.Stat(markerPath); !os.IsNotExist(err) {
		t.Fatalf("list mode executed a test, marker error = %v", err)
	}
	listRecords := readCompileRecords(t, listPath)
	if len(listRecords) != len(generated.Cases) {
		t.Fatalf(
			"list records = %d, want %d",
			len(listRecords),
			len(generated.Cases),
		)
	}
	var escapedArguments []string
	for _, record := range listRecords {
		if record.Identity == testCase.Identity {
			escapedArguments = record.Arguments
		}
	}
	if !equalCompileStrings(escapedArguments, testCase.Arguments) {
		t.Fatalf(
			"escaped arguments = %#v, want %#v",
			escapedArguments,
			testCase.Arguments,
		)
	}

	resultPath := filepath.Join(root, "result.jsonl")
	runRunner(t, executablePath, markerPath,
		"--utide-protocol", "utide.runner.v1",
		"--utide-mode", "run",
		"--utide-case", testCase.Identity,
		"--utide-result", resultPath,
	)
	marker, err := os.ReadFile(markerPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(marker) != testCase.Identity {
		t.Fatalf("executed identity = %q, want %q", marker, testCase.Identity)
	}
	results := readCompileRecords(t, resultPath)
	if len(results) != 1 ||
		results[0].Identity != testCase.Identity ||
		results[0].Status != "passed" {
		t.Fatalf("run records = %#v", results)
	}

	if err := os.Remove(markerPath); err != nil {
		t.Fatal(err)
	}
	unknownPath := filepath.Join(root, "unknown.jsonl")
	exitCode := runRunnerExpectFailure(t, executablePath, markerPath,
		"--utide-protocol", "utide.runner.v1",
		"--utide-mode", "run",
		"--utide-case", strings.TrimSuffix(testCase.Identity, ")"),
		"--utide-result", unknownPath,
	)
	if exitCode == 0 {
		t.Fatal("non-exact identity unexpectedly ran")
	}
	if _, err := os.Stat(markerPath); !os.IsNotExist(err) {
		t.Fatalf("non-exact identity executed a test, marker error = %v", err)
	}
	if data, err := os.ReadFile(unknownPath); err != nil || len(data) != 0 {
		t.Fatalf("unknown result = %q, %v", data, err)
	}
}

type compileRecord struct {
	Identity  string   `json:"identity"`
	Arguments []string `json:"arguments"`
	Status    string   `json:"status"`
}

func readCompileRecords(t *testing.T, path string) []compileRecord {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := bytes.Split(bytes.TrimSpace(data), []byte{'\n'})
	result := make([]compileRecord, 0, len(lines))
	for _, line := range lines {
		line = bytes.TrimSuffix(line, []byte{'\r'})
		if len(line) == 0 {
			continue
		}
		var record compileRecord
		if err := json.Unmarshal(line, &record); err != nil {
			t.Fatalf("decode record %q: %v", line, err)
		}
		result = append(result, record)
	}
	return result
}

func runRunner(
	t *testing.T,
	executable string,
	marker string,
	arguments ...string,
) {
	t.Helper()
	contextValue, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	command := exec.CommandContext(contextValue, executable, arguments...)
	command.Env = append(os.Environ(), "UTIDE_COMPILE_TEST_MARKER="+marker)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("run generated runner: %v\n%s", err, output)
	}
}

func runRunnerExpectFailure(
	t *testing.T,
	executable string,
	marker string,
	arguments ...string,
) int {
	t.Helper()
	contextValue, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	command := exec.CommandContext(contextValue, executable, arguments...)
	command.Env = append(os.Environ(), "UTIDE_COMPILE_TEST_MARKER="+marker)
	output, err := command.CombinedOutput()
	if err == nil {
		return 0
	}
	var exitError *exec.ExitError
	if !errors.As(err, &exitError) {
		t.Fatalf("run generated runner: %v\n%s", err, output)
	}
	return exitError.ExitCode()
}

func findCCompiler(t *testing.T) string {
	t.Helper()
	candidates := []string{}
	if configured := os.Getenv("CC"); configured != "" &&
		!strings.ContainsAny(configured, " \t") {
		candidates = append(candidates, configured)
	}
	if runtime.GOOS == "windows" {
		candidates = append(candidates, "gcc", "clang")
	} else {
		candidates = append(candidates, "cc", "gcc", "clang")
	}
	for _, candidate := range candidates {
		if path, err := exec.LookPath(candidate); err == nil {
			return path
		}
	}
	t.Skip("当前平台没有可用的 C compiler")
	return ""
}

func compileTestCase(
	manifest Manifest,
	name string,
) (TestCase, bool) {
	for _, testCase := range manifest.Cases {
		if testCase.Name == name {
			return testCase, true
		}
	}
	return TestCase{}, false
}

func equalCompileStrings(first, second []string) bool {
	if len(first) != len(second) {
		return false
	}
	for index := range first {
		if first[index] != second[index] {
			return false
		}
	}
	return true
}

func writeCompileFixture(t *testing.T, path, data string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
}

const compileTestHeader = `
#ifndef UTIDE_COMPILE_TEST_UNITY_H
#define UTIDE_COMPILE_TEST_UNITY_H

#define TEST_CASE(...)
#define TEST_RANGE(...)

typedef unsigned int UNITY_COUNTER_TYPE;
typedef unsigned int UNITY_LINE_TYPE;

typedef struct UNITY_STORAGE_T {
    UNITY_COUNTER_TYPE TestFailures;
    UNITY_COUNTER_TYPE TestIgnores;
} UNITY_STORAGE_T;

extern UNITY_STORAGE_T Unity;

void UnityBegin(const char *source);
void UnityDefaultTestRun(
    void (*test_function)(void),
    const char *name,
    UNITY_LINE_TYPE line);
int UnityEnd(void);

#endif
`

const compileTestSource = `
#include "unity.h"

void setUp(void)
{
}

void tearDown(void)
{
}

TEST_CASE(7, "quote\"slash\\")
void test_escape(int value, const char *text)
{
    (void)value;
    (void)text;
}

void test_pass(void)
{
}
`

const compileTestStub = `
#include "unity.h"

#include <stdio.h>
#include <stdlib.h>

UNITY_STORAGE_T Unity;

void UnityBegin(const char *source)
{
    (void)source;
    Unity.TestFailures = 0U;
    Unity.TestIgnores = 0U;
}

void UnityDefaultTestRun(
    void (*test_function)(void),
    const char *name,
    UNITY_LINE_TYPE line)
{
    const char *marker = getenv("UTIDE_COMPILE_TEST_MARKER");
    (void)line;
    if (marker != NULL) {
        FILE *file = fopen(marker, "wb");
        if (file != NULL) {
            (void)fputs(name, file);
            (void)fclose(file);
        }
    }
    test_function();
}

int UnityEnd(void)
{
    return Unity.TestFailures == 0U ? 0 : 1;
}
`
