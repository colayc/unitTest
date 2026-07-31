package testdiscovery

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"unit-test-ide.local/test-service/internal/ctest"
	"unit-test-ide.local/test-service/internal/testdomain"
	"unit-test-ide.local/test-service/internal/testframework"
)

const maxCatalogItems = 100_000

var (
	ErrInvalidBuild       = errors.New("invalid test catalog build input")
	ErrCatalogItemLimit   = errors.New("test catalog item limit exceeded")
	errDuplicateDiscovery = errors.New("duplicate discovered test identity")
	errMalformedDiscovery = errors.New("malformed framework discovery")
)

type ContainerInput struct {
	Descriptor     ctest.ExecutionDescriptor
	Selection      testframework.Selection
	DisplayName    string
	SourceLocation *testdomain.SourceLocation
}

type BuildInput struct {
	ProjectID   string
	ProfileID   string
	GeneratedAt time.Time
	Fingerprint Fingerprint
	Containers  []ContainerInput
}

type Builder struct {
	maxItems int
}

func NewBuilder() *Builder {
	return &Builder{maxItems: maxCatalogItems}
}

func (builder *Builder) Build(
	ctx context.Context,
	input BuildInput,
) (testdomain.Catalog, error) {
	if ctx == nil || input.ProjectID == "" || input.ProfileID == "" ||
		input.GeneratedAt.IsZero() || len(input.Containers) > 10_000 {
		return testdomain.Catalog{}, ErrInvalidBuild
	}
	if input.Fingerprint.BuildProfileIdentity != input.ProfileID {
		return testdomain.Catalog{}, ErrInvalidFingerprint
	}
	containers := append([]ContainerInput(nil), input.Containers...)
	sort.Slice(containers, func(first, second int) bool {
		return containers[first].Descriptor.LogicalName <
			containers[second].Descriptor.LogicalName
	})
	for index, container := range containers {
		if container.Descriptor.LogicalName == "" ||
			index > 0 &&
				container.Descriptor.LogicalName == containers[index-1].Descriptor.LogicalName {
			return testdomain.Catalog{}, ErrInvalidBuild
		}
	}

	fingerprint := cloneFingerprint(input.Fingerprint)
	fingerprint.AdapterContracts = adapterContracts(containers)
	revision, err := CatalogRevision(fingerprint)
	if err != nil {
		return testdomain.Catalog{}, err
	}

	result := testdomain.Catalog{
		ProjectID:   input.ProjectID,
		ProfileID:   input.ProfileID,
		Revision:    revision,
		GeneratedAt: input.GeneratedAt.UTC(),
		Containers:  make([]testdomain.Container, 0, len(containers)),
		Items:       []testdomain.Item{},
		Diagnostics: []testdomain.Diagnostic{},
	}
	limit := builder.itemLimit()
	for _, candidate := range containers {
		if err := ctx.Err(); err != nil {
			return testdomain.Catalog{}, err
		}
		container, items, diagnostics, err := buildOneContainer(
			ctx,
			result,
			candidate,
			limit-len(result.Items),
		)
		if err != nil {
			return testdomain.Catalog{}, err
		}
		if len(result.Items)+len(items) > limit {
			return testdomain.Catalog{}, ErrCatalogItemLimit
		}
		result.Containers = append(result.Containers, container)
		result.Items = append(result.Items, items...)
		result.Diagnostics = append(result.Diagnostics, diagnostics...)
	}
	sortCatalog(&result)
	return testdomain.NewCatalog(result)
}

func (builder *Builder) itemLimit() int {
	if builder == nil || builder.maxItems <= 0 || builder.maxItems > maxCatalogItems {
		return maxCatalogItems
	}
	return builder.maxItems
}

