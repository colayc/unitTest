package testdomain

import (
	"sort"
	"strings"

	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
)

type SelectionMode string

const (
	SelectionAll           SelectionMode = "all"
	SelectionContainers    SelectionMode = "containers"
	SelectionItems         SelectionMode = "items"
	SelectionFilter        SelectionMode = "filter"
	SelectionFailedFromRun SelectionMode = "failedFromRun"
)

func (mode SelectionMode) Valid() bool {
	switch mode {
	case SelectionAll, SelectionContainers, SelectionItems, SelectionFilter, SelectionFailedFromRun:
		return true
	default:
		return false
	}
}

type Filter struct {
	Group          string
	Suite          string
	Label          string
	NameContains   string
	IncludeItemIDs []ID
	ExcludeItemIDs []ID
}

type Selection struct {
	Mode         SelectionMode
	ContainerIDs []ID
	ItemIDs      []ID
	Filter       Filter
	RunID        string
}

type SelectionSnapshot struct {
	Mode         SelectionMode
	ContainerIDs []ID
	ItemIDs      []ID
	SourceRunID  string
}

func (snapshot SelectionSnapshot) Clone() SelectionSnapshot {
	snapshot.ContainerIDs = append([]ID(nil), snapshot.ContainerIDs...)
	snapshot.ItemIDs = append([]ID(nil), snapshot.ItemIDs...)
	return snapshot
}

type Limits struct {
	MaxSelectionSize int
}

func NewSelection(value Selection) (Selection, error) {
	if !value.Mode.Valid() {
		return Selection{}, invalid(ErrInvalidSelection, "mode", "unsupported value")
	}
	result := value
	result.ContainerIDs = append([]ID(nil), value.ContainerIDs...)
	result.ItemIDs = append([]ID(nil), value.ItemIDs...)
	result.Filter.IncludeItemIDs = append([]ID(nil), value.Filter.IncludeItemIDs...)
	result.Filter.ExcludeItemIDs = append([]ID(nil), value.Filter.ExcludeItemIDs...)
	if err := validateIDList("containerIds", result.ContainerIDs); err != nil {
		return Selection{}, err
	}
	if err := validateIDList("itemIds", result.ItemIDs); err != nil {
		return Selection{}, err
	}
	if err := validateIDList("filter.includeItemIds", result.Filter.IncludeItemIDs); err != nil {
		return Selection{}, err
	}
	if err := validateIDList("filter.excludeItemIds", result.Filter.ExcludeItemIDs); err != nil {
		return Selection{}, err
	}
	var err error
	if result.Filter.Group, err = normalizeIdentityText(result.Filter.Group, true); err != nil {
		return Selection{}, invalid(ErrInvalidSelection, "filter.group", err.Error())
	}
	if result.Filter.Suite, err = normalizeIdentityText(result.Filter.Suite, true); err != nil {
		return Selection{}, invalid(ErrInvalidSelection, "filter.suite", err.Error())
	}
	if result.Filter.Label, err = normalizeIdentityText(result.Filter.Label, true); err != nil {
		return Selection{}, invalid(ErrInvalidSelection, "filter.label", err.Error())
	}
	if result.Filter.NameContains, err = normalizeIdentityText(result.Filter.NameContains, true); err != nil {
		return Selection{}, invalid(ErrInvalidSelection, "filter.nameContains", err.Error())
	}
	if err := validateSelectionShape(result); err != nil {
		return Selection{}, err
	}
	return result, nil
}

