package runtime

import (
	"context"

	"unit-test-ide.local/test-service/internal/cmake"
	"unit-test-ide.local/test-service/internal/processcontrol"
	"unit-test-ide.local/test-service/internal/task"
	"unit-test-ide.local/test-service/internal/testgenvalidate"
)

type productionValidationProcessPlan struct {
	taskID, serviceInstanceID string
	tools                     map[string]string
	allowedEnvironment        []string
	allowedEnvUnset           []string
	specs                     map[testgenvalidate.Stage]processcontrol.Spec
	interpret                 testgenvalidate.StageInterpreter
	recordLease, releaseLease testgenvalidate.LeaseWriter
}

type productionValidationProcessPlanProvider interface {
	ResolveValidationProcessPlan(context.Context, string, string) (productionValidationProcessPlan, error)
}

type productionValidationStageExecutor struct {
	runner   processcontrol.Runner
	provider productionValidationProcessPlanProvider
}

func newProductionValidationStageExecutor(runner processcontrol.Runner, provider productionValidationProcessPlanProvider) (*productionValidationStageExecutor, error) {
	if runner == nil || provider == nil {
		return nil, task.ErrStorageUnavailable
	}
	return &productionValidationStageExecutor{runner: runner, provider: provider}, nil
}

func (executor *productionValidationStageExecutor) Execute(ctx context.Context, stage testgenvalidate.Stage, roots testgenvalidate.Roots) (testgenvalidate.StageEvidence, error) {
	if executor == nil || ctx == nil || ctx.Err() != nil || !validProductionDigest(roots.TaskID) ||
		!validProductionObjectID(roots.ProcessTaskID) || !validProductionDigest(roots.CandidateID) {
		return testgenvalidate.StageEvidence{}, errProductionValidationUnavailable
	}
	plan, err := executor.provider.ResolveValidationProcessPlan(ctx, roots.CandidateID, roots.ProcessTaskID)
	if err != nil || plan.taskID != roots.ProcessTaskID || !validProductionObjectID(plan.taskID) || !validProductionObjectID(plan.serviceInstanceID) ||
		len(plan.tools) == 0 || plan.recordLease == nil || plan.releaseLease == nil {
		return testgenvalidate.StageEvidence{}, errProductionValidationUnavailable
	}
	spec, ok := plan.specs[stage]
	if !ok {
		return testgenvalidate.StageEvidence{}, errProductionValidationUnavailable
	}
	tools := make(map[string]string, len(plan.tools))
	for path, digest := range plan.tools {
		tools[path] = digest
	}
	spec = cloneProductionValidationProcessSpec(spec)
	prepared := testgenvalidate.PreparedProcessExecutor{
		Runner: executor.runner, TaskID: plan.taskID, ServiceInstanceID: plan.serviceInstanceID,
		ToolSHA256: tools, AllowedEnvironment: append([]string(nil), plan.allowedEnvironment...),
		AllowedEnvUnset: append([]string(nil), plan.allowedEnvUnset...),
		RecordLease:     plan.recordLease, ReleaseLease: plan.releaseLease, Interpret: plan.interpret,
		Plan: func(context.Context, testgenvalidate.Stage, testgenvalidate.Roots) (processcontrol.Spec, error) {
			return spec, nil
		},
	}
	return prepared.Execute(ctx, stage, roots)
}

func cloneProductionValidationProcessSpec(value processcontrol.Spec) processcontrol.Spec {
	value.Args = append([]string(nil), value.Args...)
	value.Env = append([]string(nil), value.Env...)
	value.EnvUnset = append([]string(nil), value.EnvUnset...)
	value.LaunchPlan = append([]string(nil), value.LaunchPlan...)
	value.LaunchInputs = append([]cmake.FingerprintFile(nil), value.LaunchInputs...)
	value.Batch = append([]processcontrol.BatchItem(nil), value.Batch...)
	return value
}

var _ testgenvalidate.StageExecutor = (*productionValidationStageExecutor)(nil)
