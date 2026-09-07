package coveragegcc

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"unit-test-ide.local/test-service/internal/coveragebundle"
	"unit-test-ide.local/test-service/internal/coverageplatform"
	"unit-test-ide.local/test-service/internal/coveragerun"
)

func TestPrepareCollectorBuildsTheFixedGCovrRunner(t *testing.T) {
	workdir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	base, err := os.MkdirTemp(workdir, ".collector-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(base) })
	collectorRoot := filepath.Join(base, "collector")
	sourceRoot := filepath.Join(base, "source")
	objectRoot := filepath.Join(base, "objects")
	for _, path := range []string{collectorRoot, sourceRoot, objectRoot} {
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	python := filepath.Join(base, "python")
	runner := filepath.Join(base, "gcovr-runner.pyz")
	gcov := filepath.Join(base, "gcov")
	for _, path := range []string{python, runner, gcov} {
		if err := os.WriteFile(path, []byte(path), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	execution, err := PrepareCollector(
		collectorTestPin{install: coveragebundle.Installation{
			Root: base, Python: python, Runner: runner,
			PythonVersion:  coveragebundle.RequiredPythonVersion,
			GcovrVersion:   coveragebundle.RequiredGCovrVersion,
			ManifestSHA256: strings.Repeat("a", 64),
		}},
		collectorTestDirectory{path: collectorRoot},
		collectorTestDirectory{path: sourceRoot},
		collectorTestDirectory{path: objectRoot},
		collectorTestPath{path: gcov},
	)
	if err != nil {
		t.Fatalf("PrepareCollector() = %v", err)
	}
	t.Cleanup(func() { _ = execution.Close() })
	spec := execution.ProcessSpec()
	wantDescriptor := filepath.Join(collectorRoot, "gcovr", "descriptor.json")
	if spec.Executable != python || spec.Dir != filepath.Join(collectorRoot, "gcovr") ||
		strings.Join(spec.Args, "\x00") != strings.Join([]string{"-I", "-S", runner, wantDescriptor}, "\x00") {
		t.Fatalf("collector process = %#v, want fixed gcovr runner", spec)
	}
}

type collectorTestPin struct{ install coveragebundle.Installation }

func (pin collectorTestPin) Installation() coveragebundle.Installation { return pin.install }
func (collectorTestPin) Verify() error                                 { return nil }
func (collectorTestPin) Close() error                                  { return nil }

type collectorTestDirectory struct{ path string }

func (directory collectorTestDirectory) Path() string  { return directory.path }
func (directory collectorTestDirectory) Verify() error { return nil }
func (directory collectorTestDirectory) RetainDirectory() (coverageplatform.RetainedDirectory, error) {
	return collectorTestRetainedDirectory{path: directory.path}, nil
}

type collectorTestRetainedDirectory struct{ path string }

func (directory collectorTestRetainedDirectory) Path() string { return directory.path }
func (collectorTestRetainedDirectory) Verify() error          { return nil }
func (collectorTestRetainedDirectory) Close() error           { return nil }

type collectorTestPath struct{ path string }

func (path collectorTestPath) Path() string { return path.path }
func (collectorTestPath) Verify() error     { return nil }

var _ coveragebundle.Pin = collectorTestPin{}
var _ coverageplatform.DirectoryVerifier = collectorTestDirectory{}
var _ coverageplatform.RetainedDirectory = collectorTestRetainedDirectory{}
var _ coveragerun.TrustedPath = collectorTestPath{}
