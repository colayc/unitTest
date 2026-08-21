package build

import (
	"errors"
	"path/filepath"
	"sort"
	"strings"

	"unit-test-ide.local/test-service/internal/cmake"
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
	value    string
	unquoted bool
}

type cmakeInvocation struct {
	name      string
	arguments []cmakeArgument
}

type cmakeLaunchValidator struct {
	allowed      map[string]struct{}
	variables    map[string]string
	targets      map[string]string
	cmakePath    string
	allowedRoots []string
	visited      map[string]struct{}
	states       []cmake.FingerprintFile
	trustedFiles map[string]map[string]string
	files        int
	totalBytes   int64
}

// validateCMakeLaunchPlan proves every configure/build process entry in the
// supported, statically reachable CMake graph is already in the APP_ID launch
// declaration. Anything that cannot be resolved without running CMake is
// rejected before process creation.
func validateCMakeLaunchPlan(input PlanInput, sourceDir string, launchPlan []string) ([]cmake.FingerprintFile, error) {
	sourceRoot, err := canonicalCMakeLaunchPath(sourceDir)
	if err != nil {
		return nil, errInvalidCMakeLaunchDeclaration
	}
	validator := &cmakeLaunchValidator{
		allowed: make(map[string]struct{}, len(launchPlan)),
		variables: map[string]string{
			"${CMAKE_COMMAND}":       input.Installation.Executable,
			"${CMAKE_CTEST_COMMAND}": input.Installation.CTestExecutable,
			"${CMAKE_C_COMPILER}":    input.Toolchain.CCompiler,
			"${CMAKE_CXX_COMPILER}":  input.Toolchain.CXXCompiler,
		},
		cmakePath:    cmakeLaunchPathKey(input.Installation.Executable),
		targets:      make(map[string]string, len(input.Targets)),
		allowedRoots: []string{sourceRoot},
		visited:      make(map[string]struct{}),
		trustedFiles: make(map[string]map[string]string),
	}
	for _, executable := range launchPlan {
		if !filepath.IsAbs(executable) {
			return nil, errInvalidCMakeLaunchDeclaration
		}
		validator.allowed[cmakeLaunchPathKey(executable)] = struct{}{}
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
				return nil, errInvalidCMakeLaunchDeclaration
			}
			executable = filepath.Clean(artifact)
		}
		if executable != "" {
			key := strings.ToLower(target.Name)
			if previous, duplicate := validator.targets[key]; duplicate && !strings.EqualFold(previous, executable) {
				return nil, errInvalidCMakeLaunchDeclaration
			}
			validator.targets[key] = executable
		}
	}
	if input.Coverage != nil {
		include, includeErr := canonicalCMakeLaunchPath(input.Coverage.TopLevelInclude.Path)
		if includeErr != nil {
			return nil, errInvalidCMakeLaunchDeclaration
		}
		validator.allowedRoots = append(validator.allowedRoots, filepath.Dir(include))
		if input.Installation.UnityRunnerGenerator.Valid() {
			validator.trustedFiles[cmakeLaunchPathKey(include)] = map[string]string{
				"${generator}": input.Installation.UnityRunnerGenerator.Path,
			}
		}
		if err := validator.validateFile(include); err != nil {
			return nil, err
		}
	}
	if err := validator.validateFile(filepath.Join(sourceRoot, "CMakeLists.txt")); err != nil {
		return nil, err
	}
	sort.Slice(validator.states, func(i, j int) bool { return validator.states[i].Path < validator.states[j].Path })
	return append([]cmake.FingerprintFile(nil), validator.states...), nil
}

