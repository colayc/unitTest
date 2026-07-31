package testdomain

import (
	"net/url"
	"sort"
	"time"
)

type Capabilities struct {
	CanDiscoverCases        bool
	CanRunCase              bool
	CanReportSkipped        bool
	CanReportSourceLocation bool
	CanReportMockDetails    bool
}

type SourceLocation struct {
	URI        string
	Line       int
	Column     int
	Navigable  bool
	Provenance string
}

type Parameter struct {
	Name  string
	Value string
}

type Container struct {
	ID               ID
	ProjectID        string
	CTestLogicalName string
	DisplayName      string
	Framework        Framework
	Capabilities     Capabilities
	Labels           []string
	SourceLocation   *SourceLocation
	Disabled         bool
	DegradedReason   string
}

type ItemKind string

const (
	ItemGroup ItemKind = "group"
	ItemSuite ItemKind = "suite"
	ItemCase  ItemKind = "case"
)

func (kind ItemKind) Valid() bool {
	switch kind {
	case ItemGroup, ItemSuite, ItemCase:
		return true
	default:
		return false
	}
}

type Item struct {
	ID             ID
	ContainerID    ID
	ParentID       ID
	Kind           ItemKind
	Framework      Framework
	LogicalName    string
	DisplayName    string
	Labels         []string
	SourceLocation *SourceLocation
	Disabled       bool
	Parameters     []Parameter
}

type Diagnostic struct {
	Severity  string
	Category  string
	Code      string
	Message   string
	SourceURI string
	Line      int
	Column    int
}

type Catalog struct {
	ProjectID   string
	ProfileID   string
	Revision    string
	GeneratedAt time.Time
	Containers  []Container
	Items       []Item
	Diagnostics []Diagnostic
	Partial     bool
}

func NewContainer(value Container) (Container, error) {
	result := cloneContainer(value)
	if !ValidID(result.ID) {
		return Container{}, invalid(ErrInvalidCatalog, "container.id", "must be a stable test ID")
	}
	var err error
	if result.ProjectID, err = normalizeIdentityText(result.ProjectID, false); err != nil {
		return Container{}, invalid(ErrInvalidCatalog, "container.projectId", err.Error())
	}
	if result.CTestLogicalName, err = normalizeIdentityText(result.CTestLogicalName, false); err != nil {
		return Container{}, invalid(ErrInvalidCatalog, "container.ctestLogicalName", err.Error())
	}
	expectedID, err := ContainerID(result.ProjectID, result.CTestLogicalName)
	if err != nil || result.ID != expectedID {
		return Container{}, invalid(ErrInvalidCatalog, "container.id", "does not match the logical identity")
	}
	if result.DisplayName, err = normalizeIdentityText(result.DisplayName, false); err != nil {
		return Container{}, invalid(ErrInvalidCatalog, "container.displayName", err.Error())
	}
	if !result.Framework.Valid() {
		return Container{}, invalid(ErrInvalidFramework, "container.framework", "unsupported value")
	}
	if result.Labels, err = canonicalLabels(result.Labels); err != nil {
		return Container{}, err
	}
	if result.SourceLocation, err = cloneAndValidateLocation(result.SourceLocation); err != nil {
		return Container{}, invalid(ErrInvalidCatalog, "container.sourceLocation", err.Error())
	}
	if result.DegradedReason != "" {
		if result.DegradedReason, err = normalizeIdentityText(result.DegradedReason, true); err != nil {
			return Container{}, invalid(ErrInvalidCatalog, "container.degradedReason", err.Error())
		}
	}
	return result, nil
}