func ResolveSelection(catalog Catalog, selection Selection, limits Limits) (SelectionSnapshot, error) {
	selection, err := NewSelection(selection)
	if err != nil {
		return SelectionSnapshot{}, err
	}
	if limits.MaxSelectionSize < 1 || limits.MaxSelectionSize > 100_000 {
		return SelectionSnapshot{}, invalid(ErrInvalidSelection, "limits.maxSelectionSize", "must be between 1 and 100000")
	}
	if selection.Mode == SelectionFailedFromRun {
		return SelectionSnapshot{}, ErrFailedRunResolverRequired
	}
	containers := make(map[ID]Container, len(catalog.Containers))
	caseCount := make(map[ID]int, len(catalog.Containers))
	for _, container := range catalog.Containers {
		containers[container.ID] = container
	}
	items := make(map[ID]Item, len(catalog.Items))
	children := make(map[ID][]Item)
	for _, item := range catalog.Items {
		items[item.ID] = item
		if item.Kind == ItemCase {
			caseCount[item.ContainerID]++
		}
		if item.ParentID != "" {
			children[item.ParentID] = append(children[item.ParentID], item)
		}
	}
	containerIDs := make(map[ID]struct{})
	itemIDs := make(map[ID]struct{})
	switch selection.Mode {
	case SelectionAll:
		for _, container := range catalog.Containers {
			if container.Disabled {
				continue
			}
			if caseCount[container.ID] == 0 {
				containerIDs[container.ID] = struct{}{}
			}
		}
		for _, item := range catalog.Items {
			if selectableCase(item, containers) {
				itemIDs[item.ID] = struct{}{}
			}
		}
	case SelectionContainers:
		for _, id := range selection.ContainerIDs {
			_, exists := containers[id]
			if !exists {
				return SelectionSnapshot{}, invalid(ErrUnknownSelectionID, "containerIds", id.String())
			}
			containerIDs[id] = struct{}{}
		}
	case SelectionItems:
		for _, id := range selection.ItemIDs {
			item, exists := items[id]
			if !exists {
				return SelectionSnapshot{}, invalid(ErrUnknownSelectionID, "itemIds", id.String())
			}
			addItemSelection(item, children, containers, itemIDs)
		}
	case SelectionFilter:
		if err := validateFilterReferences(selection.Filter, items); err != nil {
			return SelectionSnapshot{}, err
		}
		include := idSet(selection.Filter.IncludeItemIDs)
		exclude := idSet(selection.Filter.ExcludeItemIDs)
		for _, item := range catalog.Items {
			if selectableCase(item, containers) &&
				filterMatches(item, selection.Filter, items, containers, include, exclude) {
				itemIDs[item.ID] = struct{}{}
			}
		}
		for _, container := range catalog.Containers {
			if container.Disabled || caseCount[container.ID] != 0 {
				continue
			}
			if filterMatchesContainer(container, selection.Filter) {
				containerIDs[container.ID] = struct{}{}
			}
		}
	}
	total := len(containerIDs) + len(itemIDs)
	if total == 0 {
		return SelectionSnapshot{}, ErrEmptySelection
	}
	if total > limits.MaxSelectionSize {
		return SelectionSnapshot{}, ErrSelectionTooLarge
	}
	return SelectionSnapshot{
		Mode:         selection.Mode,
		ContainerIDs: sortedIDs(containerIDs),
		ItemIDs:      sortedIDs(itemIDs),
	}, nil
}

func validateSelectionShape(selection Selection) error {
	hasFilter := selection.Filter.Group != "" || selection.Filter.Suite != "" ||
		selection.Filter.Label != "" || selection.Filter.NameContains != "" ||
		len(selection.Filter.IncludeItemIDs) != 0 || len(selection.Filter.ExcludeItemIDs) != 0
	switch selection.Mode {
	case SelectionAll:
		if len(selection.ContainerIDs) != 0 || len(selection.ItemIDs) != 0 || hasFilter || selection.RunID != "" {
			return invalid(ErrInvalidSelection, "all", "must not contain branch-specific fields")
		}
	case SelectionContainers:
		if len(selection.ContainerIDs) == 0 || len(selection.ItemIDs) != 0 || hasFilter || selection.RunID != "" {
			return invalid(ErrInvalidSelection, "containers", "requires only containerIds")
		}
	case SelectionItems:
		if len(selection.ItemIDs) == 0 || len(selection.ContainerIDs) != 0 || hasFilter || selection.RunID != "" {
			return invalid(ErrInvalidSelection, "items", "requires only itemIds")
		}
	case SelectionFilter:
		if !hasFilter || len(selection.ContainerIDs) != 0 || len(selection.ItemIDs) != 0 || selection.RunID != "" {
			return invalid(ErrInvalidSelection, "filter", "requires only a non-empty filter")
		}
	case SelectionFailedFromRun:
		if !validHex(selection.RunID, 32) || len(selection.ContainerIDs) != 0 || len(selection.ItemIDs) != 0 || hasFilter {
			return invalid(ErrInvalidSelection, "failedFromRun", "requires only a 32-character runId")
		}
	}
	return nil
}

