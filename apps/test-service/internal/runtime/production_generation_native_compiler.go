package runtime

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"sync"

	"unit-test-ide.local/test-service/internal/cmake"
	"unit-test-ide.local/test-service/internal/ctest"
	"unit-test-ide.local/test-service/internal/processcontrol"
	"unit-test-ide.local/test-service/internal/task"
	"unit-test-ide.local/test-service/internal/testgenvalidate"
)

type productionGenerationProcessLeaseStore interface {
	PutGenerationProcessLease(context.Context, task.ProcessLease) error
	ReleaseGenerationProcessLease(context.Context, task.ProcessLease) error
}

type productionValidationBuildPreparation struct {
	configure, compile processcontrol.Spec
	profile            cmake.BuildProfile
	testBinary         string
	environment        []string
	tools              map[string]string
}

type productionValidationCoveragePreparation struct {
	specs              []processcontrol.Spec
	tools              map[string]string
	environment, unset []string
	interpret          testgenvalidate.StageInterpreter
}

type productionValidationBuildPrepareFunc func(context.Context, productionValidationPlanRegistration, testgenvalidate.Roots) (productionValidationBuildPreparation, error)
type productionValidationCoveragePrepareFunc func(context.Context, productionValidationPlanRegistration, productionValidationBuildPreparation, testgenvalidate.Roots) (productionValidationCoveragePreparation, error)

type productionValidationNativeCompilerConfig struct {
	installation      cmake.Installation
	platform          string
	serviceInstanceID string
	leases            productionGenerationProcessLeaseStore
	prepareBuild      productionValidationBuildPrepareFunc
	prepareCoverage   productionValidationCoveragePrepareFunc
}

type productionValidationNativeState struct {
	registration productionValidationPlanRegistration
	roots        testgenvalidate.Roots
	build        productionValidationBuildPreparation
}

type productionValidationNativeCompiler struct {
	config productionValidationNativeCompilerConfig
	runner *ctest.Runner
	mu     sync.Mutex
	states map[string]productionValidationNativeState
}

func newProductionValidationNativeCompiler(config productionValidationNativeCompilerConfig) (*productionValidationNativeCompiler, error) {
	if config.prepareBuild == nil {
		config.prepareBuild = productionValidationBuildFactory(config.installation)
	}
	if config.prepareCoverage == nil && (config.platform == "windows" || config.platform == "linux") {
		config.prepareCoverage = productionValidationCoverageFactory(config.platform)
	}
	if !validProductionObjectID(config.serviceInstanceID) || config.leases == nil || config.prepareBuild == nil || config.prepareCoverage == nil {
		return nil, task.ErrStorageUnavailable
	}
	runner, err := ctest.NewRunner(config.installation)
	if err != nil {
		return nil, task.ErrStorageUnavailable
	}
	return &productionValidationNativeCompiler{config: config, runner: runner, states: make(map[string]productionValidationNativeState)}, nil
}

func (compiler *productionValidationNativeCompiler) CompileValidationStage(ctx context.Context, registration productionValidationPlanRegistration, stage testgenvalidate.Stage, roots testgenvalidate.Roots) (productionValidationProcessPlan, error) {
	if compiler == nil || ctx == nil || ctx.Err() != nil || !validProductionValidationStage(stage) ||
		registration.candidateID != roots.CandidateID || registration.processTaskID != roots.ProcessTaskID {
		return productionValidationProcessPlan{}, errProductionValidationUnavailable
	}
	if stage == testgenvalidate.StageConfigure {
		prepared, err := compiler.config.prepareBuild(ctx, registration, roots)
		if err != nil || !validProductionValidationBuildPreparation(prepared, registration, roots) {
			return productionValidationProcessPlan{}, errProductionValidationUnavailable
		}
		state := productionValidationNativeState{registration: cloneProductionValidationPlanRegistration(registration), roots: roots, build: cloneProductionValidationBuildPreparation(prepared)}
		compiler.mu.Lock()
		if existing, ok := compiler.states[registration.candidateID]; ok && !reflect.DeepEqual(existing, state) {
			compiler.mu.Unlock()
			return productionValidationProcessPlan{}, errProductionValidationUnavailable
		}
		compiler.states[registration.candidateID] = state
		compiler.mu.Unlock()
		return compiler.singleProcessPlan(registration, stage, prepared.configure, prepared.environment, nil, prepared.tools, nil), nil
	}
	compiler.mu.Lock()
	state, ok := compiler.states[registration.candidateID]
	compiler.mu.Unlock()
	if !ok || !reflect.DeepEqual(state.registration, registration) || state.roots != roots {
		return productionValidationProcessPlan{}, errProductionValidationUnavailable
	}
	switch stage {
	case testgenvalidate.StageCompile:
		return compiler.singleProcessPlan(registration, stage, state.build.compile, state.build.environment, nil, state.build.tools, nil), nil
	case testgenvalidate.StageDiscover:
		return compiler.discoveryPlan(registration, roots, state.build)
	case testgenvalidate.StageCandidate, testgenvalidate.StageSuite:
		return compiler.testPlan(registration, stage, roots, state.build)
	case testgenvalidate.StageCoverage:
		coverage, err := compiler.config.prepareCoverage(ctx, registration, state.build, roots)
		if err != nil || len(coverage.specs) != 2 || coverage.interpret == nil || len(coverage.tools) == 0 {
			return productionValidationProcessPlan{}, errProductionValidationUnavailable
		}
		return compiler.sequenceProcessPlan(registration, stage, coverage.specs, coverage.environment, coverage.unset, coverage.tools, coverage.interpret), nil
	default:
		return productionValidationProcessPlan{}, errProductionValidationUnavailable
	}
}

