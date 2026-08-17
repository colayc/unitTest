package testdomain

import (
	"net/url"
	"sort"
	"time"
)

type Capabilities struct {
	CanDiscoverCases        bool `json:"canDiscoverCases"`
	CanRunCase              bool `json:"canRunCase"`
	CanReportSkipped        bool `json:"canReportSkipped"`
	CanReportSourceLocation bool `json:"canReportSourceLocation"`
	CanReportMockDetails    bool `json:"canReportMockDetails"`
}

type SourceLocation struct {
	URI        string `json:"uri"`
	Line       int    `json:"line,omitempty"`
	Column     int    `json:"column,omitempty"`
	Navigable  bool   `json:"navigable"`
	Provenance string `json:"provenance"`
}

type Parameter struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type Container struct {
	ID               ID              `json:"id"`
	ProjectID        string          `json:"projectId"`
	CTestLogicalName string          `json:"ctestLogicalName"`
	DisplayName      string          `json:"displayName"`
	Framework        Framework       `json:"framework"`
	Capabilities     Capabilities    `json:"capabilities"`
	Labels           []string        `json:"labels"`
	SourceLocation   *SourceLocation `json:"sourceLocation,omitempty"`
	Disabled         bool            `json:"disabled"`
	DegradedReason   string          `json:"degradedReason,omitempty"`
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
	ID             ID              `json:"id"`
	ContainerID    ID              `json:"containerId"`
	ParentID       ID              `json:"parentId,omitempty"`
	Kind           ItemKind        `json:"kind"`
	Framework      Framework       `json:"framework"`
	LogicalName    string          `json:"logicalName"`
	DisplayName    string          `json:"displayName"`
	Labels         []string        `json:"labels"`
	SourceLocation *SourceLocation `json:"sourceLocation,omitempty"`
	Disabled       bool            `json:"disabled"`
	Parameters     []Parameter     `json:"parameters,omitempty"`
}

type Diagnostic struct {
	Severity  string `json:"severity"`
	Category  string `json:"category"`
	Code      string `json:"code"`
	Message   string `json:"message"`
	SourceURI string `json:"sourceUri,omitempty"`
	Line      int    `json:"line,omitempty"`
	Column    int    `json:"column,omitempty"`
}

type FailureSubtype string

const (
	FailureSubtypeMockUnexpectedCall    FailureSubtype = "mock_unexpected_call"
	FailureSubtypeMockMissingCall       FailureSubtype = "mock_missing_call"
	FailureSubtypeMockParameterMismatch FailureSubtype = "mock_parameter_mismatch"
	FailureSubtypeMockFailure           FailureSubtype = "mock_failure"
)

func (subtype FailureSubtype) Valid() bool {
	switch subtype {
	case "",
		FailureSubtypeMockUnexpectedCall,
		FailureSubtypeMockMissingCall,
		FailureSubtypeMockParameterMismatch,
		FailureSubtypeMockFailure:
		return true
	default:
		return false
	}
}

type FailureDetail struct {
	Category     string           `json:"category"`
	Subtype      FailureSubtype   `json:"subtype,omitempty"`
	Message      string           `json:"message"`
	Expected     string           `json:"expected,omitempty"`
	Actual       string           `json:"actual,omitempty"`
	Locations    []SourceLocation `json:"locations"`
	EvidenceRefs []string         `json:"evidenceRefs"`
}

type Catalog struct {
	ProjectID   string       `json:"projectId"`
	ProfileID   string       `json:"profileId"`
	Revision    string       `json:"revision"`
	GeneratedAt time.Time    `json:"generatedAt"`
	Containers  []Container  `json:"containers"`
	Items       []Item       `json:"items"`
	Diagnostics []Diagnostic `json:"diagnostics"`
	Partial     bool         `json:"partial"`
}

