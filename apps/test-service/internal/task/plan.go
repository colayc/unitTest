package task

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"reflect"
	"runtime"
	"strings"
	"time"

	"unit-test-ide.local/test-service/internal/cmake"
	"unit-test-ide.local/test-service/internal/coveragedomain"
	"unit-test-ide.local/test-service/internal/diagnostic"
	"unit-test-ide.local/test-service/internal/testdomain"
)

type Kind string

const (
	KindSimulation    Kind = "simulation"
	KindCMakeBuild    Kind = "cmake_build"
	KindTestDiscovery Kind = "test_discovery"
	KindTestRun       Kind = "test_run"
	KindCoverageRun   Kind = "coverage_run"
)

type StepKind string

const (
	StepSimulation        StepKind = "simulation"
	StepConfigure         StepKind = "configure"
	StepBuild             StepKind = "build"
	StepTestDiscovery     StepKind = "test-discovery"
	StepTestRun           StepKind = "test-run"
	StepCoverageConfigure StepKind = "coverage-configure"
	StepCoverageBuild     StepKind = "coverage-build"
	StepCoverageTest      StepKind = "coverage-test"
	StepCoverageMerge     StepKind = "coverage-merge"
	StepCoverageNormalize StepKind = "coverage-normalize"
	StepCoverageReport    StepKind = "coverage-report"
	StepCoveragePublish   StepKind = "coverage-publish"
)

type StepStatus string

const (
	StepPending   StepStatus = "pending"
	StepRunning   StepStatus = "running"
	StepSucceeded StepStatus = "succeeded"
	StepFailed    StepStatus = "failed"
	StepSkipped   StepStatus = "skipped"
)

const (
	maxProcessSpecArgs         = 256
	maxProcessSpecEnv          = 256
	maxProcessLaunchPlan       = 64
	maxProcessLaunchInputs     = 128
	maxProcessLaunchInputBytes = 512 * 1024
	maxProcessBatchItems       = 256
	maxCommandSummaryArgs      = 256
	maxExecutionStepState      = 256 * 1024
	maxInitialPlanSteps        = 8
	maxContinuationSteps       = 256
	maxRuntimePlanSteps        = 10_000
)

type CommandSummary struct {
	Executable string   `json:"executable"`
	Args       []string `json:"args"`
}

// ServiceAction is a closed, runtime-only operation implemented by the
// service. It is deliberately distinct from ProcessSpec so executable paths,
// environment, and callbacks cannot cross the persistence or protocol
// boundary.
type ServiceAction string

const (
	ServiceActionCoverageReport  ServiceAction = "coverage-report"
	ServiceActionCoveragePublish ServiceAction = "coverage-publish"
)

type ExecutionStep struct {
	ID               string
	Kind             StepKind
	Process          ProcessSpec
	Action           ServiceAction
	Public           CommandSummary
	State            json.RawMessage
	DiagnosticParser diagnostic.Parser
}

type ExecutionPlan struct {
	Version     int
	Fingerprint string
	Steps       []ExecutionStep
}

type StepVerdict string

const (
	StepVerdictDefault   StepVerdict = ""
	StepVerdictSucceeded StepVerdict = "succeeded"
	StepVerdictFailed    StepVerdict = "failed"
)

type StepResult struct {
	Process ProcessResult
	Verdict StepVerdict
}

type Continuation struct {
	Steps []ExecutionStep
}

// ExecutionBoundary is runtime-only. It must never be persisted or exposed
// through protocol types.
type ExecutionBoundary interface {
	ValidateExecutable(path string) error
	ValidateWorkingDirectory(path string) error
}

type ManagedExecutionBoundary interface {
	ExecutionBoundary
	Adopt(string)
	Release() error
}

// ProcessTargetBoundary is an optional runtime-only extension. Consumers that
// pin a fixed command (for example the bundled gcovr runner) can validate the
// complete target, including args and environment, on every plan and
// continuation validation without changing the protocol-facing boundary.
type ProcessTargetBoundary interface {
	ValidateProcessTarget(executable string, args, env, envUnset []string, dir string) error
}

