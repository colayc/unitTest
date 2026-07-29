package cmake

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"unit-test-ide.local/test-service/internal/probe"
)

func TestResolverDoesNotSearchPath(t *testing.T) {
	_, err := Resolve(context.Background(), fakeRunner{}, ResolverConfig{
		Platform: "linux", Architecture: "x64",
	})
	if !errors.Is(err, ErrCMakeUnavailable) {
		t.Fatalf("error = %v", err)
	}
}

func TestResolverRejectsSelfConsistentBundleOutsideImmutableProductionPolicy(t *testing.T) {
	bundle := createBundle(t, "linux-x64")
	runner := &versionRunner{t: t, version: "4.3.4"}

	_, err := Resolve(context.Background(), runner, ResolverConfig{
		BundleRoot:   bundle,
		Platform:     "linux",
		Architecture: "x64",
	})
	if !errors.Is(err, ErrInvalidManifest) {
		t.Fatalf("error = %v, want ErrInvalidManifest", err)
	}
	if len(runner.specs) != 0 {
		t.Fatalf("probe calls = %d, want 0 before immutable policy is proven", len(runner.specs))
	}
}

func TestResolverPrefersOverrideThenBundleThenDev(t *testing.T) {
	override := currentExecutable(t)
	bundle := createBundle(t, "linux-x64")
	dev := copyExecutable(t, currentExecutable(t), filepath.Join(t.TempDir(), executableName("dev-cmake")))

	runner := &versionRunner{t: t, version: "4.3.4"}
	installation, err := Resolve(context.Background(), runner, ResolverConfig{
		Override:      override,
		BundleRoot:    bundle,
		DevExecutable: dev,
		Platform:      "linux",
		Architecture:  "x64",
	})
	if err != nil {
		t.Fatalf("Resolve(override) error = %v", err)
	}
	if installation.Source != SourceOverride ||
		installation.Executable != canonicalPath(t, override) ||
		installation.Root != filepath.Dir(canonicalPath(t, override)) {
		t.Fatalf("override installation = %#v", installation)
	}
	runner.assertExecutables(t, canonicalPath(t, override))

	runner = &versionRunner{t: t, version: "4.3.4"}
	installation, err = resolveFixtureBundle(t, context.Background(), runner, ResolverConfig{
		BundleRoot:    bundle,
		DevExecutable: dev,
		Platform:      "linux",
		Architecture:  "x64",
	})
	if err != nil {
		t.Fatalf("Resolve(bundle) error = %v", err)
	}
	bundleExecutable := filepath.Join(bundle, "4.3.4", "linux-x64", "cmake-4.3.4-linux-x86_64", "bin", "cmake")
	if installation.Source != SourceBundle ||
		installation.Executable != canonicalPath(t, bundleExecutable) ||
		installation.Root != canonicalPath(t, filepath.Dir(filepath.Dir(bundleExecutable))) {
		t.Fatalf("bundle installation = %#v", installation)
	}
	runner.assertExecutables(t, canonicalPath(t, bundleExecutable))

	runner = &versionRunner{t: t, version: "4.3.4"}
	installation, err = Resolve(context.Background(), runner, ResolverConfig{
		DevExecutable: dev,
		Platform:      "linux",
		Architecture:  "x64",
	})
	if err != nil {
		t.Fatalf("Resolve(dev) error = %v", err)
	}
	if installation.Source != SourceDev ||
		installation.Executable != canonicalPath(t, dev) ||
		installation.Root != filepath.Dir(canonicalPath(t, dev)) {
		t.Fatalf("dev installation = %#v", installation)
	}
	runner.assertExecutables(t, canonicalPath(t, dev))
}

func TestResolverRejectsRelativeOverrideWithoutFallingBack(t *testing.T) {
	bundle := createBundle(t, "linux-x64")

	_, err := Resolve(context.Background(), &versionRunner{t: t, version: "4.3.4"}, ResolverConfig{
		Override:     "cmake",
		BundleRoot:   bundle,
		Platform:     "linux",
		Architecture: "x64",
	})
	if !errors.Is(err, ErrInvalidExecutable) {
		t.Fatalf("error = %v, want ErrInvalidExecutable", err)
	}
}

