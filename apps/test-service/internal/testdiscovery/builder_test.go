package testdiscovery

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"unit-test-ide.local/test-service/internal/ctest"
	"unit-test-ide.local/test-service/internal/testdomain"
	"unit-test-ide.local/test-service/internal/testframework"
)

func TestBuilderCreatesDeterministicTreeAndStableIDs(t *testing.T) {
	adapter := &discoveryAdapter{
		framework: testdomain.FrameworkUnity,
		version:   "unity.v1",
		results: map[string]testframework.DiscoveryResult{
			"math.tests": {
				Items: []testframework.DiscoveredItem{
					{Kind: testdomain.ItemCase, ParentKind: testdomain.ItemSuite, ParentLogicalName: "vector", LogicalName: "subtracts", DisplayName: "subtracts"},
					{Kind: testdomain.ItemGroup, LogicalName: "math", DisplayName: "math"},
					{Kind: testdomain.ItemCase, ParentKind: testdomain.ItemSuite, ParentLogicalName: "vector", LogicalName: "adds", DisplayName: "adds"},
					{Kind: testdomain.ItemSuite, ParentKind: testdomain.ItemGroup, ParentLogicalName: "math", LogicalName: "vector", DisplayName: "vector"},
				},
			},
		},
	}
	first := mustBuildCatalog(t, BuildInput{
		ProjectID:   "core",
		ProfileID:   strings.Repeat("1", 64),
		GeneratedAt: fixedGeneratedAt,
		Fingerprint: fingerprintFixture(strings.Repeat("1", 64)),
		Containers: []ContainerInput{{
			Descriptor: compatibleDiscoveryDescriptor("math.tests"),
			Selection:  frameworkSelection(adapter),
		}},
	})
	adapter.results["math.tests"] = testframework.DiscoveryResult{
		Items: []testframework.DiscoveredItem{
			{Kind: testdomain.ItemGroup, LogicalName: "math", DisplayName: "math"},
			{Kind: testdomain.ItemSuite, ParentKind: testdomain.ItemGroup, ParentLogicalName: "math", LogicalName: "vector", DisplayName: "vector"},
			{Kind: testdomain.ItemCase, ParentKind: testdomain.ItemSuite, ParentLogicalName: "vector", LogicalName: "adds", DisplayName: "adds"},
			{Kind: testdomain.ItemCase, ParentKind: testdomain.ItemSuite, ParentLogicalName: "vector", LogicalName: "subtracts", DisplayName: "subtracts"},
		},
	}
	secondInput := BuildInput{
		ProjectID:   "core",
		ProfileID:   strings.Repeat("2", 64),
		GeneratedAt: fixedGeneratedAt.Add(time.Minute),
		Fingerprint: fingerprintFixture(strings.Repeat("2", 64)),
		Containers: []ContainerInput{{
			Descriptor: compatibleDiscoveryDescriptor("math.tests"),
			Selection:  frameworkSelection(adapter),
		}},
	}
	second := mustBuildCatalog(t, secondInput)

	if len(first.Containers) != 1 || len(first.Items) != 4 {
		t.Fatalf("catalog sizes = %d containers, %d items", len(first.Containers), len(first.Items))
	}
	container := first.Containers[0]
	if container.Framework != testdomain.FrameworkUnity ||
		!container.Capabilities.CanDiscoverCases ||
		!container.Capabilities.CanRunCase {
		t.Fatalf("container = %#v", container)
	}
	items := itemsByName(first.Items)
	group := items["group:math"]
	suite := items["suite:vector"]
	adds := items["case:adds"]
	if suite.ParentID != group.ID || adds.ParentID != suite.ID ||
		group.ContainerID != container.ID || suite.ContainerID != container.ID ||
		adds.ContainerID != container.ID {
		t.Fatalf("tree references = group %#v, suite %#v, case %#v", group, suite, adds)
	}
	if first.Revision == second.Revision {
		t.Fatal("profile-specific catalogs kept the same revision")
	}
	if first.Containers[0].ID != second.Containers[0].ID {
		t.Fatal("container ID changed across profiles")
	}
	for index := range first.Items {
		if first.Items[index].ID != second.Items[index].ID {
			t.Fatalf("item %d ID changed across profiles", index)
		}
	}

	opaque := mustBuildCatalog(t, BuildInput{
		ProjectID:   "core",
		ProfileID:   strings.Repeat("3", 64),
		GeneratedAt: fixedGeneratedAt,
		Fingerprint: fingerprintFixture(strings.Repeat("3", 64)),
		Containers: []ContainerInput{{
			Descriptor: compatibleDiscoveryDescriptor("math.tests"),
			Selection: testframework.Selection{
				Framework:      testdomain.FrameworkOpaqueCTest,
				Capabilities:   testframework.Capabilities{CanRunContainer: true},
				Source:         testframework.SourceOpaque,
				DegradedReason: testframework.DegradedNoMetadata,
			},
		}},
	})
	if opaque.Containers[0].ID != first.Containers[0].ID {
		t.Fatal("container ID changed with framework selection")
	}
}

