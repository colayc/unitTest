package runtime

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"unit-test-ide.local/test-service/internal/processcontrol"
	"unit-test-ide.local/test-service/internal/task"
	"unit-test-ide.local/test-service/internal/testgenvalidate"
)

type productionValidationProcessFixture struct {
	lease  task.ProcessLease
	output chan processcontrol.Output
	done   chan processcontrol.Result
}

func (process *productionValidationProcessFixture) Lease() task.ProcessLease { return process.lease }
func (*productionValidationProcessFixture) Start(context.Context) error      { return nil }
func (process *productionValidationProcessFixture) Output() <-chan processcontrol.Output {
	return process.output
}
func (process *productionValidationProcessFixture) Done() <-chan processcontrol.Result {
	return process.done
}
func (*productionValidationProcessFixture) Terminate(context.Context, time.Duration) error {
	return nil
}
func (*productionValidationProcessFixture) Close(context.Context) error { return nil }

type productionValidationRunnerFixture struct {
	process   processcontrol.Process
	processes []processcontrol.Process
	prepared  int
}

func (runner *productionValidationRunnerFixture) Prepare(context.Context, processcontrol.Spec, string, string) (processcontrol.Process, error) {
	runner.prepared++
	if len(runner.processes) != 0 {
		result := runner.processes[0]
		runner.processes = runner.processes[1:]
		return result, nil
	}
	return runner.process, nil
}
func (*productionValidationRunnerFixture) Cleanup(context.Context, task.ProcessLease, time.Duration) error {
	return nil
}

type productionValidationPlanProviderFixture struct {
	candidateID string
	roots       testgenvalidate.Roots
	plan        productionValidationProcessPlan
}

func (fixture *productionValidationPlanProviderFixture) ResolveValidationProcessPlan(_ context.Context, candidateID, taskID string, stage testgenvalidate.Stage, roots testgenvalidate.Roots) (productionValidationProcessPlan, error) {
	if candidateID != fixture.candidateID || taskID != fixture.plan.taskID || roots != fixture.roots {
		return productionValidationProcessPlan{}, errProductionValidationUnavailable
	}
	if _, ok := fixture.plan.specs[stage]; !ok && len(fixture.plan.sequences[stage]) == 0 {
		return productionValidationProcessPlan{}, errProductionValidationUnavailable
	}
	return fixture.plan, nil
}

