package coveragenormalize

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

func TestBindSourcePathCreatesRelativeEscapedURIAndHidesNativePathFromPublicClone(t *testing.T) {
	root := t.TempDir()
	native := filepath.Join(root, "src", "hello world.cpp")
	binding, err := BindSourcePath(root, native)
	if err != nil {
		t.Fatal(err)
	}
	wantURI := "src/hello%20world.cpp"
	if binding.URI != wantURI || binding.NativePath != native {
		t.Fatalf("binding = %#v, want URI %q", binding, wantURI)
	}
	public := binding.Public()
	if public.URI != binding.URI || public.SHA256 != binding.SHA256 || public.NativePath != "" {
		t.Fatalf("public binding = %#v", public)
	}
}

func TestBindSourcePathRejectsOutsideAndExcludedBoundaries(t *testing.T) {
	root := t.TempDir()
	for name, path := range map[string]string{
		"outside":   filepath.Join(filepath.Dir(root), "outside.cpp"),
		"root":      root,
		"build":     filepath.Join(root, "build", "generated.cpp"),
		"data":      filepath.Join(root, "data", "fixture.cpp"),
		"git":       filepath.Join(root, ".git", "config"),
		"traversal": filepath.Join(root, "src", "..", "..", "outside.cpp"),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := BindSourcePath(root, path); !errors.Is(err, ErrInvalidSourcePath) {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestBindSourcePathRejectsMalformedValuesAndSeparatorsAreCanonical(t *testing.T) {
	root := t.TempDir()
	if _, err := BindSourcePath("relative-root", filepath.Join(root, "src", "file.c")); !errors.Is(err, ErrInvalidSourcePath) {
		t.Fatalf("relative root error = %v", err)
	}
	if _, err := BindSourcePath(root, "relative-file.c"); !errors.Is(err, ErrInvalidSourcePath) {
		t.Fatalf("relative path error = %v", err)
	}
	if _, err := BindSourcePath(root+"\x00", filepath.Join(root, "src", "file.c")); !errors.Is(err, ErrInvalidSourcePath) {
		t.Fatalf("NUL root error = %v", err)
	}
	if _, err := BindSourcePath(root, filepath.Join(root, "src", "bad\x00.c")); !errors.Is(err, ErrInvalidSourcePath) {
		t.Fatalf("NUL path error = %v", err)
	}
	if _, err := BindSourcePath(root, filepath.Join(root, string([]byte{0xff})+".c")); !errors.Is(err, ErrInvalidSourcePath) {
		t.Fatalf("invalid UTF-8 error = %v", err)
	}
	binding, err := BindSourcePath(root, filepath.Join(root, "src", "nested", "file.c"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(binding.URI, "\\") || strings.HasPrefix(binding.URI, "/") || strings.Contains(binding.URI, "..") {
		t.Fatalf("non-canonical URI = %q", binding.URI)
	}
}
