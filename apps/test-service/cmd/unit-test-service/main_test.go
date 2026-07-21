package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunPrepareTokenFileModeCreatesEmptyFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "token")
	var stdout, stderr bytes.Buffer
	if code := run([]string{"--prepare-token-file", path}, &stdout, &stderr); code != 0 {
		t.Fatalf("run code = %d, stderr = %q", code, stderr.String())
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() != 0 {
		t.Fatalf("prepared file size = %d, want 0", info.Size())
	}
}

func TestRunRejectsMixedPreparationAndServiceModes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "token")
	var stdout, stderr bytes.Buffer
	code := run([]string{
		"--prepare-token-file", path,
		"--endpoint", "unused-endpoint",
		"--token-file", "unused-token",
	}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("run code = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "cannot be combined") {
		t.Fatalf("stderr = %q, want combination error", stderr.String())
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("mixed mode created token path: %v", err)
	}
}

func TestRunRejectsEmptyEndpointFlagInPreparationMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "token")
	var stdout, stderr bytes.Buffer
	code := run([]string{"--prepare-token-file", path, "--endpoint="}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("run code = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "cannot be combined") {
		t.Fatalf("stderr = %q, want combination error", stderr.String())
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("mixed mode created token path: %v", err)
	}
}

func TestRunRejectsEmptyTokenFileFlagInPreparationMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "token")
	var stdout, stderr bytes.Buffer
	code := run([]string{"--prepare-token-file", path, "--token-file="}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("run code = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "cannot be combined") {
		t.Fatalf("stderr = %q, want combination error", stderr.String())
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("mixed mode created token path: %v", err)
	}
}

func TestRunRejectsExplicitEmptyPreparationPath(t *testing.T) {
	tests := []struct {
		name        string
		serviceArgs []string
		wantError   string
	}{
		{name: "preparation only", wantError: "--prepare-token-file requires a non-empty path"},
		{name: "with endpoint", serviceArgs: []string{"--endpoint", "unused-endpoint"}, wantError: "cannot be combined"},
		{name: "with token file", serviceArgs: []string{"--token-file", "TOKEN_FILE"}, wantError: "cannot be combined"},
		{name: "with both service flags", serviceArgs: []string{"--endpoint", "unused-endpoint", "--token-file", "TOKEN_FILE"}, wantError: "cannot be combined"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			directory := t.TempDir()
			tokenPath := filepath.Join(directory, "service-token")
			if err := os.WriteFile(tokenPath, []byte("0123456789abcdef"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := prepareTokenFileForTest(tokenPath); err != nil {
				t.Fatal(err)
			}

			args := []string{"--prepare-token-file="}
			for _, arg := range test.serviceArgs {
				if arg == "TOKEN_FILE" {
					arg = tokenPath
				}
				args = append(args, arg)
			}

			var stdout, stderr bytes.Buffer
			if code := run(args, &stdout, &stderr); code != 2 {
				t.Fatalf("run code = %d, want 2; stderr = %q", code, stderr.String())
			}
			if !strings.Contains(stderr.String(), test.wantError) {
				t.Fatalf("stderr = %q, want %q", stderr.String(), test.wantError)
			}
			if stdout.Len() != 0 {
				t.Fatalf("stdout = %q, want empty", stdout.String())
			}
			contents, err := os.ReadFile(tokenPath)
			if err != nil {
				t.Fatalf("service token was consumed: %v", err)
			}
			if string(contents) != "0123456789abcdef" {
				t.Fatalf("service token contents = %q, want unchanged", contents)
			}
		})
	}
}

func TestRunRejectsPositionalArguments(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"unexpected"}, &stdout, &stderr); code != 2 {
		t.Fatalf("run code = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "positional arguments") {
		t.Fatalf("stderr = %q, want positional argument error", stderr.String())
	}
}
