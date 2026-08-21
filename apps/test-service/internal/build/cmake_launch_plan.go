package build

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"unit-test-ide.local/test-service/internal/cmake"
	"unit-test-ide.local/test-service/internal/coveragellvm"
)

const (
	maxCMakeLaunchFiles      = 128
	maxCMakeLaunchFileBytes  = 512 * 1024
	maxCMakeLaunchTotalBytes = 4 * 1024 * 1024
	maxCMakeLaunchCommands   = 8192
	maxCMakeLaunchArguments  = 65536
)

var errInvalidCMakeLaunchDeclaration = errors.New("invalid CMake launch declaration")

type cmakeArgument struct {
	value string
}

type cmakeInvocation struct {
	name      string
	arguments []cmakeArgument
}

type cmakeLaunchValidator struct {
	allowed                map[string]struct{}
	variables              map[string]string
	targets                map[string]string
	targetPaths            map[string]struct{}
	cmakePath              string
	ctestPath              string
	binaryRoot             string
	configuration          string
	multiConfig            bool
	allowedRoots           []string
	visited                map[string]struct{}
	states                 []cmake.FingerprintFile
	planned                []string
	trustedFiles           map[string]map[string]string
	snapshots              map[string]cmake.FingerprintFile
	instrumentationPath    string
	instrumentationDigest  string
	instrumentationTrusted bool
	files                  int
	totalBytes             int64
}

// validateCMakeLaunchPlan proves every configure/build process entry in the
// supported, statically reachable CMake graph is already in the APP_ID launch
// declaration. Anything that cannot be resolved without running CMake is
// rejected before process creation.
func validateCMakeLaunchPlan(input PlanInput, sourceDir string, launchPlan []string) ([]string, []cmake.FingerprintFile, error) {
	sourceRoot, err := canonicalCMakeLaunchPath(sourceDir)
	if err != nil {
		return nil, nil, errInvalidCMakeLaunchDeclaration
	}
	binaryRoot := planBinaryDir(input)
	validator := &cmakeLaunchValidator{
		allowed: make(map[string]struct{}, len(launchPlan)),
		variables: map[string]string{
			"${CMAKE_COMMAND}":       input.Installation.Executable,
			"${CMAKE_CTEST_COMMAND}": input.Installation.CTestExecutable,
			"${CMAKE_C_COMPILER}":    input.Toolchain.CCompiler,
			"${CMAKE_CXX_COMPILER}":  input.Toolchain.CXXCompiler,
		},
		cmakePath:     cmakeLaunchPathKey(input.Installation.Executable),
		ctestPath:     cmakeLaunchPathKey(input.Installation.CTestExecutable),
		binaryRoot:    binaryRoot,
		configuration: input.Profile.Configuration,
		multiConfig:   multiConfigGenerator(input.Profile.Generator),
		targets:       make(map[string]string, len(input.Targets)),
		targetPaths:   make(map[string]struct{}, len(input.Targets)),
		allowedRoots:  []string{sourceRoot},
		visited:       make(map[string]struct{}),
		trustedFiles:  make(map[string]map[string]string),
		snapshots:     make(map[string]cmake.FingerprintFile),
	}
	for _, executable := range launchPlan {
		if !filepath.IsAbs(executable) {
			return nil, nil, errInvalidCMakeLaunchDeclaration
		}
		validator.addAllowedExecutable(executable, false)
	}
	for _, target := range input.Targets {
		if target.Name == "" || target.Type != "EXECUTABLE" {
			continue
		}
		var executable string
		for _, artifact := range target.Artifacts {
			if !filepath.IsAbs(artifact) || !strings.EqualFold(filepath.Ext(artifact), ".exe") {
				continue
			}
			if executable != "" && !strings.EqualFold(filepath.Clean(executable), filepath.Clean(artifact)) {
				return nil, nil, errInvalidCMakeLaunchDeclaration
			}
			executable = filepath.Clean(artifact)
		}
		if executable != "" {
			key := strings.ToLower(target.Name)
			if previous, duplicate := validator.targets[key]; duplicate && !strings.EqualFold(previous, executable) {
				return nil, nil, errInvalidCMakeLaunchDeclaration
			}
			validator.targets[key] = executable
			validator.targetPaths[cmakeLaunchPathKey(executable)] = struct{}{}
		}
	}
	if input.Profile.Origin == "preset" {
		if err := validator.validatePreset(sourceRoot, input.Profile.ConfigurePreset); err != nil {
			return nil, nil, err
		}
	}
	if input.Coverage != nil {
		include, includeErr := canonicalCMakeLaunchPath(input.Coverage.TopLevelInclude.Path)
		if includeErr != nil {
			return nil, nil, errInvalidCMakeLaunchDeclaration
		}
		validator.allowedRoots = append(validator.allowedRoots, filepath.Dir(include))
		validator.instrumentationPath = cmakeLaunchPathKey(include)
		validator.instrumentationDigest = input.Coverage.TopLevelInclude.SHA256
		validator.instrumentationTrusted =
			input.Coverage.InstrumentationFingerprint == coveragellvm.InstrumentationFingerprint() &&
				input.Coverage.TopLevelInclude.Identity == coveragellvm.InstrumentationFingerprint() &&
				input.Coverage.TopLevelInclude.SHA256 == coveragellvm.InstrumentationSHA256()
		if input.Installation.UnityRunnerGenerator.Valid() {
			validator.trustedFiles[cmakeLaunchPathKey(include)] = map[string]string{
				"${generator}": input.Installation.UnityRunnerGenerator.Path,
			}
		}
		if err := validator.validateFile(include, binaryRoot); err != nil {
			return nil, nil, err
		}
	}
	if err := validator.validateFile(filepath.Join(sourceRoot, "CMakeLists.txt"), binaryRoot); err != nil {
		return nil, nil, err
	}
	sort.Slice(validator.states, func(i, j int) bool { return validator.states[i].Path < validator.states[j].Path })
	resultPlan := append(append([]string(nil), launchPlan...), validator.planned...)
	if len(resultPlan) > 64 {
		return nil, nil, errInvalidCMakeLaunchDeclaration
	}
	return resultPlan, append([]cmake.FingerprintFile(nil), validator.states...), nil
}

