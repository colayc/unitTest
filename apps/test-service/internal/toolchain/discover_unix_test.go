//go:build !windows

package toolchain

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"unit-test-ide.local/test-service/internal/workspace"
)

func TestUnixDiscoveryCombinesPATHAndManualCandidatesWithManualPriority(t *testing.T) {
	t.Parallel()

	fixture := newGNUFixture(t)
	aliasDir := filepath.Join(fixture.root, "manual aliases")
	if err := os.Mkdir(aliasDir, 0o755); err != nil {
		t.Fatalf("Mkdir(alias): %v", err)
	}
	gccAlias := filepath.Join(aliasDir, "configured-gcc")
	gxxAlias := filepath.Join(aliasDir, "configured-g++")
	if err := os.Symlink(fixture.gcc, gccAlias); err != nil {
		t.Fatalf("Symlink(gcc): %v", err)
	}
	if err := os.Symlink(fixture.gxx, gxxAlias); err != nil {
		t.Fatalf("Symlink(g++): %v", err)
	}

	runner := newGNUFakeRunner(t, fixture)
	configs := []workspace.ToolchainConfig{{
		ID:          "manual-gcc",
		Family:      "gcc",
		CCompiler:   gccAlias,
		CPPCompiler: gxxAlias,
	}}
	adapters, err := newUnixAdapters(runner, configs, filepath.Dir(fixture.gcc), "x64")
	if err != nil {
		t.Fatalf("newUnixAdapters() error = %v", err)
	}
	registry, err := NewRegistry(adapters...)
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}

	instances, issues := registry.Discover(context.Background())
	if len(issues) != 0 {
		t.Fatalf("Discover() issues = %+v, want none", issues)
	}
	if got := instanceIDs(instances); len(got) != 2 || got[1] != "manual-gcc" {
		t.Fatalf("Discover() ids = %v, want auto Clang and manual GCC", got)
	}
	if instances[1].CCompiler != fixture.gcc || instances[1].CXXCompiler != fixture.gxx {
		t.Fatalf("manual aliases were not canonicalized: %+v", instances[1])
	}
	if got := instances[0].Generators; !reflect.DeepEqual(got, []string{"Ninja"}) {
		t.Fatalf("Clang Generators = %v, want Ninja preference", got)
	}
}

func TestUnixCandidateDiscoveryIgnoresUnsafePATHEntriesAndNonExecutables(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	valid := filepath.Join(root, "valid")
	invalid := filepath.Join(root, "invalid")
	for _, directory := range []string{valid, invalid} {
		if err := os.Mkdir(directory, 0o755); err != nil {
			t.Fatalf("Mkdir(%q): %v", directory, err)
		}
	}
	writeExecutable(t, filepath.Join(valid, "gcc"))
	writeExecutable(t, filepath.Join(valid, "g++"))
	writeExecutable(t, filepath.Join(valid, "make"))
	if err := os.Mkdir(filepath.Join(invalid, "gcc"), 0o755); err != nil {
		t.Fatalf("Mkdir(invalid gcc): %v", err)
	}
	writeExecutable(t, filepath.Join(invalid, "g++"))
	if err := os.WriteFile(filepath.Join(invalid, "clang"), []byte("not executable"), 0o644); err != nil {
		t.Fatalf("WriteFile(non-executable clang): %v", err)
	}
	writeExecutable(t, filepath.Join(invalid, "clang++"))

	pathValue := strings.Join([]string{"", "relative", invalid, valid, valid}, string(os.PathListSeparator))
	candidates, err := discoverUnixCandidates(pathValue, nil)
	if err != nil {
		t.Fatalf("discoverUnixCandidates() error = %v", err)
	}
	if len(candidates[FamilyGCC]) != 1 {
		t.Fatalf("GCC candidates = %+v, want one valid deduplicated pair", candidates[FamilyGCC])
	}
	if len(candidates[FamilyClang]) != 0 {
		t.Fatalf("Clang candidates = %+v, want unsafe pair rejected", candidates[FamilyClang])
	}
	candidate := candidates[FamilyGCC][0]
	if candidate.Make != filepath.Join(valid, "make") || candidate.Ninja != "" {
		t.Fatalf("GCC build tools = %+v, want verified Make candidate", candidate)
	}
}

