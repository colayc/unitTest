package cpputest

import (
	"context"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"

	"unit-test-ide.local/test-service/internal/ctest"
	"unit-test-ide.local/test-service/internal/probe"
	"unit-test-ide.local/test-service/internal/testdomain"
	"unit-test-ide.local/test-service/internal/testframework"
)

func TestCppUTestAdapterIntegrationSelectsDiscoversPlansAndParses(t *testing.T) {
	fixture := newPlannerFixture(t)
	runner := &integrationProbeRunner{result: probe.Result{
		ExitCode: 0,
		Stdout:   []byte("Core.passes Core.fails\n"),
	}}
	adapter, err := NewAdapter(runner)
	if err != nil {
		t.Fatal(err)
	}
	registry, err := testframework.NewRegistry(adapter)
	if err != nil {
		t.Fatal(err)
	}

	opaque, err := registry.Select(context.Background(), testframework.SelectionInput{
		Descriptor: fixture.descriptor,
	})
	if err != nil {
		t.Fatal(err)
	}
	if opaque.Framework != testdomain.FrameworkOpaqueCTest || len(runner.specs) != 0 {
		t.Fatalf("undeclared executable was probed: %#v, specs=%#v", opaque, runner.specs)
	}

	selected, err := registry.Select(context.Background(), testframework.SelectionInput{
		Descriptor: fixture.descriptor,
		Mappings: []testframework.Mapping{{
			CTestName: fixture.descriptor.LogicalName,
			Framework: testdomain.FrameworkCppUTest,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if selected.Adapter != adapter ||
		selected.Framework != testdomain.FrameworkCppUTest ||
		!selected.Capabilities.CanDiscoverCases ||
		!selected.Capabilities.CanRunCase ||
		!selected.Capabilities.CanReportSkipped ||
		!selected.Capabilities.CanReportSourceLocation ||
		!selected.Capabilities.CanReportMockDetails ||
		len(runner.specs) != 0 {
		t.Fatalf("selected adapter = %#v, specs=%#v", selected, runner.specs)
	}

	discovered, err := selected.Adapter.Discover(context.Background(), fixture.descriptor)
	if err != nil {
		t.Fatal(err)
	}
	wantDiscovered := []testframework.DiscoveredItem{
		{
			Kind:        testdomain.ItemGroup,
			LogicalName: "Core",
			DisplayName: "Core",
			Labels:      []string{},
			Parameters:  []testdomain.Parameter{},
		},
		{
			Kind:              testdomain.ItemCase,
			ParentKind:        testdomain.ItemGroup,
			ParentLogicalName: "Core",
			LogicalName:       "passes",
			DisplayName:       "passes",
			Labels:            []string{},
			Parameters:        []testdomain.Parameter{},
		},
		{
			Kind:              testdomain.ItemCase,
			ParentKind:        testdomain.ItemGroup,
			ParentLogicalName: "Core",
			LogicalName:       "fails",
			DisplayName:       "fails",
			Labels:            []string{},
			Parameters:        []testdomain.Parameter{},
		},
	}
	if !reflect.DeepEqual(discovered.Items, wantDiscovered) ||
		discovered.Partial ||
		len(discovered.Diagnostics) != 0 {
		t.Fatalf("discovery = %#v, want %#v", discovered, wantDiscovered)
	}
	if len(runner.specs) != 1 {
		t.Fatalf("discovery specs = %#v", runner.specs)
	}
	spec := runner.specs[0]
	if spec.Executable != fixture.descriptor.Executable.Path ||
		spec.Dir != fixture.descriptor.WorkingDirectory ||
		!reflect.DeepEqual(
			spec.Args,
			append(
				append([]string(nil), fixture.descriptor.Arguments...),
				"-ln",
			),
		) ||
		spec.Timeout <= 0 ||
		spec.MaxOutput != DefaultLimits().MaxDocumentBytes {
		t.Fatalf("discovery spec = %#v", spec)
	}

	pass := selectedCase("1", "Core", "passes")
	plan, err := selected.Adapter.PlanRun(context.Background(), testframework.RunInput{
		Descriptor: fixture.descriptor,
		Mode:       testframework.RunSelectionCases,
		Items: []testframework.RunItem{{
			ItemID:            pass.ItemID,
			ParentLogicalName: pass.Group,
			LogicalName:       pass.Name,
			Parameters:        []testdomain.Parameter{},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Invocations) != 1 ||
		!reflect.DeepEqual(
			plan.Invocations[0].Arguments,
			append(
				append([]string(nil), fixture.descriptor.Arguments...),
				"-v", "-sg", "Core", "-sn", "passes",
			),
		) {
		t.Fatalf("run plan = %#v", plan)
	}

	parser, err := selected.Adapter.NewParser(testframework.ParseInput{
		Descriptor: fixture.descriptor,
		Items: []testframework.RunItem{{
			ItemID:            pass.ItemID,
			ParentLogicalName: pass.Group,
			LogicalName:       pass.Name,
			Parameters:        []testdomain.Parameter{},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	output := strings.Join([]string{
		"TEST(Core, passes) - 1 ms",
		"",
		"OK (1 tests, 1 ran, 1 checks, 0 ignored, 0 filtered out, 1 ms)",
		"",
	}, "\n")
	events, err := parser.Feed(testframework.StreamStdout, []byte(output))
	if err != nil {
		t.Fatal(err)
	}
	result, err := parser.Finish(testframework.ProcessResult{
		ExitCode:    0,
		Termination: testframework.ProcessExited,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 ||
		events[0].Case.Status != testframework.CasePassed ||
		!result.Complete ||
		len(result.Cases) != 1 ||
		result.Cases[0].ItemID != pass.ItemID ||
		result.Cases[0].Status != testframework.CasePassed {
		t.Fatalf("events=%#v result=%#v", events, result)
	}
}

func TestCppUTestAdapterIntegrationKeepsCrashAndTimeoutPartial(t *testing.T) {
	fixture := newPlannerFixture(t)
	adapter, err := NewAdapter(&integrationProbeRunner{})
	if err != nil {
		t.Fatal(err)
	}
	pass := selectedCase("1", "Core", "passes")
	unfinished := selectedCase("2", "Core", "unfinished")
	input := testframework.ParseInput{
		Descriptor: fixture.descriptor,
		Items: []testframework.RunItem{
			{
				ItemID: pass.ItemID, ParentLogicalName: pass.Group,
				LogicalName: pass.Name, Parameters: []testdomain.Parameter{},
			},
			{
				ItemID: unfinished.ItemID, ParentLogicalName: unfinished.Group,
				LogicalName: unfinished.Name, Parameters: []testdomain.Parameter{},
			},
		},
	}
	for name, test := range map[string]struct {
		termination  testframework.ProcessTermination
		wantCategory string
	}{
		"crash": {
			termination:  testframework.ProcessCrashed,
			wantCategory: "test_process_crash",
		},
		"timeout": {
			termination:  testframework.ProcessTimedOut,
			wantCategory: "test_timeout",
		},
	} {
		t.Run(name, func(t *testing.T) {
			parser, err := adapter.NewParser(input)
			if err != nil {
				t.Fatal(err)
			}
			output := strings.Join([]string{
				"TEST(Core, passes) - 1 ms",
				"TEST(Core, unfinished)",
				"",
			}, "\n")
			if _, err := parser.Feed(
				testframework.StreamStdout,
				[]byte(output),
			); err != nil {
				t.Fatal(err)
			}
			result, err := parser.Finish(testframework.ProcessResult{
				ExitCode:    -1,
				Termination: test.termination,
			})
			if err != nil {
				t.Fatal(err)
			}
			if result.Complete ||
				len(result.Cases) != 2 ||
				result.Cases[0].Status != testframework.CasePassed ||
				!result.Cases[0].Partial ||
				result.Cases[1].Status != testframework.CaseNotRun ||
				!result.Cases[1].Partial ||
				!hasDiagnosticCategory(result, test.wantCategory) {
				t.Fatalf("result = %#v", result)
			}
		})
	}
}

func TestCppUTestAdapterRunInputUsesOnlyClosedSelectionModes(t *testing.T) {
	fixture := newPlannerFixture(t)
	adapter, err := NewAdapter(&integrationProbeRunner{})
	if err != nil {
		t.Fatal(err)
	}
	first := selectedCase("1", "Core", "passes")
	second := selectedCase("2", "Core", "fails")
	items := []testframework.RunItem{
		{
			ItemID: first.ItemID, ParentLogicalName: first.Group,
			LogicalName: first.Name, Parameters: []testdomain.Parameter{},
		},
		{
			ItemID: second.ItemID, ParentLogicalName: second.Group,
			LogicalName: second.Name, Parameters: []testdomain.Parameter{},
		},
	}
	tests := map[string]struct {
		mode  testframework.RunSelectionMode
		items []testframework.RunItem
		want  []string
	}{
		"all": {
			mode:  testframework.RunSelectionAll,
			items: items,
			want:  []string{"-v"},
		},
		"group": {
			mode:  testframework.RunSelectionGroup,
			items: items,
			want:  []string{"-v", "-sg", "Core"},
		},
		"cases": {
			mode:  testframework.RunSelectionCases,
			items: items[:1],
			want:  []string{"-v", "-sg", "Core", "-sn", "passes"},
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			plan, err := adapter.PlanRun(context.Background(), testframework.RunInput{
				Descriptor: fixture.descriptor,
				Mode:       test.mode,
				Items:      test.items,
			})
			if err != nil {
				t.Fatal(err)
			}
			if len(plan.Invocations) == 0 {
				t.Fatalf("plan = %#v", plan)
			}
			suffix := plan.Invocations[0].Arguments[len(fixture.descriptor.Arguments):]
			if !reflect.DeepEqual(suffix, test.want) {
				t.Fatalf("arguments suffix = %#v, want %#v", suffix, test.want)
			}
		})
	}

	for name, input := range map[string]testframework.RunInput{
		"empty selection": {
			Descriptor: fixture.descriptor,
			Mode:       testframework.RunSelectionAll,
		},
		"unknown mode": {
			Descriptor: fixture.descriptor,
			Mode:       "client-command",
			Items:      items,
		},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := adapter.PlanRun(
				context.Background(),
				input,
			); !errors.Is(err, ErrInvalidRunPlan) {
				t.Fatalf("PlanRun() error = %v", err)
			}
		})
	}

	runInputType := reflect.TypeOf(testframework.RunInput{})
	fields := make([]string, runInputType.NumField())
	for index := range fields {
		fields[index] = runInputType.Field(index).Name
	}
	if !reflect.DeepEqual(
		fields,
		[]string{"Descriptor", "Mode", "Items"},
	) {
		t.Fatalf("RunInput fields = %#v", fields)
	}
}

func TestCppUTestAdapterIntegrationRejectsInvalidConstructionAndDiscovery(t *testing.T) {
	if _, err := NewAdapter(nil); !errors.Is(err, ErrInvalidAdapter) {
		t.Fatalf("NewAdapter(nil) error = %v", err)
	}
	fixture := newPlannerFixture(t)
	runner := &integrationProbeRunner{result: probe.Result{
		ExitCode: 2,
		Stderr:   []byte("unsupported arguments"),
	}}
	adapter, err := NewAdapter(runner)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.Discover(
		context.Background(),
		fixture.descriptor,
	); !errors.Is(err, ErrDiscoveryFailed) {
		t.Fatalf("Discover() error = %v", err)
	}
}

func TestCppUTestDiscoveryEnvironmentAppliesCTestSemantics(t *testing.T) {
	t.Setenv("CPP_ADAPTER_RESET", "original")
	t.Setenv("CPP_ADAPTER_REMOVE", "remove-me")
	descriptor := ctest.ExecutionDescriptor{
		Environment: []ctest.EnvironmentEntry{
			{Name: "CPP_ADAPTER_RESET", Value: "overridden"},
			{Name: "CPP_ADAPTER_STRING", Value: "middle"},
			{Name: "CPP_ADAPTER_PATH", Value: "middle"},
			{Name: "CPP_ADAPTER_CMAKE", Value: "middle"},
		},
		EnvironmentChanges: []ctest.EnvironmentModification{
			{Name: "CPP_ADAPTER_RESET", Operation: "reset"},
			{Name: "CPP_ADAPTER_REMOVE", Operation: "unset"},
			{Name: "CPP_ADAPTER_SET", Operation: "set", Value: "set"},
			{Name: "CPP_ADAPTER_STRING", Operation: "string_prepend", Value: "before-"},
			{Name: "CPP_ADAPTER_STRING", Operation: "string_append", Value: "-after"},
			{Name: "CPP_ADAPTER_PATH", Operation: "path_list_prepend", Value: "before"},
			{Name: "CPP_ADAPTER_PATH", Operation: "path_list_append", Value: "after"},
			{Name: "CPP_ADAPTER_CMAKE", Operation: "cmake_list_prepend", Value: "before"},
			{Name: "CPP_ADAPTER_CMAKE", Operation: "cmake_list_append", Value: "after"},
		},
	}
	encoded, err := discoveryEnvironment(descriptor)
	if err != nil {
		t.Fatal(err)
	}
	environment := make(map[string]string, len(encoded))
	for _, entry := range encoded {
		name, value, ok := strings.Cut(entry, "=")
		if !ok {
			t.Fatalf("malformed environment entry %q", entry)
		}
		environment[environmentKey(name)] = value
	}
	pathSeparator := string(os.PathListSeparator)
	want := map[string]string{
		"CPP_ADAPTER_RESET":  "overridden",
		"CPP_ADAPTER_SET":    "set",
		"CPP_ADAPTER_STRING": "before-middle-after",
		"CPP_ADAPTER_PATH":   "before" + pathSeparator + "middle" + pathSeparator + "after",
		"CPP_ADAPTER_CMAKE":  "before;middle;after",
	}
	for name, value := range want {
		if got := environment[environmentKey(name)]; got != value {
			t.Fatalf("%s = %q, want %q", name, got, value)
		}
	}
	if _, exists := environment[environmentKey("CPP_ADAPTER_REMOVE")]; exists {
		t.Fatalf("unset environment was retained: %#v", environment)
	}
}

func TestCppUTestPreparesTaskOwnedDiscovery(t *testing.T) {
	t.Setenv("UNIT_TEST_SERVICE_TOKEN", "must-not-reach-target")
	t.Setenv("UTIDE_PRIVATE_VALUE", "must-not-reach-target")
	fixture := newPlannerFixture(t)
	adapter, err := NewAdapter(&integrationProbeRunner{})
	if err != nil {
		t.Fatal(err)
	}
	execution, err := adapter.PrepareDiscovery(
		context.Background(),
		fixture.descriptor,
	)
	if err != nil {
		t.Fatal(err)
	}
	if execution.Process.Executable !=
		fixture.descriptor.Executable.Path ||
		execution.Process.Dir != fixture.descriptor.WorkingDirectory ||
		!reflect.DeepEqual(
			execution.Public.Args,
			[]string{"<service-owned-discovery-invocation>"},
		) ||
		execution.Parser == nil {
		t.Fatalf("discovery execution = %#v", execution)
	}
	for _, entry := range execution.Process.Env {
		upper := strings.ToUpper(entry)
		if strings.HasPrefix(upper, "UTIDE_") ||
			strings.HasPrefix(upper, "UNIT_TEST_IDE_") ||
			strings.HasPrefix(
				upper,
				"UNIT_TEST_SERVICE_TOKEN=",
			) {
			t.Fatalf("service environment leaked: %q", entry)
		}
	}
	if err := execution.Parser.Feed(
		testframework.StreamStdout,
		readFixture(t, "list.valid.txt"),
	); err != nil {
		t.Fatal(err)
	}
	discovery, err := execution.Parser.Finish(
		context.Background(),
		testframework.ProcessResult{
			ExitCode:    0,
			Termination: testframework.ProcessExited,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(discovery.Items) != 7 ||
		discovery.Items[0].Kind != testdomain.ItemGroup ||
		discovery.Items[1].Kind != testdomain.ItemCase {
		t.Fatalf("task discovery = %#v", discovery)
	}
}

func TestCppUTestDiscoveryEnvironmentRejectsForgedDescriptor(t *testing.T) {
	tests := map[string]ctest.ExecutionDescriptor{
		"reserved environment": {
			Environment: []ctest.EnvironmentEntry{{
				Name: "UTIDE_COMMAND", Value: "forbidden",
			}},
		},
		"duplicate environment": {
			Environment: []ctest.EnvironmentEntry{
				{Name: "CPP_DUPLICATE", Value: "first"},
				{Name: "CPP_DUPLICATE", Value: "second"},
			},
		},
		"unknown operation": {
			EnvironmentChanges: []ctest.EnvironmentModification{{
				Name: "CPP_VALUE", Operation: "execute", Value: "forbidden",
			}},
		},
		"reset with value": {
			EnvironmentChanges: []ctest.EnvironmentModification{{
				Name: "CPP_VALUE", Operation: "reset", Value: "forbidden",
			}},
		},
	}
	for name, descriptor := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := discoveryEnvironment(descriptor); !errors.Is(
				err,
				ErrIncompatibleDescriptor,
			) {
				t.Fatalf("discoveryEnvironment() error = %v", err)
			}
		})
	}
}

type integrationProbeRunner struct {
	specs  []probe.Spec
	result probe.Result
	err    error
}

func (runner *integrationProbeRunner) Run(
	_ context.Context,
	spec probe.Spec,
) (probe.Result, error) {
	spec.Args = append([]string(nil), spec.Args...)
	spec.Env = append([]string(nil), spec.Env...)
	runner.specs = append(runner.specs, spec)
	result := runner.result
	result.Stdout = append([]byte(nil), result.Stdout...)
	result.Stderr = append([]byte(nil), result.Stderr...)
	return result, runner.err
}

func hasDiagnosticCategory(
	result testframework.ParseResult,
	category string,
) bool {
	for _, diagnostic := range result.Diagnostics {
		if diagnostic.Category == category {
			return true
		}
	}
	return false
}