func (compiler *productionValidationNativeCompiler) discoveryPlan(registration productionValidationPlanRegistration, roots testgenvalidate.Roots, prepared productionValidationBuildPreparation) (productionValidationProcessPlan, error) {
	ctestName, ok := productionValidationExecutableName(registration)
	if !ok {
		return productionValidationProcessPlan{}, errProductionValidationUnavailable
	}
	step, err := compiler.runner.ShowOnlyPlan(prepared.profile)
	if err != nil {
		return productionValidationProcessPlan{}, errProductionValidationUnavailable
	}
	spec := productionProcessSpec(step.Process)
	spec.Env = append([]string(nil), prepared.environment...)
	tools := cloneProductionValidationTools(prepared.tools)
	if err := addProductionValidationTool(tools, spec.Executable); err != nil {
		return productionValidationProcessPlan{}, errProductionValidationUnavailable
	}
	interpret := func(stage testgenvalidate.Stage, gotRoots testgenvalidate.Roots, output []byte) (testgenvalidate.StageEvidence, error) {
		if stage != testgenvalidate.StageDiscover || gotRoots != roots {
			return testgenvalidate.StageEvidence{}, errProductionValidationUnavailable
		}
		snapshot, err := ctest.ParseShowOnlyJSON(output, ctest.DefaultLimits())
		if err != nil {
			return testgenvalidate.StageEvidence{}, errProductionValidationUnavailable
		}
		matches := 0
		for _, raw := range snapshot.Tests {
			if raw.Name == ctestName && len(raw.Command) > 0 && sameProductionPath(raw.Command[0], prepared.testBinary) {
				matches++
			}
		}
		if matches != 1 {
			return testgenvalidate.StageEvidence{}, errProductionValidationUnavailable
		}
		return testgenvalidate.StageEvidence{DiscoveredCaseIDs: []string{registration.candidateID}}, nil
	}
	return compiler.singleProcessPlan(registration, testgenvalidate.StageDiscover, spec, prepared.environment, nil, tools, interpret), nil
}

func (compiler *productionValidationNativeCompiler) testPlan(registration productionValidationPlanRegistration, stage testgenvalidate.Stage, roots testgenvalidate.Roots, prepared productionValidationBuildPreparation) (productionValidationProcessPlan, error) {
	if stage != testgenvalidate.StageCandidate && stage != testgenvalidate.StageSuite {
		return productionValidationProcessPlan{}, errProductionValidationUnavailable
	}
	if err := os.MkdirAll(filepath.Join(roots.Artifacts, "profiles"), 0700); err != nil {
		return productionValidationProcessPlan{}, errProductionValidationUnavailable
	}
	ctestName, ok := productionValidationExecutableName(registration)
	if !ok {
		return productionValidationProcessPlan{}, errProductionValidationUnavailable
	}
	step, err := compiler.runner.OpaqueRunPlan(ctest.ExecutionDescriptor{
		LogicalName: ctestName, TestDirectory: roots.Build, Configuration: prepared.profile.Configuration,
	}, registration.target.wallTime)
	if err != nil {
		return productionValidationProcessPlan{}, errProductionValidationUnavailable
	}
	spec := productionProcessSpec(step.Process)
	spec.Env = append([]string(nil), prepared.environment...)
	spec.Env = append(spec.Env, "LLVM_PROFILE_FILE="+filepath.Join(roots.Artifacts, "profiles", string(stage)+"-%p.profraw"))
	spec.LaunchPlan = []string{prepared.testBinary}
	state, _, err := cmake.SnapshotLaunchInput(prepared.testBinary, 256<<20)
	if err != nil {
		return productionValidationProcessPlan{}, errProductionValidationUnavailable
	}
	spec.LaunchInputs = []cmake.FingerprintFile{state}
	tools := cloneProductionValidationTools(prepared.tools)
	if addProductionValidationTool(tools, spec.Executable) != nil || addProductionValidationTool(tools, prepared.testBinary) != nil {
		return productionValidationProcessPlan{}, errProductionValidationUnavailable
	}
	return compiler.singleProcessPlan(registration, stage, spec, spec.Env, nil, tools, nil), nil
}

