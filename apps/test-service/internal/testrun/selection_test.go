package testrun

import (
	"context"
	"errors"
	"reflect"
	"sort"
	"strings"
	"testing"

	"unit-test-ide.local/test-service/internal/testdomain"
)

func TestResolveStructuredSelectionDelegatesWithoutReadingHistory(t *testing.T) {
	catalog, ids := resolverCatalog()
	reader := &fakeRunReader{}
	snapshot, err := Resolve(
		context.Background(),
		catalog,
		testdomain.Selection{Mode: testdomain.SelectionAll},
		reader,
		testdomain.Limits{MaxSelectionSize: 100},
	)
	if err != nil {
		t.Fatal(err)
	}
	want := []testdomain.ID{ids.caseA, ids.caseUnicode}
	sort.Slice(want, func(left, right int) bool { return want[left] < want[right] })
	if !reflect.DeepEqual(snapshot.ItemIDs, want) ||
		!reflect.DeepEqual(snapshot.ContainerIDs, []testdomain.ID{ids.opaque}) ||
		reader.calls != 0 {
		t.Fatalf("Resolve(all) = %#v, reader calls = %d", snapshot, reader.calls)
	}

	group, err := Resolve(
		context.Background(),
		catalog,
		testdomain.Selection{
			Mode:    testdomain.SelectionItems,
			ItemIDs: []testdomain.ID{ids.group},
		},
		reader,
		testdomain.Limits{MaxSelectionSize: 100},
	)
	wantGroup := append(append([]testdomain.ID{}, want...), ids.disabled)
	sort.Slice(wantGroup, func(left, right int) bool {
		return wantGroup[left] < wantGroup[right]
	})
	if err != nil || !reflect.DeepEqual(group.ItemIDs, wantGroup) {
		t.Fatalf("Resolve(group) = %#v, %v", group, err)
	}
	unicode, err := Resolve(
		context.Background(),
		catalog,
		testdomain.Selection{
			Mode: testdomain.SelectionFilter,
			Filter: testdomain.Filter{
				NameContains: "STRASSE",
			},
		},
		reader,
		testdomain.Limits{MaxSelectionSize: 100},
	)
	if err != nil ||
		!reflect.DeepEqual(unicode.ItemIDs, []testdomain.ID{ids.caseUnicode}) {
		t.Fatalf("Resolve(Unicode filter) = %#v, %v", unicode, err)
	}

	container, err := Resolve(
		context.Background(),
		catalog,
		testdomain.Selection{
			Mode:         testdomain.SelectionContainers,
			ContainerIDs: []testdomain.ID{ids.primary},
		},
		reader,
		testdomain.Limits{MaxSelectionSize: 100},
	)
	if err != nil ||
		!reflect.DeepEqual(
			container.ContainerIDs,
			[]testdomain.ID{ids.primary},
		) {
		t.Fatalf("Resolve(container) = %#v, %v", container, err)
	}
	exactCase, err := Resolve(
		context.Background(),
		catalog,
		testdomain.Selection{
			Mode:    testdomain.SelectionItems,
			ItemIDs: []testdomain.ID{ids.caseA},
		},
		reader,
		testdomain.Limits{MaxSelectionSize: 100},
	)
	if err != nil ||
		!reflect.DeepEqual(exactCase.ItemIDs, []testdomain.ID{ids.caseA}) {
		t.Fatalf("Resolve(case) = %#v, %v", exactCase, err)
	}
	filtered, err := Resolve(
		context.Background(),
		catalog,
		testdomain.Selection{
			Mode: testdomain.SelectionFilter,
			Filter: testdomain.Filter{
				Group:          "Math",
				Suite:          "Arithmetic",
				IncludeItemIDs: []testdomain.ID{ids.group},
				ExcludeItemIDs: []testdomain.ID{ids.caseA},
			},
		},
		reader,
		testdomain.Limits{MaxSelectionSize: 100},
	)
	if err != nil ||
		!reflect.DeepEqual(filtered.ItemIDs, []testdomain.ID{ids.caseUnicode}) {
		t.Fatalf("Resolve(exact include/exclude filter) = %#v, %v", filtered, err)
	}
}