func TestBuilderDegradesOnlyMalformedContainer(t *testing.T) {
	adapter := &discoveryAdapter{
		framework: testdomain.FrameworkCppUTest,
		version:   "cpputest.v1",
		results: map[string]testframework.DiscoveryResult{
			"good.tests": {
				Items: []testframework.DiscoveredItem{
					{Kind: testdomain.ItemGroup, LogicalName: "math", DisplayName: "math"},
					{Kind: testdomain.ItemCase, ParentKind: testdomain.ItemGroup, ParentLogicalName: "math", LogicalName: "adds", DisplayName: "adds"},
				},
			},
			"bad.tests": {
				Partial: true,
				Items: []testframework.DiscoveredItem{
					{Kind: testdomain.ItemCase, LogicalName: "uncommitted", DisplayName: "uncommitted"},
				},
			},
		},
	}
	catalog := mustBuildCatalog(t, BuildInput{
		ProjectID:   "core",
		ProfileID:   strings.Repeat("1", 64),
		GeneratedAt: fixedGeneratedAt,
		Fingerprint: fingerprintFixture(strings.Repeat("1", 64)),
		Containers: []ContainerInput{
			{Descriptor: compatibleDiscoveryDescriptor("bad.tests"), Selection: frameworkSelection(adapter)},
			{Descriptor: compatibleDiscoveryDescriptor("good.tests"), Selection: frameworkSelection(adapter)},
		},
	})
	if len(catalog.Containers) != 2 || len(catalog.Items) != 2 || len(catalog.Diagnostics) != 1 {
		t.Fatalf("catalog sizes = %#v", catalog)
	}
	containers := containersByName(catalog.Containers)
	if containers["bad.tests"].Framework != testdomain.FrameworkOpaqueCTest ||
		containers["bad.tests"].DegradedReason != DegradedDiscoveryPartial ||
		containers["bad.tests"].Capabilities.CanDiscoverCases {
		t.Fatalf("bad container = %#v", containers["bad.tests"])
	}
	if containers["good.tests"].Framework != testdomain.FrameworkCppUTest {
		t.Fatalf("good container = %#v", containers["good.tests"])
	}
	for _, item := range catalog.Items {
		if item.ContainerID != containers["good.tests"].ID {
			t.Fatalf("partial item was committed: %#v", item)
		}
	}
}

func TestBuilderUsesTaskOwnedDiscoveryWithoutAdapterProcess(
	t *testing.T,
) {
	adapter := &discoveryAdapter{
		framework: testdomain.FrameworkCppUTest,
		version:   "cpputest.v1",
	}
	discovery := testframework.DiscoveryResult{
		Items: []testframework.DiscoveredItem{
			{
				Kind:        testdomain.ItemGroup,
				LogicalName: "math", DisplayName: "math",
			},
			{
				Kind:              testdomain.ItemCase,
				ParentKind:        testdomain.ItemGroup,
				ParentLogicalName: "math",
				LogicalName:       "adds", DisplayName: "adds",
			},
		},
	}
	catalog := mustBuildCatalog(t, BuildInput{
		ProjectID:   "core",
		ProfileID:   strings.Repeat("1", 64),
		GeneratedAt: fixedGeneratedAt,
		Fingerprint: fingerprintFixture(strings.Repeat("1", 64)),
		Containers: []ContainerInput{{
			Descriptor: compatibleDiscoveryDescriptor("math.tests"),
			Selection:  frameworkSelection(adapter),
			Discovery:  &discovery,
		}},
	})
	if adapter.discoverCalls != 0 ||
		len(catalog.Items) != 2 ||
		catalog.Containers[0].Framework !=
			testdomain.FrameworkCppUTest {
		t.Fatalf(
			"task-owned catalog = %#v, adapter calls=%d",
			catalog,
			adapter.discoverCalls,
		)
	}
}

func TestBuilderDegradesDuplicateCaseIdentity(t *testing.T) {
	adapter := &discoveryAdapter{
		framework: testdomain.FrameworkCppUTest,
		version:   "cpputest.v1",
		results: map[string]testframework.DiscoveryResult{
			"duplicate.tests": {
				Items: []testframework.DiscoveredItem{
					{Kind: testdomain.ItemGroup, LogicalName: "math", DisplayName: "math"},
					{Kind: testdomain.ItemCase, ParentKind: testdomain.ItemGroup, ParentLogicalName: "math", LogicalName: "adds", DisplayName: "adds one"},
					{Kind: testdomain.ItemCase, ParentKind: testdomain.ItemGroup, ParentLogicalName: "math", LogicalName: "adds", DisplayName: "adds two"},
				},
			},
		},
	}
	catalog := mustBuildCatalog(t, BuildInput{
		ProjectID:   "core",
		ProfileID:   strings.Repeat("1", 64),
		GeneratedAt: fixedGeneratedAt,
		Fingerprint: fingerprintFixture(strings.Repeat("1", 64)),
		Containers: []ContainerInput{{
			Descriptor: compatibleDiscoveryDescriptor("duplicate.tests"),
			Selection:  frameworkSelection(adapter),
		}},
	})
	if len(catalog.Items) != 0 ||
		catalog.Containers[0].Framework != testdomain.FrameworkOpaqueCTest ||
		catalog.Containers[0].DegradedReason != DegradedDuplicateIdentity ||
		len(catalog.Diagnostics) != 1 {
		t.Fatalf("catalog = %#v", catalog)
	}
}

