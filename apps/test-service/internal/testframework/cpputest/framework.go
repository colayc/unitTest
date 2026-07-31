package cpputest

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"time"

	"unit-test-ide.local/test-service/internal/ctest"
	"unit-test-ide.local/test-service/internal/probe"
	"unit-test-ide.local/test-service/internal/testdomain"
	"unit-test-ide.local/test-service/internal/testframework"
)

const defaultDiscoveryTimeout = 5 * time.Second

type Adapter struct {
	runner           probe.Runner
	listLimits       Limits
	resultLimits     ResultLimits
	discoveryTimeout time.Duration
}

var _ testframework.Adapter = (*Adapter)(nil)

func NewAdapter(runner probe.Runner) (*Adapter, error) {
	if nilInterface(runner) {
		return nil, ErrInvalidAdapter
	}
	return &Adapter{
		runner:           runner,
		listLimits:       DefaultLimits(),
		resultLimits:     DefaultResultLimits(),
		discoveryTimeout: defaultDiscoveryTimeout,
	}, nil
}

func (*Adapter) Framework() testdomain.Framework {
	return testdomain.FrameworkCppUTest
}

func (*Adapter) ContractVersion() string {
	return ContractVersion
}

func (adapter *Adapter) Verify(
	ctx context.Context,
	descriptor ctest.ExecutionDescriptor,
) (testframework.Capabilities, error) {
	if !adapter.valid() || ctx == nil {
		return testframework.Capabilities{}, ErrInvalidAdapter
	}
	if err := ctx.Err(); err != nil {
		return testframework.Capabilities{}, err
	}
	if _, err := BuildRunPlan(descriptor, Selection{
		Mode: SelectionAll,
	}); err != nil {
		return testframework.Capabilities{}, err
	}
	if _, err := discoveryEnvironment(descriptor); err != nil {
		return testframework.Capabilities{}, err
	}
	return testframework.Capabilities{
		CanRunContainer:         true,
		CanDiscoverCases:        true,
		CanRunCase:              true,
		CanReportSkipped:        true,
		CanReportSourceLocation: true,
		CanReportMockDetails:    true,
	}, nil
}

func (adapter *Adapter) Discover(
	ctx context.Context,
	descriptor ctest.ExecutionDescriptor,
) (testframework.DiscoveryResult, error) {
	if _, err := adapter.Verify(ctx, descriptor); err != nil {
		return testframework.DiscoveryResult{}, err
	}
	environment, err := discoveryEnvironment(descriptor)
	if err != nil {
		return testframework.DiscoveryResult{}, err
	}
	arguments := append([]string(nil), descriptor.Arguments...)
	arguments = append(arguments, "-ln")
	result, runErr := adapter.runner.Run(ctx, probe.Spec{
		Executable: descriptor.Executable.Path,
		Args:       arguments,
		Env:        environment,
		Dir:        descriptor.WorkingDirectory,
		Timeout:    adapter.discoveryTimeout,
		MaxOutput:  adapter.listLimits.MaxDocumentBytes,
	})
	if verifyErr := descriptor.ValidateExecutable(); verifyErr != nil {
		return testframework.DiscoveryResult{}, verifyErr
	}
	if contextErr := ctx.Err(); contextErr != nil {
		return testframework.DiscoveryResult{}, contextErr
	}
	if runErr != nil {
		return testframework.DiscoveryResult{}, errors.Join(
			ErrDiscoveryFailed,
			runErr,
		)
	}
	if len(result.Stdout) > adapter.listLimits.MaxDocumentBytes ||
		len(result.Stderr) > adapter.listLimits.MaxDocumentBytes ||
		result.ExitCode != 0 ||
		len(bytes.TrimSpace(result.Stderr)) != 0 {
		return testframework.DiscoveryResult{}, ErrDiscoveryFailed
	}
	identities, err := ParseList(
		bytes.NewReader(result.Stdout),
		adapter.listLimits,
	)
	if err != nil {
		return testframework.DiscoveryResult{}, errors.Join(
			ErrDiscoveryFailed,
			err,
		)
	}
	return testframework.DiscoveryResult{
		Items:       discoveredItems(identities),
		Diagnostics: []testdomain.Diagnostic{},
	}, nil
}

func discoveredItems(
	identities []CaseIdentity,
) []testframework.DiscoveredItem {
	items := make([]testframework.DiscoveredItem, 0, len(identities)*2)
	groups := make(map[string]struct{})
	for _, identity := range identities {
		if _, exists := groups[identity.Group]; !exists {
			groups[identity.Group] = struct{}{}
			items = append(items, testframework.DiscoveredItem{
				Kind:        testdomain.ItemGroup,
				LogicalName: identity.Group,
				DisplayName: identity.Group,
				Labels:      []string{},
				Parameters:  []testdomain.Parameter{},
			})
		}
		items = append(items, testframework.DiscoveredItem{
			Kind:              testdomain.ItemCase,
			ParentKind:        testdomain.ItemGroup,
			ParentLogicalName: identity.Group,
			LogicalName:       identity.Name,
			DisplayName:       identity.Name,
			Labels:            []string{},
			Parameters:        []testdomain.Parameter{},
		})
	}
	return items
}

func (adapter *Adapter) PlanRun(
	ctx context.Context,
	input testframework.RunInput,
) (testframework.RunPlan, error) {
	if _, err := adapter.Verify(ctx, input.Descriptor); err != nil {
		return testframework.RunPlan{}, err
	}
	if len(input.Items) == 0 {
		return testframework.RunPlan{}, ErrInvalidRunPlan
	}
	selection := Selection{
		Cases: make([]SelectedCase, len(input.Items)),
	}
	switch input.Mode {
	case testframework.RunSelectionAll:
		selection.Mode = SelectionAll
	case testframework.RunSelectionGroup:
		selection.Mode = SelectionGroup
		selection.Group = input.Items[0].ParentLogicalName
	case testframework.RunSelectionCases:
		selection.Mode = SelectionCases
	default:
		return testframework.RunPlan{}, ErrInvalidRunPlan
	}
	for index, item := range input.Items {
		selection.Cases[index] = SelectedCase{
			ItemID: item.ItemID,
			Group:  item.ParentLogicalName,
			Name:   item.LogicalName,
		}
	}
	return BuildRunPlan(input.Descriptor, selection)
}

func (adapter *Adapter) NewParser(
	input testframework.ParseInput,
) (testframework.ResultParser, error) {
	if !adapter.valid() {
		return nil, ErrInvalidAdapter
	}
	if _, err := BuildRunPlan(input.Descriptor, Selection{
		Mode: SelectionAll,
	}); err != nil {
		return nil, err
	}
	return NewParser(input, adapter.resultLimits)
}

func (adapter *Adapter) valid() bool {
	return adapter != nil &&
		!nilInterface(adapter.runner) &&
		adapter.listLimits.Valid() &&
		adapter.resultLimits.valid() &&
		adapter.discoveryTimeout > 0
}

func nilInterface(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map,
		reflect.Pointer, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}
