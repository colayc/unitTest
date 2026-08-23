package build

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"

	"unit-test-ide.local/test-service/internal/cmake"
	"unit-test-ide.local/test-service/internal/diagnostic"
	"unit-test-ide.local/test-service/internal/offlineboundary"
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
	Coverage       *CoverageOptions
}

func Plan(input PlanInput) (task.ExecutionPlan, error) {
	if input.Installation.Executable == "" || input.WorkspaceRoot.NativePath == "" ||
		input.Project.ID == "" || input.Profile.ID == "" ||
		input.Profile.ProjectID != input.Project.ID || input.Profile.BinaryDir == "" ||
		input.Jobs < 1 || input.Jobs > 256 ||
		(input.Installation.UnityRunnerGenerator != (cmake.ProductExecutable{}) &&
			!input.Installation.UnityRunnerGenerator.Valid()) ||
		!validPlanCoverage(input.Coverage) {
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
	launchPlan, err := nativeBuildLaunchPlan(input)
	if err != nil {
		return task.ExecutionPlan{}, task.ErrInvalidArgument
	}
	var launchInputs []cmake.FingerprintFile
	if runtime.GOOS == "windows" && (input.Coverage != nil || offlineboundary.ExecutableRegistrationActive()) {
		launchPlan, launchInputs, err = validateCMakeLaunchPlan(input, sourceDir, launchPlan)
		if err != nil {
			return task.ExecutionPlan{}, task.ErrInvalidArgument
		}
	}
	steps := make([]task.ExecutionStep, 0, 2)
	if input.Configure {
		configure, err := configureStep(input, sourceDir, launchPlan, launchInputs)
		if err != nil {
			return task.ExecutionPlan{}, err
		}
		steps = append(steps, configure)
	}
	build, err := buildStep(input, sourceDir, targetNames, launchPlan, launchInputs)
	if err != nil {
		return task.ExecutionPlan{}, err
	}
	steps = append(steps, build)
	plan := task.ExecutionPlan{Version: 1, Steps: steps}
	plan.Fingerprint = task.FingerprintPlan(plan)
	return plan, nil
}

func configureStep(input PlanInput, sourceDir string, launchPlan []string, launchInputs []cmake.FingerprintFile) (task.ExecutionStep, error) {
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
		if input.Coverage != nil {
			args = append(args, "-B", input.Coverage.BinaryDir)
		}
	case "generated":
		if input.Profile.Generator == "" || input.Toolchain.CCompiler == "" ||
			input.Toolchain.CXXCompiler == "" {
			return task.ExecutionStep{}, task.ErrInvalidArgument
		}
		args = []string{
			"-S", sourceDir,
			"-B", planBinaryDir(input),
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
	if input.Coverage != nil {
		args = append(args,
			"-DCMAKE_PROJECT_TOP_LEVEL_INCLUDES:FILEPATH="+
				filepath.ToSlash(input.Coverage.TopLevelInclude.Path),
		)
	}
	if input.Installation.UnityRunnerGenerator.Valid() {
		args = append(args,
			"-DUTIDE_UNITY_RUNNER_GENERATOR:FILEPATH="+
				filepath.ToSlash(input.Installation.UnityRunnerGenerator.Path),
		)
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
			Executable:   input.Installation.Executable,
			LaunchPlan:   launchPlan,
			LaunchInputs: append([]cmake.FingerprintFile(nil), launchInputs...),
			Args:         append([]string(nil), args...),
			Env:          environment,
			Dir:          sourceDir,
		},
		Public: task.CommandSummary{
			Executable: filepath.Base(input.Installation.Executable),
			Args:       append([]string(nil), args...),
		},
		State:            append(json.RawMessage(nil), input.ConfigureState...),
		DiagnosticParser: parser,
	}, nil
}

func buildStep(input PlanInput, sourceDir string, targetNames, launchPlan []string, launchInputs []cmake.FingerprintFile) (task.ExecutionStep, error) {
	environment, err := normalizedToolchainEnvironment(input.Toolchain.Environment)
	if err != nil {
		return task.ExecutionStep{}, err
	}
	binaryDir := planBinaryDir(input)
	args := []string{"--build", binaryDir}
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
			Executable:   input.Installation.Executable,
			LaunchPlan:   launchPlan,
			LaunchInputs: append([]cmake.FingerprintFile(nil), launchInputs...),
			Args:         append([]string(nil), args...),
			Env:          environment,
			Dir:          binaryDir,
		},
		Public: task.CommandSummary{
			Executable: filepath.Base(input.Installation.Executable),
			Args:       append([]string(nil), args...),
		},
		DiagnosticParser: parser,
	}, nil
}

