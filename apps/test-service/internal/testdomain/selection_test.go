package testdomain

import (
	"errors"
	"reflect"
	"sort"
	"strings"
	"testing"
)

func TestResolveSelectionIsDeterministicAndExcludesDisabledItems(t *testing.T) {
	catalog, ids := selectionCatalog(t, true)
	first, err := ResolveSelection(catalog, Selection{Mode: SelectionAll}, Limits{MaxSelectionSize: 100})
	if err != nil {
		t.Fatal(err)
	}
	reordered, _ := selectionCatalog(t, false)
	second, err := ResolveSelection(reordered, Selection{Mode: SelectionAll}, Limits{MaxSelectionSize: 100})
	if err != nil {
		t.Fatal(err)
	}
	wantItems := []ID{ids.caseA, ids.caseUnicode}
	sort.Slice(wantItems, func(i, j int) bool { return wantItems[i] < wantItems[j] })
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("catalog input order changed snapshot:\nfirst=%#v\nsecond=%#v", first, second)
	}
	if !reflect.DeepEqual(first.ItemIDs, wantItems) {
		t.Fatalf("all item IDs = %v, want %v", first.ItemIDs, wantItems)
	}
	if !reflect.DeepEqual(first.ContainerIDs, []ID{ids.opaqueContainer}) {
		t.Fatalf("all container IDs = %v", first.ContainerIDs)
	}
	for _, id := range first.ItemIDs {
		if id == ids.disabledCase {
			t.Fatal("disabled case entered all selection")
		}
	}
}

func TestResolveSelectionExpandsGroupsAndIgnoresInputOrder(t *testing.T) {
	catalog, ids := selectionCatalog(t, false)
	group, err := ResolveSelection(catalog, Selection{
		Mode:    SelectionItems,
		ItemIDs: []ID{ids.group},
	}, Limits{MaxSelectionSize: 100})
	if err != nil {
		t.Fatal(err)
	}
	explicit, err := ResolveSelection(catalog, Selection{
		Mode:    SelectionItems,
		ItemIDs: []ID{ids.caseUnicode, ids.disabledCase, ids.caseA},
	}, Limits{MaxSelectionSize: 100})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(group.ItemIDs, explicit.ItemIDs) {
		t.Fatalf("group expansion = %v, explicit = %v", group.ItemIDs, explicit.ItemIDs)
	}
}

func TestResolveSelectionExplicitItemsCanIncludeDisabledCases(t *testing.T) {
	catalog, ids := selectionCatalog(t, false)
	snapshot, err := ResolveSelection(catalog, Selection{
		Mode:    SelectionItems,
		ItemIDs: []ID{ids.disabledCase},
	}, Limits{MaxSelectionSize: 100})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(snapshot.ItemIDs, []ID{ids.disabledCase}) {
		t.Fatalf("explicit disabled selection = %v", snapshot.ItemIDs)
	}
}

func TestResolveSelectionFilterUsesExactFieldsAndUnicodeCaseFold(t *testing.T) {
	catalog, ids := selectionCatalog(t, false)
	fast, err := ResolveSelection(catalog, Selection{
		Mode:   SelectionFilter,
		Filter: Filter{Label: "fast"},
	}, Limits{MaxSelectionSize: 100})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(fast.ItemIDs, []ID{ids.caseA}) {
		t.Fatalf("fast filter = %v", fast.ItemIDs)
	}
	unicode, err := ResolveSelection(catalog, Selection{
		Mode:   SelectionFilter,
		Filter: Filter{NameContains: "STRASSE"},
	}, Limits{MaxSelectionSize: 100})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(unicode.ItemIDs, []ID{ids.caseUnicode}) {
		t.Fatalf("Unicode case-fold filter = %v", unicode.ItemIDs)
	}
	includedGroup, err := ResolveSelection(catalog, Selection{
		Mode:   SelectionFilter,
		Filter: Filter{IncludeItemIDs: []ID{ids.group}},
	}, Limits{MaxSelectionSize: 100})
	if err != nil {
		t.Fatal(err)
	}
	wantItems := []ID{ids.caseA, ids.caseUnicode}
	sort.Slice(wantItems, func(i, j int) bool { return wantItems[i] < wantItems[j] })
	if !reflect.DeepEqual(includedGroup.ItemIDs, wantItems) {
		t.Fatalf("included group filter = %v, want %v", includedGroup.ItemIDs, wantItems)
	}
}

func TestResolveSelectionRejectsEmptyOversizedAndUnknownSelections(t *testing.T) {
	catalog, ids := selectionCatalog(t, false)
	if _, err := ResolveSelection(catalog, Selection{
		Mode:   SelectionFilter,
		Filter: Filter{Label: "missing"},
	}, Limits{MaxSelectionSize: 100}); !errors.Is(err, ErrEmptySelection) {
		t.Fatalf("empty filter error = %v", err)
	}
	if _, err := ResolveSelection(catalog, Selection{
		Mode:    SelectionItems,
		ItemIDs: []ID{ids.group},
	}, Limits{MaxSelectionSize: 1}); !errors.Is(err, ErrSelectionTooLarge) {
		t.Fatalf("oversized error = %v", err)
	}
	if _, err := ResolveSelection(catalog, Selection{
		Mode:    SelectionItems,
		ItemIDs: []ID{ID("utid-v1-" + strings.Repeat("f", 64))},
	}, Limits{MaxSelectionSize: 100}); !errors.Is(err, ErrUnknownSelectionID) {
		t.Fatalf("unknown item error = %v", err)
	}
	if _, err := ResolveSelection(catalog, Selection{
		Mode:  SelectionFailedFromRun,
		RunID: "11111111111111111111111111111111",
	}, Limits{MaxSelectionSize: 100}); !errors.Is(err, ErrFailedRunResolverRequired) {
		t.Fatalf("failedFromRun error = %v", err)
	}
}

