//go:build windows

package diagnostic

import (
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"unit-test-ide.local/test-service/internal/workspace"
)

func TestWindowsPublicURIUsesCanonicalPathForJunctionAlias(t *testing.T) {
	base := t.TempDir()
	rootPath := filepath.Join(base, "root")
	sourceDir := filepath.Join(rootPath, "src")
	alias := filepath.Join(base, "source-alias")
	if err := os.MkdirAll(sourceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	createDiagnosticTestJunction(t, alias, sourceDir)
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
	wantFileURI := (&url.URL{Scheme: "file", Path: "/" + filepath.ToSlash(aliasedSource)}).String()
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

func TestWindowsPublicURIUsesCanonicalPathAcrossVolumeAlias(t *testing.T) {
	base := t.TempDir()
	rootPath := filepath.Join(base, "root")
	sourceDir := filepath.Join(rootPath, "src")
	if err := os.MkdirAll(sourceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	alias, cleanup := crossVolumeDiagnosticJunction(t, sourceDir, filepath.VolumeName(base))
	defer cleanup()
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
	provider, ok := value.(PublicURIProvider)
	if !ok {
		t.Fatal("parser does not expose PublicURIProvider")
	}
	aliasedSource := filepath.Join(alias, "main.cpp")
	input := (&url.URL{Scheme: "file", Path: "/" + filepath.ToSlash(aliasedSource)}).String()
	if got := provider.PublicURI(input); got != "workspace:///src/main.cpp" {
		t.Fatalf("cross-volume PublicURI(%q) = %q, want canonical workspace path", input, got)
	}
}

func createDiagnosticTestJunction(t *testing.T, link, target string) {
	t.Helper()
	if output, err := exec.Command("cmd.exe", "/d", "/c", "mklink", "/J", link, target).CombinedOutput(); err != nil {
		t.Fatalf("create directory junction: %v\ncommand output: %s", err, strings.TrimSpace(string(output)))
	}
}

func crossVolumeDiagnosticJunction(t *testing.T, target, excludedVolume string) (string, func()) {
	t.Helper()
	for drive := 'A'; drive <= 'Z'; drive++ {
		volumeRoot := string(drive) + `:\`
		if strings.EqualFold(filepath.VolumeName(volumeRoot), excludedVolume) {
			continue
		}
		base, err := os.MkdirTemp(volumeRoot, "unit-test-ide-diagnostic-")
		if err != nil {
			continue
		}
		alias := filepath.Join(base, "source-alias")
		output, err := exec.Command("cmd.exe", "/d", "/c", "mklink", "/J", alias, target).CombinedOutput()
		if err == nil {
			return alias, func() {
				_ = os.Remove(alias)
				_ = os.RemoveAll(base)
			}
		}
		_ = os.RemoveAll(base)
		t.Logf("volume %s rejected cross-volume junction: %v (%s)", volumeRoot, err, strings.TrimSpace(string(output)))
	}
	t.Skip("no writable second volume supports a cross-volume junction")
	return "", func() {}
}

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
