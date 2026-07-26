package taskfixture

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"

	"unit-test-ide.local/test-service/internal/task"
)

func Run(ctx context.Context, scenario task.Scenario, stdout, stderr io.Writer) int {
	switch scenario {
	case task.ScenarioSuccess:
		return 0
	case task.ScenarioExitNonzero:
		fmt.Fprintln(stderr, "fixture exits with code 17")
		return 17
	case task.ScenarioEmitOutput:
		fmt.Fprintln(stdout, "fixture stdout")
		fmt.Fprintln(stderr, "fixture stderr")
		return 0
	case task.ScenarioHang:
		<-ctx.Done()
		return 0
	case task.ScenarioSpawnChild:
		return runChildFixture(ctx, stdout, stderr)
	default:
		fmt.Fprintln(stderr, "unknown fixture scenario")
		return 2
	}
}

func runChildFixture(ctx context.Context, stdout, stderr io.Writer) int {
	executable, err := os.Executable()
	if err != nil {
		fmt.Fprintln(stderr, "fixture executable is unavailable")
		return 1
	}
	child := exec.CommandContext(ctx, executable, "--task-fixture-child")
	child.Stdout, child.Stderr = stdout, stderr
	if err := child.Start(); err != nil {
		fmt.Fprintln(stderr, "fixture child could not start")
		return 1
	}
	fmt.Fprintf(stdout, "CHILD_PID=%d\n", child.Process.Pid)
	if err := child.Wait(); err != nil && ctx.Err() == nil {
		return 1
	}
	return 0
}
