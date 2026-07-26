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
		name        string
		plan        task.ExecutionPlan
		boundary    task.ExecutionBoundary
		useBoundary bool
	}{
		{name: "invalid version", plan: task.ExecutionPlan{Version: 2, Steps: []task.ExecutionStep{validConfigureStep()}}},
		{name: "zero steps", plan: task.ExecutionPlan{Version: 1}},
		{name: "too many steps", plan: planWithSteps(9)},
		{name: "invalid step ID", plan: planWith(func(step *task.ExecutionStep) { step.ID = "configure step" })},
		{name: "empty executable", plan: planWith(func(step *task.ExecutionStep) { step.Process.Executable = "" })},
		{name: "NUL executable", plan: planWith(func(step *task.ExecutionStep) { step.Process.Executable = "cmake\x00bad" })},
		{name: "boundary rejects executable", plan: planWith(func(step *task.ExecutionStep) { step.Process.Executable = "ninja" })},
		{name: "duplicate step ID", plan: task.ExecutionPlan{Version: 1, Steps: []task.ExecutionStep{validConfigureStep(), validConfigureStep()}}},
		{name: "NUL argument", plan: planWith(func(step *task.ExecutionStep) { step.Process.Args = []string{"ok\x00bad"} })},
		{name: "empty working directory", plan: planWith(func(step *task.ExecutionStep) { step.Process.Dir = "" })},
		{name: "NUL working directory", plan: planWith(func(step *task.ExecutionStep) { step.Process.Dir = "src\x00bad" })},
		{name: "working directory outside boundary", plan: planWith(func(step *task.ExecutionStep) { step.Process.Dir = "outside" })},
		{name: "unknown step kind", plan: planWith(func(step *task.ExecutionStep) { step.Kind = task.StepKind("deploy") })},
		{name: "NUL environment", plan: planWith(func(step *task.ExecutionStep) { step.Process.Env = []string{"CMAKE_GENERATOR=Ni\x00nja"} })},
		{name: "invalid environment key", plan: planWith(func(step *task.ExecutionStep) { step.Process.Env = []string{"CMAKE-GENERATOR=Ninja"} })},
		{name: "service token environment key", plan: planWith(func(step *task.ExecutionStep) { step.Process.Env = []string{"UNIT_TEST_SERVICE_TOKEN=secret"} })},
		{name: "nil boundary", plan: planWith(func(*task.ExecutionStep) {}), useBoundary: true},
		{name: "typed nil boundary", plan: planWith(func(*task.ExecutionStep) {}), boundary: (*nilFakeBoundary)(nil), useBoundary: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			currentBoundary := tt.boundary
			if !tt.useBoundary {
				currentBoundary = boundary
			}
			if err := task.ValidatePlan(tt.plan, currentBoundary); !errors.Is(err, task.ErrInvalidArgument) {
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

func TestFingerprintPlanCoversExecutionFieldsAndExcludesNonExecutionFields(t *testing.T) {
	plan := task.ExecutionPlan{Version: 1, Steps: []task.ExecutionStep{validConfigureStep()}}
	if got, want := task.FingerprintPlan(plan), "24d3c4e47950028ec5c13edc3e78d3eaa8ebd63704c64df06ac07cab66905c49"; got != want {
		t.Fatalf("FingerprintPlan() = %q, want fixed digest %q", got, want)
	}

	executionChanges := []struct {
		name   string
		change func(*task.ExecutionPlan)
	}{
		{name: "version", change: func(value *task.ExecutionPlan) { value.Version = 2 }},
		{name: "step ID", change: func(value *task.ExecutionPlan) { value.Steps[0].ID = "other" }},
		{name: "step kind", change: func(value *task.ExecutionPlan) { value.Steps[0].Kind = task.StepBuild }},
		{name: "executable", change: func(value *task.ExecutionPlan) { value.Steps[0].Process.Executable = "ninja" }},
		{name: "arguments", change: func(value *task.ExecutionPlan) { value.Steps[0].Process.Args = []string{"--build", "build"} }},
		{name: "environment", change: func(value *task.ExecutionPlan) { value.Steps[0].Process.Env = []string{"CMAKE_GENERATOR=Ninja"} }},
		{name: "working directory", change: func(value *task.ExecutionPlan) { value.Steps[0].Process.Dir = "build" }},
	}
	for _, tt := range executionChanges {
		t.Run(tt.name, func(t *testing.T) {
			changed := plan
			changed.Steps = append([]task.ExecutionStep(nil), plan.Steps...)
			tt.change(&changed)
			if task.FingerprintPlan(plan) == task.FingerprintPlan(changed) {
				t.Fatalf("FingerprintPlan() did not change after %s changed", tt.name)
			}
		})
	}

	nonExecutionChange := plan
	nonExecutionChange.Fingerprint = "caller-controlled"
	nonExecutionChange.Steps = append([]task.ExecutionStep(nil), plan.Steps...)
	nonExecutionChange.Steps[0].Public = task.CommandSummary{Executable: "cmake", Args: []string{"<redacted>"}}
	if task.FingerprintPlan(plan) != task.FingerprintPlan(nonExecutionChange) {
		t.Fatal("FingerprintPlan() changed after non-execution fields changed")
	}
}

func planWith(change func(*task.ExecutionStep)) task.ExecutionPlan {
	step := validConfigureStep()
	change(&step)
	return task.ExecutionPlan{Version: 1, Steps: []task.ExecutionStep{step}}
}

func planWithSteps(count int) task.ExecutionPlan {
	steps := make([]task.ExecutionStep, count)
	for index := range steps {
		steps[index] = validConfigureStep()
		steps[index].ID = fmt.Sprintf("step%d", index)
	}
	return task.ExecutionPlan{Version: 1, Steps: steps}
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

type nilFakeBoundary struct{}

func (*nilFakeBoundary) ValidateExecutable(string) error       { return nil }
func (*nilFakeBoundary) ValidateWorkingDirectory(string) error { return nil }
