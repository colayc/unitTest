package build

import (
	"os"
	"regexp"
	"strings"
)

type cmakeScopedArguments struct {
	Scope  string
	Values []cmakeArgument
}

var closedCompileDefinition = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*(=([+-]?[0-9]+|[A-Za-z_][A-Za-z0-9_]*))?$`)

func validClosedCompileDefinition(value string) bool {
	return closedCompileDefinition.MatchString(value)
}

func parseScopedCMakeArguments(arguments []cmakeArgument) ([]cmakeScopedArguments, error) {
	if len(arguments) < 3 || !literalCMakeTargetName(arguments[0].value) {
		return nil, errInvalidCMakeLaunchDeclaration
	}
	var result []cmakeScopedArguments
	for _, argument := range arguments[1:] {
		switch argument.value {
		case "PRIVATE", "PUBLIC", "INTERFACE":
			if len(result) > 0 && len(result[len(result)-1].Values) == 0 {
				return nil, errInvalidCMakeLaunchDeclaration
			}
			result = append(result, cmakeScopedArguments{Scope: argument.value})
		default:
			if len(result) == 0 {
				return nil, errInvalidCMakeLaunchDeclaration
			}
			result[len(result)-1].Values = append(result[len(result)-1].Values, argument)
		}
	}
	if len(result) == 0 || len(result[len(result)-1].Values) == 0 {
		return nil, errInvalidCMakeLaunchDeclaration
	}
	return result, nil
}

func (validator *cmakeLaunchValidator) targetDeclarationScopes(invocation cmakeInvocation) ([]cmakeScopedArguments, error) {
	scopes, err := parseScopedCMakeArguments(invocation.arguments)
	if err != nil {
		return nil, err
	}
	if declared, defined := validator.definedTargets[strings.ToLower(invocation.arguments[0].value)]; !defined || declared != invocation.arguments[0].value {
		return nil, errInvalidCMakeLaunchDeclaration
	}
	return scopes, nil
}

func (validator *cmakeLaunchValidator) validateTargetIncludes(invocation cmakeInvocation, context cmakeDirectoryContext) error {
	scopes, err := validator.targetDeclarationScopes(invocation)
	if err != nil {
		return err
	}
	for _, scope := range scopes {
		for _, argument := range scope.Values {
			path, err := validator.resolveDeclarativeArgument(argument, context)
			if err != nil {
				return err
			}
			info, err := os.Stat(path)
			if err != nil || !info.IsDir() {
				return errInvalidCMakeLaunchDeclaration
			}
		}
	}
	return nil
}

func (validator *cmakeLaunchValidator) validateTargetDefinitions(invocation cmakeInvocation) error {
	scopes, err := validator.targetDeclarationScopes(invocation)
	if err != nil {
		return err
	}
	for _, scope := range scopes {
		for _, argument := range scope.Values {
			if argument.quoted || argument.escaped || !validClosedCompileDefinition(argument.value) {
				return errInvalidCMakeLaunchDeclaration
			}
		}
	}
	return nil
}

func (validator *cmakeLaunchValidator) validateTargetSources(invocation cmakeInvocation, context cmakeDirectoryContext) error {
	scopes, err := validator.targetDeclarationScopes(invocation)
	if err != nil {
		return err
	}
	for _, scope := range scopes {
		for _, argument := range scope.Values {
			if err := validator.validateSourceInput(argument, context); err != nil {
				return err
			}
		}
	}
	return nil
}

func (validator *cmakeLaunchValidator) validateDeclarationSources(invocation cmakeInvocation, start int, context cmakeDirectoryContext) error {
	if len(invocation.arguments) <= start {
		return errInvalidCMakeLaunchDeclaration
	}
	for _, argument := range invocation.arguments[start:] {
		if err := validator.validateSourceInput(argument, context); err != nil {
			return err
		}
	}
	return nil
}

func (validator *cmakeLaunchValidator) validateSourceInput(argument cmakeArgument, context cmakeDirectoryContext) error {
	switch argument.value {
	case "IMPORTED", "ALIAS", "FILE_SET", "HEADERS", "BASE_DIRS", "FILES", "GLOB", "GLOB_RECURSE", "WIN32", "MACOSX_BUNDLE", "EXCLUDE_FROM_ALL":
		return errInvalidCMakeLaunchDeclaration
	}
	path, err := validator.resolveDeclarativeArgument(argument, context)
	if err != nil {
		return err
	}
	_, err = validator.snapshotInput(path)
	return err
}
