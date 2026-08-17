package testframework

import (
	"context"
	"errors"
	"testing"

	"unit-test-ide.local/test-service/internal/ctest"
	"unit-test-ide.local/test-service/internal/testdomain"
)

func TestRegistryUsesHelperThenExactWorkspaceMapping(t *testing.T) {
	cpp := &fakeAdapter{framework: testdomain.FrameworkCppUTest, version: "cpputest.v1"}
	unity := &fakeAdapter{framework: testdomain.FrameworkUnity, version: UnityRunnerV1}
	registry, err := NewRegistry(cpp, unity)
	if err != nil {
		t.Fatal(err)
	}
	descriptor := compatibleDescriptor("core.tests")

	t.Run("helper priority", func(t *testing.T) {
		selection, err := registry.Select(context.Background(), SelectionInput{
			Descriptor: descriptor,
			Helper: &Declaration{
				CTestName: "core.tests", Framework: testdomain.FrameworkCppUTest,
				ContractVersion: "cpputest.v1",
			},
			Mappings: []Mapping{{CTestName: "other.tests", Framework: testdomain.FrameworkUnity}},
		})
		if err != nil {
			t.Fatal(err)
		}
		if selection.Framework != testdomain.FrameworkCppUTest ||
			selection.Source != SourceHelper || selection.Adapter != cpp ||
			cpp.verifyCalls != 1 || unity.verifyCalls != 0 {
			t.Fatalf("helper selection = %#v, calls=(%d,%d)", selection, cpp.verifyCalls, unity.verifyCalls)
		}
	})

	t.Run("consistent helper and mapping", func(t *testing.T) {
		selection, err := registry.Select(context.Background(), SelectionInput{
			Descriptor: descriptor,
			Helper: &Declaration{
				CTestName: "core.tests", Framework: testdomain.FrameworkUnity,
				ContractVersion: UnityRunnerV1,
			},
			Mappings: []Mapping{{CTestName: "core.tests", Framework: testdomain.FrameworkUnity}},
		})
		if err != nil || selection.Framework != testdomain.FrameworkUnity ||
			selection.Source != SourceHelper || selection.Adapter != unity {
			t.Fatalf("consistent selection = %#v, %v", selection, err)
		}
	})

	t.Run("exact mapping", func(t *testing.T) {
		selection, err := registry.Select(context.Background(), SelectionInput{
			Descriptor: descriptor,
			Mappings: []Mapping{
				{CTestName: "core.tests.extra", Framework: testdomain.FrameworkUnity},
				{CTestName: "core.tests", Framework: testdomain.FrameworkCppUTest},
			},
		})
		if err != nil || selection.Framework != testdomain.FrameworkCppUTest ||
			selection.Source != SourceWorkspace || selection.Adapter != cpp {
			t.Fatalf("mapping selection = %#v, %v", selection, err)
		}
	})
}

func TestRegistryRejectsHelperMappingConflict(t *testing.T) {
	registry, err := NewRegistry(
		&fakeAdapter{framework: testdomain.FrameworkCppUTest, version: "cpputest.v1"},
		&fakeAdapter{framework: testdomain.FrameworkUnity, version: UnityRunnerV1},
	)
	if err != nil {
		t.Fatal(err)
	}
	_, err = registry.Select(context.Background(), SelectionInput{
		Descriptor: compatibleDescriptor("core.tests"),
		Helper: &Declaration{
			CTestName: "core.tests", Framework: testdomain.FrameworkCppUTest,
			ContractVersion: "cpputest.v1",
		},
		Mappings: []Mapping{{CTestName: "core.tests", Framework: testdomain.FrameworkUnity}},
	})
	if !errors.Is(err, ErrFrameworkConfigurationConflict) {
		t.Fatalf("Select() error = %v", err)
	}
}

func TestRegistryFallsBackOpaqueWithoutProbingUnknownBinary(t *testing.T) {
	adapter := &fakeAdapter{framework: testdomain.FrameworkCppUTest, version: "cpputest.v1"}
	registry, err := NewRegistry(adapter)
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]SelectionInput{
		"no metadata": {
			Descriptor: compatibleDescriptor("unknown.tests"),
		},
		"unavailable declared adapter": {
			Descriptor: compatibleDescriptor("unity.tests"),
			Mappings:   []Mapping{{CTestName: "unity.tests", Framework: testdomain.FrameworkUnity}},
		},
		"incompatible CTest descriptor": {
			Descriptor: ctest.ExecutionDescriptor{
				LogicalName: "core.tests",
				Compatibility: ctest.Compatibility{
					CaseLevel: false,
					Reasons:   []ctest.Reason{ctest.ReasonUnsupportedProperty},
				},
			},
			Mappings: []Mapping{{CTestName: "core.tests", Framework: testdomain.FrameworkCppUTest}},
		},
	}
	for name, input := range cases {
		t.Run(name, func(t *testing.T) {
			before := adapter.verifyCalls
			selection, err := registry.Select(context.Background(), input)
			if err != nil {
				t.Fatal(err)
			}
			if selection.Framework != testdomain.FrameworkOpaqueCTest ||
				selection.Adapter != nil ||
				selection.Capabilities.CanDiscoverCases ||
				selection.Capabilities.CanRunCase ||
				!selection.Capabilities.CanRunContainer ||
				adapter.verifyCalls != before {
				t.Fatalf("opaque selection = %#v, verify calls=%d", selection, adapter.verifyCalls)
			}
		})
	}
}

