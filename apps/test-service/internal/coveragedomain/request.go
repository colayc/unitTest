// Package coveragedomain owns coverage-run request identity and validation.
package coveragedomain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"unit-test-ide.local/test-service/internal/testdomain"
)

var ErrInvalidRequest = errors.New("invalid coverage request")

const (
	maxSelectionContainerIDs = 10_000
	maxSelectionItemIDs      = 100_000
)

// ValidationError identifies the invalid request field while retaining any wrapped domain cause.
type ValidationError struct {
	Field  string
	Detail string
	cause  error
}

func (e *ValidationError) Error() string {
	if e.Field == "" {
		return fmt.Sprintf("%v: %s", ErrInvalidRequest, e.Detail)
	}
	return fmt.Sprintf("%v: %s: %s", ErrInvalidRequest, e.Field, e.Detail)
}

func (e *ValidationError) Unwrap() error {
	if e.cause != nil {
		return e.cause
	}
	return ErrInvalidRequest
}

func (e *ValidationError) Is(target error) bool { return target == ErrInvalidRequest }

// Request is the closed input used to create an immutable coverage run.
type Request struct {
	IdempotencyKey      string
	WorkspaceGeneration string
	ProjectID           string
	CoverageProfileID   string
	CatalogRevision     string
	Selection           testdomain.Selection
	RepeatCount         int64
	Timeout             time.Duration
}

// NewRequest validates the protocol-level request and takes ownership of its selection slices.
func NewRequest(value Request) (Request, error) {
	if !validHex(value.IdempotencyKey, 32) {
		return Request{}, invalid("idempotencyKey", "must be 32 lowercase hexadecimal characters")
	}
	if !validHex(value.WorkspaceGeneration, 64) {
		return Request{}, invalid("workspaceGeneration", "must be 64 lowercase hexadecimal characters")
	}
	if !validProjectID(value.ProjectID) {
		return Request{}, invalid("projectId", "has an invalid shape")
	}
	if !validProjectID(value.CoverageProfileID) {
		return Request{}, invalid("coverageProfileId", "has an invalid shape")
	}
	if !validHex(value.CatalogRevision, 64) {
		return Request{}, invalid("catalogRevision", "must be 64 lowercase hexadecimal characters")
	}
	if value.RepeatCount < 1 || value.RepeatCount > 100 {
		return Request{}, invalid("repeatCount", "must be between 1 and 100")
	}
	if value.Timeout < time.Millisecond || value.Timeout > 24*time.Hour || value.Timeout%time.Millisecond != 0 {
		return Request{}, invalid("timeout", "must be millisecond aligned and between 1ms and 24h")
	}
	if err := validateSelectionCardinality(value.Selection); err != nil {
		return Request{}, err
	}
	selection, err := testdomain.NewSelection(value.Selection)
	if err != nil {
		return Request{}, invalidSelection(err)
	}
	sortSelectionIDs(&selection)
	return Request{
		IdempotencyKey: value.IdempotencyKey, WorkspaceGeneration: value.WorkspaceGeneration,
		ProjectID: value.ProjectID, CoverageProfileID: value.CoverageProfileID,
		CatalogRevision: value.CatalogRevision, Selection: selection,
		RepeatCount: value.RepeatCount, Timeout: value.Timeout,
	}, nil
}

// Clone returns an independently owned request.
func (value Request) Clone() Request {
	result := value
	result.Selection.ContainerIDs = append([]testdomain.ID(nil), value.Selection.ContainerIDs...)
	result.Selection.ItemIDs = append([]testdomain.ID(nil), value.Selection.ItemIDs...)
	result.Selection.Filter.IncludeItemIDs = append([]testdomain.ID(nil), value.Selection.Filter.IncludeItemIDs...)
	result.Selection.Filter.ExcludeItemIDs = append([]testdomain.ID(nil), value.Selection.Filter.ExcludeItemIDs...)
	return result
}

// CanonicalJSON returns the closed Protocol v1.4 request representation.
func (value Request) CanonicalJSON() ([]byte, error) {
	request, err := NewRequest(value)
	if err != nil {
		return nil, err
	}
	return json.Marshal(requestWire{
		IdempotencyKey: request.IdempotencyKey, WorkspaceGeneration: request.WorkspaceGeneration,
		ProjectID: request.ProjectID, CoverageProfileID: request.CoverageProfileID,
		CatalogRevision: request.CatalogRevision, Selection: selectionWireFrom(request.Selection),
		RepeatCount: request.RepeatCount, TimeoutMS: request.Timeout.Milliseconds(),
	})
}

// CoverageRunID derives the stable identity from the canonical validated request.
func CoverageRunID(value Request) (string, error) {
	request, err := NewRequest(value)
	if err != nil {
		return "", err
	}
	raw, err := request.CanonicalJSON()
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(append([]byte("coverage-run-v1\x00"), raw...))
	return hex.EncodeToString(sum[:16]), nil
}

