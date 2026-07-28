package cmake

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"unit-test-ide.local/test-service/internal/probe"
)

func TestDiscoverPresetsRejectsTopLevelFileLinkBeforeProbe(t *testing.T) {
	root, project, sourceDir := newPresetWorkspace(t)
	realPath := filepath.Join(sourceDir, "real-presets.json")
	writeSimplePreset(t, realPath)
	linkPath := filepath.Join(sourceDir, "CMakePresets.json")
	if err := os.Symlink(realPath, linkPath); err != nil {
		if runtime.GOOS == "windows" {
			t.Skipf("file symlink is unavailable: %v", err)
		}
		t.Fatalf("Symlink() error = %v", err)
	}
	runner := &presetTestRunner{}

	_, err := DiscoverPresets(
		context.Background(),
		runner,
		presetTestInstallation(root),
		root,
		project,
	)
	if !errors.Is(err, ErrPresetBoundary) {
		t.Fatalf("error = %v, want ErrPresetBoundary", err)
	}
	if len(runner.specs) != 0 {
		t.Fatalf("probe calls = %d, want 0", len(runner.specs))
	}
}

func TestDiscoverPresetsRejectsInsideRootIntermediateDirectoryLinkBeforeProbe(t *testing.T) {
	root, project, sourceDir := newPresetWorkspace(t)
	sharedDir := filepath.Join(root.NativePath, "shared")
	writePresetJSON(t, filepath.Join(sharedDir, "included.json"), map[string]any{"version": 6})
	linkDir := filepath.Join(sourceDir, "linked")
	createDirectoryLink(t, linkDir, sharedDir)
	writePresetJSON(t, filepath.Join(sourceDir, "CMakePresets.json"), map[string]any{
		"version": 6,
		"include": []string{"linked/included.json"},
	})
	runner := &presetTestRunner{}

	_, err := DiscoverPresets(
		context.Background(),
		runner,
		presetTestInstallation(root),
		root,
		project,
	)
	if !errors.Is(err, ErrPresetBoundary) {
		t.Fatalf("error = %v, want ErrPresetBoundary", err)
	}
	if len(runner.specs) != 0 {
		t.Fatalf("probe calls = %d, want 0", len(runner.specs))
	}
}

func TestDiscoverPresetsFailsClosedWhenTopLevelFileIsReplacedAfterEachListing(t *testing.T) {
	for _, mutateAfter := range []int{1, 2} {
		t.Run(probeStageName(mutateAfter), func(t *testing.T) {
			root, project, sourceDir := newPresetWorkspace(t)
			presetPath := filepath.Join(sourceDir, "CMakePresets.json")
			writeSimplePreset(t, presetPath)
			replacement := filepath.Join(sourceDir, "replacement.json")
			writeSimplePreset(t, replacement)
			backup := filepath.Join(sourceDir, "original.json")
			runner := newPathMutationRunner(mutateAfter, func() error {
				if err := os.Rename(presetPath, backup); err != nil {
					return err
				}
				if err := os.Rename(replacement, presetPath); err != nil {
					_ = os.Rename(backup, presetPath)
					return err
				}
				return nil
			})

			_, err := DiscoverPresets(
				context.Background(),
				runner,
				presetTestInstallation(root),
				root,
				project,
			)
			assertPresetMutationFailsClosed(t, runner, err, mutateAfter)
		})
	}
}

func TestDiscoverPresetsFailsClosedWhenTopLevelBecomesOutsideLink(t *testing.T) {
	root, project, sourceDir := newPresetWorkspace(t)
	presetPath := filepath.Join(sourceDir, "CMakePresets.json")
	writeSimplePreset(t, presetPath)
	outsidePath := filepath.Join(t.TempDir(), "outside.json")
	writeSimplePreset(t, outsidePath)
	backup := filepath.Join(sourceDir, "original.json")
	runner := newPathMutationRunner(1, func() error {
		if err := os.Rename(presetPath, backup); err != nil {
			return err
		}
		if err := os.Symlink(outsidePath, presetPath); err != nil {
			_ = os.Rename(backup, presetPath)
			return err
		}
		return nil
	})

	_, err := DiscoverPresets(
		context.Background(),
		runner,
		presetTestInstallation(root),
		root,
		project,
	)
	assertPresetMutationFailsClosed(t, runner, err, 1)
}

func TestDiscoverPresetsFailsClosedWhenParentDirectoryIdentityChanges(t *testing.T) {
	root, project, sourceDir := newPresetWorkspace(t)
	presetPath := filepath.Join(sourceDir, "CMakePresets.json")
	writeSimplePreset(t, presetPath)
	backupDir := sourceDir + "-original"
	runner := newPathMutationRunner(1, func() error {
		if err := os.Rename(sourceDir, backupDir); err != nil {
			return err
		}
		if err := os.Mkdir(sourceDir, 0o755); err != nil {
			_ = os.Rename(backupDir, sourceDir)
			return err
		}
		writeSimplePreset(t, filepath.Join(sourceDir, "CMakePresets.json"))
		return nil
	})

	_, err := DiscoverPresets(
		context.Background(),
		runner,
		presetTestInstallation(root),
		root,
		project,
	)
	assertPresetMutationFailsClosed(t, runner, err, 1)
}