func buildOneContainer(
	ctx context.Context,
	catalog testdomain.Catalog,
	input ContainerInput,
	remainingItems int,
) (
	testdomain.Container,
	[]testdomain.Item,
	[]testdomain.Diagnostic,
	error,
) {
	containerID, err := testdomain.ContainerID(catalog.ProjectID, input.Descriptor.LogicalName)
	if err != nil {
		return testdomain.Container{}, nil, nil, ErrInvalidBuild
	}
	displayName := input.DisplayName
	if displayName == "" {
		displayName = input.Descriptor.LogicalName
	}
	container := testdomain.Container{
		ID:               containerID,
		ProjectID:        catalog.ProjectID,
		CTestLogicalName: input.Descriptor.LogicalName,
		DisplayName:      displayName,
		Framework:        input.Selection.Framework,
		Capabilities:     domainCapabilities(input.Selection.Capabilities),
		Labels:           append([]string(nil), input.Descriptor.Labels...),
		SourceLocation:   cloneLocation(input.SourceLocation),
		Disabled:         input.Descriptor.Disabled,
		DegradedReason:   input.Selection.DegradedReason,
	}
	if _, err := testdomain.NewContainer(container); err != nil {
		return testdomain.Container{}, nil, nil, ErrInvalidBuild
	}
	if input.Selection.Framework == testdomain.FrameworkOpaqueCTest {
		if container.DegradedReason == "" {
			container.DegradedReason = testframework.DegradedNoMetadata
		}
		container.Capabilities = testdomain.Capabilities{}
		validated, err := validateContainerResult(catalog, container, nil, nil)
		if err != nil {
			return testdomain.Container{}, nil, nil, ErrInvalidBuild
		}
		return validated.Containers[0], validated.Items, validated.Diagnostics, nil
	}
	if !selectableFramework(input.Selection.Framework) ||
		input.Selection.Adapter == nil ||
		input.Selection.Adapter.Framework() != input.Selection.Framework ||
		input.Selection.Adapter.ContractVersion() == "" ||
		!input.Selection.Capabilities.CanRunContainer ||
		!input.Selection.Capabilities.CanDiscoverCases {
		return testdomain.Container{}, nil, nil, ErrInvalidBuild
	}

	discovery, discoverErr := input.Selection.Adapter.Discover(
		ctx,
		cloneDescriptor(input.Descriptor),
	)
	if err := ctx.Err(); err != nil {
		return testdomain.Container{}, nil, nil, err
	}
	if discoverErr != nil {
		return degradedContainer(catalog, container, DegradedDiscoveryFailed)
	}
	if discovery.Partial {
		return degradedContainer(catalog, container, DegradedDiscoveryPartial)
	}
	if len(discovery.Items) > remainingItems {
		return testdomain.Container{}, nil, nil, ErrCatalogItemLimit
	}
	items, err := buildDiscoveredItems(catalog.ProjectID, container, discovery.Items)
	if err != nil {
		reason := DegradedDiscoveryMalformed
		if errors.Is(err, errDuplicateDiscovery) {
			reason = DegradedDuplicateIdentity
		}
		return degradedContainer(catalog, container, reason)
	}
	validated, err := validateContainerResult(
		catalog,
		container,
		items,
		discovery.Diagnostics,
	)
	if err != nil {
		return degradedContainer(catalog, container, DegradedDiscoveryMalformed)
	}
	return validated.Containers[0], validated.Items, validated.Diagnostics, nil
}

func degradedContainer(
	catalog testdomain.Catalog,
	container testdomain.Container,
	reason string,
) (
	testdomain.Container,
	[]testdomain.Item,
	[]testdomain.Diagnostic,
	error,
) {
	container.Framework = testdomain.FrameworkOpaqueCTest
	container.Capabilities = testdomain.Capabilities{}
	container.DegradedReason = reason
	validated, err := validateContainerResult(
		catalog,
		container,
		nil,
		[]testdomain.Diagnostic{
			degradationDiagnostic(container.CTestLogicalName, reason),
		},
	)
	if err != nil {
		return testdomain.Container{}, nil, nil, err
	}
	return validated.Containers[0], validated.Items, validated.Diagnostics, nil
}

func validateContainerResult(
	catalog testdomain.Catalog,
	container testdomain.Container,
	items []testdomain.Item,
	diagnostics []testdomain.Diagnostic,
) (testdomain.Catalog, error) {
	return testdomain.NewCatalog(testdomain.Catalog{
		ProjectID:   catalog.ProjectID,
		ProfileID:   catalog.ProfileID,
		Revision:    catalog.Revision,
		GeneratedAt: catalog.GeneratedAt,
		Containers:  []testdomain.Container{container},
		Items:       append([]testdomain.Item(nil), items...),
		Diagnostics: append([]testdomain.Diagnostic(nil), diagnostics...),
	})
}

type discoveredNode struct {
	item testdomain.Item
	key  string
}

type suiteContext struct {
	id    testdomain.ID
	group string
}

