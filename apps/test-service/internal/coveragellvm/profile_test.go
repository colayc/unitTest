package coveragellvm

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"unit-test-ide.local/test-service/internal/coveragedomain"
	"unit-test-ide.local/test-service/internal/task"
	"unit-test-ide.local/test-service/internal/testrun"
)

func TestProfileAllocatorSanitizesEnvironmentAndAppendsOneOwnedPattern(
	t *testing.T,
) {
	t.Setenv("REGISTRY_HOSTILE_TASK5", "outside")
	root := newProfileRoot(t)
	allocator, err := NewProfileAllocator(root)
	if err != nil {
		t.Fatal(err)
	}
	defer closeProfileAllocator(t, allocator)
	expectation := profileExpectation(1, 1)
	spec, err := allocator.Decorate(expectation, task.ProcessSpec{
		Executable: "test.exe",
		Args:       []string{"--run"},
		Env: []string{
			"PATH=kept",
			"llvm_profile_file=user.profraw",
			"LLVM_PROFILE_MERGE_FILE=user.profdata",
			"GCOV_PREFIX=outside",
			"PythonPath=outside",
			"https_proxy=http://outside",
			"USERPROFILE=C:\\outside",
			"REGISTRY_REDIRECT=outside",
		},
		EnvUnset: []string{"EXISTING_UNSET", "llvm_profile_file"},
		Dir:      root,
	})
	if err != nil {
		t.Fatal(err)
	}
	wantProfile := "LLVM_PROFILE_FILE=" +
		filepath.Join(root, expectation.FileName)
	if countEnvironmentKey(spec.Env, "LLVM_PROFILE_FILE") != 1 ||
		!slices.Contains(spec.Env, wantProfile) ||
		!slices.Contains(spec.Env, "PATH=kept") {
		t.Fatalf("decorated environment = %#v", spec.Env)
	}
	for _, key := range []string{
		"LLVM_PROFILE_MERGE_FILE", "GCOV_PREFIX", "PYTHONPATH",
		"HTTPS_PROXY", "USERPROFILE", "REGISTRY_REDIRECT",
		"REGISTRY_HOSTILE_TASK5",
	} {
		if countEnvironmentKey(spec.Env, key) != 0 ||
			!containsFold(spec.EnvUnset, key) {
			t.Fatalf("%s survived sanitization: env=%#v unset=%#v", key, spec.Env, spec.EnvUnset)
		}
	}
	if containsFold(spec.EnvUnset, "LLVM_PROFILE_FILE") {
		t.Fatalf("Service override conflicts with EnvUnset: %#v", spec.EnvUnset)
	}
	if spec.Executable != "test.exe" || spec.Dir != root ||
		!reflect.DeepEqual(spec.Args, []string{"--run"}) {
		t.Fatalf("allocator changed process target = %#v", spec)
	}
}

