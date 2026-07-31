package testrun

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"unit-test-ide.local/test-service/internal/cmake"
	"unit-test-ide.local/test-service/internal/ctest"
	"unit-test-ide.local/test-service/internal/task"
	"unit-test-ide.local/test-service/internal/testdomain"
	"unit-test-ide.local/test-service/internal/testframework"
)

func TestPlannerBuildsDeterministicFrameworkInvocationsAndIterations(
	t *testing.T,
) {
	catalog, container, cases := plannerCatalog(
		t,
		testdomain.FrameworkCppUTest,
		"framework-tests",
		"Group",
		"CaseA",
		"CaseB",
	)
	adapter := &plannerAdapter{version: "cpputest.v1"}
	descriptor := plannerDescriptor(t, container.CTestLogicalName)
	selected := []testdomain.ID{cases[1].ID, cases[0].ID}
	slices.Sort(selected)

	plan, err := PlanRun(context.Background(), PlannerInput{
		Catalog: catalog,
		Selection: testdomain.SelectionSnapshot{
			Mode:    testdomain.SelectionItems,
			ItemIDs: selected,
		},
		Bindings: []ContainerBinding{{
			ContainerID: container.ID,
			Descriptor:  descriptor,
			Adapter:     adapter,
		}},
		RepeatCount:    2,
		TaskTimeout:    time.Minute,
		MaxConcurrency: 8,
	})
	if err != nil {
		t.Fatal(err)
	}
	if adapter.calls != 1 ||
		adapter.lastInput.Mode != testframework.RunSelectionCases ||
		len(adapter.lastInput.Items) != 2 {
		t.Fatalf("adapter input = %#v, calls=%d", adapter.lastInput, adapter.calls)
	}
	if len(plan.Invocations) != 4 || len(plan.Waves) != 4 {
		t.Fatalf("planned run = %#v", plan)
	}
	seenIterations := make(map[int64]int)
	for _, invocation := range plan.Invocations {
		seenIterations[invocation.Job.Iteration]++
		if invocation.Step.Kind != task.StepTestRun ||
			invocation.Step.Process.Executable != descriptor.Executable.Path ||
			invocation.Step.Process.Dir != descriptor.WorkingDirectory ||
			!reflect.DeepEqual(
				invocation.Step.Public.Args,
				[]string{"<service-owned-test-invocation>"},
			) ||
			invocation.Adapter != adapter ||
			invocation.AdapterVersion != adapter.version ||
			len(invocation.ExpectedCases) != 1 ||
			len(invocation.ParseInput.Items) != 1 ||
			invocation.ParseInput.Items[0].ItemID !=
				invocation.ExpectedCases[0].ItemID {
			t.Fatalf("invocation = %#v", invocation)
		}
		var state InvocationState
		if err := decodePlannerState(invocation.Step.State, &state); err != nil {
			t.Fatal(err)
		}
		if state.CatalogRevision != catalog.Revision ||
			state.ContainerID != container.ID ||
			state.Iteration != invocation.Job.Iteration ||
			state.AdapterVersion != adapter.version ||
			state.TimeoutMS != 60_000 {
			t.Fatalf("invocation state = %#v", state)
		}
	}
	if !reflect.DeepEqual(seenIterations, map[int64]int{1: 2, 2: 2}) {
		t.Fatalf("iterations = %#v", seenIterations)
	}
}

