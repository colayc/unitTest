package coverageexec

import (
	"encoding/json"

	"unit-test-ide.local/test-service/internal/task"
)

func rewriteBuildPlan(input task.ExecutionPlan) (task.ExecutionPlan, error) {
	if input.Version != 1 || len(input.Steps) != 2 ||
		input.Steps[0].Kind != task.StepConfigure ||
		input.Steps[1].Kind != task.StepBuild {
		return task.ExecutionPlan{}, task.ErrInvalidArgument
	}
	result := clonePlan(input)
	result.Steps[0].ID = "coverage-configure"
	result.Steps[0].Kind = task.StepCoverageConfigure
	result.Steps[1].ID = "coverage-build"
	result.Steps[1].Kind = task.StepCoverageBuild
	for index := range result.Steps {
		result.Steps[index].Public = task.CommandSummary{
			Executable: string(result.Steps[index].Kind),
		}
		// Coverage tasks never publish compiler diagnostics: their public
		// protocol is intentionally path-free, while raw build diagnostics may
		// contain absolute toolchain or workspace paths.
		result.Steps[index].DiagnosticParser = nil
	}
	result.Fingerprint = task.FingerprintPlan(result)
	return result, nil
}

func rewriteTestSteps(input []task.ExecutionStep) ([]task.ExecutionStep, map[string]task.ExecutionStep, error) {
	if len(input) == 0 || len(input) > 256 {
		return nil, nil, task.ErrInvalidArgument
	}
	result := make([]task.ExecutionStep, len(input))
	originals := make(map[string]task.ExecutionStep, len(input))
	for index, source := range input {
		if source.ID == "" || source.Kind != task.StepTestRun ||
			source.Action != "" {
			return nil, nil, task.ErrInvalidArgument
		}
		cloned := cloneStep(source)
		cloned.Kind = task.StepCoverageTest
		cloned.Public = task.CommandSummary{
			Executable: string(task.StepCoverageTest),
		}
		if _, duplicate := originals[source.ID]; duplicate {
			return nil, nil, task.ErrInvalidArgument
		}
		originals[source.ID] = cloneStep(source)
		result[index] = cloned
	}
	return result, originals, nil
}

func collectorSteps(merge, export task.ProcessSpec) ([]task.ExecutionStep, error) {
	if merge.Executable == "" || export.Executable == "" ||
		len(merge.Batch) != 0 || len(export.Batch) != 0 {
		return nil, task.ErrInvalidArgument
	}
	return []task.ExecutionStep{
		{
			ID: "coverage-merge", Kind: task.StepCoverageMerge,
			Process: cloneProcess(merge),
			Public:  task.CommandSummary{Executable: string(task.StepCoverageMerge)},
		},
		{
			ID: "coverage-normalize", Kind: task.StepCoverageNormalize,
			Process: cloneProcess(export),
			Public:  task.CommandSummary{Executable: string(task.StepCoverageNormalize)},
		},
	}, nil
}

func reportActionStep() task.ExecutionStep {
	return task.ExecutionStep{
		ID: "coverage-report", Kind: task.StepCoverageReport,
		Action: task.ServiceActionCoverageReport,
		Public: task.CommandSummary{Executable: string(task.StepCoverageReport)},
	}
}

func publishActionStep() task.ExecutionStep {
	return task.ExecutionStep{
		ID: "coverage-publish", Kind: task.StepCoveragePublish,
		Action: task.ServiceActionCoveragePublish,
		Public: task.CommandSummary{Executable: string(task.StepCoveragePublish)},
	}
}

func clonePlan(input task.ExecutionPlan) task.ExecutionPlan {
	result := input
	result.Steps = make([]task.ExecutionStep, len(input.Steps))
	for index, step := range input.Steps {
		result.Steps[index] = cloneStep(step)
	}
	return result
}

func cloneStep(input task.ExecutionStep) task.ExecutionStep {
	result := input
	result.Process = cloneProcess(input.Process)
	result.Public.Args = append([]string(nil), input.Public.Args...)
	result.State = append(json.RawMessage(nil), input.State...)
	return result
}

func cloneProcess(input task.ProcessSpec) task.ProcessSpec {
	result := input
	result.Args = append([]string(nil), input.Args...)
	result.Env = append([]string(nil), input.Env...)
	result.EnvUnset = append([]string(nil), input.EnvUnset...)
	if input.Batch != nil {
		result.Batch = make([]task.ProcessBatchItem, len(input.Batch))
	}
	for index, item := range input.Batch {
		result.Batch[index] = item
		result.Batch[index].Args = append([]string(nil), item.Args...)
		result.Batch[index].Env = append([]string(nil), item.Env...)
		result.Batch[index].EnvUnset = append([]string(nil), item.EnvUnset...)
	}
	return result
}
