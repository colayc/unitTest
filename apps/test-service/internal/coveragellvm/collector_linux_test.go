//go:build linux

package coveragellvm

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"unit-test-ide.local/test-service/internal/coveragerun"
	"unit-test-ide.local/test-service/internal/testrun"
)

type linuxCollectorBinary string

func (binary linuxCollectorBinary) Path() string { return string(binary) }
func (binary linuxCollectorBinary) Verify() error {
	info, err := os.Stat(binary.Path())
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return ErrInvalidProfiles
	}
	return nil
}

func TestLinuxCollectorUsesPinnedToolsAndBothBinaries(t *testing.T) {
	toolset, err := PinToolset(linuxLLVMFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	defer toolset.Close()
	root := newProfileRoot(t)
	expectation := profileExpectation(1, 1)
	profile := writeExpandedProfile(t, root, expectation, "42", "module", []byte("raw profile"))
	manifest, err := SealProfiles(root, []testrun.ProfileExpectation{expectation},
		[]testrun.InvocationOutcome{profileOutcome(1, 1, 0)})
	if err != nil {
		t.Fatal(err)
	}
	defer closeManifest(t, &manifest)
	binRoot := t.TempDir()
	c, cxx := linuxCollectorBinary(filepath.Join(binRoot, "c-tests")), linuxCollectorBinary(filepath.Join(binRoot, "cpp-tests"))
	for _, binary := range []linuxCollectorBinary{c, cxx} {
		if err := os.WriteFile(binary.Path(), []byte("binary"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	merge, export, err := BuildCollectorInvocation(toolset, manifest, []coveragerun.TrustedPath{c, cxx})
	if err != nil {
		t.Fatal(err)
	}
	merged := filepath.Join(root, mergedProfileFileName)
	if merge.Executable != toolset.Profdata().Path() || export.Executable != toolset.Cov().Path() ||
		!reflect.DeepEqual(merge.Args, []string{"merge", "-sparse", profile, "-o", merged}) ||
		!reflect.DeepEqual(export.Args, []string{"export", "-format=text", "-instr-profile=" + merged, c.Path(), "-object", cxx.Path()}) ||
		merge.Dir != root || export.Dir != root {
		t.Fatalf("Linux LLVM collector = merge %#v export %#v", merge, export)
	}
}
