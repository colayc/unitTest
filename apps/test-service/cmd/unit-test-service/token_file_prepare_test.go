package main

import (
	"io"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestPrepareTokenFileCreatesEmptyValidatedFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "token")
	if err := prepareTokenFile(path); err != nil {
		t.Fatal(err)
	}

	info, err := inspectTokenPath(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() != 0 {
		t.Fatalf("prepared token file size = %d, want 0", info.Size())
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("prepared token file mode = %o, want 600", info.Mode().Perm())
	}

	file, err := openTokenFile(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if err := validateTokenFile(file, info); err != nil {
		t.Fatal(err)
	}
	raw, err := io.ReadAll(file)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) != 0 {
		t.Fatalf("prepared token contents = %q, want empty", raw)
	}
}

func TestPrepareTokenFileRejectsExistingPathWithoutChangingIt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(path, []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := prepareTokenFile(path); err == nil {
		t.Fatal("expected an existing token path to be rejected")
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(contents) != "original" {
		t.Fatalf("existing token contents = %q, want original", contents)
	}
}