func TestPlannerUsesAnchoredCTestForOpaqueContainer(t *testing.T) {
	catalog, container, _ := plannerCatalog(
		t,
		testdomain.FrameworkOpaqueCTest,
		"name[1]",
		"",
	)
	runner, ctestPath := plannerCTestRunner(t)
	descriptor := plannerDescriptor(t, container.CTestLogicalName)
	descriptor.Executable = cmake.FingerprintFile{}
	descriptor.TargetID = ""
	descriptor.Compatibility.CaseLevel = false

	plan, err := PlanRun(context.Background(), PlannerInput{
		Catalog: catalog,
		Selection: testdomain.SelectionSnapshot{
			Mode:         testdomain.SelectionContainers,
			ContainerIDs: []testdomain.ID{container.ID},
		},
		Bindings: []ContainerBinding{{
			ContainerID: container.ID,
			Descriptor:  descriptor,
		}},
		Runner:         runner,
		RepeatCount:    1,
		TaskTimeout:    35 * time.Second,
		MaxConcurrency: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Invocations) != 1 {
		t.Fatalf("invocations = %#v", plan.Invocations)
	}
	invocation := plan.Invocations[0]
	if invocation.Step.Process.Executable != ctestPath ||
		!slices.Contains(invocation.Step.Process.Args, "^name\\[1\\]$") ||
		invocation.Adapter != nil ||
		len(invocation.ExpectedCases) != 1 ||
		invocation.ExpectedCases[0].ItemID != container.ID {
		t.Fatalf("opaque invocation = %#v", invocation)
	}
}

func TestPlannerRejectsStaleBlockedAndUnreplayableInputs(t *testing.T) {
	catalog, container, cases := plannerCatalog(
		t,
		testdomain.FrameworkCppUTest,
		"framework-tests",
		"Group",
		"CaseA",
	)
	descriptor := plannerDescriptor(t, container.CTestLogicalName)
	valid := PlannerInput{
		Catalog: catalog,
		Selection: testdomain.SelectionSnapshot{
			Mode:    testdomain.SelectionItems,
			ItemIDs: []testdomain.ID{cases[0].ID},
		},
		Bindings: []ContainerBinding{{
			ContainerID: container.ID,
			Descriptor:  descriptor,
			Adapter:     &plannerAdapter{version: "cpputest.v1"},
		}},
		RepeatCount: 1, TaskTimeout: time.Minute, MaxConcurrency: 1,
	}
	unknownID, err := testdomain.CaseID(testdomain.CaseIdentity{
		ProjectID: "project", CTestName: "framework-tests",
		Framework: testdomain.FrameworkCppUTest,
		Group:     "Group", Name: "DeletedCase",
	})
	if err != nil {
		t.Fatal(err)
	}

	tests := map[string]func(PlannerInput) PlannerInput{
		"stale selected ID": func(value PlannerInput) PlannerInput {
			value.Selection.ItemIDs = []testdomain.ID{unknownID}
			return value
		},
		"blocked descriptor": func(value PlannerInput) PlannerInput {
			value.Bindings[0].Descriptor.Blocked = true
			return value
		},
		"framework mismatch": func(value PlannerInput) PlannerInput {
			value.Bindings[0].Adapter = &plannerAdapter{
				framework: testdomain.FrameworkUnity,
				version:   "unity.v1",
			}
			return value
		},
		"environment modification": func(value PlannerInput) PlannerInput {
			value.Bindings[0].Adapter = &plannerAdapter{
				version: "cpputest.v1",
				environmentChanges: []ctest.EnvironmentModification{{
					Name: "PATH", Operation: "path_list_append", Value: "bin",
				}},
			}
			return value
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			input := clonePlannerInput(valid)
			if _, err := PlanRun(
				context.Background(),
				mutate(input),
			); !errors.Is(err, task.ErrInvalidArgument) {
				t.Fatalf("PlanRun() error = %v", err)
			}
		})
	}
}

type plannerAdapter struct {
	framework          testdomain.Framework
	version            string
	environmentChanges []ctest.EnvironmentModification
	calls              int
	lastInput          testframework.RunInput
}

func (adapter *plannerAdapter) Framework() testdomain.Framework {
	if adapter.framework == "" {
		return testdomain.FrameworkCppUTest
	}
	return adapter.framework
}

func (adapter *plannerAdapter) ContractVersion() string {
	return adapter.version
}

func (*plannerAdapter) Verify(
	context.Context,
	ctest.ExecutionDescriptor,
) (testframework.Capabilities, error) {
	return testframework.Capabilities{
		CanRunContainer:  true,
		CanDiscoverCases: true,
		CanRunCase:       true,
	}, nil
}

func (*plannerAdapter) Discover(
	context.Context,
	ctest.ExecutionDescriptor,
) (testframework.DiscoveryResult, error) {
	return testframework.DiscoveryResult{}, errors.New("not used")
}

func (adapter *plannerAdapter) PlanRun(
	_ context.Context,
	input testframework.RunInput,
) (testframework.RunPlan, error) {
	adapter.calls++
	adapter.lastInput = input
	invocations := make(
		[]testframework.RunInvocation,
		len(input.Items),
	)
	for index, item := range input.Items {
		invocations[index] = testframework.RunInvocation{
			Arguments: []string{
				"--case",
				item.ItemID.String(),
			},
			ExpectedCases: []testframework.ExpectedCase{{
				ItemID:            item.ItemID,
				ParentLogicalName: item.ParentLogicalName,
				LogicalName:       item.LogicalName,
			}},
		}
	}
	return testframework.RunPlan{
		Invocations: invocations,
		Environment: []ctest.EnvironmentEntry{{
			Name: "FRAMEWORK_MODE", Value: "test",
		}},
		EnvironmentChanges: append(
			[]ctest.EnvironmentModification(nil),
			adapter.environmentChanges...,
		),
		WorkingDirectory: input.Descriptor.WorkingDirectory,
	}, nil
}

