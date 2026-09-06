package coveragegcc

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

const goldenGCCInstrumentation = "cmake_minimum_required(VERSION 3.25)\n" +
	"if(NOT CMAKE_C_COMPILER_ID STREQUAL \"GNU\" OR NOT CMAKE_CXX_COMPILER_ID STREQUAL \"GNU\")\n" +
	"  message(FATAL_ERROR \"unit-test-ide coverage requires GCC and G++\")\n" +
	"endif()\n" +
	"add_compile_options(\"$<$<COMPILE_LANGUAGE:C,CXX>:--coverage>\" \"$<$<COMPILE_LANGUAGE:C,CXX>:-O0>\" \"$<$<COMPILE_LANGUAGE:C,CXX>:-g>\")\n" +
	"add_link_options(\"--coverage\")\n"

func TestWriteInstrumentationPublishesExactGCCContract(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("GCC instrumentation is intentionally unsupported on Windows")
	}
	root := filepath.Join(t.TempDir(), "root")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	value, err := WriteInstrumentation(root)
	if err != nil {
		t.Fatal(err)
	}
	bytes, err := os.ReadFile(value.IncludePath)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(bytes); got != goldenGCCInstrumentation {
		t.Fatalf("contents = %q", got)
	}
	sum := sha256.Sum256([]byte(goldenGCCInstrumentation))
	if got, want := value.SHA256, hex.EncodeToString(sum[:]); got != want {
		t.Fatalf("SHA = %q, want %q", got, want)
	}
	if got := value.Fingerprint; got != InstrumentationFingerprint() {
		t.Fatalf("fingerprint = %q", got)
	}
}

func TestWriteInstrumentationWindowsHasNoSideEffects(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows-only unsupported contract")
	}
	root := filepath.Join(t.TempDir(), "root")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := WriteInstrumentation(root); err == nil {
		t.Fatal("Windows GCC instrumentation succeeded")
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 0 {
		t.Fatalf("unsupported call changed root: entries=%#v err=%v", entries, err)
	}
}