func TestRegistryVerificationFailureDegradesOnlySelection(t *testing.T) {
	verifyErr := errors.New("malformed adapter evidence")
	adapter := &fakeAdapter{
		framework: testdomain.FrameworkCppUTest, version: "cpputest.v1",
		verifyErr: verifyErr,
	}
	registry, err := NewRegistry(adapter)
	if err != nil {
		t.Fatal(err)
	}
	selection, err := registry.Select(context.Background(), SelectionInput{
		Descriptor: compatibleDescriptor("core.tests"),
		Mappings:   []Mapping{{CTestName: "core.tests", Framework: testdomain.FrameworkCppUTest}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if selection.Framework != testdomain.FrameworkOpaqueCTest ||
		selection.DegradedReason != DegradedAdapterVerification ||
		adapter.verifyCalls != 1 {
		t.Fatalf("selection = %#v, calls=%d", selection, adapter.verifyCalls)
	}
}

func TestRegistryRejectsInvalidVerifiedCapabilities(t *testing.T) {
	adapter := &fakeAdapter{
		framework: testdomain.FrameworkCppUTest,
		version:   "cpputest.v1",
		capabilities: Capabilities{
			CanRunContainer:  true,
			CanDiscoverCases: false,
			CanRunCase:       true,
		},
	}
	registry, err := NewRegistry(adapter)
	if err != nil {
		t.Fatal(err)
	}
	selection, err := registry.Select(context.Background(), SelectionInput{
		Descriptor: compatibleDescriptor("core.tests"),
		Helper: &Declaration{
			CTestName:       "core.tests",
			Framework:       testdomain.FrameworkCppUTest,
			ContractVersion: "cpputest.v1",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if selection.Framework != testdomain.FrameworkOpaqueCTest ||
		selection.DegradedReason != DegradedAdapterVerification ||
		adapter.verifyCalls != 1 {
		t.Fatalf("selection = %#v, calls=%d", selection, adapter.verifyCalls)
	}
}

func TestRegistryRequiresHelperVersionWhenFrameworkHasMultipleContracts(t *testing.T) {
	first := &fakeAdapter{framework: testdomain.FrameworkCppUTest, version: "cpputest.v1"}
	second := &fakeAdapter{framework: testdomain.FrameworkCppUTest, version: "cpputest.v2"}
	registry, err := NewRegistry(first, second)
	if err != nil {
		t.Fatal(err)
	}

	mapped, err := registry.Select(context.Background(), SelectionInput{
		Descriptor: compatibleDescriptor("core.tests"),
		Mappings: []Mapping{{
			CTestName: "core.tests",
			Framework: testdomain.FrameworkCppUTest,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if mapped.Framework != testdomain.FrameworkOpaqueCTest ||
		mapped.DegradedReason != DegradedAdapterUnavailable ||
		first.verifyCalls != 0 || second.verifyCalls != 0 {
		t.Fatalf("mapped selection = %#v, calls=(%d,%d)", mapped, first.verifyCalls, second.verifyCalls)
	}

	declared, err := registry.Select(context.Background(), SelectionInput{
		Descriptor: compatibleDescriptor("core.tests"),
		Helper: &Declaration{
			CTestName:       "core.tests",
			Framework:       testdomain.FrameworkCppUTest,
			ContractVersion: "cpputest.v2",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if declared.Adapter != second || second.verifyCalls != 1 {
		t.Fatalf("declared selection = %#v, calls=(%d,%d)", declared, first.verifyCalls, second.verifyCalls)
	}
}

func TestRegistryRejectsDuplicateAndInvalidAdapters(t *testing.T) {
	valid := &fakeAdapter{framework: testdomain.FrameworkCppUTest, version: "cpputest.v1"}
	var typedNil *fakeAdapter
	cases := map[string][]Adapter{
		"duplicate framework version": {
			valid,
			&fakeAdapter{framework: testdomain.FrameworkCppUTest, version: "cpputest.v1"},
		},
		"opaque registration": {
			&fakeAdapter{framework: testdomain.FrameworkOpaqueCTest, version: "opaque.v1"},
		},
		"empty version": {
			&fakeAdapter{framework: testdomain.FrameworkUnity},
		},
		"typed nil": {
			typedNil,
		},
	}
	for name, adapters := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := NewRegistry(adapters...); !errors.Is(err, ErrInvalidAdapter) {
				t.Fatalf("NewRegistry() error = %v", err)
			}
		})
	}
}

func compatibleDescriptor(name string) ctest.ExecutionDescriptor {
	return ctest.ExecutionDescriptor{
		LogicalName: name,
		Compatibility: ctest.Compatibility{
			CaseLevel: true,
		},
	}
}
