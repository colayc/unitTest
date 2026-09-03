//go:build windows

package diagnostic

import (
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"unit-test-ide.local/test-service/internal/workspace"
)

func TestWindowsWorkspaceDiagnosticIDIgnoresDriveAndPathCasingAcrossRoots(t *testing.T) {
	parse := func(root workspace.Root, upper bool) Diagnostic {
		rootPath := strings.ToLower(root.NativePath)
		sourcePath := filepath.Join(rootPath, "src", "main.cpp")
		headerPath := filepath.Join(rootPath, "include", "header.hpp")
		if upper {
			sourcePath = strings.ToUpper(sourcePath)
			headerPath = strings.ToUpper(headerPath)
		}
		parser, err := NewParser(FamilyGNU, Options{
			Root: root, WorkingDirectory: root.NativePath,
		})
		if err != nil {
			t.Fatal(err)
		}
		values := append(parser.Feed("stderr", []byte(
			sourcePath+":1:1: error: broken\n"+
				headerPath+":2:1: note: declared here\n",
		)), parser.Close()...)
		if len(values) != 1 {
			t.Fatalf("diagnostics=%#v", values)
		}
		return values[0]
	}

	firstRoot, err := workspace.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	secondRoot, err := workspace.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	first := parse(firstRoot, true)
	second := parse(secondRoot, false)

	if first.ID != second.ID {
		t.Fatalf("case/root-stable IDs differ: %q != %q", first.ID, second.ID)
	}
	for name, value := range map[string]Diagnostic{"upper": first, "lower": second} {
		if value.External || strings.HasPrefix(value.FileURI, "workspace:") ||
			len(value.Related) != 1 || strings.HasPrefix(value.Related[0].FileURI, "workspace:") {
			t.Fatalf("%s diagnostic output mutated=%#v", name, value)
		}
	}
	firstURI, err := url.Parse(first.FileURI)
	if err != nil {
		t.Fatal(err)
	}
	secondURI, err := url.Parse(second.FileURI)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(firstURI.Path, "/SRC/MAIN.CPP") ||
		!strings.HasSuffix(secondURI.Path, "/src/main.cpp") {
		t.Fatalf("output casing changed: upper=%q lower=%q", first.FileURI, second.FileURI)
	}
}

func TestWindowsUNCDiagnosticIdentityIgnoresHostShareAndPathCasing(t *testing.T) {
	first := &parser{
		options: Options{Root: workspace.Root{
			URI: "file://BuildServer/TeamShare/FirstRoot",
		}},
		occurrences: make(map[string]int),
	}
	second := &parser{
		options: Options{Root: workspace.Root{
			URI: "file://buildserver/teamshare/secondroot",
		}},
		occurrences: make(map[string]int),
	}
	firstValue := Diagnostic{
		Source: "compiler", Severity: "error", Code: "COMPILER_ERROR", Message: "broken",
		FileURI: "file://BUILDSERVER/TEAMSHARE/FIRSTROOT/SRC/Main.CPP",
		Related: []Related{{
			Message: "declared here",
			FileURI: "file://BuildServer/TeamShare/FirstRoot/INCLUDE/Header.HPP",
		}},
	}
	secondValue := Diagnostic{
		Source: "compiler", Severity: "error", Code: "COMPILER_ERROR", Message: "broken",
		FileURI: "file://buildserver/teamshare/secondroot/src/main.cpp",
		Related: []Related{{
			Message: "declared here",
			FileURI: "file://buildserver/teamshare/secondroot/include/header.hpp",
		}},
	}
	firstOriginal := cloneDiagnostic(firstValue)
	secondOriginal := cloneDiagnostic(secondValue)

	if first.diagnosticID(firstValue) != second.diagnosticID(secondValue) {
		t.Fatal("UNC casing or root changed diagnostic identity")
	}
	if firstValue.FileURI != firstOriginal.FileURI ||
		firstValue.Related[0].FileURI != firstOriginal.Related[0].FileURI ||
		secondValue.FileURI != secondOriginal.FileURI ||
		secondValue.Related[0].FileURI != secondOriginal.Related[0].FileURI {
		t.Fatal("fingerprint calculation mutated output URI")
	}
	const outside = "file://buildserver/teamshare/FirstRooted/src/main.cpp"
	if got := first.identityURI(outside); got != outside {
		t.Fatalf("UNC sibling crossed workspace path boundary: %q", got)
	}
}

func TestWindowsWorkspacePublicURIMapsDriveAndUNCRootsWithoutLeakingHosts(t *testing.T) {
	root, err := workspace.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	parser, err := NewParser(FamilyGNU, Options{
		Root:             root,
		WorkingDirectory: root.NativePath,
	})
	if err != nil {
		t.Fatal(err)
	}
	provider, ok := parser.(PublicURIProvider)
	if !ok {
		t.Fatal("parser does not expose PublicURIProvider")
	}

	childInput := strings.ToUpper(root.URI) + "/SRC/MAIN.CPP"
	childWant := "workspace:///SRC/MAIN.CPP"
	if got := provider.PublicURI(childInput); got != childWant {
		t.Fatalf("drive-root child PublicURI(%q) = %q, want %q", childInput, got, childWant)
	}

	rootWant := "workspace:///"
	for _, value := range []string{
		"file:///D:/sdk/include/header.hpp",
		"file://buildserver/teamshare/sdk/include/header.hpp",
	} {
		got := provider.PublicURI(value)
		if got != rootWant {
			t.Fatalf("external PublicURI(%q) = %q, want %q", value, got, rootWant)
		}
		parsed, err := url.Parse(got)
		if err != nil {
			t.Fatal(err)
		}
		if parsed.Host != "" || parsed.Path != "/" {
			t.Fatalf("external workspace URI leaked host or drive: %q", got)
		}
	}

	parsed, err := url.Parse(provider.PublicURI(childInput))
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Host != "" || !strings.HasPrefix(parsed.Path, "/SRC/MAIN.CPP") {
		t.Fatalf("child workspace URI lost path casing or leaked host: %q", parsed.String())
	}
}
