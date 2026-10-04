package runtime

import (
	"context"
	"reflect"
	"sync"

	"unit-test-ide.local/test-service/internal/cmake"
	"unit-test-ide.local/test-service/internal/task"
	"unit-test-ide.local/test-service/internal/testgenvalidate"
	"unit-test-ide.local/test-service/internal/workspace"
)

type productionValidationPlanRegistration struct {
	candidateID, processTaskID string
	target                     generationTarget
	build                      productionBuildSnapshot
	baseline                   []byte
}

type productionValidationStageCompiler interface {
	CompileValidationStage(context.Context, productionValidationPlanRegistration, testgenvalidate.Stage, testgenvalidate.Roots) (productionValidationProcessPlan, error)
}

type productionValidationPlanRegistry struct {
	compiler productionValidationStageCompiler
	mu       sync.Mutex
	records  map[string]productionValidationPlanRegistration
}

func newProductionValidationPlanRegistry(compiler productionValidationStageCompiler) (*productionValidationPlanRegistry, error) {
	if compiler == nil {
		return nil, task.ErrStorageUnavailable
	}
	return &productionValidationPlanRegistry{compiler: compiler, records: make(map[string]productionValidationPlanRegistration)}, nil
}

func (registry *productionValidationPlanRegistry) RegisterValidationPlan(registration productionValidationPlanRegistration) error {
	if registry == nil || registry.compiler == nil || !validProductionDigest(registration.candidateID) ||
		!validProductionObjectID(registration.processTaskID) || !registration.target.valid() ||
		!validProductionBuildSnapshot(registration.build, registration.target) || len(registration.baseline) > 32<<20 {
		return errProductionValidationUnavailable
	}
	registration = cloneProductionValidationPlanRegistration(registration)
	registry.mu.Lock()
	defer registry.mu.Unlock()
	if existing, ok := registry.records[registration.candidateID]; ok && !reflect.DeepEqual(existing, registration) {
		return errProductionValidationUnavailable
	}
	registry.records[registration.candidateID] = registration
	return nil
}

func (registry *productionValidationPlanRegistry) ResolveValidationProcessPlan(ctx context.Context, candidateID, taskID string, stage testgenvalidate.Stage, roots testgenvalidate.Roots) (productionValidationProcessPlan, error) {
	if registry == nil || registry.compiler == nil || ctx == nil || ctx.Err() != nil || !validProductionValidationStage(stage) ||
		!validProductionDigest(candidateID) || !validProductionObjectID(taskID) || roots.CandidateID != candidateID || roots.ProcessTaskID != taskID {
		return productionValidationProcessPlan{}, errProductionValidationUnavailable
	}
	registry.mu.Lock()
	registration, ok := registry.records[candidateID]
	registry.mu.Unlock()
	if !ok || registration.processTaskID != taskID {
		return productionValidationProcessPlan{}, errProductionValidationUnavailable
	}
	return registry.compiler.CompileValidationStage(ctx, cloneProductionValidationPlanRegistration(registration), stage, roots)
}

func (registry *productionValidationPlanRegistry) ReleaseValidationPlan(candidateID, taskID string) {
	if registry == nil {
		return
	}
	registry.mu.Lock()
	if existing, ok := registry.records[candidateID]; ok && existing.processTaskID == taskID {
		delete(registry.records, candidateID)
	}
	registry.mu.Unlock()
}

func validProductionValidationStage(stage testgenvalidate.Stage) bool {
	switch stage {
	case testgenvalidate.StageConfigure, testgenvalidate.StageCompile, testgenvalidate.StageDiscover,
		testgenvalidate.StageCandidate, testgenvalidate.StageSuite, testgenvalidate.StageCoverage:
		return true
	default:
		return false
	}
}

func cloneProductionValidationPlanRegistration(value productionValidationPlanRegistration) productionValidationPlanRegistration {
	value.target.analysis.Arguments = append([]string(nil), value.target.analysis.Arguments...)
	value.target.functions = append([]generationFunctionTarget(nil), value.target.functions...)
	value.build.project.Tests.Containers = append([]workspace.TestContainerMapping(nil), value.build.project.Tests.Containers...)
	value.build.toolchain.Environment = append([]string(nil), value.build.toolchain.Environment...)
	value.build.toolchain.Generators = append([]string(nil), value.build.toolchain.Generators...)
	value.build.targets = cmake.CloneTargets(value.build.targets)
	value.baseline = append([]byte(nil), value.baseline...)
	return value
}

var _ productionValidationProcessPlanProvider = (*productionValidationPlanRegistry)(nil)