func TestResolverRejectsNonRegularOverride(t *testing.T) {
	_, err := Resolve(context.Background(), &versionRunner{t: t, version: "4.3.4"}, ResolverConfig{
		Override:     t.TempDir(),
		Platform:     "linux",
		Architecture: "x64",
	})
	if !errors.Is(err, ErrInvalidExecutable) {
		t.Fatalf("error = %v, want ErrInvalidExecutable", err)
	}
}

func TestResolverProbesJSONVersionWithFixedBounds(t *testing.T) {
	executable := currentExecutable(t)
	runner := &versionRunner{t: t, version: "4.3.4"}

	installation, err := Resolve(context.Background(), runner, ResolverConfig{
		Override:     executable,
		Platform:     "linux",
		Architecture: "x64",
	})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if installation.Version != "4.3.4" {
		t.Fatalf("Version = %q", installation.Version)
	}
	runner.assertCalls(t, expectedVersionSpec(canonicalPath(t, executable), "--version=json-v1"))
}

func TestResolverFallsBackOnlyWhenJSONCannotBeParsed(t *testing.T) {
	executable := currentExecutable(t)
	runner := &scriptedRunner{t: t, steps: []runnerStep{
		{
			want:   expectedVersionSpec(canonicalPath(t, executable), "--version=json-v1"),
			result: probe.Result{ExitCode: 0, Stdout: []byte("not-json")},
		},
		{
			want:   expectedVersionSpec(canonicalPath(t, executable), "--version"),
			result: probe.Result{ExitCode: 0, Stdout: []byte("cmake version 4.3.4\r\n\r\nCMake suite maintained and supported by Kitware.")},
		},
	}}

	installation, err := Resolve(context.Background(), runner, ResolverConfig{
		Override:     executable,
		Platform:     "linux",
		Architecture: "x64",
	})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if installation.Version != "4.3.4" {
		t.Fatalf("Version = %q", installation.Version)
	}
	runner.assertComplete(t)
}

func TestResolverDoesNotFallbackForValidInvalidJSONVersionDocuments(t *testing.T) {
	executable := currentExecutable(t)
	tests := []struct {
		name   string
		output string
	}{
		{
			name:   "wrong program",
			output: `{"dependencies":[],"program":{"name":"ctest","version":{"major":4,"minor":3,"patch":4,"string":"4.3.4"}},"version":{"major":1,"minor":0}}`,
		},
		{
			name:   "wrong field type",
			output: `{"dependencies":[],"program":{"name":"cmake","version":{"major":"4","minor":3,"patch":4,"string":"4.3.4"}},"version":{"major":1,"minor":0}}`,
		},
		{
			name:   "wrong schema version",
			output: `{"dependencies":[],"program":{"name":"cmake","version":{"major":4,"minor":3,"patch":4,"string":"4.3.4"}},"version":{"major":2,"minor":0}}`,
		},
		{
			name:   "component string mismatch",
			output: `{"dependencies":[],"program":{"name":"cmake","version":{"major":4,"minor":3,"patch":5,"string":"4.3.4"}},"version":{"major":1,"minor":0}}`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			runner := &scriptedRunner{t: t, steps: []runnerStep{{
				want: expectedVersionSpec(canonicalPath(t, executable), "--version=json-v1"),
				result: probe.Result{
					ExitCode: 0,
					Stdout:   []byte(test.output),
				},
			}}}

			_, err := Resolve(context.Background(), runner, ResolverConfig{
				Override: executable,
			})
			if err == nil {
				t.Fatal("error = nil, want valid JSON rejection")
			}
			runner.assertComplete(t)
		})
	}
}

