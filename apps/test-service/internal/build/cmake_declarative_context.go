package build

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

type cmakeDirectoryContext struct {
	SourceRoot string
	BinaryRoot string
	SourceDir  string
	BinaryDir  string
	ListDir    string
}

func (validator *cmakeLaunchValidator) validateFile(path, binaryDir string) error {
	context := cmakeDirectoryContext{SourceRoot: validator.allowedRoots[0], BinaryRoot: validator.binaryRoot,
		SourceDir: validator.allowedRoots[0], BinaryDir: binaryDir, ListDir: filepath.Dir(path)}
	return validator.validateFileContext(path, context)
}

func isDeclarativeDirectoryVariable(value string) bool {
	switch strings.ToUpper(value) {
	case "CMAKE_SOURCE_DIR", "CMAKE_CURRENT_SOURCE_DIR", "CMAKE_BINARY_DIR", "CMAKE_CURRENT_BINARY_DIR", "CMAKE_CURRENT_LIST_DIR":
		return true
	}
	return false
}

// Only one exact, leading directory variable is expanded. Everything following
// it is a literal path; no CMake evaluation or environment lookup is performed.
func (validator *cmakeLaunchValidator) resolveDeclarativePath(value string, context cmakeDirectoryContext) (string, error) {
	if value == "" || strings.ContainsAny(value, ";<>\r\n\t\x00") {
		return "", errInvalidCMakeLaunchDeclaration
	}
	if strings.HasPrefix(value, "${") {
		end := strings.IndexByte(value, '}')
		if end < 0 {
			return "", errInvalidCMakeLaunchDeclaration
		}
		variable, suffix := value[:end+1], value[end+1:]
		var base string
		switch variable {
		case "${CMAKE_SOURCE_DIR}":
			base = context.SourceRoot
		case "${CMAKE_CURRENT_SOURCE_DIR}":
			base = context.SourceDir
		case "${CMAKE_BINARY_DIR}":
			base = context.BinaryRoot
		case "${CMAKE_CURRENT_BINARY_DIR}":
			base = context.BinaryDir
		case "${CMAKE_CURRENT_LIST_DIR}":
			base = context.ListDir
		default:
			return "", errInvalidCMakeLaunchDeclaration
		}
		if suffix != "" && !strings.HasPrefix(suffix, "/") && !strings.HasPrefix(suffix, "\\") {
			return "", errInvalidCMakeLaunchDeclaration
		}
		value = base + suffix
	}
	path, err := resolveLiteralCMakeLaunchPath(context.SourceDir, value)
	roots := append(append([]string(nil), validator.allowedRoots...), context.BinaryRoot)
	if err != nil || !cmakeLaunchPathWithinRoots(path, roots) {
		return "", errInvalidCMakeLaunchDeclaration
	}
	// Binary output directories may not exist yet. Validate their nearest
	// existing ancestor rather than permitting a junction/symlink alias.
	ancestor := path
	for {
		if _, statErr := os.Lstat(ancestor); statErr == nil {
			canonical, canonicalErr := canonicalCMakeLaunchPath(ancestor)
			if canonicalErr != nil || cmakeLaunchPathKey(canonical) != cmakeLaunchPathKey(ancestor) ||
				!cmakeLaunchPathWithinRoots(canonical, roots) {
				return "", errInvalidCMakeLaunchDeclaration
			}
			return path, nil
		} else if !errors.Is(statErr, os.ErrNotExist) {
			return "", errInvalidCMakeLaunchDeclaration
		}
		parent := filepath.Dir(ancestor)
		if parent == ancestor {
			return "", errInvalidCMakeLaunchDeclaration
		}
		ancestor = parent
	}
}

func (validator *cmakeLaunchValidator) validateInclude(invocation cmakeInvocation, context cmakeDirectoryContext) error {
	if len(invocation.arguments) != 1 {
		return errInvalidCMakeLaunchDeclaration
	}
	path, err := validator.resolveDeclarativePath(invocation.arguments[0].value, context)
	if err != nil || !strings.EqualFold(filepath.Ext(path), ".cmake") {
		return errInvalidCMakeLaunchDeclaration
	}
	return validator.validateFileContext(path, context)
}

func (validator *cmakeLaunchValidator) validateSubdirectory(invocation cmakeInvocation, context cmakeDirectoryContext) error {
	args := invocation.arguments
	if len(args) < 1 || len(args) > 3 {
		return errInvalidCMakeLaunchDeclaration
	}
	if strings.EqualFold(args[len(args)-1].value, "EXCLUDE_FROM_ALL") {
		args = args[:len(args)-1]
	}
	if len(args) < 1 || len(args) > 2 {
		return errInvalidCMakeLaunchDeclaration
	}
	for _, arg := range args {
		if strings.EqualFold(arg.value, "SYSTEM") || strings.EqualFold(arg.value, "EXCLUDE_FROM_ALL") {
			return errInvalidCMakeLaunchDeclaration
		}
	}
	source, err := validator.resolveDeclarativePath(args[0].value, context)
	if err != nil {
		return err
	}
	child := context
	child.SourceDir, child.ListDir = source, source
	binaryValue := args[0].value
	if len(args) == 2 {
		binaryValue = args[1].value
	} else {
		// For a source within this directory, CMake derives the same relative
		// child output even when the source was specified with an absolute base.
		binaryValue, err = filepath.Rel(context.SourceDir, source)
		if err != nil || !cmakeLaunchPathWithinRoots(source, []string{context.SourceDir}) {
			return errInvalidCMakeLaunchDeclaration
		}
	}
	binaryContext := context
	binaryContext.SourceDir = context.BinaryDir
	child.BinaryDir, err = validator.resolveDeclarativePath(binaryValue, binaryContext)
	if err != nil || !cmakeLaunchPathWithinRoots(child.BinaryDir, []string{context.BinaryRoot}) {
		return errInvalidCMakeLaunchDeclaration
	}
	return validator.validateFileContext(filepath.Join(source, "CMakeLists.txt"), child)
}

// Legacy script-loader declarations keep their existing list rules, but their
// recursive interpretation uses the lexical caller's directory context.
func (validator *cmakeLaunchValidator) validateScriptLoaderContext(variable, value string, context cmakeDirectoryContext) error {
	if value == "" {
		return nil
	}
	paths := strings.Split(value, ";")
	if strings.EqualFold(variable, "CMAKE_TOOLCHAIN_FILE") && len(paths) != 1 {
		return errInvalidCMakeLaunchDeclaration
	}
	for _, value := range paths {
		path, err := validator.resolveDeclarativePath(value, context)
		if err != nil || validator.validateFileContext(path, context) != nil {
			return errInvalidCMakeLaunchDeclaration
		}
	}
	return nil
}

func (validator *cmakeLaunchValidator) validateListScriptContext(variable string, values []cmakeArgument, context cmakeDirectoryContext) error {
	for _, value := range values {
		if err := validator.validateScriptLoaderContext(variable, value.value, context); err != nil {
			return err
		}
	}
	return nil
}