func (validator *cmakeLaunchValidator) validateFile(path, binaryDir string) error {
	canonical, err := canonicalCMakeLaunchPath(path)
	if err != nil || !cmakeLaunchPathWithinRoots(canonical, validator.allowedRoots) {
		return errInvalidCMakeLaunchDeclaration
	}
	key := cmakeLaunchPathKey(canonical)
	if _, seen := validator.visited[key]; seen {
		return nil
	}
	content, snapshotErr := validator.snapshotInput(canonical)
	if snapshotErr != nil {
		return snapshotErr
	}
	invocations, err := parseCMakeInvocations(content)
	if err != nil {
		return errInvalidCMakeLaunchDeclaration
	}
	for _, invocation := range invocations {
		switch strings.ToLower(invocation.name) {
		case "add_custom_command", "add_custom_target":
			if err := validator.validateCommandGroups(invocation, key, "custom"); err != nil {
				return err
			}
		case "add_test":
			if err := validator.validateCommandGroups(invocation, key, "test"); err != nil {
				return err
			}
		case "execute_process":
			if err := validator.validateCommandGroups(invocation, key, "execute"); err != nil {
				return err
			}
		case "try_run", "try_compile":
			return errInvalidCMakeLaunchDeclaration
		case "cmake_language":
			// CALL, EVAL, and DEFER can all introduce a command after this
			// closed declaration has been validated. There is no safe default
			// for an unrecognised cmake_language mode.
			return errInvalidCMakeLaunchDeclaration
		case "set":
			if err := validator.validateSetLaunchProperty(invocation, key); err != nil {
				return err
			}
		case "list":
			if err := validator.validateListLaunchMutation(invocation, key); err != nil {
				return err
			}
		case "string", "cmake_path":
			if err := validateClosedVariableWriter(invocation); err != nil {
				return err
			}
		case "set_property":
			if err := validator.validateSetProperty(invocation, key); err != nil {
				return err
			}
		case "set_target_properties":
			if err := validator.validateTargetProperties(invocation, key); err != nil {
				return err
			}
		case "set_directory_properties":
			if err := validator.validateDirectoryProperties(invocation, key); err != nil {
				return err
			}
		case "project":
			if err := validator.validateProject(invocation, key); err != nil {
				return err
			}
		case "add_executable":
			if err := validator.declareExecutable(invocation, binaryDir); err != nil {
				return err
			}
		case "file":
			if len(invocation.arguments) != 0 {
				switch strings.ToUpper(invocation.arguments[0].value) {
				case "WRITE", "APPEND", "GENERATE", "CONFIGURE":
					return errInvalidCMakeLaunchDeclaration
				}
			}
			if err := validateClosedVariableWriter(invocation); err != nil {
				return err
			}
		case "configure_file":
			return errInvalidCMakeLaunchDeclaration
		case "add_subdirectory":
			if len(invocation.arguments) == 0 {
				return errInvalidCMakeLaunchDeclaration
			}
			directory, err := resolveLiteralCMakeLaunchPath(filepath.Dir(canonical), invocation.arguments[0].value)
			childBinary := filepath.Join(binaryDir, filepath.FromSlash(invocation.arguments[0].value))
			if len(invocation.arguments) > 1 && !strings.EqualFold(invocation.arguments[1].value, "EXCLUDE_FROM_ALL") {
				childBinary, err = resolveLiteralCMakeLaunchPath(binaryDir, invocation.arguments[1].value)
			}
			if err != nil || validator.validateFile(filepath.Join(directory, "CMakeLists.txt"), childBinary) != nil {
				return errInvalidCMakeLaunchDeclaration
			}
		case "include":
			if len(invocation.arguments) == 0 {
				return errInvalidCMakeLaunchDeclaration
			}
			include, err := resolveLiteralCMakeLaunchPath(filepath.Dir(canonical), invocation.arguments[0].value)
			if err != nil {
				return errInvalidCMakeLaunchDeclaration
			}
			if filepath.Ext(include) == "" {
				include += ".cmake"
			}
			if err := validator.validateFile(include, binaryDir); err != nil {
				return errInvalidCMakeLaunchDeclaration
			}
		case "add_compile_options", "add_link_options":
			if err := validator.validateInstrumentationOptions(invocation, key); err != nil {
				return err
			}
		case "cmake_minimum_required", "enable_testing", "add_library",
			"target_compile_features", "target_link_libraries",
			"if", "elseif", "else", "endif", "message":
			// These commands are part of the canonical coverage fixture and do
			// not assign a command, tool, or script-loader output variable.
		default:
			// An unknown command may assign an output variable (for example,
			// find_program or get_filename_component) or schedule a launch.
			// Only the explicitly classified commands above are accepted.
			return errInvalidCMakeLaunchDeclaration
		}
	}
	return nil
}

func (validator *cmakeLaunchValidator) snapshotInput(canonical string) ([]byte, error) {
	key := cmakeLaunchPathKey(canonical)
	if _, seen := validator.visited[key]; seen {
		return nil, nil
	}
	if validator.files >= maxCMakeLaunchFiles {
		return nil, errInvalidCMakeLaunchDeclaration
	}
	state, content, err := cmake.SnapshotLaunchInput(canonical, maxCMakeLaunchFileBytes)
	if err != nil || validator.totalBytes > maxCMakeLaunchTotalBytes-int64(len(content)) {
		return nil, errInvalidCMakeLaunchDeclaration
	}
	validator.files++
	validator.totalBytes += int64(len(content))
	validator.visited[key] = struct{}{}
	validator.states = append(validator.states, state)
	validator.snapshots[key] = state
	return content, nil
}

type launchPreset struct {
	name          string
	inherits      []string
	cache         map[string]json.RawMessage
	environment   map[string]json.RawMessage
	toolchainFile json.RawMessage
}

