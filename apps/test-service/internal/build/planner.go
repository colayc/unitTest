package build

import (
	"encoding/json"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"

	"unit-test-ide.local/test-service/internal/cmake"
	"unit-test-ide.local/test-service/internal/diagnostic"
	"unit-test-ide.local/test-service/internal/task"
	"unit-test-ide.local/test-service/internal/toolchain"
	"unit-test-ide.local/test-service/internal/workspace"
)

type PlanInput struct {
	Installation   cmake.Installation
	WorkspaceRoot  workspace.Root
	Project        workspace.ProjectConfig
	Profile        cmake.BuildProfile
	Toolchain      toolchain.Instance
	Targets        []cmake.Target
	TargetIDs      []string
	Jobs           int
	Configure      bool
	ConfigureState json.RawMessage
}

func Plan(input PlanInput) (task.ExecutionPlan, error) {
	if input.Installation.Executable == "" || input.WorkspaceRoot.NativePath == "" ||
		input.Project.ID == "" || input.Profile.ID == "" ||
		input.Profile.ProjectID != input.Project.ID || input.Profile.BinaryDir == "" ||
		input.Jobs < 1 || input.Jobs > 256 {
		return task.ExecutionPlan{}, task.ErrInvalidArgument
	}
	sourceDir, err := input.WorkspaceRoot.ResolveRelative(input.Project.SourceDir)
	if err != nil {
		return task.ExecutionPlan{}, task.ErrInvalidArgument
	}
	targetNames, err := resolveTargetNames(input.Targets, input.TargetIDs)
	if err != nil {
		return task.ExecutionPlan{}, err
	}
	steps := make([]task.ExecutionStep, 0, 2)
	if input.Configure {
		configure, err := configureStep(input, sourceDir)
		if err != nil {
			return task.ExecutionPlan{}, err
		}
		steps = append(steps, configure)
	}
	build, err := buildStep(input, sourceDir, targetNames)
	if err != nil {
		return task.ExecutionPlan{}, err
	}
	steps = append(steps, build)
	plan := task.ExecutionPlan{Version: 1, Steps: steps}
	plan.Fingerprint = task.FingerprintPlan(plan)
	return plan, nil
}

func configureStep(input PlanInput, sourceDir string) (task.ExecutionStep, error) {
	environment, err := normalizedToolchainEnvironment(input.Toolchain.Environment)
	if err != nil {
		return task.ExecutionStep{}, err
	}
	var args []string
	switch input.Profile.Origin {
	case "preset":
		if input.Profile.ConfigurePreset == "" {
			return task.ExecutionStep{}, task.ErrInvalidArgument
		}
		args = []string{"--preset", input.Profile.ConfigurePreset}
	case "generated":
		if input.Profile.Generator == "" || input.Toolchain.CCompiler == "" ||
			input.Toolchain.CXXCompiler == "" {
			return task.ExecutionStep{}, task.ErrInvalidArgument
		}
		args = []string{
			"-S", sourceDir,
			"-B", input.Profile.BinaryDir,
			"-G", input.Profile.Generator,
		}
		if input.Profile.Configuration != "" && !multiConfigGenerator(input.Profile.Generator) {
			args = append(args, "-DCMAKE_BUILD_TYPE="+input.Profile.Configuration)
		}
		args = append(args,
			"-DCMAKE_C_COMPILER="+filepath.ToSlash(input.Toolchain.CCompiler),
			"-DCMAKE_CXX_COMPILER="+filepath.ToSlash(input.Toolchain.CXXCompiler),
		)
	default:
		return task.ExecutionStep{}, task.ErrInvalidArgument
	}
	parser, err := diagnostic.NewParser(diagnostic.FamilyCMake, diagnostic.Options{
		Root: input.WorkspaceRoot, WorkingDirectory: sourceDir, StepID: "configure",
		ToolchainID: input.Toolchain.ID,
	})
	if err != nil {
		return task.ExecutionStep{}, task.ErrInvalidArgument
	}
	return task.ExecutionStep{
		ID: "configure", Kind: task.StepConfigure,
		Process: task.ProcessSpec{
			Executable: input.Installation.Executable,
			Args:       append([]string(nil), args...),
			Env:        environment,
			Dir:        sourceDir,
		},
		Public: task.CommandSummary{
			Executable: filepath.Base(input.Installation.Executable),
			Args:       append([]string(nil), args...),
		},
		State:            append(json.RawMessage(nil), input.ConfigureState...),
		DiagnosticParser: parser,
	}, nil
}