func NewItem(value Item) (Item, error) {
	result := cloneItem(value)
	if !ValidID(result.ID) || !ValidID(result.ContainerID) ||
		(result.ParentID != "" && !ValidID(result.ParentID)) {
		return Item{}, invalid(ErrInvalidCatalog, "item.id", "item references must be stable test IDs")
	}
	if !result.Kind.Valid() {
		return Item{}, invalid(ErrInvalidCatalog, "item.kind", "unsupported value")
	}
	if !result.Framework.Valid() {
		return Item{}, invalid(ErrInvalidFramework, "item.framework", "unsupported value")
	}
	var err error
	if result.LogicalName, err = normalizeIdentityText(result.LogicalName, false); err != nil {
		return Item{}, invalid(ErrInvalidCatalog, "item.logicalName", err.Error())
	}
	if result.DisplayName, err = normalizeIdentityText(result.DisplayName, false); err != nil {
		return Item{}, invalid(ErrInvalidCatalog, "item.displayName", err.Error())
	}
	if result.Labels, err = canonicalLabels(result.Labels); err != nil {
		return Item{}, err
	}
	if result.Parameters, err = canonicalParameters(result.Parameters); err != nil {
		return Item{}, err
	}
	if result.SourceLocation, err = cloneAndValidateLocation(result.SourceLocation); err != nil {
		return Item{}, invalid(ErrInvalidCatalog, "item.sourceLocation", err.Error())
	}
	return result, nil
}

func NewCatalog(value Catalog) (Catalog, error) {
	if value.Partial {
		return Catalog{}, invalid(ErrInvalidCatalog, "partial", "partial Catalog snapshots cannot be published")
	}
	projectID, err := normalizeIdentityText(value.ProjectID, false)
	if err != nil {
		return Catalog{}, invalid(ErrInvalidCatalog, "projectId", err.Error())
	}
	if !validHex(value.ProfileID, 64) {
		return Catalog{}, invalid(ErrInvalidCatalog, "profileId", "must be 64 lowercase hexadecimal characters")
	}
	if !validHex(value.Revision, 64) {
		return Catalog{}, invalid(ErrInvalidCatalog, "revision", "must be 64 lowercase hexadecimal characters")
	}
	if value.GeneratedAt.IsZero() {
		return Catalog{}, invalid(ErrInvalidCatalog, "generatedAt", "must not be zero")
	}
	if len(value.Containers) > 100_000 || len(value.Items) > 100_000 {
		return Catalog{}, invalid(ErrInvalidCatalog, "items", "Catalog exceeds the domain limit")
	}
	result := Catalog{
		ProjectID:   projectID,
		ProfileID:   value.ProfileID,
		Revision:    value.Revision,
		GeneratedAt: value.GeneratedAt,
		Containers:  make([]Container, len(value.Containers)),
		Items:       make([]Item, len(value.Items)),
		Diagnostics: append([]Diagnostic(nil), value.Diagnostics...),
	}
	containers := make(map[ID]Container, len(value.Containers))
	allIDs := make(map[ID]struct{}, len(value.Containers)+len(value.Items))
	for index, candidate := range value.Containers {
		container, err := NewContainer(candidate)
		if err != nil {
			return Catalog{}, err
		}
		if container.ProjectID != projectID {
			return Catalog{}, invalid(ErrInvalidCatalog, "container.projectId", "does not match Catalog project")
		}
		if _, exists := allIDs[container.ID]; exists {
			return Catalog{}, invalid(ErrDuplicateIdentity, "container.id", "duplicate stable ID")
		}
		allIDs[container.ID] = struct{}{}
		containers[container.ID] = container
		result.Containers[index] = container
	}
	items := make(map[ID]Item, len(value.Items))
	for index, candidate := range value.Items {
		item, err := NewItem(candidate)
		if err != nil {
			return Catalog{}, err
		}
		if _, exists := allIDs[item.ID]; exists {
			return Catalog{}, invalid(ErrDuplicateIdentity, "item.id", "duplicate stable ID")
		}
		allIDs[item.ID] = struct{}{}
		items[item.ID] = item
		result.Items[index] = item
	}
	if err := validateCatalogTree(containers, items); err != nil {
		return Catalog{}, err
	}
	if err := validateUniqueLogicalItems(result.Items); err != nil {
		return Catalog{}, err
	}
	return result, nil
}

func (catalog Catalog) Clone() Catalog {
	result := catalog
	result.Containers = make([]Container, len(catalog.Containers))
	for index, container := range catalog.Containers {
		result.Containers[index] = cloneContainer(container)
	}
	result.Items = make([]Item, len(catalog.Items))
	for index, item := range catalog.Items {
		result.Items[index] = cloneItem(item)
	}
	result.Diagnostics = append([]Diagnostic(nil), catalog.Diagnostics...)
	return result
}

