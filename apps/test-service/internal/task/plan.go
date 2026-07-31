package task

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"reflect"
	"runtime"
	"strings"
	"time"

	"unit-test-ide.local/test-service/internal/diagnostic"
	"unit-test-ide.local/test-service/internal/testdomain"
)

type Kind string

const (
	KindSimulation    Kind = "simulation"
	KindCMakeBuild    Kind = "cmake_build"
	KindTestDiscovery Kind = "test_discovery"
	KindTestRun       Kind = "test_run"
)

type StepKind string

const (
	StepSimulation    StepKind = "simulation"
	StepConfigure     StepKind = "configure"
	StepBuild         StepKind = "build"
	StepTestDiscovery StepKind = "test-discovery"
	StepTestRun       StepKind = "test-run"
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
	maxProcessSpecArgs    = 256
	maxProcessSpecEnv     = 256
	maxCommandSummaryArgs = 256
	maxExecutionStepState = 256 * 1024
	maxInitialPlanSteps   = 8
	maxContinuationSteps  = 256
	maxRuntimePlanSteps   = 10_000
)

type CommandSummary struct {
	Executable string   `json:"executable"`
	Args       []string `json:"args"`
}

type ExecutionStep struct {
	ID               string
	Kind             StepKind
	Process          ProcessSpec
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
	TestRun             *testdomain.TestRun

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
		if !validStepID(step.ID) || !validStepKind(step.Kind) || step.Process.Executable == "" || step.Process.Dir == "" ||
			containsNUL(step.Process.Executable) || containsNUL(step.Process.Dir) ||
			len(step.Process.Args) > maxProcessSpecArgs ||
			len(step.Process.Env) > maxProcessSpecEnv ||
			len(step.Process.EnvUnset) > maxProcessSpecEnv ||
			len(step.Public.Args) > maxCommandSummaryArgs ||
			len(step.State) > maxExecutionStepState ||
			len(step.State) != 0 && !json.Valid(step.State) {
			return ErrInvalidArgument
		}
		if _, exists := ids[step.ID]; exists {
			return ErrInvalidArgument
		}
		ids[step.ID] = struct{}{}
		for _, argument := range step.Process.Args {
			if containsNUL(argument) {
				return ErrInvalidArgument
			}
		}
		if !validProcessEnvironment(
			step.Process.Env,
			step.Process.EnvUnset,
		) {
			return ErrInvalidArgument
		}
		if err := boundary.ValidateExecutable(step.Process.Executable); err != nil {
			return ErrInvalidArgument
		}
		if err := boundary.ValidateWorkingDirectory(step.Process.Dir); err != nil {
			return ErrInvalidArgument
		}
	}
	return nil
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
	type canonicalStep struct {
		ID         string   `json:"id"`
		Kind       StepKind `json:"kind"`
		Executable string   `json:"executable"`
		Args       []string `json:"args"`
		Env        []string `json:"env"`
		EnvUnset   []string `json:"envUnset"`
		Dir        string   `json:"dir"`
	}
	type canonicalPlan struct {
		Version int             `json:"version"`
		Steps   []canonicalStep `json:"steps"`
	}

	canonical := canonicalPlan{Version: plan.Version, Steps: make([]canonicalStep, len(plan.Steps))}
	for index, step := range plan.Steps {
		canonical.Steps[index] = canonicalStep{
			ID:         step.ID,
			Kind:       step.Kind,
			Executable: step.Process.Executable,
			Args:       append([]string{}, step.Process.Args...),
			Env:        append([]string{}, step.Process.Env...),
			EnvUnset: append(
				[]string{},
				step.Process.EnvUnset...,
			),
			Dir: step.Process.Dir,
		}
	}
	raw, _ := json.Marshal(canonical)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func validStepKind(value StepKind) bool {
	switch value {
	case StepSimulation, StepConfigure, StepBuild, StepTestDiscovery, StepTestRun:
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