type StartRequest struct {
	IdempotencyKey      string
	Kind                Kind
	Request             json.RawMessage
	WorkspaceGeneration string
	Timeout             time.Duration
	Plan                ExecutionPlan
	Boundary            ExecutionBoundary
	Continuation        PlanContinuation
	ResultInterpreter   ResultInterpreter
	ActionExecutor      ServiceActionExecutor
	TestRun             *testdomain.TestRun
	CoverageRun         *coveragedomain.Run

	// Scenario remains an internal compatibility input while v1.1 simulation
	// requests are projected into service-owned execution plans.
	Scenario Scenario
}

type ResumeRequest struct {
	Task              Task
	Plan              ExecutionPlan
	Boundary          ExecutionBoundary
	Continuation      PlanContinuation
	ResultInterpreter ResultInterpreter
	ActionExecutor    ServiceActionExecutor
}

func ValidatePlan(plan ExecutionPlan, boundary ExecutionBoundary) error {
	if plan.Version != 1 ||
		len(plan.Steps) < 1 ||
		len(plan.Steps) > maxInitialPlanSteps ||
		nilBoundary(boundary) {
		return ErrInvalidArgument
	}
	return validateExecutionSteps(plan.Steps, boundary)
}

func validateExecutionSteps(
	steps []ExecutionStep,
	boundary ExecutionBoundary,
) error {
	if len(steps) < 1 || len(steps) > maxRuntimePlanSteps ||
		nilBoundary(boundary) {
		return ErrInvalidArgument
	}
	ids := make(map[string]struct{}, len(steps))
	for _, step := range steps {
		if !validStepID(step.ID) || !validStepKind(step.Kind) ||
			!validExecutionStep(step, boundary) ||
			len(step.Public.Args) > maxCommandSummaryArgs ||
			len(step.State) > maxExecutionStepState ||
			len(step.State) != 0 && !json.Valid(step.State) {
			return ErrInvalidArgument
		}
		if _, exists := ids[step.ID]; exists {
			return ErrInvalidArgument
		}
		ids[step.ID] = struct{}{}
	}
	return nil
}

// validExecutionStep accepts exactly one execution mechanism. Service actions
// are intentionally closed and may not carry a diagnostic parser.
func validExecutionStep(step ExecutionStep, boundary ExecutionBoundary) bool {
	hasProcess := step.Process.Executable != "" || len(step.Process.Batch) != 0
	hasAction := step.Action != ""
	if hasProcess == hasAction {
		return false
	}
	if hasAction {
		return validServiceAction(step.Kind, step.Action) && step.DiagnosticParser == nil
	}
	return validProcessSpec(step.Process, boundary)
}

func validServiceAction(kind StepKind, action ServiceAction) bool {
	switch action {
	case ServiceActionCoverageReport:
		return kind == StepCoverageReport
	case ServiceActionCoveragePublish:
		return kind == StepCoveragePublish
	default:
		return false
	}
}

func validProcessSpec(
	spec ProcessSpec,
	boundary ExecutionBoundary,
) bool {
	if len(spec.Batch) == 0 {
		return validProcessTarget(
			spec.Executable,
			spec.LaunchPlan,
			spec.LaunchInputs,
			spec.Args,
			spec.Env,
			spec.EnvUnset,
			spec.Dir,
			boundary,
		)
	}
	if spec.Executable != "" || len(spec.Args) != 0 ||
		len(spec.Env) != 0 || len(spec.EnvUnset) != 0 ||
		spec.Dir != "" ||
		len(spec.Batch) > maxProcessBatchItems {
		return false
	}
	ids := make(map[string]struct{}, len(spec.Batch))
	for _, item := range spec.Batch {
		if !validStepID(item.ID) ||
			item.Timeout < time.Millisecond ||
			item.Timeout > 24*time.Hour ||
			item.Timeout%time.Millisecond != 0 ||
			!validProcessTarget(
				item.Executable,
				item.LaunchPlan,
				item.LaunchInputs,
				item.Args,
				item.Env,
				item.EnvUnset,
				item.Dir,
				boundary,
			) {
			return false
		}
		if _, duplicate := ids[item.ID]; duplicate {
			return false
		}
		ids[item.ID] = struct{}{}
	}
	return true
}

