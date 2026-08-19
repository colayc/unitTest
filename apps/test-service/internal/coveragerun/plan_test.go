package coveragerun

import (
	"testing"

	"unit-test-ide.local/test-service/internal/coveragedomain"
)

func TestBuildPlanUsesFixedStepsForEachCollector(t *testing.T) {
	for _, test := range []struct {
		name  string
		input coveragedomain.ToolchainSnapshot
		steps []StepKind
	}{
		{
			name: "gcc gcovr",
			input: coveragedomain.ToolchainSnapshot{
				Platform: coveragedomain.PlatformLinux, Architecture: coveragedomain.ArchitectureX64,
				Compiler:  coveragedomain.CompilerSnapshot{Family: coveragedomain.CompilerFamilyGCC, Version: "15.1.0"},
				Driver:    coveragedomain.DriverSnapshot{Name: coveragedomain.DriverGCov, Version: "15.1.0"},
				Collector: coveragedomain.CollectorSnapshot{Name: coveragedomain.CollectorGCovr, Version: "8.6"},
			},
			steps: []StepKind{StepCollectProfiles, StepGenerateReport},
		},
		{
			name: "llvm",
			input: coveragedomain.ToolchainSnapshot{
				Platform: coveragedomain.PlatformLinux, Architecture: coveragedomain.ArchitectureX64,
				Compiler:  coveragedomain.CompilerSnapshot{Family: coveragedomain.CompilerFamilyClang, Version: "18.1.8"},
				Driver:    coveragedomain.DriverSnapshot{Name: coveragedomain.DriverLLVMCov, Version: "18.1.8"},
				Collector: coveragedomain.CollectorSnapshot{Name: coveragedomain.CollectorLLVMCov, Version: "18.1.8"},
			},
			steps: []StepKind{StepCollectProfiles, StepMergeProfiles, StepGenerateReport},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			plan, err := BuildPlan(test.input)
			if err != nil {
				t.Fatal(err)
			}
			if len(plan.Steps) != len(test.steps) {
				t.Fatalf("steps = %#v", plan.Steps)
			}
			for index, want := range test.steps {
				if plan.Steps[index] != want {
					t.Fatalf("steps = %#v, want %#v", plan.Steps, test.steps)
				}
			}
			other, err := BuildPlan(test.input)
			if err != nil {
				t.Fatal(err)
			}
			plan.Steps[0] = "mutated"
			if other.Steps[0] == StepKind("mutated") {
				t.Fatal("plan steps unexpectedly aliased")
			}
		})
	}
}

func TestBuildPlanRejectsUnsupportedCollector(t *testing.T) {
	_, err := BuildPlan(coveragedomain.ToolchainSnapshot{})
	if err != ErrUnsupportedToolchain {
		t.Fatalf("error = %v", err)
	}
}