func TestResolverDoesNotFallbackAfterJSONProbeFailure(t *testing.T) {
	executable := currentExecutable(t)
	probeFailure := errors.New("probe failed")
	runner := &scriptedRunner{t: t, steps: []runnerStep{{
		want: expectedVersionSpec(canonicalPath(t, executable), "--version=json-v1"),
		err:  probeFailure,
	}}}

	_, err := Resolve(context.Background(), runner, ResolverConfig{
		Override:     executable,
		Platform:     "linux",
		Architecture: "x64",
	})
	if !errors.Is(err, probeFailure) {
		t.Fatalf("error = %v, want probe failure", err)
	}
	runner.assertComplete(t)
}

func TestResolverPropagatesProbeTimeoutWithoutFallback(t *testing.T) {
	executable := currentExecutable(t)
	runner := &scriptedRunner{t: t, steps: []runnerStep{{
		want: expectedVersionSpec(canonicalPath(t, executable), "--version=json-v1"),
		err:  probe.ErrTimeout,
	}}}

	_, err := Resolve(context.Background(), runner, ResolverConfig{
		Override:     executable,
		Platform:     "linux",
		Architecture: "x64",
	})
	if !errors.Is(err, probe.ErrTimeout) {
		t.Fatalf("error = %v, want probe.ErrTimeout", err)
	}
	runner.assertComplete(t)
}

func TestResolverPropagatesOutputLimitWithoutFallback(t *testing.T) {
	executable := currentExecutable(t)
	runner := &scriptedRunner{t: t, steps: []runnerStep{{
		want: expectedVersionSpec(canonicalPath(t, executable), "--version=json-v1"),
		err:  probe.ErrOutputLimit,
	}}}

	_, err := Resolve(context.Background(), runner, ResolverConfig{
		Override:     executable,
		Platform:     "linux",
		Architecture: "x64",
	})
	if !errors.Is(err, probe.ErrOutputLimit) {
		t.Fatalf("error = %v, want probe.ErrOutputLimit", err)
	}
	runner.assertComplete(t)
}

func TestResolverDoesNotFallbackAfterParsedVersionMismatch(t *testing.T) {
	bundle := createBundle(t, "linux-x64")
	runner := &versionRunner{t: t, version: "4.3.5"}

	_, err := resolveFixtureBundle(t, context.Background(), runner, ResolverConfig{
		BundleRoot:   bundle,
		Platform:     "linux",
		Architecture: "x64",
	})
	if !errors.Is(err, ErrVersionMismatch) {
		t.Fatalf("error = %v, want ErrVersionMismatch", err)
	}
	if len(runner.specs) != 1 {
		t.Fatalf("probe calls = %d, want 1", len(runner.specs))
	}
}

func TestResolverRejectsArchiveIdentityMismatch(t *testing.T) {
	bundle := createBundle(t, "linux-x64")
	statePath := filepath.Join(bundle, "4.3.4", "linux-x64", "bundle-state.json")
	mutateJSONFile(t, statePath, func(value map[string]any) {
		value["archiveSha256"] = strings.Repeat("0", 64)
	})

	_, err := resolveFixtureBundle(t, context.Background(), &versionRunner{t: t, version: "4.3.4"}, ResolverConfig{
		BundleRoot:   bundle,
		Platform:     "linux",
		Architecture: "x64",
	})
	if !errors.Is(err, ErrBundleIntegrity) {
		t.Fatalf("error = %v, want ErrBundleIntegrity", err)
	}
}

func TestResolverRejectsExecutableHashMismatch(t *testing.T) {
	bundle := createBundle(t, "linux-x64")
	executable := filepath.Join(bundle, "4.3.4", "linux-x64", "cmake-4.3.4-linux-x86_64", "bin", "cmake")
	if err := os.WriteFile(executable, []byte("tampered"), 0o755); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	_, err := resolveFixtureBundle(t, context.Background(), &versionRunner{t: t, version: "4.3.4"}, ResolverConfig{
		BundleRoot:   bundle,
		Platform:     "linux",
		Architecture: "x64",
	})
	if !errors.Is(err, ErrBundleIntegrity) {
		t.Fatalf("error = %v, want ErrBundleIntegrity", err)
	}
}