func validateIDList(field string, values []ID) error {
	seen := make(map[ID]struct{}, len(values))
	for _, id := range values {
		if !ValidID(id) {
			return invalid(ErrInvalidSelection, field, "contains an invalid stable ID")
		}
		if _, exists := seen[id]; exists {
			return invalid(ErrDuplicateIdentity, field, "contains a duplicate stable ID")
		}
		seen[id] = struct{}{}
	}
	return nil
}

func addItemSelection(
	item Item,
	children map[ID][]Item,
	containers map[ID]Container,
	selected map[ID]struct{},
) {
	if item.Kind == ItemCase {
		if _, exists := containers[item.ContainerID]; exists {
			selected[item.ID] = struct{}{}
		}
		return
	}
	for _, child := range children[item.ID] {
		addItemSelection(child, children, containers, selected)
	}
}

func selectableCase(item Item, containers map[ID]Container) bool {
	container, exists := containers[item.ContainerID]
	return exists && item.Kind == ItemCase && !item.Disabled && !container.Disabled
}

func validateFilterReferences(filter Filter, items map[ID]Item) error {
	for _, id := range append(append([]ID(nil), filter.IncludeItemIDs...), filter.ExcludeItemIDs...) {
		if _, exists := items[id]; !exists {
			return invalid(ErrUnknownSelectionID, "filter", id.String())
		}
	}
	return nil
}

func filterMatches(
	item Item,
	filter Filter,
	items map[ID]Item,
	containers map[ID]Container,
	include map[ID]struct{},
	exclude map[ID]struct{},
) bool {
	if len(include) != 0 && !itemOrAncestorInSet(item, items, include) {
		return false
	}
	if itemOrAncestorInSet(item, items, exclude) {
		return false
	}
	if filter.Group != "" && ancestorName(item, ItemGroup, items) != filter.Group {
		return false
	}
	if filter.Suite != "" && ancestorName(item, ItemSuite, items) != filter.Suite {
		return false
	}
	container := containers[item.ContainerID]
	if filter.Label != "" && !hasLabel(item.Labels, filter.Label) && !hasLabel(container.Labels, filter.Label) {
		return false
	}
	if filter.NameContains != "" {
		needle := foldText(filter.NameContains)
		if !strings.Contains(foldText(item.LogicalName), needle) &&
			!strings.Contains(foldText(item.DisplayName), needle) {
			return false
		}
	}
	return true
}

func itemOrAncestorInSet(item Item, items map[ID]Item, values map[ID]struct{}) bool {
	current := item
	for {
		if _, exists := values[current.ID]; exists {
			return true
		}
		if current.ParentID == "" {
			return false
		}
		current = items[current.ParentID]
	}
}

func filterMatchesContainer(container Container, filter Filter) bool {
	if filter.Group != "" || filter.Suite != "" || len(filter.IncludeItemIDs) != 0 {
		return false
	}
	if filter.Label != "" && !hasLabel(container.Labels, filter.Label) {
		return false
	}
	if filter.NameContains != "" {
		needle := foldText(filter.NameContains)
		if !strings.Contains(foldText(container.CTestLogicalName), needle) &&
			!strings.Contains(foldText(container.DisplayName), needle) {
			return false
		}
	}
	return true
}

func ancestorName(item Item, kind ItemKind, items map[ID]Item) string {
	current := item
	for current.ParentID != "" {
		parent, exists := items[current.ParentID]
		if !exists {
			return ""
		}
		if parent.Kind == kind {
			return parent.LogicalName
		}
		current = parent
	}
	return ""
}

func foldText(value string) string {
	return cases.Fold().String(norm.NFC.String(value))
}

func idSet(values []ID) map[ID]struct{} {
	result := make(map[ID]struct{}, len(values))
	for _, id := range values {
		result[id] = struct{}{}
	}
	return result
}

func sortedIDs(values map[ID]struct{}) []ID {
	result := make([]ID, 0, len(values))
	for id := range values {
		result = append(result, id)
	}
	sort.Slice(result, func(first, second int) bool {
		return result[first] < result[second]
	})
	return result
}