func TestNewSelectionDefensivelyCopiesAndRejectsDuplicateIDs(t *testing.T) {
	_, ids := selectionCatalog(t, false)
	itemsInput := Selection{
		Mode:    SelectionItems,
		ItemIDs: []ID{ids.caseA},
	}
	itemsSelection, err := NewSelection(itemsInput)
	if err != nil {
		t.Fatal(err)
	}
	itemsInput.ItemIDs[0] = ids.disabledCase
	if itemsSelection.ItemIDs[0] != ids.caseA {
		t.Fatal("NewSelection retained caller-owned item IDs")
	}

	filterInput := Selection{
		Mode: SelectionFilter,
		Filter: Filter{
			IncludeItemIDs: []ID{ids.caseUnicode},
		},
	}
	filterSelection, err := NewSelection(filterInput)
	if err != nil {
		t.Fatal(err)
	}
	filterInput.Filter.IncludeItemIDs[0] = ids.disabledCase
	if filterSelection.Filter.IncludeItemIDs[0] != ids.caseUnicode {
		t.Fatal("NewSelection retained caller-owned filter IDs")
	}
	if _, err := NewSelection(Selection{
		Mode:    SelectionItems,
		ItemIDs: []ID{ids.caseA, ids.caseA},
	}); !errors.Is(err, ErrDuplicateIdentity) {
		t.Fatalf("duplicate selection error = %v", err)
	}
}

type selectionIDs struct {
	opaqueContainer ID
	group           ID
	caseA           ID
	caseUnicode     ID
	disabledCase    ID
}

func selectionCatalog(t *testing.T, reverse bool) (Catalog, selectionIDs) {
	t.Helper()
	base := validCatalogInput(t)
	base.Diagnostics = nil
	primary := base.Containers[0]
	primary.Labels = []string{"native"}
	opaqueID := mustContainerID(t, "core", "opaque.tests")
	opaque := Container{
		ID:               opaqueID,
		ProjectID:        "core",
		CTestLogicalName: "opaque.tests",
		DisplayName:      "Opaque Tests",
		Framework:        FrameworkOpaqueCTest,
		Labels:           []string{"opaque"},
	}
	groupID, err := GroupID("core", "core.tests", FrameworkCppUTest, "Math")
	if err != nil {
		t.Fatal(err)
	}
	suiteID, err := SuiteID("core", "core.tests", FrameworkCppUTest, "Math", "Arithmetic")
	if err != nil {
		t.Fatal(err)
	}
	caseID := func(name string) ID {
		id, err := CaseID(CaseIdentity{
			ProjectID: "core",
			CTestName: "core.tests",
			Framework: FrameworkCppUTest,
			Group:     "Math",
			Suite:     "Arithmetic",
			Name:      name,
		})
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	ids := selectionIDs{
		opaqueContainer: opaqueID,
		group:           groupID,
		caseA:           caseID("adds"),
		caseUnicode:     caseID("Straße"),
		disabledCase:    caseID("disabled"),
	}
	base.Containers = []Container{primary, opaque}
	base.Items = []Item{
		{
			ID: groupID, ContainerID: primary.ID, Kind: ItemGroup,
			Framework: FrameworkCppUTest, LogicalName: "Math", DisplayName: "Math",
		},
		{
			ID: suiteID, ContainerID: primary.ID, ParentID: groupID, Kind: ItemSuite,
			Framework: FrameworkCppUTest, LogicalName: "Arithmetic", DisplayName: "Arithmetic",
		},
		{
			ID: ids.caseA, ContainerID: primary.ID, ParentID: suiteID, Kind: ItemCase,
			Framework: FrameworkCppUTest, LogicalName: "adds", DisplayName: "adds", Labels: []string{"fast"},
		},
		{
			ID: ids.caseUnicode, ContainerID: primary.ID, ParentID: suiteID, Kind: ItemCase,
			Framework: FrameworkCppUTest, LogicalName: "Straße", DisplayName: "Straße", Labels: []string{"unicode"},
		},
		{
			ID: ids.disabledCase, ContainerID: primary.ID, ParentID: suiteID, Kind: ItemCase,
			Framework: FrameworkCppUTest, LogicalName: "disabled", DisplayName: "disabled", Disabled: true,
		},
	}
	if reverse {
		for left, right := 0, len(base.Containers)-1; left < right; left, right = left+1, right-1 {
			base.Containers[left], base.Containers[right] = base.Containers[right], base.Containers[left]
		}
		for left, right := 0, len(base.Items)-1; left < right; left, right = left+1, right-1 {
			base.Items[left], base.Items[right] = base.Items[right], base.Items[left]
		}
	}
	catalog, err := NewCatalog(base)
	if err != nil {
		t.Fatal(err)
	}
	return catalog, ids
}
