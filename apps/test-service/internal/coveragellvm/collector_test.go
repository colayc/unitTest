//go:build windows

package coveragellvm

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"

	"unit-test-ide.local/test-service/internal/coveragerun"
	"unit-test-ide.local/test-service/internal/testrun"
)

func TestCollectorBuildsFixedSortedMergeAndExportArguments(t *testing.T) {
	t.Setenv("REGISTRY_HOSTILE_TASK5_COLLECTOR", "outside")
	root := newProfileRoot(t)
	expectations := []testrun.ProfileExpectation{
		profileExpectation(2, 1), profileExpectation(1, 1),
	}
	writeExpandedProfile(t, root, expectations[0], "200", "bbbb", []byte("second"))
	writeExpandedProfile(t, root, expectations[1], "100", "aaaa", []byte("first"))
	manifest, err := SealProfiles(root, expectations, []testrun.InvocationOutcome{
		profileOutcome(1, 1, 0), profileOutcome(2, 1, 0),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer closeManifest(t, &manifest)
	toolset, err := PinToolset(llvmToolchainFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	defer toolset.Close()
	binaryRoot := t.TempDir()
	primary := newCollectorBinary(t, binaryRoot, "primary.exe")
	additionalB := newCollectorBinary(t, binaryRoot, "z-additional.dll")
	additionalA := newCollectorBinary(t, binaryRoot, "a-additional.dll")
	merge, export, err := BuildCollectorInvocation(
		toolset,
		manifest,
		[]coveragerun.TrustedPath{primary, additionalB, additionalA},
	)
	if err != nil {
		t.Fatal(err)
	}
	profiles := []string{manifest.Entries[0].Path, manifest.Entries[1].Path}
	slices.Sort(profiles)
	profdata := filepath.Join(root, mergedProfileFileName)
	wantMerge := append([]string{"merge", "-sparse"}, profiles...)
	wantMerge = append(wantMerge, "-o", profdata)
	wantExport := []string{
		"export", "-format=text", "-instr-profile=" + profdata,
		primary.Path(), "-object", additionalA.Path(),
		"-object", additionalB.Path(),
	}
	if merge.Executable != toolset.Profdata().Path() ||
		export.Executable != toolset.Cov().Path() ||
		merge.Dir != root || export.Dir != root ||
		!reflect.DeepEqual(merge.Args, wantMerge) ||
		!reflect.DeepEqual(export.Args, wantExport) ||
		len(merge.Env) != 0 || len(export.Env) != 0 ||
		!containsFold(merge.EnvUnset, "LLVM_PROFILE_FILE") ||
		!containsFold(merge.EnvUnset, "REGISTRY_HOSTILE_TASK5_COLLECTOR") ||
		!reflect.DeepEqual(merge.EnvUnset, export.EnvUnset) {
		t.Fatalf("collector specs = merge %#v export %#v", merge, export)
	}
}

func TestCollectorRejectsReplacedProfileOrBinaryCapability(t *testing.T) {
	t.Run("profile replacement", func(t *testing.T) {
		root := newProfileRoot(t)
		expectation := profileExpectation(1, 1)
		path := writeExpandedProfile(t, root, expectation, "100", "aaaa", []byte("profile"))
		manifest, err := SealProfiles(root, []testrun.ProfileExpectation{expectation}, []testrun.InvocationOutcome{profileOutcome(1, 1, 0)})
		if err != nil {
			t.Fatal(err)
		}
		defer closeManifest(t, &manifest)
		toolset, err := PinToolset(llvmToolchainFixture(t))
		if err != nil {
			t.Fatal(err)
		}
		defer toolset.Close()
		binary := newCollectorBinary(t, t.TempDir(), "primary.exe")
		if err := os.Rename(path, path+".old"); err != nil {
			t.Logf("retained profile blocked replacement at rename: %v", err)
			return
		}
		writeFile(t, path, []byte("replacement"))
		if _, _, err := BuildCollectorInvocation(toolset, manifest, []coveragerun.TrustedPath{binary}); !errors.Is(err, ErrInvalidProfiles) {
			t.Fatalf("profile replacement error = %v", err)
		}
	})

	t.Run("binary capability is revalidated", func(t *testing.T) {
		root := newProfileRoot(t)
		expectation := profileExpectation(1, 1)
		writeExpandedProfile(t, root, expectation, "100", "aaaa", []byte("profile"))
		manifest, err := SealProfiles(root, []testrun.ProfileExpectation{expectation}, []testrun.InvocationOutcome{profileOutcome(1, 1, 0)})
		if err != nil {
			t.Fatal(err)
		}
		defer closeManifest(t, &manifest)
		toolset, err := PinToolset(llvmToolchainFixture(t))
		if err != nil {
			t.Fatal(err)
		}
		defer toolset.Close()
		binary := newCollectorBinary(t, t.TempDir(), "primary.exe")
		binary.failAfter = 1
		if _, _, err := BuildCollectorInvocation(toolset, manifest, []coveragerun.TrustedPath{binary}); !errors.Is(err, ErrInvalidProfiles) {
			t.Fatalf("binary revalidation error = %v", err)
		}
		if binary.verifyCalls != 2 {
			t.Fatalf("binary Verify calls = %d, want 2", binary.verifyCalls)
		}
	})
}

type collectorTrustedPath struct {
	path        string
	err         error
	failAfter   int
	verifyCalls int
}

func (path *collectorTrustedPath) Path() string { return path.path }
func (path *collectorTrustedPath) Verify() error {
	path.verifyCalls++
	if path.err != nil {
		return path.err
	}
	if path.failAfter > 0 && path.verifyCalls > path.failAfter {
		return errors.New("binary capability replaced")
	}
	return nil
}

func newCollectorBinary(
	t *testing.T,
	root,
	name string,
) *collectorTrustedPath {
	t.Helper()
	path := filepath.Join(root, name)
	if err := os.WriteFile(path, []byte(name), 0o700); err != nil {
		t.Fatal(err)
	}
	return &collectorTrustedPath{path: path}
}