func TestSealProfilesReturnsClosedSameSnapshotManifest(t *testing.T) {
	root := newProfileRoot(t)
	expectations := []testrun.ProfileExpectation{
		profileExpectation(2, 1),
		profileExpectation(1, 1),
	}
	first := []byte("first-profile")
	second := []byte("second-profile")
	writeExpandedProfile(t, root, expectations[0], "200", "bbbb", first)
	writeExpandedProfile(t, root, expectations[1], "100", "aaaa", second)
	manifest, err := SealProfiles(root, expectations, []testrun.InvocationOutcome{
		profileOutcome(1, 1, 0),
		profileOutcome(2, 1, 0),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer closeManifest(t, &manifest)
	if len(manifest.Entries) != 2 || len(manifest.PartialReasons) != 0 {
		t.Fatalf("manifest = %#v", manifest)
	}
	wantNames := []string{
		expandedProfileName(expectations[1], "100", "aaaa"),
		expandedProfileName(expectations[0], "200", "bbbb"),
	}
	for index, entry := range manifest.Entries {
		contents := second
		if index == 1 {
			contents = first
		}
		sum := sha256.Sum256(contents)
		if filepath.Base(entry.Path) != wantNames[index] ||
			entry.Size != int64(len(contents)) ||
			entry.SHA256 != hex.EncodeToString(sum[:]) {
			t.Fatalf("entry[%d] = %#v", index, entry)
		}
	}
	if err := manifest.Verify(); err != nil {
		t.Fatalf("Verify() = %v", err)
	}
}

func TestSealProfilesFailsClosedForUnexpectedAliasedOrInvalidEvidence(
	t *testing.T,
) {
	tests := []struct {
		name string
		edit func(*testing.T, string, testrun.ProfileExpectation)
	}{
		{
			name: "unknown",
			edit: func(t *testing.T, root string, _ testrun.ProfileExpectation) {
				writeFile(t, filepath.Join(root, "unknown.profraw"), []byte("unknown"))
			},
		},
		{
			name: "duplicate",
			edit: func(t *testing.T, root string, expectation testrun.ProfileExpectation) {
				writeExpandedProfile(t, root, expectation, "200", "bbbb", []byte("duplicate"))
			},
		},
		{
			name: "hardlink",
			edit: func(t *testing.T, root string, expectation testrun.ProfileExpectation) {
				path := filepath.Join(root, expandedProfileName(expectation, "100", "aaaa"))
				outside := filepath.Join(t.TempDir(), "alias.profraw")
				if err := os.Link(path, outside); err != nil {
					t.Skipf("hardlinks unavailable: %v", err)
				}
			},
		},
		{
			name: "symlink",
			edit: func(t *testing.T, root string, expectation testrun.ProfileExpectation) {
				path := filepath.Join(root, expandedProfileName(expectation, "100", "aaaa"))
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				outside := filepath.Join(t.TempDir(), "outside.profraw")
				writeFile(t, outside, []byte("outside"))
				if err := os.Symlink(outside, path); err != nil {
					t.Skipf("symlinks unavailable: %v", err)
				}
			},
		},
		{
			name: "oversized",
			edit: func(t *testing.T, root string, expectation testrun.ProfileExpectation) {
				path := filepath.Join(root, expandedProfileName(expectation, "100", "aaaa"))
				if err := os.Truncate(path, maxProfileBytes+1); err != nil {
					t.Fatal(err)
				}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := newProfileRoot(t)
			expectation := profileExpectation(1, 1)
			writeExpandedProfile(t, root, expectation, "100", "aaaa", []byte("profile"))
			test.edit(t, root, expectation)
			manifest, err := SealProfiles(root, []testrun.ProfileExpectation{expectation}, []testrun.InvocationOutcome{profileOutcome(1, 1, 0)})
			if err == nil {
				_ = manifest.Close()
				t.Fatal("SealProfiles accepted invalid closed-set evidence")
			}
		})
	}

	root := newProfileRoot(t)
	expectation := profileExpectation(1, 1)
	if _, err := SealProfiles(root, []testrun.ProfileExpectation{
		expectation, expectation,
	}, []testrun.InvocationOutcome{profileOutcome(1, 1, 0)}); err == nil {
		t.Fatal("SealProfiles accepted duplicate expectation")
	}
	expectation.FileName = filepath.Join("..", "outside.profraw")
	if _, err := SealProfiles(root, []testrun.ProfileExpectation{expectation}, []testrun.InvocationOutcome{profileOutcome(1, 1, 0)}); err == nil {
		t.Fatal("SealProfiles accepted outside expectation")
	}
}

func TestSealedProfileManifestRejectsReplacement(t *testing.T) {
	root := newProfileRoot(t)
	expectation := profileExpectation(1, 1)
	path := writeExpandedProfile(t, root, expectation, "100", "aaaa", []byte("original"))
	manifest, err := SealProfiles(root, []testrun.ProfileExpectation{expectation}, []testrun.InvocationOutcome{profileOutcome(1, 1, 0)})
	if err != nil {
		t.Fatal(err)
	}
	defer closeManifest(t, &manifest)
	if err := os.Rename(path, path+".replaced"); err != nil {
		t.Logf("retained profile blocked replacement at rename: %v", err)
		if err := manifest.Close(); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(path, path+".released"); err != nil {
			t.Fatalf("profile handle remained retained after Close: %v", err)
		}
		if err := manifest.Close(); err != nil {
			t.Fatalf("second Close() = %v", err)
		}
		return
	}
	writeFile(t, path, []byte("replacement"))
	if err := manifest.Verify(); err == nil {
		t.Fatal("Verify accepted replaced profile")
	}
}

func TestSealProfilesDistinguishesInfrastructureFailureFromPartialReasons(
	t *testing.T,
) {
	expectations := []testrun.ProfileExpectation{
		profileExpectation(1, 1),
		profileExpectation(2, 1),
		profileExpectation(3, 1),
	}
	t.Run("normal exit is infrastructure failure", func(t *testing.T) {
		root := newProfileRoot(t)
		if _, err := SealProfiles(root, expectations[:1], []testrun.InvocationOutcome{profileOutcome(1, 1, 0)}); !errors.Is(err, ErrInvalidProfiles) {
			t.Fatalf("SealProfiles error = %v", err)
		}
	})
	t.Run("failed crashed and timed out are exact partial reasons", func(t *testing.T) {
		root := newProfileRoot(t)
		manifest, err := SealProfiles(root, expectations, []testrun.InvocationOutcome{
			profileOutcome(1, 1, 7),
			{InvocationID: expectations[1].InvocationID, Iteration: 1, Crashed: true},
			{InvocationID: expectations[2].InvocationID, Iteration: 1, TimedOut: true},
		})
		if err != nil {
			t.Fatal(err)
		}
		defer closeManifest(t, &manifest)
		want := []coveragedomain.CompletenessReason{
			coveragedomain.CompletenessReasonProfileMissingForFailedInvocation,
			coveragedomain.CompletenessReasonTestCrashed,
			coveragedomain.CompletenessReasonTestTimedOut,
		}
		if !reflect.DeepEqual(manifest.PartialReasons, want) || len(manifest.Entries) != 0 {
			t.Fatalf("partial manifest = %#v", manifest)
		}
		manifest.PartialReasons[0] = coveragedomain.CompletenessReasonTestTimedOut
		if err := manifest.Verify(); err == nil {
			t.Fatal("Verify accepted mutated partial reasons")
		}
	})
}

func newProfileRoot(t *testing.T) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "profiles")
	makeOwnerOnlyInstrumentationRoot(t, root)
	return root
}

