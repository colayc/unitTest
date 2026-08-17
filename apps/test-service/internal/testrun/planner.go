package testrun

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"time"

	"unit-test-ide.local/test-service/internal/ctest"
	"unit-test-ide.local/test-service/internal/task"
	"unit-test-ide.local/test-service/internal/testdomain"
	"unit-test-ide.local/test-service/internal/testframework"
)

const maxPlannedInvocations = 100_000

type ContainerBinding struct {
	ContainerID testdomain.ID
	Descriptor  ctest.ExecutionDescriptor
	Adapter     testframework.Adapter
}

type PlannerInput struct {
	Catalog        testdomain.Catalog
	Selection      testdomain.SelectionSnapshot
	Bindings       []ContainerBinding
	Runner         *ctest.Runner
	RepeatCount    int64
	TaskTimeout    time.Duration
	MaxConcurrency int
}

type InvocationState struct {
	CatalogRevision string               `json:"catalogRevision"`
	ContainerID     testdomain.ID        `json:"containerId"`
	ItemIDs         []testdomain.ID      `json:"itemIds"`
	Iteration       int64                `json:"iteration"`
	Framework       testdomain.Framework `json:"framework"`
	AdapterVersion  string               `json:"adapterVersion,omitempty"`
	TimeoutMS       int64                `json:"timeoutMs"`
}

type PlannedInvocation struct {
	Job            ScheduledJob
	Step           task.ExecutionStep
	ContainerID    testdomain.ID
	Framework      testdomain.Framework
	ExpectedCases  []testframework.ExpectedCase
	Adapter        testframework.Adapter
	AdapterVersion string
	ParseInput     testframework.ParseInput
	ControlFile    testframework.ControlFile
	Timeout        time.Duration
}

type PlannedRun struct {
	Invocations []PlannedInvocation
	Waves       []ScheduleWave
}