func (compiler *productionValidationNativeCompiler) singleProcessPlan(registration productionValidationPlanRegistration, stage testgenvalidate.Stage, spec processcontrol.Spec, environment, unset []string, tools map[string]string, interpret testgenvalidate.StageInterpreter) productionValidationProcessPlan {
	return productionValidationProcessPlan{
		taskID: registration.processTaskID, serviceInstanceID: compiler.config.serviceInstanceID,
		tools: cloneProductionValidationTools(tools), allowedEnvironment: append([]string(nil), environment...), allowedEnvUnset: append([]string(nil), unset...),
		specs: map[testgenvalidate.Stage]processcontrol.Spec{stage: cloneProductionValidationProcessSpec(spec)}, interpret: interpret,
		recordLease: compiler.config.leases.PutGenerationProcessLease, releaseLease: compiler.config.leases.ReleaseGenerationProcessLease,
	}
}

func (compiler *productionValidationNativeCompiler) sequenceProcessPlan(registration productionValidationPlanRegistration, stage testgenvalidate.Stage, specs []processcontrol.Spec, environment, unset []string, tools map[string]string, interpret testgenvalidate.StageInterpreter) productionValidationProcessPlan {
	cloned := make([]processcontrol.Spec, len(specs))
	for index := range specs {
		cloned[index] = cloneProductionValidationProcessSpec(specs[index])
	}
	return productionValidationProcessPlan{
		taskID: registration.processTaskID, serviceInstanceID: compiler.config.serviceInstanceID,
		tools: cloneProductionValidationTools(tools), allowedEnvironment: append([]string(nil), environment...), allowedEnvUnset: append([]string(nil), unset...),
		sequences: map[testgenvalidate.Stage][]processcontrol.Spec{stage: cloned}, interpret: interpret,
		recordLease: compiler.config.leases.PutGenerationProcessLease, releaseLease: compiler.config.leases.ReleaseGenerationProcessLease,
	}
}

func (compiler *productionValidationNativeCompiler) ReleaseValidationStage(candidateID, processTaskID string) {
	if compiler == nil {
		return
	}
	compiler.mu.Lock()
	if state, ok := compiler.states[candidateID]; ok && state.registration.processTaskID == processTaskID {
		delete(compiler.states, candidateID)
	}
	compiler.mu.Unlock()
}

func validProductionValidationBuildPreparation(value productionValidationBuildPreparation, registration productionValidationPlanRegistration, roots testgenvalidate.Roots) bool {
	return value.configure.Executable != "" && value.compile.Executable != "" && value.profile.ID == registration.target.buildProfileID &&
		value.profile.ProjectID == registration.target.projectID && value.profile.BinaryDir == roots.Build &&
		value.testBinary != "" && filepath.IsAbs(value.testBinary) && filepath.Clean(value.testBinary) == value.testBinary && len(value.tools) > 0
}

func cloneProductionValidationBuildPreparation(value productionValidationBuildPreparation) productionValidationBuildPreparation {
	value.configure = cloneProductionValidationProcessSpec(value.configure)
	value.compile = cloneProductionValidationProcessSpec(value.compile)
	value.environment = append([]string(nil), value.environment...)
	value.tools = cloneProductionValidationTools(value.tools)
	return value
}

func productionProcessSpec(value task.ProcessSpec) processcontrol.Spec {
	return processcontrol.Spec{
		Executable: value.Executable, LaunchPlan: append([]string(nil), value.LaunchPlan...), LaunchInputs: append([]cmake.FingerprintFile(nil), value.LaunchInputs...),
		Args: append([]string(nil), value.Args...), Dir: value.Dir, Env: append([]string(nil), value.Env...), EnvUnset: append([]string(nil), value.EnvUnset...),
	}
}

func cloneProductionValidationTools(value map[string]string) map[string]string {
	result := make(map[string]string, len(value))
	for path, digest := range value {
		result[path] = digest
	}
	return result
}

func addProductionValidationTool(tools map[string]string, path string) error {
	if path == "" || !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return errProductionValidationUnavailable
	}
	content, err := os.ReadFile(path)
	if err != nil || len(content) > 256<<20 {
		return errProductionValidationUnavailable
	}
	digest := productionBytesDigest(content)
	if existing, ok := tools[path]; ok && existing != digest {
		return errProductionValidationUnavailable
	}
	tools[path] = digest
	return nil
}

var _ productionValidationStageCompiler = (*productionValidationNativeCompiler)(nil)
