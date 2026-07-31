package unity

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"unit-test-ide.local/test-service/internal/testdomain"
	"unit-test-ide.local/test-service/internal/testframework"
)

func TestPlanRunCreatesOneOwnedExactInvocationPerCase(t *testing.T) {
	for _, mode := range []testframework.RunSelectionMode{
		testframework.RunSelectionAll,
		testframework.RunSelectionGroup,
		testframework.RunSelectionCases,
	} {
		t.Run(string(mode), func(t *testing.T) {
			fixture := newAdapterFixture(t)
			allocator := &fakeControlAllocator{root: fixture.controlDir}
			adapter, err := NewAdapter(&fakeRunner{}, allocator)
			if err != nil {
				t.Fatal(err)
			}
			items := fixture.runItems()
			items[0], items[1] = items[1], items[0]
			plan, err := adapter.PlanRun(
				context.Background(),
				testframework.RunInput{
					Descriptor: fixture.descriptor,
					Mode:       mode,
					Items:      items,
				},
			)
			if err != nil {
				t.Fatal(err)
			}
			if len(plan.Invocations) != 2 || len(allocator.files) != 2 {
				t.Fatalf("plan = %#v allocations=%d", plan, len(allocator.files))
			}
			wantIdentities := []string{
				"test_adds_numbers",
				"test_handles_zero",
			}
			for index, invocation := range plan.Invocations {
				if invocation.ControlFile != allocator.files[index] ||
					len(invocation.ExpectedCases) != 1 ||
					invocation.ExpectedCases[0].LogicalName !=
						wantIdentities[index] {
					t.Fatalf("invocation[%d] = %#v", index, invocation)
				}
				want := []string{
					"--utide-protocol", ContractVersion,
					"--utide-mode", "run",
					"--utide-case", wantIdentities[index],
					"--utide-result", allocator.files[index].path,
				}
				if !reflect.DeepEqual(invocation.Arguments, want) {
					t.Fatalf(
						"argv[%d] = %#v, want %#v",
						index,
						invocation.Arguments,
						want,
					)
				}
			}
			if !reflect.DeepEqual(
				plan.Environment,
				fixture.descriptor.Environment,
			) ||
				!reflect.DeepEqual(
					plan.EnvironmentChanges,
					fixture.descriptor.EnvironmentChanges,
				) ||
				plan.WorkingDirectory != fixture.descriptor.WorkingDirectory ||
				plan.TimeoutSeconds == fixture.descriptor.TimeoutSeconds ||
				*plan.TimeoutSeconds != *fixture.descriptor.TimeoutSeconds {
				t.Fatalf("descriptor-owned fields = %#v", plan)
			}
		})
	}
}

func TestPlanRunIsDeterministicAndClonesMutableInputs(t *testing.T) {
	fixture := newAdapterFixture(t)
	items := fixture.runItems()
	reversed := []testframework.RunItem{items[1], items[0]}

	firstAllocator := &fakeControlAllocator{root: fixture.controlDir}
	firstAdapter, err := NewAdapter(&fakeRunner{}, firstAllocator)
	if err != nil {
		t.Fatal(err)
	}
	first, err := firstAdapter.PlanRun(
		context.Background(),
		testframework.RunInput{
			Descriptor: fixture.descriptor,
			Mode:       testframework.RunSelectionCases,
			Items:      reversed,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	secondAllocator := &fakeControlAllocator{root: fixture.controlDir}
	secondAdapter, err := NewAdapter(&fakeRunner{}, secondAllocator)
	if err != nil {
		t.Fatal(err)
	}
	second, err := secondAdapter.PlanRun(
		context.Background(),
		testframework.RunInput{
			Descriptor: fixture.descriptor,
			Mode:       testframework.RunSelectionCases,
			Items:      items,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	for index := range first.Invocations {
		if !reflect.DeepEqual(
			first.Invocations[index].Arguments,
			second.Invocations[index].Arguments,
		) ||
			!reflect.DeepEqual(
				first.Invocations[index].ExpectedCases,
				second.Invocations[index].ExpectedCases,
			) {
			t.Fatalf("selection order changed plan:\n%#v\n%#v", first, second)
		}
	}

	fixture.descriptor.Environment[0].Value = "mutated"
	reversed[0].LogicalName = "mutated"
	if first.Environment[0].Value != "fixed" ||
		first.Invocations[1].ExpectedCases[0].LogicalName !=
			"test_handles_zero" {
		t.Fatalf("plan aliases input: %#v", first)
	}
}

func TestPlanRunRejectsNonManifestAndInvalidSelections(t *testing.T) {
	tests := map[string]func(*testframework.RunInput){
		"invalid mode": func(input *testframework.RunInput) {
			input.Mode = "client-mode"
		},
		"missing items": func(input *testframework.RunInput) {
			input.Items = nil
		},
		"unknown identity": func(input *testframework.RunInput) {
			input.Items[0].LogicalName = "test_client_supplied"
		},
		"wrong source": func(input *testframework.RunInput) {
			input.Items[0].ParentLogicalName = "other.c"
		},
		"wrong parameters": func(input *testframework.RunInput) {
			input.Items[0].Parameters = []testdomain.Parameter{{
				Name: "client", Value: "value",
			}}
		},
		"duplicate item": func(input *testframework.RunInput) {
			input.Items = append(input.Items, input.Items[0])
		},
		"group mismatch": func(input *testframework.RunInput) {
			input.Mode = testframework.RunSelectionGroup
			input.Items[1].ParentLogicalName = "other.c"
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			fixture := newAdapterFixture(t)
			adapter, err := NewAdapter(
				&fakeRunner{},
				&fakeControlAllocator{root: fixture.controlDir},
			)
			if err != nil {
				t.Fatal(err)
			}
			input := testframework.RunInput{
				Descriptor: fixture.descriptor,
				Mode:       testframework.RunSelectionCases,
				Items:      fixture.runItems(),
			}
			mutate(&input)
			if _, err := adapter.PlanRun(
				context.Background(),
				input,
			); !errors.Is(err, ErrInvalidRunPlan) {
				t.Fatalf("PlanRun() error = %v", err)
			}
		})
	}
}
