package coveragebundle

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"unit-test-ide.local/test-service/internal/task"
)

type fakeRunnerPin struct {
	installation Installation
	verifyCalls  int
	closeCalls   int
	closed       bool
	closeErr     error
}

type bareDirectoryCapability struct{ path string }

func (capability bareDirectoryCapability) Path() string  { return capability.path }
func (capability bareDirectoryCapability) Verify() error { return nil }

func (pin *fakeRunnerPin) Installation() Installation { return pin.installation }
func (pin *fakeRunnerPin) Verify() error {
	if pin.closed {
		return ErrBundleIntegrity
	}
	pin.verifyCalls++
	return nil
}
func (pin *fakeRunnerPin) Close() error {
	pin.closeCalls++
	pin.closed = true
	return pin.closeErr
}

func TestPrepareRunnerReturnsOwnedCleanupFailureAfterParseMismatch(t *testing.T) {
	base := strictTestTempDir(t)
	coverageRoot, projectRoot, objects := filepath.Join(base, "coverage"), filepath.Join(base, "project"), filepath.Join(base, "objects")
	for _, directory := range []string{coverageRoot, projectRoot, objects} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	python, runner, gcov := filepath.Join(base, "python.exe"), filepath.Join(base, "runner.pyz"), filepath.Join(base, "gcov.exe")
	for _, path := range []string{python, runner, gcov} {
		if err := os.WriteFile(path, []byte(path), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	parseFailure := errors.New("injected descriptor parse failure")
	cleanupFailure := errors.New("injected owned cleanup failure")
	originalParse, originalRemove := parsePreparedDescriptor, removePinnedChildForCleanup
	parsePreparedDescriptor = func(*OwnedDescriptor) (Descriptor, error) { return Descriptor{}, parseFailure }
	removePinnedChildForCleanup = func(parent, child *pinnedObject, name string) error {
		return errors.Join(removePinnedChild(parent, child, name), cleanupFailure)
	}
	t.Cleanup(func() {
		parsePreparedDescriptor = originalParse
		removePinnedChildForCleanup = originalRemove
	})
	pin := &fakeRunnerPin{installation: Installation{Root: base, Python: python, Runner: runner, PythonVersion: "3.14.6", GcovrVersion: "8.6", ManifestSHA256: strings.Repeat("a", 64)}}
	if execution, err := PrepareRunner(pin, DescriptorInput{Root: projectRoot, ObjectDirectory: objects, GcovExecutable: gcov, OutputPath: filepath.Join(coverageRoot, "gcovr", "coverage.json")}, descriptorCapabilitiesForTest(t, coverageRoot, projectRoot, objects, gcov)); execution != nil || !errors.Is(err, parseFailure) || !errors.Is(err, cleanupFailure) {
		t.Fatalf("PrepareRunner() = (%v, %v), want parse and owned cleanup failures", execution, err)
	}
}

func TestPrepareRunnerReturnsExecutionCleanupFailureAfterFinalVerify(t *testing.T) {
	base := strictTestTempDir(t)
	coverageRoot, projectRoot, objects := filepath.Join(base, "coverage"), filepath.Join(base, "project"), filepath.Join(base, "objects")
	for _, directory := range []string{coverageRoot, projectRoot, objects} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	python, runner, gcov := filepath.Join(base, "python.exe"), filepath.Join(base, "runner.pyz"), filepath.Join(base, "gcov.exe")
	for _, path := range []string{python, runner, gcov} {
		if err := os.WriteFile(path, []byte(path), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	verifyFailure := errors.New("injected final execution verification failure")
	closeFailure := errors.New("injected execution cleanup failure")
	originalVerify := verifyPreparedExecution
	verifyPreparedExecution = func(*PreparedExecution) error { return verifyFailure }
	t.Cleanup(func() { verifyPreparedExecution = originalVerify })
	pin := &fakeRunnerPin{installation: Installation{Root: base, Python: python, Runner: runner, PythonVersion: "3.14.6", GcovrVersion: "8.6", ManifestSHA256: strings.Repeat("a", 64)}, closeErr: closeFailure}
	if execution, err := PrepareRunner(pin, DescriptorInput{Root: projectRoot, ObjectDirectory: objects, GcovExecutable: gcov, OutputPath: filepath.Join(coverageRoot, "gcovr", "coverage.json")}, descriptorCapabilitiesForTest(t, coverageRoot, projectRoot, objects, gcov)); execution != nil || !errors.Is(err, verifyFailure) || !errors.Is(err, closeFailure) {
		t.Fatalf("PrepareRunner() = (%v, %v), want execution verification and cleanup failures", execution, err)
	}
}

func TestPrepareRunnerBuildsExactIsolatedProcessSpec(t *testing.T) {
	base := strictTestTempDir(t)
	coverageRoot := filepath.Join(base, "coverage")
	projectRoot := filepath.Join(base, "project")
	objects := filepath.Join(base, "objects")
	for _, directory := range []string{coverageRoot, projectRoot, objects} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	python := filepath.Join(base, "python.exe")
	runner := filepath.Join(base, "gcovr-runner.pyz")
	gcov := filepath.Join(base, "gcov.exe")
	for _, path := range []string{python, runner, gcov} {
		if err := os.WriteFile(path, []byte(path), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	pin := &fakeRunnerPin{installation: Installation{Root: base, Python: python, Runner: runner, PythonVersion: "3.14.6", GcovrVersion: "8.6", ManifestSHA256: strings.Repeat("a", 64)}}
	execution, err := PrepareRunner(pin, DescriptorInput{
		Root: projectRoot, ObjectDirectory: objects, GcovExecutable: gcov,
		OutputPath: filepath.Join(coverageRoot, "gcovr", "coverage.json"),
	}, descriptorCapabilitiesForTest(t, coverageRoot, projectRoot, objects, gcov))
	if err != nil {
		t.Fatal(err)
	}
	spec := execution.ProcessSpec()
	if got, want := spec.Executable, python; got != want {
		t.Fatalf("Executable = %q, want %q", got, want)
	}
	if got, want := spec.Args, []string{"-I", "-S", runner, execution.DescriptorPath()}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Args = %#v, want %#v", got, want)
	}
	if len(spec.Batch) != 0 || len(spec.Env) != 0 {
		t.Fatalf("runner spec unexpectedly has batch/env: %#v", spec)
	}
	if len(spec.EnvUnset) == 0 {
		t.Fatal("runner spec did not clear hostile environment")
	}
	if err := execution.Verify(); err != nil {
		t.Fatalf("Verify() = %v", err)
	}
	if err := execution.ValidateProcessTarget(spec.Executable, spec.Args, spec.Env, spec.EnvUnset, spec.Dir); err != nil {
		t.Fatalf("ValidateProcessTarget() = %v", err)
	}
	t.Setenv("pYtHoNpAtH", "late-hostile")
	fresh := execution.ProcessSpec()
	if runtime.GOOS != "windows" {
		found := false
		for _, key := range fresh.EnvUnset {
			if key == "pYtHoNpAtH" {
				found = true
			}
		}
		if !found {
			t.Fatalf("launch-time EnvUnset omitted late case variant: %#v", fresh.EnvUnset)
		}
	}
	if err := execution.ValidateProcessTarget(spec.Executable, spec.Args, spec.Env, spec.EnvUnset, spec.Dir); err == nil {
		t.Fatal("ValidateProcessTarget accepted stale launch-time environment policy")
	}
	if err := execution.ValidateProcessTarget(spec.Executable, append([]string{}, spec.Args[:3]...), spec.Env, spec.EnvUnset, spec.Dir); err == nil {
		t.Fatal("ValidateProcessTarget accepted missing descriptor argument")
	}
	if err := execution.Close(); err != nil {
		t.Fatal(err)
	}
	if pin.closeCalls != 1 {
		t.Fatalf("pin Close calls = %d, want 1", pin.closeCalls)
	}
}

func TestPrepareRunnerAcceptsIndependentCapabilities(t *testing.T) {
	collectorBase := strictTestTempDir(t)
	rootBase := strictTestTempDir(t)
	objectsBase := strictTestTempDir(t)
	gcovBase := strictTestTempDir(t)
	collectorRoot, projectRoot := filepath.Join(collectorBase, "collector"), filepath.Join(rootBase, "project")
	objects, gcov := filepath.Join(objectsBase, "objects"), filepath.Join(gcovBase, "gcov")
	for _, directory := range []string{collectorRoot, projectRoot, objects} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(gcov, []byte("gcov"), 0o700); err != nil {
		t.Fatal(err)
	}
	python, runner := filepath.Join(collectorBase, "python"), filepath.Join(collectorBase, "runner.pyz")
	for _, path := range []string{python, runner} {
		if err := os.WriteFile(path, []byte(path), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	capabilities := descriptorCapabilitiesForTest(t, collectorRoot, projectRoot, objects, gcov)
	execution, err := PrepareRunner(&fakeRunnerPin{installation: Installation{Root: collectorBase, Python: python, Runner: runner, PythonVersion: "3.14.6", GcovrVersion: "8.6", ManifestSHA256: strings.Repeat("a", 64)}}, DescriptorInput{
		Root: projectRoot, ObjectDirectory: objects, GcovExecutable: gcov,
		OutputPath: filepath.Join(collectorRoot, "gcovr", "coverage.json"),
	}, capabilities)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := execution.TaskRoot(), filepath.Join(collectorRoot, "gcovr"); got != want {
		t.Fatalf("TaskRoot = %q, want %q", got, want)
	}
	if err := execution.Close(); err != nil {
		t.Fatal(err)
	}
	if err := capabilities.CollectorRoot.Verify(); err != nil {
		t.Fatalf("collector owner Verify = %v", err)
	}
	if err := capabilities.Root.Verify(); err != nil {
		t.Fatalf("root owner Verify = %v", err)
	}
	if err := capabilities.ObjectDirectory.Verify(); err != nil {
		t.Fatalf("object owner Verify = %v", err)
	}
	if err := capabilities.GcovExecutable.Verify(); err != nil {
		t.Fatalf("gcov owner Verify = %v", err)
	}
}

func TestPrepareRunnerRejectsBareCollectorCapabilityWithoutClosingOwners(t *testing.T) {
	base := strictTestTempDir(t)
	collectorRoot, root, objects := filepath.Join(base, "collector"), filepath.Join(base, "root"), filepath.Join(base, "objects")
	for _, directory := range []string{collectorRoot, root, objects} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	gcov, python, runner := filepath.Join(base, "gcov"), filepath.Join(base, "python"), filepath.Join(base, "runner")
	for _, path := range []string{gcov, python, runner} {
		if err := os.WriteFile(path, []byte(path), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	capabilities := descriptorCapabilitiesForTest(t, collectorRoot, root, objects, gcov)
	capabilities.CollectorRoot = bareDirectoryCapability{path: collectorRoot}
	_, err := PrepareRunner(&fakeRunnerPin{installation: Installation{Root: base, Python: python, Runner: runner, PythonVersion: "3.14.6", GcovrVersion: "8.6", ManifestSHA256: strings.Repeat("a", 64)}}, DescriptorInput{Root: root, ObjectDirectory: objects, GcovExecutable: gcov, OutputPath: filepath.Join(collectorRoot, "gcovr", "coverage.json")}, capabilities)
	if err == nil {
		t.Fatal("PrepareRunner accepted a bare collector path")
	}
	if err := capabilities.Root.Verify(); err != nil {
		t.Fatalf("root owner was closed: %v", err)
	}
	if err := capabilities.ObjectDirectory.Verify(); err != nil {
		t.Fatalf("object owner was closed: %v", err)
	}
	if err := capabilities.GcovExecutable.Verify(); err != nil {
		t.Fatalf("gcov owner was closed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(collectorRoot, "gcovr")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("collector child residue: %v", err)
	}
}

func TestPrepareRunnerRejectsTamperedPinAndOutputEscape(t *testing.T) {
	base := strictTestTempDir(t)
	coverageRoot := filepath.Join(base, "coverage")
	if err := os.MkdirAll(coverageRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	installation := Installation{
		Root:   filepath.Join(base, "bundle"),
		Python: filepath.Join(base, "bundle", "python.exe"),
		Runner: filepath.Join(base, "bundle", "runner.pyz"),
	}
	for _, path := range []string{installation.Python, installation.Runner} {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(path), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	pin := &fakeRunnerPin{installation: installation}
	_, err := PrepareRunner(pin, DescriptorInput{
		Root: filepath.Join(base, "root"), ObjectDirectory: filepath.Join(base, "objects"),
		GcovExecutable: filepath.Join(base, "gcov"), OutputPath: filepath.Join(base, "outside.json"),
	}, DescriptorCapabilities{})
	if err == nil {
		t.Fatal("PrepareRunner accepted output outside owned root")
	}
}

func TestPreparedExecutionDetectsRootAndGcovTamper(t *testing.T) {
	base := strictTestTempDir(t)
	coverageRoot := filepath.Join(base, "coverage")
	projectRoot := filepath.Join(base, "project")
	objects := filepath.Join(base, "objects")
	for _, directory := range []string{coverageRoot, projectRoot, objects} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	python := filepath.Join(base, "python.exe")
	runner := filepath.Join(base, "gcovr-runner.pyz")
	gcov := filepath.Join(base, "gcov.exe")
	for _, path := range []string{python, runner, gcov} {
		if err := os.WriteFile(path, []byte(path), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	pin := &fakeRunnerPin{installation: Installation{Root: base, Python: python, Runner: runner, PythonVersion: "3.14.6", GcovrVersion: "8.6", ManifestSHA256: strings.Repeat("a", 64)}}
	execution, err := PrepareRunner(pin, DescriptorInput{
		Root: projectRoot, ObjectDirectory: objects, GcovExecutable: gcov,
		OutputPath: filepath.Join(coverageRoot, "gcovr", "coverage.json"),
	}, descriptorCapabilitiesForTest(t, coverageRoot, projectRoot, objects, gcov))
	if err != nil {
		t.Fatal(err)
	}
	defer execution.Close()
	if err := os.WriteFile(gcov, []byte("tampered gcov"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := execution.Verify(); err == nil {
		t.Fatal("Verify accepted tampered gcov executable")
	}
}

func TestPreparedExecutionDetectsOutputReplacementAfterVerifyAfter(t *testing.T) {
	base := strictTestTempDir(t)
	coverageRoot := filepath.Join(base, "coverage")
	projectRoot := filepath.Join(base, "project")
	objects := filepath.Join(base, "objects")
	for _, directory := range []string{coverageRoot, projectRoot, objects} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	python := filepath.Join(base, "python.exe")
	runner := filepath.Join(base, "gcovr-runner.pyz")
	gcov := filepath.Join(base, "gcov.exe")
	for _, path := range []string{python, runner, gcov} {
		if err := os.WriteFile(path, []byte(path), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	execution, err := PrepareRunner(&fakeRunnerPin{installation: Installation{Root: base, Python: python, Runner: runner, PythonVersion: "3.14.6", GcovrVersion: "8.6", ManifestSHA256: strings.Repeat("a", 64)}}, DescriptorInput{
		Root: projectRoot, ObjectDirectory: objects, GcovExecutable: gcov,
		OutputPath: filepath.Join(coverageRoot, "gcovr", "coverage.json"),
	}, descriptorCapabilitiesForTest(t, coverageRoot, projectRoot, objects, gcov))
	if err != nil {
		t.Fatal(err)
	}
	defer execution.Close()
	outputPath := execution.Descriptor().OutputPath
	if err := os.WriteFile(outputPath, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := execution.VerifyAfter(); err != nil {
		t.Fatalf("first VerifyAfter() = %v", err)
	}
	replacement := outputPath + ".replacement"
	if err := os.WriteFile(replacement, []byte("replacement"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(outputPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(replacement, outputPath); err != nil {
		t.Fatal(err)
	}
	if err := execution.VerifyAfter(); err == nil {
		t.Fatal("VerifyAfter accepted replaced output")
	}
}

func TestPreparedExecutionDetectsOutputInPlaceMutation(t *testing.T) {
	base := strictTestTempDir(t)
	coverageRoot := filepath.Join(base, "coverage")
	projectRoot := filepath.Join(base, "project")
	objects := filepath.Join(base, "objects")
	for _, directory := range []string{coverageRoot, projectRoot, objects} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	python, runner, gcov := filepath.Join(base, "python.exe"), filepath.Join(base, "runner.pyz"), filepath.Join(base, "gcov.exe")
	for _, path := range []string{python, runner, gcov} {
		if err := os.WriteFile(path, []byte(path), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	execution, err := PrepareRunner(&fakeRunnerPin{installation: Installation{Root: base, Python: python, Runner: runner, PythonVersion: "3.14.6", GcovrVersion: "8.6", ManifestSHA256: strings.Repeat("a", 64)}}, DescriptorInput{
		Root: projectRoot, ObjectDirectory: objects, GcovExecutable: gcov, OutputPath: filepath.Join(coverageRoot, "gcovr", "coverage.json"),
	}, descriptorCapabilitiesForTest(t, coverageRoot, projectRoot, objects, gcov))
	if err != nil {
		t.Fatal(err)
	}
	defer execution.Close()
	outputPath := execution.Descriptor().OutputPath
	if err := os.WriteFile(outputPath, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := execution.VerifyAfter(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(outputPath, []byte("{\"mutated\":true}"), 0o600); err == nil {
		if err := execution.VerifyAfter(); err == nil {
			t.Fatal("VerifyAfter accepted in-place output mutation")
		}
	} else if err := execution.VerifyAfter(); err != nil {
		t.Fatalf("VerifyAfter rejected output after retained DELETE pin blocked mutation: %v", err)
	}
}

func TestPinnedOutputConsumesWithoutPathReopen(t *testing.T) {
	base := strictTestTempDir(t)
	coverageRoot, projectRoot, objects := filepath.Join(base, "coverage"), filepath.Join(base, "project"), filepath.Join(base, "objects")
	for _, directory := range []string{coverageRoot, projectRoot, objects} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	python, runner, gcov := filepath.Join(base, "python.exe"), filepath.Join(base, "runner.pyz"), filepath.Join(base, "gcov.exe")
	for _, path := range []string{python, runner, gcov} {
		if err := os.WriteFile(path, []byte(path), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	execution, err := PrepareRunner(&fakeRunnerPin{installation: Installation{Root: base, Python: python, Runner: runner, PythonVersion: "3.14.6", GcovrVersion: "8.6", ManifestSHA256: strings.Repeat("a", 64)}}, DescriptorInput{Root: projectRoot, ObjectDirectory: objects, GcovExecutable: gcov, OutputPath: filepath.Join(coverageRoot, "gcovr", "coverage.json")}, descriptorCapabilitiesForTest(t, coverageRoot, projectRoot, objects, gcov))
	if err != nil {
		t.Fatal(err)
	}
	defer execution.Close()
	if err := os.WriteFile(execution.Descriptor().OutputPath, []byte(`{"ok":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	output, err := execution.PinnedOutput()
	if err != nil {
		t.Fatal(err)
	}
	if execution.descriptor.outputPin == nil || execution.descriptor.outputFile == nil {
		t.Fatal("PinnedOutput did not retain output capability")
	}
	if got, err := output.ReadAll(); err != nil || string(got) != `{"ok":true}` {
		t.Fatalf("PinnedOutput.ReadAll() = %q, %v", got, err)
	}
}

func TestPreparedExecutionRejectsTaskRootReplacementBeforeOutputOpen(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("task-root replacement fixture requires rename/symlink support")
	}
	base := t.TempDir()
	coverageRoot := filepath.Join(base, "coverage")
	projectRoot := filepath.Join(base, "project")
	objects := filepath.Join(base, "objects")
	for _, directory := range []string{coverageRoot, projectRoot, objects} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	python, runner, gcov := filepath.Join(base, "python.exe"), filepath.Join(base, "runner.pyz"), filepath.Join(base, "gcov.exe")
	for _, path := range []string{python, runner, gcov} {
		if err := os.WriteFile(path, []byte(path), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	execution, err := PrepareRunner(&fakeRunnerPin{installation: Installation{Root: base, Python: python, Runner: runner, PythonVersion: "3.14.6", GcovrVersion: "8.6", ManifestSHA256: strings.Repeat("a", 64)}}, DescriptorInput{
		Root: projectRoot, ObjectDirectory: objects, GcovExecutable: gcov, OutputPath: filepath.Join(coverageRoot, "gcovr", "coverage.json"),
	}, descriptorCapabilitiesForTest(t, coverageRoot, projectRoot, objects, gcov))
	if err != nil {
		t.Fatal(err)
	}
	defer execution.Close()
	outputPath := execution.Descriptor().OutputPath
	if err := os.WriteFile(outputPath, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := execution.VerifyAfter(); err != nil {
		t.Fatal(err)
	}
	taskRoot := filepath.Dir(outputPath)
	replacement := taskRoot + ".replacement"
	if err := os.Rename(taskRoot, replacement); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(replacement, taskRoot); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if err := execution.VerifyAfter(); err == nil {
		t.Fatal("VerifyAfter accepted replaced task root")
	}
}

func TestIsolatedRunnerRejectsHostileEnvironment(t *testing.T) {
	t.Setenv("PYTHONPATH", "canonical-hostile")
	t.Setenv("pYtHoNpAtH", "hostile")
	t.Setenv("pIp_Index_URL", "https://hostile.invalid")
	t.Setenv("vIrTuAl_Env", "hostile")
	t.Setenv("hTtP_PrOxY", "http://hostile.invalid")
	t.Setenv("lC_ALL", "C.UTF-8")
	unset := fixedRunnerEnvUnset()
	seen := make(map[string]bool, len(unset))
	for _, key := range unset {
		seen[strings.ToUpper(key)] = true
	}
	for _, key := range []string{"PYTHONPATH", "PIP_INDEX_URL", "VIRTUAL_ENV", "HTTP_PROXY", "LC_ALL"} {
		if !seen[key] {
			t.Fatalf("fixed runner environment did not clear %s: %#v", key, unset)
		}
	}
	foundCanonical, foundCaseVariant := false, false
	for _, key := range unset {
		if key == "PYTHONPATH" {
			foundCanonical = true
		}
		if key == "pYtHoNpAtH" {
			foundCaseVariant = true
		}
	}
	if runtime.GOOS != "windows" && !foundCaseVariant {
		t.Fatalf("fixed runner environment normalized away case-variant key: %#v", unset)
	}
	if !foundCanonical {
		t.Fatalf("fixed runner environment omitted canonical key: %#v", unset)
	}
}

func runtimeGOOS() string {
	return os.Getenv("GOOS")
}

var _ task.ProcessSpec