type requestWire struct {
	IdempotencyKey      string        `json:"idempotencyKey"`
	WorkspaceGeneration string        `json:"workspaceGeneration"`
	ProjectID           string        `json:"projectId"`
	CoverageProfileID   string        `json:"coverageProfileId"`
	CatalogRevision     string        `json:"catalogRevision"`
	Selection           selectionWire `json:"selection"`
	RepeatCount         int64         `json:"repeatCount"`
	TimeoutMS           int64         `json:"timeoutMs"`
}

type selectionWire struct {
	Mode         testdomain.SelectionMode `json:"mode"`
	ContainerIDs []testdomain.ID          `json:"containerIds,omitempty"`
	ItemIDs      []testdomain.ID          `json:"itemIds,omitempty"`
	Filter       *filterWire              `json:"filter,omitempty"`
	RunID        string                   `json:"runId,omitempty"`
}

type filterWire struct {
	Group          string          `json:"group,omitempty"`
	Suite          string          `json:"suite,omitempty"`
	Label          string          `json:"label,omitempty"`
	NameContains   string          `json:"nameContains,omitempty"`
	IncludeItemIDs []testdomain.ID `json:"includeItemIds,omitempty"`
	ExcludeItemIDs []testdomain.ID `json:"excludeItemIds,omitempty"`
}

func selectionWireFrom(value testdomain.Selection) selectionWire {
	result := selectionWire{Mode: value.Mode, ContainerIDs: value.ContainerIDs, ItemIDs: value.ItemIDs, RunID: value.RunID}
	if value.Mode == testdomain.SelectionFilter {
		result.Filter = &filterWire{Group: value.Filter.Group, Suite: value.Filter.Suite, Label: value.Filter.Label, NameContains: value.Filter.NameContains,
			IncludeItemIDs: value.Filter.IncludeItemIDs, ExcludeItemIDs: value.Filter.ExcludeItemIDs}
	}
	return result
}

func sortSelectionIDs(value *testdomain.Selection) {
	sort.Slice(value.ContainerIDs, func(i, j int) bool { return value.ContainerIDs[i] < value.ContainerIDs[j] })
	sort.Slice(value.ItemIDs, func(i, j int) bool { return value.ItemIDs[i] < value.ItemIDs[j] })
	sort.Slice(value.Filter.IncludeItemIDs, func(i, j int) bool { return value.Filter.IncludeItemIDs[i] < value.Filter.IncludeItemIDs[j] })
	sort.Slice(value.Filter.ExcludeItemIDs, func(i, j int) bool { return value.Filter.ExcludeItemIDs[i] < value.Filter.ExcludeItemIDs[j] })
}

func invalid(field, detail string) error { return &ValidationError{Field: field, Detail: detail} }

func invalidSelection(cause error) error {
	field, detail := "selection", cause.Error()
	var selectionError *testdomain.ValidationError
	if errors.As(cause, &selectionError) {
		if selectionError.Field != "" {
			field += "." + selectionError.Field
		}
		detail = selectionError.Detail
	}
	return &ValidationError{Field: field, Detail: detail, cause: cause}
}

func validateSelectionCardinality(value testdomain.Selection) error {
	fields := []struct {
		name   string
		length int
		max    int
	}{
		{name: "selection.containerIds", length: len(value.ContainerIDs), max: maxSelectionContainerIDs},
		{name: "selection.itemIds", length: len(value.ItemIDs), max: maxSelectionItemIDs},
		{name: "selection.filter.includeItemIds", length: len(value.Filter.IncludeItemIDs), max: maxSelectionItemIDs},
		{name: "selection.filter.excludeItemIds", length: len(value.Filter.ExcludeItemIDs), max: maxSelectionItemIDs},
	}
	for _, field := range fields {
		if field.length > field.max {
			return invalid(field.name, fmt.Sprintf("must contain at most %d IDs", field.max))
		}
	}
	return nil
}

func validHex(value string, length int) bool {
	if len(value) != length {
		return false
	}
	for _, char := range []byte(value) {
		if char < '0' || char > '9' && (char < 'a' || char > 'f') {
			return false
		}
	}
	return true
}

func validProjectID(value string) bool {
	if len(value) == 0 || len(value) > 64 || !asciiAlphaNumeric(value[0]) {
		return false
	}
	for _, char := range []byte(value[1:]) {
		if !asciiAlphaNumeric(char) && char != '.' && char != '_' && char != '-' {
			return false
		}
	}
	return true
}

func asciiAlphaNumeric(value byte) bool {
	return value >= 'A' && value <= 'Z' || value >= 'a' && value <= 'z' || value >= '0' && value <= '9'
}
