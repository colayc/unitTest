package cpputest

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"unit-test-ide.local/test-service/internal/cmake"
	"unit-test-ide.local/test-service/internal/ctest"
	"unit-test-ide.local/test-service/internal/testdomain"
	"unit-test-ide.local/test-service/internal/testframework"
)

func TestBuildRunPlanCreatesAllGroupAndExactInvocations(t *testing.T) {
	fixture := newPlannerFixture(t)
	first := selectedCase("1", "Core", "passes")
	second := selectedCase("2", "Core", "fails")
	tests := map[string]struct {
		selection Selection
		want      []testframework.RunInvocation
	}{
		"all": {
			selection: Selection{
				Mode:  SelectionAll,
				Cases: []SelectedCase{first, second},
			},
			want: []testframework.RunInvocation{{
				Arguments: append(
					append([]string(nil), fixture.descriptor.Arguments...),
					"-v",
				),
				ExpectedCases: expectedCases(second, first),
			}},
		},
		"group": {
			selection: Selection{
				Mode:  SelectionGroup,
				Group: "Core",
				Cases: []SelectedCase{first, second},
			},
			want: []testframework.RunInvocation{{
				Arguments: append(
					append([]string(nil), fixture.descriptor.Arguments...),
					"-v", "-sg", "Core",
				),
				ExpectedCases: expectedCases(second, first),
			}},
		},
		"exact case": {
			selection: Selection{
				Mode:  SelectionCases,
				Cases: []SelectedCase{first},
			},
			want: []testframework.RunInvocation{{
				Arguments: append(
					append([]string(nil), fixture.descriptor.Arguments...),
					"-v", "-sg", "Core", "-sn", "passes",
				),
				ExpectedCases: expectedCases(first),
			}},
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			plan, err := BuildRunPlan(fixture.descriptor, test.selection)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(plan.Invocations, test.want) {
				t.Fatalf("Invocations = %#v, want %#v", plan.Invocations, test.want)
			}
			if plan.WorkingDirectory != fixture.descriptor.WorkingDirectory ||
				!reflect.DeepEqual(plan.Environment, fixture.descriptor.Environment) ||
				!reflect.DeepEqual(
					plan.EnvironmentChanges,
					fixture.descriptor.EnvironmentChanges,
				) ||
				plan.TimeoutSeconds == fixture.descriptor.TimeoutSeconds ||
				*plan.TimeoutSeconds != *fixture.descriptor.TimeoutSeconds {
				t.Fatalf("descriptor-owned plan fields = %#v", plan)
			}
		})
	}
}