type launchPresetDocument struct {
	Version          int             `json:"version"`
	Include          json.RawMessage `json:"include"`
	ConfigurePresets []struct {
		Name           string                     `json:"name"`
		Inherits       json.RawMessage            `json:"inherits"`
		CacheVariables map[string]json.RawMessage `json:"cacheVariables"`
		Environment    map[string]json.RawMessage `json:"environment"`
		ToolchainFile  json.RawMessage            `json:"toolchainFile"`
	} `json:"configurePresets"`
}

func (validator *cmakeLaunchValidator) validatePreset(sourceRoot, name string) error {
	if name == "" {
		return errInvalidCMakeLaunchDeclaration
	}
	presets := make(map[string]launchPreset)
	seenFiles := make(map[string]uint8)
	foundRoot := false
	for _, base := range []string{"CMakePresets.json", "CMakeUserPresets.json"} {
		path := filepath.Join(sourceRoot, base)
		if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
			continue
		} else if err != nil {
			return errInvalidCMakeLaunchDeclaration
		}
		foundRoot = true
		if err := validator.readPresetFile(path, sourceRoot, presets, seenFiles, 0); err != nil {
			return err
		}
	}
	if !foundRoot {
		return errInvalidCMakeLaunchDeclaration
	}
	resolved, err := resolveLaunchPreset(name, presets, make(map[string]bool), 0)
	if err != nil {
		return err
	}
	for variable, raw := range resolved.cache {
		if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			continue
		}
		value, err := launchPresetValue(raw)
		if err != nil {
			return errInvalidCMakeLaunchDeclaration
		}
		if isCMakeScriptLoaderVariable(variable) {
			if err := validator.validateScriptLoaderValue(sourceRoot, variable, value); err != nil {
				return err
			}
			continue
		}
		if isCompilerLauncherProperty(variable) || isRuleLauncherProperty(variable) || isPinnedCMakeToolVariable(variable) {
			if !validator.allowedBareExecutable(value, "") {
				return errInvalidCMakeLaunchDeclaration
			}
		}
	}
	for variable, raw := range resolved.environment {
		if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			continue
		}
		value, err := launchPresetValue(raw)
		if err != nil {
			return errInvalidCMakeLaunchDeclaration
		}
		if isCMakeScriptLoaderVariable(variable) {
			if err := validator.validateScriptLoaderValue(sourceRoot, variable, value); err != nil {
				return err
			}
			continue
		}
		if (isCompilerLauncherProperty(variable) || isRuleLauncherProperty(variable) || isPinnedCMakeToolVariable(variable)) &&
			!validator.allowedBareExecutable(value, "") {
			return errInvalidCMakeLaunchDeclaration
		}
	}
	if len(resolved.toolchainFile) != 0 && !bytes.Equal(bytes.TrimSpace(resolved.toolchainFile), []byte("null")) {
		value, err := launchPresetValue(resolved.toolchainFile)
		if err != nil || validator.validateToolchainFile(sourceRoot, value) != nil {
			return errInvalidCMakeLaunchDeclaration
		}
	}
	return nil
}

func (validator *cmakeLaunchValidator) readPresetFile(path, sourceRoot string, presets map[string]launchPreset, seen map[string]uint8, depth int) error {
	if depth > 16 {
		return errInvalidCMakeLaunchDeclaration
	}
	canonical, err := canonicalCMakeLaunchPath(path)
	if err != nil || !cmakeLaunchPathWithinRoots(canonical, []string{sourceRoot}) {
		return errInvalidCMakeLaunchDeclaration
	}
	key := cmakeLaunchPathKey(canonical)
	switch seen[key] {
	case 1:
		return errInvalidCMakeLaunchDeclaration
	case 2:
		return nil
	}
	seen[key] = 1
	content, err := validator.snapshotInput(canonical)
	if err != nil {
		return err
	}
	var document launchPresetDocument
	if json.Unmarshal(content, &document) != nil || document.Version < 1 || document.Version > 10 {
		return errInvalidCMakeLaunchDeclaration
	}
	includes, err := launchPresetStringList(document.Include)
	if err != nil {
		return errInvalidCMakeLaunchDeclaration
	}
	for _, include := range includes {
		if include == "" || strings.ContainsAny(include, "$;<>") {
			return errInvalidCMakeLaunchDeclaration
		}
		if err := validator.readPresetFile(filepath.Join(filepath.Dir(canonical), filepath.FromSlash(include)), sourceRoot, presets, seen, depth+1); err != nil {
			return err
		}
	}
	for _, value := range document.ConfigurePresets {
		if value.Name == "" || strings.ContainsAny(value.Name, "$;<>") {
			return errInvalidCMakeLaunchDeclaration
		}
		if _, duplicate := presets[value.Name]; duplicate {
			return errInvalidCMakeLaunchDeclaration
		}
		inherits, err := launchPresetStringList(value.Inherits)
		if err != nil {
			return errInvalidCMakeLaunchDeclaration
		}
		presets[value.Name] = launchPreset{name: value.Name, inherits: inherits, cache: value.CacheVariables, environment: value.Environment, toolchainFile: value.ToolchainFile}
	}
	seen[key] = 2
	return nil
}

