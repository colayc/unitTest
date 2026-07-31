package cpputest

import (
	"math"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"unit-test-ide.local/test-service/internal/ctest"
	"unit-test-ide.local/test-service/internal/testdomain"
	"unit-test-ide.local/test-service/internal/testframework"
)

const (
	maxPlanCases       = 100_000
	maxIdentityBytes   = 512
	maxOriginalArgs    = 1_024
	maxOriginalArgSize = 64 * 1024
)

func BuildRunPlan(
	descriptor ctest.ExecutionDescriptor,
	selection Selection,
) (testframework.RunPlan, error) {
	if descriptor.Blocked || !descriptor.Compatibility.CaseLevel {
		return testframework.RunPlan{}, ErrIncompatibleDescriptor
	}
	if hasReservedArguments(descriptor.Arguments) {
		return testframework.RunPlan{}, ErrReservedArguments
	}
	if !validDescriptorForPlan(descriptor) {
		return testframework.RunPlan{}, ErrInvalidRunPlan
	}
	if err := descriptor.ValidateExecutable(); err != nil {
		return testframework.RunPlan{}, err
	}
	cases, err := validateAndSortSelection(selection)
	if err != nil {
		return testframework.RunPlan{}, err
	}

	plan := testframework.RunPlan{
		Invocations:        make([]testframework.RunInvocation, 0),
		Environment:        append([]ctest.EnvironmentEntry(nil), descriptor.Environment...),
		EnvironmentChanges: append([]ctest.EnvironmentModification(nil), descriptor.EnvironmentChanges...),
		WorkingDirectory:   descriptor.WorkingDirectory,
		TimeoutSeconds:     cloneTimeout(descriptor.TimeoutSeconds),
	}
	base := append([]string(nil), descriptor.Arguments...)
	switch selection.Mode {
	case SelectionAll:
		plan.Invocations = append(plan.Invocations, testframework.RunInvocation{
			Arguments:     append(append([]string(nil), base...), "-v"),
			ExpectedCases: expectedCaseBoundaries(cases),
		})
	case SelectionGroup:
		plan.Invocations = append(plan.Invocations, testframework.RunInvocation{
			Arguments: append(
				append([]string(nil), base...),
				"-v", "-sg", selection.Group,
			),
			ExpectedCases: expectedCaseBoundaries(cases),
		})
	case SelectionCases:
		plan.Invocations = make([]testframework.RunInvocation, 0, len(cases))
		for _, selected := range cases {
			plan.Invocations = append(plan.Invocations, testframework.RunInvocation{
				Arguments: append(
					append([]string(nil), base...),
					"-v", "-sg", selected.Group, "-sn", selected.Name,
				),
				ExpectedCases: expectedCaseBoundaries([]SelectedCase{selected}),
			})
		}
	default:
		return testframework.RunPlan{}, ErrInvalidRunPlan
	}
	return plan, nil
}

func validateAndSortSelection(selection Selection) ([]SelectedCase, error) {
	if len(selection.Cases) > maxPlanCases {
		return nil, ErrInvalidRunPlan
	}
	switch selection.Mode {
	case SelectionAll:
		if selection.Group != "" {
			return nil, ErrInvalidRunPlan
		}
	case SelectionGroup:
		if !validIdentityPart(selection.Group) || len(selection.Cases) == 0 {
			return nil, ErrInvalidRunPlan
		}
	case SelectionCases:
		if selection.Group != "" || len(selection.Cases) == 0 {
			return nil, ErrInvalidRunPlan
		}
	default:
		return nil, ErrInvalidRunPlan
	}

	result := append([]SelectedCase(nil), selection.Cases...)
	ids := make(map[testdomain.ID]struct{}, len(result))
	identities := make(map[string]struct{}, len(result))
	for _, selected := range result {
		if !testdomain.ValidID(selected.ItemID) ||
			!validIdentityPart(selected.Group) ||
			!validIdentityPart(selected.Name) ||
			selection.Mode == SelectionGroup && selected.Group != selection.Group {
			return nil, ErrInvalidRunPlan
		}
		if _, duplicate := ids[selected.ItemID]; duplicate {
			return nil, ErrInvalidRunPlan
		}
		identity := selected.Group + "\x00" + selected.Name
		if _, duplicate := identities[identity]; duplicate {
			return nil, ErrInvalidRunPlan
		}
		ids[selected.ItemID] = struct{}{}
		identities[identity] = struct{}{}
	}
	sort.Slice(result, func(first, second int) bool {
		if result[first].Group != result[second].Group {
			return result[first].Group < result[second].Group
		}
		if result[first].Name != result[second].Name {
			return result[first].Name < result[second].Name
		}
		return result[first].ItemID < result[second].ItemID
	})
	return result, nil
}

func expectedCaseBoundaries(
	cases []SelectedCase,
) []testframework.ExpectedCase {
	result := make([]testframework.ExpectedCase, len(cases))
	for index, selected := range cases {
		result[index] = testframework.ExpectedCase{
			ItemID:            selected.ItemID,
			ParentLogicalName: selected.Group,
			LogicalName:       selected.Name,
		}
	}
	return result
}

func validDescriptorForPlan(descriptor ctest.ExecutionDescriptor) bool {
	if descriptor.LogicalName == "" || descriptor.TargetID == "" ||
		!absoluteCleanPath(descriptor.Executable.Path) ||
		!absoluteCleanPath(descriptor.WorkingDirectory) ||
		len(descriptor.Arguments) > maxOriginalArgs {
		return false
	}
	for _, argument := range descriptor.Arguments {
		if len(argument) > maxOriginalArgSize || !utf8.ValidString(argument) ||
			strings.ContainsRune(argument, '\x00') {
			return false
		}
	}
	if descriptor.TimeoutSeconds != nil &&
		(*descriptor.TimeoutSeconds <= 0 ||
			math.IsNaN(*descriptor.TimeoutSeconds) ||
			math.IsInf(*descriptor.TimeoutSeconds, 0)) {
		return false
	}
	return true
}

func validIdentityPart(value string) bool {
	if value == "" || len(value) > maxIdentityBytes ||
		!utf8.ValidString(value) || strings.ContainsRune(value, '.') {
		return false
	}
	for _, character := range value {
		if unicode.IsSpace(character) || unicode.IsControl(character) {
			return false
		}
	}
	return true
}

func hasReservedArguments(arguments []string) bool {
	for _, argument := range arguments {
		switch argument {
		case "-g", "-n", "-s", "-sg", "-sn", "-xg", "-xn",
			"-r", "-ri", "-v", "-lg", "-ln", "-ojunit",
			"-oteamcity", "-p":
			return true
		}
		if repeatedRunArgument(argument) ||
			strings.HasPrefix(argument, "TEST(") {
			return true
		}
	}
	return false
}

func repeatedRunArgument(value string) bool {
	if len(value) <= 2 || !strings.HasPrefix(value, "-r") {
		return false
	}
	for _, character := range value[2:] {
		if character < '0' || character > '9' {
			return false
		}
	}
	return true
}

func absoluteCleanPath(value string) bool {
	return value != "" && filepath.IsAbs(value) &&
		filepath.Clean(value) == value &&
		!strings.ContainsRune(value, '\x00')
}

func cloneTimeout(value *float64) *float64 {
	if value == nil {
		return nil
	}
	result := *value
	return &result
}
