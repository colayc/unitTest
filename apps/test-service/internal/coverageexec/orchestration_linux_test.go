//go:build linux

package coverageexec

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"unit-test-ide.local/test-service/internal/coveragenormalize"
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

func TestLinuxLLVMCollectorOutputBudgetCoversStdoutAndStderr(t *testing.T) {
	limit := coveragenormalize.DefaultLimits().MaxInputBytes
	for _, stream := range []string{"stdout", "stderr"} {
		t.Run(stream, func(t *testing.T) {
			execution := &execution{taskID: strings.Repeat("a", 32), collectorOutputBytes: limit - 2}
			err := execution.ObserveOutput(context.Background(), task.Task{ID: execution.taskID},
				task.ExecutionStep{Kind: task.StepCoverageNormalize}, task.ProcessOutput{Stream: stream, Data: []byte("abc")})
			if !errors.Is(err, coveragenormalize.ErrLimitExceeded) {
				t.Fatalf("%s over budget returned %v", stream, err)
			}
		})
	}
}

func TestLinuxLLVMCollectorPlanKeepsPrivatePathsOutOfPublicSteps(t *testing.T) {
	private := "/private/workspace/secret/coverage.profdata"
	steps, err := collectorSteps(CollectionPlan{Aggregate: task.ProcessSpec{Executable: "/private/llvm-profdata", Args: []string{"merge", private}},
		Normalize: &task.ProcessSpec{Executable: "/private/llvm-cov", Args: []string{"export", private}}})
	if err != nil {
		t.Fatal(err)
	}
	for _, step := range steps {
		if strings.Contains(step.Public.Executable, "/private") || strings.Contains(strings.Join(step.Public.Args, " "), private) {
			t.Fatalf("private collector path leaked in public step: %#v", step.Public)
		}
	}
}

func TestLinuxCancellationCleanupRemovesOwnedRawProfiles(t *testing.T) {
	root := t.TempDir()
	owner, _, profileRoot, _, err := allocateExecutionRoots(root, strings.Repeat("b", 32))
	if err != nil {
		t.Fatal(err)
	}
	profile := filepath.Join(profileRoot, "p-000001-i-000001-42-unit.profraw")
	if err := os.WriteFile(profile, []byte("private raw profile"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := owner.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(profile); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("cancelled task retained raw profile: %v", err)
	}
}