func resolveLaunchPreset(name string, values map[string]launchPreset, visiting map[string]bool, depth int) (launchPreset, error) {
	value, ok := values[name]
	if !ok || visiting[name] || depth > 64 {
		return launchPreset{}, errInvalidCMakeLaunchDeclaration
	}
	visiting[name] = true
	result := launchPreset{name: value.name, cache: make(map[string]json.RawMessage), environment: make(map[string]json.RawMessage)}
	for _, parentName := range value.inherits {
		parent, err := resolveLaunchPreset(parentName, values, visiting, depth+1)
		if err != nil {
			return launchPreset{}, err
		}
		for key, raw := range parent.cache {
			if _, inherited := result.cache[key]; !inherited {
				result.cache[key] = append(json.RawMessage(nil), raw...)
			}
		}
		for key, raw := range parent.environment {
			if _, inherited := result.environment[key]; !inherited {
				result.environment[key] = append(json.RawMessage(nil), raw...)
			}
		}
		if len(result.toolchainFile) == 0 {
			result.toolchainFile = append(json.RawMessage(nil), parent.toolchainFile...)
		}
	}
	delete(visiting, name)
	for key, raw := range value.cache {
		result.cache[key] = append(json.RawMessage(nil), raw...)
	}
	for key, raw := range value.environment {
		result.environment[key] = append(json.RawMessage(nil), raw...)
	}
	if len(value.toolchainFile) != 0 {
		result.toolchainFile = append(json.RawMessage(nil), value.toolchainFile...)
	}
	return result, nil
}

func launchPresetStringList(raw json.RawMessage) ([]string, error) {
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, nil
	}
	var single string
	if json.Unmarshal(raw, &single) == nil {
		return []string{single}, nil
	}
	var values []string
	if json.Unmarshal(raw, &values) != nil {
		return nil, errInvalidCMakeLaunchDeclaration
	}
	return values, nil
}

func launchPresetValue(raw json.RawMessage) (string, error) {
	var value string
	if json.Unmarshal(raw, &value) == nil {
		return value, nil
	}
	var object map[string]json.RawMessage
	if json.Unmarshal(raw, &object) != nil || len(object) == 0 || len(object) > 2 {
		return "", errInvalidCMakeLaunchDeclaration
	}
	for key := range object {
		if key != "type" && key != "value" {
			return "", errInvalidCMakeLaunchDeclaration
		}
	}
	if json.Unmarshal(object["value"], &value) != nil {
		return "", errInvalidCMakeLaunchDeclaration
	}
	return value, nil
}

func (validator *cmakeLaunchValidator) validateToolchainFile(sourceRoot, value string) error {
	return validator.validateScriptLoaderValue(sourceRoot, "CMAKE_TOOLCHAIN_FILE", value)
}

func (validator *cmakeLaunchValidator) validateScriptLoaderValue(sourceRoot, variable, value string) error {
	if value == "" {
		return nil
	}
	if strings.ContainsAny(value, "$<>") {
		return errInvalidCMakeLaunchDeclaration
	}
	paths := strings.Split(value, ";")
	if strings.EqualFold(variable, "CMAKE_TOOLCHAIN_FILE") && len(paths) != 1 {
		return errInvalidCMakeLaunchDeclaration
	}
	for _, value := range paths {
		if value == "" || strings.TrimSpace(value) != value {
			return errInvalidCMakeLaunchDeclaration
		}
		path := filepath.FromSlash(value)
		if !filepath.IsAbs(path) {
			path = filepath.Join(sourceRoot, path)
		}
		if err := validator.validateFile(path, validator.binaryRoot); err != nil {
			return err
		}
	}
	return nil
}

func (validator *cmakeLaunchValidator) validateCommandGroups(invocation cmakeInvocation, sourceKey, mode string) error {
	commands := 0
	for index, argument := range invocation.arguments {
		if !strings.EqualFold(argument.value, "COMMAND") {
			continue
		}
		commands++
		end := len(invocation.arguments)
		for next := index + 2; next < len(invocation.arguments); next++ {
			if strings.EqualFold(invocation.arguments[next].value, "COMMAND") {
				end = next
				break
			}
		}
		if index+1 >= len(invocation.arguments) ||
			!validator.allowedCommandExecutable(
				invocation.arguments[index+1].value,
				invocation.arguments[index+2:end],
				sourceKey,
				mode,
			) {
			return errInvalidCMakeLaunchDeclaration
		}
	}
	if commands == 0 {
		return errInvalidCMakeLaunchDeclaration
	}
	return nil
}

func (validator *cmakeLaunchValidator) allowedCommandExecutable(value string, arguments []cmakeArgument, sourceKey, mode string) bool {
	executable := value
	targetCommand := false
	if resolved, ok := validator.variables[executable]; ok {
		executable = resolved
	} else if resolved, ok := validator.trustedFiles[sourceKey][executable]; ok {
		executable = resolved
	}
	if strings.HasPrefix(executable, "$<TARGET_FILE:") && strings.HasSuffix(executable, ">") {
		executable = strings.TrimSuffix(strings.TrimPrefix(executable, "$<TARGET_FILE:"), ">")
	}
	if target, ok := validator.targets[strings.ToLower(executable)]; ok {
		executable = target
		targetCommand = true
	}
	if strings.ContainsAny(executable, "$;<>") || !filepath.IsAbs(executable) {
		return false
	}
	key := cmakeLaunchPathKey(executable)
	if _, allowed := validator.allowed[key]; !allowed {
		return false
	}
	if key == validator.cmakePath {
		return len(arguments) >= 2 && arguments[0].value == "-E" &&
			(arguments[1].value == "touch" || arguments[1].value == "touch_nocreate")
	}
	if key == validator.ctestPath || strings.EqualFold(filepath.Base(executable), "ninja.exe") || strings.EqualFold(filepath.Base(executable), "ninja") {
		return false
	}
	if mode == "custom" {
		return false
	}
	if mode == "execute" {
		_, trusted := validator.trustedFiles[sourceKey][value]
		return trusted
	}
	if mode == "test" {
		if !targetCommand {
			_, targetCommand = validator.targetPaths[key]
		}
		return targetCommand
	}
	// cmd.exe is registered because Ninja may use it as its shell. It is never
	// an allowed custom-command executable because /c could start an undeclared
	// process outside this parser's exact command identity.
	return !strings.EqualFold(filepath.Base(executable), "cmd.exe")
}

func (validator *cmakeLaunchValidator) addAllowedExecutable(executable string, planned bool) {
	key := cmakeLaunchPathKey(executable)
	if _, exists := validator.allowed[key]; exists {
		return
	}
	validator.allowed[key] = struct{}{}
	if planned {
		validator.planned = append(validator.planned, filepath.Clean(executable))
	}
}

