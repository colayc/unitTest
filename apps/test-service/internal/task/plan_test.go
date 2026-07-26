package task_test

import (
	"errors"
	"fmt"
	"testing"

	"unit-test-ide.local/test-service/internal/task"
)

func TestValidatePlanRejectsUnsafeSpecs(t *testing.T) {
	boundary := fakeBoundary{executables: []string{"cmake"}, roots: []string{"src", "build"}}

	tests := []struct {
		name string
		plan task.ExecutionPlan
	}{
		{name: "empty executable", plan: planWith(func(step *task.ExecutionStep) { step.Process.Executable = "" })},
		{name: "boundary rejects executable", plan: planWith(func(step *task.ExecutionStep) { step.Process.Executable = "ninja" })},
		{name: "duplicate step ID", plan: task.ExecutionPlan{Version: 1, Steps: []task.ExecutionStep{validConfigureStep(), validConfigureStep()}}},
		{name: "NUL argument", plan: planWith(func(step *task.ExecutionStep) { step.Process.Args = []string{"ok\x00bad"} })},
		{name: "empty working directory", plan: planWith(func(step *task.ExecutionStep) { step.Process.Dir = "" })},
		{name: "working directory outside boundary", plan: planWith(func(step *task.ExecutionStep) { step.Process.Dir = "outside" })},
		{name: "unknown step kind", plan: planWith(func(step *task.ExecutionStep) { step.Kind = task.StepKind("deploy") })},
		{name: "service token environment key", plan: planWith(func(step *task.ExecutionStep) { step.Process.Env = []string{"UNIT_TEST_SERVICE_TOKEN=secret"} })},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := task.ValidatePlan(tt.plan, boundary); !errors.Is(err, task.ErrInvalidArgument) {
				t.Fatalf("ValidatePlan() error = %v, want ErrInvalidArgument", err)
			}
		})
	}
}

func TestValidatePlanAcceptsValidTwoStepPlan(t *testing.T) {
	plan := task.ExecutionPlan{Version: 1, Steps: []task.ExecutionStep{
		validConfigureStep(),
		{
			ID:   "build",
			Kind: task.StepBuild,
			Process: task.ProcessSpec{
				Executable: "cmake",
				Args:       []string{"--build", "build"},
				Dir:        "build",
			},
			Public: task.CommandSummary{Executable: "cmake", Args: []string{"--build", "<build>"}},
		},
	}}

	if err := task.ValidatePlan(plan, fakeBoundary{executables: []string{"cmake"}, roots: []string{"src", "build"}}); err != nil {
		t.Fatalf("ValidatePlan() error = %v", err)
	}
}

func TestFingerprintPlanChangesWhenExecutionFieldChanges(t *testing.T) {
	plan := task.ExecutionPlan{Version: 1, Steps: []task.ExecutionStep{validConfigureStep()}}
	changed := planWith(func(step *task.ExecutionStep) { step.Process.Env = []string{"CMAKE_GENERATOR=Ninja"} })

	if got, want := task.FingerprintPlan(plan), task.FingerprintPlan(plan); got != want {
		t.Fatalf("FingerprintPlan() = %q, want stable fingerprint %q", got, want)
	}
	if task.FingerprintPlan(plan) == task.FingerprintPlan(changed) {
		t.Fatal("FingerprintPlan() did not change after execution environment changed")
	}
}

func planWith(change func(*task.ExecutionStep)) task.ExecutionPlan {
	step := validConfigureStep()
	change(&step)
	return task.ExecutionPlan{Version: 1, Steps: []task.ExecutionStep{step}}
}

func validConfigureStep() task.ExecutionStep {
	return task.ExecutionStep{
		ID:   "configure",
		Kind: task.StepConfigure,
		Process: task.ProcessSpec{
			Executable: "cmake",
			Args:       []string{"-S", "src", "-B", "build"},
			Dir:        "src",
		},
		Public: task.CommandSummary{Executable: "cmake", Args: []string{"-S", "<workspace>", "-B", "<build>"}},
	}
}

type fakeBoundary struct {
	executables []string
	roots       []string
}

func (b fakeBoundary) ValidateExecutable(path string) error {
	for _, allowed := range b.executables {
		if path == allowed {
			return nil
		}
	}
	return fmt.Errorf("executable %q is not allowed", path)
}

func (b fakeBoundary) ValidateWorkingDirectory(path string) error {
	for _, root := range b.roots {
		if path == root {
			return nil
		}
	}
	return fmt.Errorf("working directory %q is not allowed", path)
}