func PlanRun(
	ctx context.Context,
	input PlannerInput,
) (PlannedRun, error) {
	if ctx == nil ||
		input.RepeatCount < 1 ||
		input.RepeatCount > MaxRepeatCount ||
		input.TaskTimeout < time.Millisecond ||
		input.TaskTimeout > 24*time.Hour {
		return PlannedRun{}, task.ErrInvalidArgument
	}
	if err := ctx.Err(); err != nil {
		return PlannedRun{}, err
	}
	catalog, err := testdomain.NewCatalog(input.Catalog)
	if err != nil {
		return PlannedRun{}, task.ErrInvalidArgument
	}
	selection, err := resolvePlannerSelection(catalog, input.Selection)
	if err != nil {
		return PlannedRun{}, err
	}
	bindings, err := plannerBindings(catalog, input.Bindings)
	if err != nil {
		return PlannedRun{}, err
	}

	base := make([]PlannedInvocation, 0)
	for _, selected := range selection {
		if err := ctx.Err(); err != nil {
			return PlannedRun{}, err
		}
		binding, exists := bindings[selected.container.ID]
		if !exists {
			return PlannedRun{}, task.ErrInvalidArgument
		}
		if binding.Descriptor.Blocked ||
			binding.Descriptor.LogicalName !=
				selected.container.CTestLogicalName {
			return PlannedRun{}, task.ErrInvalidArgument
		}
		if selected.container.Framework ==
			testdomain.FrameworkOpaqueCTest {
			if len(selected.items) != 0 ||
				input.Runner == nil ||
				!nilAdapter(binding.Adapter) {
				return PlannedRun{}, task.ErrInvalidArgument
			}
			timeout, err := effectiveInvocationTimeout(
				input.TaskTimeout,
				binding.Descriptor.TimeoutSeconds,
			)
			if err != nil {
				return PlannedRun{}, err
			}
			step, err := input.Runner.OpaqueRunPlan(
				binding.Descriptor,
				timeout,
			)
			if err != nil {
				return PlannedRun{}, task.ErrInvalidArgument
			}
			base = append(base, PlannedInvocation{
				Step:        step,
				ContainerID: selected.container.ID,
				Framework:   selected.container.Framework,
				ExpectedCases: []testframework.ExpectedCase{{
					ItemID:      selected.container.ID,
					LogicalName: selected.container.CTestLogicalName,
				}},
				ParseInput: testframework.ParseInput{
					Descriptor: binding.Descriptor,
				},
				Timeout: timeout,
			})
			continue
		}
		if nilAdapter(binding.Adapter) ||
			binding.Adapter.Framework() != selected.container.Framework ||
			binding.Adapter.ContractVersion() == "" ||
			!selected.container.Capabilities.CanRunCase ||
			len(selected.items) == 0 {
			return PlannedRun{}, task.ErrInvalidArgument
		}
		runItems, byID, err := plannerRunItems(
			catalog,
			selected.items,
		)
		if err != nil {
			return PlannedRun{}, err
		}
		mode := testframework.RunSelectionCases
		if selected.wholeContainer {
			mode = testframework.RunSelectionAll
		}
		frameworkPlan, err := binding.Adapter.PlanRun(
			ctx,
			testframework.RunInput{
				Descriptor: binding.Descriptor,
				Mode:       mode,
				Items:      runItems,
			},
		)
		if err != nil {
			return PlannedRun{}, err
		}
		planned, err := frameworkInvocations(
			input.TaskTimeout,
			selected.container,
			binding,
			frameworkPlan,
			runItems,
			byID,
		)
		if err != nil {
			return PlannedRun{}, err
		}
		base = append(base, planned...)
		if len(base) > maxPlannedInvocations {
			return PlannedRun{}, task.ErrInvalidArgument
		}
	}
	if len(base) == 0 ||
		len(base) > maxPlannedInvocations/int(input.RepeatCount) {
		return PlannedRun{}, task.ErrInvalidArgument
	}

	candidates := make([]PlannedInvocation, 0, len(base)*int(input.RepeatCount))
	jobs := make([]ScheduledJob, 0, cap(candidates))
	for iteration := int64(1); iteration <= input.RepeatCount; iteration++ {
		for _, template := range base {
			invocation := clonePlannedInvocation(template)
			index := len(candidates) + 1
			id := plannedStepID(index)
			state := InvocationState{
				CatalogRevision: catalog.Revision,
				ContainerID:     invocation.ContainerID,
				ItemIDs:         expectedItemIDs(invocation.ExpectedCases),
				Iteration:       iteration,
				Framework:       invocation.Framework,
				AdapterVersion:  invocation.AdapterVersion,
				TimeoutMS:       invocation.Timeout.Milliseconds(),
			}
			encoded, err := json.Marshal(state)
			if err != nil {
				return PlannedRun{}, task.ErrInvalidArgument
			}
			invocation.Step.ID = id
			invocation.Step.State = encoded
			invocation.Job = ScheduledJob{
				ID: id, ContainerID: state.ContainerID,
				Iteration: iteration,
				RunSerial: invocation.ParseInput.Descriptor.
					Compatibility.RunSerial,
			}
			candidates = append(candidates, invocation)
			jobs = append(jobs, invocation.Job)
		}
	}
	waves, err := BuildSchedule(jobs, input.MaxConcurrency)
	if err != nil {
		return PlannedRun{}, err
	}
	byJobID := make(map[string]PlannedInvocation, len(candidates))
	for _, invocation := range candidates {
		byJobID[invocation.Job.ID] = invocation
	}
	ordered := make([]PlannedInvocation, 0, len(candidates))
	for _, wave := range waves {
		for _, job := range wave.Jobs {
			ordered = append(ordered, byJobID[job.ID])
		}
	}
	return PlannedRun{
		Invocations: ordered,
		Waves:       cloneScheduleWaves(waves),
	}, nil
}

type selectedContainer struct {
	container      testdomain.Container
	items          []testdomain.Item
	wholeContainer bool
}