// Windows WFP has no PID-tree condition. This is the closed declaration of
// executable identities the supported CMake build is allowed to launch. It is
// registered before CMake starts; arbitrary undeclared custom tools are not a
// supported boundary shape.
func nativeBuildLaunchPlan(input PlanInput) ([]string, error) {
	values := []string{
		input.Installation.Executable,
		input.Installation.CTestExecutable,
		input.Installation.UnityRunnerGenerator.Path,
		input.Toolchain.CCompiler,
		input.Toolchain.CXXCompiler,
	}
	if strings.HasPrefix(input.Profile.Generator, "Ninja") && input.Installation.Root != "" {
		name := "ninja"
		if runtime.GOOS == "windows" {
			name += ".exe"
		}
		if runtime.GOOS == "windows" {
			if path := verifiedNinjaPath(input.Toolchain.Environment, name); path != "" {
				values = append(values, path)
			} else if path := verifiedDefaultNinjaPath(name); path != "" {
				values = append(values, path)
			} else if len(input.Toolchain.Environment) == 0 {
				// Keep deterministic synthetic planner fixtures working when no
				// captured toolchain environment is supplied.
				values = append(values, filepath.Join(input.Installation.Root, "bin", name))
			} else {
				return nil, task.ErrInvalidArgument
			}
		} else {
			values = append(values, filepath.Join(input.Installation.Root, "bin", name))
		}
	}
	if runtime.GOOS == "windows" && input.Toolchain.CXXCompiler != "" {
		toolRoot := filepath.Dir(input.Toolchain.CXXCompiler)
		switch input.Toolchain.Family {
		case toolchain.FamilyClangCL:
			values = append(values, filepath.Join(toolRoot, "lld-link.exe"), filepath.Join(toolRoot, "llvm-lib.exe"))
		case toolchain.FamilyMSVC:
			values = append(values, filepath.Join(toolRoot, "link.exe"), filepath.Join(toolRoot, "lib.exe"))
		case toolchain.FamilyGCC:
			values = append(values, filepath.Join(toolRoot, "ld.exe"), filepath.Join(toolRoot, "ar.exe"))
		case toolchain.FamilyClang:
			values = append(values, filepath.Join(toolRoot, "ld.lld.exe"), filepath.Join(toolRoot, "llvm-ar.exe"))
		default:
			return nil, task.ErrInvalidArgument
		}
	}
	values = append(values, input.Toolchain.Coverage.LLVMProfdata, input.Toolchain.Coverage.LLVMCov)
	if runtime.GOOS == "windows" {
		shell := filepath.Clean(os.Getenv("ComSpec"))
		if !filepath.IsAbs(shell) || !strings.EqualFold(filepath.Base(shell), "cmd.exe") {
			return nil, task.ErrInvalidArgument
		}
		values = append(values, shell)
		for _, target := range input.Targets {
			if target.Type != "EXECUTABLE" {
				continue
			}
			for _, artifact := range target.Artifacts {
				if filepath.IsAbs(artifact) && strings.EqualFold(filepath.Ext(artifact), ".exe") {
					values = append(values, filepath.Clean(artifact))
				}
			}
		}
	}
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" {
			continue
		}
		key := strings.ToLower(filepath.Clean(value))
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, value)
	}
	return result, nil
}

func verifiedNinjaPath(environment []string, executableName string) string {
	for _, entry := range environment {
		key, value, found := strings.Cut(entry, "=")
		if !found || !strings.EqualFold(key, "PATH") {
			continue
		}
		for _, directory := range filepath.SplitList(value) {
			directory = strings.TrimSpace(directory)
			if directory == "" || !filepath.IsAbs(directory) {
				continue
			}
			candidate := filepath.Join(directory, executableName)
			info, err := os.Stat(candidate)
			if err == nil && !info.IsDir() {
				return filepath.Clean(candidate)
			}
		}
	}
	return ""
}

func verifiedDefaultNinjaPath(executableName string) string {
	programFiles := strings.TrimSpace(os.Getenv("ProgramFiles"))
	if programFiles == "" {
		return ""
	}
	candidate := filepath.Join(programFiles, "CMake", "bin", executableName)
	info, err := os.Stat(candidate)
	if err != nil || info.IsDir() {
		return ""
	}
	return filepath.Clean(candidate)
}

func planBinaryDir(input PlanInput) string {
	if input.Coverage != nil {
		return input.Coverage.BinaryDir
	}
	return input.Profile.BinaryDir
}

func validPlanCoverage(options *CoverageOptions) bool {
	return options == nil || validCoverageOptions(options, "")
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
