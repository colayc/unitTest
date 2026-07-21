//go:build !windows

package main

import (
	"os"
	"path/filepath"
	"testing"
)

func prepareTokenFileForTest(string) error { return nil }

func TestConsumeTokenFileRejectsPermissiveModeAndRemovesIt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(path, []byte("0123456789abcdef"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := consumeTokenFile(path); err == nil {
		t.Fatal("expected permissive mode to be rejected")
	}
	assertRemoved(t, path)
}