func buildDiscoveredItems(
	projectID string,
	container testdomain.Container,
	discovered []testframework.DiscoveredItem,
) ([]testdomain.Item, error) {
	candidates := append([]testframework.DiscoveredItem(nil), discovered...)
	sort.SliceStable(candidates, func(first, second int) bool {
		firstRank := itemKindRank(candidates[first].Kind)
		secondRank := itemKindRank(candidates[second].Kind)
		if firstRank != secondRank {
			return firstRank < secondRank
		}
		if candidates[first].ParentLogicalName != candidates[second].ParentLogicalName {
			return candidates[first].ParentLogicalName < candidates[second].ParentLogicalName
		}
		if candidates[first].LogicalName != candidates[second].LogicalName {
			return candidates[first].LogicalName < candidates[second].LogicalName
		}
		return parameterKey(candidates[first].Parameters) <
			parameterKey(candidates[second].Parameters)
	})

	groups := make(map[string]testdomain.ID)
	suites := make(map[string][]suiteContext)
	seen := make(map[testdomain.ID]struct{}, len(candidates))
	nodes := make([]discoveredNode, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate.LogicalName == "" || !candidate.Kind.Valid() {
			return nil, errMalformedDiscovery
		}
		displayName := candidate.DisplayName
		if displayName == "" {
			displayName = candidate.LogicalName
		}
		var (
			id       testdomain.ID
			parentID testdomain.ID
			group    string
			suite    string
			err      error
		)
		switch candidate.Kind {
		case testdomain.ItemGroup:
			if candidate.ParentKind != "" || candidate.ParentLogicalName != "" {
				return nil, errMalformedDiscovery
			}
			group = candidate.LogicalName
			id, _ = testdomain.GroupID(
				projectID,
				container.CTestLogicalName,
				container.Framework,
				group,
			)
			groups[group] = id
		case testdomain.ItemSuite:
			if candidate.ParentKind != testdomain.ItemGroup {
				return nil, errMalformedDiscovery
			}
			group = candidate.ParentLogicalName
			var exists bool
			parentID, exists = groups[group]
			if !exists {
				return nil, errMalformedDiscovery
			}
			suite = candidate.LogicalName
			id, _ = testdomain.SuiteID(
				projectID,
				container.CTestLogicalName,
				container.Framework,
				group,
				suite,
			)
			suites[suite] = append(suites[suite], suiteContext{id: id, group: group})
		case testdomain.ItemCase:
			parentID, group, suite, err = resolveCaseParent(
				candidate.ParentKind,
				candidate.ParentLogicalName,
				groups,
				suites,
			)
			if err != nil {
				return nil, err
			}
			id, err = testdomain.CaseID(testdomain.CaseIdentity{
				ProjectID: projectID,
				CTestName: container.CTestLogicalName,
				Framework: container.Framework,
				Group:     group,
				Suite:     suite,
				Name:      candidate.LogicalName,
				Parameters: append(
					[]testdomain.Parameter(nil),
					candidate.Parameters...,
				),
			})
			if err != nil {
				return nil, errMalformedDiscovery
			}
		}
		if id == "" {
			return nil, errMalformedDiscovery
		}
		if _, duplicate := seen[id]; duplicate {
			return nil, errDuplicateDiscovery
		}
		seen[id] = struct{}{}
		item, err := testdomain.NewItem(testdomain.Item{
			ID:             id,
			ContainerID:    container.ID,
			ParentID:       parentID,
			Kind:           candidate.Kind,
			Framework:      container.Framework,
			LogicalName:    candidate.LogicalName,
			DisplayName:    displayName,
			Labels:         append([]string(nil), candidate.Labels...),
			SourceLocation: cloneLocation(candidate.SourceLocation),
			Disabled:       candidate.Disabled,
			Parameters:     append([]testdomain.Parameter(nil), candidate.Parameters...),
		})
		if err != nil {
			return nil, errMalformedDiscovery
		}
		nodes = append(nodes, discoveredNode{
			item: item,
			key: fmt.Sprintf(
				"%d\x00%s\x00%s\x00%s\x00%s",
				itemKindRank(candidate.Kind),
				group,
				suite,
				candidate.LogicalName,
				parameterKey(candidate.Parameters),
			),
		})
	}
	sort.Slice(nodes, func(first, second int) bool {
		return nodes[first].key < nodes[second].key
	})
	result := make([]testdomain.Item, len(nodes))
	for index, node := range nodes {
		result[index] = node.item
	}
	if result == nil {
		return []testdomain.Item{}, nil
	}
	return result, nil
}