func (*plannerAdapter) NewParser(
	testframework.ParseInput,
) (testframework.ResultParser, error) {
	return nil, errors.New("not used")
}

func plannerCatalog(
	t *testing.T,
	framework testdomain.Framework,
	ctestName string,
	group string,
	caseNames ...string,
) (testdomain.Catalog, testdomain.Container, []testdomain.Item) {
	t.Helper()
	containerID, err := testdomain.ContainerID("project", ctestName)
	if err != nil {
		t.Fatal(err)
	}
	container := testdomain.Container{
		ID: containerID, ProjectID: "project",
		CTestLogicalName: ctestName, DisplayName: ctestName,
		Framework: framework,
		Capabilities: testdomain.Capabilities{
			CanDiscoverCases: framework != testdomain.FrameworkOpaqueCTest,
			CanRunCase:       framework != testdomain.FrameworkOpaqueCTest,
		},
		Labels: []string{},
	}
	items := make([]testdomain.Item, 0, len(caseNames)+1)
	var groupID testdomain.ID
	if group != "" {
		groupID, err = testdomain.GroupID(
			"project",
			ctestName,
			framework,
			group,
		)
		if err != nil {
			t.Fatal(err)
		}
		items = append(items, testdomain.Item{
			ID: groupID, ContainerID: containerID,
			Kind: testdomain.ItemGroup, Framework: framework,
			LogicalName: group, DisplayName: group, Labels: []string{},
		})
	}
	cases := make([]testdomain.Item, len(caseNames))
	for index, name := range caseNames {
		caseID, err := testdomain.CaseID(testdomain.CaseIdentity{
			ProjectID: "project", CTestName: ctestName,
			Framework: framework, Group: group, Name: name,
		})
		if err != nil {
			t.Fatal(err)
		}
		cases[index] = testdomain.Item{
			ID: caseID, ContainerID: containerID, ParentID: groupID,
			Kind: testdomain.ItemCase, Framework: framework,
			LogicalName: name, DisplayName: name, Labels: []string{},
		}
		items = append(items, cases[index])
	}
	catalog, err := testdomain.NewCatalog(testdomain.Catalog{
		ProjectID: "project", ProfileID: strings.Repeat("a", 64),
		Revision: strings.Repeat("b", 64),
		GeneratedAt: time.Date(
			2026, 7, 31, 8, 0, 0, 0, time.UTC,
		),
		Containers: []testdomain.Container{container},
		Items:      items, Diagnostics: []testdomain.Diagnostic{},
	})
	if err != nil {
		t.Fatal(err)
	}
	return catalog, container, cases
}

func plannerDescriptor(
	t *testing.T,
	logicalName string,
) ctest.ExecutionDescriptor {
	t.Helper()
	root := t.TempDir()
	executable := filepath.Join(root, "framework-test.exe")
	return ctest.ExecutionDescriptor{
		LogicalName:   logicalName,
		TestDirectory: root,
		TargetID:      "target",
		Executable: cmake.FingerprintFile{
			Path: executable, Identity: "identity",
			SHA256: strings.Repeat("c", 64),
		},
		WorkingDirectory: root,
		Environment:      []ctest.EnvironmentEntry{},
		Compatibility: ctest.Compatibility{
			CaseLevel: true, Reasons: []ctest.Reason{},
		},
	}
}

func plannerCTestRunner(t *testing.T) (*ctest.Runner, string) {
	t.Helper()
	root := t.TempDir()
	ctestPath := filepath.Join(root, "ctest.exe")
	runner, err := ctest.NewRunner(cmake.Installation{
		Executable:      filepath.Join(root, "cmake.exe"),
		CTestExecutable: ctestPath,
		Version:         "4.3.4",
		Identity:        strings.Repeat("d", 64),
	})
	if err != nil {
		t.Fatal(err)
	}
	return runner, ctestPath
}

func clonePlannerInput(value PlannerInput) PlannerInput {
	value.Catalog = value.Catalog.Clone()
	value.Selection = value.Selection.Clone()
	value.Bindings = append([]ContainerBinding(nil), value.Bindings...)
	return value
}