func (validator *cmakeLaunchValidator) validateFile(path string) error {
	canonical, err := canonicalCMakeLaunchPath(path)
	if err != nil || !cmakeLaunchPathWithinRoots(canonical, validator.allowedRoots) {
		return errInvalidCMakeLaunchDeclaration
	}
	key := cmakeLaunchPathKey(canonical)
	if _, seen := validator.visited[key]; seen {
		return nil
	}
	if validator.files >= maxCMakeLaunchFiles {
		return errInvalidCMakeLaunchDeclaration
	}
	state, content, snapshotErr := cmake.SnapshotLaunchInput(canonical, maxCMakeLaunchFileBytes)
	if snapshotErr != nil || validator.totalBytes > maxCMakeLaunchTotalBytes-int64(len(content)) {
		return errInvalidCMakeLaunchDeclaration
	}
	validator.files++
	validator.totalBytes += int64(len(content))
	validator.visited[key] = struct{}{}
	validator.states = append(validator.states, state)
	invocations, err := parseCMakeInvocations(content)
	if err != nil {
		return errInvalidCMakeLaunchDeclaration
	}
	for _, invocation := range invocations {
		switch strings.ToLower(invocation.name) {
		case "add_custom_command", "add_custom_target", "add_test":
			if err := validator.validateCommandGroups(invocation, key); err != nil {
				return err
			}
		case "execute_process":
			if err := validator.validateCommandGroups(invocation, key); err != nil {
				return err
			}
		case "try_run", "try_compile":
			return errInvalidCMakeLaunchDeclaration
		case "cmake_language":
			if len(invocation.arguments) != 0 &&
				(strings.EqualFold(invocation.arguments[0].value, "EVAL") ||
					strings.EqualFold(invocation.arguments[0].value, "CALL")) {
				return errInvalidCMakeLaunchDeclaration
			}
		case "set":
			if err := validator.validateSetLaunchProperty(invocation, key); err != nil {
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
		case "add_subdirectory":
			if len(invocation.arguments) == 0 {
				return errInvalidCMakeLaunchDeclaration
			}
			directory, err := resolveLiteralCMakeLaunchPath(filepath.Dir(canonical), invocation.arguments[0].value)
			if err != nil || validator.validateFile(filepath.Join(directory, "CMakeLists.txt")) != nil {
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
			if err := validator.validateFile(include); err != nil {
				return errInvalidCMakeLaunchDeclaration
			}
		}
	}
	return nil
}

func (validator *cmakeLaunchValidator) validateCommandGroups(invocation cmakeInvocation, sourceKey string) error {
	commands := 0
	for index, argument := range invocation.arguments {
		if !argument.unquoted || !strings.EqualFold(argument.value, "COMMAND") {
			continue
		}
		commands++
		end := len(invocation.arguments)
		for next := index + 2; next < len(invocation.arguments); next++ {
			if invocation.arguments[next].unquoted && strings.EqualFold(invocation.arguments[next].value, "COMMAND") {
				end = next
				break
			}
		}
		if index+1 >= len(invocation.arguments) ||
			!validator.allowedCommandExecutable(
				invocation.arguments[index+1].value,
				invocation.arguments[index+2:end],
				sourceKey,
			) {
			return errInvalidCMakeLaunchDeclaration
		}
	}
	if commands == 0 {
		return errInvalidCMakeLaunchDeclaration
	}
	return nil
}

func (validator *cmakeLaunchValidator) allowedCommandExecutable(value string, arguments []cmakeArgument, sourceKey string) bool {
	executable := value
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
	// cmd.exe is registered because Ninja may use it as its shell. It is never
	// an allowed custom-command executable because /c could start an undeclared
	// process outside this parser's exact command identity.
	return !strings.EqualFold(filepath.Base(executable), "cmd.exe")
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
	if !isCompilerLauncherProperty(invocation.arguments[0].value) {
		return nil
	}
	return validator.validateLaunchProperty(invocation.arguments[0].value, invocation.arguments[1:], sourceKey)
}

func (validator *cmakeLaunchValidator) validateSetProperty(invocation cmakeInvocation, sourceKey string) error {
	for index, argument := range invocation.arguments {
		if argument.unquoted && strings.EqualFold(argument.value, "PROPERTY") {
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
		if argument.unquoted && strings.EqualFold(argument.value, "PROPERTIES") {
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
				result = append(result, cmakeArgument{value: value, unquoted: true})
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