func resolvePlannerSelection(
	catalog testdomain.Catalog,
	snapshot testdomain.SelectionSnapshot,
) ([]selectedContainer, error) {
	if !snapshot.Mode.Valid() ||
		len(snapshot.ContainerIDs)+len(snapshot.ItemIDs) == 0 ||
		!strictlySortedPlannerIDs(snapshot.ContainerIDs) ||
		!strictlySortedPlannerIDs(snapshot.ItemIDs) {
		return nil, task.ErrInvalidArgument
	}
	containers := make(
		map[testdomain.ID]testdomain.Container,
		len(catalog.Containers),
	)
	for _, container := range catalog.Containers {
		containers[container.ID] = container
	}
	items := make(map[testdomain.ID]testdomain.Item, len(catalog.Items))
	itemsByContainer := make(map[testdomain.ID][]testdomain.Item)
	for _, item := range catalog.Items {
		items[item.ID] = item
		if item.Kind == testdomain.ItemCase {
			itemsByContainer[item.ContainerID] = append(
				itemsByContainer[item.ContainerID],
				item,
			)
		}
	}
	selected := make(map[testdomain.ID]*selectedContainer)
	for _, id := range snapshot.ContainerIDs {
		container, exists := containers[id]
		if !exists {
			return nil, task.ErrInvalidArgument
		}
		selected[id] = &selectedContainer{
			container:      container,
			wholeContainer: true,
			items: append(
				[]testdomain.Item(nil),
				itemsByContainer[id]...,
			),
		}
	}
	for _, id := range snapshot.ItemIDs {
		item, exists := items[id]
		if !exists || item.Kind != testdomain.ItemCase {
			return nil, task.ErrInvalidArgument
		}
		container, exists := containers[item.ContainerID]
		if !exists {
			return nil, task.ErrInvalidArgument
		}
		current := selected[item.ContainerID]
		if current == nil {
			current = &selectedContainer{container: container}
			selected[item.ContainerID] = current
		}
		if current.wholeContainer {
			return nil, task.ErrInvalidArgument
		}
		current.items = append(current.items, item)
	}
	ids := make([]testdomain.ID, 0, len(selected))
	for id := range selected {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(left, right int) bool {
		return ids[left] < ids[right]
	})
	result := make([]selectedContainer, len(ids))
	for index, id := range ids {
		current := *selected[id]
		sort.Slice(current.items, func(left, right int) bool {
			return current.items[left].ID < current.items[right].ID
		})
		result[index] = current
	}
	return result, nil
}

func plannerBindings(
	catalog testdomain.Catalog,
	values []ContainerBinding,
) (map[testdomain.ID]ContainerBinding, error) {
	containers := make(map[testdomain.ID]struct{}, len(catalog.Containers))
	for _, container := range catalog.Containers {
		containers[container.ID] = struct{}{}
	}
	result := make(map[testdomain.ID]ContainerBinding, len(values))
	for _, value := range values {
		if !testdomain.ValidID(value.ContainerID) {
			return nil, task.ErrInvalidArgument
		}
		if _, exists := containers[value.ContainerID]; !exists {
			return nil, task.ErrInvalidArgument
		}
		if _, duplicate := result[value.ContainerID]; duplicate {
			return nil, task.ErrInvalidArgument
		}
		result[value.ContainerID] = value
	}
	return result, nil
}

func plannerRunItems(
	catalog testdomain.Catalog,
	items []testdomain.Item,
) (
	[]testframework.RunItem,
	map[testdomain.ID]testframework.RunItem,
	error,
) {
	allItems := make(map[testdomain.ID]testdomain.Item, len(catalog.Items))
	for _, item := range catalog.Items {
		allItems[item.ID] = item
	}
	result := make([]testframework.RunItem, len(items))
	byID := make(map[testdomain.ID]testframework.RunItem, len(items))
	for index, item := range items {
		parent := allItems[item.ParentID]
		if item.Kind != testdomain.ItemCase ||
			item.ParentID == "" ||
			parent.LogicalName == "" {
			return nil, nil, task.ErrInvalidArgument
		}
		runItem := testframework.RunItem{
			ItemID: item.ID, ParentLogicalName: parent.LogicalName,
			LogicalName: item.LogicalName,
			Parameters: append(
				[]testdomain.Parameter(nil),
				item.Parameters...,
			),
		}
		result[index] = runItem
		byID[item.ID] = runItem
	}
	return result, byID, nil
}

