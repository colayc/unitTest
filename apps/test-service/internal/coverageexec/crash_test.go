package coverageexec

import (
	"runtime"
	"testing"

	"unit-test-ide.local/test-service/internal/task"
)

func TestInvocationOutcomeClassifiesPlatformCrashWithoutConfusingFailureOrTimeout(t *testing.T) {
	crashCode := -1
	if runtime.GOOS == "windows" {
		crashCode = int(uint32(0xc0000005))
	}
	crashed := invocationOutcomeFromChild(task.ProcessChildResult{
		ID: "crashed", ExitCode: crashCode,
	})
	failed := invocationOutcomeFromChild(task.ProcessChildResult{
		ID: "failed", ExitCode: 1,
	})
	timedOut := invocationOutcomeFromChild(task.ProcessChildResult{
		ID: "timed-out", ExitCode: crashCode, TimedOut: true,
	})
	if !crashed.Crashed || crashed.TimedOut {
		t.Fatalf("platform crash outcome = %#v", crashed)
	}
	if failed.Crashed || failed.TimedOut || failed.ExitCode != 1 {
		t.Fatalf("ordinary failure outcome = %#v", failed)
	}
	if timedOut.Crashed || !timedOut.TimedOut {
		t.Fatalf("timeout outcome = %#v", timedOut)
	}
}
