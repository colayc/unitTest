package testgenvalidate

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"unit-test-ide.local/test-service/internal/processcontrol"
	"unit-test-ide.local/test-service/internal/task"
)

type fakeRunner struct {
	prepared int
	lastSpec processcontrol.Spec
	process  *fakeProcess
}

func (r *fakeRunner) Prepare(_ context.Context, spec processcontrol.Spec, _ string, _ string) (processcontrol.Process, error) {
	r.prepared++
	r.lastSpec = spec
	return r.process, nil
}
func (r *fakeRunner) Cleanup(context.Context, task.ProcessLease, time.Duration) error { return nil }

type fakeProcess struct {
	events []string
	output chan processcontrol.Output
	done   chan processcontrol.Result
}

func (p *fakeProcess) Lease() task.ProcessLease {
	return task.ProcessLease{TaskID: testID, HostPID: 42, HostStartIdentity: "birth", ServiceInstanceID: testID}
}
func (p *fakeProcess) Start(context.Context) error          { p.events = append(p.events, "start"); return nil }
func (p *fakeProcess) Output() <-chan processcontrol.Output { return p.output }
func (p *fakeProcess) Done() <-chan processcontrol.Result   { return p.done }
func (p *fakeProcess) Terminate(context.Context, time.Duration) error {
	p.events = append(p.events, "terminate")
	select {
	case <-p.done:
	default:
		close(p.done)
	}
	return nil
}
func (p *fakeProcess) Close(context.Context) error { p.events = append(p.events, "close"); return nil }

func TestPreparedExecutorRejectsInjectedEnvironmentBeforePrepare(t *testing.T) {
	root := t.TempDir()
	executable := filepath.Join(root, "cmake.exe")
	if err := os.WriteFile(executable, []byte("fixed-tool"), 0600); err != nil {
		t.Fatal(err)
	}
	runner := &fakeRunner{}
	executor := PreparedProcessExecutor{Runner: runner, TaskID: testID, ServiceInstanceID: testID, ToolSHA256: map[string]string{executable: digestTest([]byte("fixed-tool"))}, Plan: func(context.Context, Stage, Roots) (processcontrol.Spec, error) {
		return processcontrol.Spec{Executable: executable, Dir: filepath.Join(root, "build"), Env: []string{"LD_PRELOAD=evil"}}, nil
	}}
	_, err := executor.Execute(context.Background(), StageConfigure, Roots{Source: filepath.Join(root, "source"), Build: filepath.Join(root, "build"), Artifacts: filepath.Join(root, "artifacts")})
	if err == nil || runner.prepared != 0 {
		t.Fatalf("injected environment prepared: %v %d", err, runner.prepared)
	}
}

