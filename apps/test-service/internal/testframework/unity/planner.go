package unity

import (
	"context"
	"reflect"
	"sort"

	"unit-test-ide.local/test-service/internal/ctest"
	"unit-test-ide.local/test-service/internal/testdomain"
	"unit-test-ide.local/test-service/internal/testframework"
	"unit-test-ide.local/test-service/internal/unityrunner"
)

type selectedCase struct {
	item     testframework.RunItem
	testCase unityrunner.TestCase
}

func buildRunPlan(
	ctx context.Context,
	input testframework.RunInput,
	evidence *manifestEvidence,
	allocator testframework.ControlFileAllocator,
) (testframework.RunPlan, error) {
	if ctx == nil || evidence == nil || nilInterface(allocator) ||
		len(input.Items) == 0 || len(input.Items) > maxRecords {
		return testframework.RunPlan{}, ErrInvalidRunPlan
	}
	switch input.Mode {
	case testframework.RunSelectionAll,
		testframework.RunSelectionGroup,
		testframework.RunSelectionCases:
	default:
		return testframework.RunPlan{}, ErrInvalidRunPlan
	}

	selected := make([]selectedCase, len(input.Items))
	ids := make(map[testdomain.ID]struct{}, len(input.Items))
	identities := make(map[string]struct{}, len(input.Items))
	group := input.Items[0].ParentLogicalName
	for index, item := range input.Items {
		if err := ctx.Err(); err != nil {
			return testframework.RunPlan{}, err
		}
		testCase, found := findManifestCase(evidence.manifest, item.LogicalName)
		if !found || !testdomain.ValidID(item.ItemID) ||
			item.ParentLogicalName != testCase.Location.Path ||
			!reflect.DeepEqual(item.Parameters, caseParameters(testCase)) ||
			input.Mode == testframework.RunSelectionGroup &&
				item.ParentLogicalName != group {
			return testframework.RunPlan{}, ErrInvalidRunPlan
		}
		if _, duplicate := ids[item.ItemID]; duplicate {
			return testframework.RunPlan{}, ErrInvalidRunPlan
		}
		key := item.ParentLogicalName + "\x00" + item.LogicalName
		if _, duplicate := identities[key]; duplicate {
			return testframework.RunPlan{}, ErrInvalidRunPlan
		}
		ids[item.ItemID] = struct{}{}
		identities[key] = struct{}{}
		item.Parameters = append([]testdomain.Parameter(nil), item.Parameters...)
		selected[index] = selectedCase{item: item, testCase: testCase}
	}
	sort.Slice(selected, func(first, second int) bool {
		if selected[first].item.ParentLogicalName !=
			selected[second].item.ParentLogicalName {
			return selected[first].item.ParentLogicalName <
				selected[second].item.ParentLogicalName
		}
		if selected[first].item.LogicalName !=
			selected[second].item.LogicalName {
			return selected[first].item.LogicalName <
				selected[second].item.LogicalName
		}
		return selected[first].item.ItemID < selected[second].item.ItemID
	})

	plan := testframework.RunPlan{
		Invocations: make(
			[]testframework.RunInvocation,
			0,
			len(selected),
		),
		Environment: append(
			[]ctest.EnvironmentEntry(nil),
			input.Descriptor.Environment...,
		),
		EnvironmentChanges: append(
			[]ctest.EnvironmentModification(nil),
			input.Descriptor.EnvironmentChanges...,
		),
		WorkingDirectory: input.Descriptor.WorkingDirectory,
		TimeoutSeconds:   cloneTimeout(input.Descriptor.TimeoutSeconds),
	}
	for _, selectedCase := range selected {
		if err := ctx.Err(); err != nil {
			return testframework.RunPlan{}, err
		}
		control, err := allocator.Allocate(ctx)
		if err != nil {
			return testframework.RunPlan{}, err
		}
		if err := validateControlFile(control); err != nil {
			return testframework.RunPlan{}, err
		}
		arguments := []string{
			"--utide-protocol", ContractVersion,
			"--utide-mode", "run",
			"--utide-case", selectedCase.testCase.Identity,
			"--utide-result", control.Path(),
		}
		plan.Invocations = append(
			plan.Invocations,
			testframework.RunInvocation{
				Arguments: arguments,
				ExpectedCases: []testframework.ExpectedCase{{
					ItemID:            selectedCase.item.ItemID,
					ParentLogicalName: selectedCase.item.ParentLogicalName,
					LogicalName:       selectedCase.item.LogicalName,
				}},
				ControlFile: control,
			},
		)
	}
	if err := evidence.verify(); err != nil {
		return testframework.RunPlan{}, err
	}
	return plan, nil
}

func cloneTimeout(value *float64) *float64 {
	if value == nil {
		return nil
	}
	result := *value
	return &result
}