func frameworkInvocations(
	taskTimeout time.Duration,
	container testdomain.Container,
	binding ContainerBinding,
	plan testframework.RunPlan,
	items []testframework.RunItem,
	byID map[testdomain.ID]testframework.RunItem,
) ([]PlannedInvocation, error) {
	if len(plan.Invocations) == 0 ||
		len(plan.Invocations) > maxPlannedInvocations ||
		plan.WorkingDirectory == "" {
		return nil, task.ErrInvalidArgument
	}
	environment, environmentUnset, err := plannerEnvironment(
		plan.Environment,
		plan.EnvironmentChanges,
	)
	if err != nil {
		return nil, err
	}
	timeout, err := effectiveInvocationTimeout(
		taskTimeout,
		plan.TimeoutSeconds,
	)
	if err != nil {
		return nil, err
	}
	seen := make(map[testdomain.ID]struct{}, len(items))
	result := make([]PlannedInvocation, len(plan.Invocations))
	for index, candidate := range plan.Invocations {
		if len(candidate.ExpectedCases) == 0 ||
			binding.Descriptor.Executable.Path == "" {
			return nil, task.ErrInvalidArgument
		}
		expected := make(
			[]testframework.ExpectedCase,
			len(candidate.ExpectedCases),
		)
		parseItems := make(
			[]testframework.RunItem,
			len(candidate.ExpectedCases),
		)
		for expectedIndex, value := range candidate.ExpectedCases {
			item, exists := byID[value.ItemID]
			if !exists ||
				item.ParentLogicalName != value.ParentLogicalName ||
				item.LogicalName != value.LogicalName {
				return nil, task.ErrInvalidArgument
			}
			if _, duplicate := seen[value.ItemID]; duplicate {
				return nil, task.ErrInvalidArgument
			}
			seen[value.ItemID] = struct{}{}
			expected[expectedIndex] = value
			item.Parameters = append(
				[]testdomain.Parameter(nil),
				item.Parameters...,
			)
			parseItems[expectedIndex] = item
		}
		result[index] = PlannedInvocation{
			ContainerID: container.ID,
			Framework:   container.Framework,
			Step: task.ExecutionStep{
				Kind: task.StepTestRun,
				Process: task.ProcessSpec{
					Executable: binding.Descriptor.Executable.Path,
					Args: append(
						[]string(nil),
						candidate.Arguments...,
					),
					Env: append([]string(nil), environment...),
					EnvUnset: append(
						[]string(nil),
						environmentUnset...,
					),
					Dir: plan.WorkingDirectory,
				},
				Public: task.CommandSummary{
					Executable: filepath.Base(
						binding.Descriptor.Executable.Path,
					),
					Args: []string{
						"<service-owned-test-invocation>",
					},
				},
			},
			ExpectedCases:  expected,
			Adapter:        binding.Adapter,
			AdapterVersion: binding.Adapter.ContractVersion(),
			ParseInput: testframework.ParseInput{
				Descriptor: binding.Descriptor,
				Items:      parseItems,
			},
			ControlFile: candidate.ControlFile,
			Timeout:     timeout,
		}
	}
	if len(seen) != len(items) {
		return nil, task.ErrInvalidArgument
	}
	return result, nil
}

