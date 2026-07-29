//go:build !windows

package workspace

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUnixResolveRelativeRejectsSymlinkEscapeWithMissingTail(t *testing.T) {
	base := t.TempDir()
	rootPath := filepath.Join(base, "root")
	outside := filepath.Join(base, "outside")
	if err := os.Mkdir(rootPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(rootPath, "external")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(outside, "marker.txt")
	if err := os.WriteFile(marker, []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(filepath.Join(link, "marker.txt")); err != nil || string(data) != "outside" {
		t.Fatalf("symlink did not expose outside marker: data = %q, error = %v", data, err)
	}
	root, err := OpenRoot(rootPath)
	if err != nil {
		t.Fatal(err)
	}

	for _, relative := range []string{
		filepath.Join("external", "marker.txt"),
		filepath.Join("external", "missing", "tail.cpp"),
	} {
		if _, err := root.ResolveRelative(relative); !errors.Is(err, ErrPathOutsideRoot) {
			t.Fatalf("ResolveRelative(%q) error = %v, want ErrPathOutsideRoot", relative, err)
		}
		if root.Contains(filepath.Join(rootPath, relative)) {
			t.Fatalf("Contains(%q) = true for symlink escape", relative)
		}
	}
}

func TestUnixLoadConfigRejectsSourceDirectorySymlinkEscape(t *testing.T) {
	base := t.TempDir()
	rootPath := filepath.Join(base, "root")
	outside := filepath.Join(base, "outside")
	if err := os.Mkdir(rootPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(rootPath, "linked-project")); err != nil {
		t.Fatal(err)
	}

	data := []byte(`{"version":1,"projects":[{"id":"linked","sourceDir":"linked-project"}]}`)
	if _, err := loadConfigAtRoot(t, rootPath, data); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("LoadConfig error = %v, want ErrInvalidConfig for sourceDir symlink escape", err)
	}
}

func TestUnixSymlinkAliasHasSameStableIdentity(t *testing.T) {
	base := t.TempDir()
	target := filepath.Join(base, "target")
	alias := filepath.Join(base, "alias")
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, alias); err != nil {
		t.Fatal(err)
	}

	direct, err := OpenRoot(target)
	if err != nil {
		t.Fatal(err)
	}
	throughAlias, err := OpenRoot(alias)
	if err != nil {
		t.Fatal(err)
	}
	if direct != throughAlias {
		t.Fatalf("symlink alias changed stable root:\ndirect = %#v\nalias  = %#v", direct, throughAlias)
	}
}

func TestUnixContainmentIsCaseSensitive(t *testing.T) {
	base := t.TempDir()
	rootPath := filepath.Join(base, "case-root")
	if err := os.Mkdir(rootPath, 0o755); err != nil {
		t.Fatal(err)
	}
	root, err := OpenRoot(rootPath)
	if err != nil {
		t.Fatal(err)
	}
	differentCase := filepath.Join(base, strings.ToUpper(filepath.Base(rootPath)))
	if root.Contains(differentCase) {
		t.Fatalf("Contains(%q) = true for differently cased Unix sibling", differentCase)
	}
}