func profileExpectation(invocation, iteration int64) testrun.ProfileExpectation {
	return testrun.ProfileExpectation{
		InvocationID: "test-" + leftPad6(invocation),
		Iteration:    iteration,
		FileName: "p-" + leftPad6(invocation) + "-i-" +
			leftPad6(iteration) + "-%p-%m.profraw",
	}
}

func profileOutcome(invocation, iteration int64, exitCode int) testrun.InvocationOutcome {
	return testrun.InvocationOutcome{
		InvocationID: profileExpectation(invocation, iteration).InvocationID,
		Iteration:    iteration,
		ExitCode:     exitCode,
	}
}

func leftPad6(value int64) string {
	text := "000000" + string([]byte{
		byte('0' + value/100000%10), byte('0' + value/10000%10),
		byte('0' + value/1000%10), byte('0' + value/100%10),
		byte('0' + value/10%10), byte('0' + value%10),
	})
	return text[len(text)-6:]
}

func expandedProfileName(expectation testrun.ProfileExpectation, process, module string) string {
	return strings.ReplaceAll(strings.ReplaceAll(expectation.FileName, "%p", process), "%m", module)
}

func writeExpandedProfile(t *testing.T, root string, expectation testrun.ProfileExpectation, process, module string, contents []byte) string {
	t.Helper()
	path := filepath.Join(root, expandedProfileName(expectation, process, module))
	writeFile(t, path, contents)
	return path
}

func writeFile(t *testing.T, path string, contents []byte) {
	t.Helper()
	if err := os.WriteFile(path, contents, 0o600); err != nil {
		t.Fatal(err)
	}
}

func countEnvironmentKey(environment []string, key string) int {
	count := 0
	for _, entry := range environment {
		name, _, found := strings.Cut(entry, "=")
		if found && strings.EqualFold(name, key) {
			count++
		}
	}
	return count
}

func containsFold(values []string, want string) bool {
	for _, value := range values {
		if strings.EqualFold(value, want) {
			return true
		}
	}
	return false
}

func closeProfileAllocator(t *testing.T, allocator testrun.ProfileAllocator) {
	t.Helper()
	if closer, ok := allocator.(interface{ Close() error }); ok {
		if err := closer.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func closeManifest(t *testing.T, manifest *Manifest) {
	t.Helper()
	if err := manifest.Close(); err != nil {
		t.Fatal(err)
	}
}