func plannerEnvironment(
	values []ctest.EnvironmentEntry,
	changes []ctest.EnvironmentModification,
) ([]string, []string, error) {
	if len(values) > 256 || len(changes) > 256 {
		return nil, nil, task.ErrInvalidArgument
	}
	base := plannerBaseEnvironment()
	overrides := make(map[string]plannerEnvironmentValue)
	removed := make(map[string]string)
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if !validPlannerEnvironmentEntry(
			value.Name,
			value.Value,
		) {
			return nil, nil, task.ErrInvalidArgument
		}
		key := plannerEnvironmentKey(value.Name)
		if _, duplicate := seen[key]; duplicate {
			return nil, nil, task.ErrInvalidArgument
		}
		seen[key] = struct{}{}
		overrides[key] = plannerEnvironmentValue{
			name: value.Name, value: value.Value,
		}
	}
	baseline := make(
		map[string]plannerEnvironmentValue,
		len(overrides),
	)
	for key, value := range overrides {
		baseline[key] = value
	}
	current := func(
		key string,
	) (plannerEnvironmentValue, bool) {
		if _, unset := removed[key]; unset {
			return plannerEnvironmentValue{}, false
		}
		if value, exists := overrides[key]; exists {
			return value, true
		}
		value, exists := base[key]
		return value, exists
	}
	for _, change := range changes {
		if !validPlannerEnvironmentEntry(
			change.Name,
			change.Value,
		) {
			return nil, nil, task.ErrInvalidArgument
		}
		key := plannerEnvironmentKey(change.Name)
		value, exists := current(key)
		if !exists {
			value.name = change.Name
		}
		switch change.Operation {
		case "reset":
			if change.Value != "" {
				return nil, nil, task.ErrInvalidArgument
			}
			delete(removed, key)
			if original, explicit := baseline[key]; explicit {
				overrides[key] = original
			} else {
				delete(overrides, key)
			}
		case "set":
			delete(removed, key)
			overrides[key] = plannerEnvironmentValue{
				name: change.Name, value: change.Value,
			}
		case "unset":
			if change.Value != "" {
				return nil, nil, task.ErrInvalidArgument
			}
			delete(overrides, key)
			removed[key] = change.Name
		case "string_append":
			delete(removed, key)
			value.value += change.Value
			overrides[key] = value
		case "string_prepend":
			delete(removed, key)
			value.value = change.Value + value.value
			overrides[key] = value
		case "path_list_append":
			delete(removed, key)
			value.value = appendPlannerEnvironmentList(
				value.value,
				change.Value,
				string(os.PathListSeparator),
			)
			overrides[key] = value
		case "path_list_prepend":
			delete(removed, key)
			value.value = prependPlannerEnvironmentList(
				value.value,
				change.Value,
				string(os.PathListSeparator),
			)
			overrides[key] = value
		case "cmake_list_append":
			delete(removed, key)
			value.value = appendPlannerEnvironmentList(
				value.value,
				change.Value,
				";",
			)
			overrides[key] = value
		case "cmake_list_prepend":
			delete(removed, key)
			value.value = prependPlannerEnvironmentList(
				value.value,
				change.Value,
				";",
			)
			overrides[key] = value
		default:
			return nil, nil, task.ErrInvalidArgument
		}
	}
	if len(overrides)+len(removed) > 256 {
		return nil, nil, task.ErrInvalidArgument
	}
	overrideKeys := make([]string, 0, len(overrides))
	for key := range overrides {
		overrideKeys = append(overrideKeys, key)
	}
	sort.Strings(overrideKeys)
	environment := make([]string, len(overrideKeys))
	for index, key := range overrideKeys {
		value := overrides[key]
		environment[index] = value.name + "=" + value.value
	}
	removedKeys := make([]string, 0, len(removed))
	for key := range removed {
		removedKeys = append(removedKeys, key)
	}
	sort.Strings(removedKeys)
	unset := make([]string, len(removedKeys))
	for index, key := range removedKeys {
		unset[index] = removed[key]
	}
	return environment, unset, nil
}

type plannerEnvironmentValue struct {
	name  string
	value string
}

func plannerBaseEnvironment() map[string]plannerEnvironmentValue {
	result := make(map[string]plannerEnvironmentValue)
	for _, encoded := range os.Environ() {
		name, value, found := strings.Cut(encoded, "=")
		if !found ||
			!validPlannerEnvironmentEntry(name, value) ||
			plannerServiceEnvironmentKey(name) {
			continue
		}
		result[plannerEnvironmentKey(name)] =
			plannerEnvironmentValue{name: name, value: value}
	}
	return result
}

func validPlannerEnvironmentEntry(name, value string) bool {
	if name == "" ||
		strings.ContainsAny(name, "=\x00") ||
		strings.ContainsRune(value, '\x00') ||
		plannerServiceEnvironmentKey(name) {
		return false
	}
	for index, character := range []byte(name) {
		if character >= 'A' && character <= 'Z' ||
			character >= 'a' && character <= 'z' ||
			index > 0 && character >= '0' &&
				character <= '9' ||
			character == '_' {
			continue
		}
		return false
	}
	return true
}

