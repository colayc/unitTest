//go:build !windows

package toolchain

import (
	"context"
	"sync/atomic"
	"testing"

	"unit-test-ide.local/test-service/internal/probe"
	"unit-test-ide.local/test-service/internal/workspace"
)

func TestNewWindowsAdaptersIsEmptyAndHasNoProbeSideEffectOffWindows(t *testing.T) {
	runner := &countingNonWindowsRunner{}
	adapters := NewWindowsAdapters(runner, []workspace.ToolchainConfig{{
		ID:                 "msvc",
		Family:             string(FamilyMSVC),
		InstallationID:     "visual-studio",
		ToolsetVersion:     "14.40.0",
		HostArchitecture:   "x64",
		TargetArchitecture: "x64",
	}})
	if len(adapters) != 0 {
		t.Fatalf("NewWindowsAdapters() = %#v, want empty", adapters)
	}
	if calls := runner.calls.Load(); calls != 0 {
		t.Fatalf("NewWindowsAdapters() made %d probe calls off Windows", calls)
	}
}

type countingNonWindowsRunner struct {
	calls atomic.Int32
}

func (runner *countingNonWindowsRunner) Run(
	context.Context,
	probe.Spec,
) (probe.Result, error) {
	runner.calls.Add(1)
	return probe.Result{}, nil
}