func resolveCaseParent(
	parentKind testdomain.ItemKind,
	parent string,
	groups map[string]testdomain.ID,
	suites map[string][]suiteContext,
) (testdomain.ID, string, string, error) {
	if parent == "" {
		if parentKind != "" {
			return "", "", "", errMalformedDiscovery
		}
		return "", "", "", nil
	}
	switch parentKind {
	case testdomain.ItemSuite:
		suiteMatches := suites[parent]
		if len(suiteMatches) != 1 {
			return "", "", "", errMalformedDiscovery
		}
		return suiteMatches[0].id, suiteMatches[0].group, parent, nil
	case testdomain.ItemGroup:
		groupID, exists := groups[parent]
		if !exists {
			return "", "", "", errMalformedDiscovery
		}
		return groupID, parent, "", nil
	default:
		return "", "", "", errMalformedDiscovery
	}
}

func itemKindRank(kind testdomain.ItemKind) int {
	switch kind {
	case testdomain.ItemGroup:
		return 0
	case testdomain.ItemSuite:
		return 1
	case testdomain.ItemCase:
		return 2
	default:
		return 3
	}
}

func parameterKey(parameters []testdomain.Parameter) string {
	canonical := append([]testdomain.Parameter(nil), parameters...)
	sort.Slice(canonical, func(first, second int) bool {
		if canonical[first].Name != canonical[second].Name {
			return canonical[first].Name < canonical[second].Name
		}
		return canonical[first].Value < canonical[second].Value
	})
	encoded, _ := json.Marshal(canonical)
	return string(encoded)
}

func adapterContracts(containers []ContainerInput) []AdapterContract {
	result := make([]AdapterContract, 0, len(containers))
	for _, container := range containers {
		if container.Selection.Adapter == nil ||
			!selectableFramework(container.Selection.Framework) {
			continue
		}
		result = append(result, AdapterContract{
			CTestName: container.Descriptor.LogicalName,
			Framework: container.Selection.Framework,
			Version:   container.Selection.Adapter.ContractVersion(),
		})
	}
	return result
}

func domainCapabilities(value testframework.Capabilities) testdomain.Capabilities {
	return testdomain.Capabilities{
		CanDiscoverCases:        value.CanDiscoverCases,
		CanRunCase:              value.CanRunCase,
		CanReportSkipped:        value.CanReportSkipped,
		CanReportSourceLocation: value.CanReportSourceLocation,
		CanReportMockDetails:    value.CanReportMockDetails,
	}
}

func cloneDescriptor(value ctest.ExecutionDescriptor) ctest.ExecutionDescriptor {
	value.Arguments = append([]string(nil), value.Arguments...)
	value.Environment = append([]ctest.EnvironmentEntry(nil), value.Environment...)
	value.EnvironmentChanges = append(
		[]ctest.EnvironmentModification(nil),
		value.EnvironmentChanges...,
	)
	value.Labels = append([]string(nil), value.Labels...)
	value.Compatibility.Reasons = append(
		[]ctest.Reason(nil),
		value.Compatibility.Reasons...,
	)
	if value.TimeoutSeconds != nil {
		timeout := *value.TimeoutSeconds
		value.TimeoutSeconds = &timeout
	}
	if value.SkipReturnCode != nil {
		code := *value.SkipReturnCode
		value.SkipReturnCode = &code
	}
	return value
}

func cloneLocation(value *testdomain.SourceLocation) *testdomain.SourceLocation {
	if value == nil {
		return nil
	}
	result := *value
	return &result
}

func sortCatalog(catalog *testdomain.Catalog) {
	sort.Slice(catalog.Containers, func(first, second int) bool {
		return catalog.Containers[first].CTestLogicalName <
			catalog.Containers[second].CTestLogicalName
	})
	sort.Slice(catalog.Diagnostics, func(first, second int) bool {
		if catalog.Diagnostics[first].Code != catalog.Diagnostics[second].Code {
			return catalog.Diagnostics[first].Code < catalog.Diagnostics[second].Code
		}
		return catalog.Diagnostics[first].Message < catalog.Diagnostics[second].Message
	})
}
