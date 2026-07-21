package main

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestConsumeTokenFileConsumesValidRegularFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(path, []byte("0123456789abcdef\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := prepareTokenFileForTest(path); err != nil {
		t.Fatal(err)
	}
	token, err := consumeTokenFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if token != "0123456789abcdef" {
		t.Fatalf("token = %q", token)
	}
	assertRemoved(t, path)
}

func TestConsumeTokenFileRemovesShortToken(t *testing.T) {
	path := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(path, []byte("short"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := prepareTokenFileForTest(path); err != nil {
		t.Fatal(err)
	}
	if _, err := consumeTokenFile(path); err == nil {
		t.Fatal("expected short token to be rejected")
	}
	assertRemoved(t, path)
}

func TestConsumeTokenFilePreservesInvalidNonSymlinkType(t *testing.T) {
	path := filepath.Join(t.TempDir(), "token")
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := consumeTokenFile(path); err == nil {
		t.Fatal("expected directory to be rejected")
	}
	info, statErr := os.Lstat(path)
	if statErr != nil {
		t.Fatalf("invalid non-symlink path was not preserved: %v", statErr)
	}
	if !info.IsDir() {
		t.Fatalf("preserved path mode = %v, want directory", info.Mode())
	}
}

func TestConsumeTokenFileRemovesSymlink(t *testing.T) {
	directory := t.TempDir()
	target := filepath.Join(directory, "target")
	path := filepath.Join(directory, "token")
	if err := os.WriteFile(target, []byte("0123456789abcdef"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, path); err != nil {
		if runtime.GOOS == "windows" {
			t.Skipf("symlink creation is unavailable: %v", err)
		}
		t.Fatal(err)
	}
	if _, err := consumeTokenFile(path); err == nil {
		t.Fatal("expected symlink to be rejected")
	}
	assertRemoved(t, path)
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("symlink target was affected: %v", err)
	}
}

func TestConsumeTokenFileRejectsOversizedFileAndRemovesIt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(path, make([]byte, maxTokenFileBytes+1), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := prepareTokenFileForTest(path); err != nil {
		t.Fatal(err)
	}
	if _, err := consumeTokenFile(path); err == nil {
		t.Fatal("expected oversized token file to be rejected")
	}
	assertRemoved(t, path)
}

func assertRemoved(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatalf("path was not removed: %v", err)
	}
}
