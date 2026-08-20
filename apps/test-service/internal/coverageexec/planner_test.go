package coverageexec

import (
	"reflect"
	"testing"

	"unit-test-ide.local/test-service/internal/task"
)

func TestPlannerRewritesBuildStepsToClosedCoveragePhases(t *testing.T) {
	input := task.ExecutionPlan{Version: 1, Steps: []task.ExecutionStep{
		{
			ID: "configure", Kind: task.StepConfigure,
			Process: task.ProcessSpec{
				Executable: `C:\tools\cmake.exe`,
				Args:       []string{"-B", `C:\private\build`},
				Dir:        `C:\workspace`,
			},
			Public: task.CommandSummary{
				Executable: `C:\tools\cmake.exe`,
				Args:       []string{"-B", `C:\private\build`},
			},
		},
		{
			ID: "build", Kind: task.StepBuild,
			Process: task.ProcessSpec{
				Executable: `C:\tools\cmake.exe`,
				Args:       []string{"--build", `C:\private\build`},
				Dir:        `C:\private\build`,
			},
			Public: task.CommandSummary{
				Executable: "cmake.exe",
				Args:       []string{"--build", `C:\private\build`},
			},
		},
	}}

	got, err := rewriteBuildPlan(input)
	if err != nil {
		t.Fatal(err)
	}
	wantKinds := []task.StepKind{
		task.StepCoverageConfigure,
		task.StepCoverageBuild,
	}
	wantIDs := []string{"coverage-configure", "coverage-build"}
	for index := range got.Steps {
		if got.Steps[index].Kind != wantKinds[index] ||
			got.Steps[index].ID != wantIDs[index] {
			t.Fatalf("step %d = %s/%s", index, got.Steps[index].ID, got.Steps[index].Kind)
		}
		if got.Steps[index].Public.Executable != string(wantKinds[index]) ||
			len(got.Steps[index].Public.Args) != 0 {
			t.Fatalf("step %d public command leaked native input: %#v", index, got.Steps[index].Public)
		}
	}
	if !reflect.DeepEqual(got.Steps[0].Process, input.Steps[0].Process) ||
		!reflect.DeepEqual(got.Steps[1].Process, input.Steps[1].Process) {
		t.Fatal("planner changed runtime-only process specifications")
	}
	if got.Fingerprint == "" || got.Fingerprint != task.FingerprintPlan(got) {
		t.Fatal("planner did not produce a canonical fingerprint")
	}
}

func TestPlannerRejectsMissingConfigureOrUnexpectedBuildPhase(t *testing.T) {
	for _, plan := range []task.ExecutionPlan{
		{Version: 1, Steps: []task.ExecutionStep{{ID: "build", Kind: task.StepBuild}}},
		{Version: 1, Steps: []task.ExecutionStep{{ID: "configure", Kind: task.StepConfigure}, {ID: "test", Kind: task.StepTestRun}}},
	} {
		if _, err := rewriteBuildPlan(plan); err == nil {
			t.Fatalf("rewriteBuildPlan(%#v) succeeded", plan)
		}
	}
}

func TestPlannerBuildsTheOnlyAllowedCoveragePhaseOrder(t *testing.T) {
	buildPlan, err := rewriteBuildPlan(task.ExecutionPlan{Version: 1, Steps: []task.ExecutionStep{
		{ID: "configure", Kind: task.StepConfigure},
		{ID: "build", Kind: task.StepBuild},
	}})
	if err != nil {
		t.Fatal(err)
	}
	tests, _, err := rewriteTestSteps([]task.ExecutionStep{{
		ID: "test-wave-1", Kind: task.StepTestRun,
	}})
	if err != nil {
		t.Fatal(err)
	}
	collector, err := collectorSteps(
		task.ProcessSpec{Executable: "llvm-profdata", Dir: "profiles"},
		task.ProcessSpec{Executable: "llvm-cov", Dir: "profiles"},
	)
	if err != nil {
		t.Fatal(err)
	}
	steps := append([]task.ExecutionStep(nil), buildPlan.Steps...)
	steps = append(steps, tests...)
	steps = append(steps, collector...)
	steps = append(steps, reportActionStep(), publishActionStep())
	got := make([]task.StepKind, len(steps))
	for index := range steps {
		got[index] = steps[index].Kind
	}
	want := []task.StepKind{
		task.StepCoverageConfigure, task.StepCoverageBuild,
		task.StepCoverageTest, task.StepCoverageMerge,
		task.StepCoverageNormalize, task.StepCoverageReport,
		task.StepCoveragePublish,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("coverage phase order = %v, want %v", got, want)
	}
}