func TestUnixCandidateDiscoveryBoundsInputsAndAcceptsOnlyLinuxManualFamilies(t *testing.T) {
	t.Parallel()

	tooLong := strings.Repeat("x", maxUnixPATHBytes+1)
	if _, err := discoverUnixCandidates(tooLong, nil); !errors.Is(err, ErrInvalidToolchain) {
		t.Fatalf("discoverUnixCandidates(long PATH) error = %v, want ErrInvalidToolchain", err)
	}
	segments := make([]string, maxUnixPATHSegments+1)
	for index := range segments {
		segments[index] = filepath.Join(string(filepath.Separator), "tmp", "segment", string(rune('a'+index%26)))
	}
	if _, err := discoverUnixCandidates(strings.Join(segments, string(os.PathListSeparator)), nil); !errors.Is(err, ErrInvalidToolchain) {
		t.Fatalf("discoverUnixCandidates(excess PATH entries) error = %v, want ErrInvalidToolchain", err)
	}
	tooManyManual := make([]workspace.ToolchainConfig, maxUnixManualToolchains+1)
	if _, err := discoverUnixCandidates("", tooManyManual); !errors.Is(err, ErrInvalidToolchain) {
		t.Fatalf("discoverUnixCandidates(excess manual) error = %v, want ErrInvalidToolchain", err)
	}

	fixture := newGNUFixture(t)
	configs := []workspace.ToolchainConfig{
		{ID: "gcc-manual", Family: "gcc", CCompiler: fixture.gcc, CPPCompiler: fixture.gxx},
		{ID: "clang-manual", Family: "clang", CCompiler: fixture.clang, CPPCompiler: fixture.clangxx},
		{ID: "windows-msvc", Family: "msvc"},
		{ID: "windows-clang-cl", Family: "clang-cl", CCompiler: fixture.clang, CPPCompiler: fixture.clangxx},
	}
	candidates, err := discoverUnixCandidates(filepath.Dir(fixture.ninja), configs)
	if err != nil {
		t.Fatalf("discoverUnixCandidates(manual) error = %v", err)
	}
	if got := manualCandidateIDs(candidates[FamilyGCC]); !reflect.DeepEqual(got, []string{"gcc-manual"}) {
		t.Fatalf("GCC manual ids = %v", got)
	}
	if got := manualCandidateIDs(candidates[FamilyClang]); !reflect.DeepEqual(got, []string{"clang-manual"}) {
		t.Fatalf("Clang manual ids = %v", got)
	}
}

func TestUnixAdaptersHonorCancellation(t *testing.T) {
	t.Parallel()

	fixture := newGNUFixture(t)
	runner := newGNUFakeRunner(t, fixture)
	adapters, err := newUnixAdapters(runner, nil, filepath.Dir(fixture.gcc), "x64")
	if err != nil {
		t.Fatalf("newUnixAdapters() error = %v", err)
	}
	registry, err := NewRegistry(adapters...)
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	instances, issues := registry.Discover(ctx)
	if instances != nil || issues != nil {
		t.Fatalf("Discover(cancelled) = (%+v, %+v), want (nil, nil)", instances, issues)
	}
}

func TestUnixAutomaticIDIncludesSDKDirectoryIdentity(t *testing.T) {
	fixture := newGNUFixture(t)
	runner := newGNUFakeRunner(t, fixture)
	adapter, err := newGNUAdapter(runner, FamilyClang, nil, "x64")
	if err != nil {
		t.Fatalf("newGNUAdapter() error = %v", err)
	}
	candidate := Candidate{
		Family:      FamilyClang,
		CCompiler:   fixture.clang,
		CXXCompiler: fixture.clangxx,
		Ninja:       fixture.ninja,
	}
	first, err := adapter.Probe(context.Background(), candidate)
	if err != nil {
		t.Fatalf("first Probe() error = %v", err)
	}
	oldResource := fixture.resource + "-old"
	if err := os.Rename(fixture.resource, oldResource); err != nil {
		t.Fatalf("Rename(resource): %v", err)
	}
	if err := os.Mkdir(fixture.resource, 0o755); err != nil {
		t.Fatalf("Mkdir(resource replacement): %v", err)
	}
	second, err := adapter.Probe(context.Background(), candidate)
	if err != nil {
		t.Fatalf("second Probe() error = %v", err)
	}
	if first.ID == second.ID {
		t.Fatalf("SDK directory replacement did not change automatic ID %q", first.ID)
	}
}

func TestUnixProbeRejectsSDKDirectoryReplacementBeforeReturn(t *testing.T) {
	fixture := newGNUFixture(t)
	runner := newGNUFakeRunner(t, fixture)
	runner.afterCall = func(call probeCall) {
		if call != (probeCall{fixture.ninja, "--version"}) {
			return
		}
		if err := os.Rename(fixture.sysroot, fixture.sysroot+"-old"); err != nil {
			t.Fatalf("Rename(sysroot): %v", err)
		}
		if err := os.Mkdir(fixture.sysroot, 0o755); err != nil {
			t.Fatalf("Mkdir(sysroot replacement): %v", err)
		}
	}
	adapter, err := newGNUAdapter(runner, FamilyGCC, nil, "x64")
	if err != nil {
		t.Fatalf("newGNUAdapter() error = %v", err)
	}

	_, err = adapter.Probe(context.Background(), Candidate{
		Family:      FamilyGCC,
		CCompiler:   fixture.gcc,
		CXXCompiler: fixture.gxx,
		Ninja:       fixture.ninja,
	})
	if !errors.Is(err, ErrInvalidToolchain) || !strings.Contains(err.Error(), "SDK") {
		t.Fatalf("Probe() error = %v, want SDK directory replacement rejection", err)
	}
}

func writeExecutable(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(filepath.Base(path)), 0o755); err != nil {
		t.Fatalf("WriteFile(%q): %v", path, err)
	}
}

func manualCandidateIDs(candidates []Candidate) []string {
	var ids []string
	for _, candidate := range candidates {
		if candidate.Manual {
			ids = append(ids, candidate.ID)
		}
	}
	return ids
}