func TestResolverRejectsLicenseHashMismatch(t *testing.T) {
	bundle := createBundle(t, "linux-x64")
	license := filepath.Join(bundle, "4.3.4", "linux-x64", "cmake-4.3.4-linux-x86_64", "doc", "cmake", "LICENSE.rst")
	if err := os.WriteFile(license, []byte("tampered"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	_, err := resolveFixtureBundle(t, context.Background(), &versionRunner{t: t, version: "4.3.4"}, ResolverConfig{
		BundleRoot:   bundle,
		Platform:     "linux",
		Architecture: "x64",
	})
	if !errors.Is(err, ErrBundleIntegrity) {
		t.Fatalf("error = %v, want ErrBundleIntegrity", err)
	}
}

func TestResolverRejectsBundleSymlinkEscape(t *testing.T) {
	bundle := createBundle(t, "linux-x64")
	platformDirectory := filepath.Join(bundle, "4.3.4", "linux-x64")
	if err := os.RemoveAll(platformDirectory); err != nil {
		t.Fatalf("RemoveAll() error = %v", err)
	}
	outside := t.TempDir()
	createDirectoryLink(t, platformDirectory, outside)

	_, err := resolveFixtureBundle(t, context.Background(), &versionRunner{t: t, version: "4.3.4"}, ResolverConfig{
		BundleRoot:   bundle,
		Platform:     "linux",
		Architecture: "x64",
	})
	if !errors.Is(err, ErrBundleIntegrity) {
		t.Fatalf("error = %v, want ErrBundleIntegrity", err)
	}
}

func TestResolverIdentityIsDeterministicAndIncludesArchiveIdentity(t *testing.T) {
	firstBundle := createBundle(t, "linux-x64")
	secondBundle := copyBundle(t, firstBundle)

	first, err := resolveFixtureBundle(t, context.Background(), &versionRunner{t: t, version: "4.3.4"}, ResolverConfig{
		BundleRoot: firstBundle, Platform: "linux", Architecture: "x64",
	})
	if err != nil {
		t.Fatalf("Resolve(first) error = %v", err)
	}
	firstAgain, err := resolveFixtureBundle(t, context.Background(), &versionRunner{t: t, version: "4.3.4"}, ResolverConfig{
		BundleRoot: firstBundle, Platform: "linux", Architecture: "x64",
	})
	if err != nil {
		t.Fatalf("Resolve(first again) error = %v", err)
	}
	if first.Identity != firstAgain.Identity {
		t.Fatalf("same installation identities differ: %q != %q", first.Identity, firstAgain.Identity)
	}

	second, err := resolveFixtureBundle(t, context.Background(), &versionRunner{t: t, version: "4.3.4"}, ResolverConfig{
		BundleRoot: secondBundle, Platform: "linux", Architecture: "x64",
	})
	if err != nil {
		t.Fatalf("Resolve(second) error = %v", err)
	}
	if first.Identity == second.Identity {
		t.Fatalf("different canonical paths produced identity %q", first.Identity)
	}

	manifestPath := filepath.Join(secondBundle, "manifest.json")
	statePath := filepath.Join(secondBundle, "4.3.4", "linux-x64", "bundle-state.json")
	replacement := strings.Repeat("a", 64)
	mutateJSONFile(t, manifestPath, func(value map[string]any) {
		archives := value["archives"].(map[string]any)
		archive := archives["linux-x64"].(map[string]any)
		archive["archiveSha256"] = replacement
	})
	mutateJSONFile(t, statePath, func(value map[string]any) {
		value["archiveSha256"] = replacement
	})
	changedPolicy, err := loadManifest(manifestPath)
	if err != nil {
		t.Fatalf("loadManifest(changed archive) error = %v", err)
	}
	changedArchive, err := resolveWithPolicy(context.Background(), &versionRunner{t: t, version: "4.3.4"}, ResolverConfig{
		BundleRoot: secondBundle, Platform: "linux", Architecture: "x64",
	}, changedPolicy)
	if err != nil {
		t.Fatalf("Resolve(changed archive) error = %v", err)
	}
	if second.Identity == changedArchive.Identity {
		t.Fatalf("archive identity change did not affect %q", second.Identity)
	}
}

func TestInstallationIdentityIncludesOSFileIdentity(t *testing.T) {
	input := identityInput{
		Path:    filepath.Join(t.TempDir(), "cmake"),
		Version: "4.3.4",
		Source:  SourceOverride,
		FileIdentity: fileIdentity{
			ExecutableSha256:     strings.Repeat("a", 64),
			ExecutableOSIdentity: "test-volume:file-one",
		},
	}
	first, err := installationIdentity(input)
	if err != nil {
		t.Fatalf("installationIdentity(first) error = %v", err)
	}
	input.FileIdentity.ExecutableOSIdentity = "test-volume:file-two"
	second, err := installationIdentity(input)
	if err != nil {
		t.Fatalf("installationIdentity(second) error = %v", err)
	}
	if first == second {
		t.Fatalf("different OS file identities produced %q", first)
	}
}

func TestResolverBundleReturnsVerifiedLicensePath(t *testing.T) {
	bundle := createBundle(t, "win32-x64")
	installation, err := resolveFixtureBundle(t, context.Background(), &versionRunner{t: t, version: "4.3.4"}, ResolverConfig{
		BundleRoot:   bundle,
		Platform:     "win32",
		Architecture: "x64",
	})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	want := filepath.Join(bundle, "4.3.4", "win32-x64", "cmake-4.3.4-windows-x86_64", "doc", "cmake", "LICENSE.rst")
	if installation.LicensePath != canonicalPath(t, want) {
		t.Fatalf("LicensePath = %q, want %q", installation.LicensePath, canonicalPath(t, want))
	}
}

func TestResolverFailsClosedWhenExecutableChangesDuringProbe(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(string) error
	}{
		{
			name: "content mutation",
			mutate: func(target string) error {
				return os.WriteFile(target, []byte("changed after verification"), 0o755)
			},
		},
		{
			name: "file identity replacement",
			mutate: func(target string) error {
				replacement := target + ".replacement"
				if err := os.WriteFile(replacement, []byte("replacement after verification"), 0o755); err != nil {
					return err
				}
				if err := os.Remove(target); err != nil {
					return err
				}
				return os.Rename(replacement, target)
			},
		},
	}

	for _, test := range tests {
		t.Run("bundle/"+test.name, func(t *testing.T) {
			bundle := createBundle(t, "linux-x64")
			runner := &mutatingVersionRunner{
				versionRunner: versionRunner{t: t, version: "4.3.4"},
				mutate:        test.mutate,
			}

			_, err := resolveFixtureBundle(t, context.Background(), runner, ResolverConfig{
				BundleRoot: bundle, Platform: "linux", Architecture: "x64",
			})
			assertMutationFailsClosed(t, runner.mutationErr, err, ErrBundleIntegrity)
		})

		t.Run("override/"+test.name, func(t *testing.T) {
			executable := copyExecutable(
				t,
				currentExecutable(t),
				filepath.Join(t.TempDir(), executableName("race-cmake")),
			)
			runner := &mutatingVersionRunner{
				versionRunner: versionRunner{t: t, version: "4.3.4"},
				mutate:        test.mutate,
			}

			_, err := Resolve(context.Background(), runner, ResolverConfig{Override: executable})
			assertMutationFailsClosed(t, runner.mutationErr, err, ErrInvalidExecutable)
		})
	}
}

func assertMutationFailsClosed(t *testing.T, mutationErr, resolveErr, want error) {
	t.Helper()
	if mutationErr == nil {
		if !errors.Is(resolveErr, want) {
			t.Fatalf("replacement succeeded; Resolve error = %v, want %v", resolveErr, want)
		}
		return
	}
	if resolveErr != nil {
		t.Fatalf("replacement was blocked (%v), but Resolve error = %v", mutationErr, resolveErr)
	}
}

type runnerStep struct {
	want   probe.Spec
	result probe.Result
	err    error
}

type scriptedRunner struct {
	t     *testing.T
	steps []runnerStep
	calls int
}

func (runner *scriptedRunner) Run(_ context.Context, spec probe.Spec) (probe.Result, error) {
	runner.t.Helper()
	if runner.calls >= len(runner.steps) {
		runner.t.Fatalf("unexpected probe call: %#v", spec)
	}
	step := runner.steps[runner.calls]
	runner.calls++
	if !reflect.DeepEqual(spec, step.want) {
		runner.t.Fatalf("probe spec = %#v, want %#v", spec, step.want)
	}
	return step.result, step.err
}

func (runner *scriptedRunner) assertComplete(t *testing.T) {
	t.Helper()
	if runner.calls != len(runner.steps) {
		t.Fatalf("probe calls = %d, want %d", runner.calls, len(runner.steps))
	}
}

type versionRunner struct {
	t       *testing.T
	version string
	specs   []probe.Spec
}

func (runner *versionRunner) Run(_ context.Context, spec probe.Spec) (probe.Result, error) {
	runner.t.Helper()
	runner.specs = append(runner.specs, spec)
	if !reflect.DeepEqual(spec.Args, []string{"--version=json-v1"}) {
		runner.t.Fatalf("probe args = %#v, want --version=json-v1", spec.Args)
	}
	var major, minor, patch int
	if _, err := fmt.Sscanf(runner.version, "%d.%d.%d", &major, &minor, &patch); err != nil {
		runner.t.Fatalf("invalid fake version %q: %v", runner.version, err)
	}
	output := fmt.Sprintf(
		`{"dependencies":[],"program":{"name":"cmake","version":{"major":%d,"minor":%d,"patch":%d,"string":%q}},"version":{"major":1,"minor":0}}`,
		major, minor, patch, runner.version,
	)
	return probe.Result{ExitCode: 0, Stdout: []byte(output), Stderr: []byte{}}, nil
}

func (runner *versionRunner) assertExecutables(t *testing.T, want ...string) {
	t.Helper()
	if len(runner.specs) != len(want) {
		t.Fatalf("probe calls = %d, want %d", len(runner.specs), len(want))
	}
	for index := range want {
		if runner.specs[index].Executable != want[index] {
			t.Fatalf("probe executable[%d] = %q, want %q", index, runner.specs[index].Executable, want[index])
		}
	}
}

func (runner *versionRunner) assertCalls(t *testing.T, want ...probe.Spec) {
	t.Helper()
	if !reflect.DeepEqual(runner.specs, want) {
		t.Fatalf("probe specs = %#v, want %#v", runner.specs, want)
	}
}

type mutatingVersionRunner struct {
	versionRunner
	mutate      func(string) error
	mutationErr error
}

func (runner *mutatingVersionRunner) Run(ctx context.Context, spec probe.Spec) (probe.Result, error) {
	runner.mutationErr = runner.mutate(spec.Executable)
	return runner.versionRunner.Run(ctx, spec)
}

func expectedVersionSpec(executable, argument string) probe.Spec {
	return probe.Spec{
		Executable: executable,
		Args:       []string{argument},
		Env:        []string{},
		Timeout:    5 * time.Second,
		MaxOutput:  256 * 1024,
	}
}

func currentExecutable(t *testing.T) string {
	t.Helper()
	path, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable() error = %v", err)
	}
	return path
}

