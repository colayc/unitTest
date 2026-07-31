package testdomain

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestNewCatalogValidatesAndDefensivelyCopies(t *testing.T) {
	input := validCatalogInput(t)
	catalog, err := NewCatalog(input)
	if err != nil {
		t.Fatal(err)
	}

	input.Containers[0].Labels[0] = "mutated"
	input.Items[0].Labels[0] = "mutated"
	input.Items[0].Parameters[0].Value = "mutated"
	if catalog.Containers[0].Labels[0] != "fast" ||
		catalog.Items[0].Labels[0] != "fast" ||
		catalog.Items[0].Parameters[0].Value != "debug" {
		t.Fatal("NewCatalog retained caller-owned slices")
	}

	cloned := catalog.Clone()
	cloned.Containers[0].Labels[0] = "clone-mutated"
	cloned.Items[0].Parameters[0].Value = "clone-mutated"
	if catalog.Containers[0].Labels[0] != "fast" ||
		catalog.Items[0].Parameters[0].Value != "debug" {
		t.Fatal("Catalog.Clone did not return a defensive copy")
	}
}

func TestNewCatalogRejectsDuplicateIdentity(t *testing.T) {
	input := validCatalogInput(t)
	input.Items = append(input.Items, input.Items[0])
	_, err := NewCatalog(input)
	if !errors.Is(err, ErrDuplicateIdentity) {
		t.Fatalf("duplicate identity error = %v", err)
	}

	input = validCatalogInput(t)
	duplicate := input.Items[0]
	duplicate.ID = mustContainerID(t, "core", "different-logical-id")
	input.Items = append(input.Items, duplicate)
	if _, err := NewCatalog(input); !errors.Is(err, ErrDuplicateIdentity) {
		t.Fatalf("duplicate logical identity error = %v", err)
	}
}

func TestNewCatalogRejectsBrokenTreeAndPartialSnapshots(t *testing.T) {
	t.Run("container id mismatch", func(t *testing.T) {
		input := validCatalogInput(t)
		input.Containers[0].ID = mustContainerID(t, "core", "other.tests")
		if _, err := NewCatalog(input); !errors.Is(err, ErrInvalidCatalog) {
			t.Fatalf("container ID mismatch error = %v", err)
		}
	})
	t.Run("unknown container", func(t *testing.T) {
		input := validCatalogInput(t)
		input.Items[0].ContainerID = ID("utid-v1-" + strings.Repeat("f", 64))
		if _, err := NewCatalog(input); !errors.Is(err, ErrInvalidCatalog) {
			t.Fatalf("unknown container error = %v", err)
		}
	})
	t.Run("parent cycle", func(t *testing.T) {
		input := validCatalogInput(t)
		input.Items[0].ParentID = input.Items[0].ID
		if _, err := NewCatalog(input); !errors.Is(err, ErrInvalidCatalog) {
			t.Fatalf("parent cycle error = %v", err)
		}
	})
	t.Run("partial", func(t *testing.T) {
		input := validCatalogInput(t)
		input.Partial = true
		if _, err := NewCatalog(input); !errors.Is(err, ErrInvalidCatalog) {
			t.Fatalf("partial catalog error = %v", err)
		}
	})
	t.Run("diagnostic enum", func(t *testing.T) {
		input := validCatalogInput(t)
		input.Diagnostics[0].Severity = "fatal"
		if _, err := NewCatalog(input); !errors.Is(err, ErrInvalidCatalog) {
			t.Fatalf("invalid diagnostic error = %v", err)
		}
	})
	t.Run("source provenance", func(t *testing.T) {
		input := validCatalogInput(t)
		input.Items[0].SourceLocation = &SourceLocation{
			URI: "file:///workspace/test.cpp", Navigable: true, Provenance: "guessed",
		}
		if _, err := NewCatalog(input); !errors.Is(err, ErrInvalidCatalog) {
			t.Fatalf("invalid source provenance error = %v", err)
		}
	})
}

func validCatalogInput(t *testing.T) Catalog {
	t.Helper()
	containerID := mustContainerID(t, "core", "core.tests")
	caseID, err := CaseID(CaseIdentity{
		ProjectID: "core",
		CTestName: "core.tests",
		Framework: FrameworkCppUTest,
		Group:     "Math",
		Name:      "adds",
		Parameters: []Parameter{{
			Name:  "configuration",
			Value: "debug",
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return Catalog{
		ProjectID:   "core",
		ProfileID:   strings.Repeat("1", 64),
		Revision:    strings.Repeat("2", 64),
		GeneratedAt: time.Date(2026, 7, 31, 0, 0, 0, 0, time.UTC),
		Containers: []Container{{
			ID:               containerID,
			ProjectID:        "core",
			CTestLogicalName: "core.tests",
			DisplayName:      "Core Tests",
			Framework:        FrameworkCppUTest,
			Capabilities: Capabilities{
				CanDiscoverCases: true,
				CanRunCase:       true,
			},
			Labels: []string{"fast"},
		}},
		Items: []Item{{
			ID:          caseID,
			ContainerID: containerID,
			Kind:        ItemCase,
			Framework:   FrameworkCppUTest,
			LogicalName: "adds",
			DisplayName: "adds",
			Labels:      []string{"fast"},
			Parameters: []Parameter{{
				Name:  "configuration",
				Value: "debug",
			}},
		}},
		Diagnostics: []Diagnostic{{
			Severity: "info",
			Category: "configuration_error",
			Code:     "CATALOG_READY",
			Message:  "ready",
		}},
	}
}
