//go:build !windows

package testgenpublish

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestGeneratedFileIsReadableWithPrivateModeOnUnix(t *testing.T) {
	f := newFixture(t)
	defer f.close(t)
	plan := f.plan(t)
	if _, err := f.p.Accept(context.Background(), f.request(plan)); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(f.root, "tests", "generated", "choose_test.cpp")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("generated mode = %v, want 0600", info.Mode().Perm())
	}
	if data, err := os.ReadFile(path); err != nil || len(data) == 0 {
		t.Fatalf("unreadable generated test: %v", err)
	}
}