func TestResolveRejectsUnknownEmptyOversizedAndNilContext(t *testing.T) {
	catalog, ids := resolverCatalog()
	tests := []struct {
		name    string
		ctx     context.Context
		request testdomain.Selection
		limit   int
		want    error
	}{
		{
			name: "unknown exact ID",
			ctx:  context.Background(),
			request: testdomain.Selection{
				Mode: testdomain.SelectionItems,
				ItemIDs: []testdomain.ID{
					stableTestID("f"),
				},
			},
			limit: 100,
			want:  testdomain.ErrUnknownSelectionID,
		},
		{
			name: "empty filter",
			ctx:  context.Background(),
			request: testdomain.Selection{
				Mode: testdomain.SelectionFilter,
				Filter: testdomain.Filter{
					Label: "missing",
				},
			},
			limit: 100,
			want:  testdomain.ErrEmptySelection,
		},
		{
			name: "oversized group",
			ctx:  context.Background(),
			request: testdomain.Selection{
				Mode:    testdomain.SelectionItems,
				ItemIDs: []testdomain.ID{ids.group},
			},
			limit: 1,
			want:  testdomain.ErrSelectionTooLarge,
		},
		{
			name: "nil context",
			request: testdomain.Selection{
				Mode: testdomain.SelectionAll,
			},
			limit: 100,
			want:  testdomain.ErrInvalidSelection,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := Resolve(
				test.ctx,
				catalog,
				test.request,
				nil,
				testdomain.Limits{MaxSelectionSize: test.limit},
			); !errors.Is(err, test.want) {
				t.Fatalf("Resolve() error = %v, want %v", err, test.want)
			}
		})
	}
}

type resolverIDs struct {
	primary, opaque, group, suite testdomain.ID
	caseA, caseUnicode, disabled  testdomain.ID
}

func resolverCatalog() (testdomain.Catalog, resolverIDs) {
	ids := resolverIDs{
		primary:     stableTestID("a"),
		opaque:      stableTestID("b"),
		group:       stableTestID("c"),
		suite:       stableTestID("d"),
		caseA:       stableTestID("1"),
		caseUnicode: stableTestID("2"),
		disabled:    stableTestID("3"),
	}
	return testdomain.Catalog{
		ProjectID: "core", ProfileID: strings.Repeat("4", 64),
		Revision: strings.Repeat("5", 64),
		Containers: []testdomain.Container{
			{
				ID: ids.primary, ProjectID: "core",
				CTestLogicalName: "core.tests",
				Framework:        testdomain.FrameworkCppUTest,
			},
			{
				ID: ids.opaque, ProjectID: "core",
				CTestLogicalName: "opaque.tests",
				Framework:        testdomain.FrameworkOpaqueCTest,
			},
		},
		Items: []testdomain.Item{
			{
				ID: ids.group, ContainerID: ids.primary,
				Kind: testdomain.ItemGroup, Framework: testdomain.FrameworkCppUTest,
				LogicalName: "Math", DisplayName: "Math",
			},
			{
				ID: ids.suite, ContainerID: ids.primary, ParentID: ids.group,
				Kind: testdomain.ItemSuite, Framework: testdomain.FrameworkCppUTest,
				LogicalName: "Arithmetic", DisplayName: "Arithmetic",
			},
			{
				ID: ids.caseA, ContainerID: ids.primary, ParentID: ids.suite,
				Kind: testdomain.ItemCase, Framework: testdomain.FrameworkCppUTest,
				LogicalName: "adds", DisplayName: "adds", Labels: []string{"fast"},
			},
			{
				ID: ids.caseUnicode, ContainerID: ids.primary, ParentID: ids.suite,
				Kind: testdomain.ItemCase, Framework: testdomain.FrameworkCppUTest,
				LogicalName: "Straße", DisplayName: "Straße",
			},
			{
				ID: ids.disabled, ContainerID: ids.primary, ParentID: ids.suite,
				Kind: testdomain.ItemCase, Framework: testdomain.FrameworkCppUTest,
				LogicalName: "disabled", DisplayName: "disabled", Disabled: true,
			},
		},
	}, ids
}

func stableTestID(character string) testdomain.ID {
	return testdomain.ID("utid-v1-" + strings.Repeat(character, 64))
}