func executableName(base string) string {
	if runtime.GOOS == "windows" {
		return base + ".exe"
	}
	return base
}

func copyExecutable(t *testing.T, source, target string) string {
	t.Helper()
	data, err := os.ReadFile(source)
	if err != nil {
		t.Fatalf("ReadFile(%q) error = %v", source, err)
	}
	if err := os.WriteFile(target, data, 0o755); err != nil {
		t.Fatalf("WriteFile(%q) error = %v", target, err)
	}
	return target
}

func canonicalPath(t *testing.T, path string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		canonical, err := filepath.Abs(path)
		if err != nil {
			t.Fatalf("Abs(%q) error = %v", path, err)
		}
		return filepath.Clean(canonical)
	}
	canonical, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatalf("EvalSymlinks(%q) error = %v", path, err)
	}
	canonical, err = filepath.Abs(canonical)
	if err != nil {
		t.Fatalf("Abs(%q) error = %v", canonical, err)
	}
	return filepath.Clean(canonical)
}

func resolveFixtureBundle(
	t *testing.T,
	ctx context.Context,
	runner probe.Runner,
	config ResolverConfig,
) (Installation, error) {
	t.Helper()
	policy, err := loadManifest(filepath.Join("testdata", "bundle-manifest.valid.json"))
	if err != nil {
		t.Fatalf("load fixture policy: %v", err)
	}
	return resolveWithPolicy(ctx, runner, config, policy)
}