func (validator *cmakeLaunchValidator) declareExecutable(invocation cmakeInvocation, binaryDir string) error {
	if len(invocation.arguments) < 2 || !literalCMakeTargetName(invocation.arguments[0].value) {
		return errInvalidCMakeLaunchDeclaration
	}
	for _, argument := range invocation.arguments[1:] {
		if strings.EqualFold(argument.value, "IMPORTED") || strings.EqualFold(argument.value, "ALIAS") {
			return errInvalidCMakeLaunchDeclaration
		}
	}
	outputDir := binaryDir
	if validator.multiConfig && validator.configuration != "" {
		outputDir = filepath.Join(outputDir, validator.configuration)
	}
	executable := filepath.Join(outputDir, invocation.arguments[0].value+".exe")
	name := strings.ToLower(invocation.arguments[0].value)
	if existing, duplicate := validator.targets[name]; duplicate && !strings.EqualFold(existing, executable) {
		return errInvalidCMakeLaunchDeclaration
	}
	validator.targets[name] = executable
	validator.targetPaths[cmakeLaunchPathKey(executable)] = struct{}{}
	validator.addAllowedExecutable(executable, true)
	return nil
}

func literalCMakeTargetName(value string) bool {
	if value == "" || strings.ContainsAny(value, "$;<>/\\") {
		return false
	}
	for _, character := range value {
		if character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' ||
			character >= '0' && character <= '9' || strings.ContainsRune("_+.-", character) {
			continue
		}
		return false
	}
	return true
}

func (validator *cmakeLaunchValidator) validateInstrumentationOptions(invocation cmakeInvocation, sourceKey string) error {
	state, snapshotted := validator.snapshots[sourceKey]
	if !validator.instrumentationTrusted || sourceKey != validator.instrumentationPath || !snapshotted ||
		state.Identity == "" || cmakeLaunchPathKey(state.Path) != sourceKey ||
		state.SHA256 != validator.instrumentationDigest ||
		state.SHA256 != coveragellvm.InstrumentationSHA256() {
		return errInvalidCMakeLaunchDeclaration
	}
	var expected []string
	switch strings.ToLower(invocation.name) {
	case "add_compile_options":
		expected = []string{
			"$<$<COMPILE_LANGUAGE:C,CXX>:-fprofile-instr-generate>",
			"$<$<COMPILE_LANGUAGE:C,CXX>:-fcoverage-mapping>",
		}
	case "add_link_options":
		expected = []string{"-fprofile-instr-generate"}
	default:
		return errInvalidCMakeLaunchDeclaration
	}
	if len(invocation.arguments) != len(expected) {
		return errInvalidCMakeLaunchDeclaration
	}
	for index, argument := range invocation.arguments {
		if argument.value != expected[index] {
			return errInvalidCMakeLaunchDeclaration
		}
	}
	return nil
}

func (validator *cmakeLaunchValidator) validateProject(invocation cmakeInvocation, sourceKey string) error {
	if len(invocation.arguments) == 0 || !literalCMakeTargetName(invocation.arguments[0].value) {
		return errInvalidCMakeLaunchDeclaration
	}
	languagesDeclared := false
	for index := 1; index < len(invocation.arguments); {
		keyword := strings.ToUpper(invocation.arguments[index].value)
		switch keyword {
		case "VERSION":
			if index+1 >= len(invocation.arguments) || !literalCMakeProjectVersion(invocation.arguments[index+1].value) {
				return errInvalidCMakeLaunchDeclaration
			}
			index += 2
		case "DESCRIPTION", "HOMEPAGE_URL":
			if index+1 >= len(invocation.arguments) ||
				strings.ContainsAny(invocation.arguments[index+1].value, "$;<>") {
				return errInvalidCMakeLaunchDeclaration
			}
			index += 2
		case "LANGUAGES":
			if languagesDeclared || index+1 >= len(invocation.arguments) {
				return errInvalidCMakeLaunchDeclaration
			}
			languagesDeclared = true
			for _, language := range invocation.arguments[index+1:] {
				if !validator.registeredProjectLanguage(language.value, sourceKey) {
					return errInvalidCMakeLaunchDeclaration
				}
			}
			return nil
		default:
			return errInvalidCMakeLaunchDeclaration
		}
	}
	if !validator.registeredProjectLanguage("C", sourceKey) ||
		!validator.registeredProjectLanguage("CXX", sourceKey) {
		return errInvalidCMakeLaunchDeclaration
	}
	return nil
}

func (validator *cmakeLaunchValidator) registeredProjectLanguage(language, sourceKey string) bool {
	switch strings.ToUpper(language) {
	case "C":
		return validator.allowedBareExecutable("${CMAKE_C_COMPILER}", sourceKey)
	case "CXX":
		return validator.allowedBareExecutable("${CMAKE_CXX_COMPILER}", sourceKey)
	default:
		return false
	}
}

func literalCMakeProjectVersion(value string) bool {
	if value == "" || strings.Trim(value, "0123456789.") != "" ||
		strings.HasPrefix(value, ".") || strings.HasSuffix(value, ".") || strings.Contains(value, "..") {
		return false
	}
	return true
}

func (validator *cmakeLaunchValidator) validateSetLaunchProperty(invocation cmakeInvocation, sourceKey string) error {
	if len(invocation.arguments) == 0 {
		return nil
	}
	if strings.ContainsAny(invocation.arguments[0].value, "$;<>") {
		if _, trusted := validator.trustedFiles[sourceKey]; trusted {
			return nil
		}
		return errInvalidCMakeLaunchDeclaration
	}
	if isPinnedCMakeToolVariable(invocation.arguments[0].value) {
		if len(invocation.arguments) != 2 ||
			!validator.allowedBareExecutable(invocation.arguments[1].value, sourceKey) {
			return errInvalidCMakeLaunchDeclaration
		}
		return nil
	}
	if isCMakeScriptLoaderVariable(invocation.arguments[0].value) {
		if len(invocation.arguments) == 1 {
			return nil
		}
		if len(invocation.arguments) != 2 {
			return errInvalidCMakeLaunchDeclaration
		}
		return validator.validateScriptLoaderValue(filepath.Dir(sourceKey), invocation.arguments[0].value, invocation.arguments[1].value)
	}
	if !isCompilerLauncherProperty(invocation.arguments[0].value) {
		return nil
	}
	return validator.validateLaunchProperty(invocation.arguments[0].value, invocation.arguments[1:], sourceKey)
}

