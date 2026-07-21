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

func TestRunRejectsPositionalArguments(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"unexpected"}, &stdout, &stderr); code != 2 {
		t.Fatalf("run code = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "positional arguments") {
		t.Fatalf("stderr = %q, want positional argument error", stderr.String())
	}
}