func validateCatalogTree(containers map[ID]Container, items map[ID]Item) error {
	for _, item := range items {
		container, exists := containers[item.ContainerID]
		if !exists {
			return invalid(ErrInvalidCatalog, "item.containerId", "unknown container reference")
		}
		if container.Framework != item.Framework {
			return invalid(ErrInvalidCatalog, "item.framework", "does not match container")
		}
		if item.ParentID != "" {
			parent, exists := items[item.ParentID]
			if !exists {
				return invalid(ErrInvalidCatalog, "item.parentId", "unknown item reference")
			}
			if parent.ContainerID != item.ContainerID {
				return invalid(ErrInvalidCatalog, "item.parentId", "crosses container boundary")
			}
		}
	}
	for _, item := range items {
		visited := make(map[ID]struct{})
		current := item
		for current.ParentID != "" {
			if _, exists := visited[current.ID]; exists {
				return invalid(ErrInvalidCatalog, "item.parentId", "contains a cycle")
			}
			visited[current.ID] = struct{}{}
			current = items[current.ParentID]
		}
	}
	return nil
}

func validateUniqueLogicalItems(items []Item) error {
	seen := make(map[ID]struct{}, len(items))
	for _, item := range items {
		fields, err := identityFields(
			identityField{name: "kind", value: string(item.Kind)},
			identityField{name: "container_id", value: item.ContainerID.String()},
			identityField{name: "parent_id", value: item.ParentID.String(), optional: true},
			identityField{name: "logical_name", value: item.LogicalName},
		)
		if err != nil {
			return invalid(ErrInvalidCatalog, "item.logicalIdentity", err.Error())
		}
		for _, parameter := range item.Parameters {
			fields = append(fields,
				identityField{name: "parameter_name", value: parameter.Name},
				identityField{name: "parameter_value", value: parameter.Value, optional: true},
			)
		}
		key := hashIdentity(fields)
		if _, exists := seen[key]; exists {
			return invalid(ErrDuplicateIdentity, "item.logicalIdentity", "duplicate logical identity")
		}
		seen[key] = struct{}{}
	}
	return nil
}

func canonicalLabels(values []string) ([]string, error) {
	result := append([]string(nil), values...)
	seen := make(map[string]struct{}, len(result))
	for index, value := range result {
		normalized, err := normalizeIdentityText(value, false)
		if err != nil {
			return nil, invalid(ErrInvalidCatalog, "labels", err.Error())
		}
		if _, exists := seen[normalized]; exists {
			return nil, invalid(ErrDuplicateIdentity, "labels", "duplicate value")
		}
		seen[normalized] = struct{}{}
		result[index] = normalized
	}
	sort.Strings(result)
	return result, nil
}

func cloneContainer(value Container) Container {
	value.Labels = append([]string(nil), value.Labels...)
	if value.SourceLocation != nil {
		location := *value.SourceLocation
		value.SourceLocation = &location
	}
	return value
}

func cloneItem(value Item) Item {
	value.Labels = append([]string(nil), value.Labels...)
	value.Parameters = append([]Parameter(nil), value.Parameters...)
	if value.SourceLocation != nil {
		location := *value.SourceLocation
		value.SourceLocation = &location
	}
	return value
}

func cloneAndValidateLocation(value *SourceLocation) (*SourceLocation, error) {
	if value == nil {
		return nil, nil
	}
	result := *value
	parsed, err := url.ParseRequestURI(result.URI)
	if err != nil || parsed.Scheme == "" {
		return nil, invalid(ErrInvalidCatalog, "uri", "must be an absolute URI")
	}
	if result.Line < 0 || result.Column < 0 {
		return nil, invalid(ErrInvalidCatalog, "location", "line and column must be non-negative")
	}
	return &result, nil
}

func validHex(value string, size int) bool {
	if len(value) != size {
		return false
	}
	for _, character := range value {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}

func hasLabel(labels []string, expected string) bool {
	index := sort.SearchStrings(labels, expected)
	return index < len(labels) && labels[index] == expected
}