func (validator *cmakeLaunchValidator) validateListLaunchMutation(invocation cmakeInvocation, sourceKey string) error {
	involvesControlled := false
	for _, argument := range invocation.arguments {
		if strings.ContainsAny(argument.value, "$<>") {
			return errInvalidCMakeLaunchDeclaration
		}
		if mentionsControlledCMakeListVariable(argument.value) {
			involvesControlled = true
			break
		}
	}
	if !involvesControlled {
		return nil
	}
	if len(invocation.arguments) < 2 || !isControlledCMakeListVariable(invocation.arguments[1].value) {
		return errInvalidCMakeLaunchDeclaration
	}
	variable := invocation.arguments[1].value
	if !isCMakeScriptLoaderVariable(variable) {
		return errInvalidCMakeLaunchDeclaration
	}
	operation := strings.ToUpper(invocation.arguments[0].value)
	switch operation {
	case "APPEND", "PREPEND":
		return validator.validateListScriptValues(filepath.Dir(sourceKey), variable, invocation.arguments[2:])
	case "INSERT":
		if len(invocation.arguments) < 4 || !literalCMakeListIndex(invocation.arguments[2].value) {
			return errInvalidCMakeLaunchDeclaration
		}
		return validator.validateListScriptValues(filepath.Dir(sourceKey), variable, invocation.arguments[3:])
	case "REMOVE_ITEM":
		return validator.validateListScriptValues(filepath.Dir(sourceKey), variable, invocation.arguments[2:])
	case "REMOVE_AT":
		if len(invocation.arguments) < 3 {
			return errInvalidCMakeLaunchDeclaration
		}
		for _, argument := range invocation.arguments[2:] {
			if !literalCMakeListIndex(argument.value) {
				return errInvalidCMakeLaunchDeclaration
			}
		}
		return nil
	case "POP_BACK", "POP_FRONT", "REMOVE_DUPLICATES", "REVERSE", "SORT":
		if len(invocation.arguments) != 2 {
			return errInvalidCMakeLaunchDeclaration
		}
		return nil
	default:
		return errInvalidCMakeLaunchDeclaration
	}
}

func validateClosedVariableWriter(invocation cmakeInvocation) error {
	for _, argument := range invocation.arguments {
		if strings.ContainsAny(argument.value, "$<>") || mentionsControlledCMakeListVariable(argument.value) {
			return errInvalidCMakeLaunchDeclaration
		}
	}
	return nil
}

func (validator *cmakeLaunchValidator) validateListScriptValues(sourceRoot, variable string, values []cmakeArgument) error {
	for _, value := range values {
		if err := validator.validateScriptLoaderValue(sourceRoot, variable, value.value); err != nil {
			return err
		}
	}
	return nil
}

func literalCMakeListIndex(value string) bool {
	if value == "" || strings.TrimSpace(value) != value {
		return false
	}
	_, err := strconv.Atoi(value)
	return err == nil
}

func (validator *cmakeLaunchValidator) validateSetProperty(invocation cmakeInvocation, sourceKey string) error {
	for index, argument := range invocation.arguments {
		if strings.EqualFold(argument.value, "PROPERTY") {
			if index+1 >= len(invocation.arguments) {
				return errInvalidCMakeLaunchDeclaration
			}
			if strings.ContainsAny(invocation.arguments[index+1].value, "$;<>") {
				return errInvalidCMakeLaunchDeclaration
			}
			return validator.validateLaunchProperty(invocation.arguments[index+1].value, invocation.arguments[index+2:], sourceKey)
		}
	}
	return nil
}

func (validator *cmakeLaunchValidator) validateTargetProperties(invocation cmakeInvocation, sourceKey string) error {
	properties := -1
	for index, argument := range invocation.arguments {
		if strings.EqualFold(argument.value, "PROPERTIES") {
			properties = index + 1
			break
		}
	}
	if properties < 0 {
		return nil
	}
	if (len(invocation.arguments)-properties)%2 != 0 {
		return errInvalidCMakeLaunchDeclaration
	}
	for index := properties; index < len(invocation.arguments); index += 2 {
		if strings.ContainsAny(invocation.arguments[index].value, "$;<>") {
			return errInvalidCMakeLaunchDeclaration
		}
		if err := validator.validateLaunchProperty(invocation.arguments[index].value, invocation.arguments[index+1:index+2], sourceKey); err != nil {
			return err
		}
	}
	return nil
}

func (validator *cmakeLaunchValidator) validateDirectoryProperties(invocation cmakeInvocation, sourceKey string) error {
	if len(invocation.arguments) == 0 || !strings.EqualFold(invocation.arguments[0].value, "PROPERTIES") ||
		(len(invocation.arguments)-1)%2 != 0 {
		return errInvalidCMakeLaunchDeclaration
	}
	for index := 1; index < len(invocation.arguments); index += 2 {
		if strings.ContainsAny(invocation.arguments[index].value, "$;<>") {
			return errInvalidCMakeLaunchDeclaration
		}
		if err := validator.validateLaunchProperty(invocation.arguments[index].value, invocation.arguments[index+1:index+2], sourceKey); err != nil {
			return err
		}
	}
	return nil
}

