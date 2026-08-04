package coveragedomain

import (
	"encoding/json"
	"errors"
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
		"repeat below minimum":      func(v *Request) { v.RepeatCount = 0 },
		"repeat above maximum":      func(v *Request) { v.RepeatCount = 101 },
		"timeout below millisecond": func(v *Request) { v.Timeout = time.Millisecond - time.Nanosecond },
		"timeout zero":              func(v *Request) { v.Timeout = 0 },
		"timeout above maximum":     func(v *Request) { v.Timeout = 24*time.Hour + time.Millisecond },
		"uppercase id":              func(v *Request) { v.IdempotencyKey = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA" },
		"short id":                  func(v *Request) { v.IdempotencyKey = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" },
		"invalid project":           func(v *Request) { v.ProjectID = "-bad" },
		"invalid coverage profile":  func(v *Request) { v.CoverageProfileID = "bad profile" },
		"empty selection":           func(v *Request) { v.Selection.ItemIDs = nil },
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
