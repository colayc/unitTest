package coveragedomain

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"unit-test-ide.local/test-service/internal/testdomain"
)

const (
	requestID       = "11111111111111111111111111111111"
	workspaceGen    = "2222222222222222222222222222222222222222222222222222222222222222"
	catalogRevision = "3333333333333333333333333333333333333333333333333333333333333333"
	firstStableID   = testdomain.ID("utid-v1-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	secondStableID  = testdomain.ID("utid-v1-bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")
)

func validRequest() Request {
	return Request{
		IdempotencyKey:      requestID,
		WorkspaceGeneration: workspaceGen,
		ProjectID:           "core",
		CoverageProfileID:   "coverage.default",
		CatalogRevision:     catalogRevision,
		Selection: testdomain.Selection{
			Mode:    testdomain.SelectionItems,
			ItemIDs: []testdomain.ID{secondStableID, firstStableID},
		},
		RepeatCount: 2,
		Timeout:     5 * time.Second,
	}
}

func TestRequestValidatesProtocolBounds(t *testing.T) {
	for name, mutate := range map[string]func(*Request){
		"repeat minimum":  func(v *Request) { v.RepeatCount = 1 },
		"repeat maximum":  func(v *Request) { v.RepeatCount = 100 },
		"timeout minimum": func(v *Request) { v.Timeout = time.Millisecond },
		"timeout maximum": func(v *Request) { v.Timeout = 24 * time.Hour },
	} {
		t.Run(name, func(t *testing.T) {
			value := validRequest()
			mutate(&value)
			if _, err := NewRequest(value); err != nil {
				t.Fatalf("NewRequest() error = %v, want nil", err)
			}
		})
	}
	for name, mutate := range map[string]func(*Request){
		"repeat below minimum":            func(v *Request) { v.RepeatCount = 0 },
		"repeat above maximum":            func(v *Request) { v.RepeatCount = 101 },
		"timeout below millisecond":       func(v *Request) { v.Timeout = time.Millisecond - time.Nanosecond },
		"timeout not millisecond aligned": func(v *Request) { v.Timeout = time.Millisecond + time.Nanosecond },
		"timeout zero":                    func(v *Request) { v.Timeout = 0 },
		"timeout above maximum":           func(v *Request) { v.Timeout = 24*time.Hour + time.Millisecond },
		"uppercase id":                    func(v *Request) { v.IdempotencyKey = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA" },
		"short id":                        func(v *Request) { v.IdempotencyKey = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" },
		"invalid project":                 func(v *Request) { v.ProjectID = "-bad" },
		"invalid coverage profile":        func(v *Request) { v.CoverageProfileID = "bad profile" },
		"empty selection":                 func(v *Request) { v.Selection.ItemIDs = nil },
	} {
		t.Run(name, func(t *testing.T) {
			value := validRequest()
			mutate(&value)
			_, err := NewRequest(value)
			if !errors.Is(err, ErrInvalidRequest) {
				t.Fatalf("NewRequest() error = %v, want ErrInvalidRequest", err)
			}
		})
	}
}

func TestRequestSelectionCardinalityBounds(t *testing.T) {
	tests := []struct {
		name  string
		field string
		max   int
		set   func(*Request, []testdomain.ID)
	}{
		{
			name: "container IDs", field: "selection.containerIds", max: 10_000,
			set: func(value *Request, ids []testdomain.ID) {
				value.Selection = testdomain.Selection{Mode: testdomain.SelectionContainers, ContainerIDs: ids}
			},
		},
		{
			name: "item IDs", field: "selection.itemIds", max: 100_000,
			set: func(value *Request, ids []testdomain.ID) {
				value.Selection = testdomain.Selection{Mode: testdomain.SelectionItems, ItemIDs: ids}
			},
		},
		{
			name: "filter include item IDs", field: "selection.filter.includeItemIds", max: 100_000,
			set: func(value *Request, ids []testdomain.ID) {
				value.Selection = testdomain.Selection{
					Mode:   testdomain.SelectionFilter,
					Filter: testdomain.Filter{IncludeItemIDs: ids},
				}
			},
		},
		{
			name: "filter exclude item IDs", field: "selection.filter.excludeItemIds", max: 100_000,
			set: func(value *Request, ids []testdomain.ID) {
				value.Selection = testdomain.Selection{
					Mode:   testdomain.SelectionFilter,
					Filter: testdomain.Filter{ExcludeItemIDs: ids},
				}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ids := requestStableIDs(test.max + 1)

			atMaximum := validRequest()
			test.set(&atMaximum, ids[:test.max])
			if _, err := NewRequest(atMaximum); err != nil {
				t.Fatalf("NewRequest(exact maximum %d) error = %v", test.max, err)
			}

			overMaximum := validRequest()
			test.set(&overMaximum, ids)
			if _, err := NewRequest(overMaximum); !errors.Is(err, ErrInvalidRequest) {
				t.Fatalf("NewRequest(maximum+1) error = %v, want ErrInvalidRequest", err)
			} else {
				var validation *ValidationError
				if !errors.As(err, &validation) || validation.Field != test.field {
					t.Fatalf("NewRequest(maximum+1) error = %#v, want local ValidationError field %q", err, test.field)
				}
			}
			if raw, err := overMaximum.CanonicalJSON(); raw != nil || !errors.Is(err, ErrInvalidRequest) {
				t.Fatalf("CanonicalJSON(maximum+1) = %q, %v; want nil, ErrInvalidRequest", raw, err)
			}
			if id, err := CoverageRunID(overMaximum); id != "" || !errors.Is(err, ErrInvalidRequest) {
				t.Fatalf("CoverageRunID(maximum+1) = %q, %v; want empty, ErrInvalidRequest", id, err)
			}
		})
	}
}

func TestRequestReusesSelectionValidation(t *testing.T) {
	for name, mutate := range map[string]func(*Request){
		"duplicate":    func(v *Request) { v.Selection.ItemIDs = []testdomain.ID{firstStableID, firstStableID} },
		"invalid mode": func(v *Request) { v.Selection.Mode = "nope" },
		"invalid failed from run": func(v *Request) {
			v.Selection = testdomain.Selection{Mode: testdomain.SelectionFailedFromRun, RunID: "bad"}
		},
		"normalized filter": func(v *Request) {
			v.Selection = testdomain.Selection{Mode: testdomain.SelectionFilter, Filter: testdomain.Filter{Label: "e\u0301"}}
		},
	} {
		t.Run(name, func(t *testing.T) {
			value := validRequest()
			mutate(&value)
			validated, err := NewRequest(value)
			if name == "normalized filter" {
				if err != nil || validated.Selection.Filter.Label != "é" {
					t.Fatalf("NewRequest() = %#v, %v; want NFC-normalized filter", validated, err)
				}
				return
			}
			if !errors.Is(err, ErrInvalidRequest) {
				t.Fatalf("NewRequest() error = %v, want ErrInvalidRequest", err)
			}
		})
	}
}

func TestRequestSelectionFailuresExposeLocalValidationErrorAndPreserveCause(t *testing.T) {
	tests := []struct {
		name            string
		mutate          func(*Request)
		localField      string
		localDetail     string
		underlyingKind  error
		underlyingField string
	}{
		{
			name: "duplicate item ID",
			mutate: func(value *Request) {
				value.Selection.ItemIDs = []testdomain.ID{firstStableID, firstStableID}
			},
			localField: "selection.itemIds", localDetail: "contains a duplicate stable ID",
			underlyingKind: testdomain.ErrDuplicateIdentity, underlyingField: "itemIds",
		},
		{
			name: "invalid mode",
			mutate: func(value *Request) {
				value.Selection.Mode = "nope"
			},
			localField: "selection.mode", localDetail: "unsupported value",
			underlyingKind: testdomain.ErrInvalidSelection, underlyingField: "mode",
		},
		{
			name: "invalid failed from run",
			mutate: func(value *Request) {
				value.Selection = testdomain.Selection{Mode: testdomain.SelectionFailedFromRun, RunID: "bad"}
			},
			localField: "selection.failedFromRun", localDetail: "requires only a 32-character runId",
			underlyingKind: testdomain.ErrInvalidSelection, underlyingField: "failedFromRun",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			value := validRequest()
			test.mutate(&value)
			_, err := NewRequest(value)

			var local *ValidationError
			if !errors.As(err, &local) || local.Field != test.localField || local.Detail != test.localDetail {
				t.Fatalf(
					"NewRequest() error = %#v, want local ValidationError field/detail %q/%q",
					err, test.localField, test.localDetail,
				)
			}
			var underlying *testdomain.ValidationError
			if !errors.As(err, &underlying) || underlying.Field != test.underlyingField {
				t.Fatalf("NewRequest() underlying error = %#v, want testdomain.ValidationError field %q", err, test.underlyingField)
			}
			if unwrapped := errors.Unwrap(local); unwrapped != underlying {
				t.Fatalf("errors.Unwrap(local) = %#v, want original testdomain error %#v", unwrapped, underlying)
			}
			if !errors.Is(err, ErrInvalidRequest) || !errors.Is(err, test.underlyingKind) {
				t.Fatalf("NewRequest() error = %v, want ErrInvalidRequest and %v", err, test.underlyingKind)
			}
		})
	}
}

func TestRequestCanonicalizesAndOwnsSelection(t *testing.T) {
	itemInput := validRequest()
	items, err := NewRequest(itemInput)
	if err != nil {
		t.Fatalf("NewRequest() error = %v", err)
	}
	want := []testdomain.ID{firstStableID, secondStableID}
	if !reflect.DeepEqual(items.Selection.ItemIDs, want) {
		t.Fatalf("NewRequest() item ids = %#v, want %#v", items.Selection.ItemIDs, want)
	}
	itemInput.Selection.ItemIDs[0] = firstStableID
	itemClone := items.Clone()
	itemClone.Selection.ItemIDs[0] = secondStableID
	if !reflect.DeepEqual(items.Selection.ItemIDs, want) {
		t.Fatal("validated request item IDs were mutated through input or clone")
	}
	value := validRequest()
	value.Selection = testdomain.Selection{
		Mode: testdomain.SelectionFilter,
		Filter: testdomain.Filter{
			Label:          "fast",
			IncludeItemIDs: []testdomain.ID{secondStableID, firstStableID},
			ExcludeItemIDs: []testdomain.ID{secondStableID, firstStableID},
		},
	}
	validated, err := NewRequest(value)
	if err != nil {
		t.Fatalf("NewRequest() error = %v", err)
	}
	if !reflect.DeepEqual(validated.Selection.Filter.IncludeItemIDs, want) || !reflect.DeepEqual(validated.Selection.Filter.ExcludeItemIDs, want) {
		t.Fatalf("NewRequest() filter ids = %#v, %#v; want %#v", validated.Selection.Filter.IncludeItemIDs, validated.Selection.Filter.ExcludeItemIDs, want)
	}
	value.Selection.Filter.IncludeItemIDs[0] = firstStableID
	value.Selection.Filter.ExcludeItemIDs[0] = firstStableID
	clone := validated.Clone()
	clone.Selection.Filter.IncludeItemIDs[0] = secondStableID
	clone.Selection.Filter.ExcludeItemIDs[0] = secondStableID
	if !reflect.DeepEqual(validated.Selection.Filter.IncludeItemIDs, want) || !reflect.DeepEqual(validated.Selection.Filter.ExcludeItemIDs, want) {
		t.Fatal("validated request selection was mutated through input or clone")
	}
	containers := validRequest()
	containers.Selection = testdomain.Selection{Mode: testdomain.SelectionContainers, ContainerIDs: []testdomain.ID{secondStableID, firstStableID}}
	owned, err := NewRequest(containers)
	if err != nil || !reflect.DeepEqual(owned.Selection.ContainerIDs, want) {
		t.Fatalf("NewRequest() containers = %#v, %v; want %#v", owned.Selection.ContainerIDs, err, want)
	}
	containers.Selection.ContainerIDs[0] = firstStableID
	containerClone := owned.Clone()
	containerClone.Selection.ContainerIDs[0] = secondStableID
	if !reflect.DeepEqual(owned.Selection.ContainerIDs, want) {
		t.Fatal("validated request container IDs were mutated through input or clone")
	}
}

func TestRequestCanonicalJSONIsClosedAndUsesMilliseconds(t *testing.T) {
	validated, err := NewRequest(validRequest())
	if err != nil {
		t.Fatal(err)
	}
	raw, err := validated.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	var decoded any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	allowed := map[string]bool{
		"idempotencyKey": true, "workspaceGeneration": true,
		"projectId": true, "coverageProfileId": true,
		"catalogRevision": true, "selection": true,
		"repeatCount": true, "timeoutMs": true,
	}
	assertClosedJSON(t, decoded, allowed, true)
	root := decoded.(map[string]any)
	if root["timeoutMs"] != float64(5000) {
		t.Fatalf("timeoutMs = %#v, want 5000", root["timeoutMs"])
	}
}

func TestRequestCanonicalJSONAndCoverageRunIDGoldens(t *testing.T) {
	const wantCanonical = `{"idempotencyKey":"11111111111111111111111111111111","workspaceGeneration":"2222222222222222222222222222222222222222222222222222222222222222","projectId":"core","coverageProfileId":"coverage.default","catalogRevision":"3333333333333333333333333333333333333333333333333333333333333333","selection":{"mode":"items","itemIds":["utid-v1-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","utid-v1-bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"]},"repeatCount":2,"timeoutMs":5000}`
	const wantID = "c0f78bf62c0f096a8c753219dc26072d"

	raw, err := validRequest().CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	if got := string(raw); got != wantCanonical {
		t.Fatalf("CanonicalJSON() = %q, want exact golden %q", got, wantCanonical)
	}
	id, err := CoverageRunID(validRequest())
	if err != nil {
		t.Fatal(err)
	}
	if id != wantID {
		t.Fatalf("CoverageRunID() = %q, want fixed digest %q", id, wantID)
	}
}

func TestCoverageRunIDIsStableForSetOrderAndSensitiveToInputs(t *testing.T) {
	base := validRequest()
	baseID, err := CoverageRunID(base)
	if err != nil {
		t.Fatal(err)
	}
	reordered := base
	reordered.Selection.ItemIDs = []testdomain.ID{firstStableID, secondStableID}
	got, err := CoverageRunID(reordered)
	if err != nil || got != baseID {
		t.Fatalf("CoverageRunID(reordered) = %q, %v; want %q", got, err, baseID)
	}
	for name, mutate := range map[string]func(*Request){
		"idempotency key": func(v *Request) { v.IdempotencyKey = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" },
		"workspace generation": func(v *Request) {
			v.WorkspaceGeneration = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
		},
		"project": func(v *Request) { v.ProjectID = "other" },
		"profile": func(v *Request) { v.CoverageProfileID = "other" },
		"catalog": func(v *Request) {
			v.CatalogRevision = "4444444444444444444444444444444444444444444444444444444444444444"
		},
		"selection": func(v *Request) { v.Selection.ItemIDs = []testdomain.ID{firstStableID} },
		"repeat":    func(v *Request) { v.RepeatCount++ },
		"timeout":   func(v *Request) { v.Timeout += time.Millisecond },
	} {
		t.Run(name, func(t *testing.T) {
			value := base
			mutate(&value)
			got, err := CoverageRunID(value)
			if err != nil || got == baseID {
				t.Fatalf("CoverageRunID() = %q, %v; want ID distinct from %q", got, err, baseID)
			}
		})
	}
	invalid := base
	invalid.Timeout = 0
	if _, err := invalid.CanonicalJSON(); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("raw CanonicalJSON() error = %v, want ErrInvalidRequest", err)
	}
	if _, err := CoverageRunID(invalid); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("CoverageRunID() error = %v, want ErrInvalidRequest", err)
	}
}

func assertClosedJSON(t *testing.T, value any, allowed map[string]bool, top bool) {
	t.Helper()
	forbidden := map[string]bool{"executable": true, "command": true, "args": true, "argv": true, "env": true, "environment": true, "cwd": true, "path": true, "driver": true, "collector": true, "reportFormat": true, "hook": true}
	switch item := value.(type) {
	case map[string]any:
		for key, nested := range item {
			if forbidden[key] || top && !allowed[key] {
				t.Fatalf("unexpected JSON key %q", key)
			}
			assertClosedJSON(t, nested, allowed, false)
		}
	case []any:
		for _, nested := range item {
			assertClosedJSON(t, nested, allowed, false)
		}
	}
}

func requestStableIDs(count int) []testdomain.ID {
	result := make([]testdomain.ID, count)
	for index := range result {
		result[index] = testdomain.ID(fmt.Sprintf("utid-v1-%064x", index))
	}
	return result
}