func (validator *cmakeLaunchValidator) validateLaunchProperty(name string, values []cmakeArgument, sourceKey string) error {
	if !isCompilerLauncherProperty(name) && !isRuleLauncherProperty(name) {
		return nil
	}
	if len(values) == 0 || len(values) == 1 && values[0].value == "" {
		return nil
	}
	if len(values) != 1 || !validator.allowedBareExecutable(values[0].value, sourceKey) {
		return errInvalidCMakeLaunchDeclaration
	}
	return nil
}

func (validator *cmakeLaunchValidator) allowedBareExecutable(value, sourceKey string) bool {
	executable := value
	if resolved, ok := validator.variables[executable]; ok {
		executable = resolved
	} else if resolved, ok := validator.trustedFiles[sourceKey][executable]; ok {
		executable = resolved
	}
	if strings.ContainsAny(executable, "$;<>\t\r\n") || !filepath.IsAbs(executable) ||
		strings.EqualFold(filepath.Base(executable), "cmd.exe") {
		return false
	}
	_, allowed := validator.allowed[cmakeLaunchPathKey(executable)]
	return allowed
}

func isCompilerLauncherProperty(value string) bool {
	upper := strings.ToUpper(value)
	if strings.HasPrefix(upper, "CMAKE_") {
		upper = strings.TrimPrefix(upper, "CMAKE_")
	}
	for _, suffix := range []string{"_COMPILER_LAUNCHER", "_LINKER_LAUNCHER"} {
		if strings.HasSuffix(upper, suffix) && len(strings.TrimSuffix(upper, suffix)) > 0 {
			return true
		}
	}
	return false
}

func isPinnedCMakeToolVariable(value string) bool {
	upper := strings.ToUpper(value)
	switch upper {
	case "CMAKE_MAKE_PROGRAM", "CMAKE_LINKER", "CMAKE_AR", "CMAKE_RANLIB":
		return true
	}
	if !strings.HasPrefix(upper, "CMAKE_") {
		return false
	}
	languageTool := strings.TrimPrefix(upper, "CMAKE_")
	for _, suffix := range []string{"_COMPILER", "_LINKER"} {
		if strings.HasSuffix(languageTool, suffix) && len(strings.TrimSuffix(languageTool, suffix)) > 0 {
			return true
		}
	}
	return false
}

func isCMakeScriptLoaderVariable(value string) bool {
	upper := strings.ToUpper(value)
	switch upper {
	case "CMAKE_TOOLCHAIN_FILE",
		"CMAKE_PROJECT_TOP_LEVEL_INCLUDES",
		"CMAKE_PROJECT_INCLUDE",
		"CMAKE_PROJECT_INCLUDE_BEFORE",
		"CMAKE_USER_MAKE_RULES_OVERRIDE":
		return true
	}
	if strings.HasPrefix(upper, "CMAKE_USER_MAKE_RULES_OVERRIDE_") {
		return len(strings.TrimPrefix(upper, "CMAKE_USER_MAKE_RULES_OVERRIDE_")) > 0
	}
	if !strings.HasPrefix(upper, "CMAKE_PROJECT_") {
		return false
	}
	projectVariable := strings.TrimPrefix(upper, "CMAKE_PROJECT_")
	for _, suffix := range []string{"_INCLUDE", "_INCLUDE_BEFORE"} {
		if strings.HasSuffix(projectVariable, suffix) && len(strings.TrimSuffix(projectVariable, suffix)) > 0 {
			return true
		}
	}
	return false
}

func isControlledCMakeListVariable(value string) bool {
	return isCMakeScriptLoaderVariable(value) || isCompilerLauncherProperty(value) ||
		isRuleLauncherProperty(value) || isPinnedCMakeToolVariable(value)
}

func mentionsControlledCMakeListVariable(value string) bool {
	if isControlledCMakeListVariable(value) {
		return true
	}
	upper := strings.ToUpper(value)
	return strings.Contains(upper, "CMAKE_PROJECT_") ||
		strings.Contains(upper, "CMAKE_TOOLCHAIN_FILE") ||
		strings.Contains(upper, "CMAKE_USER_MAKE_RULES_OVERRIDE") ||
		strings.Contains(upper, "CMAKE_MAKE_PROGRAM") ||
		strings.Contains(upper, "CMAKE_LINKER") ||
		strings.Contains(upper, "CMAKE_AR") ||
		strings.Contains(upper, "CMAKE_RANLIB") ||
		strings.Contains(upper, "CMAKE_") &&
			(strings.Contains(upper, "_COMPILER") || strings.Contains(upper, "_LINKER")) ||
		strings.Contains(upper, "_COMPILER_LAUNCHER") ||
		strings.Contains(upper, "_LINKER_LAUNCHER") ||
		strings.Contains(upper, "RULE_LAUNCH_")
}

func isRuleLauncherProperty(value string) bool {
	switch strings.ToUpper(value) {
	case "RULE_LAUNCH_COMPILE", "RULE_LAUNCH_LINK", "RULE_LAUNCH_CUSTOM":
		return true
	default:
		return false
	}
}

func canonicalCMakeLaunchPath(path string) (string, error) {
	if path == "" || !filepath.IsAbs(path) {
		return "", errInvalidCMakeLaunchDeclaration
	}
	canonical, err := filepath.EvalSymlinks(filepath.Clean(path))
	if err != nil || !filepath.IsAbs(canonical) {
		return "", errInvalidCMakeLaunchDeclaration
	}
	return filepath.Clean(canonical), nil
}

func resolveLiteralCMakeLaunchPath(base, value string) (string, error) {
	if value == "" || strings.ContainsAny(value, "$;<>") {
		return "", errInvalidCMakeLaunchDeclaration
	}
	if !filepath.IsAbs(value) {
		value = filepath.Join(base, filepath.FromSlash(value))
	}
	return filepath.Clean(value), nil
}

func cmakeLaunchPathWithinRoots(path string, roots []string) bool {
	for _, root := range roots {
		relative, err := filepath.Rel(root, path)
		if err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) && !filepath.IsAbs(relative) {
			return true
		}
	}
	return false
}

func cmakeLaunchPathKey(path string) string {
	return strings.ToLower(filepath.Clean(path))
}