func plannerServiceEnvironmentKey(value string) bool {
	upper := strings.ToUpper(value)
	return strings.HasPrefix(upper, "UTIDE_") ||
		strings.HasPrefix(upper, "UNIT_TEST_IDE_") ||
		upper == "UNIT_TEST_SERVICE_TOKEN"
}

func plannerEnvironmentKey(value string) string {
	if runtime.GOOS == "windows" {
		return strings.ToUpper(value)
	}
	return value
}

func appendPlannerEnvironmentList(
	current string,
	value string,
	separator string,
) string {
	if current == "" {
		return value
	}
	if value == "" {
		return current
	}
	return current + separator + value
}

func prependPlannerEnvironmentList(
	current string,
	value string,
	separator string,
) string {
	if current == "" {
		return value
	}
	if value == "" {
		return current
	}
	return value + separator + current
}

func effectiveInvocationTimeout(
	taskTimeout time.Duration,
	property *float64,
) (time.Duration, error) {
	result := taskTimeout
	if property == nil {
		return result, nil
	}
	if *property <= 0 ||
		math.IsNaN(*property) ||
		math.IsInf(*property, 0) {
		return 0, task.ErrInvalidArgument
	}
	milliseconds := math.Ceil(*property * 1000)
	if milliseconds > float64((24 * time.Hour).Milliseconds()) {
		return 0, task.ErrInvalidArgument
	}
	propertyTimeout := time.Duration(milliseconds) * time.Millisecond
	if propertyTimeout < result {
		result = propertyTimeout
	}
	return result, nil
}

func expectedItemIDs(
	values []testframework.ExpectedCase,
) []testdomain.ID {
	result := make([]testdomain.ID, len(values))
	for index, value := range values {
		result[index] = value.ItemID
	}
	sort.Slice(result, func(left, right int) bool {
		return result[left] < result[right]
	})
	return result
}

func clonePlannedInvocation(
	value PlannedInvocation,
) PlannedInvocation {
	result := value
	result.Step.Process.Args = append(
		[]string(nil),
		value.Step.Process.Args...,
	)
	result.Step.Process.Env = append(
		[]string(nil),
		value.Step.Process.Env...,
	)
	result.Step.Process.EnvUnset = append(
		[]string(nil),
		value.Step.Process.EnvUnset...,
	)
	result.Step.Public.Args = append(
		[]string(nil),
		value.Step.Public.Args...,
	)
	result.ExpectedCases = append(
		[]testframework.ExpectedCase(nil),
		value.ExpectedCases...,
	)
	result.ParseInput.Items = append(
		[]testframework.RunItem(nil),
		value.ParseInput.Items...,
	)
	for index := range result.ParseInput.Items {
		result.ParseInput.Items[index].Parameters = append(
			[]testdomain.Parameter(nil),
			value.ParseInput.Items[index].Parameters...,
		)
	}
	return result
}

func cloneScheduleWaves(values []ScheduleWave) []ScheduleWave {
	result := make([]ScheduleWave, len(values))
	for index, wave := range values {
		result[index].Jobs = append(
			[]ScheduledJob(nil),
			wave.Jobs...,
		)
	}
	return result
}

func plannedStepID(index int) string {
	const digits = "0123456789"
	var buffer [11]byte
	copy(buffer[:5], "test-")
	value := index
	for position := len(buffer) - 1; position >= 5; position-- {
		buffer[position] = digits[value%10]
		value /= 10
	}
	return string(buffer[:])
}

func strictlySortedPlannerIDs(values []testdomain.ID) bool {
	for index, value := range values {
		if !testdomain.ValidID(value) ||
			index > 0 && values[index-1] >= value {
			return false
		}
	}
	return true
}

func nilAdapter(value testframework.Adapter) bool {
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

func decodePlannerState(
	encoded []byte,
	destination any,
) error {
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return task.ErrInvalidArgument
	}
	return nil
}