func TestBuildRunPlanSortsDiscreteCasesAndClonesInputs(t *testing.T) {
	fixture := newPlannerFixture(t)
	first := selectedCase("1", "Alpha", "first")
	second := selectedCase("2", "Beta", "second")
	selection := Selection{
		Mode:  SelectionCases,
		Cases: []SelectedCase{second, first},
	}
	plan, err := BuildRunPlan(fixture.descriptor, selection)
	if err != nil {
		t.Fatal(err)
	}
	reordered, err := BuildRunPlan(fixture.descriptor, Selection{
		Mode:  SelectionCases,
		Cases: []SelectedCase{first, second},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(plan, reordered) {
		t.Fatalf("selection order changed plan:\n%#v\n%#v", plan, reordered)
	}
	if len(plan.Invocations) != 2 ||
		!reflect.DeepEqual(
			plan.Invocations[0].Arguments[len(fixture.descriptor.Arguments):],
			[]string{"-v", "-sg", "Alpha", "-sn", "first"},
		) ||
		!reflect.DeepEqual(
			plan.Invocations[1].Arguments[len(fixture.descriptor.Arguments):],
			[]string{"-v", "-sg", "Beta", "-sn", "second"},
		) {
		t.Fatalf("discrete invocations = %#v", plan.Invocations)
	}

	fixture.descriptor.Arguments[0] = "--mutated"
	fixture.descriptor.Environment[0].Value = "mutated"
	selection.Cases[0].Name = "mutated"
	if plan.Invocations[0].Arguments[0] != "--fixture" ||
		plan.Environment[0].Value != "fixed" ||
		plan.Invocations[1].ExpectedCases[0].LogicalName != "second" {
		t.Fatalf("RunPlan aliases mutable input: %#v", plan)
	}
	plan.Invocations[0].Arguments[0] = "--plan-mutated"
	plan.Invocations[0].ExpectedCases[0].LogicalName = "plan-mutated"
	if plan.Invocations[1].Arguments[0] != "--fixture" ||
		plan.Invocations[1].ExpectedCases[0].LogicalName != "second" {
		t.Fatalf("RunPlan invocations alias each other: %#v", plan)
	}
}

func TestBuildRunPlanKeepsShellMetacharactersAsLiteralArguments(t *testing.T) {
	fixture := newPlannerFixture(t)
	group := `Group;$()[]{}&|`
	name := `Case*?^$()+{}[]|`
	item := selectedCase("1", group, name)
	plan, err := BuildRunPlan(fixture.descriptor, Selection{
		Mode:  SelectionCases,
		Cases: []SelectedCase{item},
	})
	if err != nil {
		t.Fatal(err)
	}
	added := plan.Invocations[0].Arguments[len(fixture.descriptor.Arguments):]
	if !reflect.DeepEqual(
		added,
		[]string{"-v", "-sg", group, "-sn", name},
	) {
		t.Fatalf("literal arguments = %#v", added)
	}
	for _, argument := range plan.Invocations[0].Arguments {
		if strings.Contains(argument, "client-filter") {
			t.Fatalf("client filter text entered argv: %#v", plan.Invocations)
		}
	}
}

func TestBuildRunPlanRejectsReservedCppUTestArguments(t *testing.T) {
	fixture := newPlannerFixture(t)
	selection := Selection{
		Mode:  SelectionCases,
		Cases: []SelectedCase{selectedCase("1", "Core", "passes")},
	}
	for _, argument := range []string{
		"-g", "-n", "-s", "-sg", "-sn", "-xg", "-xn",
		"-r", "-r10", "-ri", "-v", "-lg", "-ln", "-ojunit",
		"-oteamcity", "-p", "TEST(Core, passes)",
	} {
		t.Run(argument, func(t *testing.T) {
			descriptor := fixture.descriptor
			descriptor.Arguments = []string{"--fixture", argument}
			if _, err := BuildRunPlan(descriptor, selection); !errors.Is(
				err,
				ErrReservedArguments,
			) {
				t.Fatalf("BuildRunPlan() error = %v, want ErrReservedArguments", err)
			}
		})
	}

	descriptor := fixture.descriptor
	descriptor.Arguments = []string{"--fixture", "-color", "fixed"}
	if _, err := BuildRunPlan(descriptor, selection); err != nil {
		t.Fatalf("non-reserved arguments rejected: %v", err)
	}
}

func TestBuildRunPlanRejectsInvalidSelectionAndExecutableMutation(t *testing.T) {
	fixture := newPlannerFixture(t)
	valid := Selection{
		Mode:  SelectionCases,
		Cases: []SelectedCase{selectedCase("1", "Core", "passes")},
	}
	tests := map[string]func(*ctest.ExecutionDescriptor, *Selection){
		"blocked descriptor": func(descriptor *ctest.ExecutionDescriptor, _ *Selection) {
			descriptor.Blocked = true
		},
		"incompatible descriptor": func(descriptor *ctest.ExecutionDescriptor, _ *Selection) {
			descriptor.Compatibility = ctest.Compatibility{
				CaseLevel: false,
				Reasons:   []ctest.Reason{ctest.ReasonUnsupportedProperty},
			}
		},
		"relative working directory": func(descriptor *ctest.ExecutionDescriptor, _ *Selection) {
			descriptor.WorkingDirectory = "relative"
		},
		"invalid mode": func(_ *ctest.ExecutionDescriptor, selection *Selection) {
			selection.Mode = "invalid"
		},
		"missing exact cases": func(_ *ctest.ExecutionDescriptor, selection *Selection) {
			selection.Cases = nil
		},
		"missing group cases": func(_ *ctest.ExecutionDescriptor, selection *Selection) {
			selection.Mode = SelectionGroup
			selection.Group = "Core"
			selection.Cases = nil
		},
		"group case mismatch": func(_ *ctest.ExecutionDescriptor, selection *Selection) {
			selection.Mode = SelectionGroup
			selection.Group = "Other"
		},
		"duplicate case": func(_ *ctest.ExecutionDescriptor, selection *Selection) {
			selection.Cases = append(selection.Cases, selection.Cases[0])
		},
		"duplicate logical identity": func(_ *ctest.ExecutionDescriptor, selection *Selection) {
			duplicate := selection.Cases[0]
			duplicate.ItemID = testdomain.ID("utid-v1-" + strings.Repeat("2", 64))
			selection.Cases = append(selection.Cases, duplicate)
		},
		"invalid item ID": func(_ *ctest.ExecutionDescriptor, selection *Selection) {
			selection.Cases[0].ItemID = "client-text"
		},
		"invalid group": func(_ *ctest.ExecutionDescriptor, selection *Selection) {
			selection.Cases[0].Group = "Core group"
		},
		"ambiguous case name": func(_ *ctest.ExecutionDescriptor, selection *Selection) {
			selection.Cases[0].Name = "Core.passes"
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			descriptor := fixture.descriptor
			selection := valid
			selection.Cases = append([]SelectedCase(nil), valid.Cases...)
			mutate(&descriptor, &selection)
			if _, err := BuildRunPlan(descriptor, selection); err == nil {
				t.Fatal("BuildRunPlan() error = nil")
			}
		})
	}

	if err := os.WriteFile(
		fixture.descriptor.Executable.Path,
		[]byte("replacement executable"),
		0o700,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := BuildRunPlan(fixture.descriptor, valid); !errors.Is(
		err,
		cmake.ErrTargetArtifactChanged,
	) {
		t.Fatalf("mutated executable error = %v", err)
	}
}

func TestCppUTestSelectionHasNoClientCommandSurface(t *testing.T) {
	assertFields := func(value any, want []string) {
		t.Helper()
		kind := reflect.TypeOf(value)
		got := make([]string, kind.NumField())
		for index := range got {
			got[index] = kind.Field(index).Name
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("%s fields = %#v, want %#v", kind.Name(), got, want)
		}
	}
	assertFields(Selection{}, []string{"Mode", "Group", "Cases"})
	assertFields(SelectedCase{}, []string{"ItemID", "Group", "Name"})
}

type plannerFixture struct {
	descriptor ctest.ExecutionDescriptor
}

func newPlannerFixture(t *testing.T) plannerFixture {
	t.Helper()
	root := t.TempDir()
	sourceDir := filepath.Join(root, "source")
	buildDir := filepath.Join(root, "build")
	if err := os.MkdirAll(sourceDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(buildDir, "bin"), 0o700); err != nil {
		t.Fatal(err)
	}
	executable := filepath.Join(buildDir, "bin", "tests.exe")
	if err := os.WriteFile(executable, []byte("test executable"), 0o700); err != nil {
		t.Fatal(err)
	}
	profile := cmake.BuildProfile{
		ID:            strings.Repeat("a", 64),
		ProjectID:     "core",
		BinaryDir:     buildDir,
		Configuration: "Debug",
	}
	target := cmake.Target{
		ID: "target-1", Name: "tests", Type: "EXECUTABLE",
		ProjectID: "core", ProfileID: profile.ID, Configuration: "Debug",
		SourceDir: sourceDir, BuildDir: buildDir,
		ProjectSourceDir: sourceDir, ProjectBuildDir: buildDir,
		Artifacts: []string{executable},
	}
	state, err := cmake.SnapshotTargetArtifact(profile, target, executable)
	if err != nil {
		t.Fatal(err)
	}
	timeout := 10.0
	return plannerFixture{descriptor: ctest.ExecutionDescriptor{
		LogicalName:      "core.tests",
		TestDirectory:    buildDir,
		Configuration:    "Debug",
		TargetID:         target.ID,
		Executable:       state,
		Arguments:        []string{"--fixture", "fixed"},
		WorkingDirectory: buildDir,
		Environment: []ctest.EnvironmentEntry{{
			Name: "FIXTURE_MODE", Value: "fixed",
		}},
		EnvironmentChanges: []ctest.EnvironmentModification{{
			Name: "PATH", Operation: "path_list_prepend", Value: buildDir,
		}},
		TimeoutSeconds: &timeout,
		Compatibility:  ctest.Compatibility{CaseLevel: true, Reasons: []ctest.Reason{}},
	}}
}

func selectedCase(seed, group, name string) SelectedCase {
	return SelectedCase{
		ItemID: testdomain.ID("utid-v1-" + strings.Repeat(seed, 64)),
		Group:  group,
		Name:   name,
	}
}

func expectedCases(values ...SelectedCase) []testframework.ExpectedCase {
	result := make([]testframework.ExpectedCase, len(values))
	for index, value := range values {
		result[index] = testframework.ExpectedCase{
			ItemID:            value.ItemID,
			ParentLogicalName: value.Group,
			LogicalName:       value.Name,
		}
	}
	return result
}