type cmakeParser struct {
	content       []byte
	offset        int
	commandCount  int
	argumentCount int
}

func parseCMakeInvocations(content []byte) ([]cmakeInvocation, error) {
	parser := &cmakeParser{content: content}
	if len(parser.content) >= 3 && string(parser.content[:3]) == "\xef\xbb\xbf" {
		parser.offset = 3
	}
	var result []cmakeInvocation
	for {
		if err := parser.skipSpaceAndComments(); err != nil {
			return nil, err
		}
		if parser.offset == len(parser.content) {
			return result, nil
		}
		name := parser.readIdentifier()
		if name == "" || parser.commandCount >= maxCMakeLaunchCommands {
			return nil, errInvalidCMakeLaunchDeclaration
		}
		parser.commandCount++
		if err := parser.skipSpaceAndComments(); err != nil || parser.take() != '(' {
			return nil, errInvalidCMakeLaunchDeclaration
		}
		arguments, err := parser.readArguments()
		if err != nil {
			return nil, err
		}
		result = append(result, cmakeInvocation{name: name, arguments: arguments})
	}
}

func (parser *cmakeParser) readArguments() ([]cmakeArgument, error) {
	depth := 1
	var result []cmakeArgument
	for depth > 0 {
		if err := parser.skipSpaceAndComments(); err != nil || parser.offset >= len(parser.content) {
			return nil, errInvalidCMakeLaunchDeclaration
		}
		switch parser.content[parser.offset] {
		case '(':
			parser.offset++
			depth++
			continue
		case ')':
			parser.offset++
			depth--
			continue
		case '"':
			value, err := parser.readQuotedArgument()
			if err != nil {
				return nil, err
			}
			result = append(result, cmakeArgument{value: value})
		default:
			if value, ok, err := parser.readBracketArgument(); err != nil {
				return nil, err
			} else if ok {
				result = append(result, cmakeArgument{value: value})
			} else {
				value, err := parser.readUnquotedArgument()
				if err != nil {
					return nil, err
				}
				result = append(result, cmakeArgument{value: value})
			}
		}
		parser.argumentCount++
		if parser.argumentCount > maxCMakeLaunchArguments {
			return nil, errInvalidCMakeLaunchDeclaration
		}
	}
	return result, nil
}

func (parser *cmakeParser) readIdentifier() string {
	start := parser.offset
	for parser.offset < len(parser.content) {
		character := parser.content[parser.offset]
		if character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' || character == '_' ||
			parser.offset > start && character >= '0' && character <= '9' {
			parser.offset++
			continue
		}
		break
	}
	return string(parser.content[start:parser.offset])
}

func (parser *cmakeParser) readQuotedArgument() (string, error) {
	parser.offset++
	var result strings.Builder
	for parser.offset < len(parser.content) {
		character := parser.content[parser.offset]
		parser.offset++
		if character == '"' {
			return result.String(), nil
		}
		if character == '\\' {
			if parser.offset >= len(parser.content) {
				return "", errInvalidCMakeLaunchDeclaration
			}
			character = parser.content[parser.offset]
			parser.offset++
			if character == '\r' && parser.offset < len(parser.content) && parser.content[parser.offset] == '\n' {
				parser.offset++
				continue
			}
			if character == '\n' {
				continue
			}
		}
		if character == 0 {
			return "", errInvalidCMakeLaunchDeclaration
		}
		result.WriteByte(character)
	}
	return "", errInvalidCMakeLaunchDeclaration
}

func (parser *cmakeParser) readUnquotedArgument() (string, error) {
	var result strings.Builder
	for parser.offset < len(parser.content) {
		character := parser.content[parser.offset]
		if isCMakeSpace(character) || character == '(' || character == ')' {
			break
		}
		parser.offset++
		if character == '\\' {
			if parser.offset >= len(parser.content) {
				return "", errInvalidCMakeLaunchDeclaration
			}
			character = parser.content[parser.offset]
			parser.offset++
		}
		if character == 0 {
			return "", errInvalidCMakeLaunchDeclaration
		}
		result.WriteByte(character)
	}
	if result.Len() == 0 {
		return "", errInvalidCMakeLaunchDeclaration
	}
	return result.String(), nil
}

func (parser *cmakeParser) skipSpaceAndComments() error {
	for parser.offset < len(parser.content) {
		if isCMakeSpace(parser.content[parser.offset]) {
			parser.offset++
			continue
		}
		if parser.content[parser.offset] != '#' {
			return nil
		}
		parser.offset++
		if _, ok, err := parser.readBracketArgument(); err != nil {
			return err
		} else if ok {
			continue
		}
		for parser.offset < len(parser.content) && parser.content[parser.offset] != '\n' {
			parser.offset++
		}
	}
	return nil
}

func (parser *cmakeParser) readBracketArgument() (string, bool, error) {
	if parser.offset >= len(parser.content) || parser.content[parser.offset] != '[' {
		return "", false, nil
	}
	start := parser.offset
	parser.offset++
	equals := 0
	for parser.offset < len(parser.content) && parser.content[parser.offset] == '=' {
		equals++
		parser.offset++
	}
	if parser.offset >= len(parser.content) || parser.content[parser.offset] != '[' {
		parser.offset = start
		return "", false, nil
	}
	parser.offset++
	body := parser.offset
	closing := "]" + strings.Repeat("=", equals) + "]"
	index := strings.Index(string(parser.content[body:]), closing)
	if index < 0 {
		return "", false, errInvalidCMakeLaunchDeclaration
	}
	value := string(parser.content[body : body+index])
	parser.offset = body + index + len(closing)
	return value, true, nil
}

func (parser *cmakeParser) take() byte {
	if parser.offset >= len(parser.content) {
		return 0
	}
	value := parser.content[parser.offset]
	parser.offset++
	return value
}

func isCMakeSpace(value byte) bool {
	return value == ' ' || value == '\t' || value == '\r' || value == '\n'
}