func NewFailureDetail(value FailureDetail) (FailureDetail, error) {
	result := value
	if !validFailureCategory(result.Category) {
		return FailureDetail{}, invalid(
			ErrInvalidResult,
			"failureDetail.category",
			"unsupported value",
		)
	}
	if !result.Subtype.Valid() {
		return FailureDetail{}, invalid(
			ErrInvalidResult,
			"failureDetail.subtype",
			"unsupported value",
		)
	}
	if result.Subtype != "" && result.Category != "assertion_failure" {
		return FailureDetail{}, invalid(
			ErrInvalidResult,
			"failureDetail.subtype",
			"mock subtype requires assertion_failure category",
		)
	}
	var err error
	if result.Message, err = normalizeBoundedText(result.Message, false, 8192); err != nil {
		return FailureDetail{}, invalid(
			ErrInvalidResult,
			"failureDetail.message",
			err.Error(),
		)
	}
	if result.Expected, err = normalizeBoundedText(result.Expected, true, 8192); err != nil {
		return FailureDetail{}, invalid(
			ErrInvalidResult,
			"failureDetail.expected",
			err.Error(),
		)
	}
	if result.Actual, err = normalizeBoundedText(result.Actual, true, 8192); err != nil {
		return FailureDetail{}, invalid(
			ErrInvalidResult,
			"failureDetail.actual",
			err.Error(),
		)
	}
	if len(result.Locations) > 16 {
		return FailureDetail{}, invalid(
			ErrInvalidResult,
			"failureDetail.locations",
			"must not contain more than 16 values",
		)
	}
	result.Locations = make([]SourceLocation, len(value.Locations))
	for index := range value.Locations {
		location, locationErr := cloneAndValidateLocation(&value.Locations[index])
		if locationErr != nil {
			return FailureDetail{}, invalid(
				ErrInvalidResult,
				"failureDetail.locations",
				locationErr.Error(),
			)
		}
		result.Locations[index] = *location
	}
	if len(result.EvidenceRefs) > 64 {
		return FailureDetail{}, invalid(
			ErrInvalidResult,
			"failureDetail.evidenceRefs",
			"must not contain more than 64 values",
		)
	}
	result.EvidenceRefs = make([]string, len(value.EvidenceRefs))
	copy(result.EvidenceRefs, value.EvidenceRefs)
	seenEvidence := make(map[string]struct{}, len(result.EvidenceRefs))
	for _, reference := range result.EvidenceRefs {
		if !validHex(reference, 32) {
			return FailureDetail{}, invalid(
				ErrInvalidResult,
				"failureDetail.evidenceRefs",
				"must contain artifact IDs",
			)
		}
		if _, duplicate := seenEvidence[reference]; duplicate {
			return FailureDetail{}, invalid(
				ErrInvalidResult,
				"failureDetail.evidenceRefs",
				"must not contain duplicates",
			)
		}
		seenEvidence[reference] = struct{}{}
	}
	return result, nil
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
	if !validProjectID(result.ProjectID) {
		return Container{}, invalid(ErrInvalidCatalog, "container.projectId", "has an invalid format")
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
		if result.DegradedReason, err = normalizeBoundedText(result.DegradedReason, true, 2048); err != nil {
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
	if !validProjectID(projectID) {
		return Catalog{}, invalid(ErrInvalidCatalog, "projectId", "has an invalid format")
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
	if len(value.Containers) > 10_000 || len(value.Items) > 100_000 || len(value.Diagnostics) > 1_000 {
		return Catalog{}, invalid(ErrInvalidCatalog, "items", "Catalog exceeds the domain limit")
	}
	result := Catalog{
		ProjectID:   projectID,
		ProfileID:   value.ProfileID,
		Revision:    value.Revision,
		GeneratedAt: value.GeneratedAt.UTC(),
		Containers:  make([]Container, len(value.Containers)),
		Items:       make([]Item, len(value.Items)),
		Diagnostics: make([]Diagnostic, len(value.Diagnostics)),
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
	for index, candidate := range value.Diagnostics {
		diagnostic, err := newDiagnostic(candidate)
		if err != nil {
			return Catalog{}, err
		}
		result.Diagnostics[index] = diagnostic
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
	if len(values) > 256 {
		return nil, invalid(ErrInvalidCatalog, "labels", "must not contain more than 256 values")
	}
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
	switch result.Provenance {
	case "ctest-backtrace", "framework-manifest", "framework-output",
		"mock-expectation", "mock-actual-call", "test-declaration":
	default:
		return nil, invalid(ErrInvalidCatalog, "provenance", "unsupported value")
	}
	return &result, nil
}

func newDiagnostic(value Diagnostic) (Diagnostic, error) {
	switch value.Severity {
	case "error", "warning", "info":
	default:
		return Diagnostic{}, invalid(ErrInvalidCatalog, "diagnostic.severity", "unsupported value")
	}
	if !validFailureCategory(value.Category) {
		return Diagnostic{}, invalid(ErrInvalidCatalog, "diagnostic.category", "unsupported value")
	}
	var err error
	if value.Code, err = normalizeBoundedText(value.Code, false, 128); err != nil {
		return Diagnostic{}, invalid(ErrInvalidCatalog, "diagnostic.code", err.Error())
	}
	if value.Message, err = normalizeBoundedText(value.Message, false, 8192); err != nil {
		return Diagnostic{}, invalid(ErrInvalidCatalog, "diagnostic.message", err.Error())
	}
	if value.SourceURI != "" {
		parsed, err := url.ParseRequestURI(value.SourceURI)
		if err != nil || parsed.Scheme == "" {
			return Diagnostic{}, invalid(ErrInvalidCatalog, "diagnostic.sourceUri", "must be an absolute URI")
		}
	}
	if value.Line < 0 || value.Column < 0 {
		return Diagnostic{}, invalid(ErrInvalidCatalog, "diagnostic.location", "line and column must be non-negative")
	}
	return value, nil
}

func validFailureCategory(value string) bool {
	switch value {
	case "configuration_error", "build_error", "assertion_failure", "test_process_crash",
		"test_timeout", "cancelled", "framework_output_invalid", "infrastructure_error",
		"unexpected_exit", "inconsistent_exit_status":
		return true
	default:
		return false
	}
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

func validProjectID(value string) bool {
	if len(value) < 1 || len(value) > 64 {
		return false
	}
	for index := range len(value) {
		character := value[index]
		if character >= 'A' && character <= 'Z' ||
			character >= 'a' && character <= 'z' ||
			character >= '0' && character <= '9' ||
			index > 0 && (character == '.' || character == '_' || character == '-') {
			continue
		}
		return false
	}
	return true
}

func hasLabel(labels []string, expected string) bool {
	index := sort.SearchStrings(labels, expected)
	return index < len(labels) && labels[index] == expected
}
