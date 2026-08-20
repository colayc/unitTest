package coveragellvm

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

const goldenInstrumentation = "cmake_minimum_required(VERSION 3.25)\n" +
	"if(NOT CMAKE_CXX_COMPILER_ID MATCHES \"Clang\")\n" +
	"  message(FATAL_ERROR \"unit-test-ide coverage requires clang-cl\")\n" +
	"endif()\n" +
	"add_compile_options(\"$<$<COMPILE_LANGUAGE:C,CXX>:-fprofile-instr-generate>\" \"$<$<COMPILE_LANGUAGE:C,CXX>:-fcoverage-mapping>\")\n" +
	"add_link_options(\"-fprofile-instr-generate\")\n"

func TestWriteInstrumentationPublishesGoldenReadOnlyInclude(t *testing.T) {
	root := filepath.Join(t.TempDir(), "task")
	makeOwnerOnlyInstrumentationRoot(t, root)
	got, err := WriteInstrumentation(root)
	if err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(got.IncludePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(contents) != goldenInstrumentation {
		t.Fatalf("instrumentation contents = %q, want byte-identical LF golden", contents)
	}
	sum := sha256.Sum256([]byte(goldenInstrumentation))
	wantDigest := hex.EncodeToString(sum[:])
	if got.SHA256 != wantDigest {
		t.Fatalf("SHA256 = %q, want %q", got.SHA256, wantDigest)
	}
	if len(got.Fingerprint) != 64 || got.Fingerprint != strings.ToLower(got.Fingerprint) {
		t.Fatalf("Fingerprint = %q, want lowercase SHA-256", got.Fingerprint)
	}
	info, err := os.Lstat(got.IncludePath)
	if err != nil {
		t.Fatal(err)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("instrumentation mode = %v, want direct regular file", info.Mode())
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o222 != 0 {
		t.Fatalf("instrumentation mode = %v, want read-only", info.Mode())
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != filepath.Base(got.IncludePath) {
		t.Fatalf("task root entries = %#v, want only published instrumentation", entries)
	}
}

func TestWriteInstrumentationRejectsNonFreshOrAliasedTaskRoot(t *testing.T) {
	t.Run("non-fresh", func(t *testing.T) {
		root := filepath.Join(t.TempDir(), "task")
		makeOwnerOnlyInstrumentationRoot(t, root)
		if err := os.WriteFile(filepath.Join(root, "foreign"), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := WriteInstrumentation(root); err == nil {
			t.Fatal("WriteInstrumentation accepted a non-fresh Task root")
		}
	})

	t.Run("symlink", func(t *testing.T) {
		target := filepath.Join(t.TempDir(), "target")
		makeOwnerOnlyInstrumentationRoot(t, target)
		link := filepath.Join(filepath.Dir(target), "alias")
		if err := os.Symlink(target, link); err != nil {
			t.Skipf("symlinks are unavailable: %v", err)
		}
		if _, err := WriteInstrumentation(link); err == nil {
			t.Fatal("WriteInstrumentation accepted an aliased Task root")
		}
	})
}