func validProcessTarget(
	executable string,
	launchPlan []string,
	launchInputs []cmake.FingerprintFile,
	arguments, environment, unset []string,
	directory string,
	boundary ExecutionBoundary,
) bool {
	if executable == "" || directory == "" ||
		containsNUL(executable) || containsNUL(directory) ||
		len(arguments) > maxProcessSpecArgs ||
		len(launchPlan) > maxProcessLaunchPlan ||
		len(launchInputs) > maxProcessLaunchInputs ||
		len(environment) > maxProcessSpecEnv ||
		len(unset) > maxProcessSpecEnv {
		return false
	}
	for _, plannedExecutable := range launchPlan {
		if plannedExecutable == "" || containsNUL(plannedExecutable) {
			return false
		}
	}
	seenLaunchInputs := make(map[string]struct{}, len(launchInputs))
	for _, state := range launchInputs {
		if state.Path == "" || state.Identity == "" || len(state.Identity) > 128 ||
			cmake.VerifyLaunchInput(state, maxProcessLaunchInputBytes) != nil {
			return false
		}
		key := strings.ToLower(state.Path)
		if _, duplicate := seenLaunchInputs[key]; duplicate {
			return false
		}
		seenLaunchInputs[key] = struct{}{}
	}
	for _, argument := range arguments {
		if containsNUL(argument) {
			return false
		}
	}
	if !validProcessEnvironment(environment, unset) {
		return false
	}
	if err := boundary.ValidateExecutable(executable); err != nil {
		return false
	}
	if targetBoundary, ok := boundary.(ProcessTargetBoundary); ok {
		if err := targetBoundary.ValidateProcessTarget(
			executable, arguments, environment, unset, directory,
		); err != nil {
			return false
		}
	}
	return boundary.ValidateWorkingDirectory(directory) == nil
}

func extendExecutionPlan(
	plan ExecutionPlan,
	continuation Continuation,
	boundary ExecutionBoundary,
) (ExecutionPlan, []StepSnapshot, error) {
	if len(continuation.Steps) == 0 {
		return plan, nil, nil
	}
	if len(continuation.Steps) > maxContinuationSteps ||
		len(continuation.Steps) > maxRuntimePlanSteps-len(plan.Steps) {
		return ExecutionPlan{}, nil, ErrInvalidArgument
	}
	combined := cloneExecutionPlan(plan)
	appended := cloneExecutionPlan(ExecutionPlan{
		Version: plan.Version,
		Steps:   continuation.Steps,
	})
	combined.Steps = append(combined.Steps, appended.Steps...)
	if err := validateExecutionSteps(combined.Steps, boundary); err != nil {
		return ExecutionPlan{}, nil, err
	}
	combined.Fingerprint = FingerprintPlan(combined)
	snapshots := initialStepSnapshots(appended)
	return combined, snapshots, nil
}

