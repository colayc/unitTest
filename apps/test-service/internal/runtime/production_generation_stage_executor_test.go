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
	process  processcontrol.Process
	prepared int
}

func (runner *productionValidationRunnerFixture) Prepare(context.Context, processcontrol.Spec, string, string) (processcontrol.Process, error) {
	runner.prepared++
	return runner.process, nil
}
func (*productionValidationRunnerFixture) Cleanup(context.Context, task.ProcessLease, time.Duration) error {
	return nil
}

type productionValidationPlanProviderFixture struct {
	candidateID string
	plan        productionValidationProcessPlan
}

func (fixture *productionValidationPlanProviderFixture) ResolveValidationProcessPlan(_ context.Context, candidateID, taskID string) (productionValidationProcessPlan, error) {
	if candidateID != fixture.candidateID || taskID != fixture.plan.taskID {
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
	provider := &productionValidationPlanProviderFixture{candidateID: roots.CandidateID, plan: productionValidationProcessPlan{
		taskID: roots.ProcessTaskID, serviceInstanceID: serviceID,
		tools:       map[string]string{tool: productionBytesDigest([]byte("fixed-cmake"))},
		specs:       map[testgenvalidate.Stage]processcontrol.Spec{testgenvalidate.StageConfigure: {Executable: tool, Dir: roots.Build}},
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