func createBundle(t *testing.T, key string) string {
	t.Helper()
	bundle := t.TempDir()
	manifestBytes, err := os.ReadFile(filepath.Join("testdata", "bundle-manifest.valid.json"))
	if err != nil {
		t.Fatalf("ReadFile(manifest) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(bundle, "manifest.json"), manifestBytes, 0o600); err != nil {
		t.Fatalf("WriteFile(manifest) error = %v", err)
	}

	var manifest Manifest
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
		t.Fatalf("Unmarshal(manifest) error = %v", err)
	}
	archive, ok := manifest.Archives[key]
	if !ok {
		t.Fatalf("fixture has no archive %q", key)
	}
	platformDirectory := filepath.Join(bundle, manifest.CMakeVersion, key)
	installRoot := filepath.Join(platformDirectory, filepath.FromSlash(archive.RootDirectory))
	for relative, content := range map[string]string{
		archive.Executable:  "fake-cmake",
		archive.LicensePath: "license",
	} {
		path := filepath.Join(installRoot, filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("MkdirAll(%q) error = %v", filepath.Dir(path), err)
		}
		mode := os.FileMode(0o644)
		if relative == archive.Executable {
			mode = 0o755
		}
		if err := os.WriteFile(path, []byte(content), mode); err != nil {
			t.Fatalf("WriteFile(%q) error = %v", path, err)
		}
	}
	state := bundleState{
		SchemaVersion:  manifest.SchemaVersion,
		Key:            key,
		CMakeVersion:   manifest.CMakeVersion,
		ArchiveSha256:  archive.ArchiveSha256,
		InstalledFiles: archive.InstalledFiles,
	}
	stateBytes, err := json.Marshal(state)
	if err != nil {
		t.Fatalf("Marshal(state) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(platformDirectory, "bundle-state.json"), stateBytes, 0o600); err != nil {
		t.Fatalf("WriteFile(state) error = %v", err)
	}
	return bundle
}

func copyBundle(t *testing.T, source string) string {
	t.Helper()
	target := t.TempDir()
	err := filepath.WalkDir(source, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		destination := filepath.Join(target, relative)
		if entry.IsDir() {
			return os.MkdirAll(destination, 0o755)
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(destination, data, info.Mode().Perm())
	})
	if err != nil {
		t.Fatalf("copy bundle error = %v", err)
	}
	return target
}

func mutateJSONFile(t *testing.T, path string, mutate func(map[string]any)) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%q) error = %v", path, err)
	}
	var value map[string]any
	if err := json.Unmarshal(data, &value); err != nil {
		t.Fatalf("Unmarshal(%q) error = %v", path, err)
	}
	mutate(value)
	data, err = json.Marshal(value)
	if err != nil {
		t.Fatalf("Marshal(%q) error = %v", path, err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("WriteFile(%q) error = %v", path, err)
	}
}

func createDirectoryLink(t *testing.T, link, target string) {
	t.Helper()
	if runtime.GOOS != "windows" {
		if err := os.Symlink(target, link); err != nil {
			t.Fatalf("Symlink() error = %v", err)
		}
		return
	}
	command := exec.Command("cmd.exe", "/d", "/c", "mklink", "/J", link, target)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("create junction error = %v: %s", err, output)
	}
}

type fakeRunner struct{}

func (fakeRunner) Run(context.Context, probe.Spec) (probe.Result, error) {
	return probe.Result{}, errors.New("unexpected probe")
}