func TestPreparedExecutorAcceptsOnlyExactTrustedEnvironment(t *testing.T) {
	root := t.TempDir()
	executable := filepath.Join(root, "cmake.exe")
	if err := os.WriteFile(executable, []byte("fixed-tool"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{"source", "build", "artifacts"} {
		if err := os.Mkdir(filepath.Join(root, dir), 0700); err != nil {
			t.Fatal(err)
		}
	}
	process := &fakeProcess{output: make(chan processcontrol.Output), done: make(chan processcontrol.Result, 1)}
	process.done <- processcontrol.Result{}
	close(process.done)
	close(process.output)
	runner := &fakeRunner{process: process}
	roots := Roots{Source: filepath.Join(root, "source"), Build: filepath.Join(root, "build"), Artifacts: filepath.Join(root, "artifacts")}
	trusted := []string{"INCLUDE=C:\\toolchain\\include", "PATH=C:\\toolchain\\bin"}
	spec := processcontrol.Spec{Executable: executable, Dir: roots.Build, Env: []string{trusted[1], trusted[0]}, EnvUnset: []string{"CL"}}
	executor := PreparedProcessExecutor{
		Runner: runner, TaskID: testID, ServiceInstanceID: testID,
		ToolSHA256:         map[string]string{executable: digestTest([]byte("fixed-tool"))},
		AllowedEnvironment: trusted,
		AllowedEnvUnset:    []string{"CL"},
		Plan:               func(context.Context, Stage, Roots) (processcontrol.Spec, error) { return spec, nil },
		RecordLease:        func(context.Context, task.ProcessLease) error { return nil }, ReleaseLease: func(context.Context, task.ProcessLease) error { return nil },
	}
	if _, err := executor.Execute(context.Background(), StageConfigure, roots); err != nil || runner.prepared != 1 {
		t.Fatalf("trusted environment rejected: %v prepared=%d", err, runner.prepared)
	}
}

func TestPreparedExecutorRejectsEnvironmentOutsideTrustedSnapshot(t *testing.T) {
	root := t.TempDir()
	executable := filepath.Join(root, "cmake.exe")
	if err := os.WriteFile(executable, []byte("fixed-tool"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{"source", "build", "artifacts"} {
		if err := os.Mkdir(filepath.Join(root, dir), 0700); err != nil {
			t.Fatal(err)
		}
	}
	trusted := []string{"PATH=C:\\toolchain\\bin"}
	for _, test := range []struct {
		name  string
		env   []string
		unset []string
	}{
		{name: "changed value", env: []string{"PATH=C:\\attacker"}},
		{name: "extra variable", env: []string{trusted[0], "INCLUDE=C:\\attacker"}},
		{name: "duplicate key", env: []string{trusted[0], "PATH=C:\\toolchain\\bin"}},
		{name: "missing unset", env: trusted},
		{name: "extra unset", env: trusted, unset: []string{"CL", "LINK"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			runner := &fakeRunner{}
			executor := PreparedProcessExecutor{
				Runner: runner, TaskID: testID, ServiceInstanceID: testID,
				ToolSHA256:         map[string]string{executable: digestTest([]byte("fixed-tool"))},
				AllowedEnvironment: trusted,
				AllowedEnvUnset:    []string{"CL"},
				Plan: func(context.Context, Stage, Roots) (processcontrol.Spec, error) {
					return processcontrol.Spec{Executable: executable, Dir: filepath.Join(root, "build"), Env: test.env, EnvUnset: test.unset}, nil
				},
				RecordLease: func(context.Context, task.ProcessLease) error { return nil }, ReleaseLease: func(context.Context, task.ProcessLease) error { return nil },
			}
			if _, err := executor.Execute(context.Background(), StageConfigure, Roots{Source: filepath.Join(root, "source"), Build: filepath.Join(root, "build"), Artifacts: filepath.Join(root, "artifacts")}); err == nil || runner.prepared != 0 {
				t.Fatalf("untrusted environment accepted: %v prepared=%d", err, runner.prepared)
			}
		})
	}
}

func TestPreparedExecutorPersistsLeaseBeforeStartAndCloses(t *testing.T) {
	root := t.TempDir()
	executable := filepath.Join(root, "cmake.exe")
	if err := os.WriteFile(executable, []byte("fixed-tool"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{"source", "build", "artifacts"} {
		if err := os.Mkdir(filepath.Join(root, dir), 0700); err != nil {
			t.Fatal(err)
		}
	}
	process := &fakeProcess{output: make(chan processcontrol.Output), done: make(chan processcontrol.Result, 1)}
	process.done <- processcontrol.Result{}
	close(process.done)
	close(process.output)
	runner := &fakeRunner{process: process}
	events := &process.events
	executor := PreparedProcessExecutor{Runner: runner, TaskID: testID, ServiceInstanceID: testID, ToolSHA256: map[string]string{executable: digestTest([]byte("fixed-tool"))}, Plan: func(context.Context, Stage, Roots) (processcontrol.Spec, error) {
		return processcontrol.Spec{Executable: executable, Dir: filepath.Join(root, "build")}, nil
	}, RecordLease: func(context.Context, task.ProcessLease) error { *events = append(*events, "record"); return nil }, ReleaseLease: func(context.Context, task.ProcessLease) error { *events = append(*events, "release"); return nil }}
	if _, err := executor.Execute(context.Background(), StageCompile, Roots{Source: filepath.Join(root, "source"), Build: filepath.Join(root, "build"), Artifacts: filepath.Join(root, "artifacts")}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(process.events, []string{"record", "start", "record", "close", "release"}) {
		t.Fatalf("lease lifecycle: %v", process.events)
	}
	if !runner.lastSpec.ClosedEnvironment {
		t.Fatal("validator allowed inherited host environment")
	}
}

func TestPreparedExecutorAcceptsOnlyPinnedLaunchPlanTools(t *testing.T) {
	root := t.TempDir()
	executable := filepath.Join(root, "cmake.exe")
	compiler := filepath.Join(root, "clang.exe")
	for path, content := range map[string]string{executable: "fixed-cmake", compiler: "fixed-compiler"} {
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	for _, dir := range []string{"source", "build", "artifacts"} {
		if err := os.Mkdir(filepath.Join(root, dir), 0700); err != nil {
			t.Fatal(err)
		}
	}
	newExecutor := func(spec processcontrol.Spec) (*fakeRunner, PreparedProcessExecutor) {
		process := &fakeProcess{output: make(chan processcontrol.Output), done: make(chan processcontrol.Result, 1)}
		process.done <- processcontrol.Result{}
		close(process.done)
		close(process.output)
		runner := &fakeRunner{process: process}
		return runner, PreparedProcessExecutor{
			Runner: runner, TaskID: testID, ServiceInstanceID: testID,
			ToolSHA256:  map[string]string{executable: digestTest([]byte("fixed-cmake")), compiler: digestTest([]byte("fixed-compiler"))},
			Plan:        func(context.Context, Stage, Roots) (processcontrol.Spec, error) { return spec, nil },
			RecordLease: func(context.Context, task.ProcessLease) error { return nil }, ReleaseLease: func(context.Context, task.ProcessLease) error { return nil },
		}
	}
	roots := Roots{Source: filepath.Join(root, "source"), Build: filepath.Join(root, "build"), Artifacts: filepath.Join(root, "artifacts")}
	spec := processcontrol.Spec{Executable: executable, LaunchPlan: []string{compiler}, Dir: roots.Build}
	runner, executor := newExecutor(spec)
	if _, err := executor.Execute(context.Background(), StageCompile, roots); err != nil || runner.prepared != 1 {
		t.Fatalf("pinned launch plan rejected: %v prepared=%d", err, runner.prepared)
	}

	unpinned := filepath.Join(root, "unknown.exe")
	if err := os.WriteFile(unpinned, []byte("unknown"), 0600); err != nil {
		t.Fatal(err)
	}
	spec.LaunchPlan = []string{unpinned}
	runner, executor = newExecutor(spec)
	if _, err := executor.Execute(context.Background(), StageCompile, roots); err == nil || runner.prepared != 0 {
		t.Fatalf("unpinned launch plan accepted: %v prepared=%d", err, runner.prepared)
	}
}

func TestPreparedExecutorStopsOutputFlood(t *testing.T) {
	root := t.TempDir()
	executable := filepath.Join(root, "cmake.exe")
	if err := os.WriteFile(executable, []byte("fixed-tool"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{"source", "build", "artifacts"} {
		if err := os.Mkdir(filepath.Join(root, dir), 0700); err != nil {
			t.Fatal(err)
		}
	}
	process := &fakeProcess{output: make(chan processcontrol.Output, 1), done: make(chan processcontrol.Result, 1)}
	process.output <- processcontrol.Output{Data: []byte(strings.Repeat("x", maxStageOutput+1))}
	runner := &fakeRunner{process: process}
	executor := PreparedProcessExecutor{Runner: runner, TaskID: testID, ServiceInstanceID: testID, ToolSHA256: map[string]string{executable: digestTest([]byte("fixed-tool"))}, Plan: func(context.Context, Stage, Roots) (processcontrol.Spec, error) {
		return processcontrol.Spec{Executable: executable, Dir: filepath.Join(root, "build")}, nil
	}, RecordLease: func(context.Context, task.ProcessLease) error { return nil }, ReleaseLease: func(context.Context, task.ProcessLease) error { return nil }}
	if _, err := executor.Execute(context.Background(), StageCompile, Roots{Source: filepath.Join(root, "source"), Build: filepath.Join(root, "build"), Artifacts: filepath.Join(root, "artifacts")}); err == nil {
		t.Fatal("output flood accepted")
	}
	if !reflect.DeepEqual(process.events, []string{"start", "terminate", "close"}) {
		t.Fatalf("process not terminated: %v", process.events)
	}
}

func TestPreparedExecutorAllowsBoundedCoverageExportAndCompactsReceiptOutput(t *testing.T) {
	root := t.TempDir()
	executable := filepath.Join(root, "llvm-cov.exe")
	if err := os.WriteFile(executable, []byte("fixed-tool"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{"source", "build", "artifacts"} {
		if err := os.Mkdir(filepath.Join(root, dir), 0700); err != nil {
			t.Fatal(err)
		}
	}
	raw := []byte(strings.Repeat("x", maxStageOutput+1))
	process := &fakeProcess{output: make(chan processcontrol.Output, 1), done: make(chan processcontrol.Result, 1)}
	process.output <- processcontrol.Output{Data: raw}
	process.done <- processcontrol.Result{}
	close(process.output)
	close(process.done)
	executor := PreparedProcessExecutor{
		Runner: &fakeRunner{process: process}, TaskID: testID, ServiceInstanceID: testID,
		ToolSHA256: map[string]string{executable: digestTest([]byte("fixed-tool"))},
		Plan: func(context.Context, Stage, Roots) (processcontrol.Spec, error) {
			return processcontrol.Spec{Executable: executable, Dir: filepath.Join(root, "artifacts")}, nil
		},
		Interpret: func(stage Stage, _ Roots, output []byte) (StageEvidence, error) {
			if stage != StageCoverage || !reflect.DeepEqual(output, raw) {
				return StageEvidence{}, ErrProcessRejected
			}
			return StageEvidence{Output: []byte("coverage-normalized"), CoverageJSON: []byte(`{"schemaVersion":"1.0"}`)}, nil
		},
		RecordLease: func(context.Context, task.ProcessLease) error { return nil }, ReleaseLease: func(context.Context, task.ProcessLease) error { return nil },
	}
	evidence, err := executor.Execute(context.Background(), StageCoverage, Roots{Source: filepath.Join(root, "source"), Build: filepath.Join(root, "build"), Artifacts: filepath.Join(root, "artifacts")})
	if err != nil || string(evidence.Output) != "coverage-normalized" || len(evidence.CoverageJSON) == 0 {
		t.Fatalf("coverage evidence=%+v error=%v", evidence, err)
	}
}

func TestPreparedExecutorRejectsMissingExitResult(t *testing.T) {
	root := t.TempDir()
	executable := filepath.Join(root, "cmake.exe")
	_ = os.WriteFile(executable, []byte("fixed-tool"), 0600)
	for _, dir := range []string{"source", "build", "artifacts"} {
		_ = os.Mkdir(filepath.Join(root, dir), 0700)
	}
	process := &fakeProcess{output: make(chan processcontrol.Output), done: make(chan processcontrol.Result)}
	close(process.output)
	close(process.done)
	executor := PreparedProcessExecutor{Runner: &fakeRunner{process: process}, TaskID: testID, ServiceInstanceID: testID, ToolSHA256: map[string]string{executable: digestTest([]byte("fixed-tool"))}, Plan: func(context.Context, Stage, Roots) (processcontrol.Spec, error) {
		return processcontrol.Spec{Executable: executable, Dir: filepath.Join(root, "build")}, nil
	}, RecordLease: func(context.Context, task.ProcessLease) error { return nil }, ReleaseLease: func(context.Context, task.ProcessLease) error { return nil }}
	if _, err := executor.Execute(context.Background(), StageCompile, Roots{Source: filepath.Join(root, "source"), Build: filepath.Join(root, "build"), Artifacts: filepath.Join(root, "artifacts")}); err == nil {
		t.Fatal("missing completion accepted")
	}
}

func TestPreparedExecutorRejectsSymlinkUnderBuildOrArtifactRoot(t *testing.T) {
	for _, kind := range []string{"build-dir", "artifact-env", "unreferenced-build"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			executable := filepath.Join(root, "cmake.exe")
			_ = os.WriteFile(executable, []byte("fixed-tool"), 0600)
			for _, dir := range []string{"source", "build", "artifacts"} {
				_ = os.Mkdir(filepath.Join(root, dir), 0700)
			}
			outside := t.TempDir()
			branch := "build"
			if kind == "artifact-env" {
				branch = "artifacts"
			}
			link := filepath.Join(root, branch, "escape")
			if err := os.Symlink(outside, link); err != nil {
				t.Skipf("symlink unavailable: %v", err)
			}
			spec := processcontrol.Spec{Executable: executable, Dir: filepath.Join(root, "build")}
			if kind == "build-dir" {
				spec.Dir = link
			}
			if kind == "artifact-env" {
				spec.Env = []string{"LLVM_PROFILE_FILE=" + filepath.Join(link, "profile.profraw")}
			}
			process := &fakeProcess{output: make(chan processcontrol.Output), done: make(chan processcontrol.Result, 1)}
			process.done <- processcontrol.Result{}
			close(process.done)
			close(process.output)
			runner := &fakeRunner{process: process}
			executor := PreparedProcessExecutor{Runner: runner, TaskID: testID, ServiceInstanceID: testID, ToolSHA256: map[string]string{executable: digestTest([]byte("fixed-tool"))}, Plan: func(context.Context, Stage, Roots) (processcontrol.Spec, error) { return spec, nil }, RecordLease: func(context.Context, task.ProcessLease) error { return nil }, ReleaseLease: func(context.Context, task.ProcessLease) error { return nil }}
			_, err := executor.Execute(context.Background(), StageCompile, Roots{Source: filepath.Join(root, "source"), Build: filepath.Join(root, "build"), Artifacts: filepath.Join(root, "artifacts")})
			if err == nil || runner.prepared != 0 {
				t.Fatalf("escaped owned root prepared: %v %d", err, runner.prepared)
			}
		})
	}
}
