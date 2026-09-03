//go:build !windows

package diagnostic

import (
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"unit-test-ide.local/test-service/internal/workspace"
)

func TestPOSIXPublicURIUsesCanonicalPathForSymlinkAlias(t *testing.T) {
	base := t.TempDir()
	rootPath := filepath.Join(base, "root")
	sourceDir := filepath.Join(rootPath, "src")
	alias := filepath.Join(base, "source-alias")
	if err := os.MkdirAll(sourceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(sourceDir, alias); err != nil {
		t.Fatal(err)
	}
	root, err := workspace.OpenRoot(rootPath)
	if err != nil {
		t.Fatal(err)
	}
	value, err := NewParser(FamilyGNU, Options{
		Root: root, WorkingDirectory: root.NativePath,
	})
	if err != nil {
		t.Fatal(err)
	}

	aliasedSource := filepath.Join(alias, "main.cpp")
	diagnostics := append(value.Feed("stderr", []byte(
		aliasedSource+":3:1: error: broken\n",
	)), value.Close()...)
	if len(diagnostics) != 1 {
		t.Fatalf("diagnostics = %#v, want one", diagnostics)
	}
	wantFileURI := (&url.URL{Scheme: "file", Path: filepath.ToSlash(aliasedSource)}).String()
	if diagnostics[0].FileURI != wantFileURI {
		t.Fatalf("ordinary FileURI = %q, want lexical alias %q", diagnostics[0].FileURI, wantFileURI)
	}
	provider, ok := value.(PublicURIProvider)
	if !ok {
		t.Fatal("parser does not expose PublicURIProvider")
	}
	if got := provider.PublicURI(diagnostics[0].FileURI); got != "workspace:///src/main.cpp" {
		t.Fatalf("PublicURI(%q) = %q, want canonical workspace path", diagnostics[0].FileURI, got)
	}
}

func TestPOSIXDiagnosticIdentityRemainsCaseSensitive(t *testing.T) {
	lower := &parser{
		options:     Options{Root: workspace.Root{URI: "file:///tmp/workspace"}},
		occurrences: make(map[string]int),
	}
	upper := &parser{
		options:     Options{Root: workspace.Root{URI: "file:///tmp/workspace"}},
		occurrences: make(map[string]int),
	}
	lowerValue := Diagnostic{
		Source: "compiler", Severity: "error", Code: "COMPILER_ERROR",
		Message: "broken", FileURI: "file:///tmp/workspace/src/main.cpp",
	}
	upperValue := lowerValue
	upperValue.FileURI = "file:///tmp/workspace/src/Main.cpp"

	if lower.diagnosticID(lowerValue) == upper.diagnosticID(upperValue) {
		t.Fatal("POSIX filename casing collapsed to one diagnostic identity")
	}
}