func FingerprintPlan(plan ExecutionPlan) string {
	type canonicalBatchProcess struct {
		ID           string                  `json:"id"`
		Executable   string                  `json:"executable"`
		LaunchPlan   []string                `json:"launchPlan"`
		LaunchInputs []cmake.FingerprintFile `json:"launchInputs,omitempty"`
		Args         []string                `json:"args"`
		Env          []string                `json:"env"`
		EnvUnset     []string                `json:"envUnset"`
		Dir          string                  `json:"dir"`
		TimeoutMS    int64                   `json:"timeoutMs"`
	}
	type canonicalStep struct {
		ID           string                  `json:"id"`
		Kind         StepKind                `json:"kind"`
		Action       ServiceAction           `json:"action,omitempty"`
		Executable   string                  `json:"executable"`
		LaunchPlan   []string                `json:"launchPlan"`
		LaunchInputs []cmake.FingerprintFile `json:"launchInputs,omitempty"`
		Args         []string                `json:"args"`
		Env          []string                `json:"env"`
		EnvUnset     []string                `json:"envUnset"`
		Dir          string                  `json:"dir"`
		Batch        []canonicalBatchProcess `json:"batch,omitempty"`
	}
	type canonicalPlan struct {
		Version int             `json:"version"`
		Steps   []canonicalStep `json:"steps"`
	}

	canonical := canonicalPlan{Version: plan.Version, Steps: make([]canonicalStep, len(plan.Steps))}
	for index, step := range plan.Steps {
		canonical.Steps[index] = canonicalStep{
			ID:           step.ID,
			Kind:         step.Kind,
			Action:       step.Action,
			Executable:   step.Process.Executable,
			LaunchPlan:   append([]string{}, step.Process.LaunchPlan...),
			LaunchInputs: append([]cmake.FingerprintFile{}, step.Process.LaunchInputs...),
			Args:         append([]string{}, step.Process.Args...),
			Env:          append([]string{}, step.Process.Env...),
			EnvUnset: append(
				[]string{},
				step.Process.EnvUnset...,
			),
			Dir: step.Process.Dir,
		}
		for _, item := range step.Process.Batch {
			canonical.Steps[index].Batch = append(
				canonical.Steps[index].Batch,
				canonicalBatchProcess{
					ID:           item.ID,
					Executable:   item.Executable,
					LaunchPlan:   append([]string{}, item.LaunchPlan...),
					LaunchInputs: append([]cmake.FingerprintFile{}, item.LaunchInputs...),
					Args:         append([]string{}, item.Args...),
					Env:          append([]string{}, item.Env...),
					EnvUnset: append(
						[]string{},
						item.EnvUnset...,
					),
					Dir:       item.Dir,
					TimeoutMS: item.Timeout.Milliseconds(),
				},
			)
		}
	}
	raw, _ := json.Marshal(canonical)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func validStepKind(value StepKind) bool {
	switch value {
	case StepSimulation, StepConfigure, StepBuild, StepTestDiscovery, StepTestRun,
		StepCoverageConfigure, StepCoverageBuild, StepCoverageTest, StepCoverageMerge,
		StepCoverageNormalize, StepCoverageReport, StepCoveragePublish:
		return true
	default:
		return false
	}
}

func validStepID(value string) bool {
	if len(value) < 1 || len(value) > 64 {
		return false
	}
	for index := 0; index < len(value); index++ {
		character := value[index]
		if (character >= 'a' && character <= 'z') || (character >= '0' && character <= '9' && index > 0) ||
			(character == '-' && index > 0) || (character == '_' && index > 0) {
			continue
		}
		return false
	}
	return true
}

func validProcessEnvironment(
	environment []string,
	unset []string,
) bool {
	if len(environment)+len(unset) > maxProcessSpecEnv {
		return false
	}
	seen := make(map[string]struct{}, len(environment)+len(unset))
	for _, value := range environment {
		key, _, found := strings.Cut(value, "=")
		if !found || containsNUL(value) ||
			!validEnvironmentKey(key) ||
			serviceOwnedEnvironmentKey(key) {
			return false
		}
		canonical := environmentKey(key)
		if _, duplicate := seen[canonical]; duplicate {
			return false
		}
		seen[canonical] = struct{}{}
	}
	for _, key := range unset {
		if containsNUL(key) ||
			!validEnvironmentKey(key) ||
			serviceOwnedEnvironmentKey(key) {
			return false
		}
		canonical := environmentKey(key)
		if _, duplicate := seen[canonical]; duplicate {
			return false
		}
		seen[canonical] = struct{}{}
	}
	return true
}

func validEnvironmentKey(value string) bool {
	if value == "" {
		return false
	}
	for index := 0; index < len(value); index++ {
		character := value[index]
		if (character >= 'A' && character <= 'Z') ||
			(character >= 'a' && character <= 'z') ||
			character == '_' ||
			(character >= '0' && character <= '9' && index > 0) {
			continue
		}
		return false
	}
	return true
}

func serviceOwnedEnvironmentKey(value string) bool {
	upper := strings.ToUpper(value)
	return strings.HasPrefix(upper, "UTIDE_") ||
		strings.HasPrefix(upper, "UNIT_TEST_IDE_") ||
		upper == "UNIT_TEST_SERVICE_TOKEN"
}

func environmentKey(value string) string {
	if runtime.GOOS == "windows" {
		return strings.ToUpper(value)
	}
	return value
}

func containsNUL(value string) bool { return strings.IndexByte(value, 0) >= 0 }

func nilBoundary(boundary ExecutionBoundary) bool {
	if boundary == nil {
		return true
	}
	value := reflect.ValueOf(boundary)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Ptr, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}
