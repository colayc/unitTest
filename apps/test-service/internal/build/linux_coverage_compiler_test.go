package build

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"unit-test-ide.local/test-service/internal/cmake"
	"unit-test-ide.local/test-service/internal/task"
	"unit-test-ide.local/test-service/internal/toolchain"
)

func TestLinuxCoverageCompilerCacheRejectsAlternateSameBasenamePair(t *testing.T) {
	root := t.TempDir()
	pinned := filepath.Join(root, "pinned")
	alternate := filepath.Join(root, "alternate")
	for _, dir := range []string{pinned, alternate} {
		if err := os.Mkdir(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{"clang", "clang++"} {
			if err := os.WriteFile(filepath.Join(dir, name), []byte(dir), 0o700); err != nil {
				t.Fatal(err)
			}
		}
	}
	cache := filepath.Join(root, "CMakeCache.txt")
	instance := toolchain.Instance{Family: toolchain.FamilyClang, CCompiler: filepath.Join(pinned, "clang"), CXXCompiler: filepath.Join(pinned, "clang++")}
	write := func(c, cxx string) {
		t.Helper()
		contents := "CMAKE_C_COMPILER:FILEPATH=" + filepath.ToSlash(c) + "\nCMAKE_CXX_COMPILER:FILEPATH=" + filepath.ToSlash(cxx) + "\n"
		if err := os.WriteFile(cache, []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(instance.CCompiler, instance.CXXCompiler)
	if err := verifyLinuxCoverageCompilerCache(cache, instance); err != nil {
		t.Fatalf("pinned pair rejected: %v", err)
	}
	write(filepath.Join(alternate, "clang"), filepath.Join(alternate, "clang++"))
	if err := verifyLinuxCoverageCompilerCache(cache, instance); err == nil {
		t.Fatal("alternate same-basename pair accepted")
	}
	write(instance.CCompiler, filepath.Join(alternate, "clang++"))
	if err := verifyLinuxCoverageCompilerCache(cache, instance); err == nil {
		t.Fatal("alternate C++ compiler accepted")
	}
	write(instance.CCompiler, "")
	if err := verifyLinuxCoverageCompilerCache(cache, instance); err == nil {
		t.Fatal("missing C++ compiler accepted")
	}
	write(instance.CCompiler, instance.CXXCompiler)
	if err := os.WriteFile(cache, []byte(strings.Repeat("x", 2<<20)), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := verifyLinuxCoverageCompilerCache(cache, instance); err == nil {
		t.Fatal("oversized cache accepted")
	}
}

func TestLinuxCoveragePreparedPlanChecksEffectiveCompilersBeforeBuild(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux compiler checkpoint")
	}
	root := t.TempDir()
	pinned := filepath.Join(root, "pinned")
	alternate := filepath.Join(root, "alternate")
	for _, dir := range []string{pinned, alternate} {
		if err := os.Mkdir(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{"clang", "clang++"} {
			if err := os.WriteFile(filepath.Join(dir, name), []byte(dir), 0o700); err != nil {
				t.Fatal(err)
			}
		}
	}
	cache := filepath.Join(root, "CMakeCache.txt")
	write := func(dir string) {
		t.Helper()
		contents := "CMAKE_C_COMPILER:FILEPATH=" + filepath.Join(dir, "clang") + "\nCMAKE_CXX_COMPILER:FILEPATH=" + filepath.Join(dir, "clang++") + "\n"
		if err := os.WriteFile(cache, []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	instance := toolchain.Instance{Family: toolchain.FamilyClang, CCompiler: filepath.Join(pinned, "clang"), CXXCompiler: filepath.Join(pinned, "clang++")}
	replyPaths := map[string]string{"C": instance.CCompiler, "CXX": instance.CXXCompiler}
	prepared := &PreparedPlan{prepared: &preparedBuild{coverage: &CoverageOptions{}, profile: cmake.BuildProfile{BinaryDir: root}, toolchain: instance}, coordinator: &Coordinator{dependencies: coordinatorDependencies{readReply: func(string, []string, ...cmake.BuildProfile) (cmake.FileAPIReply, error) {
		return cmake.FileAPIReply{CompilerPaths: replyPaths}, nil
	}}}}
	write(alternate)
	if _, err := prepared.RefreshTargets(context.Background()); !errors.Is(err, ErrConfigureRequired) {
		t.Fatalf("build refresh with alternate compiler = %v", err)
	}
	if err := prepared.PersistConfiguration(context.Background()); !errors.Is(err, ErrConfigureRequired) {
		t.Fatalf("configure checkpoint with alternate compiler = %v", err)
	}
	write(pinned)
	replyPaths = map[string]string{"C": filepath.Join(alternate, "clang"), "CXX": filepath.Join(alternate, "clang++")}
	if _, err := prepared.RefreshTargets(context.Background()); !errors.Is(err, ErrConfigureRequired) {
		t.Fatalf("File API alternate same-basename pair accepted: %v", err)
	}
	if err := prepared.PersistConfiguration(context.Background()); !errors.Is(err, ErrConfigureRequired) {
		t.Fatalf("configure checkpoint accepted File API alternate pair: %v", err)
	}
	replyPaths = map[string]string{"C": instance.CCompiler, "CXX": instance.CXXCompiler}
	if _, err := prepared.RefreshTargets(context.Background()); err != nil {
		t.Fatalf("pinned compiler refresh = %v", err)
	}
	if err := prepared.PersistConfiguration(context.Background()); !errors.Is(err, task.ErrInvalidArgument) {
		t.Fatalf("pinned compiler checkpoint advanced past identity check = %v", err)
	}
}

func TestPlannerPinsLinuxClangPresetCoverageCompilers(t *testing.T) {
	fixture := newPlannerFixture(t)
	fixture.profile.Origin = "preset"
	fixture.profile.ConfigurePreset = "debug"
	fixture.toolchain.Family = toolchain.FamilyClang
	writePlannerPreset(t, fixture.sourceDir, "debug")
	include := filepath.Join(fixture.dataRoot, "coverage.cmake")
	if err := os.WriteFile(include, []byte("# fixture\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	plan, err := Plan(PlanInput{Installation: fixture.installation, WorkspaceRoot: fixture.root, Project: fixture.project, Profile: fixture.profile, Toolchain: fixture.toolchain, Jobs: 1, Configure: true, Coverage: &CoverageOptions{BinaryDir: filepath.Join(fixture.dataRoot, "coverage-build"), TopLevelInclude: cmake.FingerprintFile{Path: include, Identity: strings.Repeat("4", 64), SHA256: strings.Repeat("5", 64)}, InstrumentationFingerprint: strings.Repeat("4", 64)}})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"-DCMAKE_C_COMPILER=" + filepath.ToSlash(mustPlannerLaunchPath(t, fixture.toolchain.CCompiler)), "-DCMAKE_CXX_COMPILER=" + filepath.ToSlash(mustPlannerLaunchPath(t, fixture.toolchain.CXXCompiler))} {
		if countArgument(plan.Steps[0].Process.Args, want) != 1 {
			t.Fatalf("preset configure args = %#v, want %q", plan.Steps[0].Process.Args, want)
		}
	}
}
