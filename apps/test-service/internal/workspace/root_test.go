package workspace

import (
	"encoding/hex"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOpenRootCanonicalizesAbsolutePathAndKeepsIdentityStable(t *testing.T) {
	workingDirectory := mustWorkingDirectory(t)
	base, err := os.MkdirTemp(workingDirectory, ".workspace-root-test-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(base) })
	rootPath := filepath.Join(base, "workspace with #")
	if err := os.Mkdir(rootPath, 0o755); err != nil {
		t.Fatal(err)
	}
	relative, err := filepath.Rel(workingDirectory, rootPath)
	if err != nil {
		t.Fatal(err)
	}

	first, err := OpenRoot(relative)
	if err != nil {
		t.Fatal(err)
	}
	second, err := OpenRoot(rootPath)
	if err != nil {
		t.Fatal(err)
	}

	if !filepath.IsAbs(first.NativePath) {
		t.Fatalf("NativePath = %q, want absolute path", first.NativePath)
	}
	if first != second {
		t.Fatalf("stable root mismatch:\nfirst  = %#v\nsecond = %#v", first, second)
	}
	parsed, err := url.Parse(first.URI)
	if err != nil {
		t.Fatalf("URI %q: %v", first.URI, err)
	}
	if parsed.Scheme != "file" || strings.Contains(first.URI, `\`) || strings.Contains(first.URI, "#") {
		t.Fatalf("URI = %q, want escaped file URI without native separators", first.URI)
	}
	digest, err := hex.DecodeString(first.ID)
	if err != nil || len(digest) != 32 {
		t.Fatalf("ID = %q, want SHA-256 hex digest: %v", first.ID, err)
	}
}

func TestOpenRootRejectsInvalidRootsWithSentinel(t *testing.T) {
	base := t.TempDir()
	filePath := filepath.Join(base, "file.txt")
	if err := os.WriteFile(filePath, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}

	for name, path := range map[string]string{
		"empty":        "",
		"missing":      filepath.Join(base, "missing"),
		"not-a-dir":    filePath,
		"embedded-nul": rootWithNUL(base),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := OpenRoot(path); !errors.Is(err, ErrInvalidRoot) {
				t.Fatalf("OpenRoot(%q) error = %v, want ErrInvalidRoot", path, err)
			}
		})
	}
}

func TestResolveRelativeAcceptsExistingAndMissingDescendants(t *testing.T) {
	rootPath := t.TempDir()
	existing := filepath.Join(rootPath, "src")
	if err := os.Mkdir(existing, 0o755); err != nil {
		t.Fatal(err)
	}
	root, err := OpenRoot(rootPath)
	if err != nil {
		t.Fatal(err)
	}

	for name, relative := range map[string]string{
		"root":         ".",
		"existing":     "src",
		"missing-tail": filepath.Join("src", "generated", "file.cpp"),
		"cleaned":      filepath.Join("src", "..", "CMakeLists.txt"),
	} {
		t.Run(name, func(t *testing.T) {
			got, err := root.ResolveRelative(relative)
			if err != nil {
				t.Fatal(err)
			}
			want := filepath.Clean(filepath.Join(root.NativePath, relative))
			if got != want {
				t.Fatalf("ResolveRelative(%q) = %q, want %q", relative, got, want)
			}
			if !root.Contains(got) {
				t.Fatalf("Contains(%q) = false", got)
			}
		})
	}
}

func TestResolveRelativeRejectsInvalidOrEscapingInput(t *testing.T) {
	rootPath := t.TempDir()
	root, err := OpenRoot(rootPath)
	if err != nil {
		t.Fatal(err)
	}

	for name, relative := range map[string]string{
		"empty":         "",
		"absolute":      filepath.Join(rootPath, "inside"),
		"root-relative": string(filepath.Separator) + "outside",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := root.ResolveRelative(relative); !errors.Is(err, ErrInvalidRelativePath) {
				t.Fatalf("ResolveRelative(%q) error = %v, want ErrInvalidRelativePath", relative, err)
			}
		})
	}

	for name, relative := range map[string]string{
		"parent":       filepath.Join("..", "outside"),
		"cleaned-away": filepath.Join("src", "..", "..", "outside"),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := root.ResolveRelative(relative); !errors.Is(err, ErrPathOutsideRoot) {
				t.Fatalf("ResolveRelative(%q) error = %v, want ErrPathOutsideRoot", relative, err)
			}
		})
	}
}

func TestContainsUsesPathBoundaryForExistingAndMissingPaths(t *testing.T) {
	base := t.TempDir()
	rootPath := filepath.Join(base, "root")
	sibling := filepath.Join(base, "root-neighbor")
	if err := os.Mkdir(rootPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(sibling, 0o755); err != nil {
		t.Fatal(err)
	}
	root, err := OpenRoot(rootPath)
	if err != nil {
		t.Fatal(err)
	}

	for _, path := range []string{
		root.NativePath,
		filepath.Join(root.NativePath, "missing", "tail"),
	} {
		if !root.Contains(path) {
			t.Fatalf("Contains(%q) = false, want true", path)
		}
	}
	for _, path := range []string{
		"",
		base,
		sibling,
		filepath.Join(sibling, "missing"),
	} {
		if root.Contains(path) {
			t.Fatalf("Contains(%q) = true, want false", path)
		}
	}
}

func TestDifferentRootsHaveDifferentStableIDs(t *testing.T) {
	base := t.TempDir()
	firstPath := filepath.Join(base, "first")
	secondPath := filepath.Join(base, "second")
	if err := os.Mkdir(firstPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(secondPath, 0o755); err != nil {
		t.Fatal(err)
	}
	first, err := OpenRoot(firstPath)
	if err != nil {
		t.Fatal(err)
	}
	second, err := OpenRoot(secondPath)
	if err != nil {
		t.Fatal(err)
	}
	if first.ID == second.ID {
		t.Fatalf("different roots share ID %q", first.ID)
	}
}

func mustWorkingDirectory(t *testing.T) string {
	t.Helper()
	directory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return directory
}

func rootWithNUL(base string) string {
	return base + string(rune(0)) + "invalid"
}