func TestProductionValidationStageExecutorUsesCandidateBoundPlan(t *testing.T) {
	root := t.TempDir()
	tool := filepath.Join(root, "cmake.exe")
	if err := os.WriteFile(tool, []byte("fixed-cmake"), 0600); err != nil {
		t.Fatal(err)
	}
	roots := testgenvalidate.Roots{
		Source: filepath.Join(root, "source"), Build: filepath.Join(root, "build"), Artifacts: filepath.Join(root, "artifacts"),
		TaskID: strings.Repeat("1", 64), ProcessTaskID: strings.Repeat("f", 32), CandidateID: strings.Repeat("2", 64),
	}
	for _, path := range []string{roots.Source, roots.Build, roots.Artifacts} {
		if err := os.Mkdir(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	serviceID := strings.Repeat("3", 32)
	process := &productionValidationProcessFixture{
		lease:  task.ProcessLease{TaskID: roots.ProcessTaskID, ServiceInstanceID: serviceID, HostPID: 42, HostStartIdentity: "start"},
		output: make(chan processcontrol.Output), done: make(chan processcontrol.Result, 1),
	}
	process.done <- processcontrol.Result{}
	close(process.output)
	close(process.done)
	runner := &productionValidationRunnerFixture{process: process}
	provider := &productionValidationPlanProviderFixture{candidateID: roots.CandidateID, roots: roots, plan: productionValidationProcessPlan{
		taskID: roots.ProcessTaskID, serviceInstanceID: serviceID,
		tools:              map[string]string{tool: productionBytesDigest([]byte("fixed-cmake"))},
		allowedEnvironment: []string{"PATH=C:\\toolchain\\bin"},
		allowedEnvUnset:    []string{"CL"},
		specs: map[testgenvalidate.Stage]processcontrol.Spec{testgenvalidate.StageConfigure: {
			Executable: tool, Dir: roots.Build, Env: []string{"PATH=C:\\toolchain\\bin"}, EnvUnset: []string{"CL"},
		}},
		recordLease: func(context.Context, task.ProcessLease) error { return nil }, releaseLease: func(context.Context, task.ProcessLease) error { return nil },
	}}
	executor, err := newProductionValidationStageExecutor(runner, provider)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := executor.Execute(context.Background(), testgenvalidate.StageConfigure, roots); err != nil || runner.prepared != 1 {
		t.Fatalf("Execute() error = %v prepared=%d", err, runner.prepared)
	}

	roots.CandidateID = strings.Repeat("4", 64)
	if _, err := executor.Execute(context.Background(), testgenvalidate.StageConfigure, roots); err == nil || runner.prepared != 1 {
		t.Fatalf("unbound candidate executed: %v prepared=%d", err, runner.prepared)
	}
}

func TestProductionValidationStageExecutorRunsBoundSequenceAndInterpretsFinalOutput(t *testing.T) {
	root := t.TempDir()
	tool := filepath.Join(root, "llvm.exe")
	if err := os.WriteFile(tool, []byte("fixed-llvm"), 0600); err != nil {
		t.Fatal(err)
	}
	roots := testgenvalidate.Roots{
		Source: filepath.Join(root, "source"), Build: filepath.Join(root, "build"), Artifacts: filepath.Join(root, "artifacts"),
		TaskID: strings.Repeat("1", 64), ProcessTaskID: strings.Repeat("f", 32), CandidateID: strings.Repeat("2", 64),
	}
	for _, path := range []string{roots.Source, roots.Build, roots.Artifacts} {
		if err := os.Mkdir(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	serviceID := strings.Repeat("3", 32)
	newProcess := func(output string) processcontrol.Process {
		value := &productionValidationProcessFixture{
			lease:  task.ProcessLease{TaskID: roots.ProcessTaskID, ServiceInstanceID: serviceID, HostPID: 42, HostStartIdentity: "start"},
			output: make(chan processcontrol.Output, 1), done: make(chan processcontrol.Result, 1),
		}
		value.output <- processcontrol.Output{Data: []byte(output)}
		value.done <- processcontrol.Result{}
		close(value.output)
		close(value.done)
		return value
	}
	runner := &productionValidationRunnerFixture{processes: []processcontrol.Process{newProcess("merge"), newProcess("export")}}
	provider := &productionValidationPlanProviderFixture{candidateID: roots.CandidateID, roots: roots, plan: productionValidationProcessPlan{
		taskID: roots.ProcessTaskID, serviceInstanceID: serviceID,
		tools: map[string]string{tool: productionBytesDigest([]byte("fixed-llvm"))},
		sequences: map[testgenvalidate.Stage][]processcontrol.Spec{testgenvalidate.StageCoverage: {
			{Executable: tool, Dir: roots.Artifacts}, {Executable: tool, Dir: roots.Artifacts},
		}},
		interpret: func(stage testgenvalidate.Stage, gotRoots testgenvalidate.Roots, output []byte) (testgenvalidate.StageEvidence, error) {
			if stage != testgenvalidate.StageCoverage || gotRoots != roots || string(output) != "export" {
				return testgenvalidate.StageEvidence{}, errProductionValidationUnavailable
			}
			return testgenvalidate.StageEvidence{CoverageJSON: []byte(`{"schemaVersion":"1.0"}`)}, nil
		},
		recordLease: func(context.Context, task.ProcessLease) error { return nil }, releaseLease: func(context.Context, task.ProcessLease) error { return nil },
	}}
	executor, err := newProductionValidationStageExecutor(runner, provider)
	if err != nil {
		t.Fatal(err)
	}
	evidence, err := executor.Execute(context.Background(), testgenvalidate.StageCoverage, roots)
	if err != nil || runner.prepared != 2 || string(evidence.Output) != "export" || string(evidence.CoverageJSON) != `{"schemaVersion":"1.0"}` {
		t.Fatalf("sequence evidence=%+v error=%v prepared=%d", evidence, err, runner.prepared)
	}
}
