//go:build linux

package coverageexec

import (
	"testing"

	"unit-test-ide.local/test-service/internal/task"
)

// GCC collectors are deliberately file-backed: the normalize step is a
// closed service action and therefore cannot depend on gcovr stdout.
func TestLinuxGCCPlannerUsesPinnedOutputServiceNormalize(t *testing.T) {
	steps, err := collectorSteps(CollectionPlan{Aggregate: task.ProcessSpec{
		Executable: "/trusted/gcovr", Dir: "/private/collector/gcovr",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(steps) != 2 || steps[0].Kind != task.StepCoverageMerge ||
		steps[1].Kind != task.StepCoverageNormalize ||
		steps[1].Action != task.ServiceActionCoverageNormalize ||
		steps[1].Process.Executable != "" {
		t.Fatalf("GCC coverage continuation = %#v", steps)
	}
}