func TestDiscoverPresetsFailsClosedWhenIncludeDirectoryBecomesLink(t *testing.T) {
	root, project, sourceDir := newPresetWorkspace(t)
	includeDir := filepath.Join(sourceDir, "includes")
	writePresetJSON(t, filepath.Join(includeDir, "included.json"), map[string]any{"version": 6})
	writePresetJSON(t, filepath.Join(sourceDir, "CMakePresets.json"), map[string]any{
		"version": 6,
		"include": []string{"includes/included.json"},
		"configurePresets": []map[string]any{{
			"name":      "debug",
			"generator": "Ninja",
			"binaryDir": "out/debug",
		}},
	})
	outsideDir := t.TempDir()
	writePresetJSON(t, filepath.Join(outsideDir, "included.json"), map[string]any{"version": 6})
	backupDir := includeDir + "-original"
	runner := newPathMutationRunner(1, func() error {
		if err := os.Rename(includeDir, backupDir); err != nil {
			return err
		}
		if err := os.Symlink(outsideDir, includeDir); err != nil {
			_ = os.Rename(backupDir, includeDir)
			return err
		}
		return nil
	})

	_, err := DiscoverPresets(
		context.Background(),
		runner,
		presetTestInstallation(root),
		root,
		project,
	)
	assertPresetMutationFailsClosed(t, runner, err, 1)
}

func TestDiscoverPresetsDetectsObservableReplaceAndRestore(t *testing.T) {
	root, project, sourceDir := newPresetWorkspace(t)
	presetPath := filepath.Join(sourceDir, "CMakePresets.json")
	writeSimplePreset(t, presetPath)
	backup := filepath.Join(sourceDir, "original.json")
	replacement := filepath.Join(sourceDir, "replacement.json")
	writeSimplePreset(t, replacement)
	runner := newPathMutationRunner(1, func() error {
		if err := os.Rename(presetPath, backup); err != nil {
			return err
		}
		if err := os.Rename(replacement, presetPath); err != nil {
			_ = os.Rename(backup, presetPath)
			return err
		}
		if err := os.Remove(presetPath); err != nil {
			return err
		}
		return os.Rename(backup, presetPath)
	})

	_, err := DiscoverPresets(
		context.Background(),
		runner,
		presetTestInstallation(root),
		root,
		project,
	)
	assertPresetMutationFailsClosed(t, runner, err, 1)
}

type pathMutationRunner struct {
	presetTestRunner
	mutateAfter int
	mutate      func() error
	mutationErr error
}

func newPathMutationRunner(mutateAfter int, mutate func() error) *pathMutationRunner {
	return &pathMutationRunner{
		presetTestRunner: presetTestRunner{results: []probe.Result{
			{
				ExitCode: 0,
				Stdout:   []byte("Available configure presets:\n  \"debug\"\n"),
			},
			{
				ExitCode: 0,
				Stdout:   []byte("Available build presets:\n"),
			},
		}},
		mutateAfter: mutateAfter,
		mutate:      mutate,
	}
}

func (runner *pathMutationRunner) Run(
	ctx context.Context,
	spec probe.Spec,
) (probe.Result, error) {
	result, err := runner.presetTestRunner.Run(ctx, spec)
	if len(runner.specs) == runner.mutateAfter {
		runner.mutationErr = runner.mutate()
	}
	return result, err
}

func assertPresetMutationFailsClosed(
	t *testing.T,
	runner *pathMutationRunner,
	discoveryErr error,
	mutateAfter int,
) {
	t.Helper()
	if runner.mutationErr != nil {
		if discoveryErr != nil {
			t.Fatalf("mutation was blocked (%v), but discovery failed: %v", runner.mutationErr, discoveryErr)
		}
		return
	}
	if !errors.Is(discoveryErr, ErrInvalidPresets) &&
		!errors.Is(discoveryErr, ErrPresetBoundary) {
		t.Fatalf("mutation succeeded; error = %v, want fail closed", discoveryErr)
	}
	if len(runner.specs) != mutateAfter {
		t.Fatalf("probe calls = %d, want stop after mutation at call %d", len(runner.specs), mutateAfter)
	}
}

func writeSimplePreset(t *testing.T, path string) {
	t.Helper()
	writePresetJSON(t, path, map[string]any{
		"version": 6,
		"configurePresets": []map[string]any{{
			"name":      "debug",
			"generator": "Ninja",
			"binaryDir": "out/debug",
		}},
	})
}

func probeStageName(stage int) string {
	if stage == 1 {
		return "after configure listing"
	}
	return "after build listing"
}
