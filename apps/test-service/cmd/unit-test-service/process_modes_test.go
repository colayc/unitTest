package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunRejectsMixedInternalAndPublicModesBeforeSideEffects(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{name: "host and endpoint", args: []string{"--process-host", "--endpoint=unused"}},
		{name: "host and token", args: []string{"--process-host", "--token-file=TOKEN"}},
		{name: "host and preparation", args: []string{"--process-host", "--prepare-token-file=PREPARE"}},
		{name: "fixture and endpoint", args: []string{"--task-fixture=success", "--endpoint=unused"}},
		{name: "fixture and token", args: []string{"--task-fixture=success", "--token-file=TOKEN"}},
		{name: "fixture and preparation", args: []string{"--task-fixture=success", "--prepare-token-file=PREPARE"}},
		{name: "child and endpoint", args: []string{"--task-fixture-child", "--endpoint=unused"}},
		{name: "multiple internal", args: []string{"--process-host", "--task-fixture=success"}},
		{name: "probe supervisor and endpoint", args: []string{"--probe-supervisor", "--endpoint=unused"}},
		{name: "probe supervisor and host", args: []string{"--probe-supervisor", "--process-host"}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			directory := t.TempDir()
			tokenPath := filepath.Join(directory, "token")
			preparePath := filepath.Join(directory, "prepared")
			if err := os.WriteFile(tokenPath, []byte("0123456789abcdef"), 0o600); err != nil {
				t.Fatal(err)
			}
			args := make([]string, len(test.args))
			for index, argument := range test.args {
				argument = strings.ReplaceAll(argument, "TOKEN", tokenPath)
				args[index] = strings.ReplaceAll(argument, "PREPARE", preparePath)
			}
			var stdout, stderr bytes.Buffer
			if code := run(args, strings.NewReader(""), &stdout, &stderr); code != 2 {
				t.Fatalf("code = %d, stderr = %q", code, stderr.String())
			}
			contents, err := os.ReadFile(tokenPath)
			if err != nil || string(contents) != "0123456789abcdef" {
				t.Fatalf("token was consumed: contents=%q err=%v", contents, err)
			}
			if _, err := os.Stat(preparePath); !os.IsNotExist(err) {
				t.Fatalf("preparation path was created: %v", err)
			}
		})
	}
}

func TestRunProbeSupervisorDelegatesAsExclusiveInternalMode(t *testing.T) {
	input := strings.NewReader("bounded-control-frame")
	var stdout, stderr bytes.Buffer
	previous := probeSupervisorEntry
	probeSupervisorEntry = func(stdin io.Reader, gotStdout, gotStderr io.Writer) int {
		if stdin != input || gotStdout != &stdout || gotStderr != &stderr {
			t.Fatal("probe supervisor streams were not passed through")
		}
		return 29
	}
	defer func() { probeSupervisorEntry = previous }()

	if code := run([]string{"--probe-supervisor"}, input, &stdout, &stderr); code != 29 {
		t.Fatalf("code = %d, want 29; stderr = %q", code, stderr.String())
	}
}

func TestRunTaskFixtureModeAllowsOnlyEnumeratedScenarios(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"--task-fixture=emit-output"}, strings.NewReader(""), &stdout, &stderr); code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, stderr.String())
	}
	if stdout.String() != "fixture stdout\n" || stderr.String() != "fixture stderr\n" {
		t.Fatalf("stdout=%q stderr=%q", stdout.String(), stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"--task-fixture=unknown"}, strings.NewReader(""), &stdout, &stderr); code != 2 {
		t.Fatalf("code = %d, stderr = %q", code, stderr.String())
	}
	if stderr.String() != "unknown fixture scenario\n" {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestRunProcessHostRequiresValidInheritedStatusHandle(t *testing.T) {
	for _, value := range []string{"", "invalid", "0", "-1"} {
		t.Run(value, func(t *testing.T) {
			t.Setenv("UNIT_TEST_IDE_STATUS_HANDLE", value)
			called := false
			previous := processHostEntry
			processHostEntry = func(io.Reader, io.Writer, io.Writer) int { called = true; return 0 }
			defer func() { processHostEntry = previous }()
			var stdout, stderr bytes.Buffer
			if code := run([]string{"--process-host"}, strings.NewReader(""), &stdout, &stderr); code != 2 {
				t.Fatalf("code = %d, stderr = %q", code, stderr.String())
			}
			if called {
				t.Fatal("process host entry was called with invalid status handle")
			}
			if strings.Contains(stderr.String(), value) && value != "" {
				t.Fatalf("stderr reflected handle: %q", stderr.String())
			}
		})
	}
}

func TestRunProcessHostDelegatesWithoutReadingTokenOrCreatingIPC(t *testing.T) {
	t.Setenv("UNIT_TEST_IDE_STATUS_HANDLE", "123")
	directory := t.TempDir()
	tokenPath := filepath.Join(directory, "token")
	if err := os.WriteFile(tokenPath, []byte("0123456789abcdef"), 0o600); err != nil {
		t.Fatal(err)
	}
	input := strings.NewReader("control-frame")
	var stdout, stderr bytes.Buffer
	previous := processHostEntry
	processHostEntry = func(stdin io.Reader, gotStdout, gotStderr io.Writer) int {
		if stdin != input || gotStdout != &stdout || gotStderr != &stderr {
			t.Fatal("process host streams were not passed through")
		}
		return 23
	}
	defer func() { processHostEntry = previous }()

	if code := run([]string{"--process-host"}, input, &stdout, &stderr); code != 23 {
		t.Fatalf("code = %d", code)
	}
	contents, err := os.ReadFile(tokenPath)
	if err != nil || string(contents) != "0123456789abcdef" {
		t.Fatalf("unrelated token changed: contents=%q err=%v", contents, err)
	}
}

func TestRunRejectsArbitraryProcessFlags(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"--task-fixture=success", "--executable=C:\\private\\program.exe"}, strings.NewReader(""), &stdout, &stderr)
	if code != 2 {
		t.Fatalf("code = %d", code)
	}
	if strings.Contains(stderr.String(), "private") {
		t.Fatalf("stderr leaked rejected executable: %q", stderr.String())
	}
}