func buildStep(input PlanInput, sourceDir string, targetNames []string) (task.ExecutionStep, error) {
	environment, err := normalizedToolchainEnvironment(input.Toolchain.Environment)
	if err != nil {
		return task.ExecutionStep{}, err
	}
	args := []string{"--build", input.Profile.BinaryDir}
	if input.Profile.Configuration != "" {
		args = append(args, "--config", input.Profile.Configuration)
	}
	args = append(args, "--parallel", strconv.Itoa(input.Jobs))
	if len(targetNames) != 0 {
		args = append(args, "--target")
		args = append(args, targetNames...)
	}
	family := diagnostic.FamilyGNU
	if input.Toolchain.Family == toolchain.FamilyMSVC ||
		input.Toolchain.Family == toolchain.FamilyClangCL ||
		input.Toolchain.Family == "" && runtime.GOOS == "windows" {
		family = diagnostic.FamilyMSVC
	}
	parser, err := diagnostic.NewParser(family, diagnostic.Options{
		Root: input.WorkspaceRoot, WorkingDirectory: sourceDir, StepID: "build",
		ToolchainID: input.Toolchain.ID,
	})
	if err != nil {
		return task.ExecutionStep{}, task.ErrInvalidArgument
	}
	return task.ExecutionStep{
		ID: "build", Kind: task.StepBuild,
		Process: task.ProcessSpec{
			Executable: input.Installation.Executable,
			Args:       append([]string(nil), args...),
			Env:        environment,
			Dir:        input.Profile.BinaryDir,
		},
		Public: task.CommandSummary{
			Executable: filepath.Base(input.Installation.Executable),
			Args:       append([]string(nil), args...),
		},
		DiagnosticParser: parser,
	}, nil
}

func resolveTargetNames(targets []cmake.Target, ids []string) ([]string, error) {
	byID := make(map[string]string, len(targets))
	for _, target := range targets {
		if target.ID == "" || target.Name == "" {
			return nil, task.ErrInvalidArgument
		}
		byID[target.ID] = target.Name
	}
	names := make([]string, 0, len(ids))
	seen := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		name, ok := byID[id]
		if !ok {
			return nil, ErrTargetNotFound
		}
		if _, duplicate := seen[id]; duplicate {
			return nil, task.ErrInvalidArgument
		}
		seen[id] = struct{}{}
		names = append(names, name)
	}
	return names, nil
}

func multiConfigGenerator(generator string) bool {
	return strings.HasPrefix(generator, "Visual Studio ") ||
		generator == "Xcode" || generator == "Ninja Multi-Config"
}

func normalizedToolchainEnvironment(values []string) ([]string, error) {
	byKey := make(map[string]string, len(values))
	for _, value := range values {
		key, content, found := strings.Cut(value, "=")
		key = strings.ToUpper(key)
		if !found || !validEnvironmentKey(key) || serviceEnvironmentKey(key) {
			return nil, task.ErrInvalidArgument
		}
		if previous, duplicate := byKey[key]; duplicate && previous != content {
			return nil, task.ErrInvalidArgument
		}
		byKey[key] = content
	}
	keys := make([]string, 0, len(byKey))
	for key := range byKey {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]string, 0, len(keys))
	for _, key := range keys {
		result = append(result, key+"="+byKey[key])
	}
	return result, nil
}

func validEnvironmentKey(value string) bool {
	if value == "" {
		return false
	}
	for index := range len(value) {
		character := value[index]
		if character >= 'A' && character <= 'Z' ||
			character == '_' ||
			character >= '0' && character <= '9' && index > 0 {
			continue
		}
		return false
	}
	return true
}

func serviceEnvironmentKey(value string) bool {
	return value == "UNIT_TEST_SERVICE_TOKEN" ||
		value == "UNIT_TEST_IDE_TOKEN" ||
		value == "UNIT_TEST_IDE_STATUS_HANDLE"
}