func TestBuilderRejectsCatalogItemLimit(t *testing.T) {
	adapter := &discoveryAdapter{
		framework: testdomain.FrameworkCppUTest,
		version:   "cpputest.v1",
		results: map[string]testframework.DiscoveryResult{
			"core.tests": {
				Items: []testframework.DiscoveredItem{
					{Kind: testdomain.ItemGroup, LogicalName: "math", DisplayName: "math"},
					{Kind: testdomain.ItemCase, ParentKind: testdomain.ItemGroup, ParentLogicalName: "math", LogicalName: "adds", DisplayName: "adds"},
				},
			},
		},
	}
	builder := &Builder{maxItems: 1}
	_, err := builder.Build(context.Background(), BuildInput{
		ProjectID:   "core",
		ProfileID:   strings.Repeat("1", 64),
		GeneratedAt: fixedGeneratedAt,
		Fingerprint: fingerprintFixture(strings.Repeat("1", 64)),
		Containers: []ContainerInput{{
			Descriptor: compatibleDiscoveryDescriptor("core.tests"),
			Selection:  frameworkSelection(adapter),
		}},
	})
	if !errors.Is(err, ErrCatalogItemLimit) {
		t.Fatalf("Build() error = %v", err)
	}
}

func mustBuildCatalog(t *testing.T, input BuildInput) testdomain.Catalog {
	t.Helper()
	catalog, err := NewBuilder().Build(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	return catalog
}

func compatibleDiscoveryDescriptor(name string) ctest.ExecutionDescriptor {
	return ctest.ExecutionDescriptor{
		LogicalName: name,
		Labels:      []string{"unit"},
		Compatibility: ctest.Compatibility{
			CaseLevel: true,
		},
	}
}

func frameworkSelection(adapter testframework.Adapter) testframework.Selection {
	return testframework.Selection{
		Framework: adapter.Framework(),
		Adapter:   adapter,
		Capabilities: testframework.Capabilities{
			CanRunContainer:         true,
			CanDiscoverCases:        true,
			CanRunCase:              true,
			CanReportSkipped:        true,
			CanReportSourceLocation: true,
		},
		Source: testframework.SourceHelper,
	}
}

func itemsByName(items []testdomain.Item) map[string]testdomain.Item {
	result := make(map[string]testdomain.Item, len(items))
	for _, item := range items {
		result[string(item.Kind)+":"+item.LogicalName] = item
	}
	return result
}

func containersByName(containers []testdomain.Container) map[string]testdomain.Container {
	result := make(map[string]testdomain.Container, len(containers))
	for _, container := range containers {
		result[container.CTestLogicalName] = container
	}
	return result
}

type discoveryAdapter struct {
	framework     testdomain.Framework
	version       string
	results       map[string]testframework.DiscoveryResult
	errs          map[string]error
	discoverCalls int
}

func (adapter *discoveryAdapter) Framework() testdomain.Framework {
	return adapter.framework
}

func (adapter *discoveryAdapter) ContractVersion() string {
	return adapter.version
}

func (*discoveryAdapter) Verify(
	context.Context,
	ctest.ExecutionDescriptor,
) (testframework.Capabilities, error) {
	return testframework.Capabilities{CanRunContainer: true, CanDiscoverCases: true}, nil
}

func (adapter *discoveryAdapter) Discover(
	_ context.Context,
	descriptor ctest.ExecutionDescriptor,
) (testframework.DiscoveryResult, error) {
	adapter.discoverCalls++
	if err := adapter.errs[descriptor.LogicalName]; err != nil {
		return testframework.DiscoveryResult{}, err
	}
	return adapter.results[descriptor.LogicalName], nil
}

func (*discoveryAdapter) PlanRun(
	context.Context,
	testframework.RunInput,
) (testframework.RunPlan, error) {
	return testframework.RunPlan{}, nil
}

func (*discoveryAdapter) NewParser(
	testframework.ParseInput,
) (testframework.ResultParser, error) {
	return &discoveryParser{}, nil
}

type discoveryParser struct{}

func (*discoveryParser) Feed(
	testframework.Stream,
	[]byte,
) ([]testframework.ResultEvent, error) {
	return nil, nil
}

func (*discoveryParser) Finish(
	testframework.ProcessResult,
) (testframework.ParseResult, error) {
	return testframework.ParseResult{}, nil
}

var fixedGeneratedAt = time.Date(2026, 7, 31, 1, 2, 3, 0, time.UTC)
