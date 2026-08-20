package coverageexec

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"unit-test-ide.local/test-service/internal/task"
)

func TestExecutionRootCleanupDoesNotFollowReplacedPath(t *testing.T) {
	parent := t.TempDir()
	path := filepath.Join(parent, "owned")
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	owner, err := retainExecutionRoot(path)
	if err != nil {
		t.Fatal(err)
	}
	replaced := filepath.Join(parent, "replaced")
	if err := os.Rename(path, replaced); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(path, "do-not-delete")
	if err := os.WriteFile(marker, []byte("retained"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := owner.Close(); err == nil {
		t.Fatal("cleanup accepted a replaced execution root")
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("cleanup followed replaced path: %v", err)
	}
	if err := owner.Close(); err == nil {
		t.Fatal("idempotent cleanup did not preserve the first validation error")
	}
}

func TestExecutionBoundaryReleasesDelegateAdapterAndRootExactlyOnce(t *testing.T) {
	base := t.TempDir()
	owner, taskRoot, _, _, err := allocateExecutionRoots(
		base, "22222222222222222222222222222222",
	)
	if err != nil {
		t.Fatal(err)
	}
	adapter := &countingPreparedAdapter{}
	delegate := &countingManagedBoundary{}
	execution := &execution{adapter: adapter, root: owner}
	boundary := &executionBoundary{
		delegate: delegate, execution: execution, root: owner,
	}
	execution.boundary = boundary
	if err := boundary.Release(); err != nil {
		t.Fatal(err)
	}
	if err := boundary.Release(); err != nil {
		t.Fatal(err)
	}
	if delegate.releases != 1 || adapter.closes != 1 {
		t.Fatalf("release counts = delegate %d, adapter %d", delegate.releases, adapter.closes)
	}
	if _, err := os.Stat(taskRoot); !os.IsNotExist(err) {
		t.Fatalf("task root remains after release: %v", err)
	}
}

type countingPreparedAdapter struct {
	PreparedAdapter
	closes int
}

func (adapter *countingPreparedAdapter) Close() error {
	adapter.closes++
	return nil
}

type countingManagedBoundary struct{ releases int }

func (*countingManagedBoundary) ValidateExecutable(string) error       { return nil }
func (*countingManagedBoundary) ValidateWorkingDirectory(string) error { return nil }
func (*countingManagedBoundary) Adopt(string)                          {}
func (boundary *countingManagedBoundary) Release() error {
	boundary.releases++
	if boundary.releases > 1 {
		return errors.New("delegate released more than once")
	}
	return nil
}

var _ task.ManagedExecutionBoundary = (*countingManagedBoundary)(nil)

func TestAllocateExecutionRootsReturnsIsolatedInstrumentationProfileAndBuildRoots(t *testing.T) {
	base := t.TempDir()
	taskID := "11111111111111111111111111111111"
	owner, taskRoot, profileRoot, buildRoot, err := allocateExecutionRoots(base, taskID)
	if err != nil {
		t.Fatal(err)
	}
	executionRoot := filepath.Join(base, taskID)
	if taskRoot != filepath.Join(executionRoot, "instrumentation") ||
		profileRoot != filepath.Join(executionRoot, "profiles") ||
		buildRoot != filepath.Join(executionRoot, "build") {
		t.Fatalf("allocated roots = %q, %q, %q", taskRoot, profileRoot, buildRoot)
	}
	for _, path := range []string{executionRoot, taskRoot, profileRoot, buildRoot} {
		if err := owner.VerifyDirectory(path); err != nil {
			t.Fatalf("VerifyDirectory(%q) = %v", path, err)
		}
	}
	if err := owner.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(taskRoot); !os.IsNotExist(err) {
		t.Fatalf("owned task root remains after Close: %v", err)
	}
}

func TestProcessTargetAuthorizationBindsArgumentsEnvironmentAndDirectory(t *testing.T) {
	target := processTarget{
		executable:  `C:\llvm\llvm-profdata.exe`,
		arguments:   []string{"merge", "-o", `C:\owned\coverage.profdata`},
		environment: []string{"SAFE=1"}, unset: []string{"LLVM_PROFILE_FILE"},
		directory: `C:\owned`,
	}
	execution := &execution{targets: []processTarget{target}}
	if !execution.approvesTarget(
		target.executable, target.arguments, target.environment,
		target.unset, target.directory,
	) {
		t.Fatal("exact retained process target was rejected")
	}
	mutations := []struct {
		name string
		edit func(*processTarget)
	}{
		{"executable", func(value *processTarget) { value.executable = `C:\other\llvm-profdata.exe` }},
		{"arguments", func(value *processTarget) { value.arguments[2] = `C:\other\coverage.profdata` }},
		{"environment", func(value *processTarget) { value.environment[0] = "TOKEN=secret" }},
		{"unset", func(value *processTarget) { value.unset = nil }},
		{"directory", func(value *processTarget) { value.directory = `C:\other` }},
	}
	for _, mutation := range mutations {
		t.Run(mutation.name, func(t *testing.T) {
			candidate := processTarget{
				executable:  target.executable,
				arguments:   append([]string(nil), target.arguments...),
				environment: append([]string(nil), target.environment...),
				unset:       append([]string(nil), target.unset...),
				directory:   target.directory,
			}
			mutation.edit(&candidate)
			if execution.approvesTarget(
				candidate.executable, candidate.arguments,
				candidate.environment, candidate.unset, candidate.directory,
			) {
				t.Fatal("mutated process target was accepted")
			}
		})
	}
}
