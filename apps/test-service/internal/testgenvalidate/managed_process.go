package testgenvalidate

import (
	"bytes"
	"context"

	"unit-test-ide.local/test-service/internal/processcontrol"
)

// PreparedSelectedRunner is the native adapter for an explicitly configured
// selected-output validator. It inherits executable-hash checks, closed
// environment, durable process leases, process-tree cancellation and bounded
// output from PreparedProcessExecutor. No instance is installed by default.
type PreparedSelectedRunner struct {
	Runner                    processcontrol.Runner
	TaskID, ServiceInstanceID string
	ToolSHA256                map[string]string
	RecordLease, ReleaseLease LeaseWriter
	// Interpret is product-owned; it may parse discovery/test counts from
	// bounded output, but cannot choose an executable or collector location.
	Interpret func(SelectedPhase, []byte) (SelectedStageResult, error)
}

func (runner PreparedSelectedRunner) Run(ctx context.Context, phase SelectedPhase, roots Roots, command SelectedCommand) (SelectedStageResult, error) {
	if ctx == nil || !validSelectedCommand(command, runner.ToolSHA256) || runner.Interpret == nil {
		return SelectedStageResult{}, ErrSelectedValidation
	}
	var dir string
	switch command.Dir {
	case SelectedSourceDir:
		dir = roots.Source
	case SelectedBuildDir:
		dir = roots.Build
	case SelectedArtifactsDir:
		dir = roots.Artifacts
	default:
		return SelectedStageResult{}, ErrSelectedValidation
	}
	executor := PreparedProcessExecutor{
		Runner: runner.Runner, TaskID: runner.TaskID, ServiceInstanceID: runner.ServiceInstanceID,
		ToolSHA256: runner.ToolSHA256, RecordLease: runner.RecordLease, ReleaseLease: runner.ReleaseLease,
		Plan: func(context.Context, Stage, Roots) (processcontrol.Spec, error) {
			return processcontrol.Spec{Executable: command.Executable, Args: append([]string(nil), command.Args...), Dir: dir}, nil
		},
	}
	evidence, err := executor.Execute(ctx, Stage(phase), roots)
	if err != nil {
		return SelectedStageResult{}, err
	}
	parsed, err := runner.Interpret(phase, bytes.Clone(evidence.Output))
	if err != nil || parsed.ExitCode != 0 || len(parsed.Output) != 0 || parsed.CollectorRelativePath != "" {
		return SelectedStageResult{}, ErrSelectedValidation
	}
	parsed.Output = evidence.Output
	if phase == SelectedCoveragePhase {
		parsed.CollectorRelativePath = "selected-coverage.json"
	}
	return parsed, nil
}
